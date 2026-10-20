// Package webui embeds the built frontend (web/dist, copied here by
// "make web"). Only dist/.gitkeep is committed; without a build the server
// shows a "frontend not built" page.
package webui

import (
	"embed"
	"io/fs"
)

//go:embed all:dist
var dist embed.FS

// FS returns the frontend files rooted at dist/.
func FS() fs.FS {
	sub, err := fs.Sub(dist, "dist")
	if err != nil {
		panic(err)
	}
	return sub
}
