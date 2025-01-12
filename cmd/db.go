package cmd

import (
	"database/sql"
	_ "embed"
	"encoding/json"
	"fmt"
	"time"

	"github.com/celer-network/goutils/log"

	"github.com/go-gorp/gorp"
	_ "github.com/lib/pq"
	"github.com/spf13/viper"
)

type DB struct {
	*gorp.DbMap
}

var migration string

// DB models
type MonitorBlock struct {
	Event    string `db:"event"`
	BlockNum uint64 `db:"block_num"`
	BlockIdx int64  `db:"block_idx"`
	Restart  bool   `db:"restart"`
}

func NewMonitorBlock(event string, blockNum uint64, blockIdx int64) *MonitorBlock {
	return &MonitorBlock{
		Event:    event,
		BlockNum: blockNum,
		BlockIdx: blockIdx,
		Restart:  false,
	}
}

type Query struct {
	Hash          string `db:"hash"`
	ChainId       uint64 `db:"chain_id"`
	TargetChainId uint64 `db:"target_chain_id"`
	RequestData   string `db:"request_data"`
	Fee           string `db:"fee"`
	Status        uint64 `db:"status"`
	FulfillTx     string `db:"tx"`

	CreateTime time.Time `db:"create_time"`
	UpdateTime time.Time `db:"update_time"`
}

const (
	QueryStatus_QS_UNKNOWN = iota
	QueryStatus_QS_TO_BE_PAID
	QueryStatus_QS_PAID
	QueryStatus_QS_COMPLETE
	QueryStatus_QS_FAILED
)

func NewQuery(hash string, chainId, targetChainId uint64, requestData *RequestData, fee string) *Query {
	data, _ := json.Marshal(requestData)
	now := time.Now().UTC()
	return &Query{
		Hash:          hash,
		ChainId:       chainId,
		TargetChainId: targetChainId,
		RequestData:   string(data),
		Fee:           fee,
		Status:        QueryStatus_QS_TO_BE_PAID,
		CreateTime:    now,
		UpdateTime:    now,
	}
}

func NewDB() *DB {
	log.Infoln("init db")
	dbUrl := viper.GetString(kDb)

	if len(dbUrl) == 0 {
		dbUrl = "localhost:26257"
	}

	createDatabaseIfNotExists(dbUrl)

	dbUrl = fmt.Sprintf("postgresql://root@%s/brevis_gw?sslmode=disable", dbUrl)
	db, err := sql.Open("postgres", dbUrl)
	chkErr(err, "sql.Open failed")

	// construct a gorp DbMap
	dbmap := &gorp.DbMap{Db: db, Dialect: gorp.PostgresDialect{}}

	dbmap.AddTableWithName(MonitorBlock{}, "monitor_block").SetKeys(false, "event")
	queryTable := dbmap.AddTableWithName(Query{}, "query").SetKeys(false, "hash", "target_chain_id")
	queryTable.AddIndex("idx_status_createtime", "Hash", []string{"status", "create_time"})

	log.Infoln("sync tables")
	err = dbmap.CreateTablesIfNotExists()
	chkErr(err, "Create tables failed")

	log.Infoln("sync index")
	err0 := dbmap.CreateIndex()
	if err != nil {
		log.Fatalln("sync index failed", err0)
	}
	dal := &DB{dbmap}

	log.Infoln("applying sql migrations...")
	_, err = dbmap.Exec(migration)
	if err != nil {
		log.Warnf("failed to apply migration sql script: %s", err.Error())
	} else {
		log.Infoln("applied sql migrations")
	}

	return dal
}

func createDatabaseIfNotExists(dbUrl string) {
	log.Infoln("sync database")
	dbUrl = fmt.Sprintf("postgresql://root@%s?sslmode=disable", dbUrl)
	log.Infof("dialing db %s", dbUrl)
	db, err := sql.Open("postgres", dbUrl)
	chkErr(err, "sql.Open failed")
	_, err = db.Exec("create database if not exists brevis_gw;")
	chkErr(err, "create database failed")
}

func (dal *DB) Close() {
	if dal.Db != nil {
		err := dal.Db.Close()
		if err != nil {
			return
		}
		dal.Db = nil
	}
}

func (db *DB) SelectQuery(hash string, targetChainId uint64) (*Query, error) {
	query, err := db.Get(&Query{}, hash, targetChainId)
	if err != nil || query == nil {
		return nil, err
	}
	return query.(*Query), nil
}

func (db *DB) GetToBeFulfilledQueries(period time.Duration) ([]*Query, error) {
	q := "SELECT hash, target_chain_id FROM query WHERE status=$1 AND create_time > $2"
	rows, err := db.Query(q, QueryStatus_QS_PAID, time.Now().UTC().Add(period*-1))
	defer closeRows(rows)
	if err != nil {
		return nil, err
	}

	var queries []*Query
	var hash string
	var targetChainId uint64
	for rows.Next() {
		err = rows.Scan(&hash, &targetChainId)
		if err != nil {
			return nil, err
		}
		query := &Query{
			Hash:          hash,
			TargetChainId: targetChainId,
		}
		queries = append(queries, query)
	}
	return queries, nil
}

func closeRows(rows *sql.Rows) {
	if err := rows.Close(); err != nil {
		log.Warnln("closeRows: error:", err)
	}
}
