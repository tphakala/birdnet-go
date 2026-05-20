package api

import (
	"net/http"
	"os"
	"strings"

	"github.com/labstack/echo/v4"
	"github.com/tphakala/birdnet-go/internal/conf"
	"github.com/tphakala/birdnet-go/internal/logger"
)

const mqttTLSServiceName = "mqtt"

// MQTTTLSCertificateInfo represents the installation status of MQTT TLS certificates.
type MQTTTLSCertificateInfo struct {
	CA     *TLSCertificateInfo `json:"ca"`
	Client *TLSCertificateInfo `json:"client"`
	HasKey bool                `json:"hasKey"`
}

// MQTTTLSCertificateUpload represents the request body for uploading MQTT TLS certificates.
// Pointer fields distinguish "omitted" (nil = preserve) from "empty string" (clear).
type MQTTTLSCertificateUpload struct {
	CACertificate     *string `json:"caCertificate,omitempty"`
	ClientCertificate *string `json:"clientCertificate,omitempty"`
	ClientKey         *string `json:"clientKey,omitempty"`
}

// getMQTTCertPath returns the path to an MQTT certificate, checking settings paths first,
// then falling back to TLSManager's managed directory.
func (c *Controller) getMQTTCertPath(certType conf.TLSCertificateType) string {
	c.settingsMutex.RLock()
	mqttTLS := c.Settings.Realtime.MQTT.TLS
	c.settingsMutex.RUnlock()

	// Check settings paths first (covers manually configured certs)
	var settingsPath string
	switch certType {
	case conf.TLSCertTypeCA:
		settingsPath = mqttTLS.CACert
	case conf.TLSCertTypeClient:
		settingsPath = mqttTLS.ClientCert
	case conf.TLSCertTypeKey:
		settingsPath = mqttTLS.ClientKey
	default:
		// Server cert types are not applicable for MQTT client configuration
		return ""
	}

	if settingsPath != "" {
		if _, err := os.Stat(settingsPath); err == nil {
			return settingsPath
		}
	}

	// Fall back to TLSManager managed path
	tlsMgr := conf.GetTLSManager()
	if tlsMgr.CertificateExists(mqttTLSServiceName, certType) {
		return tlsMgr.GetCertificatePath(mqttTLSServiceName, certType)
	}

	return ""
}

// GetMQTTTLSCertificate handles GET /api/v2/integrations/mqtt/tls/certificate.
func (c *Controller) GetMQTTTLSCertificate(ctx echo.Context) error {
	result := &MQTTTLSCertificateInfo{
		CA:     &TLSCertificateInfo{Installed: false},
		Client: &TLSCertificateInfo{Installed: false},
		HasKey: false,
	}

	// Check CA certificate
	if caPath := c.getMQTTCertPath(conf.TLSCertTypeCA); caPath != "" {
		if info, err := ParseCertificateInfo(caPath); err == nil {
			result.CA = info
		}
	}

	// Check client certificate
	if clientPath := c.getMQTTCertPath(conf.TLSCertTypeClient); clientPath != "" {
		if info, err := ParseCertificateInfo(clientPath); err == nil {
			result.Client = info
		}
	}

	// Check client key exists
	if keyPath := c.getMQTTCertPath(conf.TLSCertTypeKey); keyPath != "" {
		result.HasKey = true
	}

	return ctx.JSON(http.StatusOK, result)
}

