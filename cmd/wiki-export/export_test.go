package main

import (
	"os"
	"path/filepath"
	"runtime"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestExport(t *testing.T) {
	t.Parallel()

	src := t.TempDir()
	out := t.TempDir()

	// A remapped page that links to a sibling page and a repo file.
	writeFile(t, filepath.Join(src, "guide.md"),
		"# Guide\n\nSee [installation](installation.md) and [privacy](../../PRIVACY.md).\n")
	// A pass-through page.
	writeFile(t, filepath.Join(src, "installation.md"),
		"# Installation\n\nInstall steps.\n")
	// An image asset that must be copied verbatim.
	require.NoError(t, os.MkdirAll(filepath.Join(src, "images"), 0o755))
	writeFile(t, filepath.Join(src, "images", "diagram.png"), "PNGDATA")
	// A non-markdown file at the top level must be ignored.
	writeFile(t, filepath.Join(src, "notes.txt"), "ignore me")

	require.NoError(t, export(src, out))

	// guide.md is published as BirdNET-Go-Guide.md with rewritten links + banner.
	guide := readFile(t, filepath.Join(out, "BirdNET-Go-Guide.md"))
	assert.Contains(t, guide, bannerMarker)
	assert.Contains(t, guide, "[installation](installation)")
	assert.Contains(t, guide, "[privacy](https://github.com/tphakala/birdnet-go/blob/main/PRIVACY.md)")
	assert.Contains(t, guide, "doc/wiki/guide.md", "banner should reference the source path")

	// installation.md is published under its own name.
	assert.FileExists(t, filepath.Join(out, "installation.md"))

	// The original source name for a remapped page must NOT appear in output.
	assert.NoFileExists(t, filepath.Join(out, "guide.md"))

	// Images are copied through.
	assert.Equal(t, "PNGDATA", readFile(t, filepath.Join(out, "images", "diagram.png")))

	// Non-markdown top-level files are not published.
	assert.NoFileExists(t, filepath.Join(out, "notes.txt"))
}

func TestExportSkipsImageSymlinks(t *testing.T) {
	t.Parallel()

	src := t.TempDir()
	out := t.TempDir()
	writeFile(t, filepath.Join(src, "index.md"), "# Home\n\nBody.\n")

	// A secret file outside the wiki tree that a symlink could try to leak.
	secret := filepath.Join(t.TempDir(), "secret.txt")
	writeFile(t, secret, "TOP SECRET")

	require.NoError(t, os.MkdirAll(filepath.Join(src, "images"), 0o755))
	writeFile(t, filepath.Join(src, "images", "real.png"), "PNGDATA")
	if err := os.Symlink(secret, filepath.Join(src, "images", "leak.png")); err != nil {
		// Windows runners often lack the privilege to create symlinks; elsewhere
		// a failure is unexpected and must not silently hide the test.
		if runtime.GOOS == "windows" {
			t.Skipf("symlink creation not supported here: %v", err)
		}
		require.NoError(t, err)
	}

	require.NoError(t, export(src, out))

	// The regular image is copied; the symlink is skipped, so its target's
	// contents are never published to the wiki.
	assert.Equal(t, "PNGDATA", readFile(t, filepath.Join(out, "images", "real.png")))
	assert.NoFileExists(t, filepath.Join(out, "images", "leak.png"))
}

func writeFile(t *testing.T, path, content string) {
	t.Helper()
	require.NoError(t, os.WriteFile(path, []byte(content), 0o600))
}

func readFile(t *testing.T, path string) string {
	t.Helper()
	b, err := os.ReadFile(path) //nolint:gosec // test reads files it just wrote
	require.NoError(t, err)
	return string(b)
}
