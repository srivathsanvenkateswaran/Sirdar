package config

import (
	"bytes"
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"time"

	"github.com/srivathsanvenkateswaran/sirdar/internal/procgroup"
)

// NotFoundError reports a credential reference that could not be resolved.
// Ref carries the scheme it was written with, so an operator reading the
// failure knows which store was consulted; Detail says what that store
// reported. Neither ever holds the secret.
type NotFoundError struct {
	Ref    string
	Detail string
}

func (e *NotFoundError) Error() string {
	if e.Detail == "" {
		return "credential not found: " + e.Ref
	}
	return "credential not found: " + e.Ref + " (" + e.Detail + ")"
}

// SecretStore reads a secret from the operating system's own credential
// store by service name. Every platform Sirdar runs on has one — the macOS
// login keychain, libsecret on Linux, the Windows Credential Manager — and
// each implementation lives behind a build tag in a creds_*.go file.
//
// An implementation returns *NotFoundError when the store holds no entry
// for the service, and puts the secret nowhere but the return value.
type SecretStore interface {
	Read(service string) (string, error)
}

// KeychainReader is the old name for SecretStore, kept so existing callers
// keep compiling.
//
// Deprecated: use SecretStore.
type KeychainReader = SecretStore

// credSchemes are the prefixes a credential reference may carry.
var credSchemes = []string{"env:", "keychain:", "file:", "cmd:"}

// credSchemeList is the human-readable form of credSchemes, for errors.
const credSchemeList = "env:, keychain:, file: or cmd:"

// IsCredentialRef reports whether ref names a credential rather than
// carrying one. Config validation and Resolve share it so the set of
// schemes lives in exactly one place.
func IsCredentialRef(ref string) bool {
	for _, s := range credSchemes {
		if strings.HasPrefix(ref, s) && len(ref) > len(s) {
			return true
		}
	}
	return false
}

// cmdTimeout bounds a "cmd:" reference. A credential helper that prompts
// for a touch or a master password needs a few seconds; one that hangs
// must not hold a run open forever. A var so a test can shorten it.
var cmdTimeout = 10 * time.Second

// killGrace is how long a timed-out helper's process group has to die
// before Wait gives up on it.
const killGrace = 2 * time.Second

// Resolver turns a credential reference into the secret it names. A literal
// value is always an error: config files must never hold a credential
// directly. Four schemes resolve, all of them on every platform:
//
//	env:NAME       the environment variable NAME
//	keychain:SVC   the OS credential store, through Keychain
//	file:PATH      a file holding nothing but the secret
//	cmd:COMMAND    the standard output of a credential helper
type Resolver struct {
	Env      func(string) (string, bool)
	Keychain SecretStore
}

// Resolve looks up ref and returns the secret it points to. Every failure
// names the scheme that failed, so an operator knows which store to go and
// look in.
func (r Resolver) Resolve(ref string) (string, error) {
	switch {
	case strings.HasPrefix(ref, "env:"):
		if r.Env == nil {
			r.Env = os.LookupEnv
		}
		name := ref[len("env:"):]
		v, ok := r.Env(name)
		if !ok || v == "" {
			return "", &NotFoundError{Ref: ref, Detail: "environment variable " + name + " is unset or empty"}
		}
		return v, nil
	case strings.HasPrefix(ref, "keychain:"):
		if r.Keychain == nil {
			return "", fmt.Errorf("keychain: refs are not supported on %s; use env:, file: or cmd:", runtime.GOOS)
		}
		return r.Keychain.Read(ref[len("keychain:"):])
	case strings.HasPrefix(ref, "file:"):
		return readSecretFile(ref[len("file:"):])
	case strings.HasPrefix(ref, "cmd:"):
		return runSecretCommand(ref[len("cmd:"):])
	}
	return "", fmt.Errorf("credential ref %q must start with %s", ref, credSchemeList)
}

