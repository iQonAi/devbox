package main

import (
	"fmt"
	"os"
)

func main() {
	// os.Args[0] is the porgram name; os.args[1] is the subcommand (if any).
	if len(os.Args) < 2 {
		usage()
		os.Exit(2)
	}

	switch os.Args[1] {
	case "serve":
		fmt.Println("serve: not implemented yet")
	case "repos":
		fmt.Println("repos: not implemented yet")
	case "ls":
		fmt.Println("ls: not implemented yet")
	default:
		fmt.Fprintf(os.Stderr, "unknonw command %q\n\n", os.Args[1])
		usage()
		os.Exit(2)
	}
}

func usage() {
	fmt.Fprint(os.Stderr, `agent-task - devbox orchestrator

usage:
	agent-task serve	start the daemon (Unix socket)
	agent-task repos	list registered repositories
	agent-task ls		list tasks
`)
}
