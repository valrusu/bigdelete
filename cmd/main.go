package main

// temp table used is hardcoded to "bigdeletetemp" because it can be any table with
// a single column ( rid rowid ); create it in a non-sys schema with any name, then create
// a synonym bigdeletetemp to it

import (
	"flag"
	"log"

	// github.com/valrusu/bigdelete/pkg
	"github.com/valrusu/bigdelete"
)

var (
	connStr, tableName        string
	numthreads, rowidspercall int
	debugFlag                 bool
	err                       error
)

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

	bigdelete.ConnStr = connStr
	bigdelete.TableName = tableName
	bigdelete.Numthreads = numthreads
	bigdelete.Rowidspercall = rowidspercall
	bigdelete.DebugFlag = debugFlag
	bigdelete.BigDelete()

	// TODO verify program parameters: tableName threadCount recsPerCall

}
