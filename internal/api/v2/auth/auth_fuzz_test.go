// Package authapi fuzz/security tests for the auth domain input validators.
// These moved here verbatim from package api's fuzz_test.go when the auth domain
// was split into its own package; they validate the auth input handling against
// malformed and malicious inputs to keep the API hardened.
package authapi

import (
	"net/url"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
)

// =============================================================================
// Fuzz Tests for Auth Input Validation
// =============================================================================

// FuzzIsValidBasePath tests base path validation with random inputs.
// Goals:
// - No panics on any input
// - Consistent behavior (same input = same output)
// - Rejects dangerous patterns (traversal, XSS, CRLF)
func FuzzIsValidBasePath(f *testing.F) {
	seedInputs := []string{
		// Valid base paths
		"/",
		"/ui/",
		"/app/",
		"/admin/",
		"/dashboard/",
		"/some-path/",
		"/path_with_underscore/",
		"/Path123/",
		// Invalid - missing leading slash
		"ui/",
		"",
		"app",
		// Invalid - missing trailing slash
		"/ui",
		"/app",
		// Path traversal attempts
		"/../",
		"/ui/../",
		"/..%2f/",
		"/ui/../../",
		"/../../../etc/passwd/",
		// Protocol-relative URLs
		"//evil.com/",
		"///evil.com/",
		// XSS attempts
		"/ui/<script>/",
		"/ui/javascript:/",
		"/ui/data:/",
		"/<img%20onerror=alert(1)>/",
		// CRLF injection
		"/ui/\r\n/",
		"/ui/\n/",
		"/ui/\r/",
		"/ui/%0d%0a/",
		// Null bytes
		"/ui/\x00/",
		"/ui/%00/",
		// Backslashes
		"/ui\\/",
		"\\ui/",
		// Very long paths
		"/" + strings.Repeat("a", 200) + "/",
		// Unicode
		"/настройки/",
		"/日本語/",
		// Special characters
		"/path with spaces/",
		"/path%20encoded/",
		"/path+plus/",
	}

	for _, input := range seedInputs {
		f.Add(input)
	}

	f.Fuzz(func(t *testing.T, input string) {
		// Should never panic
		result := IsValidBasePath(input)

		// Invariants that must hold for valid paths
		if result {
			// Must start with /
			assert.True(t, strings.HasPrefix(input, "/"), "Valid base path must start with /")

			// Must end with /
			assert.True(t, strings.HasSuffix(input, "/"), "Valid base path must end with /")

			// Must not exceed max length
			assert.LessOrEqual(t, len(input), maxBasePathLength, "Valid base path must not exceed max length")

			// Must not contain dangerous patterns
			lower := strings.ToLower(input)
			assert.NotContains(t, input, "..", "Valid base path must not contain ..")
			assert.NotContains(t, input, "//", "Valid base path must not contain //")
			assert.NotContains(t, input, "\\", "Valid base path must not contain \\")
			assert.NotContains(t, input, "<", "Valid base path must not contain <")
			assert.NotContains(t, input, ">", "Valid base path must not contain >")
			assert.NotContains(t, lower, "javascript:", "Valid base path must not contain javascript:")
			assert.NotContains(t, lower, "data:", "Valid base path must not contain data:")
			assert.NotContains(t, input, "\n", "Valid base path must not contain newline")
			assert.NotContains(t, input, "\r", "Valid base path must not contain carriage return")
			assert.NotContains(t, input, "\x00", "Valid base path must not contain null byte")
		}

		// Consistency check
		result2 := IsValidBasePath(input)
		assert.Equal(t, result, result2, "Inconsistent results for: %q", input)
	})
}

