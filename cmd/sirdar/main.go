package main

import (
	"fmt"
	"io"
	"os"
	"strings"
)

// version, commit, and date are stamped at release time with -ldflags
// "-X main.version=... -X main.commit=... -X main.date=...", so they must
// stay vars: a const is folded into the binary before the linker could ever
// replace it. Unstamped (a `go build` or `go run` outside goreleaser) they
// keep these defaults.
var (
	version = "dev"
	commit  = "unknown"
	date    = "unknown"
)

type command func(args []string, stdout, stderr io.Writer) int

var commands = map[string]command{}

func main() { os.Exit(run(os.Args[1:], os.Stdout, os.Stderr)) }

func run(args []string, stdout, stderr io.Writer) int {
	if len(args) == 0 || args[0] == "help" || args[0] == "-h" || args[0] == "--help" {
		usage(stderr)
		return 2
	}
	if args[0] == "version" {
		fmt.Fprintf(stdout, "sirdar %s (%s, %s)\n", displayVersion(version), commit, date)
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
  fix         implement an approved triage note's fix on a branch and open a PR
  eval        replay the golden bundles and score the notes they produce
  golden      manage the golden set (add, list)
  resume      continue a blocked or interrupted run
  runs        list runs and states
  register    print the register
  serve       serve the web UI and API on loopback
  version     print version`)
}

// displayVersion prefixes release versions with "v" and leaves dev builds as they are.
func displayVersion(v string) string {
	if v == "" || v == "dev" || strings.HasPrefix(v, "v") {
		return v
	}
	return "v" + v
}
