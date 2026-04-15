// basepath_test.go: Tests for base path support - prefix stripping middleware,
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

	"github.com/tphakala/birdnet-go/internal/conf"
)

// stripMiddlewareForTest mirrors the production Pre middleware in setupMiddleware().
// Kept in sync by hand. We can't easily instantiate a full Server in this package-level
// unit test, so the stripping logic is duplicated here and exercised with the same
// table-driven assertions production would see.
//
// The middleware resolves the effective basepath through ingressPath() so that
// header-supplied basepaths strip the request prefix even when no config basepath
// is set. getSettings may return nil when the test does not care about config.
func stripMiddlewareForTest(getSettings func() *conf.Settings) echo.MiddlewareFunc {
	return func(next echo.HandlerFunc) echo.HandlerFunc {
		return func(c echo.Context) error {
			bp := ingressPath(c, getSettings())
			if bp == "" {
				return next(c)
			}
			req := c.Request()
			hasProxyHeader := req.Header.Get("X-Ingress-Path") != "" || req.Header.Get("X-Forwarded-Prefix") != ""
			if hasProxyHeader && !strings.HasPrefix(req.URL.Path, bp+"/") && req.URL.Path != bp {
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
					// Mirror production: also rewrite RawPath when set,
					// so percent-encoded paths route correctly under test.
					if req.URL.RawPath != "" && strings.HasPrefix(req.URL.RawPath, bp) {
						raw := req.URL.RawPath[len(bp):]
						if raw == "" {
							raw = "/"
						}
						req.URL.RawPath = raw
					}
				}
			}
			return next(c)
		}
	}
}

// settingsWithBasePath returns a *conf.Settings whose WebServer.BasePath is set
// to the given value. Used as a compact helper for table-driven tests.
func settingsWithBasePath(bp string) *conf.Settings {
	s := &conf.Settings{}
	s.WebServer.BasePath = bp
	return s
}

