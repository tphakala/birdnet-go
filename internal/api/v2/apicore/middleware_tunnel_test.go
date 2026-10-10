// middleware_tunnel_test.go: tests for TunnelDetectionMiddleware.

package apicore

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/labstack/echo/v4"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestTunnelDetectionMiddleware verifies a request is labeled tunneled only when
// the IP extractor honored a forwarded header, so an untrusted direct client
// cannot spoof the label.
func TestTunnelDetectionMiddleware(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name         string
		remoteAddr   string
		header       string
		value        string
		wantTunneled bool
		wantProvider string
	}{
		{name: "trusted peer with CF-Connecting-IP", remoteAddr: "127.0.0.1:40000", header: headerCFConnectingIP, value: "198.51.100.9", wantTunneled: true, wantProvider: "cloudflare"},
		{name: "trusted peer with XFF", remoteAddr: "192.168.1.10:40000", header: echo.HeaderXForwardedFor, value: "198.51.100.9", wantTunneled: true, wantProvider: "generic"},
		{name: "untrusted peer with XFF", remoteAddr: testPublicPeerAddr, header: echo.HeaderXForwardedFor, value: "198.51.100.9", wantTunneled: false},
		{name: "direct client", remoteAddr: "192.168.1.10:40000", wantTunneled: false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			e := echo.New()
			e.IPExtractor = newTrustedProxyIPExtractor(nil)
			req := httptest.NewRequest(http.MethodGet, "/", http.NoBody)
			req.RemoteAddr = tt.remoteAddr
			if tt.header != "" {
				req.Header.Set(tt.header, tt.value)
			}
			ctx := e.NewContext(req, httptest.NewRecorder())

			err := (&Core{}).TunnelDetectionMiddleware()(func(echo.Context) error { return nil })(ctx)
			require.NoError(t, err)
			assert.Equal(t, tt.wantTunneled, ctx.Get(CtxKeyIsTunneled))
			if tt.wantTunneled {
				assert.Equal(t, tt.wantProvider, ctx.Get(CtxKeyTunnelProvider))
			}
		})
	}
}
