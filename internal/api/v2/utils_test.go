// utils_test.go: Package api provides tests for utility functions.

package api

import (
	"testing"

	"github.com/stretchr/testify/assert"
)

// TestNormalizeClipPathStrict tests the NormalizeClipPathStrict function
func TestNormalizeClipPathStrict(t *testing.T) {
	t.Parallel()
	t.Attr("component", "utils")
	t.Attr("type", "unit")
	t.Attr("feature", "path-normalization")

	tests := []struct {
		name         string
		path         string
		clipsPrefix  string
		expectedPath string
		expectedOK   bool
	}{
		// Basic prefix stripping
		{
			name:         "Strip clips prefix",
			path:         "clips/2024/01/bird.wav",
			clipsPrefix:  "clips/",
			expectedPath: "2024/01/bird.wav",
			expectedOK:   true,
		},
		{
			name:         "Path without clips prefix unchanged",
			path:         "2024/01/bird.wav",
			clipsPrefix:  "clips/",
			expectedPath: "2024/01/bird.wav",
			expectedOK:   true,
		},
		{
			name:         "Just clips prefix returns empty valid",
			path:         "clips/",
			clipsPrefix:  "clips/",
			expectedPath: "",
			expectedOK:   true,
		},

		// Custom prefix handling
		{
			name:         "Custom prefix without trailing slash",
			path:         "audio/2024/01/bird.wav",
			clipsPrefix:  "audio",
			expectedPath: "2024/01/bird.wav",
			expectedOK:   true,
		},
		{
			name:         "Custom prefix with trailing slash",
			path:         "audio/2024/01/bird.wav",
			clipsPrefix:  "audio/",
			expectedPath: "2024/01/bird.wav",
			expectedOK:   true,
		},
		{
			name:         "Deep custom prefix",
			path:         "/var/audio/clips/2024/01/bird.wav",
			clipsPrefix:  "/var/audio/clips/",
			expectedPath: "2024/01/bird.wav",
			expectedOK:   true,
		},

		// Empty prefix defaults to clips/
		{
			name:         "Empty prefix defaults to clips",
			path:         "clips/2024/01/bird.wav",
			clipsPrefix:  "",
			expectedPath: "2024/01/bird.wav",
			expectedOK:   true,
		},

		// Path traversal attacks - MUST return false
		{
			name:         "Path traversal with ../",
			path:         "../etc/passwd",
			clipsPrefix:  "clips/",
			expectedPath: "",
			expectedOK:   false,
		},
		{
			name:         "Path traversal with clips/../",
			path:         "clips/../etc/passwd",
			clipsPrefix:  "clips/",
			expectedPath: "",
			expectedOK:   false,
		},
		{
			name:         "Path traversal with multiple ../",
			path:         "../../etc/passwd",
			clipsPrefix:  "clips/",
			expectedPath: "",
			expectedOK:   false,
		},
		{
			name:         "Path traversal hidden in path",
			path:         "clips/2024/../../../etc/passwd",
			clipsPrefix:  "clips/",
			expectedPath: "",
			expectedOK:   false,
		},

		// Absolute paths - MUST return false
		{
			name:         "Absolute path Unix",
			path:         "/etc/passwd",
			clipsPrefix:  "clips/",
			expectedPath: "",
			expectedOK:   false,
		},
		{
			name:         "Absolute path in result after stripping",
			path:         "clips//etc/passwd",
			clipsPrefix:  "clips/",
			expectedPath: "",
			expectedOK:   false,
		},

		// Windows path handling
		{
			name:         "Windows backslash converted",
			path:         "clips\\2024\\01\\bird.wav",
			clipsPrefix:  "clips/",
			expectedPath: "2024/01/bird.wav",
			expectedOK:   true,
		},
		{
			name:         "Mixed slashes",
			path:         "clips/2024\\01/bird.wav",
			clipsPrefix:  "clips/",
			expectedPath: "2024/01/bird.wav",
			expectedOK:   true,
		},

		// Case sensitivity
		{
			name:         "Case insensitive clips prefix",
			path:         "Clips/2024/01/bird.wav",
			clipsPrefix:  "clips/",
			expectedPath: "2024/01/bird.wav",
			expectedOK:   true,
		},
		{
			name:         "Uppercase CLIPS prefix",
			path:         "CLIPS/2024/01/bird.wav",
			clipsPrefix:  "clips/",
			expectedPath: "2024/01/bird.wav",
			expectedOK:   true,
		},

		// Edge cases
		{
			name:         "Empty path",
			path:         "",
			clipsPrefix:  "clips/",
			expectedPath: "",
			expectedOK:   true,
		},
		{
			name:         "Single dot",
			path:         ".",
			clipsPrefix:  "clips/",
			expectedPath: "",
			expectedOK:   true,
		},
		{
			name:         "Double slashes after prefix creates absolute path - rejected",
			path:         "clips//2024//01//bird.wav",
			clipsPrefix:  "clips/",
			expectedPath: "",
			expectedOK:   false, // After stripping "clips/", remaining "/2024/..." is absolute
		},
		{
			name:         "Trailing slash in path",
			path:         "clips/2024/01/",
			clipsPrefix:  "clips/",
			expectedPath: "2024/01",
			expectedOK:   true,
		},

		// File with special characters (valid)
		{
			name:         "File with spaces",
			path:         "clips/2024/01/bird song.wav",
			clipsPrefix:  "clips/",
			expectedPath: "2024/01/bird song.wav",
			expectedOK:   true,
		},
		{
			name:         "File with unicode",
			path:         "clips/2024/01/鳥.wav",
			clipsPrefix:  "clips/",
			expectedPath: "2024/01/鳥.wav",
			expectedOK:   true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			result, ok := NormalizeClipPathStrict(tt.path, tt.clipsPrefix)
			assert.Equal(t, tt.expectedOK, ok, "Validity flag mismatch for path %q", tt.path)
			if tt.expectedOK {
				assert.Equal(t, tt.expectedPath, result, "Normalized path mismatch")
			}
		})
	}
}

