package conf

import (
	"crypto/x509"
	"encoding/pem"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"

	"github.com/tphakala/birdnet-go/internal/errors"
)

// TLSCertificateType represents the type of TLS certificate/key
type TLSCertificateType string

const (
	TLSCertTypeCA     TLSCertificateType = "ca"
	TLSCertTypeClient TLSCertificateType = "client"
	TLSCertTypeKey    TLSCertificateType = "key"
)

// TLSManager handles TLS certificate storage and retrieval
type TLSManager struct {
	configDir string
	tlsDir    string
}

var (
	globalTLSManager *TLSManager
	tlsManagerOnce   sync.Once
)

// GetTLSManager returns the global TLS manager instance
func GetTLSManager() *TLSManager {
	tlsManagerOnce.Do(func() {
		configPaths, _ := GetDefaultConfigPaths()
		if len(configPaths) > 0 {
			globalTLSManager = NewTLSManager(configPaths[0])
		}
	})
	return globalTLSManager
}

// NewTLSManager creates a new TLS certificate manager
func NewTLSManager(configDir string) *TLSManager {
	return &TLSManager{
		configDir: configDir,
		tlsDir:    filepath.Join(configDir, "tls"),
	}
}

// EnsureTLSDirectory creates the TLS directory if it doesn't exist
func (tm *TLSManager) EnsureTLSDirectory() error {
	// Create TLS directory with restricted permissions (0700)
	if err := os.MkdirAll(tm.tlsDir, 0o700); err != nil {
		return errors.New(err).
			Component("tls-manager").
			Category(errors.CategoryFileIO).
			Context("operation", "create-tls-dir").
			Context("dir", tm.tlsDir).
			Build()
	}
	return nil
}

// getCertificateFilename generates a filename for a certificate
func (tm *TLSManager) getCertificateFilename(service string, certType TLSCertificateType) string {
	// Use lowercase service name and certificate type
	service = strings.ToLower(service)
	var extension string
	switch certType {
	case TLSCertTypeCA:
		extension = "ca.crt"
	case TLSCertTypeClient:
		extension = "client.crt"
	case TLSCertTypeKey:
		extension = "client.key"
	default:
		extension = "unknown"
	}
	return fmt.Sprintf("%s_%s", service, extension)
}

// GetServiceDirectory returns the directory path for a specific service
func (tm *TLSManager) GetServiceDirectory(service string) string {
	return filepath.Join(tm.tlsDir, strings.ToLower(service))
}

// EnsureServiceDirectory creates the service-specific directory if it doesn't exist
func (tm *TLSManager) EnsureServiceDirectory(service string) error {
	serviceDir := tm.GetServiceDirectory(service)
	// Create service directory with restricted permissions (0700)
	if err := os.MkdirAll(serviceDir, 0o700); err != nil {
		return errors.New(err).
			Component("tls-manager").
			Category(errors.CategoryFileIO).
			Context("operation", "create-service-dir").
			Context("dir", serviceDir).
			Build()
	}
	return nil
}

// SaveCertificate saves a certificate or key to the service-specific TLS directory
func (tm *TLSManager) SaveCertificate(service string, certType TLSCertificateType, content string) (string, error) {
	// Trim whitespace
	content = strings.TrimSpace(content)
	
	// If content is empty, remove the certificate file if it exists
	if content == "" {
		return "", tm.RemoveCertificate(service, certType)
	}

	// Ensure service directory exists
	if err := tm.EnsureServiceDirectory(service); err != nil {
		return "", err
	}

	// Validate certificate content
	if err := validateCertificateContent(certType, content); err != nil {
		return "", errors.New(err).
			Component("tls-manager").
			Category(errors.CategoryValidation).
			Context("operation", "validate-cert").
			Context("service", service).
			Context("cert-type", string(certType)).
			Build()
	}

	// Generate filename
	filename := tm.getCertificateFilename(service, certType)
	filePath := filepath.Join(tm.GetServiceDirectory(service), filename)

	// Set appropriate permissions based on certificate type
	var perm os.FileMode
	if certType == TLSCertTypeKey {
		perm = 0o600 // Private key: read/write for owner only
	} else {
		perm = 0o644 // Certificates: read for all, write for owner
	}

	// Write file with appropriate permissions
	if err := os.WriteFile(filePath, []byte(content), perm); err != nil {
		return "", errors.New(err).
			Component("tls-manager").
			Category(errors.CategoryFileIO).
			Context("operation", "write-cert").
			Context("file", filePath).
			Build()
	}

	return filePath, nil
}

// GetCertificatePath returns the path to a certificate file
func (tm *TLSManager) GetCertificatePath(service string, certType TLSCertificateType) string {
	filename := tm.getCertificateFilename(service, certType)
	return filepath.Join(tm.GetServiceDirectory(service), filename)
}

// CertificateExists checks if a certificate file exists
func (tm *TLSManager) CertificateExists(service string, certType TLSCertificateType) bool {
	path := tm.GetCertificatePath(service, certType)
	_, err := os.Stat(path)
	return err == nil
}

// RemoveCertificate removes a certificate file
func (tm *TLSManager) RemoveCertificate(service string, certType TLSCertificateType) error {
	path := tm.GetCertificatePath(service, certType)
	if err := os.Remove(path); err != nil && !os.IsNotExist(err) {
		return errors.New(err).
			Component("tls-manager").
			Category(errors.CategoryFileIO).
			Context("operation", "remove-cert").
			Context("file", path).
			Build()
	}
	return nil
}

// RemoveAllCertificates removes all certificates for a service
func (tm *TLSManager) RemoveAllCertificates(service string) error {
	certTypes := []TLSCertificateType{TLSCertTypeCA, TLSCertTypeClient, TLSCertTypeKey}
	for _, certType := range certTypes {
		if err := tm.RemoveCertificate(service, certType); err != nil {
			return err
		}
	}
	return nil
}

// validateCertificateContent validates that the content is a valid PEM-encoded certificate or key
func validateCertificateContent(certType TLSCertificateType, content string) error {
	content = strings.TrimSpace(content)
	if content == "" {
		return fmt.Errorf("certificate content is empty")
	}

	// Decode PEM block
	block, _ := pem.Decode([]byte(content))
	if block == nil {
		return fmt.Errorf("failed to decode PEM block")
	}

	switch certType {
	case TLSCertTypeCA, TLSCertTypeClient:
		// Validate certificate
		if block.Type != "CERTIFICATE" {
			return fmt.Errorf("expected CERTIFICATE block, got %s", block.Type)
		}
		_, err := x509.ParseCertificate(block.Bytes)
		if err != nil {
			return fmt.Errorf("invalid certificate: %w", err)
		}

	case TLSCertTypeKey:
		// Validate private key
		validKeyTypes := []string{
			"RSA PRIVATE KEY",
			"EC PRIVATE KEY",
			"PRIVATE KEY", // PKCS#8
		}
		validType := false
		for _, kt := range validKeyTypes {
			if block.Type == kt {
				validType = true
				break
			}
		}
		if !validType {
			return fmt.Errorf("expected private key block, got %s", block.Type)
		}
		// We don't parse the actual key to avoid potential issues with different formats
	}

	return nil
}