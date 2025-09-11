package api

import (
	"strings"
)

// NormalizeClipPath normalizes audio clip paths for use with SecureFS.
// The database stores paths with a configurable prefix (default "clips/"),
// but SecureFS is already rooted at the clips directory, so we need to strip this prefix.
//
// Examples:
//   - "clips/2024/01/bird.wav" → "2024/01/bird.wav"
//   - "2024/01/bird.wav" → "2024/01/bird.wav" (unchanged)
//   - "clips/" → "" (empty string)
func NormalizeClipPath(path, clipsPrefix string) string {
	// If no prefix is configured, default to "clips/"
	if clipsPrefix == "" {
		clipsPrefix = "clips/"
	}

	// Ensure the prefix ends with a separator for proper matching
	if !strings.HasSuffix(clipsPrefix, "/") {
		clipsPrefix += "/"
	}

	// Strip the prefix if present
	if strings.HasPrefix(path, clipsPrefix) {
		return strings.TrimPrefix(path, clipsPrefix)
	}

	return path
}
