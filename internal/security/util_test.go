package security

import (
	"net/url"
	"testing"

	"github.com/stretchr/testify/assert"
)

// TestValidateAuthCallbackRedirect tests the redirect validation for auth callbacks
func TestValidateAuthCallbackRedirect(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name     string
		redirect string
		expected string
	}{
		// Empty and default cases
		{
			name:     "empty redirect returns default",
			redirect: "",
			expected: "/",
		},
		{
			name:     "root path is valid",
			redirect: "/",
			expected: "/",
		},

		// Valid relative paths
		{
			name:     "simple relative path",
			redirect: "/dashboard",
			expected: "/dashboard",
		},
		{
			name:     "nested relative path",
			redirect: "/admin/settings",
			expected: "/admin/settings",
		},
		{
			name:     "path with query parameters",
			redirect: "/dashboard?tab=settings",
			expected: "/dashboard?tab=settings",
		},
		{
			name:     "path with multiple query parameters",
			redirect: "/dashboard?tab=settings&view=all&sort=name",
			expected: "/dashboard?tab=settings&view=all&sort=name",
		},

		// Protocol-relative URL attacks
		{
			name:     "protocol-relative URL rejected",
			redirect: "//evil.com",
			expected: "/",
		},
		{
			name:     "protocol-relative URL with path rejected",
			redirect: "//evil.com/attack",
			expected: "/",
		},
		{
			name:     "protocol-relative URL with query rejected",
			redirect: "//evil.com?steal=cookies",
			expected: "/",
		},

		// Absolute URL attacks
		{
			name:     "http absolute URL rejected",
			redirect: "http://evil.com",
			expected: "/",
		},
		{
			name:     "https absolute URL rejected",
			redirect: "https://evil.com/attack",
			expected: "/",
		},
		{
			name:     "javascript URL rejected",
			redirect: "javascript:alert(1)",
			expected: "/",
		},
		{
			name:     "data URL rejected",
			redirect: "data:text/html,<script>alert(1)</script>",
			expected: "/",
		},

		// Backslash attacks
		{
			name:     "backslash-relative URL rejected",
			redirect: "/\\evil.com",
			expected: "/",
		},
		{
			name:     "double backslash rejected",
			redirect: "\\\\evil.com",
			expected: "/",
		},
		{
			name:     "mixed slashes rejected",
			redirect: "/\\/evil.com",
			expected: "/",
		},

		// CRLF injection attacks
		{
			name:     "CRLF in path rejected",
			redirect: "/dashboard\r\nSet-Cookie:evil=true",
			expected: "/",
		},
		{
			name:     "percent-encoded CRLF rejected",
			redirect: "/dashboard%0d%0aSet-Cookie:evil=true",
			expected: "/",
		},
		{
			name:     "only CR rejected",
			redirect: "/dashboard%0dSet-Cookie:evil=true",
			expected: "/",
		},
		{
			name:     "only LF rejected",
			redirect: "/dashboard%0aSet-Cookie:evil=true",
			expected: "/",
		},
		{
			name:     "uppercase percent-encoded CRLF rejected",
			redirect: "/dashboard%0D%0ASet-Cookie:evil=true",
			expected: "/",
		},
		{
			name:     "CRLF in query string rejected",
			redirect: "/dashboard?param=%0d%0aSet-Cookie:evil=true",
			expected: "/",
		},

		// Edge cases
		{
			name:     "path without leading slash rejected",
			redirect: "dashboard",
			expected: "/",
		},
		{
			name:     "fragment is stripped (not included in result)",
			redirect: "/dashboard#section",
			expected: "/dashboard",
		},
		{
			name:     "unicode path allowed",
			redirect: "/настройки",
			expected: "/настройки",
		},
		{
			name:     "percent-encoded path allowed",
			redirect: "/path%20with%20spaces",
			expected: "/path with spaces", // url.Parse decodes percent-encoded characters in Path
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			result := ValidateAuthCallbackRedirect(tt.redirect)
			assert.Equal(t, tt.expected, result)
		})
	}
}

