//go:build unix

package config

import (
	"bytes"
	"errors"
	"fmt"
	"os/exec"
	"strings"
)

// passFolder is the directory inside a pass(1) store that Sirdar's entries
// live in, so `keychain:zoho-desk-token` reads `sirdar/zoho-desk-token`
// rather than colliding with the rest of the operator's passwords.
const passFolder = "sirdar/"

// SecretTool reads secrets from the freedesktop Secret Service — GNOME
// Keyring, KWallet through the org.freedesktop.secrets portal, KeePassXC —
// using libsecret's `secret-tool`, and falls back to pass(1) where
// `secret-tool` is not installed but `pass` is.
//
// The two stores are looked up by different keys, because that is how each
// one is organised: `secret-tool` matches the attribute `service`, pass
// reads the entry `sirdar/<service>`. docs/credentials.md gives the store
// command for each.
//
// The implementation is compiled on every unix, macOS included, so that a
// test can exercise it against a stub on PATH; only Linux and the BSDs
// select it (see PlatformSecretStore).
type SecretTool struct{}

func (SecretTool) Read(service string) (string, error) {
	ref := "keychain:" + service
	haveSecretTool := onPath("secret-tool")
	havePass := onPath("pass")

	switch {
	case haveSecretTool:
		out, err := runQuiet("secret-tool", "lookup", "service", service)
		if err != nil {
			return "", secretToolError(ref, service, err)
		}
		// secret-tool prints the secret with no trailing newline and
		// prints nothing at all when the attribute matches no item.
		secret := trimOneNewline(out)
		if secret == "" {
			return "", &NotFoundError{Ref: ref, Detail: "no secret-tool item with service=" + service}
		}
		return secret, nil

	case havePass:
		out, err := runQuiet("pass", "show", passFolder+service)
		if err != nil {
			return "", passError(ref, passFolder+service, err)
		}
		// A pass entry is a file whose first line is the password; the
		// rest is whatever notes the operator keeps with it.
		secret := strings.TrimRight(firstLineRaw(out), "\r")
		if secret == "" {
			return "", &NotFoundError{Ref: ref, Detail: "pass entry " + passFolder + service + " is empty"}
		}
		return secret, nil
	}
	return "", fmt.Errorf("%s: neither secret-tool nor pass is on PATH; install libsecret (Debian/Ubuntu: libsecret-tools, Fedora: libsecret) or pass, or use an env:, file: or cmd: ref instead", ref)
}

// secretToolError distinguishes "no such item" — which secret-tool reports
// as exit status 1 with nothing on either stream — from a keyring that is
// locked, absent or broken, which says so on stderr.
func secretToolError(ref, service string, err error) error {
	var ee *exec.ExitError
	if errors.As(err, &ee) {
		if msg := firstLine(string(ee.Stderr)); msg != "" {
			return fmt.Errorf("%s: secret-tool: %s", ref, msg)
		}
		return &NotFoundError{Ref: ref, Detail: "no secret-tool item with service=" + service}
	}
	return fmt.Errorf("%s: secret-tool: %w", ref, err)
}

// passError distinguishes "no such entry" — which pass reports on stderr
// as "Error: <entry> is not in the password store." — from a failure
// somewhere in the GPG chain underneath it (gpg-agent unreachable, no
// secret key, a locked smartcard), which pass also reports on stderr but
// with no such wording. The first is a missing credential; the second is
// not, and is surfaced like secretToolError does.
func passError(ref, entry string, err error) error {
	var ee *exec.ExitError
	if errors.As(err, &ee) {
		if msg := firstLine(string(ee.Stderr)); msg != "" {
			if strings.Contains(msg, "is not in the password store") {
				return &NotFoundError{Ref: ref, Detail: "pass has no entry " + entry}
			}
			return fmt.Errorf("%s: pass: %s", ref, msg)
		}
		return &NotFoundError{Ref: ref, Detail: "pass has no entry " + entry}
	}
	return fmt.Errorf("%s: pass: %w", ref, err)
}

// onPath reports whether a program is available to run.
func onPath(name string) bool {
	_, err := exec.LookPath(name)
	return err == nil
}

// runQuiet runs a credential helper and returns its standard output. The
// output is the secret, so it goes nowhere else: stderr is captured
// separately for the error message and never mixed in.
func runQuiet(name string, args ...string) (string, error) {
	cmd := exec.Command(name, args...)
	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr
	if err := cmd.Run(); err != nil {
		var ee *exec.ExitError
		if errors.As(err, &ee) {
			ee.Stderr = stderr.Bytes()
		}
		return "", err
	}
	return stdout.String(), nil
}

// firstLineRaw is everything before the first newline, untrimmed: a
// password may legitimately begin or end with a space.
func firstLineRaw(s string) string {
	if i := strings.IndexByte(s, '\n'); i >= 0 {
		return s[:i]
	}
	return s
}
