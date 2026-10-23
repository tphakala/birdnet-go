// basepath_test.go: Tests for base path support - prefix stripping middleware,
// HTML asset URL rewriting, and manifest path rewriting.

package api

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/labstack/echo/v4"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/tphakala/birdnet-go/internal/conf"
	"github.com/tphakala/birdnet-go/internal/conf/conftest"
)

// routedRequest records what the routed handler saw after the production
// middleware stack ran.
type routedRequest struct {
	path, rawPath, query, basePath string
}

// newBasePathTestServer builds a Server with the production middleware stack
// (setupMiddleware) and a catch-all route that records the request as the
// handler sees it. The strip and context middleware read the published
// settings snapshot (currentSettings), so this publishes settings and restores
// the previous snapshot on cleanup. Tests using it must not run in parallel.
func newBasePathTestServer(t *testing.T, basePath string) (*Server, *routedRequest) {
	t.Helper()
	settings := conftest.NewTestSettings().Build()
	settings.WebServer.BasePath = basePath

	previous := conf.GetSettings()
	t.Cleanup(func() { conf.StoreSettings(previous) })
	conf.StoreSettings(settings)

	s := &Server{
		echo:     echo.New(),
		config:   DefaultConfig(),
		settings: settings,
		slogger:  GetLogger(),
	}
	s.setupMiddleware()

	seen := &routedRequest{}
	s.echo.Any("/*", func(c echo.Context) error {
		seen.path = c.Request().URL.Path
		seen.rawPath = c.Request().URL.RawPath
		seen.query = c.Request().URL.RawQuery
		seen.basePath, _ = c.Get("basePath").(string)
		return c.NoContent(http.StatusOK)
	})
	return s, seen
}

// serveBasePathRequest sends a GET for target through s with the given proxy
// headers and requires that it was routed.
func serveBasePathRequest(t *testing.T, s *Server, target, xIngressPath, xForwardedPrefix string) {
	t.Helper()
	req := httptest.NewRequest(http.MethodGet, target, http.NoBody)
	if xIngressPath != "" {
		req.Header.Set("X-Ingress-Path", xIngressPath)
	}
	if xForwardedPrefix != "" {
		req.Header.Set("X-Forwarded-Prefix", xForwardedPrefix)
	}
	rec := httptest.NewRecorder()
	s.echo.ServeHTTP(rec, req)
	require.Equal(t, http.StatusOK, rec.Code)
}

