package config

import (
	"fmt"
	"os"
	"os/exec"
	"strings"
)

// NotFoundError reports a credential reference that could not be resolved.
type NotFoundError struct{ Ref string }

func (e *NotFoundError) Error() string { return "credential not found: " + e.Ref }

// KeychainReader reads a secret from a platform keychain by service name.
type KeychainReader interface {
	Read(service string) (string, error)
}

// Resolver turns a credential reference ("env:NAME" or "keychain:SERVICE")
// into the secret it names. A literal value is always an error: config files
// must never hold a credential directly.
type Resolver struct {
	Env      func(string) (string, bool)
	Keychain KeychainReader
}

// Resolve looks up ref and returns the secret it points to.
func (r Resolver) Resolve(ref string) (string, error) {
	switch {
	case strings.HasPrefix(ref, "env:"):
		if r.Env == nil {
			r.Env = os.LookupEnv
		}
		v, ok := r.Env(ref[len("env:"):])
		if !ok || v == "" {
			return "", &NotFoundError{Ref: ref}
		}
		return v, nil
	case strings.HasPrefix(ref, "keychain:"):
		if r.Keychain == nil {
			return "", fmt.Errorf("keychain refs are not supported on this platform")
		}
		return r.Keychain.Read(ref[len("keychain:"):])
	}
	return "", fmt.Errorf("credential ref %q must start with env: or keychain:", ref)
}

// MacKeychain reads secrets from the macOS login keychain via `security`.
type MacKeychain struct{}

func (MacKeychain) Read(service string) (string, error) {
	out, err := exec.Command("security", "find-generic-password", "-s", service, "-w").Output()
	if err != nil {
		return "", &NotFoundError{Ref: "keychain:" + service}
	}
	return strings.TrimSpace(string(out)), nil
}
