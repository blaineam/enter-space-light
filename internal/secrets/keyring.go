//go:build !android

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

func load() string {
	p, err := keyring.Get(service(), account)
	if err != nil {
		return ""
	}
	return p
}

func save(p string) error { return keyring.Set(service(), account, p) }

func forget() error {
	err := keyring.Delete(service(), account)
	if errors.Is(err, keyring.ErrNotFound) {
		return nil
	}
	return err
}
