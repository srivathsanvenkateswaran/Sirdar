//go:build darwin

package config

import (
	"os/exec"
	"strings"
)

// PlatformSecretStore returns the credential store this operating system
// ships with: on macOS the login keychain, read through `security`.
func PlatformSecretStore() SecretStore { return MacKeychain{} }

// MacKeychain reads secrets from the macOS login keychain via `security`.
type MacKeychain struct{}

func (MacKeychain) Read(service string) (string, error) {
	out, err := exec.Command("security", "find-generic-password", "-s", service, "-w").Output()
	if err != nil {
		return "", &NotFoundError{Ref: "keychain:" + service, Detail: "no generic password for service " + service + " in the login keychain"}
	}
	return strings.TrimSpace(string(out)), nil
}
