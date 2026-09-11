package config

import (
	"errors"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

type fakeKC map[string]string

func (f fakeKC) Read(s string) (string, error) {
	v, ok := f[s]
	if !ok {
		return "", &NotFoundError{Ref: "keychain:" + s}
	}
	return v, nil
}

func testResolver() Resolver {
	return Resolver{
		Env: func(k string) (string, bool) {
			if k == "ZOHO" {
				return "e-tok", true
			}
			return "", false
		},
		Keychain: fakeKC{"zoho": "k-tok"},
	}
}

func TestResolve(t *testing.T) {
	r := testResolver()
	if v, _ := r.Resolve("env:ZOHO"); v != "e-tok" {
		t.Fatal(v)
	}
	if v, _ := r.Resolve("keychain:zoho"); v != "k-tok" {
		t.Fatal(v)
	}
	if _, err := r.Resolve("env:MISSING"); err == nil {
		t.Fatal("want error")
	}
	if _, err := r.Resolve("keychain:nope"); err == nil {
		t.Fatal("want error")
	}
	if _, err := r.Resolve("literal"); err == nil {
		t.Fatal("literal refs are rejected")
	}
}

// TestResolveErrorsNameTheScheme: every failure has to say which store was
// consulted, because that is the only thing an operator can act on.
func TestResolveErrorsNameTheScheme(t *testing.T) {
	r := testResolver()
	dir := t.TempDir()
	for _, tc := range []struct{ ref, want string }{
		{"env:MISSING", "env:MISSING"},
		{"keychain:nope", "keychain:nope"},
		{"file:" + filepath.Join(dir, "absent"), "file:"},
		{"cmd:exit 7", "cmd:"},
		{"nonsense", "env:, keychain:, file: or cmd:"},
	} {
		_, err := r.Resolve(tc.ref)
		if err == nil {
			t.Fatalf("%s: want an error", tc.ref)
		}
		if !strings.Contains(err.Error(), tc.want) {
			t.Fatalf("%s: %v does not name %q", tc.ref, err, tc.want)
		}
	}
}

// TestResolveKeychainWithoutStore: on a platform with no store Sirdar can
// read, a keychain: ref says so and points at what does work.
func TestResolveKeychainWithoutStore(t *testing.T) {
	_, err := Resolver{}.Resolve("keychain:zoho")
	if err == nil || !strings.Contains(err.Error(), "not supported") {
		t.Fatalf("got %v", err)
	}
	var nf *NotFoundError
	if errors.As(err, &nf) {
		t.Fatal("an unsupported platform is not a missing credential")
	}
}

func TestIsCredentialRef(t *testing.T) {
	for _, ok := range []string{"env:X", "keychain:svc", "file:/tmp/t", "cmd:op read op://v/i/f", "file:~/x"} {
		if !IsCredentialRef(ok) {
			t.Errorf("%q should be a credential ref", ok)
		}
	}
	for _, bad := range []string{"", "plaintext-token", "env:", "keychain:", "file:", "cmd:", "ENV:X", "envX", "https://example.com"} {
		if IsCredentialRef(bad) {
			t.Errorf("%q should not be a credential ref", bad)
		}
	}
}

// writeSecretFile puts body in a file with the given mode and returns its
// path. t.TempDir is 0700, so only the file's own bits decide.
func writeSecretFile(t *testing.T, name, body string, mode os.FileMode) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), name)
	if err := os.WriteFile(path, []byte(body), mode); err != nil {
		t.Fatal(err)
	}
	// WriteFile applies the umask; the test needs the mode it asked for.
	if err := os.Chmod(path, mode); err != nil {
		t.Fatal(err)
	}
	return path
}

func TestResolveFile(t *testing.T) {
	r := testResolver()

	path := writeSecretFile(t, "token", "s3cret\n", 0o600)
	v, err := r.Resolve("file:" + path)
	if err != nil || v != "s3cret" {
		t.Fatalf("got %q err %v", v, err)
	}

	// Only one trailing newline goes; a secret that ends in a blank line
	// keeps it, and CRLF counts as one newline.
	crlf := writeSecretFile(t, "crlf", "s3cret\r\n", 0o600)
	if v, err := r.Resolve("file:" + crlf); err != nil || v != "s3cret" {
		t.Fatalf("got %q err %v", v, err)
	}
	two := writeSecretFile(t, "two", "s3cret\n\n", 0o600)
	if v, err := r.Resolve("file:" + two); err != nil || v != "s3cret\n" {
		t.Fatalf("got %q err %v", v, err)
	}

	empty := writeSecretFile(t, "empty", "\n", 0o600)
	var nf *NotFoundError
	if _, err := r.Resolve("file:" + empty); !errors.As(err, &nf) {
		t.Fatalf("an empty file is a missing credential, got %v", err)
	}
	if _, err := r.Resolve("file:" + filepath.Join(t.TempDir(), "absent")); !errors.As(err, &nf) {
		t.Fatalf("a missing file is a missing credential, got %v", err)
	}
	if _, err := r.Resolve("file:" + t.TempDir()); err == nil {
		t.Fatal("a directory is not a secret")
	}
}

