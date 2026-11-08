package main

import (
	"fmt"
	"os"

	"agentmem/internal/cli"
)

func main() {
	if err := run(os.Args[1:], os.Stdout); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}

func run(args []string, out *os.File) error {
	return cli.Run(args, out)
}
