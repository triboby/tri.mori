package main

import (
	"encoding/json"
	"fmt"
	"io"
	"os"
	"strings"
)

func run(err error) {
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}

func main() {
	cmd := "serve"
	if len(os.Args) > 1 {
		cmd = os.Args[1]
	}
	switch cmd {
	case "serve":
		serve()
	case "gen":
		run(runGen(os.Args[2:]))
	case "gen-books":
		run(runGenBooks(os.Args[2:]))
	case "organize-books":
		run(runOrganize(os.Args[2:]))
	case "recategorize-books":
		run(runRecategorize(os.Args[2:]))
	case "lists":
		run(runLists(os.Args[2:]))
	case "seg":
		// seg [--search]: segment stdin line by line, one JSON array per line.
		search := len(os.Args) > 2 && os.Args[2] == "--search"
		in, _ := io.ReadAll(os.Stdin)
		for _, l := range strings.Split(string(in), "\n") {
			words := segment(l, search)
			if words == nil {
				words = []string{}
			}
			out, _ := json.Marshal(words)
			fmt.Println(string(out))
		}
	default:
		fmt.Fprintf(os.Stderr, "usage: shelf [serve | gen [--rescan] [--no-nas-posters] | gen-books [--rescan] [--offline] |\n  organize-books [--apply | --rollback <log>] | recategorize-books [--apply | --rollback <backup>] | seg [--search]]\n")
		os.Exit(2)
	}
}
