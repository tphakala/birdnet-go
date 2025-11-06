package backup

import (
	"crypto/aes"
	"crypto/cipher"
	"crypto/rand"
	"encoding/hex"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/tphakala/birdnet-go/internal/conf"
	"github.com/tphakala/birdnet-go/internal/errors"
)

// getEncryptionKeyPath returns the path to the encryption key file
func (m *Manager) getEncryptionKeyPath() (string, error) {
	// Get the config directory
	configPaths, err := conf.GetDefaultConfigPaths()
	if err != nil {
		return "", errors.New(err).
			Component("backup").
			Category(errors.CategoryConfiguration).
			Context("operation", "get_encryption_key_path").
			Build()
	}
	if len(configPaths) == 0 {
		return "", errors.Newf("no config paths available").
			Component("backup").
			Category(errors.CategoryConfiguration).
			Context("operation", "get_encryption_key_path").
			Build()
	}

	// Use the first config path (which should be the active one)
	return filepath.Join(configPaths[0], "encryption.key"), nil
}

// getEncryptionKey returns the encryption key, generating it if necessary
func (m *Manager) getEncryptionKey() ([]byte, error) {
	if !m.config.Encryption {
		return nil, errors.Newf("encryption is not enabled").
			Component("backup").
			Category(errors.CategoryConfiguration).
			Context("operation", "get_encryption_key").
			Build()
	}

	// Get the encryption key file path
	keyPath, err := m.getEncryptionKeyPath()
	if err != nil {
		return nil, err
	}

	// Try to read the existing key file with secure path validation
	secureOp := NewSecureFileOp("backup")
	keyBytes, cleanKeyPath, err := secureOp.SecureReadFile(keyPath)
	if err != nil {
		// Check if it's a file not found error by checking if file exists
		if _, statErr := os.Stat(cleanKeyPath); !os.IsNotExist(statErr) {
			return nil, err
		}

		// Generate a new key if the file doesn't exist
		key := make([]byte, 32) // 256 bits
		if _, err := rand.Read(key); err != nil {
			return nil, errors.New(err).
				Component("backup").
				Category(errors.CategorySystem).
				Context("operation", "generate_encryption_key").
				Build()
		}

		// Encode the key as hex
		keyHex := hex.EncodeToString(key)

		// Create the config directory if it doesn't exist
		if err := os.MkdirAll(filepath.Dir(keyPath), 0o700); err != nil {
			return nil, errors.New(err).
				Component("backup").
				Category(errors.CategoryFileIO).
				Context("operation", "create_config_directory").
				Context("dir_path", filepath.Dir(keyPath)).
				Build()
		}

		// Write the key to the file with secure permissions
		if err := os.WriteFile(keyPath, []byte(keyHex), 0o600); err != nil {
			return nil, errors.New(err).
				Component("backup").
				Category(errors.CategoryFileIO).
				Context("operation", "write_encryption_key").
				Context("key_path", keyPath).
				Build()
		}

		return key, nil
	}

	// Decode existing key from hex
	keyStr := strings.TrimSpace(string(keyBytes))
	key, err := hex.DecodeString(keyStr)
	if err != nil {
		return nil, errors.New(err).
			Component("backup").
			Category(errors.CategorySystem).
			Context("operation", "decode_encryption_key").
			Build()
	}

	// Validate key length
	if len(key) != 32 {
		return nil, errors.Newf("invalid encryption key length: expected 32 bytes, got %d", len(key)).
			Component("backup").
			Category(errors.CategoryValidation).
			Context("operation", "validate_encryption_key").
			Build()
	}

	return key, nil
}

// encryptData encrypts data using AES-256-GCM
func encryptData(data, key []byte) ([]byte, error) {
	block, err := aes.NewCipher(key)
	if err != nil {
		return nil, errors.New(err).
			Component("backup").
			Category(errors.CategorySystem).
			Context("operation", "create_cipher_for_encryption").
			Build()
	}

	gcm, err := cipher.NewGCM(block)
	if err != nil {
		return nil, errors.New(err).
			Component("backup").
			Category(errors.CategorySystem).
			Context("operation", "create_gcm_for_encryption").
			Build()
	}

	// Generate a random nonce
	nonce := make([]byte, gcm.NonceSize())
	if _, err := rand.Read(nonce); err != nil {
		return nil, errors.New(err).
			Component("backup").
			Category(errors.CategorySystem).
			Context("operation", "generate_nonce").
			Build()
	}

	// Encrypt the data
	ciphertext := gcm.Seal(nonce, nonce, data, nil)
	return ciphertext, nil
}

// decryptData decrypts data using AES-256-GCM
func decryptData(encryptedData, key []byte) ([]byte, error) {
	block, err := aes.NewCipher(key)
	if err != nil {
		return nil, errors.New(err).
			Component("backup").
			Category(errors.CategorySystem).
			Context("operation", "create_cipher_for_decryption").
			Build()
	}

	gcm, err := cipher.NewGCM(block)
	if err != nil {
		return nil, errors.New(err).
			Component("backup").
			Category(errors.CategorySystem).
			Context("operation", "create_gcm_for_decryption").
			Build()
	}

	if len(encryptedData) < gcm.NonceSize() {
		return nil, errors.Newf("encrypted data too short: expected at least %d bytes, got %d", gcm.NonceSize(), len(encryptedData)).
			Component("backup").
			Category(errors.CategoryValidation).
			Context("operation", "validate_encrypted_data_size").
			Build()
	}

	nonce := encryptedData[:gcm.NonceSize()]
	ciphertext := encryptedData[gcm.NonceSize():]

	plaintext, err := gcm.Open(nil, nonce, ciphertext, nil)
	if err != nil {
		return nil, errors.New(err).
			Component("backup").
			Category(errors.CategorySystem).
			Context("operation", "decrypt_data").
			Build()
	}

	return plaintext, nil
}

