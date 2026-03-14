// Package api provides TLS certificate management endpoints.
package api

import (
	"crypto/sha256"
	cryptotls "crypto/tls"
	"crypto/x509"
	"encoding/pem"
	"fmt"
	"math"
	"net/http"
	"os"
	"strings"
	"time"

	"github.com/labstack/echo/v4"
	"github.com/tphakala/birdnet-go/internal/conf"
	"github.com/tphakala/birdnet-go/internal/logger"
	tlspkg "github.com/tphakala/birdnet-go/internal/tls"
)

// TLS validity constraints
const (
	defaultValidityDays = 1825          // Default certificate validity in days (5 years)
	minValidityHours    = 24            // Minimum validity: 1 day
	maxValidityHours    = 24 * 365 * 10 // Maximum validity: 10 years
	tlsServiceName      = "webserver"   // Service name for TLS certificate storage
)

// TLSCertificateInfo represents TLS certificate details returned by the API.
type TLSCertificateInfo struct {
	Installed       bool     `json:"installed"`
	Mode            string   `json:"mode,omitempty"`
	Subject         string   `json:"subject,omitempty"`
	Issuer          string   `json:"issuer,omitempty"`
	NotBefore       string   `json:"notBefore,omitempty"`
	NotAfter        string   `json:"notAfter,omitempty"`
	DaysUntilExpiry int      `json:"daysUntilExpiry,omitempty"`
	SANs            []string `json:"sans,omitempty"`
	SerialNumber    string   `json:"serialNumber,omitempty"`
	Fingerprint     string   `json:"fingerprint,omitempty"`
}

// TLSCertificateUpload represents the request body for uploading a TLS certificate.
type TLSCertificateUpload struct {
	Certificate   string `json:"certificate"`
	PrivateKey    string `json:"privateKey"`
	CACertificate string `json:"caCertificate,omitempty"`
}

// TLSGenerateRequest represents the request body for generating a self-signed certificate.
type TLSGenerateRequest struct {
	Validity string `json:"validity,omitempty"`
}

// initTLSRoutes registers TLS certificate management endpoints.
func (c *Controller) initTLSRoutes() {
	c.logInfoIfEnabled("Initializing TLS routes")

	tlsGroup := c.Group.Group("/tls", c.authMiddleware)
	tlsGroup.GET("/certificate", c.GetTLSCertificate)
	tlsGroup.POST("/certificate", c.UploadTLSCertificate)
	tlsGroup.DELETE("/certificate", c.DeleteTLSCertificate)
	tlsGroup.POST("/certificate/generate", c.GenerateSelfSignedCertificate)
	tlsGroup.GET("/certificate/download", c.DownloadTLSCertificate)

	c.logInfoIfEnabled("TLS routes initialized successfully")
}

// GetTLSCertificate handles GET /api/v2/tls/certificate.
// Returns certificate information if installed, or {installed: false} otherwise.
func (c *Controller) GetTLSCertificate(ctx echo.Context) error {
	tlsMgr := conf.GetTLSManager()
	if !tlsMgr.CertificateExists(tlsServiceName, conf.TLSCertTypeServerCert) {
		return ctx.JSON(http.StatusOK, &TLSCertificateInfo{Installed: false})
	}

	certPath := tlsMgr.GetCertificatePath(tlsServiceName, conf.TLSCertTypeServerCert)
	info, err := ParseCertificateInfo(certPath)
	if err != nil {
		return c.HandleError(ctx, err, "Failed to parse TLS certificate", http.StatusInternalServerError)
	}

	// Include current TLS mode from settings
	c.settingsMutex.RLock()
	info.Mode = string(c.Settings.Security.TLSMode)
	c.settingsMutex.RUnlock()

	return ctx.JSON(http.StatusOK, info)
}

