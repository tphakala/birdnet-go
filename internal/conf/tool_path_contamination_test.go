package conf

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestIsContaminatedToolPath verifies the shared proxy/ingress contamination
// detector used by both configuration-time and execution-time tool validation.
func TestIsContaminatedToolPath(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name string
		path string
		want bool
	}{
		{name: "clean absolute unix path", path: "/usr/bin/ffmpeg", want: false},
		{name: "clean absolute usr local path", path: "/usr/local/bin/ffmpeg", want: false},
		{name: "clean windows path", path: `C:\ffmpeg\bin\ffmpeg.exe`, want: false},
		{name: "empty path", path: "", want: false},
		{name: "home assistant ingress", path: "/api/hassio_ingress/abc123token/usr/bin/ffmpeg", want: true},
		{name: "api prefix", path: "/api/foo/usr/bin/sox", want: true},
		{name: "ingress segment", path: "/ingress/abc/usr/bin/ffmpeg", want: true},
		{name: "proxy segment", path: "/proxy/host/usr/bin/ffmpeg", want: true},
		{name: "hassio segment", path: "/hassio/abc/usr/bin/ffmpeg", want: true},
		{name: "case insensitive", path: "/API/foo/ffmpeg", want: true},
		// A legitimate directory literally named "apidata" must not trip the
		// check: only full "/api/" style segments count.
		{name: "substring not a segment", path: "/opt/apidata/ffmpeg", want: false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			assert.Equal(t, tt.want, IsContaminatedToolPath(tt.path))
		})
	}
}

// TestValidateToolPath_RejectsContaminatedConfiguredPath verifies that a
// contaminated configured path is never returned as the validated path, even
// when os.Stat would succeed on it. Regression test for Home Assistant add-ons
// resolving ffmpeg to "/api/hassio_ingress/<token>/usr/bin/ffmpeg".
func TestValidateToolPath_RejectsContaminatedConfiguredPath(t *testing.T) {
	t.Parallel()

	// Create a real file whose path contains an ingress-style segment, so
	// os.Stat succeeds and only the contamination check can reject it. The real
	// contamination is a Home Assistant ingress URL prefix, which is always
	// forward-slash separated, so build the path with forward slashes (not
	// filepath.Join, which would emit backslashes on Windows and never match the
	// forward-slash contamination regex). Go's os package accepts forward slashes
	// on Windows, so MkdirAll/WriteFile/Stat still work cross-platform.
	base := filepath.ToSlash(t.TempDir())
	ingressDir := base + "/api/hassio_ingress/token"
	require.NoError(t, os.MkdirAll(ingressDir, 0o755))
	contaminated := ingressDir + "/ffmpeg"
	require.NoError(t, os.WriteFile(contaminated, []byte("#!/bin/sh\n"), 0o755))

	got, err := ValidateToolPath(contaminated, "nonexistent-tool-binary-xyz")

	// The contaminated path must never be returned. With no such tool in PATH,
	// validation falls through to an error rather than accepting the bad path.
	assert.NotEqual(t, contaminated, got, "must not accept contaminated path even though it exists on disk")
	require.Error(t, err)
	assert.Empty(t, got)
}
