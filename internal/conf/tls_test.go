package conf

import (
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/rsa"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/pem"
	"math/big"
	"os"
	"path/filepath"
	"sync"
	"testing"
	"time"
)

// Helper function to generate a test certificate
func generateTestCertificate(t *testing.T) (caCert, clientCert, clientKey string) {
	// Generate RSA private key
	priv, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		t.Fatalf("Failed to generate private key: %v", err)
	}

	// Create certificate template
	template := x509.Certificate{
		SerialNumber:          big.NewInt(1),
		Subject:               pkix.Name{Organization: []string{"Test"}},
		NotBefore:             time.Now(),
		NotAfter:              time.Now().Add(365 * 24 * time.Hour),
		KeyUsage:              x509.KeyUsageKeyEncipherment | x509.KeyUsageDigitalSignature,
		ExtKeyUsage:           []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth},
		BasicConstraintsValid: true,
	}

	// Generate certificate
	certBytes, err := x509.CreateCertificate(rand.Reader, &template, &template, &priv.PublicKey, priv)
	if err != nil {
		t.Fatalf("Failed to create certificate: %v", err)
	}

	// Encode certificate
	certPEM := pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: certBytes})
	
	// Encode private key
	privKeyPEM := pem.EncodeToMemory(&pem.Block{Type: "RSA PRIVATE KEY", Bytes: x509.MarshalPKCS1PrivateKey(priv)})
	
	// Create CA certificate (same as cert for simplicity)
	caPEM := certPEM

	return string(caPEM), string(certPEM), string(privKeyPEM)
}

