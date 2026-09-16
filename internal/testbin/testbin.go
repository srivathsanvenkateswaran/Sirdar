// Package testbin stands a real executable up in place of an external
// program a test needs — the `claude` CLI, a stdio MCP server, a
// transcription command, a source adapter — on every operating system
// Sirdar runs on.
//
// It exists because the obvious fixture does not work everywhere. A
// `#!/bin/sh` script is not an executable on Windows: CreateProcess
// refuses it with "%1 is not a valid Win32 application", and exec.LookPath
// does not consider an extensionless file executable at all. Tests built
// on shell fixtures therefore failed on Windows for a reason that had
// nothing to do with what they were testing, and skipping them there left
// the MCP, serve and runs paths unexercised on the platform most likely to
// break them.
//
// What is executable on every operating system is the test binary that is
// already running. Install hardlinks it (copying when a link cannot be
// made) under the name the fake should have, and Dispatch — called first
// thing in TestMain — notices when the process was started under one of
// those names and runs that fake's Go function instead of any test. The
// fake is chosen by the name it was installed as rather than by an
// environment variable, because several of the things under test hand
// their child a cut-down environment.
//
// This package is only ever imported by tests.
package testbin

import (
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

// Ext is the extension an executable needs on this platform.
var Ext = map[bool]string{true: ".exe", false: ""}[runtime.GOOS == "windows"]

// nameSuffix marks the sidecar file that records which fake an installed
// copy should behave as, for the case where the copy has to carry some
// other name — a stand-in for `claude` has to be called "claude".
const nameSuffix = ".fakename"

// Dispatch runs the fake this process was started as, if it was started as
// one, and never returns in that case. Call it as the first statement of
// TestMain:
//
//	func TestMain(m *testing.M) {
//		testbin.Dispatch(map[string]func() int{"fakemcp": testbin.FakeMCP})
//		os.Exit(m.Run())
//	}
//
// A process started under its own build name — the ordinary `go test` run —
// matches nothing and falls through, so the tests run as usual.
func Dispatch(fakes map[string]func() int) {
	// os.Args[0] is the name the process was started under, which is the
	// one that matters; os.Executable is the fallback for a launcher that
	// leaves argv[0] bare. Both are consulted rather than one, because
	// their answers for a hard link differ between operating systems.
	paths := []string{os.Args[0]}
	if self, err := os.Executable(); err == nil {
		paths = append(paths, self)
	}
	for _, p := range paths {
		if p == "" {
			continue
		}
		name := strings.TrimSuffix(filepath.Base(p), Ext)
		if recorded, err := os.ReadFile(p + nameSuffix); err == nil {
			name = strings.TrimSpace(string(recorded))
		}
		if fake, ok := fakes[name]; ok {
			os.Exit(fake())
		}
	}
}

// Install puts a copy of the running test binary in dir under the base name
// as (plus ".exe" on Windows) and returns its path. Started from there, it
// runs the fake registered with Dispatch under the name fake.
//
// The copy is a hard link where the filesystem allows one, which is free;
// the fallback is a real copy, which costs a few megabytes of a directory
// the test framework deletes anyway.
func Install(t testing.TB, dir, as, fake string) string {
	t.Helper()
	self, err := os.Executable()
	if err != nil {
		t.Fatalf("testbin: locate the test binary: %v", err)
	}
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatalf("testbin: %v", err)
	}
	path := filepath.Join(dir, as+Ext)
	if err := link(self, path); err != nil {
		t.Fatalf("testbin: install %s: %v", path, err)
	}
	if as != fake {
		if err := os.WriteFile(path+nameSuffix, []byte(fake), 0o644); err != nil {
			t.Fatalf("testbin: %v", err)
		}
	}
	return path
}

// link hardlinks src to dst, falling back to a copy. A hard link across
// volumes, or on a filesystem that has no links, is the ordinary failure
// here and is not worth reporting.
func link(src, dst string) error {
	if err := os.Remove(dst); err != nil && !errors.Is(err, os.ErrNotExist) {
		return err
	}
	if err := os.Link(src, dst); err == nil {
		return nil
	}
	in, err := os.Open(src)
	if err != nil {
		return err
	}
	defer in.Close()
	out, err := os.OpenFile(dst, os.O_WRONLY|os.O_CREATE|os.O_TRUNC, 0o755)
	if err != nil {
		return err
	}
	if _, err := io.Copy(out, in); err != nil {
		out.Close()
		return err
	}
	return out.Close()
}

// Fail prints msg to the fake's stderr and returns an exit status, so a
// fake can end with `return testbin.Fail("...")`.
func Fail(format string, args ...any) int {
	fmt.Fprintf(os.Stderr, format+"\n", args...)
	return 1
}

// Args is the fake's own arguments, without the program name.
func Args() []string { return os.Args[1:] }

// HasArg reports whether s is one of the fake's arguments.
func HasArg(s string) bool {
	for _, a := range Args() {
		if a == s {
			return true
		}
	}
	return false
}
