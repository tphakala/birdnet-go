package security

import (
	"errors"
	"fmt"
	"net/url"
	"path"
	"strings"
)

// normalizePort returns the port string, substituting the default port if empty.
func normalizePort(scheme, port string) string {
	if port == "" {
		switch scheme {
		case "https":
			return "443"
		case "http":
			return "80"
		}
	}
	return port
}

// ValidateRedirectURI parses the provided URI string and compares its essential components
// (Scheme, Hostname, normalized Port, Path) against the pre-parsed expected URI.
// It returns an error if the URIs do not match or if parsing fails.
func ValidateRedirectURI(providedURIString string, expectedURI *url.URL) error {
	if expectedURI == nil {
		// This indicates a configuration error occurred during startup where the expected
		// URI could not be parsed. Fail validation immediately.
		return fmt.Errorf("internal configuration error: expected redirect URI was not successfully parsed at startup")
	}

	parsedProvidedURI, err := url.Parse(providedURIString)
	if err != nil {
		return fmt.Errorf("invalid redirect_uri format: %w", err)
	}

	// Unescape paths before cleaning to handle potential percent-encoding differences
	providedPathUnescaped, err := url.PathUnescape(parsedProvidedURI.Path)
	if err != nil {
		// Should be unlikely if url.Parse succeeded, but handle defensively
		return fmt.Errorf("failed to unescape provided redirect path '%s': %w", parsedProvidedURI.Path, err)
	}
	expectedPathUnescaped, err := url.PathUnescape(expectedURI.Path)
	if err != nil {
		// This indicates an issue with the pre-parsed expected URI
		return fmt.Errorf("internal configuration error: failed to unescape expected redirect path '%s': %w", expectedURI.Path, err)
	}

	// Normalize paths: Use path.Clean on unescaped paths and trim trailing slash for consistent comparison
	// Note: path.Clean removes trailing slashes unless the path is "/"
	providedPath := path.Clean(providedPathUnescaped)
	expectedPath := path.Clean(expectedPathUnescaped)

	// Normalize ports
	providedPort := normalizePort(parsedProvidedURI.Scheme, parsedProvidedURI.Port())
	expectedPort := normalizePort(expectedURI.Scheme, expectedURI.Port())

	// Compare Scheme (case-insensitive), Hostname (case-insensitive), normalized Port, Path
	// RFC 3986 Section 3.1: Scheme names are case-insensitive.
	// RFC 3986 Section 3.2.2: Host names are case-insensitive.
	if !strings.EqualFold(parsedProvidedURI.Scheme, expectedURI.Scheme) ||
		!strings.EqualFold(parsedProvidedURI.Hostname(), expectedURI.Hostname()) || // Compare Hostname case-insensitively
		providedPort != expectedPort || // Compare normalized ports
		providedPath != expectedPath || // Compare cleaned paths
		parsedProvidedURI.RawQuery != "" || // Ensure provided URI has no query parameters
		parsedProvidedURI.Fragment != "" { // Ensure provided URI has no fragment

		// Construct a clearer error message showing the difference
		// Redacted the full providedURIString from the main error message to prevent potential info leak.
		// Details are still available if needed for debugging from the parsed components.
		mismatchDetail := fmt.Sprintf("provided URI parsed components (Scheme=%s, Hostname=%s, Port=%s, Path=%s, Query=%s, Fragment=%s)"+
			" do not match expected base URI (Scheme=%s, Hostname=%s, Port=%s, Path=%s) or contain disallowed query/fragment components",
			parsedProvidedURI.Scheme, parsedProvidedURI.Hostname(), providedPort, providedPath, parsedProvidedURI.RawQuery, parsedProvidedURI.Fragment,
			expectedURI.Scheme, expectedURI.Hostname(), expectedPort, expectedPath)

		// Construct the detailed error message first
		mismatchErr := errors.New(mismatchDetail) // Use errors.New for a simple error string

		// Wrap the detailed error using a constant format string
		return fmt.Errorf("invalid redirect_uri: %w", mismatchErr)
	}

	return nil // URIs match
}

