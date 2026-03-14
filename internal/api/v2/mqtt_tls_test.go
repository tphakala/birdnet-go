package api

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/tphakala/birdnet-go/internal/conf"
)

// TestGetMQTTTLSCertificate_NoCerts verifies that when no certificates are installed,
// all fields report as not installed.
func TestGetMQTTTLSCertificate_NoCerts(t *testing.T) {
	e, controller, _ := setupTLSTestEnvironment(t)

	req := httptest.NewRequest(http.MethodGet, "/api/v2/integrations/mqtt/tls/certificate", http.NoBody)
	rec := httptest.NewRecorder()
	ctx := e.NewContext(req, rec)

	err := controller.GetMQTTTLSCertificate(ctx)
	require.NoError(t, err)
	assert.Equal(t, http.StatusOK, rec.Code)

	var info MQTTTLSCertificateInfo
	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &info))
	require.NotNil(t, info.CA)
	require.NotNil(t, info.Client)
	assert.False(t, info.CA.Installed, "CA should not be installed")
	assert.False(t, info.Client.Installed, "client should not be installed")
	assert.False(t, info.HasKey, "key should not be present")
}

// TestGetMQTTTLSCertificate_WithManualPaths verifies that manually configured
// certificate paths in settings are recognized and parsed correctly.
func TestGetMQTTTLSCertificate_WithManualPaths(t *testing.T) {
	e, controller, _ := setupTLSTestEnvironment(t)

	certPEM, _ := generateTestCertKeyPair(t)

	// Write cert to a temp file outside the managed dir
	certFile := filepath.Join(t.TempDir(), "ca.crt")
	require.NoError(t, os.WriteFile(certFile, []byte(certPEM), 0o644))

	// Point settings to this file
	controller.settingsMutex.Lock()
	controller.Settings.Realtime.MQTT.TLS.CACert = certFile
	controller.settingsMutex.Unlock()

	req := httptest.NewRequest(http.MethodGet, "/api/v2/integrations/mqtt/tls/certificate", http.NoBody)
	rec := httptest.NewRecorder()
	ctx := e.NewContext(req, rec)

	err := controller.GetMQTTTLSCertificate(ctx)
	require.NoError(t, err)
	assert.Equal(t, http.StatusOK, rec.Code)

	var info MQTTTLSCertificateInfo
	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &info))
	require.NotNil(t, info.CA)
	assert.True(t, info.CA.Installed, "CA should be installed")
	assert.Equal(t, "CN=BirdNET-Go", info.CA.Subject)
	assert.False(t, info.Client.Installed, "client should not be installed")
	assert.False(t, info.HasKey, "key should not be present")
}

// TestUploadMQTTTLSCertificate_CAOnly verifies that uploading only a CA certificate
// sets CA as installed while client cert remains uninstalled.
func TestUploadMQTTTLSCertificate_CAOnly(t *testing.T) {
	e, controller, _ := setupTLSTestEnvironment(t)

	certPEM, _ := generateTestCertKeyPair(t)

	caStr := certPEM
	body, err := json.Marshal(MQTTTLSCertificateUpload{CACertificate: &caStr})
	require.NoError(t, err)

	req := httptest.NewRequest(http.MethodPost, "/api/v2/integrations/mqtt/tls/certificate", strings.NewReader(string(body)))
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	ctx := e.NewContext(req, rec)

	err = controller.UploadMQTTTLSCertificate(ctx)
	require.NoError(t, err)
	assert.Equal(t, http.StatusOK, rec.Code)

	var info MQTTTLSCertificateInfo
	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &info))
	require.NotNil(t, info.CA)
	assert.True(t, info.CA.Installed, "CA should be installed")
	assert.Equal(t, "CN=BirdNET-Go", info.CA.Subject)
	assert.InDelta(t, 1, info.CA.DaysUntilExpiry, 1) // 24h cert ± 1 day
	require.NotNil(t, info.Client)
	assert.False(t, info.Client.Installed, "client should not be installed")
	assert.False(t, info.HasKey, "key should not be present")
}

