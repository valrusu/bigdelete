package bigdelete

import (
	"context"
	"log"
	"os"
	"time"
	// _ "github.com/godror/godror"
)

// does not work
func debug4(rid string) {
	log.Println("debug4 1", rid)

	// spew.Dump(stmtIns)

	//

	ctx := context.TODO()

	conn, err := db.Conn(ctx)
	defer conn.Close()

	stmtIns, err := conn.PrepareContext(ctx, "insert into bigdeletetemp values (:1)")
	log.Println(1, err)
	defer stmtIns.Close()

	stmtDel, err := conn.PrepareContext(ctx, "delete from "+"x"+" where rowid in (select rid from bigdeletetemp)")
	log.Println(6, err)
	defer stmtDel.Close()

	stmtCnt, err := conn.PrepareContext(ctx, "select count(*) from bigdeletetemp")
	log.Println(7, err)
	defer stmtCnt.Close()

	// tx, err := db.BeginTx(ctx, &sql.TxOptions{Isolation: sql.LevelReadCommitted})
	// tx, err := db.Begin()
	tx, err := conn.BeginTx(ctx, nil)
	log.Println(2, err)

	//

	ret, err := tx.StmtContext(ctx, stmtIns).ExecContext(ctx, rid)
	log.Println(3, err)

	n, err := ret.RowsAffected()
	log.Println(4, err)
	log.Println("inserted", n, "sleep")
	time.Sleep(20 * time.Second)

	//

	var cnt int
	err = tx.StmtContext(ctx, stmtCnt).QueryRowContext(ctx).Scan(&cnt)
	log.Println(3, err)
	defer tx.Rollback()
	log.Println("counted", cnt, "sleep")
	time.Sleep(20 * time.Second)

	//

	ret, err = tx.Stmt(stmtDel).ExecContext(ctx)
	log.Println(3, err)

	n, err = ret.RowsAffected()
	log.Println(4, err)
	log.Println("deleted", n, "sleep")
	time.Sleep(20 * time.Second)

	err = tx.Commit()
	log.Println(5, err)

	db.Close()
	os.Exit(0)
}
