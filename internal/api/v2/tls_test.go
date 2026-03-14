package api

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/labstack/echo/v4"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
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

// setupTLSTestEnvironment creates a test environment with a TLS manager
// pointing at a temporary directory.
func setupTLSTestEnvironment(t *testing.T) (*echo.Echo, *Controller, *conf.TLSManager) {
	t.Helper()

	e, _, controller := setupTestEnvironment(t)

	// Create a TLS manager using a temp directory
	tlsDir := t.TempDir()
	tlsMgr := conf.NewTLSManager(tlsDir)

	// Override the global TLS manager for the duration of this test
	origManager := conf.GetTLSManager()
	conf.SetTLSManagerForTest(tlsMgr)
	t.Cleanup(func() {
		conf.SetTLSManagerForTest(origManager)
	})

	// Disable settings persistence in tests
	controller.DisableSaveSettings = true

	return e, controller, tlsMgr
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
	controller.settingsMutex.Lock()
	controller.Settings.Security.TLSMode = conf.TLSModeSelfSigned
	controller.settingsMutex.Unlock()

	req := httptest.NewRequest(http.MethodGet, "/api/v2/tls/certificate", http.NoBody)
	rec := httptest.NewRecorder()
	ctx := e.NewContext(req, rec)

	err = controller.GetTLSCertificate(ctx)
	require.NoError(t, err)
	assert.Equal(t, http.StatusOK, rec.Code)

	var info TLSCertificateInfo
	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &info))
	assert.True(t, info.Installed, "should report certificate installed")
	assert.Equal(t, "BirdNET-Go", info.Subject)
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

	body, err := json.Marshal(TLSCertificateUpload{
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
	assert.Equal(t, "BirdNET-Go", info.Subject)

	// Verify settings were updated
	controller.settingsMutex.RLock()
	assert.Equal(t, conf.TLSModeManual, controller.Settings.Security.TLSMode)
	controller.settingsMutex.RUnlock()
}

func TestTLSUploadCertificate_InvalidPEM(t *testing.T) {
	e, controller, _ := setupTLSTestEnvironment(t)

	body, err := json.Marshal(TLSCertificateUpload{
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
	body, err := json.Marshal(TLSCertificateUpload{
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
	controller.settingsMutex.Lock()
	controller.Settings.Security.TLSMode = conf.TLSModeManual
	controller.settingsMutex.Unlock()

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
	controller.settingsMutex.RLock()
	assert.Equal(t, conf.TLSModeNone, controller.Settings.Security.TLSMode)
	controller.settingsMutex.RUnlock()
}

func TestTLSGenerateSelfSignedCertificate(t *testing.T) {
	e, controller, _ := setupTLSTestEnvironment(t)

	body, err := json.Marshal(TLSGenerateRequest{
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
	assert.Equal(t, "BirdNET-Go", info.Subject)
	assert.NotEmpty(t, info.SANs)
	assert.Positive(t, info.DaysUntilExpiry)

	// Verify settings were updated
	controller.settingsMutex.RLock()
	assert.Equal(t, conf.TLSModeSelfSigned, controller.Settings.Security.TLSMode)
	controller.settingsMutex.RUnlock()
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
	// Default validity is 365 days, so DaysUntilExpiry should be around 364-365
	assert.Greater(t, info.DaysUntilExpiry, 360)
}

func TestTLSRouteRegistration(t *testing.T) {
	e, _, controller := setupTestEnvironment(t)

	controller.initTLSRoutes()

	expectedRoutes := []string{
		"GET /api/v2/tls/certificate",
		"POST /api/v2/tls/certificate",
		"DELETE /api/v2/tls/certificate",
		"POST /api/v2/tls/certificate/generate",
	}
	assertRoutesRegistered(t, e, expectedRoutes)
}