// UploadMQTTTLSCertificate handles POST /api/v2/integrations/mqtt/tls/certificate.
// Accepts optional CA certificate, client certificate, and client key.
// Acts as a partial update:
//   - nil pointer (field omitted from JSON) = preserve existing
//   - empty string = clear/remove that certificate
//   - non-empty string = save new certificate
func (c *Controller) UploadMQTTTLSCertificate(ctx echo.Context) error {
	var req MQTTTLSCertificateUpload
	if err := ctx.Bind(&req); err != nil {
		return c.HandleError(ctx, err, "Invalid request body", http.StatusBadRequest)
	}

	// Validate: at least one field must be present
	if req.CACertificate == nil && req.ClientCertificate == nil && req.ClientKey == nil {
		return c.HandleError(ctx, nil,
			"At least one certificate field must be provided",
			http.StatusBadRequest)
	}

	// Validate: client cert and key must come together (both present or both nil)
	if (req.ClientCertificate != nil) != (req.ClientKey != nil) {
		return c.HandleError(ctx, nil,
			"Client certificate and client key must be provided together",
			http.StatusBadRequest)
	}

	// Validate: if both provided, both must be empty or both non-empty
	if req.ClientCertificate != nil && req.ClientKey != nil {
		cert := strings.TrimSpace(*req.ClientCertificate)
		key := strings.TrimSpace(*req.ClientKey)
		if (cert == "") != (key == "") {
			return c.HandleError(ctx, nil,
				"Client certificate and client key must both be provided or both be cleared",
				http.StatusBadRequest)
		}
	}

	tlsMgr := conf.GetTLSManager()

	// Serialise the entire cert-save-settings sequence.
	c.settingsMutex.Lock()
	defer c.settingsMutex.Unlock()

	if err := tlsMgr.BackupAllCertificates(mqttTLSServiceName); err != nil {
		return c.HandleError(ctx, err, "Failed to backup MQTT TLS certificates", http.StatusInternalServerError)
	}

	// Stage 1: Save new cert files. On failure, restore backups.
	type pathUpdate struct {
		caCert, clientCert, clientKey string
		clearCA, clearClient          bool
	}
	var update pathUpdate

	if req.CACertificate != nil {
		ca := strings.TrimSpace(*req.CACertificate)
		if ca == "" {
			update.clearCA = true
		} else {
			path, err := tlsMgr.SaveCertificate(mqttTLSServiceName, conf.TLSCertTypeCA, ca)
			if err != nil {
				tlsMgr.RestoreBackups(mqttTLSServiceName)
				return c.HandleError(ctx, err, "Failed to save CA certificate", http.StatusBadRequest)
			}
			update.caCert = path
		}
	}

	if req.ClientCertificate != nil {
		cert := strings.TrimSpace(*req.ClientCertificate)
		key := strings.TrimSpace(*req.ClientKey)
		if cert == "" {
			update.clearClient = true
		} else {
			certPath, err := tlsMgr.SaveCertificate(mqttTLSServiceName, conf.TLSCertTypeClient, cert)
			if err != nil {
				tlsMgr.RestoreBackups(mqttTLSServiceName)
				return c.HandleError(ctx, err, "Failed to save client certificate", http.StatusBadRequest)
			}
			keyPath, err := tlsMgr.SaveCertificate(mqttTLSServiceName, conf.TLSCertTypeKey, key)
			if err != nil {
				tlsMgr.RestoreBackups(mqttTLSServiceName)
				return c.HandleError(ctx, err, "Failed to save client key", http.StatusBadRequest)
			}
			update.clientCert = certPath
			update.clientKey = keyPath
		}
	}

	// Stage 2: Apply settings atomically.
	current := c.getSettingsOrFallback()
	updated := conf.CloneSettings(current)
	if update.caCert != "" {
		updated.Realtime.MQTT.TLS.CACert = update.caCert
	} else if update.clearCA {
		updated.Realtime.MQTT.TLS.CACert = ""
	}
	if update.clientCert != "" {
		updated.Realtime.MQTT.TLS.ClientCert = update.clientCert
		updated.Realtime.MQTT.TLS.ClientKey = update.clientKey
	} else if update.clearClient {
		updated.Realtime.MQTT.TLS.ClientCert = ""
		updated.Realtime.MQTT.TLS.ClientKey = ""
	}
	if err := c.publishAndSaveSettings(current, updated); err != nil {
		tlsMgr.RestoreBackups(mqttTLSServiceName)
		return c.HandleError(ctx, err, "Failed to save settings after MQTT TLS certificate upload",
			http.StatusInternalServerError)
	}
	if handleErr := c.handleSettingsChanges(current, updated); handleErr != nil {
		GetLogger().Warn("Failed to trigger settings side-effects after MQTT TLS certificate change",
			logger.Error(handleErr))
	}
	tlsMgr.CleanupBackups(mqttTLSServiceName)

	// Stage 3: Settings saved. Now perform clears (best-effort).
	if update.clearCA {
		_ = tlsMgr.RemoveCertificate(mqttTLSServiceName, conf.TLSCertTypeCA)
	}
	if update.clearClient {
		_ = tlsMgr.RemoveCertificate(mqttTLSServiceName, conf.TLSCertTypeClient)
		_ = tlsMgr.RemoveCertificate(mqttTLSServiceName, conf.TLSCertTypeKey)
	}

	return c.GetMQTTTLSCertificate(ctx)
}

// DeleteMQTTTLSCertificate handles DELETE /api/v2/integrations/mqtt/tls/certificate.
// Removes managed certificates and clears settings paths.
// External certificate files (manually configured) are not deleted.
func (c *Controller) DeleteMQTTTLSCertificate(ctx echo.Context) error {
	tlsMgr := conf.GetTLSManager()

	// Serialise the entire backup-remove-save sequence so concurrent
	// requests cannot interleave between backup and remove.
	c.settingsMutex.Lock()
	defer c.settingsMutex.Unlock()

	if err := tlsMgr.BackupAllCertificates(mqttTLSServiceName); err != nil {
		return c.HandleError(ctx, err, "Failed to backup MQTT TLS certificates before deletion",
			http.StatusInternalServerError)
	}
	if err := tlsMgr.RemoveAllCertificates(mqttTLSServiceName); err != nil {
		tlsMgr.RestoreBackups(mqttTLSServiceName)
		return c.HandleError(ctx, err, "Failed to remove MQTT TLS certificates",
			http.StatusInternalServerError)
	}

	current := c.getSettingsOrFallback()
	updated := conf.CloneSettings(current)
	updated.Realtime.MQTT.TLS.CACert = ""
	updated.Realtime.MQTT.TLS.ClientCert = ""
	updated.Realtime.MQTT.TLS.ClientKey = ""
	if err := c.publishAndSaveSettings(current, updated); err != nil {
		tlsMgr.RestoreBackups(mqttTLSServiceName)
		return c.HandleError(ctx, err, "Failed to save settings after MQTT TLS certificate deletion",
			http.StatusInternalServerError)
	}
	if handleErr := c.handleSettingsChanges(current, updated); handleErr != nil {
		GetLogger().Warn("Failed to trigger settings side-effects after MQTT TLS certificate change",
			logger.Error(handleErr))
	}
	tlsMgr.CleanupBackups(mqttTLSServiceName)

	return ctx.NoContent(http.StatusNoContent)
}