// TestIsValidRelativePath tests the relative path validation helper
func TestIsValidRelativePath(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name     string
		url      string
		expected bool
	}{
		// Valid relative paths
		{
			name:     "simple relative path",
			url:      "/dashboard",
			expected: true,
		},
		{
			name:     "root path",
			url:      "/",
			expected: true,
		},
		{
			name:     "nested path",
			url:      "/admin/settings/users",
			expected: true,
		},
		{
			name:     "path with query",
			url:      "/dashboard?tab=settings",
			expected: true,
		},

		// Invalid: has scheme
		{
			name:     "http scheme rejected",
			url:      "http://example.com/path",
			expected: false,
		},
		{
			name:     "https scheme rejected",
			url:      "https://example.com/path",
			expected: false,
		},

		// Invalid: has host (protocol-relative)
		{
			name:     "protocol-relative URL rejected",
			url:      "//evil.com/path",
			expected: false,
		},

		// Invalid: doesn't start with /
		{
			name:     "no leading slash rejected",
			url:      "dashboard",
			expected: false,
		},
		{
			name:     "empty path rejected",
			url:      "",
			expected: false,
		},

		// Invalid: starts with // or /\
		{
			name:     "double slash start rejected",
			url:      "//path",
			expected: false,
		},
		{
			name:     "slash-backslash start rejected",
			url:      "/\\path",
			expected: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			parsedURL, err := url.Parse(tt.url)
			if err != nil {
				// If URL can't be parsed, it should be invalid
				assert.False(t, tt.expected, "unparseable URL should be expected to fail")
				return
			}
			result := isValidRelativePath(parsedURL)
			assert.Equal(t, tt.expected, result)
		})
	}
}

// TestContainsCRLF tests the CRLF detection helper
func TestContainsCRLF(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name     string
		input    string
		expected bool
	}{
		// No CRLF
		{
			name:     "empty string",
			input:    "",
			expected: false,
		},
		{
			name:     "normal string",
			input:    "hello world",
			expected: false,
		},
		{
			name:     "path string",
			input:    "/dashboard?tab=settings",
			expected: false,
		},

		// Raw CRLF characters
		{
			name:     "contains CR",
			input:    "hello\rworld",
			expected: true,
		},
		{
			name:     "contains LF",
			input:    "hello\nworld",
			expected: true,
		},
		{
			name:     "contains CRLF",
			input:    "hello\r\nworld",
			expected: true,
		},

		// Percent-encoded CRLF (lowercase)
		{
			name:     "percent-encoded CR lowercase",
			input:    "hello%0dworld",
			expected: true,
		},
		{
			name:     "percent-encoded LF lowercase",
			input:    "hello%0aworld",
			expected: true,
		},
		{
			name:     "percent-encoded CRLF lowercase",
			input:    "hello%0d%0aworld",
			expected: true,
		},

		// Percent-encoded CRLF (uppercase)
		{
			name:     "percent-encoded CR uppercase",
			input:    "hello%0Dworld",
			expected: true,
		},
		{
			name:     "percent-encoded LF uppercase",
			input:    "hello%0Aworld",
			expected: true,
		},
		{
			name:     "percent-encoded CRLF uppercase",
			input:    "hello%0D%0Aworld",
			expected: true,
		},

		// Mixed case
		{
			name:     "percent-encoded CRLF mixed case",
			input:    "hello%0d%0Aworld",
			expected: true,
		},

		// Double-encoded CRLF (security fix)
		{
			name:     "double-encoded CR",
			input:    "hello%250dworld",
			expected: true,
		},
		{
			name:     "double-encoded LF",
			input:    "hello%250aworld",
			expected: true,
		},
		{
			name:     "double-encoded CRLF",
			input:    "hello%250d%250aworld",
			expected: true,
		},
		{
			name:     "double-encoded CR uppercase",
			input:    "hello%250Dworld",
			expected: true,
		},

		// Triple-encoded CRLF (defense in depth)
		{
			name:     "triple-encoded CR",
			input:    "hello%25250dworld",
			expected: true,
		},
		{
			name:     "triple-encoded LF",
			input:    "hello%25250aworld",
			expected: true,
		},

		// False positives check - these should NOT match
		{
			name:     "normal percent encoding not matched",
			input:    "hello%20world",
			expected: false,
		},
		{
			name:     "partial pattern not matched",
			input:    "hello%0world",
			expected: false,
		},
		{
			name:     "percent-25 without CRLF",
			input:    "hello%25world",
			expected: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			result := containsCRLF(tt.input)
			assert.Equal(t, tt.expected, result)
		})
	}
}

