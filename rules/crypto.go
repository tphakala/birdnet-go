//go:build ruleguard

package gorules

import "github.com/quasilyte/go-ruleguard/dsl"

// DeprecatedCipherModes detects deprecated cipher modes from crypto/cipher.
//
// Deprecated modes (Go 1.24):
//   - cipher.NewOFB
//   - cipher.NewCFBEncrypter
//   - cipher.NewCFBDecrypter
//
// These modes are not authenticated and are vulnerable to active attacks.
// Use AEAD modes (GCM, CCM) or CTR mode instead.
//
// Old patterns:
//
//	stream := cipher.NewOFB(block, iv)
//	stream := cipher.NewCFBEncrypter(block, iv)
//	stream := cipher.NewCFBDecrypter(block, iv)
//
// Recommended alternatives:
//
//	// For authenticated encryption (preferred):
//	aead, _ := cipher.NewGCM(block)
//	ciphertext := aead.Seal(nil, nonce, plaintext, additionalData)
//
//	// For stream cipher without authentication:
//	stream := cipher.NewCTR(block, iv)
//
// See: https://pkg.go.dev/crypto/cipher#NewGCM
// See: https://pkg.go.dev/crypto/cipher#NewCTR
func DeprecatedCipherModes(m dsl.Matcher) {
	m.Match(
		`cipher.NewOFB($block, $iv)`,
	).
		Report("cipher.NewOFB is deprecated in Go 1.24: OFB mode is not authenticated and vulnerable to active attacks; use cipher.NewGCM (AEAD) or cipher.NewCTR instead")

	m.Match(
		`cipher.NewCFBEncrypter($block, $iv)`,
	).
		Report("cipher.NewCFBEncrypter is deprecated in Go 1.24: CFB mode is not authenticated and vulnerable to active attacks; use cipher.NewGCM (AEAD) or cipher.NewCTR instead")

	m.Match(
		`cipher.NewCFBDecrypter($block, $iv)`,
	).
		Report("cipher.NewCFBDecrypter is deprecated in Go 1.24: CFB mode is not authenticated and vulnerable to active attacks; use cipher.NewGCM (AEAD) or cipher.NewCTR instead")
}

// WeakRSAKeySize detects RSA key generation with sizes less than 2048 bits.
//
// Go 1.24 enforces minimum 1024-bit RSA keys, but 2048 bits is the modern recommendation.
//
// Weak pattern:
//
//	key, _ := rsa.GenerateKey(rand.Reader, 1024)
//
// Recommended:
//
//	key, _ := rsa.GenerateKey(rand.Reader, 2048)  // Minimum recommended
//	key, _ := rsa.GenerateKey(rand.Reader, 4096)  // For long-term security
//
// See: https://pkg.go.dev/crypto/rsa#GenerateKey
func WeakRSAKeySize(m dsl.Matcher) {
	// Flag 1024-bit keys (allowed but weak)
	m.Match(
		`rsa.GenerateKey($rand, 1024)`,
	).
		Report("RSA 1024-bit keys are considered weak; use at least 2048 bits for modern security")

	// Flag explicitly small keys (will error in Go 1.24+)
	m.Match(
		`rsa.GenerateKey($rand, 512)`,
		`rsa.GenerateKey($rand, 768)`,
	).
		Report("RSA keys smaller than 1024 bits are rejected in Go 1.24+; use at least 2048 bits")
}

// DeprecatedElliptic detects deprecated crypto/elliptic usage and suggests
// using crypto/ecdh instead.
//
// Deprecated pattern:
//
//	import "crypto/elliptic"
//	curve := elliptic.P256()
//	key, _ := elliptic.GenerateKey(curve, rand.Reader)
//
// New pattern (Go 1.21+):
//
//	import "crypto/ecdh"
//	key, _ := ecdh.P256().GenerateKey(rand.Reader)
//
// Benefits:
//   - Modern, safer API
//   - Better encapsulation of key material
//   - Cleaner interface
//
// See: https://pkg.go.dev/crypto/ecdh
func DeprecatedElliptic(m dsl.Matcher) {
	m.Match(
		`elliptic.GenerateKey($curve, $rand)`,
	).
		Report("elliptic.GenerateKey is deprecated; use crypto/ecdh package instead (Go 1.21+)")

	m.Match(
		`elliptic.Marshal($curve, $x, $y)`,
	).
		Report("elliptic.Marshal is deprecated; use crypto/ecdh package instead (Go 1.21+)")

	m.Match(
		`elliptic.Unmarshal($curve, $data)`,
	).
		Report("elliptic.Unmarshal is deprecated; use crypto/ecdh package instead (Go 1.21+)")
}

