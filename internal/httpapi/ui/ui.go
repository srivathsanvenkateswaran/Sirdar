// Package ui carries the built frontend into the binary. The dist tree is
// what `make ui` writes; only .gitkeep is committed, so a checkout without
// a frontend build still compiles and the server falls back to the "not
// built" page.
package ui

import (
	"embed"
	"io/fs"
)

//go:embed all:dist
var Dist embed.FS

// FS returns the dist subtree, which is what the HTTP server serves: paths
// in it are the ones the browser asks for ("index.html", "assets/app.js").
func FS() fs.FS {
	sub, err := fs.Sub(Dist, "dist")
	if err != nil {
		// dist is embedded above, so fs.Sub can only fail if the embed
		// itself is broken, which is a build-time mistake.
		panic("httpapi/ui: " + err.Error())
	}
	return sub
}
