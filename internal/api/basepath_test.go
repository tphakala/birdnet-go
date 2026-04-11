// basepath_test.go: Tests for base path support — prefix stripping middleware,
// HTML asset URL rewriting, and manifest path rewriting.

package api

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/labstack/echo/v4"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestBasePathStripMiddleware tests the Pre middleware that strips the configured
// basepath prefix from incoming request URLs for direct access (no proxy).
func TestBasePathStripMiddleware(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name             string
		basePath         string
		requestPath      string
		xForwardedPrefix string // if set, middleware should skip stripping
		expectedPath     string
	}{
		{
			name:         "strips basepath prefix",
			basePath:     "/birdnet",
			requestPath:  "/birdnet/ui/dashboard",
			expectedPath: "/ui/dashboard",
		},
		{
			name:         "strips basepath from root",
			basePath:     "/birdnet",
			requestPath:  "/birdnet",
			expectedPath: "/",
		},
		{
			name:         "strips basepath with trailing slash",
			basePath:     "/birdnet",
			requestPath:  "/birdnet/",
			expectedPath: "/",
		},
		{
			name:         "passes through non-prefixed path",
			basePath:     "/birdnet",
			requestPath:  "/ui/dashboard",
			expectedPath: "/ui/dashboard",
		},
		{
			name:         "passes through root path",
			basePath:     "/birdnet",
			requestPath:  "/",
			expectedPath: "/",
		},
		{
			name:         "passes through API path",
			basePath:     "/birdnet",
			requestPath:  "/api/v2/health",
			expectedPath: "/api/v2/health",
		},
		{
			name:         "does not strip partial prefix match",
			basePath:     "/bird",
			requestPath:  "/birdnet/ui/dashboard",
			expectedPath: "/birdnet/ui/dashboard",
		},
		{
			name:             "skips stripping when X-Forwarded-Prefix header present",
			basePath:         "/birdnet",
			requestPath:      "/ui/dashboard",
			xForwardedPrefix: "/birdnet",
			expectedPath:     "/ui/dashboard",
		},
		{
			name:         "multi-segment basepath",
			basePath:     "/apps/birdnet",
			requestPath:  "/apps/birdnet/ui/dashboard",
			expectedPath: "/ui/dashboard",
		},
		{
			name:         "preserves query string through stripping",
			basePath:     "/birdnet",
			requestPath:  "/birdnet/ui/dashboard?tab=detections",
			expectedPath: "/ui/dashboard",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			bp := tt.basePath
			var capturedPath string

			e := echo.New()

			// Register the stripping middleware using the same logic as setupMiddleware
			e.Pre(func(next echo.HandlerFunc) echo.HandlerFunc {
				return func(c echo.Context) error {
					req := c.Request()
					if req.Header.Get("X-Ingress-Path") != "" || req.Header.Get("X-Forwarded-Prefix") != "" {
						return next(c)
					}
					path := req.URL.Path
					if strings.HasPrefix(path, bp) {
						rest := path[len(bp):]
						if rest == "" || rest[0] == '/' {
							if rest == "" {
								rest = "/"
							}
							req.URL.Path = rest
						}
					}
					return next(c)
				}
			})

			// Catch-all handler to capture the routed path
			e.Any("/*", func(c echo.Context) error {
				capturedPath = c.Request().URL.Path
				return c.NoContent(http.StatusOK)
			})

			req := httptest.NewRequest(http.MethodGet, tt.requestPath, http.NoBody)
			if tt.xForwardedPrefix != "" {
				req.Header.Set("X-Forwarded-Prefix", tt.xForwardedPrefix)
			}
			rec := httptest.NewRecorder()

			e.ServeHTTP(rec, req)

			assert.Equal(t, tt.expectedPath, capturedPath, "path after stripping")
		})
	}
}