// UploadTLSCertificate handles POST /api/v2/tls/certificate.
// Accepts a PEM-encoded certificate and private key pair, validates them,
// and stores them for use by the web server.
func (c *Controller) UploadTLSCertificate(ctx echo.Context) error {
	var req TLSCertificateUpload
	if err := ctx.Bind(&req); err != nil {
		return c.HandleError(ctx, err, "Invalid request body", http.StatusBadRequest)
	}

	if strings.TrimSpace(req.Certificate) == "" || strings.TrimSpace(req.PrivateKey) == "" {
		return c.HandleError(ctx, nil, "Certificate and private key are required", http.StatusBadRequest)
	}

	// Validate that the certificate and key form a valid pair
	if err := validateKeyPair([]byte(req.Certificate), []byte(req.PrivateKey)); err != nil {
		return c.HandleError(ctx, err, "Certificate and private key do not form a valid pair", http.StatusBadRequest)
	}

	tlsMgr := conf.GetTLSManager()

	// Save certificate
	if _, err := tlsMgr.SaveCertificate(tlsServiceName, conf.TLSCertTypeServerCert, req.Certificate); err != nil {
		return c.HandleError(ctx, err, "Failed to save TLS certificate", http.StatusInternalServerError)
	}

	// Save private key
	if _, err := tlsMgr.SaveCertificate(tlsServiceName, conf.TLSCertTypeServerKey, req.PrivateKey); err != nil {
		// Clean up the cert that was saved
		_ = tlsMgr.RemoveCertificate(tlsServiceName, conf.TLSCertTypeServerCert)
		return c.HandleError(ctx, err, "Failed to save TLS private key", http.StatusInternalServerError)
	}

	// Save CA certificate if provided
	if strings.TrimSpace(req.CACertificate) != "" {
		if _, err := tlsMgr.SaveCertificate(tlsServiceName, conf.TLSCertTypeCA, req.CACertificate); err != nil {
			// Clean up cert and key that were saved
			_ = tlsMgr.RemoveCertificate(tlsServiceName, conf.TLSCertTypeServerCert)
			_ = tlsMgr.RemoveCertificate(tlsServiceName, conf.TLSCertTypeServerKey)
			return c.HandleError(ctx, err, "Failed to save CA certificate", http.StatusInternalServerError)
		}
	}

	// Update TLS mode to manual
	c.settingsMutex.Lock()
	c.Settings.Security.TLSMode = conf.TLSModeManual
	c.settingsMutex.Unlock()

	if !c.DisableSaveSettings {
		if err := conf.SaveSettings(); err != nil {
			c.logErrorIfEnabled("Failed to save settings after TLS certificate upload",
				logger.Error(err))
		}
	}

	// Return certificate info
	certPath := tlsMgr.GetCertificatePath(tlsServiceName, conf.TLSCertTypeServerCert)
	info, err := ParseCertificateInfo(certPath)
	if err != nil {
		return c.HandleError(ctx, err, "Certificate saved but failed to parse info", http.StatusInternalServerError)
	}
	info.Mode = string(conf.TLSModeManual)

	return ctx.JSON(http.StatusOK, info)
}

// DeleteTLSCertificate handles DELETE /api/v2/tls/certificate.
// Removes all TLS certificates for the web server and resets TLS mode to none.
func (c *Controller) DeleteTLSCertificate(ctx echo.Context) error {
	tlsMgr := conf.GetTLSManager()

	if err := tlsMgr.RemoveAllCertificates(tlsServiceName); err != nil {
		return c.HandleError(ctx, err, "Failed to remove TLS certificates", http.StatusInternalServerError)
	}

	// Reset TLS mode to none
	c.settingsMutex.Lock()
	c.Settings.Security.TLSMode = conf.TLSModeNone
	c.settingsMutex.Unlock()

	if !c.DisableSaveSettings {
		if err := conf.SaveSettings(); err != nil {
			c.logErrorIfEnabled("Failed to save settings after TLS certificate deletion",
				logger.Error(err))
		}
	}

	return ctx.NoContent(http.StatusNoContent)
}

// GenerateSelfSignedCertificate handles POST /api/v2/tls/certificate/generate.
// Generates a self-signed TLS certificate with SANs collected from the system.
func (c *Controller) GenerateSelfSignedCertificate(ctx echo.Context) error {
	var req TLSGenerateRequest
	if err := ctx.Bind(&req); err != nil {
		return c.HandleError(ctx, err, "Invalid request body", http.StatusBadRequest)
	}

	// Parse validity duration
	validityHours := defaultValidityDays * 24
	if req.Validity != "" {
		hours, err := conf.ParseRetentionPeriod(req.Validity)
		if err != nil {
			return c.HandleError(ctx, err,
				fmt.Sprintf("Invalid validity period %q", req.Validity), http.StatusBadRequest)
		}
		if hours < minValidityHours {
			return c.HandleError(ctx, nil,
				fmt.Sprintf("Validity must be at least %d hours (1 day)", minValidityHours), http.StatusBadRequest)
		}
		if hours > maxValidityHours {
			return c.HandleError(ctx, nil,
				fmt.Sprintf("Validity must not exceed %d hours (10 years)", maxValidityHours), http.StatusBadRequest)
		}
		validityHours = hours
	}

	// Collect SANs from settings
	c.settingsMutex.RLock()
	host := c.Settings.Security.Host
	baseURL := c.Settings.Security.BaseURL
	c.settingsMutex.RUnlock()

	sans := tlspkg.CollectSANs(host, baseURL)

	// Generate self-signed certificate
	certPEM, keyPEM, err := tlspkg.GenerateSelfSigned(tlspkg.SelfSignedOptions{
		Validity: time.Duration(validityHours) * time.Hour,
		SANs:     sans,
	})
	if err != nil {
		return c.HandleError(ctx, err, "Failed to generate self-signed certificate", http.StatusInternalServerError)
	}

	// Save certificate and key
	tlsMgr := conf.GetTLSManager()

	if _, err := tlsMgr.SaveCertificate(tlsServiceName, conf.TLSCertTypeServerCert, certPEM); err != nil {
		return c.HandleError(ctx, err, "Failed to save generated certificate", http.StatusInternalServerError)
	}
	if _, err := tlsMgr.SaveCertificate(tlsServiceName, conf.TLSCertTypeServerKey, keyPEM); err != nil {
		// Clean up the cert that was saved
		_ = tlsMgr.RemoveCertificate(tlsServiceName, conf.TLSCertTypeServerCert)
		return c.HandleError(ctx, err, "Failed to save generated private key", http.StatusInternalServerError)
	}

	// Update TLS mode to self-signed
	c.settingsMutex.Lock()
	c.Settings.Security.TLSMode = conf.TLSModeSelfSigned
	c.settingsMutex.Unlock()

	if !c.DisableSaveSettings {
		if err := conf.SaveSettings(); err != nil {
			c.logErrorIfEnabled("Failed to save settings after self-signed certificate generation",
				logger.Error(err))
		}
	}

	// Return certificate info
	certPath := tlsMgr.GetCertificatePath(tlsServiceName, conf.TLSCertTypeServerCert)
	info, err := ParseCertificateInfo(certPath)
	if err != nil {
		return c.HandleError(ctx, err, "Certificate generated but failed to parse info", http.StatusInternalServerError)
	}
	info.Mode = string(conf.TLSModeSelfSigned)

	return ctx.JSON(http.StatusOK, info)
}

