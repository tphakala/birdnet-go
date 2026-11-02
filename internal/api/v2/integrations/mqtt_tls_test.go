package integrations

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/labstack/echo/v4"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/tphakala/birdnet-go/internal/conf"
)

const (
	// mqttTLSCertificatePath is the route of the MQTT TLS certificate endpoints.
	mqttTLSCertificatePath = "/api/v2/integrations/mqtt/tls/certificate"

	// testCertSubject is the subject of the certificates generateTestCertKeyPair
	// creates.
	testCertSubject = "CN=BirdNET-Go"

	// testCertFileMode is the permission of the certificate files the tests write.
	testCertFileMode = 0o644
)

// postTLSUpload sends upload to the certificate upload handler and returns the
// recorded response.
func postTLSUpload(t *testing.T, e *echo.Echo, h *Handler, upload MQTTTLSCertificateUpload) *httptest.ResponseRecorder {
	t.Helper()
	body, err := json.Marshal(upload)
	require.NoError(t, err)
	req := httptest.NewRequest(http.MethodPost, mqttTLSCertificatePath, strings.NewReader(string(body)))
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	require.NoError(t, h.UploadMQTTTLSCertificate(e.NewContext(req, rec)))
	return rec
}

// getTLSInfo calls the certificate status handler and decodes its answer.
func getTLSInfo(t *testing.T, e *echo.Echo, h *Handler) MQTTTLSCertificateInfo {
	t.Helper()
	req := httptest.NewRequest(http.MethodGet, mqttTLSCertificatePath, http.NoBody)
	rec := httptest.NewRecorder()
	require.NoError(t, h.GetMQTTTLSCertificate(e.NewContext(req, rec)))
	require.Equal(t, http.StatusOK, rec.Code)
	return decodeTLSInfo(t, rec)
}

// decodeTLSInfo decodes the certificate status carried by rec and checks that
// both certificate entries are present.
func decodeTLSInfo(t *testing.T, rec *httptest.ResponseRecorder) MQTTTLSCertificateInfo {
	t.Helper()
	var info MQTTTLSCertificateInfo
	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &info))
	require.NotNil(t, info.CA)
	require.NotNil(t, info.Client)
	return info
}

// TestGetMQTTTLSCertificate verifies the reported certificate state, both with
// nothing installed and with a manually configured CA path.
func TestGetMQTTTLSCertificate(t *testing.T) {
	tests := []struct {
		name        string
		caFromFile  bool
		wantCA      bool
		wantSubject string
	}{
		{name: "no certificates installed"},
		{name: "manual CA path", caFromFile: true, wantCA: true, wantSubject: testCertSubject},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			e, controller, _ := setupTLSTestEnvironment(t)

			if tt.caFromFile {
				// Write the certificate outside the managed dir and point settings at it.
				certPEM, _ := generateTestCertKeyPair(t)
				certFile := filepath.Join(t.TempDir(), "ca.crt")
				require.NoError(t, os.WriteFile(certFile, []byte(certPEM), testCertFileMode))
				controller.Settings.Load().Realtime.MQTT.TLS.CACert = certFile
			}

			info := getTLSInfo(t, e, controller)

			assert.Equal(t, tt.wantCA, info.CA.Installed, "CA installed")
			assert.Equal(t, tt.wantSubject, info.CA.Subject)
			assert.False(t, info.Client.Installed, "client should not be installed")
			assert.False(t, info.HasKey, "key should not be present")
		})
	}
}

// TestUploadMQTTTLSCertificate_Installs verifies which entries each valid
// upload installs.
func TestUploadMQTTTLSCertificate_Installs(t *testing.T) {
	tests := []struct {
		name       string
		withCA     bool
		withClient bool
		withKey    bool
	}{
		{name: "CA only", withCA: true},
		{name: "client certificate and key", withClient: true, withKey: true},
		{name: "CA, client certificate and key", withCA: true, withClient: true, withKey: true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			e, controller, _ := setupTLSTestEnvironment(t)
			certPEM, keyPEM := generateTestCertKeyPair(t)

			var upload MQTTTLSCertificateUpload
			if tt.withCA {
				upload.CACertificate = &certPEM
			}
			if tt.withClient {
				upload.ClientCertificate = &certPEM
			}
			if tt.withKey {
				upload.ClientKey = &keyPEM
			}

			rec := postTLSUpload(t, e, controller, upload)
			require.Equal(t, http.StatusOK, rec.Code)

			info := decodeTLSInfo(t, rec)
			assert.Equal(t, tt.withCA, info.CA.Installed, "CA installed")
			assert.Equal(t, tt.withClient, info.Client.Installed, "client certificate installed")
			assert.Equal(t, tt.withKey, info.HasKey, "client key present")
			if tt.withCA {
				assert.Equal(t, testCertSubject, info.CA.Subject)
				assert.InDelta(t, 1, info.CA.DaysUntilExpiry, 1) // 24h cert, plus or minus a day
			}
			if tt.withClient {
				assert.Equal(t, testCertSubject, info.Client.Subject)
			}
		})
	}
}

