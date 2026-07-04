//go:build integration && pebble_e2e

// End-to-end AutoTLS validation against a real ACME server (Pebble) running as a
// testcontainer. It drives the REAL startBlocking() dual-listener path through a
// full ACME challenge validation and issuance, and asserts that (1) the CA
// validates a challenge against the server's own listeners and issues a
// certificate, (2) the HTTP-01 challenge listener serves ACME challenge paths
// itself instead of redirecting them, and (3) non-ACME HTTP traffic 308-redirects
// to the advertised host. This is the merge-gate regression proof for the AutoTLS
// dual-listener fix (GitHub #3527): the earlier bug called StartAutoTLS on the
// HTTP port with no separate HTTP-01 listener, so AutoTLS could never validate.
//
// The stock binary cannot point AutoTLS at a non-Let's-Encrypt directory, so the
// test injects the ACME client (pointed at Pebble) directly on the AutoTLSManager,
// which startBlocking leaves intact. Build+run with:
//
//	go test -tags=integration,pebble_e2e -run TestAutoTLS_Pebble ./internal/api/ -count=1 -v
//
// Two implementation notes:
//
//   - Success signal: autocert cannot retrieve the issued certificate from Pebble
//     (Pebble omits the Location header on its finalize response, which x/crypto's
//     CreateOrderCert requires to poll the order; this works against real Let's
//     Encrypt). The test therefore confirms issuance from Pebble's own server side
//     (WaitForIssuedCertificate) rather than by serving the cert back. That
//     server-side confirmation is exactly what #3527 broke: with no HTTP-01 listener
//     the CA could never validate, so no cert would issue.
//   - Issuance runs through the TLS-ALPN-01 challenge (validation ports aligned with
//     the server). A forced HTTP-01 issuance is not exercised because autocert's
//     HostWhitelist compares the full Host header and Pebble validates HTTP-01 on the
//     non-standard configured port, so the request arrives as "host:port" and is
//     rejected; real Let's Encrypt validates HTTP-01 on port 80 with a bare-domain
//     Host, so this only affects Pebble. The HTTP-01 listener is still asserted
//     directly (assertion 2) to guard the #3527 regression.
package api

import (
	"context"
	"crypto/tls"
	"fmt"
	"net"
	"net/http"
	"path/filepath"
	"strconv"
	"sync"
	"testing"
	"time"

	"github.com/labstack/echo/v4"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"golang.org/x/crypto/acme"
	"golang.org/x/crypto/acme/autocert"

	"github.com/tphakala/birdnet-go/internal/conf"
	"github.com/tphakala/birdnet-go/internal/conf/conftest"
	"github.com/tphakala/birdnet-go/internal/testutil/containers"
)

// issuanceDeadline bounds how long the test waits for Pebble to validate a
// challenge and issue a certificate.
const issuanceDeadline = 45 * time.Second

