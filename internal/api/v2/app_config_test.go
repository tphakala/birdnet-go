// app_config_test.go: Tests for the requestBasePath helper that resolves
// the effective base path prefix for reverse proxy / ingress support in API v2.

package api

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/labstack/echo/v4"
	"github.com/stretchr/testify/assert"
	"github.com/tphakala/birdnet-go/internal/conf"
)

// TestRequestBasePath tests the requestBasePath helper priority chain:
// X-Ingress-Path > X-Forwarded-Prefix > config BasePath > empty.
func TestRequestBasePath(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name             string
		xIngressPath     string
		xForwardedPrefix string
		basePath         string
		nilSettings      bool
		expected         string
	}{
		{
			name:         "X-Ingress-Path header takes priority",
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
			name:             "X-Ingress-Path takes priority over config BasePath",
			xIngressPath:     "/ingress/token",
			basePath:         "/configured",
			expected:         "/ingress/token",
		},
		{
			name:             "X-Ingress-Path takes priority over both header and config",
			xIngressPath:     "/ingress/token",
			xForwardedPrefix: "/forwarded",
			basePath:         "/configured",
			expected:         "/ingress/token",
		},
		{
			name:             "X-Forwarded-Prefix used when X-Ingress-Path absent",
			xForwardedPrefix: "/proxy/prefix",
			expected:         "/proxy/prefix",
		},
		{
			name:             "X-Forwarded-Prefix takes priority over config BasePath",
			xForwardedPrefix: "/proxy/prefix",
			basePath:         "/configured",
			expected:         "/proxy/prefix",
		},
		{
			name:     "Config BasePath used when no headers present",
			basePath: "/birdnet",
			expected: "/birdnet",
		},
		{
			name:     "Returns empty string when nothing configured",
			expected: "",
		},
		{
			name:        "Returns empty string with nil settings and no headers",
			nilSettings: true,
			expected:    "",
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
			name:     "Trailing slash trimmed from config BasePath",
			basePath: "/birdnet/",
			expected: "/birdnet",
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
			name:     "Multiple trailing slashes trimmed from config BasePath",
			basePath: "/birdnet///",
			expected: "/birdnet",
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

			var settings *conf.Settings
			if !tt.nilSettings {
				settings = &conf.Settings{
					WebServer: conf.WebServerSettings{
						BasePath: tt.basePath,
					},
				}
			}

			result := requestBasePath(c, settings)
			assert.Equal(t, tt.expected, result)
		})
	}
}
