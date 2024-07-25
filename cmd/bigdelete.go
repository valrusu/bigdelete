package main

// temp table used is hardcoded to "bigdeletetemp" because it can be any table with
// a single column ( rid rowid ); create it in a non-sys schema with any name, then create
// a synonym bigdeletetemp to it

import (
	"flag"
	"log"
	"os"
	// "github.com/godror/godror"
	_ "github.com/godror/godror"
	// github.com/valrusu/bigdelete/pkg
	"github.com/valrusu/bigdelete"
)

func main() {

	flag.StringVar(&bigdelete.ConnStr, "connect", "", "")
	flag.StringVar(&bigdelete.TableName, "table", "", "")
	flag.IntVar(&bigdelete.Numthreads, "threads", 20, "")
	flag.IntVar(&bigdelete.Rowidspercall, "commit", 1237, "")
	flag.StringVar(&bigdelete.Tnsadmin, "tnsadmin", "", "")
	flag.BoolVar(&bigdelete.DebugFlag, "debug", false, "")
	flag.Parse()

	// TODO verify program parameters: tableName threadCount recsPerCall

	perr := bigdelete.BigDelete()
	if perr.Err != nil {
		log.Println(perr.Msg)
		os.Exit(perr.Code)
	}

}
