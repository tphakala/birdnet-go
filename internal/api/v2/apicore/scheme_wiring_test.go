// scheme_wiring_test.go: ties NewCore's scheme extractor wiring to HSTS behavior.

package apicore_test

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/labstack/echo/v4"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/tphakala/birdnet-go/internal/api/middleware"
	"github.com/tphakala/birdnet-go/internal/api/v2/apitest"
	"github.com/tphakala/birdnet-go/internal/conf"
)

const (
	hstsHeader      = "Strict-Transport-Security"
	hstsDefaultWant = "max-age=31536000; includeSubdomains"
)

// TestNewCore_HSTSBehindTrustedProxy verifies NewCore configures the Echo
// instance so HSTS follows the trusted-proxy list, including after a hot reload.
func TestNewCore_HSTSBehindTrustedProxy(t *testing.T) {
	t.Parallel()

	core := apitest.NewCore(t,
		apitest.WithSettingsFunc(func(s *conf.Settings) {
			s.Security.TrustedProxies = []string{conf.TrustedProxyCloudflarePreset}
		}),
		apitest.WithoutSettingsPublish(),
	)
	core.Echo.Use(middleware.NewSecureHeaders(middleware.DefaultSecurityConfig()))
	core.Echo.GET("/probe", func(c echo.Context) error { return c.NoContent(http.StatusNoContent) })

	hsts := func(peer string) string {
		req := httptest.NewRequest(http.MethodGet, "/probe", http.NoBody)
		req.RemoteAddr = peer
		req.Header.Set("X-Forwarded-Proto", "https")
		rec := httptest.NewRecorder()
		core.Echo.ServeHTTP(rec, req)
		require.Equal(t, http.StatusNoContent, rec.Code)
		return rec.Header().Get(hstsHeader)
	}

	assert.Equal(t, hstsDefaultWant, hsts("104.16.0.1:443"), "Cloudflare edge listed via preset")
	assert.Equal(t, hstsDefaultWant, hsts("127.0.0.1:1"), "loopback proxy")
	assert.Empty(t, hsts("203.0.113.50:1"), "unlisted public peer cannot choose the scheme")

	next := conf.CloneSettings(core.ControllerSettings())
	next.Security.TrustedProxies = []string{"100.64.0.0/10"}
	core.Settings.Store(next)

	assert.Equal(t, hstsDefaultWant, hsts("100.64.1.1:1"), "CGNAT proxy after hot reload")
	assert.Empty(t, hsts("104.16.0.1:443"), "Cloudflare edge after preset removed")
}
