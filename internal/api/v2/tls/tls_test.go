// tls_test.go: tests for the API v2 TLS domain endpoints.

package tls

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/labstack/echo/v4"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/tphakala/birdnet-go/internal/api/v2/apicore"
	"github.com/tphakala/birdnet-go/internal/api/v2/apitest"
	"github.com/tphakala/birdnet-go/internal/conf"
	tlspkg "github.com/tphakala/birdnet-go/internal/tls"
)

// generateTestCertKeyPair creates a valid self-signed cert+key pair for testing.
func generateTestCertKeyPair(t *testing.T) (certPEM, keyPEM string) {
	t.Helper()
	cert, key, err := tlspkg.GenerateSelfSigned(tlspkg.SelfSignedOptions{
		Validity: 24 * time.Hour,
		SANs:     []string{"localhost", "127.0.0.1"},
	})
	require.NoError(t, err, "failed to generate test certificate")
	return cert, key
}

// newTestHandler builds a TLS Handler around a *apicore.Core via apitest, with
// in-memory test doubles for the facade's settings-save machinery. The doubles
// mirror the production behavior the original tests asserted: getSettingsOrFallback
// reads the core's atomic snapshot, publishAndSaveSettings stores the updated
// snapshot back (no disk write, matching the original DisableSaveSettings=true),
// and handleSettingsChanges is a no-op. They share the supplied mutex so the
// handler serialises just as the facade does.
func newTestHandler(t *testing.T, core *apicore.Core, mu *sync.RWMutex) *Handler {
	t.Helper()
	return New(core, mu,
		func() *conf.Settings { return core.Settings.Load() },
		func(_, updated *conf.Settings) error { core.Settings.Store(updated); return nil },
		func(_, _ *conf.Settings) error { return nil },
	)
}

// setupTLSTestEnvironment creates a test environment with Echo, an apicore.Core
// built via apitest, a TLS Handler, and a TLS manager pointing at a temporary
// directory. The returned Handler reads through the same core whose Settings the
// test doubles mutate, so assertions on the TLS mode observe the writes.
func setupTLSTestEnvironment(t *testing.T) (*echo.Echo, *Handler, *conf.TLSManager) {
	t.Helper()

	e := echo.New()
	core := apitest.NewCore(t, apitest.WithEcho(e))

	// Create a TLS manager using a temp directory
	tlsDir := t.TempDir()
	tlsMgr := conf.NewTLSManager(tlsDir)

	// Override the global TLS manager for the duration of this test
	origManager := conf.GetTLSManager()
	conf.SetTLSManagerForTest(tlsMgr)
	t.Cleanup(func() {
		conf.SetTLSManagerForTest(origManager)
	})

	var mu sync.RWMutex
	return e, newTestHandler(t, core, &mu), tlsMgr
}

func TestTLSGetCertificate_NoCert(t *testing.T) {
	e, controller, _ := setupTLSTestEnvironment(t)

	req := httptest.NewRequest(http.MethodGet, "/api/v2/tls/certificate", http.NoBody)
	rec := httptest.NewRecorder()
	ctx := e.NewContext(req, rec)

	err := controller.GetTLSCertificate(ctx)
	require.NoError(t, err)
	assert.Equal(t, http.StatusOK, rec.Code)

	var info TLSCertificateInfo
	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &info))
	assert.False(t, info.Installed, "should report no certificate installed")
}

func TestTLSGetCertificate_WithCert(t *testing.T) {
	e, controller, tlsMgr := setupTLSTestEnvironment(t)

	// Save a test certificate via TLS manager
	certPEM, keyPEM := generateTestCertKeyPair(t)
	_, err := tlsMgr.SaveCertificate(tlsServiceName, conf.TLSCertTypeServerCert, certPEM)
	require.NoError(t, err)
	_, err = tlsMgr.SaveCertificate(tlsServiceName, conf.TLSCertTypeServerKey, keyPEM)
	require.NoError(t, err)

	// Set TLS mode in settings
	controller.Settings.Load().Security.TLSMode = conf.TLSModeSelfSigned

	req := httptest.NewRequest(http.MethodGet, "/api/v2/tls/certificate", http.NoBody)
	rec := httptest.NewRecorder()
	ctx := e.NewContext(req, rec)

	err = controller.GetTLSCertificate(ctx)
	require.NoError(t, err)
	assert.Equal(t, http.StatusOK, rec.Code)

	var info TLSCertificateInfo
	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &info))
	assert.True(t, info.Installed, "should report certificate installed")
	assert.Equal(t, "CN=BirdNET-Go", info.Subject)
	assert.Equal(t, "selfsigned", info.Mode)
	assert.NotEmpty(t, info.NotBefore)
	assert.NotEmpty(t, info.NotAfter)
	assert.NotEmpty(t, info.SerialNumber)
	assert.NotEmpty(t, info.Fingerprint)
	assert.Greater(t, info.DaysUntilExpiry, -1)
	assert.NotEmpty(t, info.SANs)
}