// DeprecatedRSAMultiPrime detects deprecated rsa.GenerateMultiPrimeKey.
//
// Deprecated pattern:
//
//	key, _ := rsa.GenerateMultiPrimeKey(rand.Reader, nprimes, bits)
//
// Use instead:
//
//	key, _ := rsa.GenerateKey(rand.Reader, bits)
//
// Multi-prime RSA keys are rarely needed and the function is deprecated.
//
// See: https://pkg.go.dev/crypto/rsa#GenerateKey
func DeprecatedRSAMultiPrime(m dsl.Matcher) {
	m.Match(
		`rsa.GenerateMultiPrimeKey($rand, $nprimes, $bits)`,
	).
		Report("rsa.GenerateMultiPrimeKey is deprecated; use rsa.GenerateKey for standard 2-prime RSA (Go 1.21+)")
}

// DeprecatedPKCS1v15 detects deprecated PKCS#1 v1.5 encryption functions
// which are vulnerable to Bleichenbacher padding oracle attacks.
//
// Deprecated patterns (Go 1.26):
//
//	ciphertext, _ := rsa.EncryptPKCS1v15(rand.Reader, pub, plaintext)
//	plaintext, _ := rsa.DecryptPKCS1v15(rand.Reader, priv, ciphertext)
//	rsa.DecryptPKCS1v15SessionKey(rand.Reader, priv, ciphertext, key)
//
// Recommended alternatives:
//
//	// OAEP encryption (preferred):
//	ciphertext, _ := rsa.EncryptOAEP(sha256.New(), rand.Reader, pub, plaintext, nil)
//	plaintext, _ := rsa.DecryptOAEP(sha256.New(), rand.Reader, priv, ciphertext, nil)
//
//	// OAEP with separate MGF1 hash (Go 1.26+):
//	ciphertext, _ := rsa.EncryptOAEPWithOptions(rand.Reader, pub, plaintext,
//	    &rsa.OAEPOptions{Hash: crypto.SHA256, MGFHash: crypto.SHA1})
//
// PKCS#1 v1.5 encryption is vulnerable to Bleichenbacher's chosen-ciphertext
// attack, which can allow an attacker to decrypt ciphertexts by observing
// padding errors. OAEP provides provable security against this class of attack.
//
// See: https://pkg.go.dev/crypto/rsa#EncryptOAEP
// See: https://pkg.go.dev/crypto/rsa#EncryptOAEPWithOptions
func DeprecatedPKCS1v15(m dsl.Matcher) {
	m.Match(
		`rsa.EncryptPKCS1v15($rand, $pub, $msg)`,
	).
		Report("rsa.EncryptPKCS1v15 is deprecated in Go 1.26: PKCS#1 v1.5 encryption is vulnerable to Bleichenbacher attacks; use rsa.EncryptOAEP instead")

	m.Match(
		`rsa.DecryptPKCS1v15($rand, $priv, $ciphertext)`,
	).
		Report("rsa.DecryptPKCS1v15 is deprecated in Go 1.26: PKCS#1 v1.5 encryption is vulnerable to Bleichenbacher attacks; use rsa.DecryptOAEP instead")

	m.Match(
		`rsa.DecryptPKCS1v15SessionKey($rand, $priv, $ciphertext, $key)`,
	).
		Report("rsa.DecryptPKCS1v15SessionKey is deprecated in Go 1.26: PKCS#1 v1.5 encryption is vulnerable to Bleichenbacher attacks; use OAEP-based encryption instead")
}