// TestResolveFilePermissions: a credential in a file the rest of the
// machine can read is refused outright rather than used with a warning.
func TestResolveFileTooOpen(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("unix permission bits do not govern the file on Windows")
	}
	r := testResolver()
	for _, mode := range []os.FileMode{0o644, 0o640, 0o604, 0o660} {
		path := writeSecretFile(t, "token", "s3cret\n", mode)
		_, err := r.Resolve("file:" + path)
		if err == nil {
			t.Fatalf("mode %04o: want a refusal", mode)
		}
		if !strings.Contains(err.Error(), "chmod 600") {
			t.Fatalf("mode %04o: %v should say how to fix it", mode, err)
		}
		var nf *NotFoundError
		if errors.As(err, &nf) {
			t.Fatalf("mode %04o: a too-open file is not a missing credential", mode)
		}
	}
	// 0600 and 0400 are both fine: the owner alone can read them.
	for _, mode := range []os.FileMode{0o600, 0o400} {
		path := writeSecretFile(t, "token", "s3cret", mode)
		if v, err := r.Resolve("file:" + path); err != nil || v != "s3cret" {
			t.Fatalf("mode %04o: got %q err %v", mode, v, err)
		}
	}
}

func TestResolveFileExpandsHome(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	if runtime.GOOS == "windows" {
		t.Setenv("USERPROFILE", home)
	}
	path := filepath.Join(home, ".sirdar-token")
	if err := os.WriteFile(path, []byte("h0me\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod(path, 0o600); err != nil {
		t.Fatal(err)
	}
	v, err := testResolver().Resolve("file:~/.sirdar-token")
	if err != nil || v != "h0me" {
		t.Fatalf("got %q err %v", v, err)
	}
}

func TestResolveCmd(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("the echo in these refs is a shell builtin, not cmd.exe's")
	}
	r := testResolver()

	if v, err := r.Resolve("cmd:echo s3cret"); err != nil || v != "s3cret" {
		t.Fatalf("got %q err %v", v, err)
	}
	// A helper that pipes, like `vault ... | jq -r`, works as written.
	if v, err := r.Resolve("cmd:printf 's3cret\\n' | tr -d '\\n'"); err != nil || v != "s3cret" {
		t.Fatalf("got %q err %v", v, err)
	}

	var nf *NotFoundError
	if _, err := r.Resolve("cmd:true"); !errors.As(err, &nf) {
		t.Fatalf("a helper that prints nothing is a missing credential, got %v", err)
	}
	if _, err := r.Resolve("cmd:   "); err == nil {
		t.Fatal("an empty command is rejected")
	}

	// A failing helper's stderr reaches the error, since that is the
	// diagnostic; its stdout never does, since that is the secret. The
	// helper is a script rather than an inline `echo`, so that what it
	// prints is nowhere in the reference the error quotes back.
	helper := filepath.Join(t.TempDir(), "helper")
	body := "#!/bin/sh\necho s3cret\necho 'not signed in' >&2\nexit 1\n"
	if err := os.WriteFile(helper, []byte(body), 0o700); err != nil {
		t.Fatal(err)
	}
	_, err := r.Resolve("cmd:" + helper)
	if err == nil {
		t.Fatal("want an error")
	}
	if !strings.Contains(err.Error(), "not signed in") {
		t.Fatalf("%v should carry the helper's stderr", err)
	}
	if strings.Contains(err.Error(), "s3cret") {
		t.Fatalf("%v leaks the helper's stdout", err)
	}
}

// TestSecretStoreSatisfiedByPlatformStore: the platform's own store is a
// SecretStore, and KeychainReader still names the same interface for
// callers written against the old name.
func TestPlatformSecretStore(t *testing.T) {
	var s SecretStore = PlatformSecretStore()
	var k KeychainReader = s
	_ = k
	switch runtime.GOOS {
	case "darwin", "linux", "windows":
		if s == nil {
			t.Fatalf("%s ships a credential store; PlatformSecretStore returned nil", runtime.GOOS)
		}
	}
}

// TestCredentialRefValidationUsesTheSameSchemes: the config validator and
// the resolver must agree, or a config that loads holds a ref nothing can
// resolve. They share IsCredentialRef precisely so they cannot drift.
func TestCredentialRefValidationUsesTheSameSchemes(t *testing.T) {
	for _, ref := range []string{"env:ZOHO", "keychain:zoho", "file:/etc/sirdar/token", "cmd:op read op://v/i/f"} {
		if err := credentialRef("sources.helpdesk.token", ref); err != nil {
			t.Errorf("%q: %v", ref, err)
		}
	}
	err := credentialRef("sources.helpdesk.token", "1000.an-actual-token")
	if err == nil {
		t.Fatal("a literal secret is rejected")
	}
	for _, want := range []string{"sources.helpdesk.token", "env:, keychain:, file: or cmd:"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("%v should mention %q", err, want)
		}
	}
}
