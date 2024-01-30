package bigdelete

import (
	"log"
	"os"
	"time"
)

// does not work
func debug2(rid string) {
	log.Println("debug2", rid)

	// spew.Dump(stmtIns)

	stmtIns, err := db.Prepare("insert into bigdeletetemp values (:1)")
	log.Println(1, err)
	defer stmtIns.Close()

	stmtDel, err := db.Prepare("delete from " + "x" + " where rowid in (select rid from bigdeletetemp)")
	log.Println(6, err)
	defer stmtDel.Close()

	stmtCnt, err := db.Prepare("select count(*) from bigdeletetemp")
	log.Println(7, err)
	defer stmtCnt.Close()

	//

	tx, err := db.Begin()
	log.Println(2, err)

	//

	ret, err := (tx.Stmt(stmtIns)).Exec(rid)
	log.Println(3, err)

	n, err := ret.RowsAffected()
	log.Println(4, err)
	log.Println("inserted", n, "sleep")
	time.Sleep(30 * time.Second)

	//

	var cnt int
	err = (tx.Stmt(stmtCnt)).QueryRow().Scan(&cnt)
	log.Println(3, err)
	log.Println("counted", cnt, "sleep")
	time.Sleep(30 * time.Second)

	//

	ret, err = (tx.Stmt(stmtDel)).Exec()
	log.Println(3, err)

	n, err = ret.RowsAffected()
	log.Println(4, err)
	log.Println("deleted", n, "sleep")
	time.Sleep(30 * time.Second)

	err = tx.Commit()
	log.Println(5, err)

	db.Close()
	os.Exit(0)
}

/*
[etctrx@i395-host1-sit bigdelete]$ ./r
2023/11/28 09:54:18 hello debug mode
2023/11/28 09:54:18 connStr /@etctrxdb_dataarchive table x numthreads 1 rowidspercall 1
2023/11/28 09:54:18 connected
2023/11/28 09:54:18 get db info
godror WARNING: discrepancy between DBTIMEZONE ("+00:00"=0) and SYSTIMESTAMP ("-05:00"=-500) - set connection timezone, see https://github.com/godror/godror/blob/master/doc/timezone.md
2023/11/28 09:54:18 session_user DATAARCHIVE
2023/11/28 09:54:18 current_schema DATAARCHIVE
2023/11/28 09:54:18 current_user DATAARCHIVE
2023/11/28 09:54:18 db_name ETCTRXDB
2023/11/28 09:54:18 db_unique_name ETCTRXDBC
2023/11/28 09:54:18 host i395-host1-sit
2023/11/28 09:54:18 instance_name ETCTRXDB
2023/11/28 09:54:18 service_name ETCTRXDBC
2023/11/28 09:54:18 server_host i395-host1-sit
2023/11/28 09:54:18 sid 872
2023/11/28 09:54:18 terminal
2023/11/28 09:54:18 ====== ======
2023/11/28 09:54:18 debug1 AAQ4uzAAHAAAADEAAB
2023/11/28 09:54:18 1 <nil>
2023/11/28 09:54:18 6 <nil>
2023/11/28 09:54:18 7 <nil>
2023/11/28 09:54:18 2 <nil>
2023/11/28 09:54:18 3 <nil>
2023/11/28 09:54:18 4 <nil>
2023/11/28 09:54:18 inserted 1
2023/11/28 09:54:18 3 <nil>
2023/11/28 09:54:18 counted 1 0
2023/11/28 09:54:18 3 <nil>
2023/11/28 09:54:18 4 <nil>
2023/11/28 09:54:18 deleted 0
2023/11/28 09:54:18 5 <nil>

X
------------------
AAQ4uzAAHAAAADEAAB


no rows selected
*/
