//go:build tools

package mobile

// gomobile bind needs its support package in the module graph.
import _ "golang.org/x/mobile/bind"
