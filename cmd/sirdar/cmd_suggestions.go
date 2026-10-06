package main

import (
	"errors"
	"fmt"
	"io"
	"strings"

	"github.com/srivathsanvenkateswaran/sirdar/internal/app"
)

func init() { commands["suggestions"] = cmdSuggestions }

const suggestionsUsage = `usage: sirdar suggestions [list]
       sirdar suggestions accept ID [--playbook NAME]
       sirdar suggestions dismiss ID`

// cmdSuggestions reviews the queue of playbook additions that Remember
// this and RCA runs propose. Nothing reaches a playbook until it is
// accepted here or in Settings.
func cmdSuggestions(args []string, stdout, stderr io.Writer) int {
	if len(args) == 0 {
		return cmdSuggestionsList(nil, stdout, stderr)
	}
	sub, rest := args[0], args[1:]
	switch sub {
	case "list":
		return cmdSuggestionsList(rest, stdout, stderr)
	case "accept":
		return cmdSuggestionsAccept(rest, stdout, stderr)
	case "dismiss":
		return cmdSuggestionsDismiss(rest, stdout, stderr)
	}
	fmt.Fprintf(stderr, "sirdar suggestions: unknown subcommand %q\n", sub)
	fmt.Fprintln(stderr, suggestionsUsage)
	return exitUsage
}

func cmdSuggestionsList(args []string, stdout, stderr io.Writer) int {
	fs := newFlagSet("suggestions list", stderr, "usage: sirdar suggestions [list]")
	if _, ok := parseFlags(fs, args, 0, 0, stderr); !ok {
		return exitUsage
	}
	svc, wsID, ok := openWorkspaceService(app.Options{}, stderr)
	if !ok {
		return exitUsage
	}
	list, err := svc.Suggestions(wsID)
	if err != nil {
		fmt.Fprintf(stderr, "sirdar suggestions: %v\n", err)
		return 1
	}
	if len(list) == 0 {
		fmt.Fprintln(stdout, "No pending suggestions.")
		return 0
	}
	// The columns are padded by hand rather than with a tabwriter: the
	// addition under each row has no columns, and would end the
	// tabwriter's alignment block at every suggestion.
	idWidth, pbWidth := len("ID"), len("PLAYBOOK")
	for _, s := range list {
		idWidth, pbWidth = max(idWidth, len(s.ID)), max(pbWidth, len(s.Playbook))
	}
	fmt.Fprintf(stdout, "%-*s  %-*s  %s\n", idWidth, "ID", pbWidth, "PLAYBOOK", "SOURCE")
	for _, s := range list {
		fmt.Fprintf(stdout, "%-*s  %-*s  %s\n", idWidth, s.ID, pbWidth, s.Playbook, s.Source)
		for _, line := range strings.Split(strings.TrimRight(s.Addition, "\n"), "\n") {
			fmt.Fprintf(stdout, "    %s\n", line)
		}
	}
	return 0
}

func cmdSuggestionsAccept(args []string, stdout, stderr io.Writer) int {
	fs := newFlagSet("suggestions accept", stderr, "usage: sirdar suggestions accept ID [--playbook NAME]")
	playbook := fs.String("playbook", "", "file it into this playbook instead of the one proposed")
	positional, ok := parseFlags(fs, args, 1, 1, stderr)
	if !ok {
		return exitUsage
	}
	svc, wsID, ok := openWorkspaceService(app.Options{}, stderr)
	if !ok {
		return exitUsage
	}
	got, err := svc.AcceptSuggestion(wsID, positional[0], app.AcceptSuggestionInput{Playbook: *playbook})
	if err != nil {
		fmt.Fprintf(stderr, "sirdar suggestions accept: %v\n", err)
		// The one refusal the operator can fix from here: a playbook the
		// workspace does not have, under a name a new one may not take.
		if errors.Is(err, app.ErrBadInput) {
			fmt.Fprintln(stderr, "sirdar suggestions accept: pass --playbook with an existing playbook, or a new name like 20-logs.md")
		}
		return 1
	}
	fmt.Fprintf(stdout, "Accepted %s into %s\n", got.ID, got.Playbook)
	return 0
}

func cmdSuggestionsDismiss(args []string, stdout, stderr io.Writer) int {
	fs := newFlagSet("suggestions dismiss", stderr, "usage: sirdar suggestions dismiss ID")
	positional, ok := parseFlags(fs, args, 1, 1, stderr)
	if !ok {
		return exitUsage
	}
	svc, wsID, ok := openWorkspaceService(app.Options{}, stderr)
	if !ok {
		return exitUsage
	}
	if err := svc.DismissSuggestion(wsID, positional[0]); err != nil {
		fmt.Fprintf(stderr, "sirdar suggestions dismiss: %v\n", err)
		return 1
	}
	fmt.Fprintf(stdout, "Dismissed %s\n", positional[0])
	return 0
}
