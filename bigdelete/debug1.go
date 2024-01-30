package bigdelete

import (
	"log"
	"os"
)

// stmt prepared at tx level, it works
func debug1(rid string) {
	log.Println("debug1", rid)

	// spew.Dump(stmtIns)

	tx, err := db.Begin()
	log.Println(2, err)

	stmtIns, err := tx.Prepare("insert into bigdeletetemp values (:1)")
	log.Println(1, err)

	stmtDel, err := tx.Prepare("delete from " + "x" + " where rowid in (select rid from bigdeletetemp)")
	log.Println(6, err)

	ret, err := stmtIns.Exec(rid)
	log.Println(3, err)

	n, err := ret.RowsAffected()
	log.Println(4, err)
	log.Println("inserted", n)

	ret, err = stmtDel.Exec()
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
