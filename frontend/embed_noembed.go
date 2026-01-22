//go:build noembed

// Package frontend provides the embedded frontend filesystem.
// This file is used when building with -tags noembed (CI linting/testing).
// It provides a stub filesystem that allows the code to compile without
// building the actual frontend. This is NOT suitable for production use.
package frontend

import (
	"io/fs"
	"testing/fstest"
)

// DistFS is a stub filesystem for noembed builds (linting/testing).
// This allows the code to compile without building the frontend.
// WARNING: This stub only contains a minimal index.html placeholder.
var DistFS fs.FS = fstest.MapFS{
	"index.html": &fstest.MapFile{Data: []byte("<!-- noembed stub -->")},
}
