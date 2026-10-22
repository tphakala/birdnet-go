// ingress_test.go: Tests for the requestBasePath helper in the auth package
// that resolves the reverse proxy base path: the value the server's context
// middleware already stored, else the proxy request headers (no config
// fallback).

package auth

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/labstack/echo/v4"
	"github.com/stretchr/testify/assert"
)

// TestRequestBasePath tests the requestBasePath helper priority chain:
// X-Ingress-Path > X-Forwarded-Prefix > empty.
func TestRequestBasePath(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name             string
		xIngressPath     string
		xForwardedPrefix string
		ctxBasePath      string // basePath set in the Echo context by the server's middleware
		expected         string
	}{
		{
			name:         "X-Ingress-Path header present",
			xIngressPath: "/api/hassio_ingress/abc123",
			expected:     "/api/hassio_ingress/abc123",
		},
		{
			name:             "X-Ingress-Path takes priority over X-Forwarded-Prefix",
			xIngressPath:     "/ingress/token",
			xForwardedPrefix: "/forwarded",
			expected:         "/ingress/token",
		},
		{
			name:             "X-Forwarded-Prefix used when X-Ingress-Path absent",
			xForwardedPrefix: "/proxy/prefix",
			expected:         "/proxy/prefix",
		},
		{
			name:     "No headers returns empty string",
			expected: "",
		},
		{
			name:         "Trailing slash trimmed from X-Ingress-Path",
			xIngressPath: "/ingress/token/",
			expected:     "/ingress/token",
		},
		{
			name:             "Trailing slash trimmed from X-Forwarded-Prefix",
			xForwardedPrefix: "/proxy/prefix/",
			expected:         "/proxy/prefix",
		},
		{
			name:         "Multiple trailing slashes trimmed from X-Ingress-Path",
			xIngressPath: "/ingress/token///",
			expected:     "/ingress/token",
		},
		{
			name:             "Multiple trailing slashes trimmed from X-Forwarded-Prefix",
			xForwardedPrefix: "/proxy///",
			expected:         "/proxy",
		},
		{
			name:         "Deep ingress path preserved",
			xIngressPath: "/api/hassio_ingress/very/long/token/path",
			expected:     "/api/hassio_ingress/very/long/token/path",
		},
		// Validation: reject malicious header values
		{
			name:             "Protocol-relative X-Ingress-Path rejected, falls through to X-Forwarded-Prefix",
			xIngressPath:     "//evil.com",
			xForwardedPrefix: "/safe/prefix",
			expected:         "/safe/prefix",
		},
		{
			name:         "Protocol-relative X-Ingress-Path rejected, returns empty",
			xIngressPath: "//evil.com",
			expected:     "",
		},
		{
			name:             "Absolute URL in X-Forwarded-Prefix rejected",
			xForwardedPrefix: "https://evil.com/path",
			expected:         "",
		},
		{
			name:         "Non-slash X-Ingress-Path rejected",
			xIngressPath: "relative/path",
			expected:     "",
		},
		// One rejected sequence per row, so dropping any entry other than "://"
		// (subsumed by "//") from this package's isSafePathPrefix fails a row.
		{
			name:         "Path traversal in X-Ingress-Path rejected",
			xIngressPath: "/ingress/../admin",
			expected:     "",
		},
		{
			name:         "Backslash in X-Ingress-Path rejected",
			xIngressPath: `/ingress\evil.com`,
			expected:     "",
		},
		{
			name:         "Newline in X-Ingress-Path rejected",
			xIngressPath: "/ingress\nX-Injected: 1",
			expected:     "",
		},
		{
			name:         "Carriage return in X-Ingress-Path rejected",
			xIngressPath: "/ingress\rX-Injected: 1",
			expected:     "",
		},
		{
			name:         "NUL in X-Ingress-Path rejected",
			xIngressPath: "/ingress\x00evil",
			expected:     "",
		},
		// The server's context middleware has already resolved the base path.
		{
			name:         "Context basePath wins over headers",
			ctxBasePath:  "/configured",
			xIngressPath: "/ingress/token",
			expected:     "/configured",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			e := echo.New()
			req := httptest.NewRequest(http.MethodGet, "/", http.NoBody)
			if tt.xIngressPath != "" {
				req.Header.Set("X-Ingress-Path", tt.xIngressPath)
			}
			if tt.xForwardedPrefix != "" {
				req.Header.Set("X-Forwarded-Prefix", tt.xForwardedPrefix)
			}
			rec := httptest.NewRecorder()
			c := e.NewContext(req, rec)
			if tt.ctxBasePath != "" {
				c.Set("basePath", tt.ctxBasePath)
			}

			result := requestBasePath(c)
			assert.Equal(t, tt.expected, result)
		})
	}
}
