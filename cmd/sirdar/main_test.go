package main

import (
	"bytes"
	"strings"
	"testing"
)

func TestVersion(t *testing.T) {
	var out, errb bytes.Buffer
	if code := run([]string{"version"}, &out, &errb); code != 0 {
		t.Fatalf("exit %d, stderr %q", code, errb.String())
	}
	if !strings.HasPrefix(out.String(), "sirdar ") {
		t.Fatalf("unexpected output %q", out.String())
	}
}

func TestUnknownCommand(t *testing.T) {
	var out, errb bytes.Buffer
	if code := run([]string{"bogus"}, &out, &errb); code != 2 {
		t.Fatalf("want exit 2, got %d", code)
	}
	if !strings.Contains(errb.String(), "unknown command") {
		t.Fatalf("stderr %q", errb.String())
	}
}

// Bare `sirdar` serves the UI rather than printing usage; this pins the
// dispatch without binding a port by handing serve a flag it rejects.
func TestNoArgumentsIsServe(t *testing.T) {
	var out, errb bytes.Buffer
	if code := run([]string{"help"}, &out, &errb); code != 2 || !strings.Contains(errb.String(), "With no command, sirdar serves the web UI") {
		t.Fatalf("help: exit %d, stderr %q", code, errb.String())
	}
	if !strings.Contains(usageText(), "sirdar [<command> [flags]]") {
		t.Fatalf("usage does not say the command is optional")
	}
}

func TestUsageListsAskAndNote(t *testing.T) {
	text := usageText()
	if !strings.Contains(text, "ask         ask about anything, with or without a ticket; the answer comes back as chat") {
		t.Fatalf("usage does not list ask:\n%s", text)
	}
	if !strings.Contains(text, "note        file the note again for a triage or RCA run that has a reply") {
		t.Fatalf("usage does not list note:\n%s", text)
	}
}

func TestAskWithoutAnInstructionIsUsage(t *testing.T) {
	var out, errb bytes.Buffer
	if code := run([]string{"ask"}, &out, &errb); code != 2 {
		t.Fatalf("exit %d, stderr %q", code, errb.String())
	}
}

func TestNoteWithoutARunIDIsUsage(t *testing.T) {
	var out, errb bytes.Buffer
	if code := run([]string{"note"}, &out, &errb); code != 2 {
		t.Fatalf("exit %d, stderr %q", code, errb.String())
	}
}