// IsSafePath ensures the given path is internal and doesn't contain potentially harmful sequences.
// It checks for:
// - Path starting with '/'
// - No double slashes '//' or backslashes '\\'
// - No protocol specifiers '://'
// - No directory traversal '..'
// - No null bytes '\x00'
// - Reasonable length limit
func IsSafePath(pathStr string) bool {
	return strings.HasPrefix(pathStr, "/") &&
		!strings.Contains(pathStr, "//") &&
		!strings.Contains(pathStr, "\\") &&
		!strings.Contains(pathStr, "://") &&
		!strings.Contains(pathStr, "..") &&
		!strings.Contains(pathStr, "\x00") &&
		len(pathStr) < 512 // Prevent excessively long paths
}

// IsValidRedirect ensures the redirect path is safe and internal by checking IsSafePath.
// It logs a warning if the path is deemed unsafe.
// Note: Consider where the logger should come from if needed outside httpcontroller.
// For now, it logs using the standard log package if unsafe.
func IsValidRedirect(redirectPath string) bool {
	isSafe := IsSafePath(redirectPath) // Use the exported function
	if !isSafe {
		// Log potentially unsafe redirect attempt using the security logger
		LogWarn("Invalid or potentially unsafe redirect path detected", "path", redirectPath)
	}
	return isSafe
}

// ValidateAuthCallbackRedirect validates and sanitizes a redirect path for auth callbacks.
// It returns a safe redirect path, defaulting to "/" if validation fails.
// This function handles:
// - Empty redirects (returns default)
// - Protocol-relative URLs (//evil.com)
// - Absolute URLs (https://evil.com)
// - Backslash-relative URLs (/\evil.com)
// - CRLF injection attempts
// - Preserving query parameters for valid relative paths
func ValidateAuthCallbackRedirect(redirect string) string {
	const defaultRedirect = "/"

	if redirect == "" {
		return defaultRedirect
	}

	// Replace ALL backslashes with forward slashes for robust normalization
	cleanedRedirect := strings.ReplaceAll(redirect, "\\", "/")
	parsedURL, err := url.Parse(cleanedRedirect)

	// Validate the parsed URL
	if err != nil || !isValidRelativePath(parsedURL) {
		return defaultRedirect
	}

	// Check for CRLF injection
	if containsCRLF(parsedURL.Path) || containsCRLF(parsedURL.RawQuery) {
		return defaultRedirect
	}

	// Construct safe redirect preserving path and query
	safeRedirect := parsedURL.Path
	if parsedURL.RawQuery != "" {
		safeRedirect += "?" + parsedURL.RawQuery
	}

	return safeRedirect
}

// isValidRelativePath checks if a URL is a valid relative path for redirects.
// Valid paths must:
// - Have no scheme (not http://, https://, etc.)
// - Have no host (not //evil.com)
// - Start with a single '/' (valid relative path)
// - NOT start with '//' or '/\' (protocol-relative URLs)
func isValidRelativePath(parsedURL *url.URL) bool {
	if parsedURL.Scheme != "" || parsedURL.Host != "" {
		return false
	}

	if !strings.HasPrefix(parsedURL.Path, "/") {
		return false
	}

	// Reject "//" and "/\" patterns at the start
	if len(parsedURL.Path) > 1 && (parsedURL.Path[1] == '/' || parsedURL.Path[1] == '\\') {
		return false
	}

	return true
}

// containsCRLF checks if a string contains CR/LF characters or their percent-encoded forms.
func containsCRLF(s string) bool {
	if strings.ContainsAny(s, "\r\n") {
		return true
	}
	lower := strings.ToLower(s)
	return strings.Contains(lower, "%0d") || strings.Contains(lower, "%0a")
}
