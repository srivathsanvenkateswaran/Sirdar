//go:build unix

package config

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// stubPath puts scripts in a directory of their own and makes it the whole
// of PATH, so the test controls exactly which credential helpers
// SecretTool can find. Each entry is a name and a shell script body.
func stubPath(t *testing.T, scripts map[string]string) {
	t.Helper()
	dir := t.TempDir()
	for name, body := range scripts {
		path := filepath.Join(dir, name)
		if err := os.WriteFile(path, []byte("#!/bin/sh\n"+body), 0o700); err != nil {
			t.Fatal(err)
		}
	}
	t.Setenv("PATH", dir)
}

// secretToolStub answers `secret-tool lookup service zoho` and nothing
// else, the way libsecret does: the secret on stdout with no newline, or
// status 1 and silence when no item matches.
const secretToolStub = `
if [ "$1" = lookup ] && [ "$2" = service ] && [ "$3" = zoho ]; then
	printf 's3cret'
	exit 0
fi
exit 1
`

// passStub answers `pass show sirdar/zoho`. A pass entry is a file whose
// first line is the password and whose remaining lines are notes.
const passStub = `
if [ "$1" = show ] && [ "$2" = sirdar/zoho ]; then
	printf 'p4ss\nurl: https://desk.zoho.in\n'
	exit 0
fi
echo "Error: $2 is not in the password store." >&2
exit 1
`

func TestSecretToolReadsLibsecret(t *testing.T) {
	stubPath(t, map[string]string{"secret-tool": secretToolStub})

	v, err := SecretTool{}.Read("zoho")
	if err != nil || v != "s3cret" {
		t.Fatalf("got %q err %v", v, err)
	}

	var nf *NotFoundError
	_, err = SecretTool{}.Read("absent")
	if !errors.As(err, &nf) {
		t.Fatalf("a missing item is a missing credential, got %v", err)
	}
	if !strings.Contains(err.Error(), "keychain:absent") {
		t.Fatalf("%v should name the ref that failed", err)
	}
}

// TestSecretToolReportsALockedKeyring: secret-tool says nothing at all
// when the attribute matches no item, and says why on stderr when the
// keyring itself is the problem. The second is not a missing credential.
func TestSecretToolReportsALockedKeyring(t *testing.T) {
	stubPath(t, map[string]string{
		"secret-tool": "echo 'Cannot autolaunch D-Bus without X11 $DISPLAY' >&2\nexit 1\n",
	})
	_, err := SecretTool{}.Read("zoho")
	if err == nil {
		t.Fatal("want an error")
	}
	var nf *NotFoundError
	if errors.As(err, &nf) {
		t.Fatal("a broken keyring is not a missing credential")
	}
	if !strings.Contains(err.Error(), "D-Bus") {
		t.Fatalf("%v should carry secret-tool's own message", err)
	}
}

// TestSecretToolFallsBackToPass: where libsecret is not installed but pass
// is, the entry is read from sirdar/<service> and only its first line is
// the password.
func TestSecretToolFallsBackToPass(t *testing.T) {
	stubPath(t, map[string]string{"pass": passStub})

	v, err := SecretTool{}.Read("zoho")
	if err != nil || v != "p4ss" {
		t.Fatalf("got %q err %v", v, err)
	}

	var nf *NotFoundError
	if _, err := (SecretTool{}).Read("absent"); !errors.As(err, &nf) {
		t.Fatalf("a missing entry is a missing credential, got %v", err)
	}
}

// TestSecretToolPrefersLibsecret: with both installed, the Secret Service
// is the store, because that is where `secret-tool store` put the secret.
func TestSecretToolPrefersLibsecret(t *testing.T) {
	stubPath(t, map[string]string{"secret-tool": secretToolStub, "pass": passStub})
	if v, err := (SecretTool{}).Read("zoho"); err != nil || v != "s3cret" {
		t.Fatalf("got %q err %v", v, err)
	}
}

// TestSecretToolWithNoHelper: neither helper installed is a configuration
// problem, and the error says what to install and what else would work.
func TestSecretToolWithNoHelper(t *testing.T) {
	stubPath(t, nil)
	_, err := SecretTool{}.Read("zoho")
	if err == nil {
		t.Fatal("want an error")
	}
	for _, want := range []string{"keychain:zoho", "secret-tool", "pass", "cmd:"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("%v should mention %q", err, want)
		}
	}
	var nf *NotFoundError
	if errors.As(err, &nf) {
		t.Fatal("no helper installed is not a missing credential")
	}
}