// TestNormalizePort tests the port normalization helper
func TestNormalizePort(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name     string
		scheme   string
		port     string
		expected string
	}{
		{
			name:     "https with empty port returns 443",
			scheme:   "https",
			port:     "",
			expected: "443",
		},
		{
			name:     "http with empty port returns 80",
			scheme:   "http",
			port:     "",
			expected: "80",
		},
		{
			name:     "https with explicit port returns port",
			scheme:   "https",
			port:     "8443",
			expected: "8443",
		},
		{
			name:     "http with explicit port returns port",
			scheme:   "http",
			port:     "8080",
			expected: "8080",
		},
		{
			name:     "unknown scheme with empty port returns empty",
			scheme:   "ftp",
			port:     "",
			expected: "",
		},
		{
			name:     "empty scheme with empty port returns empty",
			scheme:   "",
			port:     "",
			expected: "",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			result := normalizePort(tt.scheme, tt.port)
			assert.Equal(t, tt.expected, result)
		})
	}
}

// TestIsSafePath tests the path safety validation
func TestIsSafePath(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name     string
		path     string
		expected bool
	}{
		// Valid paths
		{
			name:     "simple path",
			path:     "/dashboard",
			expected: true,
		},
		{
			name:     "root path",
			path:     "/",
			expected: true,
		},
		{
			name:     "nested path",
			path:     "/admin/settings/users",
			expected: true,
		},
		{
			name:     "path with query",
			path:     "/dashboard?tab=settings",
			expected: true,
		},

		// Invalid: doesn't start with /
		{
			name:     "no leading slash",
			path:     "dashboard",
			expected: false,
		},
		{
			name:     "empty path",
			path:     "",
			expected: false,
		},

		// Invalid: contains dangerous patterns
		{
			name:     "double slash",
			path:     "/path//to/resource",
			expected: false,
		},
		{
			name:     "backslash",
			path:     "/path\\to\\resource",
			expected: false,
		},
		{
			name:     "protocol specifier",
			path:     "/path://evil",
			expected: false,
		},
		{
			name:     "directory traversal",
			path:     "/path/../etc/passwd",
			expected: false,
		},
		{
			name:     "null byte",
			path:     "/path\x00evil",
			expected: false,
		},

		// Invalid: URL-encoded attacks (security fix)
		{
			name:     "URL-encoded directory traversal",
			path:     "/path/%2e%2e/etc/passwd",
			expected: false,
		},
		{
			name:     "mixed encoded directory traversal",
			path:     "/path/%2e./etc/passwd",
			expected: false,
		},
		{
			name:     "mixed encoded directory traversal 2",
			path:     "/path/.%2e/etc/passwd",
			expected: false,
		},
		{
			name:     "URL-encoded null byte",
			path:     "/path%00evil",
			expected: false,
		},
		{
			name:     "URL-encoded backslash",
			path:     "/path%5cto%5cresource",
			expected: false,
		},
		{
			name:     "URL-encoded double slash",
			path:     "/path%2f%2fto",
			expected: false,
		},
		{
			name:     "URL-encoded slash with literal",
			path:     "/path/%2f/to",
			expected: false,
		},
		{
			name:     "uppercase URL-encoded traversal",
			path:     "/path/%2E%2E/etc",
			expected: false,
		},

		// Invalid: double-encoded attacks (security fix)
		{
			name:     "double-encoded directory traversal",
			path:     "/path/%252e%252e/etc",
			expected: false,
		},
		{
			name:     "double-encoded slash",
			path:     "/path%252f%252fetc",
			expected: false,
		},
		{
			name:     "double-encoded backslash",
			path:     "/path%255c%255c",
			expected: false,
		},
		{
			name:     "double-encoded null byte",
			path:     "/path%2500evil",
			expected: false,
		},
		{
			name:     "triple-encoded directory traversal",
			path:     "/path/%25252e%25252e/etc",
			expected: false,
		},

		// Invalid: too long
		{
			name:     "path too long",
			path:     "/" + string(make([]byte, MaxSafePathLength)),
			expected: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			result := IsSafePath(tt.path)
			assert.Equal(t, tt.expected, result)
		})
	}
}

// TestIsValidRedirect tests the redirect validation wrapper
func TestIsValidRedirect(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name     string
		path     string
		expected bool
	}{
		{
			name:     "valid path",
			path:     "/dashboard",
			expected: true,
		},
		{
			name:     "invalid path with traversal",
			path:     "/path/../secret",
			expected: false,
		},
		{
			name:     "invalid path without leading slash",
			path:     "dashboard",
			expected: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			result := IsValidRedirect(tt.path)
			assert.Equal(t, tt.expected, result)
		})
	}
}
