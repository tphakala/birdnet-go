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
	t.Helper()
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

// testCertificateData holds test certificate data
type testCertificateData struct {
	caCert     string
	clientCert string
	clientKey  string
}

// setupTestEnvironment creates a test environment with temporary directory and TLS manager
func setupTestEnvironment(t *testing.T) (tm *TLSManager, tempDir string) {
	t.Helper()
	tempDir = t.TempDir()
	tm = NewTLSManager(tempDir)
	return
}

// verifyCertificatePermissions checks if a file has the expected permissions
func verifyCertificatePermissions(t *testing.T, path string, expectedPerm os.FileMode) {
	t.Helper()
	info, err := os.Stat(path)
	if err != nil {
		t.Errorf("Failed to stat file: %v", err)
		return
	}
	if info.Mode().Perm() != expectedPerm {
		t.Errorf("File has wrong permissions: got %v, want %v", info.Mode().Perm(), expectedPerm)
	}
}

// saveCertificatesSet saves a complete set of certificates for testing
func saveCertificatesSet(t *testing.T, tm *TLSManager, service string, data testCertificateData) {
	t.Helper()
	if _, err := tm.SaveCertificate(service, TLSCertTypeCA, data.caCert); err != nil {
		t.Errorf("Failed to save CA cert: %v", err)
	}
	if _, err := tm.SaveCertificate(service, TLSCertTypeClient, data.clientCert); err != nil {
		t.Errorf("Failed to save client cert: %v", err)
	}
	if _, err := tm.SaveCertificate(service, TLSCertTypeKey, data.clientKey); err != nil {
		t.Errorf("Failed to save client key: %v", err)
	}
}

// verifyCertificatesExist checks if all certificates for a service exist
func verifyCertificatesExist(t *testing.T, tm *TLSManager, service string, shouldExist bool) {
	t.Helper()
	caExists := tm.CertificateExists(service, TLSCertTypeCA)
	clientExists := tm.CertificateExists(service, TLSCertTypeClient)
	keyExists := tm.CertificateExists(service, TLSCertTypeKey)

	if shouldExist {
		if !caExists {
			t.Error("CA certificate should exist")
		}
		if !clientExists {
			t.Error("Client certificate should exist")
		}
		if !keyExists {
			t.Error("Client key should exist")
		}
	} else {
		if caExists {
			t.Error("CA certificate should not exist")
		}
		if clientExists {
			t.Error("Client certificate should not exist")
		}
		if keyExists {
			t.Error("Client key should not exist")
		}
	}
}

func TestTLSManager(t *testing.T) {
	t.Parallel()
	tm, tempDir := setupTestEnvironment(t)

	// Generate test certificates
	caCert, clientCert, clientKey := generateTestCertificate(t)
	testData := testCertificateData{
		caCert:     caCert,
		clientCert: clientCert,
		clientKey:  clientKey,
	}

	t.Run("SaveAndRetrieveCertificates", func(t *testing.T) {
		t.Parallel()
		testSaveAndRetrieveCertificates(t, tm, testData)
	})

	t.Run("RemoveCertificate", func(t *testing.T) {
		t.Parallel()
		testRemoveCertificate(t, tm)
	})

	t.Run("RemoveAllCertificates", func(t *testing.T) {
		t.Parallel()
		testRemoveAllCertificates(t, tm, testData)
	})

	t.Run("EmptyContentRemovesCertificate", func(t *testing.T) {
		t.Parallel()
		testEmptyContentRemovesCertificate(t, tm, testData.caCert)
	})

	t.Run("InvalidCertificateValidation", func(t *testing.T) {
		t.Parallel()
		testInvalidCertificateValidation(t, tm)
	})

	t.Run("ConcurrentAccess", func(t *testing.T) {
		t.Parallel()
		testConcurrentAccess(t, tm, testData.caCert)
	})

	t.Run("ServiceIsolation", func(t *testing.T) {
		t.Parallel()
		testServiceIsolation(t, tm, testData.caCert)
	})

	t.Run("DirectoryPermissions", func(t *testing.T) {
		t.Parallel()
		testDirectoryPermissions(t, tempDir, testData.caCert)
	})
}

