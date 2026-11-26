// Package secrets keeps the rclone.conf password in the OS keychain
// (Windows Credential Manager, the Secret Service on Linux, the Android
// Keystore) when the user opts in, so an encrypted config can unlock at
// login without a prompt.
package secrets

// ConfigPassword returns the saved password, or "" if none is saved or no
// keychain is available.
func ConfigPassword() string { return load() }

// SaveConfigPassword stores the password.
func SaveConfigPassword(p string) error { return save(p) }

// ForgetConfigPassword removes it; not having one is not an error.
func ForgetConfigPassword() error { return forget() }
