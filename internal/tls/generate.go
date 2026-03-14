// Package tls provides self-signed certificate generation for BirdNET-Go's HTTPS server.
package tls

import (
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/pem"
	"math/big"
	"net"
	"time"

	"github.com/tphakala/birdnet-go/internal/errors"
)

// SelfSignedOptions configures self-signed certificate generation.
type SelfSignedOptions struct {
	// Validity is the duration the certificate is valid for.
	Validity time.Duration
	// SANs is a list of Subject Alternative Names (DNS names and IP addresses).
	SANs []string
}

// serialNumberBitLen is the bit length for random certificate serial numbers.
const serialNumberBitLen = 128

// GenerateSelfSigned creates a self-signed TLS certificate and private key
// using EC P-256. It returns PEM-encoded certificate and key strings.
func GenerateSelfSigned(opts SelfSignedOptions) (certPEM, keyPEM string, err error) {
	if opts.Validity <= 0 {
		return "", "", errors.Newf("certificate validity must be positive").
			Component("tls-generate").
			Category(errors.CategoryValidation).
			Build()
	}

	// Generate EC P-256 private key
	privateKey, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		return "", "", errors.New(err).
			Component("tls-generate").
			Category(errors.CategorySystem).
			Context("operation", "key-generation").
			Build()
	}

	// Generate random 128-bit serial number
	serialNumber, err := rand.Int(rand.Reader, new(big.Int).Lsh(big.NewInt(1), serialNumberBitLen))
	if err != nil {
		return "", "", errors.New(err).
			Component("tls-generate").
			Category(errors.CategorySystem).
			Context("operation", "serial-generation").
			Build()
	}

	// Separate DNS names and IP addresses from SANs
	var dnsNames []string
	var ipAddresses []net.IP
	for _, san := range opts.SANs {
		if ip := net.ParseIP(san); ip != nil {
			// Normalize IPv4-mapped IPv6 addresses to 4-byte form
			if v4 := ip.To4(); v4 != nil {
				ip = v4
			}
			ipAddresses = append(ipAddresses, ip)
		} else {
			dnsNames = append(dnsNames, san)
		}
	}

	now := time.Now()
	template := &x509.Certificate{
		SerialNumber: serialNumber,
		Subject: pkix.Name{
			CommonName: "BirdNET-Go",
		},
		NotBefore:             now,
		NotAfter:              now.Add(opts.Validity),
		KeyUsage:              x509.KeyUsageDigitalSignature,
		ExtKeyUsage:           []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth},
		BasicConstraintsValid: true,
		DNSNames:              dnsNames,
		IPAddresses:           ipAddresses,
	}

	// Self-sign the certificate
	certDER, err := x509.CreateCertificate(rand.Reader, template, template, &privateKey.PublicKey, privateKey)
	if err != nil {
		return "", "", errors.New(err).
			Component("tls-generate").
			Category(errors.CategorySystem).
			Context("operation", "certificate-creation").
			Build()
	}

	// PEM-encode the certificate
	certBlock := &pem.Block{
		Type:  "CERTIFICATE",
		Bytes: certDER,
	}

	// PEM-encode the private key
	keyDER, err := x509.MarshalECPrivateKey(privateKey)
	if err != nil {
		return "", "", errors.New(err).
			Component("tls-generate").
			Category(errors.CategorySystem).
			Context("operation", "key-marshal").
			Build()
	}
	keyBlock := &pem.Block{
		Type:  "EC PRIVATE KEY",
		Bytes: keyDER,
	}

	return string(pem.EncodeToMemory(certBlock)), string(pem.EncodeToMemory(keyBlock)), nil
}