//nolint:gocognit // Test functions can have high complexity
func TestTLSManager(t *testing.T) {
	// Create a temporary directory for testing
	tempDir, err := os.MkdirTemp("", "tls-test")
	if err != nil {
		t.Fatalf("Failed to create temp dir: %v", err)
	}
	defer func() {
		if err := os.RemoveAll(tempDir); err != nil {
			t.Logf("Failed to remove temp dir: %v", err)
		}
	}()

	tm := NewTLSManager(tempDir)

	// Generate test certificates
	caCert, clientCert, clientKey := generateTestCertificate(t)

	t.Run("SaveAndRetrieveCertificates", func(t *testing.T) {
		// Save CA certificate
		caPath, err := tm.SaveCertificate("mqtt", TLSCertTypeCA, caCert)
		if err != nil {
			t.Errorf("Failed to save CA certificate: %v", err)
		}
		if caPath == "" {
			t.Error("CA certificate path is empty")
		}

		// Verify file exists and has correct permissions
		info, err := os.Stat(caPath)
		if err != nil {
			t.Errorf("Failed to stat CA certificate file: %v", err)
		}
		if info.Mode().Perm() != 0o644 {
			t.Errorf("CA certificate has wrong permissions: %v", info.Mode().Perm())
		}

		// Save client certificate
		_, err = tm.SaveCertificate("mqtt", TLSCertTypeClient, clientCert)
		if err != nil {
			t.Errorf("Failed to save client certificate: %v", err)
		}

		// Save client key
		keyPath, err := tm.SaveCertificate("mqtt", TLSCertTypeKey, clientKey)
		if err != nil {
			t.Errorf("Failed to save client key: %v", err)
		}

		// Verify key has restricted permissions
		keyInfo, err := os.Stat(keyPath)
		if err != nil {
			t.Errorf("Failed to stat key file: %v", err)
		}
		if keyInfo.Mode().Perm() != 0o600 {
			t.Errorf("Key file has wrong permissions: %v", keyInfo.Mode().Perm())
		}

		// Check existence
		if !tm.CertificateExists("mqtt", TLSCertTypeCA) {
			t.Error("CA certificate should exist")
		}
		if !tm.CertificateExists("mqtt", TLSCertTypeClient) {
			t.Error("Client certificate should exist")
		}
		if !tm.CertificateExists("mqtt", TLSCertTypeKey) {
			t.Error("Client key should exist")
		}
	})

	t.Run("RemoveCertificate", func(t *testing.T) {
		// Remove CA certificate
		err := tm.RemoveCertificate("mqtt", TLSCertTypeCA)
		if err != nil {
			t.Errorf("Failed to remove CA certificate: %v", err)
		}

		// Verify it's gone
		if tm.CertificateExists("mqtt", TLSCertTypeCA) {
			t.Error("CA certificate should not exist after removal")
		}
	})

	t.Run("RemoveAllCertificates", func(t *testing.T) {
		// First save some certificates
		if _, err := tm.SaveCertificate("mysql", TLSCertTypeCA, caCert); err != nil {
			t.Errorf("Failed to save CA cert: %v", err)
		}
		if _, err := tm.SaveCertificate("mysql", TLSCertTypeClient, clientCert); err != nil {
			t.Errorf("Failed to save client cert: %v", err)
		}
		if _, err := tm.SaveCertificate("mysql", TLSCertTypeKey, clientKey); err != nil {
			t.Errorf("Failed to save client key: %v", err)
		}

		// Remove all
		err := tm.RemoveAllCertificates("mysql")
		if err != nil {
			t.Errorf("Failed to remove all certificates: %v", err)
		}

		// Verify all are gone
		if tm.CertificateExists("mysql", TLSCertTypeCA) ||
			tm.CertificateExists("mysql", TLSCertTypeClient) ||
			tm.CertificateExists("mysql", TLSCertTypeKey) {
			t.Error("Certificates should not exist after RemoveAllCertificates")
		}
	})

	t.Run("EmptyContentRemovesCertificate", func(t *testing.T) {
		// Save a certificate
		_, err := tm.SaveCertificate("redis", TLSCertTypeCA, caCert)
		if err != nil {
			t.Errorf("Failed to save certificate: %v", err)
		}

		// Save empty content
		path, err := tm.SaveCertificate("redis", TLSCertTypeCA, "")
		if err != nil {
			t.Errorf("Failed to save empty content: %v", err)
		}
		if path != "" {
			t.Error("Path should be empty when saving empty content")
		}

		// Verify certificate is removed
		if tm.CertificateExists("redis", TLSCertTypeCA) {
			t.Error("Certificate should be removed when saving empty content")
		}
	})

	t.Run("InvalidCertificateValidation", func(t *testing.T) {
		// Test invalid PEM
		_, err := tm.SaveCertificate("test", TLSCertTypeCA, "invalid certificate")
		if err == nil {
			t.Error("Should fail with invalid certificate")
		}

		// Test wrong block type for certificate
		wrongTypePEM := pem.EncodeToMemory(&pem.Block{Type: "PRIVATE KEY", Bytes: []byte("test")})
		_, err = tm.SaveCertificate("test", TLSCertTypeCA, string(wrongTypePEM))
		if err == nil {
			t.Error("Should fail with wrong block type for certificate")
		}

		// Test invalid key format
		invalidKeyPEM := pem.EncodeToMemory(&pem.Block{Type: "RSA PRIVATE KEY", Bytes: []byte("invalid key data")})
		_, err = tm.SaveCertificate("test", TLSCertTypeKey, string(invalidKeyPEM))
		if err == nil {
			t.Error("Should fail with invalid RSA key data")
		}
	})

	t.Run("ConcurrentAccess", func(t *testing.T) {
		// Test concurrent saves
		var wg sync.WaitGroup
		errors := make(chan error, 10)

		for i := 0; i < 10; i++ {
			wg.Add(1)
			go func(idx int) {
				defer wg.Done()
				service := "concurrent"
				_, err := tm.SaveCertificate(service, TLSCertTypeCA, caCert)
				if err != nil {
					errors <- err
				}
			}(i)
		}

		wg.Wait()
		close(errors)

		// Check for errors
		for err := range errors {
			t.Errorf("Concurrent save error: %v", err)
		}

		// Verify certificate exists
		if !tm.CertificateExists("concurrent", TLSCertTypeCA) {
			t.Error("Certificate should exist after concurrent saves")
		}
	})

	t.Run("ServiceIsolation", func(t *testing.T) {
		// Save certificates for different services
		if _, err := tm.SaveCertificate("service1", TLSCertTypeCA, caCert); err != nil {
			t.Errorf("Failed to save service1 CA cert: %v", err)
		}
		if _, err := tm.SaveCertificate("service2", TLSCertTypeCA, caCert); err != nil {
			t.Errorf("Failed to save service2 CA cert: %v", err)
		}

		// Remove service1 certificates
		if err := tm.RemoveAllCertificates("service1"); err != nil {
			t.Errorf("Failed to remove service1 certificates: %v", err)
		}

		// Verify service2 certificates still exist
		if !tm.CertificateExists("service2", TLSCertTypeCA) {
			t.Error("Service2 certificates should not be affected by service1 removal")
		}
	})

	t.Run("DirectoryPermissions", func(t *testing.T) {
		// Create a new manager to test directory creation
		newTempDir := filepath.Join(tempDir, "new-test")
		newTm := NewTLSManager(newTempDir)

		// Save a certificate to trigger directory creation
		_, err := newTm.SaveCertificate("perm-test", TLSCertTypeCA, caCert)
		if err != nil {
			t.Errorf("Failed to save certificate: %v", err)
		}

		// Check TLS directory permissions
		tlsDir := filepath.Join(newTempDir, "tls")
		info, err := os.Stat(tlsDir)
		if err != nil {
			t.Errorf("Failed to stat TLS directory: %v", err)
		}
		if info.Mode().Perm() != 0o700 {
			t.Errorf("TLS directory has wrong permissions: %v", info.Mode().Perm())
		}

		// Check service directory permissions
		serviceDir := filepath.Join(tlsDir, "perm-test")
		serviceInfo, err := os.Stat(serviceDir)
		if err != nil {
			t.Errorf("Failed to stat service directory: %v", err)
		}
		if serviceInfo.Mode().Perm() != 0o700 {
			t.Errorf("Service directory has wrong permissions: %v", serviceInfo.Mode().Perm())
		}
	})
}

