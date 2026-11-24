// Package ui serves the settings window's static files, embedded in the
// binary.
package ui

import (
	"embed"
	"io/fs"
	"net/http"
)

//go:embed web
var files embed.FS

// Handler serves the UI.
func Handler() http.Handler {
	sub, err := fs.Sub(files, "web")
	if err != nil {
		panic(err)
	}
	return http.FileServer(http.FS(sub))
}
