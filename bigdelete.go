package bigdelete

import (
	"bufio"
	"database/sql"
	"log"
	"os"
	"sync"
	"time"
)

var (
	ConnStr, TableName string
	Numthreads         int
	Rowidspercall      int
	DebugFlag          bool
	db                 *sql.DB
	err                error
)

type count struct {
	deleter     int
	counttarget int
	countdelete int
}

func BigDelete() {
	db, err = sql.Open("godror", ConnStr)
	stopOnError(err, "cannot connect to database", 1)
	defer db.Close()
	if DebugFlag {
		log.Println("connected, sleep")
		time.Sleep(5 * time.Second)
	}

	err = db.Ping()
	stopOnError(err, "cannot ping db", 1)
	if DebugFlag {
		log.Println("sleep after ping")
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
		stopOnError(err, "get db info failed", 1)

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

	go piperowids(bufio.NewScanner(os.Stdin), chdata)

	var wgdata, wgcnt sync.WaitGroup
	//	log.Println("read rowid", <-out)
	//	log.Println("read rowid", <-out)
	//	log.Println("read rowid", <-out)

	// wgcnt.Add(1)
	wgcnt.Add(Numthreads)
	go countreader(chcnt, &wgcnt)

	for i := 0; i < Numthreads; i++ {
		// wgdata.Add(1)
		go deletedata(i, chdata, chcnt, db, &wgdata)
	}

	wgdata.Wait()
	close(chcnt)
	wgcnt.Wait()
}
