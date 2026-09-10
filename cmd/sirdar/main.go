package main

import (
	"fmt"
	"io"
	"os"
)

const version = "0.1.0-dev"

type command func(args []string, stdout, stderr io.Writer) int

var commands = map[string]command{}

func main() { os.Exit(run(os.Args[1:], os.Stdout, os.Stderr)) }

func run(args []string, stdout, stderr io.Writer) int {
	if len(args) == 0 || args[0] == "help" || args[0] == "-h" || args[0] == "--help" {
		usage(stderr)
		return 2
	}
	if args[0] == "version" {
		fmt.Fprintf(stdout, "sirdar %s\n", version)
		return 0
	}
	cmd, ok := commands[args[0]]
	if !ok {
		fmt.Fprintf(stderr, "sirdar: unknown command %q\n", args[0])
		usage(stderr)
		return 2
	}
	return cmd(args[1:], stdout, stderr)
}

func usage(w io.Writer) {
	fmt.Fprintln(w, `usage: sirdar <command> [flags]

commands:
  init        scaffold .sirdar/config.yaml and playbooks
  doctor      check CLIs, logins, adapters, notes directory
  triage      run triage for one or more ticket keys
  rca         produce the RCA note and Resolution draft
  resume      continue a blocked or interrupted run
  runs        list runs and states
  register    print the register
  version     print version`)
}
