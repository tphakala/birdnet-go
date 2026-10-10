// subnet_bypass_test.go: regression tests for the allowed-subnet bypass. The
// bypass must be decided on the verified client address, never on a forwarded
// client-IP header from a peer the operator has not configured as a proxy.

package auth

import (
	"net"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/gorilla/sessions"
	"github.com/labstack/echo/v4"
	"github.com/markbates/goth/gothic"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/tphakala/birdnet-go/internal/conf"
	"github.com/tphakala/birdnet-go/internal/security"
	"github.com/tphakala/birdnet-go/internal/security/securitytest"
)

const (
	// bypassSubnet is the configured bypass subnet (TEST-NET-3).
	bypassSubnet = "203.0.113.0/24"
	// insideBypassSubnet is an address inside bypassSubnet.
	insideBypassSubnet = "203.0.113.7"
	// lanPeer is a private peer outside bypassSubnet, standing in for another
	// device on the same LAN or container network.
	lanPeer = "172.25.5.9:40000"
	// configuredProxy is a reverse proxy listed in security.trustedproxies.
	configuredProxy = "10.0.0.2"
)

// newSubnetBypassMiddleware builds a Middleware with basic auth enabled and the
// subnet bypass limited to bypassSubnet. Not parallel: it publishes the global
// settings snapshot.
func newSubnetBypassMiddleware(t *testing.T, trustedProxies ...string) *Middleware {
	t.Helper()
	settings := &conf.Settings{}
	settings.Security.SessionSecret = "test-secret-32-bytes-minimum-len"
	settings.Security.BasicAuth.Enabled = true
	settings.Security.BasicAuth.ClientID = "admin"
	settings.Security.BasicAuth.Password = "correct-horse"
	settings.Security.AllowSubnetBypass.Enabled = true
	settings.Security.AllowSubnetBypass.Subnet = bypassSubnet
	settings.Security.TrustedProxies = trustedProxies

	previousStore := gothic.Store
	gothic.Store = sessions.NewCookieStore([]byte(settings.Security.SessionSecret))
	t.Cleanup(func() { gothic.Store = previousStore })

	return NewMiddleware(NewSecurityAdapter(securitytest.NewOAuth2ServerForTesting(t, settings)))
}

// TestSubnetBypassIgnoresForgedClientIPHeaders sends requests to a protected
// handler through the auth middleware with Echo's default extractors, which,
// like the production extractor, honor forwarded headers from private and
// loopback peers.
func TestSubnetBypassIgnoresForgedClientIPHeaders(t *testing.T) {
	extractors := map[string]echo.IPExtractor{
		"X-Forwarded-For": echo.ExtractIPFromXFFHeader(),
		"X-Real-IP":       echo.ExtractIPFromRealIPHeader(),
	}

	tests := []struct {
		name           string
		trustedProxies []string
		remoteAddr     string
		header         string
		value          string
		// forgedHeader and forgedValue add a second header the client forged,
		// passed through by a proxy that does not write it.
		forgedHeader string
		forgedValue  string
		wantStatus   int
		// peerMayBeLocal marks a case that relies on the peer being off the
		// test host's networks: a direct client on them is admitted by the
		// automatic local-network check, which is not what the case tests.
		peerMayBeLocal bool
	}{
		{name: "no forged header", remoteAddr: lanPeer, wantStatus: http.StatusUnauthorized, peerMayBeLocal: true},
		{name: "forged XFF inside bypass subnet", remoteAddr: lanPeer, header: "X-Forwarded-For", value: insideBypassSubnet, wantStatus: http.StatusUnauthorized},
		{name: "forged X-Real-IP inside bypass subnet", remoteAddr: lanPeer, header: "X-Real-IP", value: insideBypassSubnet, wantStatus: http.StatusUnauthorized},
		{name: "forged XFF loopback", remoteAddr: lanPeer, header: "X-Forwarded-For", value: "127.0.0.1", wantStatus: http.StatusUnauthorized},
		{name: "forged X-Real-IP loopback", remoteAddr: lanPeer, header: "X-Real-IP", value: "::1", wantStatus: http.StatusUnauthorized},
		{name: "unconfigured local proxy forwarding a bypass address", remoteAddr: "127.0.0.1:40000", header: "X-Forwarded-For", value: insideBypassSubnet, wantStatus: http.StatusUnauthorized},
		{name: "direct client inside bypass subnet", remoteAddr: insideBypassSubnet + ":40000", wantStatus: http.StatusOK},
		{name: "direct loopback client", remoteAddr: "127.0.0.1:40000", wantStatus: http.StatusOK},
		{name: "configured proxy forwarding a bypass client", trustedProxies: []string{configuredProxy}, remoteAddr: configuredProxy + ":40000", header: "X-Forwarded-For", value: insideBypassSubnet, wantStatus: http.StatusOK},
		{name: "X-Real-IP-only proxy passing a forged XFF", trustedProxies: []string{configuredProxy}, remoteAddr: configuredProxy + ":40000", header: echo.HeaderXRealIP, value: "198.51.100.9", forgedHeader: echo.HeaderXForwardedFor, forgedValue: insideBypassSubnet, wantStatus: http.StatusUnauthorized},
		{name: "configured proxy forwarding an outside client", trustedProxies: []string{configuredProxy}, remoteAddr: configuredProxy + ":40000", header: "X-Forwarded-For", value: "198.51.100.9", wantStatus: http.StatusUnauthorized},
	}

	for extractorName, extractor := range extractors {
		for _, tt := range tests {
			t.Run(extractorName+" extractor/"+tt.name, func(t *testing.T) {
				if tt.peerMayBeLocal {
					if peer, _, err := net.SplitHostPort(tt.remoteAddr); err == nil && security.IsInLocalSubnet(net.ParseIP(peer)) {
						t.Skipf("peer %s is on a network of this test host", peer)
					}
				}
				m := newSubnetBypassMiddleware(t, tt.trustedProxies...)

				e := echo.New()
				e.IPExtractor = extractor
				req := httptest.NewRequest(http.MethodGet, "/api/v2/settings", http.NoBody)
				req.RemoteAddr = tt.remoteAddr
				if tt.header != "" {
					req.Header.Set(tt.header, tt.value)
				}
				if tt.forgedHeader != "" {
					req.Header.Set(tt.forgedHeader, tt.forgedValue)
				}
				rec := httptest.NewRecorder()
				c := e.NewContext(req, rec)

				handlerRan := false
				err := m.Authenticate(func(c echo.Context) error {
					handlerRan = true
					return c.NoContent(http.StatusOK)
				})(c)

				bypassed := tt.wantStatus == http.StatusOK
				require.NoError(t, err)
				assert.Equal(t, tt.wantStatus, rec.Code)
				assert.Equal(t, bypassed, handlerRan, "protected handler ran")

				// The adapter's other bypass readers must agree with the middleware.
				// A fresh context keeps GetAuthMethod from returning the method the
				// middleware stored, so its own subnet check is what is tested.
				adapter, ok := m.AuthService.(*SecurityAdapter)
				require.True(t, ok, "middleware must use the security adapter")
				fresh := e.NewContext(req, httptest.NewRecorder())
				assert.Equal(t, !bypassed, adapter.IsAuthRequired(fresh), "IsAuthRequired")
				assert.Equal(t, bypassed, adapter.GetAuthMethod(fresh) == AuthMethodLocalSubnet, "GetAuthMethod reports the subnet bypass")
			})
		}
	}
}
