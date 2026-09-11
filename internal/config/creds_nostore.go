//go:build !unix && !windows

package config

// PlatformSecretStore returns nil on a platform with no credential store
// Sirdar knows how to read. A "keychain:" ref then fails with a message
// saying so, and env:, file: and cmd: refs still work.
func PlatformSecretStore() SecretStore { return nil }
