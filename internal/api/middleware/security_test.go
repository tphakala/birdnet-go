package middleware

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/labstack/echo/v4"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestNewSecureHeaders_ReferrerPolicy(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name     string
		policy   string
		expected string
	}{
		{
			name:     "explicit policy",
			policy:   "no-referrer",
			expected: "no-referrer",
		},
		{
			name:     "default when empty",
			policy:   "",
			expected: "strict-origin-when-cross-origin",
		},
		{
			name:     "custom policy",
			policy:   "origin-when-cross-origin",
			expected: "origin-when-cross-origin",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			mw := NewSecureHeaders(&SecurityConfig{ReferrerPolicy: tt.policy})
			c, rec := newTestContext(t, http.MethodGet, "/")

			handler := mw(func(c echo.Context) error {
				return c.String(http.StatusOK, "ok")
			})

			err := handler(c)
			require.NoError(t, err)
			assert.Equal(t, tt.expected, rec.Header().Get("Referrer-Policy"))
		})
	}
}

func TestNewSecureHeaders_CrossOriginOpenerPolicy(t *testing.T) {
	t.Parallel()

	mw := NewSecureHeaders(DefaultSecurityConfig())

	t.Run("set on HTTPS request", func(t *testing.T) {
		t.Parallel()
		c, rec := newTestContext(t, http.MethodGet, "/")
		c.Request().Header.Set("X-Forwarded-Proto", "https")
		c.Request().RemoteAddr = "127.0.0.1:54321" // simulate trusted reverse proxy

		handler := mw(func(c echo.Context) error {
			return c.String(http.StatusOK, "ok")
		})

		err := handler(c)
		require.NoError(t, err)
		assert.Equal(t, "same-origin", rec.Header().Get("Cross-Origin-Opener-Policy"))
	})

	t.Run("set on localhost request", func(t *testing.T) {
		t.Parallel()
		c, rec := newTestContext(t, http.MethodGet, "/")
		c.Request().Host = "localhost:8080"

		handler := mw(func(c echo.Context) error {
			return c.String(http.StatusOK, "ok")
		})

		err := handler(c)
		require.NoError(t, err)
		assert.Equal(t, "same-origin", rec.Header().Get("Cross-Origin-Opener-Policy"))
	})

	t.Run("omitted on plain HTTP non-localhost", func(t *testing.T) {
		t.Parallel()
		c, rec := newTestContext(t, http.MethodGet, "/")
		c.Request().Host = "192.168.1.100:8080"

		handler := mw(func(c echo.Context) error {
			return c.String(http.StatusOK, "ok")
		})

		err := handler(c)
		require.NoError(t, err)
		assert.Empty(t, rec.Header().Get("Cross-Origin-Opener-Policy"))
	})
}

func TestIsLocalhost(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name     string
		host     string
		expected bool
	}{
		{"localhost no port", "localhost", true},
		{"localhost with port", "localhost:8080", true},
		{"127.0.0.1 no port", "127.0.0.1", true},
		{"127.0.0.1 with port", "127.0.0.1:8080", true},
		{"IPv6 loopback", "::1", true},
		{"IPv6 loopback with port", "[::1]:8080", true},
		{"LAN IP", "192.168.1.100:8080", false},
		{"hostname", "birdnet.local:8080", false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			r := &http.Request{Host: tt.host}
			assert.Equal(t, tt.expected, isLocalhost(r))
		})
	}
}

func TestNewSecureHeaders_FrameAncestors(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name           string
		allowEmbedding bool
		inputCSP       string
		wantExact      string
		wantContains   string
		wantNotContain string
	}{
		{
			name:         "adds frame-ancestors when embedding disabled",
			inputCSP:     "",
			wantContains: "frame-ancestors 'self'",
		},
		{
			name:           "no frame-ancestors when embedding allowed",
			allowEmbedding: true,
			inputCSP:       "",
			wantNotContain: "frame-ancestors",
		},
		{
			name:         "appends to existing CSP",
			inputCSP:     "default-src 'self'",
			wantExact:    "default-src 'self'; frame-ancestors 'self'",
			wantContains: "frame-ancestors 'self'",
		},
		{
			name:         "does not duplicate if already present",
			inputCSP:     "frame-ancestors 'none'",
			wantContains: "frame-ancestors 'none'",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			mw := NewSecureHeaders(&SecurityConfig{
				AllowEmbedding:        tt.allowEmbedding,
				ContentSecurityPolicy: tt.inputCSP,
			})
			c, rec := newTestContext(t, http.MethodGet, "/")

			handler := mw(func(c echo.Context) error {
				return c.String(http.StatusOK, "ok")
			})

			err := handler(c)
			require.NoError(t, err)

			csp := rec.Header().Get("Content-Security-Policy")
			if tt.wantExact != "" {
				assert.Equal(t, tt.wantExact, csp)
			}
			if tt.wantContains != "" {
				assert.Contains(t, csp, tt.wantContains)
			}
			if tt.wantNotContain != "" {
				assert.NotContains(t, csp, tt.wantNotContain)
			}
		})
	}
}

