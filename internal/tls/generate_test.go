package tls

import (
	"crypto/x509"
	"encoding/pem"
	"net"
	"testing"
	"time"

	"crypto/tls"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestGenerateSelfSigned_BasicGeneration(t *testing.T) {
	t.Parallel()

	opts := SelfSignedOptions{
		Validity: 365 * 24 * time.Hour,
		SANs:     []string{"localhost", "127.0.0.1", "birds.example.com"},
	}

	certPEM, keyPEM, err := GenerateSelfSigned(opts)
	require.NoError(t, err)
	assert.Contains(t, certPEM, "BEGIN CERTIFICATE")
	assert.Contains(t, keyPEM, "BEGIN EC PRIVATE KEY")

	_, err = tls.X509KeyPair([]byte(certPEM), []byte(keyPEM))
	require.NoError(t, err)
}

func TestGenerateSelfSigned_SANsPopulated(t *testing.T) {
	t.Parallel()

	opts := SelfSignedOptions{
		Validity: 24 * time.Hour,
		SANs:     []string{"localhost", "127.0.0.1", "192.168.1.50", "birds.example.com"},
	}

	certPEM, _, err := GenerateSelfSigned(opts)
	require.NoError(t, err)

	block, _ := pem.Decode([]byte(certPEM))
	require.NotNil(t, block)

	cert, err := x509.ParseCertificate(block.Bytes)
	require.NoError(t, err)

	assert.Contains(t, cert.DNSNames, "localhost")
	assert.Contains(t, cert.DNSNames, "birds.example.com")
	assert.Contains(t, cert.IPAddresses, net.ParseIP("127.0.0.1").To4())
	assert.Contains(t, cert.IPAddresses, net.ParseIP("192.168.1.50").To4())
}

func TestGenerateSelfSigned_ValidityPeriod(t *testing.T) {
	t.Parallel()

	validity := 90 * 24 * time.Hour
	opts := SelfSignedOptions{
		Validity: validity,
		SANs:     []string{"localhost"},
	}

	certPEM, _, err := GenerateSelfSigned(opts)
	require.NoError(t, err)

	block, _ := pem.Decode([]byte(certPEM))
	cert, err := x509.ParseCertificate(block.Bytes)
	require.NoError(t, err)

	expectedExpiry := time.Now().Add(validity)
	assert.InDelta(t, expectedExpiry.Unix(), cert.NotAfter.Unix(), 60)
}

func TestGenerateSelfSigned_ECKeyType(t *testing.T) {
	t.Parallel()

	opts := SelfSignedOptions{
		Validity: 24 * time.Hour,
		SANs:     []string{"localhost"},
	}

	certPEM, _, err := GenerateSelfSigned(opts)
	require.NoError(t, err)

	block, _ := pem.Decode([]byte(certPEM))
	cert, err := x509.ParseCertificate(block.Bytes)
	require.NoError(t, err)

	assert.Equal(t, x509.ECDSA, cert.PublicKeyAlgorithm)
	assert.Contains(t, cert.Subject.CommonName, "BirdNET-Go")
}

func TestGenerateSelfSigned_HasServerAuth(t *testing.T) {
	t.Parallel()

	opts := SelfSignedOptions{
		Validity: 24 * time.Hour,
		SANs:     []string{"localhost"},
	}

	certPEM, _, err := GenerateSelfSigned(opts)
	require.NoError(t, err)

	block, _ := pem.Decode([]byte(certPEM))
	cert, err := x509.ParseCertificate(block.Bytes)
	require.NoError(t, err)

	assert.Contains(t, cert.ExtKeyUsage, x509.ExtKeyUsageServerAuth)
}
