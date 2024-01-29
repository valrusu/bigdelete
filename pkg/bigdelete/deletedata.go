package bigdelete

// temp table:
// create global temporary table romsrep.tempdelete (rid rowid) on delete delete rows;
// create or replace synonym sys.bigdeletetemp for romsrep.tempdelete;

import (
	"database/sql"
	"log"
	"sync"
)

// deletedata reads ROWIDs from the ch channel, builds 1000 of them in a delete statement and
// runs the statement on the DB
// TODO return the number of rows deleted; if failed push the ROWIDs back to the main program to report on stderr
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
	)

	if debugFlag {
		log.Println("thread", threadnbr, "get connection")
	}

	stmtIns, err := db.Prepare("insert into bigdeletetemp values (:rid)")
	stopOnError(err, "", 7)
	defer stmtIns.Close()

	if debugFlag {
		log.Println("thread", threadnbr, "prepare delete stmt at conn level")
	}

	stmtDel, err := db.Prepare("delete from " + tableName + " where rowid in (select rid from bigdeletetemp)")
	stopOnError(err, "", 6)
	defer stmtDel.Close()

	var stmtInsTx, stmtDelTx *sql.Stmt

	tx, err = db.Begin()
	stopOnError(err, "failed to start ransaction", 8)
	stmtInsTx = tx.Stmt(stmtIns)
	stmtDelTx = tx.Stmt(stmtDel)

	for rid := range ch {

		if debugFlag {
			log.Println("thread", threadnbr, "read rid", rid)
		}

		_, err := stmtInsTx.Exec(rid)
		if err != nil && debugFlag {
			log.Println("insert received error", rid, err)
		}
		stopOnError(err, "failed to insert into temp table bigdeletetemp "+rid, 9)

		totrows++
		crtrows++
		if debugFlag {
			log.Println("thread", threadnbr, "total", totrows, "crt", crtrows)
		}

		if crtrows == rowidspercall {
			if debugFlag {
				log.Println("thread", threadnbr, "running delete")
			}

			stmtDelTx = tx.Stmt(stmtDel)

			ret, err := stmtDelTx.Exec()
			stopOnError(err, "failed to delete", 10)

			rowsdeleted, err := ret.RowsAffected()
			stopOnError(err, "failed to get number of rows affected", 15)
			if debugFlag {
				log.Println("rows deleted", rowsdeleted)
			}

			if debugFlag {
				log.Println("thread", threadnbr, "commit")
			}
			// if debugFlag {
			// log.Println("thread", threadnbr, "before commit, sleep")
			// time.Sleep(5 * time.Second)
			// }

			err = tx.Commit()
			stopOnError(err, "failed to commit", 11)

			chok <- count{threadnbr, crtrows, int(rowsdeleted)}
			crtrows = 0

			tx, err = db.Begin()
			stopOnError(err, "failed to start transaction", 12)
			stmtInsTx = tx.Stmt(stmtIns)
			stmtDelTx = tx.Stmt(stmtDel)
		}
	}

	// read all the input rows and we have some left not committed
	if crtrows != 0 {
		if debugFlag {
			log.Println("thread", threadnbr, "running final delete")
		}

		ret, err := stmtDelTx.Exec()
		stopOnError(err, "failed to delete", 13)

		rowsdeleted, err := ret.RowsAffected()
		if debugFlag {
			log.Println("thread", threadnbr, "rows deleted", rowsdeleted)
		}

		chok <- count{threadnbr, crtrows, int(rowsdeleted)}

		err = tx.Commit()
		stopOnError(err, "failed to commit", 14)
	}

	// log.Println("consumer", threadnbr, "processed", totcnt, "rows")
	wg.Done()
}