func TestNewSecureHeaders_XFrameOptions(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name           string
		allowEmbedding bool
		expected       string
	}{
		{
			name:     "SAMEORIGIN when embedding disabled",
			expected: "SAMEORIGIN",
		},
		{
			name:           "empty when embedding allowed",
			allowEmbedding: true,
			expected:       "",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			mw := NewSecureHeaders(&SecurityConfig{AllowEmbedding: tt.allowEmbedding})
			c, rec := newTestContext(t, http.MethodGet, "/")

			handler := mw(func(c echo.Context) error {
				return c.String(http.StatusOK, "ok")
			})

			err := handler(c)
			require.NoError(t, err)
			assert.Equal(t, tt.expected, rec.Header().Get("X-Frame-Options"))
		})
	}
}

func TestHasWildcardOrigin(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name    string
		origins []string
		want    bool
	}{
		{"wildcard only", []string{"*"}, true},
		{"wildcard among others", []string{"https://example.com", "*"}, true},
		{"no wildcard", []string{"https://example.com"}, false},
		{"empty list", []string{}, false},
		{"nil list", nil, false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			assert.Equal(t, tt.want, hasWildcardOrigin(tt.origins))
		})
	}
}

// newCORSTestContext creates an Echo context for CORS testing with the Origin header set.
func newCORSTestContext(t *testing.T, method, path, origin string) (echo.Context, *httptest.ResponseRecorder) {
	t.Helper()
	e := echo.New()
	req := httptest.NewRequest(method, path, http.NoBody)
	if origin != "" {
		req.Header.Set(echo.HeaderOrigin, origin)
	}
	if method == http.MethodOptions {
		req.Header.Set(echo.HeaderAccessControlRequestMethod, http.MethodGet)
	}
	rec := httptest.NewRecorder()
	return e.NewContext(req, rec), rec
}

func TestNewCORS_WildcardWithCredentials(t *testing.T) {
	t.Parallel()

	t.Run("reflects origin instead of wildcard when credentials enabled", func(t *testing.T) {
		t.Parallel()

		corsMiddleware := NewCORS(&SecurityConfig{
			AllowedOrigins:   []string{"*"},
			AllowCredentials: true,
		})

		c, rec := newCORSTestContext(t, http.MethodGet, "/api/v2/detections", "https://my-birdnet.local:8080")

		handler := corsMiddleware(func(c echo.Context) error {
			return c.String(http.StatusOK, "ok")
		})

		err := handler(c)
		require.NoError(t, err)

		// The origin should be reflected, not "*"
		allowOrigin := rec.Header().Get(echo.HeaderAccessControlAllowOrigin)
		assert.Equal(t, "https://my-birdnet.local:8080", allowOrigin,
			"expected reflected origin, not wildcard")

		assert.Equal(t, "true", rec.Header().Get(echo.HeaderAccessControlAllowCredentials))
	})

	t.Run("uses wildcard when credentials disabled", func(t *testing.T) {
		t.Parallel()

		corsMiddleware := NewCORS(&SecurityConfig{
			AllowedOrigins:   []string{"*"},
			AllowCredentials: false,
		})

		c, rec := newCORSTestContext(t, http.MethodGet, "/api/v2/detections", "https://example.com")

		handler := corsMiddleware(func(c echo.Context) error {
			return c.String(http.StatusOK, "ok")
		})

		err := handler(c)
		require.NoError(t, err)

		allowOrigin := rec.Header().Get(echo.HeaderAccessControlAllowOrigin)
		assert.Equal(t, "*", allowOrigin,
			"expected wildcard origin when credentials are disabled")

		// Credentials header should not be set
		assert.Empty(t, rec.Header().Get(echo.HeaderAccessControlAllowCredentials))
	})

	t.Run("explicit origins with credentials works normally", func(t *testing.T) {
		t.Parallel()

		corsMiddleware := NewCORS(&SecurityConfig{
			AllowedOrigins:   []string{"https://trusted.example.com"},
			AllowCredentials: true,
		})

		c, rec := newCORSTestContext(t, http.MethodGet, "/api/v2/detections", "https://trusted.example.com")

		handler := corsMiddleware(func(c echo.Context) error {
			return c.String(http.StatusOK, "ok")
		})

		err := handler(c)
		require.NoError(t, err)

		allowOrigin := rec.Header().Get(echo.HeaderAccessControlAllowOrigin)
		assert.Equal(t, "https://trusted.example.com", allowOrigin)
		assert.Equal(t, "true", rec.Header().Get(echo.HeaderAccessControlAllowCredentials))
	})

	t.Run("preflight reflects origin with credentials", func(t *testing.T) {
		t.Parallel()

		corsMiddleware := NewCORS(&SecurityConfig{
			AllowedOrigins:   []string{"*"},
			AllowCredentials: true,
		})

		c, rec := newCORSTestContext(t, http.MethodOptions, "/api/v2/detections", "https://home-assistant.local:8123")

		handler := corsMiddleware(func(c echo.Context) error {
			return c.String(http.StatusOK, "ok")
		})

		err := handler(c)
		require.NoError(t, err)

		allowOrigin := rec.Header().Get(echo.HeaderAccessControlAllowOrigin)
		assert.Equal(t, "https://home-assistant.local:8123", allowOrigin,
			"preflight should reflect origin when credentials are enabled")

		assert.Equal(t, "true", rec.Header().Get(echo.HeaderAccessControlAllowCredentials))
	})
}