// testSaveAndRetrieveCertificates tests saving and retrieving certificates
func testSaveAndRetrieveCertificates(t *testing.T, tm *TLSManager, data testCertificateData) {
	t.Helper()
	// Save CA certificate
	caPath, err := tm.SaveCertificate("mqtt", TLSCertTypeCA, data.caCert)
	if err != nil {
		t.Errorf("Failed to save CA certificate: %v", err)
	}
	if caPath == "" {
		t.Error("CA certificate path is empty")
	}

	// Verify file exists and has correct permissions
	verifyCertificatePermissions(t, caPath, 0o644)

	// Save client certificate
	_, err = tm.SaveCertificate("mqtt", TLSCertTypeClient, data.clientCert)
	if err != nil {
		t.Errorf("Failed to save client certificate: %v", err)
	}

	// Save client key
	keyPath, err := tm.SaveCertificate("mqtt", TLSCertTypeKey, data.clientKey)
	if err != nil {
		t.Errorf("Failed to save client key: %v", err)
	}

	// Verify key has restricted permissions
	verifyCertificatePermissions(t, keyPath, 0o600)

	// Check existence
	verifyCertificatesExist(t, tm, "mqtt", true)
}

// testRemoveCertificate tests removing a single certificate
func testRemoveCertificate(t *testing.T, tm *TLSManager) {
	t.Helper()
	// Remove CA certificate
	err := tm.RemoveCertificate("mqtt", TLSCertTypeCA)
	if err != nil {
		t.Errorf("Failed to remove CA certificate: %v", err)
	}

	// Verify it's gone
	if tm.CertificateExists("mqtt", TLSCertTypeCA) {
		t.Error("CA certificate should not exist after removal")
	}
}

// testRemoveAllCertificates tests removing all certificates for a service
func testRemoveAllCertificates(t *testing.T, tm *TLSManager, data testCertificateData) {
	t.Helper()
	// First save some certificates
	saveCertificatesSet(t, tm, "mysql", data)

	// Remove all
	err := tm.RemoveAllCertificates("mysql")
	if err != nil {
		t.Errorf("Failed to remove all certificates: %v", err)
	}

	// Verify all are gone
	verifyCertificatesExist(t, tm, "mysql", false)
}

// testEmptyContentRemovesCertificate tests that saving empty content removes the certificate
func testEmptyContentRemovesCertificate(t *testing.T, tm *TLSManager, caCert string) {
	t.Helper()
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
}

// testInvalidCertificateValidation tests validation of invalid certificates
func testInvalidCertificateValidation(t *testing.T, tm *TLSManager) {
	t.Helper()
	testCases := []struct {
		name     string
		certType TLSCertificateType
		content  string
	}{
		{"invalid PEM", TLSCertTypeCA, "invalid certificate"},
		{"wrong block type for certificate", TLSCertTypeCA, string(pem.EncodeToMemory(&pem.Block{Type: "PRIVATE KEY", Bytes: []byte("test")}))},
		{"invalid key format", TLSCertTypeKey, string(pem.EncodeToMemory(&pem.Block{Type: "RSA PRIVATE KEY", Bytes: []byte("invalid key data")}))},
	}

	for _, tc := range testCases {
		_, err := tm.SaveCertificate("test", tc.certType, tc.content)
		if err == nil {
			t.Errorf("Should fail with %s", tc.name)
		}
	}
}

// testConcurrentAccess tests concurrent certificate saves
func testConcurrentAccess(t *testing.T, tm *TLSManager, caCert string) {
	t.Helper()
	// Test concurrent saves
	var wg sync.WaitGroup
	errors := make(chan error, 10)

	for i := range 10 {
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
}

// testServiceIsolation tests that certificates for different services are isolated
func testServiceIsolation(t *testing.T, tm *TLSManager, caCert string) {
	t.Helper()
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
}

// testDirectoryPermissions tests that directories are created with correct permissions
func testDirectoryPermissions(t *testing.T, tempDir, caCert string) {
	t.Helper()
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
	verifyCertificatePermissions(t, tlsDir, 0o700)

	// Check service directory permissions
	serviceDir := filepath.Join(tlsDir, "perm-test")
	verifyCertificatePermissions(t, serviceDir, 0o700)
}

func TestGetTLSManager(t *testing.T) {
	t.Parallel()
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
	t.Helper()
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
	t.Helper()
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
	t.Parallel()
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
			t.Parallel()
			err := validateCertificateContent(tt.certType, tt.content)
			if (err != nil) != tt.wantErr {
				t.Errorf("validateCertificateContent() error = %v, wantErr %v", err, tt.wantErr)
			}
		})
	}
}