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

	// Stage 1: Perform all I/O operations, collecting path updates.
	// Settings are not modified until all operations succeed, ensuring atomicity.
	type pathUpdate struct {
		caCert, clientCert, clientKey string
		clearCA, clearClient          bool
	}
	var update pathUpdate

	// Process CA certificate
	if req.CACertificate != nil {
		ca := strings.TrimSpace(*req.CACertificate)
		if ca == "" {
			_ = tlsMgr.RemoveCertificate(mqttTLSServiceName, conf.TLSCertTypeCA)
			update.clearCA = true
		} else {
			path, err := tlsMgr.SaveCertificate(mqttTLSServiceName, conf.TLSCertTypeCA, ca)
			if err != nil {
				return c.HandleError(ctx, err, "Failed to save CA certificate", http.StatusBadRequest)
			}
			update.caCert = path
		}
	}

	// Process client certificate and key
	if req.ClientCertificate != nil {
		cert := strings.TrimSpace(*req.ClientCertificate)
		key := strings.TrimSpace(*req.ClientKey)
		if cert == "" {
			_ = tlsMgr.RemoveCertificate(mqttTLSServiceName, conf.TLSCertTypeClient)
			_ = tlsMgr.RemoveCertificate(mqttTLSServiceName, conf.TLSCertTypeKey)
			update.clearClient = true
		} else {
			certPath, err := tlsMgr.SaveCertificate(mqttTLSServiceName, conf.TLSCertTypeClient, cert)
			if err != nil {
				// Roll back CA cert if it was saved in this request
				if update.caCert != "" {
					_ = tlsMgr.RemoveCertificate(mqttTLSServiceName, conf.TLSCertTypeCA)
				}
				return c.HandleError(ctx, err, "Failed to save client certificate", http.StatusBadRequest)
			}
			keyPath, err := tlsMgr.SaveCertificate(mqttTLSServiceName, conf.TLSCertTypeKey, key)
			if err != nil {
				// Roll back client cert and CA cert if saved in this request
				_ = tlsMgr.RemoveCertificate(mqttTLSServiceName, conf.TLSCertTypeClient)
				if update.caCert != "" {
					_ = tlsMgr.RemoveCertificate(mqttTLSServiceName, conf.TLSCertTypeCA)
				}
				return c.HandleError(ctx, err, "Failed to save client key", http.StatusBadRequest)
			}
			update.clientCert = certPath
			update.clientKey = keyPath
		}
	}

	// Stage 2: Apply all settings atomically after all I/O succeeded.
	c.settingsMutex.Lock()
	if update.caCert != "" {
		c.Settings.Realtime.MQTT.TLS.CACert = update.caCert
	} else if update.clearCA {
		c.Settings.Realtime.MQTT.TLS.CACert = ""
	}
	if update.clientCert != "" {
		c.Settings.Realtime.MQTT.TLS.ClientCert = update.clientCert
		c.Settings.Realtime.MQTT.TLS.ClientKey = update.clientKey
	} else if update.clearClient {
		c.Settings.Realtime.MQTT.TLS.ClientCert = ""
		c.Settings.Realtime.MQTT.TLS.ClientKey = ""
	}
	c.settingsMutex.Unlock()

	if !c.DisableSaveSettings {
		if err := conf.SaveSettings(); err != nil {
			c.logErrorIfEnabled("Failed to save settings after MQTT TLS certificate upload",
				logger.Error(err))
		}
	}

	return c.GetMQTTTLSCertificate(ctx)
}

// DeleteMQTTTLSCertificate handles DELETE /api/v2/integrations/mqtt/tls/certificate.
// Removes managed certificates and clears settings paths.
// External certificate files (manually configured) are not deleted.
func (c *Controller) DeleteMQTTTLSCertificate(ctx echo.Context) error {
	tlsMgr := conf.GetTLSManager()

	// Only remove files in the managed directory
	if err := tlsMgr.RemoveAllCertificates(mqttTLSServiceName); err != nil {
		return c.HandleError(ctx, err, "Failed to remove MQTT TLS certificates",
			http.StatusInternalServerError)
	}

	// Clear all certificate paths in settings
	c.settingsMutex.Lock()
	c.Settings.Realtime.MQTT.TLS.CACert = ""
	c.Settings.Realtime.MQTT.TLS.ClientCert = ""
	c.Settings.Realtime.MQTT.TLS.ClientKey = ""
	c.settingsMutex.Unlock()

	if !c.DisableSaveSettings {
		if err := conf.SaveSettings(); err != nil {
			c.logErrorIfEnabled("Failed to save settings after MQTT TLS certificate deletion",
				logger.Error(err))
		}
	}

	return ctx.NoContent(http.StatusNoContent)
}
