// Package secrets keeps the rclone.conf password in the OS keychain
// (Windows Credential Manager, the Secret Service on Linux) when the user
// opts in, so an encrypted config can unlock at login without a prompt.
package secrets

import (
	"errors"

	"github.com/blaineam/enter-space-light/internal/paths"
	"github.com/zalando/go-keyring"
)

const account = "rclone.conf"

// service keeps a sandbox's password apart from the real one.
func service() string {
	if h := paths.Home(); h != "" {
		return "Enter Space Light (sandbox " + h + ")"
	}
	return "Enter Space Light"
}

// ConfigPassword returns the saved password, or "" if none is saved or no
// keychain is available.
func ConfigPassword() string {
	p, err := keyring.Get(service(), account)
	if err != nil {
		return ""
	}
	return p
}

// SaveConfigPassword stores the password.
func SaveConfigPassword(p string) error { return keyring.Set(service(), account, p) }

// ForgetConfigPassword removes it; not having one is not an error.
func ForgetConfigPassword() error {
	err := keyring.Delete(service(), account)
	if errors.Is(err, keyring.ErrNotFound) {
		return nil
	}
	return err
}
