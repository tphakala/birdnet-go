package api

import (
	"path"
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
func NormalizeClipPath(p, clipsPrefix string) string {
	// If no prefix is configured, default to "clips/"
	if clipsPrefix == "" {
		clipsPrefix = "clips/"
	}

	// Ensure the prefix ends with a separator for proper matching
	if !strings.HasSuffix(clipsPrefix, "/") {
		clipsPrefix += "/"
	}

	// Strip the prefix if present using strings.CutPrefix (more efficient)
	if trimmed, ok := strings.CutPrefix(p, clipsPrefix); ok {
		p = trimmed
	}

	// Normalize and defend against traversal
	// Convert backslashes to forward slashes for consistency
	p = path.Clean(strings.ReplaceAll(p, "\\", "/"))

	// Handle the special case where Clean returns "."
	if p == "." {
		return ""
	}

	// Reject paths that would escape the SecureFS root
	if path.IsAbs(p) || strings.HasPrefix(p, "../") {
		// Return empty string for invalid paths
		return ""
	}

	return p
}