func TestGetTLSManager(t *testing.T) {
	// Test that GetTLSManager returns a valid manager
	manager := GetTLSManager()
	if manager == nil {
		t.Error("GetTLSManager should not return nil")
	}

	// Test that subsequent calls return the same instance
	manager2 := GetTLSManager()
	if manager != manager2 {
		t.Error("GetTLSManager should return the same instance")
	}
}

// Helper function to generate an EC private key
func generateECKey(t *testing.T) string {
	priv, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatalf("Failed to generate EC key: %v", err)
	}
	
	keyBytes, err := x509.MarshalECPrivateKey(priv)
	if err != nil {
		t.Fatalf("Failed to marshal EC key: %v", err)
	}
	
	return string(pem.EncodeToMemory(&pem.Block{Type: "EC PRIVATE KEY", Bytes: keyBytes}))
}

// Helper function to generate a PKCS8 private key
func generatePKCS8Key(t *testing.T) string {
	priv, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		t.Fatalf("Failed to generate RSA key: %v", err)
	}
	
	keyBytes, err := x509.MarshalPKCS8PrivateKey(priv)
	if err != nil {
		t.Fatalf("Failed to marshal PKCS8 key: %v", err)
	}
	
	return string(pem.EncodeToMemory(&pem.Block{Type: "PRIVATE KEY", Bytes: keyBytes}))
}

func TestValidateCertificateContent(t *testing.T) {
	// Generate test certificates
	_, certPEM, keyPEM := generateTestCertificate(t)
	ecKeyPEM := generateECKey(t)
	pkcs8KeyPEM := generatePKCS8Key(t)

	tests := []struct {
		name     string
		certType TLSCertificateType
		content  string
		wantErr  bool
	}{
		{"Valid CA certificate", TLSCertTypeCA, certPEM, false},
		{"Valid client certificate", TLSCertTypeClient, certPEM, false},
		{"Valid RSA private key", TLSCertTypeKey, keyPEM, false},
		{"Valid EC private key", TLSCertTypeKey, ecKeyPEM, false},
		{"Valid PKCS8 private key", TLSCertTypeKey, pkcs8KeyPEM, false},
		{"Empty content", TLSCertTypeCA, "", true},
		{"Invalid PEM", TLSCertTypeCA, "not a pem", true},
		{"Wrong block type for cert", TLSCertTypeCA, string(pem.EncodeToMemory(&pem.Block{Type: "PRIVATE KEY", Bytes: []byte("test")})), true},
		{"Invalid certificate data", TLSCertTypeCA, string(pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: []byte("invalid")})), true},
		{"Invalid RSA key data", TLSCertTypeKey, string(pem.EncodeToMemory(&pem.Block{Type: "RSA PRIVATE KEY", Bytes: []byte("invalid")})), true},
		{"Invalid EC key data", TLSCertTypeKey, string(pem.EncodeToMemory(&pem.Block{Type: "EC PRIVATE KEY", Bytes: []byte("invalid")})), true},
		{"Invalid PKCS8 key data", TLSCertTypeKey, string(pem.EncodeToMemory(&pem.Block{Type: "PRIVATE KEY", Bytes: []byte("invalid")})), true},
		{"Unknown key type", TLSCertTypeKey, string(pem.EncodeToMemory(&pem.Block{Type: "UNKNOWN KEY", Bytes: []byte("test")})), true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := validateCertificateContent(tt.certType, tt.content)
			if (err != nil) != tt.wantErr {
				t.Errorf("validateCertificateContent() error = %v, wantErr %v", err, tt.wantErr)
			}
		})
	}
}