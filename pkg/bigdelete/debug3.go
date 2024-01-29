package main

import (
	"log"
	"os"
	"time"
)

func debug3(rid string) {
	log.Println("debug3", rid)

	// spew.Dump(stmtIns)

	stmtIns, err := db.Prepare("insert into bigdeletetemp values (:1)")
	log.Println(1, err)
	defer stmtIns.Close()

	//

	tx, err := db.Begin()
	log.Println(2, err)

	//

	ret, err := tx.Stmt(stmtIns).Exec(rid)
	log.Println(3, err)

	n, err := ret.RowsAffected()
	log.Println(4, err)
	log.Println("inserted", n, "sleep")
	time.Sleep(30 * time.Second)

	//

	var cnt int
	err = tx.QueryRow("select count(*) from bigdeletetemp").Scan(&cnt)
	log.Println(3, err)
	log.Println("counted", cnt, "sleep")
	time.Sleep(30 * time.Second)

	//

	ret, err = tx.Exec("delete from " + "x" + " where rowid in (select rid from bigdeletetemp)")
	log.Println(3, err)

	n, err = ret.RowsAffected()
	log.Println(4, err)
	log.Println("deleted", n)

	// time.Sleep(30 * time.Second)

	err = tx.Commit()
	log.Println(5, err)

	db.Close()
	os.Exit(0)
}