// TestBasePathStripMiddleware tests the Pre middleware that strips the configured
// basepath prefix from incoming request URLs for direct access (no proxy).
func TestBasePathStripMiddleware(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name             string
		basePath         string
		requestPath      string
		xForwardedPrefix string // if set, middleware should skip stripping
		xIngressPath     string // if set, takes priority over X-Forwarded-Prefix
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
		{
			// Issue #2778: the login callback URL is built from the header-derived
			// basepath, so the strip middleware must honor the same header when
			// no config basepath is set, otherwise the returned callback 404s.
			name:             "strips with X-Forwarded-Prefix header and no config basepath",
			basePath:         "",
			requestPath:      "/birdnet/api/v2/health",
			xForwardedPrefix: "/birdnet",
			expectedPath:     "/api/v2/health",
		},
		{
			// Same scenario but via the Home Assistant ingress header.
			name:         "strips with X-Ingress-Path header and no config basepath",
			basePath:     "",
			requestPath:  "/api/hassio_ingress/TOKEN/api/v2/health",
			xIngressPath: "/api/hassio_ingress/TOKEN",
			expectedPath: "/api/v2/health",
		},
		{
			// When the proxy already stripped the prefix but still sent the header,
			// the request path does not start with the basepath so stripping must
			// be skipped to avoid breaking routing.
			name:             "no strip when proxy header present and path already stripped",
			basePath:         "",
			requestPath:      "/api/v2/health",
			xForwardedPrefix: "/birdnet",
			expectedPath:     "/api/v2/health",
		},
		{
			// No source of basepath at all: neither header nor config is set,
			// so the middleware must be a no-op.
			name:         "no strip when no basepath from any source",
			basePath:     "",
			requestPath:  "/api/v2/health",
			expectedPath: "/api/v2/health",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			settings := settingsWithBasePath(tt.basePath)
			var capturedPath string
			var capturedQuery string

			e := echo.New()

			// Exercise the strip middleware via the shared test helper that mirrors production.
			e.Pre(stripMiddlewareForTest(func() *conf.Settings { return settings }))

			// Catch-all handler to capture the routed path
			e.Any("/*", func(c echo.Context) error {
				capturedPath = c.Request().URL.Path
				capturedQuery = c.Request().URL.RawQuery
				return c.NoContent(http.StatusOK)
			})

			req := httptest.NewRequest(http.MethodGet, tt.requestPath, http.NoBody)
			if tt.xForwardedPrefix != "" {
				req.Header.Set("X-Forwarded-Prefix", tt.xForwardedPrefix)
			}
			if tt.xIngressPath != "" {
				req.Header.Set("X-Ingress-Path", tt.xIngressPath)
			}
			rec := httptest.NewRecorder()

			e.ServeHTTP(rec, req)

			assert.Equal(t, tt.expectedPath, capturedPath, "path after stripping")
			if strings.Contains(tt.requestPath, "?") {
				assert.NotEmpty(t, capturedQuery, "query string should be preserved")
			}
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
		xIngressPath     string
		xForwardedPrefix string
		expectedBasePath string
	}{
		{
			name:             "sets basePath from X-Forwarded-Prefix",
			xForwardedPrefix: "/birdnet",
			expectedBasePath: "/birdnet",
		},
		{
			name:             "sets basePath from X-Ingress-Path",
			xIngressPath:     "/api/hassio_ingress/TOKEN",
			expectedBasePath: "/api/hassio_ingress/TOKEN",
		},
		{
			name:             "X-Ingress-Path wins over X-Forwarded-Prefix",
			xIngressPath:     "/api/hassio_ingress/TOKEN",
			xForwardedPrefix: "/birdnet",
			expectedBasePath: "/api/hassio_ingress/TOKEN",
		},
		{
			name:             "rejects protocol-relative X-Forwarded-Prefix",
			xForwardedPrefix: "//evil.example",
			expectedBasePath: "",
		},
		{
			name:             "rejects backslash-prefixed X-Forwarded-Prefix",
			xForwardedPrefix: `/\evil.example`,
			expectedBasePath: "",
		},
		{
			// X-Ingress-Path is the higher-priority header (Home Assistant uses it), so
			// guard it against the same backslash → protocol-relative URL trick.
			name:             "rejects backslash-prefixed X-Ingress-Path",
			xIngressPath:     `/\evil.example`,
			expectedBasePath: "",
		},
		{
			name:             "rejects scheme-embedded X-Forwarded-Prefix",
			xForwardedPrefix: "/http://evil",
			expectedBasePath: "",
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
			if tt.xIngressPath != "" {
				req.Header.Set("X-Ingress-Path", tt.xIngressPath)
			}
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

// TestIsSafePathPrefix verifies the guard used by ingressPath/requestBasePath
// for proxy-supplied path prefixes. This prevents header-driven open redirect
// / DOM XSS when the rewritten value ends up as a URL prefix in HTML/JS.
func TestIsSafePathPrefix(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name  string
		input string
		want  bool
	}{
		{"empty is unsafe", "", false},
		{"simple prefix is safe", "/birdnet", true},
		{"multi-segment is safe", "/apps/birdnet", true},
		{"hassio ingress prefix is safe", "/api/hassio_ingress/TOKEN", true},
		{"trailing slash is safe", "/birdnet/", true},
		{"no leading slash is unsafe", "birdnet", false},
		{"protocol-relative // is unsafe", "//evil.example", false},
		{"embedded // is unsafe", "/birdnet//foo", false},
		{"backslash is unsafe", `/\evil.example`, false},
		{"backslash midpath is unsafe", `/birdnet\foo`, false},
		{"scheme :// is unsafe", "/http://evil", false},
		{"naked scheme prefix is unsafe", "https://evil", false},
		{"path traversal leading is unsafe", "/../admin", false},
		{"path traversal midpath is unsafe", "/birdnet/../admin", false},
		{"lone dotdot segment is unsafe", "/..", false},
		{"newline injection is unsafe", "/birdnet\nX-Injected: 1", false},
		{"carriage return injection is unsafe", "/birdnet\rX-Injected: 1", false},
		{"null byte is unsafe", "/birdnet\x00/evil", false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			assert.Equal(t, tt.want, isSafePathPrefix(tt.input))
		})
	}
}

// TestBasePathStripMiddleware_HotReload verifies that the strip middleware reads
// the current basepath on every request rather than capturing it at setup time.
// Regression guard: previously the middleware was registered conditionally and
// captured bp in a closure, so changes to settings.WebServer.BasePath required
// a server restart to take effect.
func TestBasePathStripMiddleware_HotReload(t *testing.T) {
	t.Parallel()

	// Mutable basepath source, simulating settings.WebServer.BasePath being
	// updated at runtime. Access is serialized through the test; no goroutines.
	var currentBP string

	e := echo.New()
	e.Pre(stripMiddlewareForTest(func() *conf.Settings { return settingsWithBasePath(currentBP) }))

	var capturedPath string
	e.Any("/*", func(c echo.Context) error {
		capturedPath = c.Request().URL.Path
		return c.NoContent(http.StatusOK)
	})

	do := func(path string) string {
		req := httptest.NewRequest(http.MethodGet, path, http.NoBody)
		rec := httptest.NewRecorder()
		e.ServeHTTP(rec, req)
		require.Equal(t, http.StatusOK, rec.Code)
		return capturedPath
	}

	// 1. No basepath configured: requests pass through untouched.
	assert.Equal(t, "/ui/dashboard", do("/ui/dashboard"), "no basepath should pass through")
	assert.Equal(t, "/birdnet/ui/dashboard", do("/birdnet/ui/dashboard"),
		"no basepath should not strip anything")

	// 2. Basepath set to /birdnet at runtime — middleware must pick it up immediately.
	currentBP = "/birdnet"
	assert.Equal(t, "/ui/dashboard", do("/birdnet/ui/dashboard"),
		"strip should activate without restart after basepath is set")
	assert.Equal(t, "/other", do("/other"),
		"non-prefixed paths should still pass through unchanged")

	// 3. Basepath changed to a different value at runtime.
	currentBP = "/apps/birdnet"
	assert.Equal(t, "/ui/dashboard", do("/apps/birdnet/ui/dashboard"),
		"strip should follow runtime basepath changes")
	assert.Equal(t, "/birdnet/ui/dashboard", do("/birdnet/ui/dashboard"),
		"old basepath must no longer strip after change")

	// 4. Basepath cleared — strip must disable again without restart.
	currentBP = ""
	assert.Equal(t, "/apps/birdnet/ui/dashboard", do("/apps/birdnet/ui/dashboard"),
		"clearing basepath should restore pass-through behavior")
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