// TestBasePathStripMiddleware tests the Pre middleware that strips the configured
// basepath prefix from incoming request URLs for direct access (no proxy).
func TestBasePathStripMiddleware(t *testing.T) {
	// Not parallel: newBasePathTestServer publishes the global settings.

	tests := []struct {
		name             string
		basePath         string
		requestPath      string
		xForwardedPrefix string // if set, middleware should skip stripping
		xIngressPath     string // if set, takes priority over X-Forwarded-Prefix
		expectedPath     string
		wantRawPath      string // URL.RawPath the handler sees; "" when Go leaves it unset
		wantQuery        string
		wantBasePath     string // c.Get("basePath") set by the context middleware
	}{
		{
			name:         "strips basepath prefix",
			basePath:     "/birdnet",
			requestPath:  "/birdnet/ui/dashboard",
			expectedPath: "/ui/dashboard",
			wantBasePath: "/birdnet",
		},
		{
			name:         "strips basepath from root",
			basePath:     "/birdnet",
			requestPath:  "/birdnet",
			expectedPath: "/",
			wantBasePath: "/birdnet",
		},
		{
			name:         "strips basepath with trailing slash",
			basePath:     "/birdnet",
			requestPath:  "/birdnet/",
			expectedPath: "/",
			wantBasePath: "/birdnet",
		},
		{
			name:         "passes through non-prefixed path",
			basePath:     "/birdnet",
			requestPath:  "/ui/dashboard",
			expectedPath: "/ui/dashboard",
			wantBasePath: "/birdnet",
		},
		{
			name:         "passes through root path",
			basePath:     "/birdnet",
			requestPath:  "/",
			expectedPath: "/",
			wantBasePath: "/birdnet",
		},
		{
			name:         "passes through API path",
			basePath:     "/birdnet",
			requestPath:  "/api/v2/health",
			expectedPath: "/api/v2/health",
			wantBasePath: "/birdnet",
		},
		{
			name:         "does not strip partial prefix match",
			basePath:     "/bird",
			requestPath:  "/birdnet/ui/dashboard",
			expectedPath: "/birdnet/ui/dashboard",
			wantBasePath: "/bird",
		},
		{
			name:             "skips stripping when X-Forwarded-Prefix header present",
			basePath:         "/birdnet",
			requestPath:      "/ui/dashboard",
			xForwardedPrefix: "/birdnet",
			expectedPath:     "/ui/dashboard",
			wantBasePath:     "/birdnet",
		},
		{
			name:         "multi-segment basepath",
			basePath:     "/apps/birdnet",
			requestPath:  "/apps/birdnet/ui/dashboard",
			expectedPath: "/ui/dashboard",
			wantBasePath: "/apps/birdnet",
		},
		{
			name:         "preserves query string through stripping",
			basePath:     "/birdnet",
			requestPath:  "/birdnet/ui/dashboard?tab=detections",
			expectedPath: "/ui/dashboard",
			wantQuery:    "tab=detections",
			wantBasePath: "/birdnet",
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
			wantBasePath:     "/birdnet",
		},
		{
			// Same scenario but via the Home Assistant ingress header.
			name:         "strips with X-Ingress-Path header and no config basepath",
			basePath:     "",
			requestPath:  "/api/hassio_ingress/TOKEN/api/v2/health",
			xIngressPath: "/api/hassio_ingress/TOKEN",
			expectedPath: "/api/v2/health",
			wantBasePath: "/api/hassio_ingress/TOKEN",
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
			wantBasePath:     "/birdnet",
		},
		{
			// No source of basepath at all: neither header nor config is set,
			// so the middleware must be a no-op.
			name:         "no strip when no basepath from any source",
			basePath:     "",
			requestPath:  "/api/v2/health",
			expectedPath: "/api/v2/health",
			wantBasePath: "",
		},
		{
			// Issue #447: RawPath is set on the parsed request only when the
			// raw input differs from Go's default path encoding, e.g. when a
			// proxy forwards non-canonical escape sequences (%2F for a slash
			// or lowercase hex). In that case both URL.Path and URL.RawPath
			// must be stripped in lockstep.
			//
			// Space-in-basepath case: the %2F in the trailing segment forces
			// RawPath to be non-empty so the bug path is actually exercised.
			name:         "strips encoded basepath with space",
			basePath:     "/birdnet go",
			requestPath:  "/birdnet%20go/ui/a%2Fb",
			expectedPath: "/ui/a/b",
			wantRawPath:  "/ui/a%2Fb",
			wantBasePath: "/birdnet go",
		},
		{
			// Non-ASCII basepath with lowercase hex encoding from the proxy.
			// Go's default escape uses uppercase hex, so the raw input is
			// non-canonical and RawPath is populated on the parsed request.
			name:         "strips encoded basepath with non-ASCII",
			basePath:     "/Übersicht",
			requestPath:  "/%c3%9cbersicht/ui/dashboard",
			expectedPath: "/ui/dashboard",
			wantRawPath:  "/ui/dashboard",
			wantBasePath: "/Übersicht",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			s, seen := newBasePathTestServer(t, tt.basePath)
			serveBasePathRequest(t, s, tt.requestPath, tt.xIngressPath, tt.xForwardedPrefix)

			assert.Equal(t, tt.expectedPath, seen.path, "URL.Path after stripping")
			// RawPath must be stripped in lockstep with Path, or Echo routes on
			// mismatched values.
			assert.Equal(t, tt.wantRawPath, seen.rawPath, "URL.RawPath after stripping")
			assert.Equal(t, tt.wantQuery, seen.query, "query string")
			assert.Equal(t, tt.wantBasePath, seen.basePath, "basePath in the Echo context")
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

// TestIsSafePathPrefix verifies the guard ingressPath uses for proxy-supplied
// path prefixes. This prevents header-driven open redirect / DOM XSS when the
// rewritten value ends up as a URL prefix in HTML/JS. The api/auth package has
// its own copy of the guard, tested through requestBasePath by
// TestRequestBasePath.
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
	// Not parallel: publishes the global settings.
	s, seen := newBasePathTestServer(t, "")
	do := func(path string) string {
		serveBasePathRequest(t, s, path, "", "")
		return seen.path
	}
	// The context middleware reads the published settings on every request
	// too, so handlers see the base path that is current for that request.
	wantContextBasePath := func(want string) {
		t.Helper()
		assert.Equal(t, want, seen.basePath, "context basePath should follow published settings")
	}
	publish := func(bp string) {
		next := conf.CloneSettings(conf.GetSettings())
		next.WebServer.BasePath = bp
		conf.StoreSettings(next)
	}

	// 1. No basepath configured: requests pass through untouched.
	assert.Equal(t, "/birdnet/ui/dashboard", do("/birdnet/ui/dashboard"),
		"no basepath should not strip anything")
	wantContextBasePath("")

	// 2. Basepath published at runtime: the middleware must pick it up.
	publish("/birdnet")
	assert.Equal(t, "/ui/dashboard", do("/birdnet/ui/dashboard"),
		"strip should activate without restart after basepath is set")
	wantContextBasePath("/birdnet")

	// 3. Basepath changed to a different value at runtime.
	publish("/apps/birdnet")
	assert.Equal(t, "/ui/dashboard", do("/apps/birdnet/ui/dashboard"),
		"strip should follow runtime basepath changes")
	assert.Equal(t, "/birdnet/ui/dashboard", do("/birdnet/ui/dashboard"),
		"old basepath must no longer strip after change")
	wantContextBasePath("/apps/birdnet")

	// 4. Basepath cleared: strip must disable again without restart.
	publish("")
	assert.Equal(t, "/apps/birdnet/ui/dashboard", do("/apps/birdnet/ui/dashboard"),
		"clearing basepath should restore pass-through behavior")
	wantContextBasePath("")
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
