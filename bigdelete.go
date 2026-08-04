package bigdelete

import (
	"bufio"
	"database/sql"
	"fmt"
	"io"
	"log"
	"os"
	"sync"
	"time"
)

var (
	ConnStr, TableName, Tnsadmin string
	Numthreads                   int
	Rowidspercall                int
	Groupsize                    int
	DebugFlag                    bool
)

type count struct {
	deleter     int
	counttarget int
	countdelete int
	timetaken   time.Duration
}

type Progerr struct {
	Err  error
	Msg  string
	Code int
}

func connectDB() (*sql.DB, Progerr) {
	var err error
	db, err := sql.Open("godror", ConnStr)
	if err != nil {
		return nil, Progerr{err, "cannot connect to database", 1}
	}

	if DebugFlag {
		log.Println("connected [ 5s ]")
		time.Sleep(5 * time.Second)
	}

	err = db.Ping()
	if err != nil {
		db.Close()
		return nil, Progerr{err, "cannot ping db", 2}
	}

	if DebugFlag {
		log.Println("sleep after ping [ 20s ]")
		time.Sleep(20 * time.Second)
	}

	if DebugFlag {
		log.Println("get db info")
		input := `select 'session_user'  ,sys_context('userenv','session_user'  ) from dual union all
select 'current_schema', sys_context('userenv','current_schema') from dual union all
select 'current_user'  , sys_context('userenv','current_user'  ) from dual union all
select 'db_name'       , sys_context('userenv','db_name'       ) from dual union all
select 'db_unique_name', sys_context('userenv','db_unique_name') from dual union all
select 'host'          , sys_context('userenv','host'          ) from dual union all
select 'instance_name' , sys_context('userenv','instance_name' ) from dual union all
select 'service_name'  , sys_context('userenv','service_name'  ) from dual union all
select 'server_host'   , sys_context('userenv','server_host'   ) from dual union all
select 'sid'           , sys_context('userenv','sid'           ) from dual union all
select 'terminal'      , sys_context('userenv','terminal'      ) from dual union all
select '======','======' from dual`
		rows, err := db.Query(input)
		if err != nil {
			db.Close()
			return nil, Progerr{err, "get db info failed", 3}
		}

		var s1, s2 string
		for rows.Next() {
			rows.Scan(&s1, &s2)
			log.Println(s1 + " " + s2)
		}
		rows.Close()
	}

	return db, Progerr{nil, "", 0}
}

func BigDelete() Progerr {
	db, perr := connectDB()
	if perr.Err != nil {
		return perr
	}
	defer db.Close()
	return runDelete(db, TableName, Numthreads, Rowidspercall, Groupsize, os.Stdin)
}

func runDelete(db *sql.DB, tableName string, numthreads, rowidspercall, groupsize int, in io.Reader) Progerr {
	if groupsize < 1 {
		groupsize = 1
	}

	// chdata passes the ROWDIDs from stdin for deletion, in groups of
	// groupsize sequential ROWIDs so rows from the same block stay in one session
	chdata := make(chan []string)
	// chcnt receives count of deleted ROWIDs from consumers, keeps a total, and writes it to stdout at the end
	chcnt := make(chan count)

	var wgdata, wgcnt, wgpiper sync.WaitGroup

	wgpiper.Add(1)
	go piperowids(bufio.NewScanner(in), chdata, groupsize, &wgpiper)

	wgcnt.Add(1)
	go countreader(tableName, chcnt, &wgcnt)

	wgdata.Add(numthreads)

	for i := 0; i < numthreads; i++ {
		go deletedata(i, tableName, rowidspercall, chdata, chcnt, db, &wgdata)
	}

	// wait for all delete threads
	// when they finish reading from the dataCh they stop
	wgdata.Wait()

	// nothing will write to the countCh anymore
	// close the channel and wait for the goroutine to finish
	close(chcnt)
	wgcnt.Wait()

	// this should be done already at this point since all the data delete threads are done
	wgpiper.Wait()

	return Progerr{nil, "", 0}
}

// func stopOnError(err error, msg string, errcode int) {
// 	if err != nil {
// 		if msg != "" {
// 			log.Println(msg)
// 		}
// 		log.Println(err)
// 		os.Exit(errcode)
// 	}
// }

// piperowids reads ROWIDs from a scanner and pushes them in the ch channel for fanning out,
// grouped in batches of groupsize sequential ROWIDs
func piperowids(in *bufio.Scanner, ch chan []string, groupsize int, wg *sync.WaitGroup) {
	batch := make([]string, 0, groupsize)
	for in.Scan() {
		batch = append(batch, in.Text())
		if len(batch) == groupsize {
			ch <- batch
			batch = make([]string, 0, groupsize)
		}
	}
	if len(batch) > 0 {
		ch <- batch
	}
	close(ch)
	wg.Done()
}

