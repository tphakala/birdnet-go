// clip_path.go holds the audio-clip path normalization used to map database clip
// paths onto the media SecureFS root. It is shared substrate: the media handler
// serves clips/spectrograms by it, the detections handler deletes a detection's
// files by it, and the fuzz/security tests exercise it, so it lives on apicore so
// no domain package re-implements the path-traversal defenses.
package apicore

import (
	"path"
	"path/filepath"
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
//   - "clips/2024/01/bird.wav" -> ("2024/01/bird.wav", true)
//   - "2024/01/bird.wav" -> ("2024/01/bird.wav", true) (unchanged)
//   - "clips/" -> ("", true) (intentionally empty)
//   - "../etc/passwd" -> ("", false) (invalid traversal)
//   - "/absolute/path" -> ("", false) (invalid absolute path)
func NormalizeClipPathStrict(p, clipsPrefix string) (string, bool) {
	clipsPrefix = normalizeClipsPrefix(clipsPrefix)
	p = strings.ReplaceAll(p, "\\", "/")
	p = stripClipsPrefix(p, clipsPrefix)
	p = path.Clean(p)

	// Handle the special case where Clean returns "."
	if p == "." {
		return "", true
	}

	// Reject paths that would escape the SecureFS root
	if isUnsafePath(p) {
		return "", false
	}

	return p, true
}

// normalizeClipsPrefix ensures the clips prefix is properly formatted
func normalizeClipsPrefix(prefix string) string {
	if prefix == "" {
		return "clips/"
	}
	if !strings.HasSuffix(prefix, "/") {
		return prefix + "/"
	}
	return prefix
}

// stripClipsPrefix attempts to remove the clips prefix using multiple strategies
func stripClipsPrefix(p, clipsPrefix string) string {
	// Strategy 1: Try the configured prefix as-is
	if trimmed, ok := strings.CutPrefix(p, clipsPrefix); ok {
		return trimmed
	}

	// Strategy 2: Try the basename of the configured path + "/"
	baseName := path.Base(strings.TrimSuffix(clipsPrefix, "/"))
	if baseName != "" && baseName != "." && baseName != "/" {
		basePrefix := baseName + "/"
		if trimmed, ok := strings.CutPrefix(p, basePrefix); ok {
			return trimmed
		}
	}

	// Strategy 3: Try literal "clips/" as fallback (case-insensitive)
	if strings.HasPrefix(strings.ToLower(p), "clips/") {
		return p[6:]
	}

	return p
}

// dangerousEncodedPatterns contains URL-encoded patterns that indicate path traversal attempts.
// These patterns are checked against the lowercased path.
var dangerousEncodedPatterns = []string{
	// Encoded null bytes
	"%00", "%2500",
	// URL-encoded path traversal: %2e = '.'
	"%2e%2e", "%2e.", ".%2e",
	// Double-encoded: %25 = '%', so %252e = '%2e' after one decode
	"%252e",
	// Triple-encoded (defense in depth)
	"%25252e",
	// Encoded slashes that could create traversal after decoding
	"%2f", "%252f",
	// Encoded backslashes for Windows-style traversal
	"%5c", "%255c",
}

// containsDangerousEncodedPattern checks if a lowercased path contains any dangerous URL-encoded patterns.
func containsDangerousEncodedPattern(lower string) bool {
	for _, pattern := range dangerousEncodedPatterns {
		if strings.Contains(lower, pattern) {
			return true
		}
	}
	return false
}

// minWindowsDrivePathLen is the minimum length for a Windows drive path (e.g., "C:").
const minWindowsDrivePathLen = 2

// isWindowsAbsolutePath checks for Windows-style absolute paths (e.g., C:, D:).
func isWindowsAbsolutePath(p string) bool {
	if len(p) < minWindowsDrivePathLen {
		return false
	}
	firstChar := p[0]
	return ((firstChar >= 'A' && firstChar <= 'Z') || (firstChar >= 'a' && firstChar <= 'z')) && p[1] == ':'
}

// isUnsafePath checks if a path would escape the SecureFS root.
// Uses explicit ".." check for URL/HTTP paths (which are not cleaned by the OS),
// plus filepath.IsLocal for platform-specific validation.
// Also checks for URL-encoded attacks and null bytes.
func isUnsafePath(p string) bool {
	// Check for null bytes - do this first as they can bypass other checks
	if strings.Contains(p, "\x00") {
		return true
	}

	// Check for Windows-style absolute paths on any platform
	if isWindowsAbsolutePath(p) {
		return true
	}

	// Check for dangerous URL-encoded patterns
	if containsDangerousEncodedPattern(strings.ToLower(p)) {
		return true
	}

	// Explicit ".." check needed for URL paths since filepath.IsLocal cleans paths internally.
	// For HTTP/URL contexts, "path/../etc" is dangerous even though it cleans to "etc".
	// This check must come BEFORE filepath.IsLocal to catch patterns like "..../file" which
	// contain ".." as a substring but aren't traversal after cleaning.
	//nolint:gocritic // URL path context requires explicit ".." check; filepath.IsLocal would clean "path/../etc" to "etc" (valid)
	if strings.Contains(p, "..") {
		return true
	}

	// filepath.IsLocal provides comprehensive platform-specific validation for:
	// - Absolute paths, empty paths
	// - Windows reserved names (NUL, COM1, LPT1, etc.)
	// - Windows \??\ prefix attacks (CVE-2023-45283)
	// - Space-padded reserved names (CVE-2023-45284)
	// Note: The ".." check is intentionally above, not relying on IsLocal's ".." detection
	return !filepath.IsLocal(p)
}

// NormalizeClipPath normalizes audio clip paths for use with SecureFS.
// The database stores paths with a configurable prefix (default "clips/"),
// but SecureFS is already rooted at the clips directory, so we need to strip this prefix.
//
// This is the backward-compatible version that returns only the string result.
// Returns empty string for both invalid paths and intentionally empty normalized paths.
//
// Examples:
//   - "clips/2024/01/bird.wav" -> "2024/01/bird.wav"
//   - "2024/01/bird.wav" -> "2024/01/bird.wav" (unchanged)
//   - "clips/" -> "" (empty string)
//   - "../etc/passwd" -> "" (invalid path)
func NormalizeClipPath(p, clipsPrefix string) string {
	normalized, _ := NormalizeClipPathStrict(p, clipsPrefix)
	return normalized
}