func TestAutoTLS_Pebble_EndToEnd(t *testing.T) {
	acmeHost := containers.DefaultPebbleChallengeHost
	httpPort := mustGetFreePort(t)
	tlsPort := mustGetFreePort(t)

	// Start the ACME server. Its challenge validation must target the exact host
	// ports the server binds below, and resolve acmeHost to this host.
	startCtx, cancel := context.WithTimeout(t.Context(), 2*time.Minute)
	t.Cleanup(cancel)
	pebble, err := containers.NewPebbleContainer(startCtx, &containers.PebbleConfig{
		ChallengeHost:      acmeHost,
		HTTPValidationPort: mustAtoi(t, httpPort),
		TLSValidationPort:  mustAtoi(t, tlsPort),
	})
	require.NoError(t, err, "failed to start Pebble ACME test server")
	t.Cleanup(func() {
		termCtx, termCancel := context.WithTimeout(context.Background(), 30*time.Second) //nolint:gocritic // t.Context() is already cancelled in Cleanup
		defer termCancel()
		assert.NoError(t, pebble.Terminate(termCtx))
	})

	// Build an AutoTLS config through the real path under test.
	settings := conftest.NewTestSettings().WithWebServer(httpPort, true).Build()
	settings.Security.TLSMode = conf.TLSModeAutoTLS
	settings.Security.Host = acmeHost
	settings.Security.TLSPort = tlsPort
	conftest.SetTestSettings(settings)

	// Point config discovery at a non-existent path so startBlocking's cache setup
	// (conf.FindConfigFile) fails deterministically and leaves the injected
	// AutoTLSManager.Cache untouched, rather than writing certs into a real config
	// directory.
	origConfigPath := conf.ConfigPath
	conf.ConfigPath = filepath.Join(t.TempDir(), "nonexistent-config.yaml")
	t.Cleanup(func() { conf.ConfigPath = origConfigPath })

	cfg := ConfigFromSettings(settings)
	require.True(t, cfg.RedirectToHTTPS, "AutoTLS must default redirect on")
	require.Equal(t, acmeHost, cfg.RedirectAuthority, "redirect authority is the advertised host")
	require.Equal(t, tlsPort, cfg.TLSPort)

	e := echo.New()
	e.HideBanner = true
	e.HidePort = true

	// Point AutoTLS at Pebble. startBlocking sets Cache + HostPolicy but never
	// Client, so this injection is exactly what the shipped binary lacks. Pre-discover
	// the directory so autocert's account registration (which can run before its own
	// discovery) has populated endpoint URLs.
	acmeClient := &acme.Client{DirectoryURL: pebble.DirectoryURL(), HTTPClient: pebble.APIHTTPClient()}
	_, err = acmeClient.Discover(startCtx)
	require.NoError(t, err, "ACME directory discovery against Pebble must succeed")
	e.AutoTLSManager.Client = acmeClient
	e.AutoTLSManager.Cache = autocert.DirCache(t.TempDir())
	e.AutoTLSManager.Prompt = autocert.AcceptTOS

	s := &Server{config: cfg, settings: settings, echo: e, slogger: GetLogger()}
	t.Cleanup(func() {
		ctx, ccancel := context.WithTimeout(context.Background(), 3*time.Second) //nolint:gocritic // t.Context() is already cancelled in Cleanup
		defer ccancel()
		_ = s.echo.Shutdown(ctx)
		if srv := s.httpRedirectServer.Load(); srv != nil {
			_ = srv.Shutdown(ctx)
		}
	})
	go func() { _ = s.startBlocking() }()

	requirePortOpen(t, httpPort)
	requirePortOpen(t, tlsPort)

	// (1) Drive a full ACME issuance through the real dual-listener path and confirm
	// it from the CA's server side. A client TLS handshake to the TLS listener makes
	// the server's own autocert run one ACME order whose challenge Pebble validates
	// against the TLS-ALPN-01 listener. The background loop keeps handshaking until
	// Pebble confirms issuance (see the file header for why we confirm server-side
	// rather than via a served handshake).
	stopTrigger := startACMEIssuanceTrigger(t, tlsPort, acmeHost)
	waitErr := pebble.WaitForIssuedCertificate(startCtx, issuanceDeadline)
	stopTrigger()
	require.NoError(t, waitErr,
		"Pebble must validate a challenge against the server listeners and issue a cert, proving the dual-listener ACME flow works end-to-end")

	nonRedirectClient := &http.Client{
		CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse },
		Timeout:       5 * time.Second,
	}

	// (2) The HTTP listener must serve ACME challenge paths itself (via
	// autocert.HTTPHandler), not 308-redirect them: that is the whole point of the
	// dual-listener fix. The request uses a bare-domain Host so it passes autocert's
	// HostWhitelist; an unknown token then yields 404 (handled by the ACME path, not
	// redirected).
	acmeReq, err := http.NewRequest(http.MethodGet,
		fmt.Sprintf("http://127.0.0.1:%s/.well-known/acme-challenge/probe-token", httpPort), http.NoBody)
	require.NoError(t, err)
	acmeReq.Host = acmeHost
	acmeProbe, err := nonRedirectClient.Do(acmeReq)
	require.NoError(t, err)
	_ = acmeProbe.Body.Close()
	assert.Equal(t, http.StatusNotFound, acmeProbe.StatusCode,
		"ACME challenge path must be handled by the HTTP listener (404 for unknown token), not redirected to HTTPS")

	// (3) Non-ACME HTTP traffic 308-redirects to the advertised HTTPS host, not the
	// internal TLS port.
	resp, err := nonRedirectClient.Get(fmt.Sprintf("http://127.0.0.1:%s/dashboard", httpPort))
	require.NoError(t, err)
	_ = resp.Body.Close()
	assert.Equal(t, http.StatusPermanentRedirect, resp.StatusCode, "non-ACME HTTP must 308-redirect")
	assert.Equal(t, "https://"+acmeHost+"/dashboard", resp.Header.Get("Location"),
		"redirect targets the advertised host, not the internal TLS port")
}

// startACMEIssuanceTrigger repeatedly handshakes the server's TLS listener in the
// background to drive ACME issuance against the challenge listeners. The handshake
// runs the server's own autocert (on the server goroutine, avoiding any data race
// on the AutoTLSManager fields that startBlocking sets). The handshake itself fails
// because autocert cannot retrieve the issued cert from Pebble; that is expected
// and ignored, since the CA has already validated and issued by that point, which
// is what the test asserts. It returns a stop function, safe to call once.
func startACMEIssuanceTrigger(t *testing.T, tlsPort, host string) func() {
	t.Helper()
	stop := make(chan struct{})
	addr := net.JoinHostPort("127.0.0.1", tlsPort)
	triggerCtx := t.Context()
	go func() {
		// We do not verify the served cert here (issuance is confirmed via Pebble),
		// so skip verification; the handshake exists only to trigger issuance.
		cfg := &tls.Config{ServerName: host, InsecureSkipVerify: true} //nolint:gosec // triggers issuance only; not verifying
		for {
			dctx, dcancel := context.WithTimeout(triggerCtx, 10*time.Second)
			conn, err := (&tls.Dialer{Config: cfg}).DialContext(dctx, "tcp", addr)
			dcancel()
			if err == nil {
				_ = conn.Close()
			}
			select {
			case <-stop:
				return
			case <-time.After(2 * time.Second):
			}
		}
	}()
	var once sync.Once
	stopFn := func() { once.Do(func() { close(stop) }) }
	// Always stop the loop, even if the test fails before the explicit call.
	t.Cleanup(stopFn)
	return stopFn
}

// mustAtoi parses a numeric string port or fails the test.
func mustAtoi(t *testing.T, s string) int {
	t.Helper()
	n, err := strconv.Atoi(s)
	require.NoError(t, err, "port %q must be numeric", s)
	return n
}
