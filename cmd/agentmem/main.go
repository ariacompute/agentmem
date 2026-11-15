package main

import (
	"fmt"
	"io"
	"os"

	"agentmem/internal/cli"
)

// exit is os.Exit in production. Tests replace it to observe the process exit
// code without terminating the test binary.
var exit = os.Exit

func main() { exit(runCLI(os.Args[1:], os.Stdout, os.Stderr)) }

// runCLI maps one CLI invocation to a process exit code.
func runCLI(args []string, out, errOut io.Writer) int {
	if err := run(args, out); err != nil {
		fmt.Fprintln(errOut, err)
		return 1
	}
	return 0
}

func run(args []string, out io.Writer) error {
	return cli.Run(args, out)
}
