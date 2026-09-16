package osopen

import (
	"errors"
	"os/exec"
	"strings"
	"testing"
)

// The table this package exists for. Every branch is checked from whatever
// machine runs the tests, which is the only way the Windows and Linux forms
// get any coverage at all on a macOS laptop.
func TestCommandFor(t *testing.T) {
	cases := []struct {
		goos   string
		target string
		want   []string
	}{
		{"darwin", "/tmp/a.yaml", []string{"open", "/tmp/a.yaml"}},
		{"darwin", "http://127.0.0.1:8080", []string{"open", "http://127.0.0.1:8080"}},
		{"windows", `C:\Users\sri\.sirdar\config.yaml`, []string{"rundll32", "url.dll,FileProtocolHandler", `C:\Users\sri\.sirdar\config.yaml`}},
		{"windows", "http://127.0.0.1:8080", []string{"rundll32", "url.dll,FileProtocolHandler", "http://127.0.0.1:8080"}},
		{"linux", "/tmp/a.yaml", []string{"xdg-open", "/tmp/a.yaml"}},
		{"freebsd", "/tmp/a.yaml", []string{"xdg-open", "/tmp/a.yaml"}},
		{"openbsd", "/tmp/a.yaml", []string{"xdg-open", "/tmp/a.yaml"}},
	}
	for _, c := range cases {
		name, args := commandFor(c.goos, c.target)
		got := append([]string{name}, args...)
		if strings.Join(got, "\x00") != strings.Join(c.want, "\x00") {
			t.Errorf("commandFor(%q, %q) = %q, want %q", c.goos, c.target, got, c.want)
		}
	}
}

// A Windows path with a space and an ampersand in it stays one argv entry:
// nothing re-parses it as a command line, which is why the rundll32 form is
// preferred over `cmd /c start`.
func TestCommandForWindowsDoesNotSplitAwkwardPaths(t *testing.T) {
	path := `C:\Users\R & D\My Notes\rca.md`
	name, args := commandFor("windows", path)
	if name != "rundll32" {
		t.Fatalf("opener = %q, want rundll32", name)
	}
	if len(args) != 2 || args[1] != path {
		t.Fatalf("args = %q, want the path unaltered as one entry", args)
	}
}

func TestCommandForPlatformsWithoutADesktop(t *testing.T) {
	for _, goos := range []string{"android", "ios", "js", "wasip1", "plan9"} {
		if name, _ := commandFor(goos, "/tmp/a"); name != "" {
			t.Errorf("commandFor(%q) = %q, want no opener", goos, name)
		}
	}
}

func TestOpenStartsTheOpener(t *testing.T) {
	var got *exec.Cmd
	restore := start
	start = func(cmd *exec.Cmd) error { got = cmd; return nil }
	t.Cleanup(func() { start = restore })

	if err := Open("/tmp/whatever"); err != nil {
		t.Fatalf("Open: %v", err)
	}
	if got == nil {
		t.Fatal("Open did not start a command")
	}
	if len(got.Args) == 0 || got.Args[len(got.Args)-1] != "/tmp/whatever" {
		t.Fatalf("started %q, want the target as the last argument", got.Args)
	}
}

func TestOpenReportsTheOpenersFailure(t *testing.T) {
	restore := start
	start = func(*exec.Cmd) error { return errors.New("no handler") }
	t.Cleanup(func() { start = restore })

	if err := Open("/tmp/whatever"); err == nil {
		t.Fatal("Open returned nil for an opener that would not start")
	}
}
