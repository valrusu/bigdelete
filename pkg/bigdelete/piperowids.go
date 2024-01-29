package main

import (
	"bufio"
)

// piperowids reads ROWIDs from stdin and pushes them in the ch channel for fanning out
func piperowids(in *bufio.Scanner, ch chan string) {
	cnt := 0
	for in.Scan() {
		ch <- in.Text()
		cnt++
	}
	close(ch)
	stopOnError(in.Err(), "", 4)
}