// FuzzValidateAndSanitizeRedirect tests redirect path sanitization.
// Goals:
// - No panics
// - Always returns safe relative path
// - Rejects open redirect attempts
func FuzzValidateAndSanitizeRedirect(f *testing.F) {
	seedInputs := []string{
		// Valid redirects
		"/",
		"/dashboard",
		"/admin/settings",
		"/path?query=value",
		"/path?a=1&b=2",
		// Empty
		"",
		// Protocol-relative URLs (should be rejected)
		"//evil.com",
		"//evil.com/path",
		"///evil.com",
		// Absolute URLs (should be rejected)
		"http://evil.com",
		"https://evil.com/path",
		"javascript:alert(1)",
		"data:text/html,<script>alert(1)</script>",
		// Backslash attacks
		"/\\evil.com",
		"\\\\evil.com",
		"/path\\..\\..\\etc",
		// CRLF injection
		"/path\r\n",
		"/path%0d%0a",
		"/path\r\nSet-Cookie:evil=true",
		"/path%0d%0aLocation:http://evil.com",
		// Path traversal
		"/../etc/passwd",
		"/path/../../../etc/passwd",
		// Encoded attacks
		"/%2f%2fevil.com",
		"/%5c%5cevil.com",
		// Unicode attacks
		"/\u202epath",
		"/path\uFEFF",
		// Very long paths
		"/" + strings.Repeat("a", 10000),
		// Null bytes
		"/path\x00",
		"/path%00",
	}

	for _, input := range seedInputs {
		f.Add(input)
	}

	f.Fuzz(func(t *testing.T, input string) {
		// Should never panic
		result := ValidateAndSanitizeRedirect(input)

		// Result must always be a safe relative path
		assert.True(t, strings.HasPrefix(result, "/"), "Result must start with /")

		// Result must not be protocol-relative
		if len(result) > 1 {
			assert.NotEqual(t, '/', result[1], "Result must not be protocol-relative (//...)")
			assert.NotEqual(t, '\\', result[1], "Result must not start with /\\")
		}

		// Result must not contain raw CRLF
		assert.False(t, strings.ContainsAny(result, "\r\n"), "Result must not contain CRLF")

		// Result must be a valid relative URL
		parsed, err := url.Parse(result)
		if err == nil {
			assert.Empty(t, parsed.Scheme, "Result must not have a scheme")
			assert.Empty(t, parsed.Host, "Result must not have a host")
		}

		// Consistency check
		result2 := ValidateAndSanitizeRedirect(input)
		assert.Equal(t, result, result2, "Inconsistent results for: %q", input)
	})
}

// FuzzContainsCRLFCharacters tests CRLF detection in auth module.
func FuzzContainsCRLFCharacters(f *testing.F) {
	seedInputs := []string{
		// No CRLF
		"",
		"hello world",
		"/path/to/resource",
		"normal%20encoding",
		// Raw CRLF
		"hello\rworld",
		"hello\nworld",
		"hello\r\nworld",
		// Encoded CRLF
		"hello%0dworld",
		"hello%0aworld",
		"hello%0d%0aworld",
		"%0D%0A",
		// Mixed case encoding
		"%0D%0a",
		"%0d%0A",
		// Partial patterns
		"test%0test",
		"test%2test",
		"0d0a",
	}

	for _, input := range seedInputs {
		f.Add(input)
	}

	f.Fuzz(func(t *testing.T, input string) {
		// Should never panic
		result := ContainsCRLFCharacters(input)

		// If input contains raw CRLF, result must be true
		if strings.ContainsAny(input, "\r\n") {
			assert.True(t, result, "Must detect raw CRLF in: %q", input)
		}

		// If input contains encoded CRLF (case insensitive), result must be true
		lower := strings.ToLower(input)
		if strings.Contains(lower, "%0d") || strings.Contains(lower, "%0a") {
			assert.True(t, result, "Must detect encoded CRLF in: %q", input)
		}

		// Consistency check
		result2 := ContainsCRLFCharacters(input)
		assert.Equal(t, result, result2, "Inconsistent results")
	})
}

// FuzzIsValidRelativePath tests relative path validation.
func FuzzIsValidRelativePath(f *testing.F) {
	seedInputs := []string{
		// Valid relative paths
		"/",
		"/dashboard",
		"/admin/settings",
		"/path/to/resource",
		// Invalid - has scheme
		"http://example.com",
		"https://example.com/path",
		"javascript:alert(1)",
		"data:text/html",
		// Invalid - has host
		"//evil.com",
		"//evil.com/path",
		// Invalid - protocol-relative patterns
		"/\\evil.com",
		// Invalid - doesn't start with /
		"path",
		"",
		"dashboard",
	}

	for _, input := range seedInputs {
		f.Add(input)
	}

	f.Fuzz(func(t *testing.T, input string) {
		parsed, err := url.Parse(input)
		if err != nil {
			// Invalid URL, can't test
			return
		}

		// Should never panic
		result := isValidRelativePath(parsed)

		// If result is true, verify invariants
		if result {
			assert.Empty(t, parsed.Scheme, "Valid relative path must not have scheme")
			assert.Empty(t, parsed.Host, "Valid relative path must not have host")
			assert.True(t, strings.HasPrefix(parsed.Path, "/"), "Valid relative path must start with /")
			if len(parsed.Path) > 1 {
				assert.NotEqual(t, '/', parsed.Path[1], "Valid relative path must not start with //")
				assert.NotEqual(t, '\\', parsed.Path[1], "Valid relative path must not start with /\\")
			}
		}

		// Consistency check
		result2 := isValidRelativePath(parsed)
		assert.Equal(t, result, result2, "Inconsistent results")
	})
}

