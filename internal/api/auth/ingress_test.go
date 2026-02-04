// ingress_test.go: Tests for the requestBasePath helper in the auth package
// that resolves the reverse proxy base path from request headers.
// This version only reads headers (no config fallback).

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

			result := requestBasePath(c)
			assert.Equal(t, tt.expected, result)
		})
	}
}
