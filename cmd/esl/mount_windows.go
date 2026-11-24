//go:build windows

package main

// Windows mounts go through WinFsp via rclone's cmount, which is only
// compiled with the "cmount" build tag. Build with:  go build -tags cmount
import _ "github.com/rclone/rclone/cmd/cmount"