// TestUploadMQTTTLSCertificate_ClientCertAndKey verifies that uploading a client cert
// and key pair results in both being installed.
func TestUploadMQTTTLSCertificate_ClientCertAndKey(t *testing.T) {
	e, controller, _ := setupTLSTestEnvironment(t)

	certPEM, keyPEM := generateTestCertKeyPair(t)

	clientStr := certPEM
	keyStr := keyPEM
	body, err := json.Marshal(MQTTTLSCertificateUpload{
		ClientCertificate: &clientStr,
		ClientKey:         &keyStr,
	})
	require.NoError(t, err)

	req := httptest.NewRequest(http.MethodPost, "/api/v2/integrations/mqtt/tls/certificate", strings.NewReader(string(body)))
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	ctx := e.NewContext(req, rec)

	err = controller.UploadMQTTTLSCertificate(ctx)
	require.NoError(t, err)
	assert.Equal(t, http.StatusOK, rec.Code)

	var info MQTTTLSCertificateInfo
	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &info))
	require.NotNil(t, info.CA)
	assert.False(t, info.CA.Installed, "CA should not be installed")
	require.NotNil(t, info.Client)
	assert.True(t, info.Client.Installed, "client cert should be installed")
	assert.Equal(t, "CN=BirdNET-Go", info.Client.Subject)
	assert.True(t, info.HasKey, "client key should be present")
}

// TestUploadMQTTTLSCertificate_AllThree verifies that uploading CA, client cert, and key
// results in all three being installed.
func TestUploadMQTTTLSCertificate_AllThree(t *testing.T) {
	e, controller, _ := setupTLSTestEnvironment(t)

	certPEM, keyPEM := generateTestCertKeyPair(t)

	caStr := certPEM
	clientStr := certPEM
	keyStr := keyPEM
	body, err := json.Marshal(MQTTTLSCertificateUpload{
		CACertificate:     &caStr,
		ClientCertificate: &clientStr,
		ClientKey:         &keyStr,
	})
	require.NoError(t, err)

	req := httptest.NewRequest(http.MethodPost, "/api/v2/integrations/mqtt/tls/certificate", strings.NewReader(string(body)))
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	ctx := e.NewContext(req, rec)

	err = controller.UploadMQTTTLSCertificate(ctx)
	require.NoError(t, err)
	assert.Equal(t, http.StatusOK, rec.Code)

	var info MQTTTLSCertificateInfo
	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &info))
	require.NotNil(t, info.CA)
	assert.True(t, info.CA.Installed, "CA should be installed")
	require.NotNil(t, info.Client)
	assert.True(t, info.Client.Installed, "client cert should be installed")
	assert.True(t, info.HasKey, "client key should be present")
}

// TestUploadMQTTTLSCertificate_CertWithoutKey verifies that providing a client cert
// without a key returns 400.
func TestUploadMQTTTLSCertificate_CertWithoutKey(t *testing.T) {
	e, controller, _ := setupTLSTestEnvironment(t)

	certPEM, _ := generateTestCertKeyPair(t)

	clientStr := certPEM
	body, err := json.Marshal(MQTTTLSCertificateUpload{ClientCertificate: &clientStr})
	require.NoError(t, err)

	req := httptest.NewRequest(http.MethodPost, "/api/v2/integrations/mqtt/tls/certificate", strings.NewReader(string(body)))
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	ctx := e.NewContext(req, rec)

	err = controller.UploadMQTTTLSCertificate(ctx)
	require.NoError(t, err)
	assert.Equal(t, http.StatusBadRequest, rec.Code)
}

// TestUploadMQTTTLSCertificate_KeyWithoutCert verifies that providing a client key
// without a cert returns 400.
func TestUploadMQTTTLSCertificate_KeyWithoutCert(t *testing.T) {
	e, controller, _ := setupTLSTestEnvironment(t)

	_, keyPEM := generateTestCertKeyPair(t)

	keyStr := keyPEM
	body, err := json.Marshal(MQTTTLSCertificateUpload{ClientKey: &keyStr})
	require.NoError(t, err)

	req := httptest.NewRequest(http.MethodPost, "/api/v2/integrations/mqtt/tls/certificate", strings.NewReader(string(body)))
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	ctx := e.NewContext(req, rec)

	err = controller.UploadMQTTTLSCertificate(ctx)
	require.NoError(t, err)
	assert.Equal(t, http.StatusBadRequest, rec.Code)
}