// FuzzExtractBasePathFromReferer tests referer parsing for base path extraction.
func FuzzExtractBasePathFromReferer(f *testing.F) {
	seedInputs := []string{
		// Valid referers
		"http://localhost/ui/login",
		"https://example.com/app/dashboard",
		"http://localhost:8080/admin/settings",
		// Invalid URLs
		"not-a-url",
		"",
		"   ",
		// Malicious referers
		"http://evil.com/ui/attack",
		"javascript:alert(1)",
		"data:text/html,<script>alert(1)</script>",
		// Path traversal
		"http://localhost/../../../etc/passwd",
		"http://localhost/ui/../../../etc/passwd",
		// CRLF injection
		"http://localhost/ui/%0d%0aSet-Cookie:evil=true",
		// Unicode
		"http://localhost/\u202e/",
		// Very long
		"http://localhost/" + strings.Repeat("a", 10000),
	}

	for _, input := range seedInputs {
		f.Add(input)
	}

	f.Fuzz(func(t *testing.T, input string) {
		// Should never panic
		result := extractBasePathFromReferer(input)

		// Result is either empty or a valid base path
		if result != "" {
			// Must be a valid base path if non-empty
			assert.True(t, IsValidBasePath(result), "Extracted base path must be valid: %q", result)
		}

		// Consistency check
		result2 := extractBasePathFromReferer(input)
		assert.Equal(t, result, result2, "Inconsistent results")
	})
}

// FuzzEnsurePathWithinBase tests path containment validation.
func FuzzEnsurePathWithinBase(f *testing.F) {
	type testCase struct {
		redirect string
		basePath string
	}

	seeds := []testCase{
		{"/dashboard", "/ui/"},
		{"/ui/settings", "/ui/"},
		{"/", "/app/"},
		{"//evil.com", "/ui/"},
		{"/\\evil.com", "/ui/"},
		{"/../etc/passwd", "/ui/"},
		{"", "/ui/"},
	}

	for _, s := range seeds {
		f.Add(s.redirect, s.basePath)
	}

	f.Fuzz(func(t *testing.T, redirect, basePath string) {
		// Should never panic
		result := ensurePathWithinBase(redirect, basePath)

		// Result should start with something
		assert.NotEmpty(t, result, "Result should not be empty")

		// If redirect was protocol-relative, result should be safe
		if strings.HasPrefix(redirect, "//") || strings.HasPrefix(redirect, "/\\") {
			// Should return base path for protocol-relative redirects
			assert.True(t, strings.HasPrefix(result, basePath) || result == basePath,
				"Protocol-relative redirect should use base path")
		}

		// Consistency check
		result2 := ensurePathWithinBase(redirect, basePath)
		assert.Equal(t, result, result2, "Inconsistent results")
	})
}

// TestAdvancedRedirectAttacks tests sophisticated open redirect attempts.
func TestAdvancedRedirectAttacks(t *testing.T) {
	t.Parallel()

	attacks := []struct {
		name           string
		redirect       string
		expectedPrefix string
		shouldBeRoot   bool
	}{
		// Protocol-relative variations
		{"Double slash", "//evil.com", "/", true},
		{"Triple slash", "///evil.com", "/", true},
		{"Backslash protocol-relative", "/\\evil.com", "/", true},
		{"Mixed slashes", "/\\/evil.com", "/", true},

		// Scheme attacks
		{"JavaScript scheme", "javascript:alert(1)", "/", true},
		{"Data scheme", "data:text/html,<script>alert(1)</script>", "/", true},
		{"VBScript scheme", "vbscript:msgbox(1)", "/", true},
		{"File scheme", "file:///etc/passwd", "/", true},

		// Domain confusion
		{"Domain with @", "//@evil.com", "/", true},

		// Whitespace attacks
		{"Tab before scheme", "\thttp://evil.com", "/", true},
		{"Newline before scheme", "\nhttp://evil.com", "/", true},

		// Valid redirects
		{"Simple path", "/dashboard", "/dashboard", false},
		{"Path with query", "/path?param=value", "/path?param=value", false},
		{"Root path", "/", "/", true},
	}

	for _, tc := range attacks {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			result := ValidateAndSanitizeRedirect(tc.redirect)

			if tc.shouldBeRoot {
				assert.Equal(t, "/", result, "Should be root for: %q", tc.redirect)
			} else {
				assert.True(t, strings.HasPrefix(result, tc.expectedPrefix),
					"Result %q should start with %q for input %q", result, tc.expectedPrefix, tc.redirect)
			}

			// All results must be safe relative paths
			assert.True(t, strings.HasPrefix(result, "/"), "Must start with /")
			if len(result) > 1 {
				assert.NotEqual(t, '/', result[1], "Must not be protocol-relative")
			}
		})
	}
}
