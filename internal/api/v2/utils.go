package api

import (
	"path"
	"strings"
)

// NormalizeClipPathStrict normalizes the audio clip path by removing the clips prefix if present.
// This is the strict variant that returns a boolean to distinguish between invalid paths and
// intentionally empty normalized paths.
//
// Returns:
//   - (normalizedPath, true) for valid results
//   - ("", false) for invalid/unsafe inputs (absolute paths, traversal, etc.)
//
// Examples:
//   - "clips/2024/01/bird.wav" → ("2024/01/bird.wav", true)
//   - "2024/01/bird.wav" → ("2024/01/bird.wav", true) (unchanged)
//   - "clips/" → ("", true) (intentionally empty)
//   - "../etc/passwd" → ("", false) (invalid traversal)
//   - "/absolute/path" → ("", false) (invalid absolute path)
func NormalizeClipPathStrict(p, clipsPrefix string) (string, bool) {
	// If no prefix is configured, default to "clips/"
	if clipsPrefix == "" {
		clipsPrefix = "clips/"
	}

	// Ensure the prefix ends with a separator for proper matching
	if !strings.HasSuffix(clipsPrefix, "/") {
		clipsPrefix += "/"
	}

	// Normalize slashes first for consistent processing
	p = strings.ReplaceAll(p, "\\", "/")

	// Try multiple prefix stripping strategies in order
	// 1. Try the configured prefix as-is
	if trimmed, ok := strings.CutPrefix(p, clipsPrefix); ok {
		p = trimmed
	} else {
		// 2. Try the basename of the configured path + "/"
		baseName := path.Base(strings.TrimSuffix(clipsPrefix, "/"))
		if baseName != "" && baseName != "." && baseName != "/" {
			basePrefix := baseName + "/"
			if trimmed, ok := strings.CutPrefix(p, basePrefix); ok {
				p = trimmed
			} else {
				// 3. Try literal "clips/" as fallback (case-insensitive)
				lowerPath := strings.ToLower(p)
				if strings.HasPrefix(lowerPath, "clips/") {
					p = p[6:] // Remove "clips/" prefix
				}
			}
		}
	}

	// Apply path.Clean for normalization
	p = path.Clean(p)

	// Handle the special case where Clean returns "."
	if p == "." {
		return "", true // Intentionally empty, but valid
	}

	// Reject paths that would escape the SecureFS root
	// Check for absolute paths, parent directory traversal ("../..."), and bare parent reference ("..")
	if path.IsAbs(p) || strings.HasPrefix(p, "../") || p == ".." {
		return "", false // Invalid path
	}

	return p, true
}

// NormalizeClipPath normalizes audio clip paths for use with SecureFS.
// The database stores paths with a configurable prefix (default "clips/"),
// but SecureFS is already rooted at the clips directory, so we need to strip this prefix.
//
// This is the backward-compatible version that returns only the string result.
// Returns empty string for both invalid paths and intentionally empty normalized paths.
//
// Examples:
//   - "clips/2024/01/bird.wav" → "2024/01/bird.wav"
//   - "2024/01/bird.wav" → "2024/01/bird.wav" (unchanged)
//   - "clips/" → "" (empty string)
//   - "../etc/passwd" → "" (invalid path)
func NormalizeClipPath(p, clipsPrefix string) string {
	normalized, _ := NormalizeClipPathStrict(p, clipsPrefix)
	return normalized
}