// TestUploadMQTTTLSCertificate_EmptyRequest verifies that an empty JSON object
// (all nil pointer fields) returns 400.
func TestUploadMQTTTLSCertificate_EmptyRequest(t *testing.T) {
	e, controller, _ := setupTLSTestEnvironment(t)

	req := httptest.NewRequest(http.MethodPost, "/api/v2/integrations/mqtt/tls/certificate", strings.NewReader("{}"))
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	ctx := e.NewContext(req, rec)

	err := controller.UploadMQTTTLSCertificate(ctx)
	require.NoError(t, err)
	assert.Equal(t, http.StatusBadRequest, rec.Code)
}

// TestUploadMQTTTLSCertificate_ClearCA verifies that after uploading all three certs,
// sending an empty CA string clears the CA while preserving the client cert and key.
func TestUploadMQTTTLSCertificate_ClearCA(t *testing.T) {
	e, controller, _ := setupTLSTestEnvironment(t)

	certPEM, keyPEM := generateTestCertKeyPair(t)

	// First upload all three
	caStr := certPEM
	clientStr := certPEM
	keyStr := keyPEM
	allBody, err := json.Marshal(MQTTTLSCertificateUpload{
		CACertificate:     &caStr,
		ClientCertificate: &clientStr,
		ClientKey:         &keyStr,
	})
	require.NoError(t, err)

	req := httptest.NewRequest(http.MethodPost, "/api/v2/integrations/mqtt/tls/certificate", strings.NewReader(string(allBody)))
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	ctx := e.NewContext(req, rec)

	err = controller.UploadMQTTTLSCertificate(ctx)
	require.NoError(t, err)
	require.Equal(t, http.StatusOK, rec.Code)

	// Now clear only the CA
	emptyStr := ""
	clearBody, err := json.Marshal(MQTTTLSCertificateUpload{CACertificate: &emptyStr})
	require.NoError(t, err)

	req2 := httptest.NewRequest(http.MethodPost, "/api/v2/integrations/mqtt/tls/certificate", strings.NewReader(string(clearBody)))
	req2.Header.Set("Content-Type", "application/json")
	rec2 := httptest.NewRecorder()
	ctx2 := e.NewContext(req2, rec2)

	err = controller.UploadMQTTTLSCertificate(ctx2)
	require.NoError(t, err)
	assert.Equal(t, http.StatusOK, rec2.Code)

	var info MQTTTLSCertificateInfo
	require.NoError(t, json.Unmarshal(rec2.Body.Bytes(), &info))
	require.NotNil(t, info.CA)
	assert.False(t, info.CA.Installed, "CA should be cleared")
	require.NotNil(t, info.Client)
	assert.True(t, info.Client.Installed, "client cert should still be installed")
	assert.True(t, info.HasKey, "client key should still be present")
}