func TestTLSUploadCertificate_ValidPEM(t *testing.T) {
	e, controller, _ := setupTLSTestEnvironment(t)

	certPEM, keyPEM := generateTestCertKeyPair(t)

	body, err := json.Marshal(certificateUpload{
		Certificate: certPEM,
		PrivateKey:  keyPEM,
	})
	require.NoError(t, err)

	req := httptest.NewRequest(http.MethodPost, "/api/v2/tls/certificate", strings.NewReader(string(body)))
	req.Header.Set(echo.HeaderContentType, echo.MIMEApplicationJSON)
	rec := httptest.NewRecorder()
	ctx := e.NewContext(req, rec)

	err = controller.UploadTLSCertificate(ctx)
	require.NoError(t, err)
	assert.Equal(t, http.StatusOK, rec.Code)

	var info TLSCertificateInfo
	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &info))
	assert.True(t, info.Installed)
	assert.Equal(t, "manual", info.Mode)
	assert.Equal(t, "CN=BirdNET-Go", info.Subject)

	// Verify settings were updated
	assert.Equal(t, conf.TLSModeManual, controller.Settings.Load().Security.TLSMode)
}

func TestTLSUploadCertificate_InvalidPEM(t *testing.T) {
	e, controller, _ := setupTLSTestEnvironment(t)

	body, err := json.Marshal(certificateUpload{
		Certificate: "not-a-valid-pem",
		PrivateKey:  "also-not-valid",
	})
	require.NoError(t, err)

	req := httptest.NewRequest(http.MethodPost, "/api/v2/tls/certificate", strings.NewReader(string(body)))
	req.Header.Set(echo.HeaderContentType, echo.MIMEApplicationJSON)
	rec := httptest.NewRecorder()
	ctx := e.NewContext(req, rec)

	err = controller.UploadTLSCertificate(ctx)
	require.NoError(t, err)
	assert.Equal(t, http.StatusBadRequest, rec.Code)
}

func TestTLSUploadCertificate_MismatchedKeyPair(t *testing.T) {
	e, controller, _ := setupTLSTestEnvironment(t)

	// Generate two separate cert+key pairs
	certPEM1, _ := generateTestCertKeyPair(t)
	_, keyPEM2 := generateTestCertKeyPair(t)

	// Upload cert from pair 1 with key from pair 2
	body, err := json.Marshal(certificateUpload{
		Certificate: certPEM1,
		PrivateKey:  keyPEM2,
	})
	require.NoError(t, err)

	req := httptest.NewRequest(http.MethodPost, "/api/v2/tls/certificate", strings.NewReader(string(body)))
	req.Header.Set(echo.HeaderContentType, echo.MIMEApplicationJSON)
	rec := httptest.NewRecorder()
	ctx := e.NewContext(req, rec)

	err = controller.UploadTLSCertificate(ctx)
	require.NoError(t, err)
	assert.Equal(t, http.StatusBadRequest, rec.Code)
}

func TestTLSDeleteCertificate(t *testing.T) {
	e, controller, tlsMgr := setupTLSTestEnvironment(t)

	// First save a certificate
	certPEM, keyPEM := generateTestCertKeyPair(t)
	_, err := tlsMgr.SaveCertificate(tlsServiceName, conf.TLSCertTypeServerCert, certPEM)
	require.NoError(t, err)
	_, err = tlsMgr.SaveCertificate(tlsServiceName, conf.TLSCertTypeServerKey, keyPEM)
	require.NoError(t, err)

	// Set TLS mode
	controller.Settings.Load().Security.TLSMode = conf.TLSModeManual

	// Delete the certificate
	req := httptest.NewRequest(http.MethodDelete, "/api/v2/tls/certificate", http.NoBody)
	rec := httptest.NewRecorder()
	ctx := e.NewContext(req, rec)

	err = controller.DeleteTLSCertificate(ctx)
	require.NoError(t, err)
	assert.Equal(t, http.StatusNoContent, rec.Code)

	// Verify certificate was removed
	assert.False(t, tlsMgr.CertificateExists(tlsServiceName, conf.TLSCertTypeServerCert))
	assert.False(t, tlsMgr.CertificateExists(tlsServiceName, conf.TLSCertTypeServerKey))

	// Verify TLS mode was reset
	assert.Equal(t, conf.TLSModeNone, controller.Settings.Load().Security.TLSMode)
}

