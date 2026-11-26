package secrets

import (
	"errors"

	"github.com/blaineam/enter-space-light/internal/android"
)

// The app encrypts the password with a key held in the Android Keystore.

func load() string {
	if h := android.Current(); h != nil {
		return h.LoadSecret()
	}
	return ""
}

func save(p string) error {
	if h := android.Current(); h != nil && h.SaveSecret(p) {
		return nil
	}
	return errors.New("the Android Keystore refused to save the password")
}

func forget() error {
	if h := android.Current(); h != nil && h.SaveSecret("") {
		return nil
	}
	return errors.New("the saved password could not be removed")
}
