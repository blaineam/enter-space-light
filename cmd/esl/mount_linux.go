//go:build linux && !android

package main

// Linux mounts use rclone's pure-Go FUSE implementation (no cgo, just the
// fusermount3 helper from the fuse3 package).
import _ "github.com/rclone/rclone/cmd/mount"
