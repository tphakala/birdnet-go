package backup

import (
	"crypto/aes"
	"crypto/cipher"
	"crypto/rand"
	"encoding/hex"
	"os"
	"path/filepath"
	"strings"

	"github.com/tphakala/birdnet-go/internal/conf"
)

// getEncryptionKeyPath returns the path to the encryption key file
func (m *Manager) getEncryptionKeyPath() (string, error) {
	// Get the config directory
	configPaths, err := conf.GetDefaultConfigPaths()
	if err != nil {
		return "", NewError(ErrConfig, "failed to get config paths", err)
	}
	if len(configPaths) == 0 {
		return "", NewError(ErrConfig, "no config paths available", nil)
	}

	// Use the first config path (which should be the active one)
	return filepath.Join(configPaths[0], "encryption.key"), nil
}

// getEncryptionKey returns the encryption key, generating it if necessary
func (m *Manager) getEncryptionKey() ([]byte, error) {
	if !m.config.Encryption {
		return nil, NewError(ErrConfig, "encryption is not enabled", nil)
	}

	// Get the encryption key file path
	keyPath, err := m.getEncryptionKeyPath()
	if err != nil {
		return nil, err
	}

	// Try to read the existing key file
	keyBytes, err := os.ReadFile(keyPath)
	if err != nil {
		if !os.IsNotExist(err) {
			return nil, NewError(ErrIO, "failed to read encryption key file", err)
		}

		// Generate a new key if the file doesn't exist
		key := make([]byte, 32) // 256 bits
		if _, err := rand.Read(key); err != nil {
			return nil, NewError(ErrEncryption, "failed to generate encryption key", err)
		}

		// Encode the key as hex
		keyHex := hex.EncodeToString(key)

		// Create the config directory if it doesn't exist
		if err := os.MkdirAll(filepath.Dir(keyPath), 0o700); err != nil {
			return nil, NewError(ErrIO, "failed to create config directory", err)
		}

		// Write the key to the file with secure permissions
		if err := os.WriteFile(keyPath, []byte(keyHex), 0o600); err != nil {
			return nil, NewError(ErrIO, "failed to write encryption key file", err)
		}

		return key, nil
	}

	// Decode existing key from hex
	keyStr := strings.TrimSpace(string(keyBytes))
	key, err := hex.DecodeString(keyStr)
	if err != nil {
		return nil, NewError(ErrEncryption, "failed to decode encryption key", err)
	}

	// Validate key length
	if len(key) != 32 {
		return nil, NewError(ErrEncryption, "invalid encryption key length", nil)
	}

	return key, nil
}

// encryptData encrypts data using AES-256-GCM
func encryptData(data, key []byte) ([]byte, error) {
	block, err := aes.NewCipher(key)
	if err != nil {
		return nil, NewError(ErrEncryption, "failed to create cipher", err)
	}

	gcm, err := cipher.NewGCM(block)
	if err != nil {
		return nil, NewError(ErrEncryption, "failed to create GCM", err)
	}

	// Generate a random nonce
	nonce := make([]byte, gcm.NonceSize())
	if _, err := rand.Read(nonce); err != nil {
		return nil, NewError(ErrEncryption, "failed to generate nonce", err)
	}

	// Encrypt the data
	ciphertext := gcm.Seal(nonce, nonce, data, nil)
	return ciphertext, nil
}

// decryptData decrypts data using AES-256-GCM
func decryptData(encryptedData, key []byte) ([]byte, error) {
	block, err := aes.NewCipher(key)
	if err != nil {
		return nil, NewError(ErrEncryption, "failed to create cipher", err)
	}

	gcm, err := cipher.NewGCM(block)
	if err != nil {
		return nil, NewError(ErrEncryption, "failed to create GCM", err)
	}

	if len(encryptedData) < gcm.NonceSize() {
		return nil, NewError(ErrEncryption, "encrypted data too short", nil)
	}

	nonce := encryptedData[:gcm.NonceSize()]
	ciphertext := encryptedData[gcm.NonceSize():]

	plaintext, err := gcm.Open(nil, nonce, ciphertext, nil)
	if err != nil {
		return nil, NewError(ErrEncryption, "failed to decrypt data", err)
	}

	return plaintext, nil
}

// GenerateEncryptionKey generates a new encryption key and saves it to the default location
func (m *Manager) GenerateEncryptionKey() (string, error) {
	// Generate a new 256-bit key
	key := make([]byte, 32)
	if _, err := rand.Read(key); err != nil {
		return "", NewError(ErrEncryption, "failed to generate random key", err)
	}

	// Convert key to hex string
	keyHex := hex.EncodeToString(key)

	// Get the key file path
	keyPath, err := m.getEncryptionKeyPath()
	if err != nil {
		return "", err
	}

	// Create the config directory if it doesn't exist
	if err := os.MkdirAll(filepath.Dir(keyPath), 0o700); err != nil {
		return "", NewError(ErrIO, "failed to create config directory", err)
	}

	// Write the key to file with secure permissions
	if err := os.WriteFile(keyPath, []byte(keyHex), 0o600); err != nil {
		return "", NewError(ErrIO, "failed to write encryption key file", err)
	}

	m.logger.Printf("✅ Encryption key saved to: %s", keyPath)
	return keyHex, nil
}

// ValidateEncryption checks if encryption is properly configured
func (m *Manager) ValidateEncryption() error {
	if !m.config.Encryption {
		return nil // Encryption is not enabled, no validation needed
	}

	// Try to read the encryption key
	key, err := m.getEncryptionKey()
	if err != nil {
		if os.IsNotExist(err) {
			return NewError(ErrEncryption, "encryption is enabled but no key file found, please generate a key first", err)
		}
		return NewError(ErrEncryption, "failed to read encryption key", err)
	}

	// Validate key length
	if len(key) != 32 {
		return NewError(ErrEncryption, "invalid encryption key length", nil)
	}

	return nil
}

// GetEncryptionKey returns the current encryption key
func (m *Manager) GetEncryptionKey() ([]byte, error) {
	return m.getEncryptionKey()
}

// DecryptData decrypts the provided data using the configured encryption key
func (m *Manager) DecryptData(encryptedData []byte) ([]byte, error) {
	if !m.config.Encryption {
		return nil, NewError(ErrEncryption, "encryption is not enabled", nil)
	}

	key, err := m.getEncryptionKey()
	if err != nil {
		return nil, err
	}

	return decryptData(encryptedData, key)
}
