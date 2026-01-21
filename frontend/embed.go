//go:build !noembed

package frontend

import (
	"embed"
	"io/fs"
)

//go:embed all:dist
var distDir embed.FS

// DistFS is the embedded Svelte build output filesystem
var DistFS fs.FS

func init() {
	// Strip the "dist" prefix to serve files directly
	DistFS, _ = fs.Sub(distDir, "dist")
}