// TestNormalizeClipPath tests the backward-compatible NormalizeClipPath wrapper
func TestNormalizeClipPath(t *testing.T) {
	t.Parallel()
	t.Attr("component", "utils")
	t.Attr("type", "unit")
	t.Attr("feature", "path-normalization")

	tests := []struct {
		name         string
		path         string
		clipsPrefix  string
		expectedPath string
	}{
		{
			name:         "Valid path returns normalized",
			path:         "clips/2024/01/bird.wav",
			clipsPrefix:  "clips/",
			expectedPath: "2024/01/bird.wav",
		},
		{
			name:         "Invalid path returns empty",
			path:         "../etc/passwd",
			clipsPrefix:  "clips/",
			expectedPath: "",
		},
		{
			name:         "Empty clips prefix returns empty",
			path:         "clips/",
			clipsPrefix:  "clips/",
			expectedPath: "",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			result := NormalizeClipPath(tt.path, tt.clipsPrefix)
			assert.Equal(t, tt.expectedPath, result)
		})
	}
}

// TestNormalizeClipPathStrict_SecurityCritical tests security-critical paths
// These tests verify the function properly rejects path traversal attempts
func TestNormalizeClipPathStrict_SecurityCritical(t *testing.T) {
	t.Parallel()
	t.Attr("component", "utils")
	t.Attr("type", "security")
	t.Attr("feature", "path-traversal-prevention")

	// These paths MUST be rejected to prevent directory traversal
	mustRejectPaths := []struct {
		path   string
		reason string
	}{
		{"../etc/passwd", "parent directory traversal"},
		{"..\\etc\\passwd", "backslash traversal"},
		{"clips/../../../etc/passwd", "traversal through clips prefix"},
		{"clips/foo/../../..", "traversal to root"},
		{"/etc/passwd", "absolute path"},
		{"/root/.ssh/id_rsa", "absolute path to sensitive file"},
		{"clips/..\\..\\etc\\passwd", "mixed slash traversal"},
	}

	for _, tc := range mustRejectPaths {
		t.Run(tc.path, func(t *testing.T) {
			t.Parallel()
			result, ok := NormalizeClipPathStrict(tc.path, "clips/")
			assert.False(t, ok, "Path %q should be rejected (%s)", tc.path, tc.reason)
			if ok {
				assert.Failf(t, "SECURITY", "Path %q was accepted and normalized to %q", tc.path, result)
			}
		})
	}
}

// TestNormalizeClipPathStrict_EdgeCases tests edge cases and expected behaviors
func TestNormalizeClipPathStrict_EdgeCases(t *testing.T) {
	t.Parallel()
	t.Attr("component", "utils")
	t.Attr("type", "unit")
	t.Attr("feature", "edge-cases")

	// Bare parent directory reference must be rejected (fixed bug)
	t.Run("Bare parent directory reference rejected", func(t *testing.T) {
		t.Parallel()
		_, ok := NormalizeClipPathStrict("../", "clips/")
		assert.False(t, ok, "'../' should be rejected as path traversal")
	})

	// URL-encoded paths with potential traversal patterns are rejected for security
	// Even though %2f is not a filesystem slash, it could be decoded later in the
	// processing chain, making these paths dangerous. Defense in depth requires rejection.
	t.Run("URL-encoded traversal patterns rejected", func(t *testing.T) {
		t.Parallel()
		_, ok := NormalizeClipPathStrict("..%2f..%2fetc/passwd", "clips/")
		assert.False(t, ok, "URL-encoded paths with '..' should be rejected for security")
	})

	// Paths with URL-encoded slashes are rejected as they could enable traversal after decoding
	t.Run("URL-encoded slashes rejected", func(t *testing.T) {
		t.Parallel()
		_, ok := NormalizeClipPathStrict("path%2fto%2ffile.wav", "clips/")
		assert.False(t, ok, "Paths with encoded slashes should be rejected for security")
	})

	// Paths with multiple dots that look like traversal are rejected
	t.Run("Paths starting with double dots rejected", func(t *testing.T) {
		t.Parallel()
		_, ok := NormalizeClipPathStrict("..../test.wav", "clips/")
		assert.False(t, ok, "Paths containing '..' anywhere should be rejected")
	})

	// Valid paths with dots in filenames are still accepted
	t.Run("Dots in filenames are valid", func(t *testing.T) {
		t.Parallel()
		result, ok := NormalizeClipPathStrict("file.name.with.dots.wav", "clips/")
		assert.True(t, ok, "Dots in filenames are valid")
		assert.Equal(t, "file.name.with.dots.wav", result)
	})
}
