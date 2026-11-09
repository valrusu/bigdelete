package main

// temp table used is hardcoded to "bigdeletetemp" because it can be any table with
// a single column ( rid rowid ); create it in a non-sys schema with any name, then create
// a synonym bigdeletetemp to it

import (
	"flag"
	"fmt"
	"log"
	"os"
	"os/exec"

	_ "github.com/godror/godror"
	"github.com/valrusu/bigdelete"
)

func main() {
	var configFile string
	var parallel int
	var extractOnly bool
	var checkConfig bool
	var prerun, postrun string

	flag.StringVar(&bigdelete.ConnStr, "connect", "", "")
	flag.StringVar(&bigdelete.TableName, "table", "", "")
	flag.IntVar(&bigdelete.Numthreads, "threads", 20, "")
	flag.IntVar(&bigdelete.Rowidspercall, "commit", 1237, "")
	flag.IntVar(&bigdelete.Groupsize, "group", 1, "")
	flag.StringVar(&bigdelete.Tnsadmin, "tnsadmin", "", "")
	flag.StringVar(&configFile, "configfile", "bigdelete.json", "")
	flag.BoolVar(&checkConfig, "checkconfig", false, "only check the config file and exit")
	flag.IntVar(&parallel, "parallel", 0, "")
	flag.BoolVar(&extractOnly, "extractonly", false, "")
	flag.BoolVar(&bigdelete.DebugFlag, "debug", false, "")
	flag.StringVar(&prerun, "prerun", "", "script to run before the run; a failure aborts the run")
	flag.StringVar(&postrun, "postrun", "", "script to run after the run; gets the run's exit code in BIGDELETE_EXITCODE")
	exampleConfig := flag.Bool("exampleconfig", false, "print an example config file and exit")
	flag.Parse()

	if *exampleConfig {
		fmt.Print(bigdelete.ExampleConfig)
		return
	}

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

	runScripts := !checkConfig

	if runScripts && prerun != "" {
		if err := runScript(prerun, nil); err != nil {
			log.Println("prerun script", prerun, "failed:", err)
			os.Exit(26)
		}
	}

	var perr bigdelete.Progerr
	if tableSet {
		perr = bigdelete.BigDelete()
	} else {
		perr = bigdelete.RunConfigFile(configFile, checkConfig, parallel, extractOnly)
	}

	if perr.Err != nil {
		log.Println(perr.Msg)
	}

	if runScripts && postrun != "" {
		env := []string{fmt.Sprintf("BIGDELETE_EXITCODE=%d", perr.Code)}
		if err := runScript(postrun, env); err != nil {
			log.Println("postrun script", postrun, "failed:", err)
			if perr.Err == nil {
				os.Exit(27)
			}
		}
	}

	if perr.Err != nil {
		os.Exit(perr.Code)
	}
}

// runScript runs a script with stdout/stderr passed through; stdin is not
// passed because in -table mode it carries the ROWIDs.
func runScript(script string, extraEnv []string) error {
	log.Println("running script", script)
	cmd := exec.Command(script)
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	cmd.Env = append(os.Environ(), extraEnv...)
	if err := cmd.Run(); err != nil {
		return err
	}
	log.Println("script", script, "completed")
	return nil
}