// TestDeleteMQTTTLSCertificate verifies that DELETE removes all managed certs and
// a subsequent GET shows nothing installed.
func TestDeleteMQTTTLSCertificate(t *testing.T) {
	e, controller, tlsMgr := setupTLSTestEnvironment(t)

	// Upload certs first via TLS manager directly
	certPEM, keyPEM := generateTestCertKeyPair(t)
	_, err := tlsMgr.SaveCertificate(mqttTLSServiceName, conf.TLSCertTypeCA, certPEM)
	require.NoError(t, err)
	_, err = tlsMgr.SaveCertificate(mqttTLSServiceName, conf.TLSCertTypeClient, certPEM)
	require.NoError(t, err)
	_, err = tlsMgr.SaveCertificate(mqttTLSServiceName, conf.TLSCertTypeKey, keyPEM)
	require.NoError(t, err)

	// Update settings paths
	controller.settingsMutex.Lock()
	controller.Settings.Realtime.MQTT.TLS.CACert = tlsMgr.GetCertificatePath(mqttTLSServiceName, conf.TLSCertTypeCA)
	controller.Settings.Realtime.MQTT.TLS.ClientCert = tlsMgr.GetCertificatePath(mqttTLSServiceName, conf.TLSCertTypeClient)
	controller.Settings.Realtime.MQTT.TLS.ClientKey = tlsMgr.GetCertificatePath(mqttTLSServiceName, conf.TLSCertTypeKey)
	controller.settingsMutex.Unlock()

	// DELETE
	req := httptest.NewRequest(http.MethodDelete, "/api/v2/integrations/mqtt/tls/certificate", http.NoBody)
	rec := httptest.NewRecorder()
	ctx := e.NewContext(req, rec)

	err = controller.DeleteMQTTTLSCertificate(ctx)
	require.NoError(t, err)
	assert.Equal(t, http.StatusNoContent, rec.Code)

	// Verify files are gone
	assert.False(t, tlsMgr.CertificateExists(mqttTLSServiceName, conf.TLSCertTypeCA), "CA should be removed")
	assert.False(t, tlsMgr.CertificateExists(mqttTLSServiceName, conf.TLSCertTypeClient), "client cert should be removed")
	assert.False(t, tlsMgr.CertificateExists(mqttTLSServiceName, conf.TLSCertTypeKey), "client key should be removed")

	// Verify settings paths cleared
	controller.settingsMutex.RLock()
	assert.Empty(t, controller.Settings.Realtime.MQTT.TLS.CACert)
	assert.Empty(t, controller.Settings.Realtime.MQTT.TLS.ClientCert)
	assert.Empty(t, controller.Settings.Realtime.MQTT.TLS.ClientKey)
	controller.settingsMutex.RUnlock()

	// GET should show nothing installed
	req2 := httptest.NewRequest(http.MethodGet, "/api/v2/integrations/mqtt/tls/certificate", http.NoBody)
	rec2 := httptest.NewRecorder()
	ctx2 := e.NewContext(req2, rec2)

	err = controller.GetMQTTTLSCertificate(ctx2)
	require.NoError(t, err)
	assert.Equal(t, http.StatusOK, rec2.Code)

	var info MQTTTLSCertificateInfo
	require.NoError(t, json.Unmarshal(rec2.Body.Bytes(), &info))
	assert.False(t, info.CA.Installed, "CA should not be installed after DELETE")
	assert.False(t, info.Client.Installed, "client should not be installed after DELETE")
	assert.False(t, info.HasKey, "key should not be present after DELETE")
}

// TestDeleteMQTTTLSCertificate_ExternalPaths verifies that DELETE clears settings paths
// pointing to external files but does not delete those files.
func TestDeleteMQTTTLSCertificate_ExternalPaths(t *testing.T) {
	e, controller, _ := setupTLSTestEnvironment(t)

	// Write cert to a directory outside the managed TLS dir
	certPEM, _ := generateTestCertKeyPair(t)
	externalDir := t.TempDir()
	certFile := filepath.Join(externalDir, "ca.crt")
	require.NoError(t, os.WriteFile(certFile, []byte(certPEM), 0o644))

	// Point settings to this external file
	controller.settingsMutex.Lock()
	controller.Settings.Realtime.MQTT.TLS.CACert = certFile
	controller.settingsMutex.Unlock()

	// DELETE
	req := httptest.NewRequest(http.MethodDelete, "/api/v2/integrations/mqtt/tls/certificate", http.NoBody)
	rec := httptest.NewRecorder()
	ctx := e.NewContext(req, rec)

	err := controller.DeleteMQTTTLSCertificate(ctx)
	require.NoError(t, err)
	assert.Equal(t, http.StatusNoContent, rec.Code)

	// External file must still exist (DELETE only removes managed files)
	_, statErr := os.Stat(certFile)
	require.NoError(t, statErr, "external cert file should not be deleted")

	// Settings path should be cleared
	controller.settingsMutex.RLock()
	assert.Empty(t, controller.Settings.Realtime.MQTT.TLS.CACert, "settings path should be cleared")
	controller.settingsMutex.RUnlock()
}

// TestMQTTTLSRouteRegistration verifies that all three MQTT TLS routes are registered
// when initIntegrationsRoutes is called.
func TestMQTTTLSRouteRegistration(t *testing.T) {
	e, _, controller := setupTestEnvironment(t)

	controller.initIntegrationsRoutes()

	expectedRoutes := []string{
		"GET /api/v2/integrations/mqtt/tls/certificate",
		"POST /api/v2/integrations/mqtt/tls/certificate",
		"DELETE /api/v2/integrations/mqtt/tls/certificate",
	}
	assertRoutesRegistered(t, e, expectedRoutes)
}
