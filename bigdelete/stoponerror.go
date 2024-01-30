package bigdelete

import (
	"log"
	"os"
)

func stopOnError(err error, msg string, errcode int) {
	if err != nil {
		if msg != "" {
			log.Println(msg)
		}
		log.Println(err)
		os.Exit(errcode)
	}
}