// GenerateEncryptionKey generates a new encryption key and saves it to the default location
func (m *Manager) GenerateEncryptionKey() (string, error) {
	m.logger.Info("Generating new encryption key...")
	start := time.Now()

	// Generate a new 256-bit key
	key := make([]byte, 32)
	if _, err := rand.Read(key); err != nil {
		return "", errors.New(err).
			Component("backup").
			Category(errors.CategorySystem).
			Context("operation", "generate_random_key").
			Build()
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
		return "", errors.New(err).
			Component("backup").
			Category(errors.CategoryFileIO).
			Context("operation", "create_config_directory_for_key").
			Context("dir_path", filepath.Dir(keyPath)).
			Build()
	}

	// Write the key to file with secure permissions
	if err := os.WriteFile(keyPath, []byte(keyHex), 0o600); err != nil {
		return "", errors.New(err).
			Component("backup").
			Category(errors.CategoryFileIO).
			Context("operation", "write_new_encryption_key").
			Context("key_path", keyPath).
			Build()
	}

	m.logger.Info("Encryption key generated and saved successfully",
		"path", keyPath,
		"duration_ms", time.Since(start).Milliseconds(),
	)
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
			return errors.Newf("encryption is enabled but no key file found, please generate a key first").
				Component("backup").
				Category(errors.CategoryConfiguration).
				Context("operation", "validate_encryption").
				Build()
		}
		return errors.New(err).
			Component("backup").
			Category(errors.CategoryFileIO).
			Context("operation", "read_encryption_key_for_validation").
			Build()
	}

	// Validate key length
	if len(key) != 32 {
		return errors.Newf("invalid encryption key length: expected 32 bytes, got %d", len(key)).
			Component("backup").
			Category(errors.CategoryValidation).
			Context("operation", "validate_encryption_key_length").
			Build()
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
		return nil, errors.Newf("encryption is not enabled").
			Component("backup").
			Category(errors.CategoryConfiguration).
			Context("operation", "decrypt_data").
			Build()
	}

	key, err := m.getEncryptionKey()
	if err != nil {
		return nil, err
	}

	m.logger.Debug("Retrieved encryption key")
	return decryptData(encryptedData, key)
}

// GetEncryptionKeyPath returns the path to the encryption key file
func (m *Manager) GetEncryptionKeyPath() (string, error) {
	return m.getEncryptionKeyPath()
}

// ImportEncryptionKey imports an encryption key from a file
func (m *Manager) ImportEncryptionKey(content []byte) error {
	m.logger.Info("Attempting to import encryption key")
	start := time.Now()
	defer func() {
		m.logger.Debug("Import encryption key processing completed", "duration_ms", time.Since(start).Milliseconds())
	}()

	// Parse the key file content
	lines := strings.Split(string(content), "\n")
	if len(lines) < 3 {
		return errors.Newf("invalid key file format: expected at least 3 lines, got %d", len(lines)).
			Component("backup").
			Category(errors.CategoryValidation).
			Context("operation", "import_encryption_key").
			Build()
	}

	// Verify the header
	if !strings.HasPrefix(lines[0], "BirdNET-Go Backup Encryption Key") {
		return errors.Newf("invalid key file format: missing 'BirdNET-Go Backup Encryption Key' header").
			Component("backup").
			Category(errors.CategoryValidation).
			Context("operation", "import_encryption_key").
			Context("expected_header", "BirdNET-Go Backup Encryption Key").
			Build()
	}

	// Extract the key
	var key string
	for _, line := range lines {
		if after, ok := strings.CutPrefix(line, "Key: "); ok {
			key = after
			key = strings.TrimSpace(key)
			break
		}
	}

	if key == "" {
		return errors.Newf("invalid key file format: 'Key: ' line not found").
			Component("backup").
			Category(errors.CategoryValidation).
			Context("operation", "import_encryption_key").
			Build()
	}

	// Validate key format (should be hex-encoded)
	if _, err := hex.DecodeString(key); err != nil {
		return errors.New(err).
			Component("backup").
			Category(errors.CategoryValidation).
			Context("operation", "validate_imported_key_format").
			Context("error_detail", "key must be hex-encoded").
			Build()
	}

	// Get the key file path FIRST
	keyPath, err := m.getEncryptionKeyPath()
	if err != nil {
		return err // Propagate error early
	}

	m.logger.Info("Attempting to import encryption key", "target_path", keyPath)
	start = time.Now()

	// Create the config directory if it doesn't exist
	err = os.MkdirAll(filepath.Dir(keyPath), 0o700)
	if err != nil {
		return errors.New(err).
			Component("backup").
			Category(errors.CategoryFileIO).
			Context("operation", "create_config_directory_for_import").
			Context("dir_path", filepath.Dir(keyPath)).
			Build()
	}

	// Write the key to file with secure permissions
	err = os.WriteFile(keyPath, []byte(key), 0o600)
	if err != nil {
		return errors.New(err).
			Component("backup").
			Category(errors.CategoryFileIO).
			Context("operation", "write_imported_encryption_key").
			Context("key_path", keyPath).
			Build()
	}

	m.logger.Info("Encryption key imported successfully",
		"path", keyPath,
		"duration_ms", time.Since(start).Milliseconds(),
	)
	return nil
}
