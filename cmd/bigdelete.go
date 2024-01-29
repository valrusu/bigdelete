package main

// temp table used is hardcoded to "bigdeletetemp" because it can be any table with
// a single column ( rid rowid ); create it in a non-sys schema with any name, then create
// a synonym bigdeletetemp to it

import (
	"bufio"
	"flag"
	"log"
	"time"

	"database/sql"

	_ "github.com/godror/godror"

	// github.com/valrusu/bigdelete/pkg
	github.com/valrusu/bigdelete/pkg

	//	"log"
	"os"
	"sync"
)

var (
	connStr, tableName string
	numthreads         int
	rowidspercall      int
	debugFlag          bool
	db                 *sql.DB
	err                error
)

type count struct {
	deleter     int
	counttarget int
	countdelete int
}

func main() {
	// TODO accept parameter for conn (user/pwd or @/tnsentry or -config and read from config file)

	flag.StringVar(&connStr, "connect", "", "")
	flag.StringVar(&tableName, "table", "", "")
	flag.IntVar(&numthreads, "threads", 20, "")
	flag.IntVar(&rowidspercall, "commit", 1237, "")
	flag.BoolVar(&debugFlag, "debug", false, "")
	flag.Parse()

	if debugFlag {
		log.Println("hello debug mode")
		log.Println("connStr", connStr, "table", tableName, "numthreads", numthreads, "rowidspercall", rowidspercall)
	}

	db, err = sql.Open("godror", connStr)
	stopOnError(err, "cannot connect to database", 1)
	defer db.Close()
	if debugFlag {
		log.Println("connected, sleep")
		time.Sleep(5 * time.Second)
	}

	err = db.Ping()
	stopOnError(err, "cannot ping db", 1)
	if debugFlag {
		log.Println("sleep after ping")
		time.Sleep(20 * time.Second)
	}

	if debugFlag {
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

	// TODO verify program parameters: tableName threadCount recsPerCall

	// chdata passes the ROWDIDs from stdin for deletion
	chdata := make(chan string)
	// chcnt receives count of deleted ROWIDs from consumers, keeps a total, and writes it to stdout at the end
	chcnt := make(chan count)

	go piperowids(bufio.NewScanner(os.Stdin), chdata)

	var wgdata, wgcnt sync.WaitGroup
	//	log.Println("read rowid", <-out)
	//	log.Println("read rowid", <-out)
	//	log.Println("read rowid", <-out)

	wgcnt.Add(1)
	go countreader(chcnt, &wgcnt)

	for i := 0; i < numthreads; i++ {
		wgdata.Add(1)
		go deletedata(i, chdata, chcnt, db, &wgdata)
	}

	wgdata.Wait()
	close(chcnt)
	wgcnt.Wait()
}