// readSecretFile reads a secret from a file that holds nothing else. The
// file must not be reachable by anyone but its owner: a credential sitting
// in a group- or world-readable file is a credential everyone with an
// account on the machine has, and Sirdar refuses it rather than quietly
// using it.
//
// One trailing newline is dropped, because every editor and every `> file`
// adds one; anything further inside the file is part of the secret.
func readSecretFile(path string) (string, error) {
	ref := "file:" + path
	expanded, err := expandHome(path)
	if err != nil {
		return "", fmt.Errorf("%s: %w", ref, err)
	}

	fi, err := os.Stat(expanded)
	if err != nil {
		if os.IsNotExist(err) {
			return "", &NotFoundError{Ref: ref, Detail: "no such file " + expanded}
		}
		return "", fmt.Errorf("%s: %w", ref, err)
	}
	if fi.IsDir() {
		return "", fmt.Errorf("%s: %s is a directory", ref, expanded)
	}
	// Unix permission bits mean nothing on Windows, where os.Stat
	// synthesises 0666 or 0444 from the read-only attribute and the ACL
	// that does govern the file is invisible here. The rule is enforced
	// where it can be.
	if runtime.GOOS != "windows" {
		if mode := fi.Mode().Perm(); mode&0o077 != 0 {
			return "", fmt.Errorf("%s: %s is readable or writable beyond its owner (mode %04o); run: chmod 600 %s", ref, expanded, mode, expanded)
		}
	}

	b, err := os.ReadFile(expanded)
	if err != nil {
		return "", fmt.Errorf("%s: %w", ref, err)
	}
	secret := trimOneNewline(string(b))
	if secret == "" {
		return "", &NotFoundError{Ref: ref, Detail: expanded + " is empty"}
	}
	return secret, nil
}

// runSecretCommand runs a credential helper and takes its standard output
// as the secret: `op read`, `bw get password`, `vault kv get -field`,
// `gopass show -o`, or anything else that prints one secret and exits.
//
// The command goes through the platform's shell, so pipes and quoting in
// the reference behave the way they do in a terminal. Its standard output
// never reaches a log, an error message or a run directory — only the
// return value. Standard error does reach the error, because a helper that
// fails says why there and that message is a diagnostic, not the secret.
func runSecretCommand(command string) (string, error) {
	ref := "cmd:" + command
	if strings.TrimSpace(command) == "" {
		return "", fmt.Errorf("%s: the command is empty", ref)
	}

	ctx, cancel := context.WithTimeout(context.Background(), cmdTimeout)
	defer cancel()

	var cmd *exec.Cmd
	if runtime.GOOS == "windows" {
		cmd = exec.CommandContext(ctx, "cmd", "/C", command)
	} else {
		cmd = exec.CommandContext(ctx, "sh", "-c", command)
	}
	// The helper runs in a process group of its own so a timeout can kill
	// everything it started: a piped helper ("op read ... | tr -d '\n'")
	// leaves children that keep stdout open, and killing only the shell
	// would leave them running with Wait blocked on that pipe. WaitDelay is
	// the backstop for a grandchild that survives the group kill. This is a
	// no-op on Windows, which has no addressable process group here; see
	// internal/procgroup.
	procgroup.Setup(cmd)
	cmd.Cancel = func() error { return procgroup.Kill(cmd) }
	cmd.WaitDelay = killGrace
	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr

	err := cmd.Run()
	if ctx.Err() == context.DeadlineExceeded {
		return "", fmt.Errorf("%s: timed out after %s", ref, cmdTimeout)
	}
	if err != nil {
		if msg := firstLine(stderr.String()); msg != "" {
			return "", fmt.Errorf("%s: %v: %s", ref, err, msg)
		}
		return "", fmt.Errorf("%s: %v", ref, err)
	}
	secret := strings.TrimSpace(stdout.String())
	if secret == "" {
		return "", &NotFoundError{Ref: ref, Detail: "the command printed nothing"}
	}
	return secret, nil
}

// expandHome turns a leading "~" or "~/" into the user's home directory.
// A relative path stays relative to the working directory: a credential
// reference is written once, in a config file an operator owns, and an
// absolute path or a "~/" one is what belongs there.
func expandHome(p string) (string, error) {
	if p == "" {
		return "", fmt.Errorf("the path is empty")
	}
	if p != "~" && !strings.HasPrefix(p, "~/") {
		return p, nil
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return "", fmt.Errorf("cannot expand ~: %w", err)
	}
	if p == "~" {
		return home, nil
	}
	return filepath.Join(home, p[2:]), nil
}

// trimOneNewline drops a single trailing newline, CRLF included. Only one:
// a secret that genuinely ends in a blank line keeps it.
func trimOneNewline(s string) string {
	s = strings.TrimSuffix(s, "\n")
	return strings.TrimSuffix(s, "\r")
}

// firstLine is the first non-empty line of a helper's stderr, capped, for
// an error message.
func firstLine(s string) string {
	for _, line := range strings.Split(s, "\n") {
		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}
		if len(line) > 200 {
			return line[:200] + "..."
		}
		return line
	}
	return ""
}
