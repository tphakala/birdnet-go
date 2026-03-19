package middleware

import (
	"net/http"
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
	c, rec := newTestContext(t, http.MethodGet, "/")

	handler := mw(func(c echo.Context) error {
		return c.String(http.StatusOK, "ok")
	})

	err := handler(c)
	require.NoError(t, err)
	assert.Equal(t, "same-origin", rec.Header().Get("Cross-Origin-Opener-Policy"))
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