// TestUploadMQTTTLSCertificate_Rejects verifies that incomplete or invalid
// uploads are answered with 400.
func TestUploadMQTTTLSCertificate_Rejects(t *testing.T) {
	const invalidPEM = "not-valid-pem-content"

	tests := []struct {
		name   string
		upload func(certPEM, keyPEM string) MQTTTLSCertificateUpload
	}{
		{
			name: "client certificate without key",
			upload: func(certPEM, _ string) MQTTTLSCertificateUpload {
				return MQTTTLSCertificateUpload{ClientCertificate: &certPEM}
			},
		},
		{
			name: "client key without certificate",
			upload: func(_, keyPEM string) MQTTTLSCertificateUpload {
				return MQTTTLSCertificateUpload{ClientKey: &keyPEM}
			},
		},
		{
			name: "invalid PEM",
			upload: func(_, _ string) MQTTTLSCertificateUpload {
				pem := invalidPEM
				return MQTTTLSCertificateUpload{CACertificate: &pem}
			},
		},
		{
			name: "empty request",
			upload: func(_, _ string) MQTTTLSCertificateUpload {
				return MQTTTLSCertificateUpload{}
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			e, controller, _ := setupTLSTestEnvironment(t)
			certPEM, keyPEM := generateTestCertKeyPair(t)

			rec := postTLSUpload(t, e, controller, tt.upload(certPEM, keyPEM))

			assert.Equal(t, http.StatusBadRequest, rec.Code)
		})
	}
}

// TestUploadMQTTTLSCertificate_ClearCA verifies that after uploading all three certs,
// sending an empty CA string clears the CA while preserving the client cert and key.
func TestUploadMQTTTLSCertificate_ClearCA(t *testing.T) {
	e, controller, _ := setupTLSTestEnvironment(t)

	certPEM, keyPEM := generateTestCertKeyPair(t)

	// First upload all three
	require.Equal(t, http.StatusOK, postTLSUpload(t, e, controller, MQTTTLSCertificateUpload{
		CACertificate:     &certPEM,
		ClientCertificate: &certPEM,
		ClientKey:         &keyPEM,
	}).Code)

	// Now clear only the CA
	emptyStr := ""
	rec := postTLSUpload(t, e, controller, MQTTTLSCertificateUpload{CACertificate: &emptyStr})
	require.Equal(t, http.StatusOK, rec.Code)

	info := decodeTLSInfo(t, rec)
	assert.False(t, info.CA.Installed, "CA should be cleared")
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
	controller.Settings.Load().Realtime.MQTT.TLS.CACert = tlsMgr.GetCertificatePath(mqttTLSServiceName, conf.TLSCertTypeCA)
	controller.Settings.Load().Realtime.MQTT.TLS.ClientCert = tlsMgr.GetCertificatePath(mqttTLSServiceName, conf.TLSCertTypeClient)
	controller.Settings.Load().Realtime.MQTT.TLS.ClientKey = tlsMgr.GetCertificatePath(mqttTLSServiceName, conf.TLSCertTypeKey)

	// DELETE
	req := httptest.NewRequest(http.MethodDelete, mqttTLSCertificatePath, http.NoBody)
	rec := httptest.NewRecorder()
	require.NoError(t, controller.DeleteMQTTTLSCertificate(e.NewContext(req, rec)))
	assert.Equal(t, http.StatusNoContent, rec.Code)

	// Verify files are gone
	assert.False(t, tlsMgr.CertificateExists(mqttTLSServiceName, conf.TLSCertTypeCA), "CA should be removed")
	assert.False(t, tlsMgr.CertificateExists(mqttTLSServiceName, conf.TLSCertTypeClient), "client cert should be removed")
	assert.False(t, tlsMgr.CertificateExists(mqttTLSServiceName, conf.TLSCertTypeKey), "client key should be removed")

	// Verify settings paths cleared
	assert.Empty(t, controller.Settings.Load().Realtime.MQTT.TLS.CACert)
	assert.Empty(t, controller.Settings.Load().Realtime.MQTT.TLS.ClientCert)
	assert.Empty(t, controller.Settings.Load().Realtime.MQTT.TLS.ClientKey)

	// GET should show nothing installed
	info := getTLSInfo(t, e, controller)
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
	require.NoError(t, os.WriteFile(certFile, []byte(certPEM), testCertFileMode))

	// Point settings to this external file
	controller.Settings.Load().Realtime.MQTT.TLS.CACert = certFile

	// DELETE
	req := httptest.NewRequest(http.MethodDelete, mqttTLSCertificatePath, http.NoBody)
	rec := httptest.NewRecorder()
	require.NoError(t, controller.DeleteMQTTTLSCertificate(e.NewContext(req, rec)))
	assert.Equal(t, http.StatusNoContent, rec.Code)

	// External file must still exist (DELETE only removes managed files)
	_, statErr := os.Stat(certFile)
	require.NoError(t, statErr, "external cert file should not be deleted")

	// Settings path should be cleared
	assert.Empty(t, controller.Settings.Load().Realtime.MQTT.TLS.CACert, "settings path should be cleared")
}
