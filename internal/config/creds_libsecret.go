//go:build unix && !darwin

package config

// PlatformSecretStore returns the credential store this operating system
// ships with. On Linux and the BSDs that is the Secret Service: GNOME
// Keyring, KWallet behind the org.freedesktop.secrets portal, KeePassXC's
// implementation of the same interface — every one of them answers
// `secret-tool`, so one reader covers all of them. Where libsecret is not
// installed, pass(1) stands in.
func PlatformSecretStore() SecretStore { return SecretTool{} }