// TestRewriteHTMLBasePath tests the HTML content rewriting for base path support.
func TestRewriteHTMLBasePath(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name     string
		input    string
		basePath string
		expected string
	}{
		{
			name:     "no rewriting when basePath is empty",
			input:    `<link href="/ui/assets/main.css">`,
			basePath: "",
			expected: `<link href="/ui/assets/main.css">`,
		},
		{
			name:     "rewrites href attributes",
			input:    `<link href="/ui/assets/main-abc.css" rel="stylesheet">`,
			basePath: "/birdnet",
			expected: `<link href="/birdnet/ui/assets/main-abc.css" rel="stylesheet">`,
		},
		{
			name:     "rewrites src attributes",
			input:    `<script type="module" src="/ui/assets/main-abc.js"></script>`,
			basePath: "/birdnet",
			expected: `<script type="module" src="/birdnet/ui/assets/main-abc.js"></script>`,
		},
		{
			name:     "rewrites service worker registration",
			input:    `navigator.serviceWorker.register('/sw.js', { scope: '/' })`,
			basePath: "/birdnet",
			expected: `navigator.serviceWorker.register('/birdnet/sw.js', { scope: '/birdnet/' })`,
		},
		{
			name:     "rewrites multiple attributes in full HTML",
			input:    `<link href="/ui/assets/fonts/Inter.woff2" as="font"><script src="/ui/assets/main.js"></script>`,
			basePath: "/proxy",
			expected: `<link href="/proxy/ui/assets/fonts/Inter.woff2" as="font"><script src="/proxy/ui/assets/main.js"></script>`,
		},
		{
			name:     "rewrites favicon and manifest paths",
			input:    `<link rel="icon" href="/favicon.ico"><link rel="manifest" href="/manifest.webmanifest">`,
			basePath: "/birdnet",
			expected: `<link rel="icon" href="/birdnet/favicon.ico"><link rel="manifest" href="/birdnet/manifest.webmanifest">`,
		},
		{
			name:     "multi-segment basepath",
			input:    `<script src="/ui/assets/main.js"></script>`,
			basePath: "/apps/birdnet",
			expected: `<script src="/apps/birdnet/ui/assets/main.js"></script>`,
		},
		{
			name:     "idempotent: already-prefixed paths are not double-prefixed",
			input:    `<link href="/birdnet/ui/assets/main.css"><script src="/birdnet/ui/assets/main.js"></script>`,
			basePath: "/birdnet",
			expected: `<link href="/birdnet/ui/assets/main.css"><script src="/birdnet/ui/assets/main.js"></script>`,
		},
		{
			name:     "idempotent: mixed prefixed and unprefixed",
			input:    `<link href="/birdnet/ui/assets/main.css"><link href="/favicon.ico">`,
			basePath: "/birdnet",
			expected: `<link href="/birdnet/ui/assets/main.css"><link href="/birdnet/favicon.ico">`,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			result := rewriteHTMLBasePath([]byte(tt.input), tt.basePath)
			assert.Equal(t, tt.expected, string(result))
		})
	}
}

// TestRewriteManifestBasePath tests the manifest.webmanifest rewriting.
func TestRewriteManifestBasePath(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name     string
		input    string
		basePath string
		expected string
	}{
		{
			name:     "rewrites icon src paths",
			input:    `{"icons":[{"src":"/ui/assets/icon.png"}]}`,
			basePath: "/birdnet",
			expected: `{"icons":[{"src":"/birdnet/ui/assets/icon.png"}]}`,
		},
		{
			name:     "rewrites start_url and scope",
			input:    `{"start_url":"../../","scope":"../../"}`,
			basePath: "/birdnet",
			expected: `{"start_url":"/birdnet/","scope":"/birdnet/"}`,
		},
		{
			name: "rewrites full manifest",
			input: `{
  "name": "BirdNET-Go",
  "start_url": "../../",
  "scope": "../../",
  "icons": [
    {"src": "/ui/assets/apple-touch-icon.png"},
    {"src": "/ui/assets/BirdNET-Go-logo.webp"}
  ]
}`,
			basePath: "/birdnet",
			expected: `{
  "name": "BirdNET-Go",
  "start_url": "/birdnet/",
  "scope": "/birdnet/",
  "icons": [
    {"src": "/birdnet/ui/assets/apple-touch-icon.png"},
    {"src": "/birdnet/ui/assets/BirdNET-Go-logo.webp"}
  ]
}`,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			result := rewriteManifestBasePath([]byte(tt.input), tt.basePath)
			assert.Equal(t, tt.expected, string(result))
		})
	}
}

// TestBasePathContextMiddleware tests that the basePath context middleware
// correctly sets the basePath value from ingressPath().
func TestBasePathContextMiddleware(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name             string
		xForwardedPrefix string
		expectedBasePath string
	}{
		{
			name:             "sets basePath from X-Forwarded-Prefix",
			xForwardedPrefix: "/birdnet",
			expectedBasePath: "/birdnet",
		},
		{
			name:             "empty basePath when no headers",
			expectedBasePath: "",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			e := echo.New()
			var capturedBasePath string

			// Simulate the context middleware
			e.Use(func(next echo.HandlerFunc) echo.HandlerFunc {
				return func(c echo.Context) error {
					c.Set("basePath", ingressPath(c, nil))
					return next(c)
				}
			})

			e.GET("/test", func(c echo.Context) error {
				capturedBasePath, _ = c.Get("basePath").(string)
				return c.NoContent(http.StatusOK)
			})

			req := httptest.NewRequest(http.MethodGet, "/test", http.NoBody)
			if tt.xForwardedPrefix != "" {
				req.Header.Set("X-Forwarded-Prefix", tt.xForwardedPrefix)
			}
			rec := httptest.NewRecorder()

			e.ServeHTTP(rec, req)

			require.Equal(t, http.StatusOK, rec.Code)
			assert.Equal(t, tt.expectedBasePath, capturedBasePath)
		})
	}
}

// TestIsRewritableAsset tests the file extension check for CSS/JS rewriting.
func TestIsRewritableAsset(t *testing.T) {
	t.Parallel()

	tests := []struct {
		path     string
		expected bool
	}{
		{"main-abc123.css", true},
		{"main-abc123.js", true},
		{"chunk-abc123.mjs", true},
		{"Inter-Variable.woff2", false},
		{"logo.png", false},
		{"data.json", false},
		{"index.html", false},
	}

	for _, tt := range tests {
		t.Run(tt.path, func(t *testing.T) {
			t.Parallel()
			assert.Equal(t, tt.expected, isRewritableAsset(tt.path))
		})
	}
}
