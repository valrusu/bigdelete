package main

import (
	"fmt"
	"log"
	"sync"
)

// countreader receives deleted record counts from channel ch and displays them in log format on stderr
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
		log.Println(tableName, totaltarget, totaldelete, "[", del.deleter, "]", del.counttarget, del.countdelete)
		// lastreported = totaldeleted
		//	}
	}

	fmt.Println(tableName, totaltarget, totaldelete)
	wg.Done()
}
