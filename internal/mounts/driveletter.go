package mounts

import "errors"

// pickLetter chooses a drive letter for an automatic mount, from Z: down to
// D: like Windows itself, skipping letters that are taken and, while any
// other remains, letters where a mount just failed.
//
// We don't leave this to rclone: it calls a letter free when opening it says
// "not found", and a disconnected network drive answers "network path not
// found", so rclone hands out a letter that is taken and the mount fails.
func pickLetter(taken func(letter byte) bool, failed map[byte]bool) (string, error) {
	fallback := byte(0)
	for l := byte('Z'); l >= 'D'; l-- {
		if taken(l) {
			continue
		}
		if !failed[l] {
			return string(rune(l)) + ":", nil
		}
		if fallback == 0 {
			fallback = l
		}
	}
	if fallback != 0 { // every free letter has failed once: start over
		clear(failed)
		return string(rune(fallback)) + ":", nil
	}
	return "", errors.New("no free drive letter between D: and Z:")
}