// DownloadTLSCertificate handles GET /api/v2/tls/certificate/download.
// Serves the installed server certificate as a downloadable PEM file.
// Users can install this in their OS trust store to avoid browser warnings
// when using self-signed certificates.
func (c *Controller) DownloadTLSCertificate(ctx echo.Context) error {
	tlsMgr := conf.GetTLSManager()
	if !tlsMgr.CertificateExists(tlsServiceName, conf.TLSCertTypeServerCert) {
		return c.HandleError(ctx, nil, "No certificate installed", http.StatusNotFound)
	}

	certPath := tlsMgr.GetCertificatePath(tlsServiceName, conf.TLSCertTypeServerCert)

	ctx.Response().Header().Set("Content-Disposition", `attachment; filename="birdnet-go.crt"`)

	return ctx.File(certPath)
}

// ParseCertificateInfo reads a PEM certificate file and extracts its metadata.
func ParseCertificateInfo(certPath string) (*TLSCertificateInfo, error) {
	data, err := os.ReadFile(certPath)
	if err != nil {
		return nil, fmt.Errorf("failed to read certificate file: %w", err)
	}

	block, _ := pem.Decode(data)
	if block == nil {
		return nil, fmt.Errorf("failed to decode PEM block from certificate file")
	}

	cert, err := x509.ParseCertificate(block.Bytes)
	if err != nil {
		return nil, fmt.Errorf("failed to parse x509 certificate: %w", err)
	}

	// Compute SHA-256 fingerprint of the DER-encoded certificate
	fingerprint := sha256.Sum256(cert.Raw)
	// Build colon-separated hex fingerprint with SHA256: prefix
	hexStr := fmt.Sprintf("%x", fingerprint[:])
	var colonSep strings.Builder
	colonSep.WriteString("SHA256:")
	for i := 0; i < len(hexStr); i += 2 {
		if i > 0 {
			colonSep.WriteByte(':')
		}
		colonSep.WriteString(hexStr[i : i+2])
	}

	// Collect SANs: DNS names + IP addresses
	sans := make([]string, 0, len(cert.DNSNames)+len(cert.IPAddresses))
	sans = append(sans, cert.DNSNames...)
	for _, ip := range cert.IPAddresses {
		sans = append(sans, ip.String())
	}

	daysUntilExpiry := int(math.Floor(time.Until(cert.NotAfter).Hours() / 24))

	return &TLSCertificateInfo{
		Installed:       true,
		Subject:         cert.Subject.String(),
		Issuer:          cert.Issuer.String(),
		NotBefore:       cert.NotBefore.Format(time.RFC3339),
		NotAfter:        cert.NotAfter.Format(time.RFC3339),
		DaysUntilExpiry: daysUntilExpiry,
		SANs:            sans,
		SerialNumber:    cert.SerialNumber.String(),
		Fingerprint:     colonSep.String(),
	}, nil
}

// validateKeyPair checks that a PEM-encoded certificate and key form a valid pair
// using crypto/tls.X509KeyPair.
func validateKeyPair(certPEM, keyPEM []byte) error {
	_, err := cryptotls.X509KeyPair(certPEM, keyPEM)
	return err
}
