package main

// temp table used is hardcoded to "bigdeletetemp" because it can be any table with
// a single column ( rid rowid ); create it in a non-sys schema with any name, then create
// a synonym bigdeletetemp to it

import (
	"flag"
	_ "github.com/godror/godror"
	"github.com/valrusu/bigdelete"
	"log"
	"os"
)

func main() {
	var configFile string
	var parallel int
	var extractOnly bool

	flag.StringVar(&bigdelete.ConnStr, "connect", "", "")
	flag.StringVar(&bigdelete.TableName, "table", "", "")
	flag.IntVar(&bigdelete.Numthreads, "threads", 20, "")
	flag.IntVar(&bigdelete.Rowidspercall, "commit", 1237, "")
	flag.IntVar(&bigdelete.Groupsize, "group", 1, "")
	flag.StringVar(&bigdelete.Tnsadmin, "tnsadmin", "", "")
	flag.StringVar(&configFile, "configfile", "bigdelete.json", "")
	flag.IntVar(&parallel, "parallel", 0, "")
	flag.BoolVar(&extractOnly, "extractonly", false, "")
	flag.BoolVar(&bigdelete.DebugFlag, "debug", false, "")
	flag.Parse()

	var tableSet, configSet bool
	flag.Visit(func(f *flag.Flag) {
		switch f.Name {
		case "table":
			tableSet = true
		case "configfile":
			configSet = true
		}
	})

	if tableSet && configSet {
		log.Println("-table and -configfile are mutually exclusive")
		os.Exit(20)
	}

	var perr bigdelete.Progerr
	if tableSet {
		perr = bigdelete.BigDelete()
	} else {
		perr = bigdelete.RunConfigFile(configFile, parallel, extractOnly)
	}

	if perr.Err != nil {
		log.Println(perr.Msg)
		os.Exit(perr.Code)
	}
}