// deletedata reads batches of ROWIDs from the ch channel, inserts them in the temp table and then deletes them from the target table
func deletedata(threadnbr int, tableName string, rowidspercall int, ch chan []string, chok chan count, db *sql.DB, wg *sync.WaitGroup) {
	var (
		totrows int
		crtrows int
		tx      *sql.Tx
		t1, t2  time.Time
	)

	if DebugFlag {
		log.Println("thread", threadnbr, "get connection")
	}

	stmtIns, err := db.Prepare("insert into bigdeletetemp values (:rid)")
	if err != nil {
		log.Println("failed to prepare statement", err)
		os.Exit(7)
	}
	defer stmtIns.Close()

	if DebugFlag {
		log.Println("thread", threadnbr, "prepare delete stmt at conn level")
	}

	stmtDel, err := db.Prepare("delete from " + tableName + " where rowid in (select rid from bigdeletetemp)")
	if err != nil {
		log.Println("failed to prepare statement", err)
		os.Exit(6)
	}
	defer stmtDel.Close()

	var stmtInsTx, stmtDelTx *sql.Stmt

	tx, err = db.Begin()
	if err != nil {
		log.Println("failed to start transaction", err)
		os.Exit(8)
	}
	stmtInsTx = tx.Stmt(stmtIns)
	stmtDelTx = tx.Stmt(stmtDel)

	t1 = time.Now()

	for batch := range ch {
		for _, rid := range batch {

			if DebugFlag {
				log.Println("thread", threadnbr, "read rid", rid)
			}

			_, err := stmtInsTx.Exec(rid)
			if err != nil && DebugFlag {
				log.Println("insert received error", rid, err)
			}
			if err != nil {
				log.Println("failed to insert into temp table bigdeletetemp "+rid, err)
				os.Exit(8)
			}

			totrows++
			crtrows++
			if DebugFlag {
				log.Println("thread", threadnbr, "total", totrows, "crt", crtrows)
			}

			if crtrows == rowidspercall {
				if DebugFlag {
					log.Println("thread", threadnbr, "running delete")
				}

				stmtDelTx = tx.Stmt(stmtDel)

				ret, err := stmtDelTx.Exec()
				if err != nil {
					log.Println("failed to delete", err)
					os.Exit(10)
				}

				rowsdeleted, err := ret.RowsAffected()
				if err != nil {
					log.Println("failed to get number of rows affected", err)
					os.Exit(15)
				}
				if DebugFlag {
					log.Println("rows deleted", rowsdeleted)
				}

				if DebugFlag {
					log.Println("thread", threadnbr, "commit")
				}

				err = tx.Commit()
				if err != nil {
					log.Println("failed to commit", err)
					os.Exit(11)
				}

				t2 = time.Now()
				chok <- count{threadnbr, crtrows, int(rowsdeleted), t2.Sub(t1)}
				crtrows = 0
				t1 = t2

				tx, err = db.Begin()
				if err != nil {
					log.Println("failed to start transaction", err)
					os.Exit(12)
				}

				stmtInsTx = tx.Stmt(stmtIns)
				stmtDelTx = tx.Stmt(stmtDel)
			}
		}
	}

	if crtrows != 0 {
		if DebugFlag {
			log.Println("thread", threadnbr, "running final delete")
		}

		ret, err := stmtDelTx.Exec()
		if err != nil {
			log.Println("failed to delete", err)
			os.Exit(14)
		}

		rowsdeleted, err := ret.RowsAffected()
		if DebugFlag {
			log.Println("thread", threadnbr, "rows deleted", rowsdeleted)
		}

		err = tx.Commit()
		if err != nil {
			log.Println("failed to commit", err)
			os.Exit(14)
		}

		t2 = time.Now()
		chok <- count{threadnbr, crtrows, int(rowsdeleted), t2.Sub(t1)}
	}

	wg.Done()
}

func countreader(tableName string, ch chan count, wg *sync.WaitGroup) {
	var (
		totaltarget int
		totaldelete int
	)

	for del := range ch {
		totaltarget += del.counttarget
		totaldelete += del.countdelete
		log.Printf("%s %d %d [ %d ] %d %d %.2f", tableName, totaltarget, totaldelete, del.deleter, del.counttarget, del.countdelete, del.timetaken.Seconds())
	}

	fmt.Println(tableName, totaltarget, totaldelete)
	wg.Done()
}
