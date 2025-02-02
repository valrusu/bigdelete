package bigdelete

import (
	"bufio"
	"database/sql"
	"fmt"
	"log"
	"os"
	"sync"
	"time"
)

var (
	ConnStr, TableName, Tnsadmin string
	Numthreads                   int
	Rowidspercall                int
	DebugFlag                    bool
	db                           *sql.DB
	err                          error
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

func BigDelete() Progerr {
	db, err = sql.Open("godror", ConnStr)
	if err != nil {
		return Progerr{err, "cannot connect to database", 1}
	}
	defer db.Close()
	if DebugFlag {
		log.Println("connecteds [ 5s ]")
		time.Sleep(5 * time.Second)
	}

	err = db.Ping()
	if err != nil {
		return Progerr{err, "cannot ping db", 2}
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
			return Progerr{err, "get db info failed", 3}
		}

		var s1, s2 string
		for rows.Next() {
			rows.Scan(&s1, &s2)
			log.Println(s1 + " " + s2)
		}
		rows.Close()

	}

	// chdata passes the ROWDIDs from stdin for deletion
	chdata := make(chan string)
	// chcnt receives count of deleted ROWIDs from consumers, keeps a total, and writes it to stdout at the end
	chcnt := make(chan count)

	var wgdata, wgcnt, wgpiper sync.WaitGroup

	wgpiper.Add(1)
	go piperowids(bufio.NewScanner(os.Stdin), chdata, &wgpiper)

	wgcnt.Add(1)
	go countreader(chcnt, &wgcnt)

	wgdata.Add(Numthreads)

	for i := 0; i < Numthreads; i++ {
		// wgdata.Add(1)
		go deletedata(i, chdata, chcnt, db, &wgdata)
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

// piperowids reads ROWIDs from stdin and pushes them in the ch channel for fanning out
func piperowids(in *bufio.Scanner, ch chan string, wg *sync.WaitGroup) {
	for in.Scan() {
		ch <- in.Text()
	}
	close(ch)
	wg.Done()
}

// deletedata reads ROWIDs from the ch channel, inserts them in the temp table and then deletes them from the target table
func deletedata(threadnbr int, ch chan string, chok chan count, db *sql.DB, wg *sync.WaitGroup) {
	// read N
	// build a block
	// call DB
	// some report
	// push failures back in the channel ?!?
	//i := 0

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
		// TODO a better way to stop on errors
		log.Println("failed to prepare statement", err)
		os.Exit(7)
	}
	defer stmtIns.Close()

	if DebugFlag {
		log.Println("thread", threadnbr, "prepare delete stmt at conn level")
	}

	stmtDel, err := db.Prepare("delete from " + TableName + " where rowid in (select rid from bigdeletetemp)")
	if err != nil {
		// TODO a better way to stop on errors
		log.Println("failed to prepare statement", err)
		os.Exit(6)
	}
	defer stmtDel.Close()

	var stmtInsTx, stmtDelTx *sql.Stmt

	tx, err = db.Begin()
	if err != nil {
		// TODO a better way to stop on errors
		log.Println("failed to start transaction", err)
		os.Exit(8)
	}
	stmtInsTx = tx.Stmt(stmtIns)
	stmtDelTx = tx.Stmt(stmtDel)

	t1 = time.Now()

	for rid := range ch {

		if DebugFlag {
			log.Println("thread", threadnbr, "read rid", rid)
		}

		_, err := stmtInsTx.Exec(rid)
		if err != nil && DebugFlag {
			log.Println("insert received error", rid, err)
		}
		if err != nil {
			// TODO a better way to stop on errors
			log.Println("failed to insert into temp table bigdeletetemp "+rid, err)
			os.Exit(8)
		}

		totrows++
		crtrows++
		if DebugFlag {
			log.Println("thread", threadnbr, "total", totrows, "crt", crtrows)
		}

		if crtrows == Rowidspercall {
			if DebugFlag {
				log.Println("thread", threadnbr, "running delete")
			}

			stmtDelTx = tx.Stmt(stmtDel)

			ret, err := stmtDelTx.Exec()
			if err != nil {
				// TODO a better way to stop on errors
				log.Println("failed to delete", err)
				os.Exit(10)
			}

			rowsdeleted, err := ret.RowsAffected()
			if err != nil {
				// TODO a better way to stop on errors
				log.Println("failed to get number of rows affected", err)
				os.Exit(15)
			}
			if DebugFlag {
				log.Println("rows deleted", rowsdeleted)
			}

			if DebugFlag {
				log.Println("thread", threadnbr, "commit")
			}
			// if DebugFlag {
			// log.Println("thread", threadnbr, "before commit, sleep")
			// time.Sleep(5 * time.Second)
			// }

			err = tx.Commit()
			if err != nil {
				// TODO a better way to stop on errors
				log.Println("failed to commit", err)
				os.Exit(11)
			}

			t2 = time.Now()
			chok <- count{threadnbr, crtrows, int(rowsdeleted), t2.Sub(t1)}
			crtrows = 0
			t1 = t2

			tx, err = db.Begin()
			if err != nil {
				// TODO a better way to stop on errors
				log.Println("failed to start transaction", err)
				os.Exit(12)
			}

			stmtInsTx = tx.Stmt(stmtIns)
			stmtDelTx = tx.Stmt(stmtDel)
		}
	}

	// read all the input rows and we have some left not committed
	if crtrows != 0 {
		if DebugFlag {
			log.Println("thread", threadnbr, "running final delete")
		}

		ret, err := stmtDelTx.Exec()
		if err != nil {
			// TODO a better way to stop on errors
			log.Println("failed to delete", err)
			os.Exit(14)
		}

		rowsdeleted, err := ret.RowsAffected()
		if DebugFlag {
			log.Println("thread", threadnbr, "rows deleted", rowsdeleted)
		}

		err = tx.Commit()
		if err != nil {
			// TODO a better way to stop on errors
			log.Println("failed to commit", err)
			os.Exit(14)
		}

		t2 = time.Now()
		chok <- count{threadnbr, crtrows, int(rowsdeleted), t2.Sub(t1)}
	}

	// log.Println("consumer", threadnbr, "processed", totcnt, "rows")
	wg.Done()
}

func countreader(ch chan count, wg *sync.WaitGroup) {
	var (
		totaltarget int
		totaldelete int
		// interval     int = numthreads * rowidspercall
		// lastreported int
	)

	for del := range ch {
		totaltarget += del.counttarget
		totaldelete += del.countdelete
		//	if totaldeleted >= lastreported+interval {
		//log.Println("table", tableName, "thread", del.deleter, "deleted", totaldeleted)
		// log.Println(TableName, totaltarget, totaldelete, "[", del.deleter, "]", del.counttarget, del.countdelete, del.timetaken)
		log.Printf("%s %d %d [ %d ] %d %d %.2f", TableName, totaltarget, totaldelete, del.deleter, del.counttarget, del.countdelete, del.timetaken.Seconds())
		// lastreported = totaldeleted
		//	}
	}

	fmt.Println(TableName, totaltarget, totaldelete)
	wg.Done()
}