func TestTLSGenerateSelfSignedCertificate(t *testing.T) {
	e, controller, _ := setupTLSTestEnvironment(t)

	body, err := json.Marshal(generateRequest{
		Validity: "30d",
	})
	require.NoError(t, err)

	req := httptest.NewRequest(http.MethodPost, "/api/v2/tls/certificate/generate", strings.NewReader(string(body)))
	req.Header.Set(echo.HeaderContentType, echo.MIMEApplicationJSON)
	rec := httptest.NewRecorder()
	ctx := e.NewContext(req, rec)

	err = controller.GenerateSelfSignedCertificate(ctx)
	require.NoError(t, err)
	assert.Equal(t, http.StatusOK, rec.Code)

	var info TLSCertificateInfo
	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &info))
	assert.True(t, info.Installed)
	assert.Equal(t, "selfsigned", info.Mode)
	assert.Equal(t, "CN=BirdNET-Go", info.Subject)
	assert.NotEmpty(t, info.SANs)
	assert.Positive(t, info.DaysUntilExpiry)

	// Verify settings were updated
	assert.Equal(t, conf.TLSModeSelfSigned, controller.Settings.Load().Security.TLSMode)
}

func TestTLSGenerateSelfSignedCertificate_DefaultValidity(t *testing.T) {
	e, controller, _ := setupTLSTestEnvironment(t)

	// Empty body should use default validity
	req := httptest.NewRequest(http.MethodPost, "/api/v2/tls/certificate/generate",
		strings.NewReader("{}"))
	req.Header.Set(echo.HeaderContentType, echo.MIMEApplicationJSON)
	rec := httptest.NewRecorder()
	ctx := e.NewContext(req, rec)

	err := controller.GenerateSelfSignedCertificate(ctx)
	require.NoError(t, err)
	assert.Equal(t, http.StatusOK, rec.Code)

	var info TLSCertificateInfo
	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &info))
	assert.True(t, info.Installed)
	// Default validity is 5 years (1825 days)
	assert.InDelta(t, 1825, info.DaysUntilExpiry, 2) // 5 years ± 2 days
}

func TestTLSDownloadCertificate(t *testing.T) {
	e, controller, tlsMgr := setupTLSTestEnvironment(t)

	// Save a test certificate
	certPEM, keyPEM := generateTestCertKeyPair(t)
	_, err := tlsMgr.SaveCertificate(tlsServiceName, conf.TLSCertTypeServerCert, certPEM)
	require.NoError(t, err)
	_, err = tlsMgr.SaveCertificate(tlsServiceName, conf.TLSCertTypeServerKey, keyPEM)
	require.NoError(t, err)

	req := httptest.NewRequest(http.MethodGet, "/api/v2/tls/certificate/download", http.NoBody)
	rec := httptest.NewRecorder()
	ctx := e.NewContext(req, rec)

	err = controller.DownloadTLSCertificate(ctx)
	require.NoError(t, err)
	assert.Equal(t, http.StatusOK, rec.Code)
	assert.Contains(t, rec.Header().Get("Content-Disposition"), "birdnet-go.crt")
	assert.Contains(t, rec.Body.String(), "BEGIN CERTIFICATE")
}

func TestTLSDownloadCertificate_NoCert(t *testing.T) {
	e, controller, _ := setupTLSTestEnvironment(t)

	req := httptest.NewRequest(http.MethodGet, "/api/v2/tls/certificate/download", http.NoBody)
	rec := httptest.NewRecorder()
	ctx := e.NewContext(req, rec)

	err := controller.DownloadTLSCertificate(ctx)
	require.NoError(t, err)
	assert.Equal(t, http.StatusNotFound, rec.Code)
}

func TestTLSRouteRegistration(t *testing.T) {
	e := echo.New()
	core := apitest.NewCore(t, apitest.WithEcho(e))
	var mu sync.RWMutex
	h := newTestHandler(t, core, &mu)

	h.RegisterRoutes(core.Group)

	expectedRoutes := []string{
		"GET /api/v2/tls/certificate",
		"POST /api/v2/tls/certificate",
		"DELETE /api/v2/tls/certificate",
		"POST /api/v2/tls/certificate/generate",
		"GET /api/v2/tls/certificate/download",
	}
	apitest.AssertRoutesRegistered(t, e, expectedRoutes)
}
