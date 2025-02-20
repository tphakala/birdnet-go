// Package backup provides functionality for backing up application data
package backup

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"context"
	"crypto/aes"
	"crypto/cipher"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/tphakala/birdnet-go/internal/conf"
	"gopkg.in/yaml.v3"
)

// writeCloserBuffer wraps bytes.Buffer to implement io.WriteCloser
type writeCloserBuffer struct {
	*bytes.Buffer
}

func (b *writeCloserBuffer) Close() error {
	return nil
}

// Source represents a data source that needs to be backed up
type Source interface {
	// Name returns the name of the source
	Name() string
	// Backup performs the backup operation and returns a reader for streaming the backup data
	Backup(ctx context.Context) (io.ReadCloser, error)
	// Validate validates the source configuration
	Validate() error
}

// Target represents a destination where backups are stored
type Target interface {
	// Name returns the name of the target
	Name() string
	// Store stores a backup file in the target's storage
	Store(ctx context.Context, sourcePath string, metadata *Metadata) error
	// List returns a list of stored backups
	List(ctx context.Context) ([]BackupInfo, error)
	// Delete deletes a backup from storage
	Delete(ctx context.Context, id string) error
	// Validate validates the target configuration
	Validate() error
}

// Metadata contains information about a backup
type Metadata struct {
	ID         string    // Unique identifier for the backup
	Timestamp  time.Time // When the backup was created
	Size       int64     // Size of the backup in bytes
	Type       string    // Type of backup (e.g., "sqlite", "mysql")
	Source     string    // Source of the backup (e.g., database name)
	IsDaily    bool      // Whether this is a daily backup
	ConfigHash string    // Hash of the configuration file (for verification)
	AppVersion string    // Version of the application that created the backup
}

// BackupInfo represents information about a stored backup
type BackupInfo struct {
	Metadata
	Target string // Name of the target storing this backup
}

// FileMetadata contains platform-specific file metadata
type FileMetadata struct {
	Mode   os.FileMode // File mode and permission bits
	UID    int         // User ID (Unix only)
	GID    int         // Group ID (Unix only)
	IsUnix bool        // Whether this metadata is from a Unix system
}

// sanitizeConfig creates a copy of the configuration with sensitive data removed
func sanitizeConfig(config *conf.Settings) *conf.Settings {
	// Create a deep copy of the config
	sanitized := *config

	// Remove sensitive information
	sanitized.Security.BasicAuth.Password = ""
	sanitized.Security.BasicAuth.ClientSecret = ""
	sanitized.Security.GoogleAuth.ClientSecret = ""
	sanitized.Security.GithubAuth.ClientSecret = ""
	sanitized.Security.SessionSecret = ""
	sanitized.Output.MySQL.Password = ""
	sanitized.Realtime.MQTT.Password = ""
	sanitized.Realtime.Weather.OpenWeather.APIKey = ""

	return &sanitized
}

// Manager handles the backup operations
type Manager struct {
	config  *conf.BackupConfig
	sources map[string]Source
	targets map[string]Target
	mu      sync.RWMutex
	logger  *log.Logger
}

// NewManager creates a new backup manager
func NewManager(config *conf.BackupConfig, logger *log.Logger) *Manager {
	if logger == nil {
		logger = log.Default()
	}

	return &Manager{
		config:  config,
		sources: make(map[string]Source),
		targets: make(map[string]Target),
		logger:  logger,
	}
}

// RegisterSource registers a backup source
func (m *Manager) RegisterSource(source Source) error {
	m.mu.Lock()
	defer m.mu.Unlock()

	if err := source.Validate(); err != nil {
		return NewError(ErrValidation, "invalid source configuration", err)
	}

	m.sources[source.Name()] = source
	return nil
}

// RegisterTarget registers a backup target
func (m *Manager) RegisterTarget(target Target) error {
	m.mu.Lock()
	defer m.mu.Unlock()

	if err := target.Validate(); err != nil {
		return NewError(ErrValidation, "invalid target configuration", err)
	}

	m.targets[target.Name()] = target
	return nil
}

// Start starts the backup manager
func (m *Manager) Start() error {
	if !m.config.Enabled {
		m.logger.Println("ℹ️ Backup manager is disabled")
		return nil
	}

	// Validate that we have at least one source and target
	if len(m.sources) == 0 {
		return NewError(ErrValidation, "no backup sources registered", nil)
	}
	if len(m.targets) == 0 {
		return NewError(ErrValidation, "no backup targets registered", nil)
	}

	m.logger.Printf("✅ Backup manager started")
	return nil
}

// RunBackup performs an immediate backup of all sources
func (m *Manager) RunBackup(ctx context.Context) error {
	m.mu.RLock()
	defer m.mu.RUnlock()

	// Add a timeout for the entire backup operation
	ctx, cancel := context.WithTimeout(ctx, m.getBackupTimeout())
	defer cancel()

	m.logger.Println("🔄 Starting backup process...")

	// Validate that we have at least one target
	if len(m.targets) == 0 {
		return NewError(ErrValidation, "no backup targets registered, backup cannot proceed", nil)
	}

	// Get current timestamp in UTC
	now := time.Now().UTC()
	isDaily := now.Hour() == 0 && now.Minute() < 15 // Consider it a daily backup if run between 00:00 and 00:15

	var allTempDirs []string
	var errs []error

	// Process each source
	for sourceName, source := range m.sources {
		select {
		case <-ctx.Done():
			// Clean up temp dirs before returning
			m.cleanupTempDirectories(allTempDirs)
			return NewError(ErrCanceled, "backup process cancelled", ctx.Err())
		default:
		}

		m.logger.Printf("🔄 Processing backup source: %s", sourceName)
		tempDirs, err := m.processBackupSource(ctx, sourceName, source, now, isDaily)
		allTempDirs = append(allTempDirs, tempDirs...)
		if err != nil {
			errs = append(errs, err)
			continue
		}
	}

	// Clean up temporary directories after all operations are complete
	defer func() {
		m.logger.Printf("🧹 Cleaning up %d temporary directories...", len(allTempDirs))
		m.cleanupTempDirectories(allTempDirs)
	}()

	if len(errs) > 0 {
		return combineErrors(errs)
	}

	m.logger.Println("✅ Backup process completed successfully")
	return nil
}

// processBackupSource handles the backup process for a single source
func (m *Manager) processBackupSource(ctx context.Context, sourceName string, source Source, now time.Time, isDaily bool) ([]string, error) {
	var tempDirs []string
	var errs []error

	// Create backup file with timeout
	m.logger.Printf("🔄 Creating backup file for source: %s", sourceName)
	backupReader, err := source.Backup(ctx)
	if err != nil {
		return tempDirs, err
	}
	defer backupReader.Close()

	// Create temporary directory for the archive
	tempDir, err := os.MkdirTemp("", "backup-*")
	if err != nil {
		return tempDirs, NewError(ErrIO, "failed to create temporary directory", err)
	}
	tempDirs = append(tempDirs, tempDir)

	// Create metadata
	metadata := Metadata{
		ID:        fmt.Sprintf("birdnet-go-backup-%s", now.Format("20060102-150405")),
		Timestamp: now,
		Type:      sourceName,
		IsDaily:   isDaily,
	}

	// Create the archive file
	archivePath := filepath.Join(tempDir, fmt.Sprintf("%s.tar.gz", metadata.ID))
	m.logger.Printf("🔄 Creating archive file at: %s", archivePath)

	// Create archive file
	archiveFile, err := os.Create(archivePath)
	if err != nil {
		return tempDirs, NewError(ErrIO, "failed to create archive", err)
	}
	defer archiveFile.Close()

	// Create gzip writer
	gzipWriter := gzip.NewWriter(archiveFile)
	defer gzipWriter.Close()

	// Create tar writer
	tarWriter := tar.NewWriter(gzipWriter)
	defer tarWriter.Close()

	// Add metadata
	metadataBytes, err := json.Marshal(metadata)
	if err != nil {
		return tempDirs, NewError(ErrIO, "failed to marshal metadata", err)
	}

	// Add metadata file to tar
	metadataHeader := &tar.Header{
		Name:    "metadata.json",
		Size:    int64(len(metadataBytes)),
		Mode:    0o644,
		ModTime: now,
	}
	if err := tarWriter.WriteHeader(metadataHeader); err != nil {
		return tempDirs, NewError(ErrIO, "failed to write metadata header", err)
	}
	if _, err := tarWriter.Write(metadataBytes); err != nil {
		return tempDirs, NewError(ErrIO, "failed to write metadata", err)
	}

	// Add database file to tar
	dbHeader := &tar.Header{
		Name:    fmt.Sprintf("%s.db", sourceName),
		Mode:    0o644,
		ModTime: now,
	}

	// Create a buffer to calculate the size
	var buf bytes.Buffer
	size, err := io.Copy(&buf, backupReader)
	if err != nil {
		return tempDirs, NewError(ErrIO, "failed to read backup data", err)
	}
	dbHeader.Size = size

	// Write the header and data
	if err := tarWriter.WriteHeader(dbHeader); err != nil {
		return tempDirs, NewError(ErrIO, "failed to write database header", err)
	}
	if _, err := io.Copy(tarWriter, &buf); err != nil {
		return tempDirs, NewError(ErrIO, "failed to write database content", err)
	}

	// Add configuration if available
	if err := m.addConfigToArchive(tarWriter, &metadata); err != nil {
		return tempDirs, err
	}

	// Close writers in correct order
	if err := tarWriter.Close(); err != nil {
		return tempDirs, NewError(ErrIO, "failed to close tar writer", err)
	}
	if err := gzipWriter.Close(); err != nil {
		return tempDirs, NewError(ErrIO, "failed to close gzip writer", err)
	}

	// Store backup in all targets
	if err := m.storeBackupInTargets(ctx, archivePath, &metadata); err != nil {
		return tempDirs, err
	}

	// Clean up old backups if this is a daily backup
	if isDaily {
		if err := m.performBackupCleanup(ctx); err != nil {
			errs = append(errs, err)
		}
	}

	if len(errs) > 0 {
		return tempDirs, combineErrors(errs)
	}
	return tempDirs, nil
}

// addConfigToArchive adds sanitized configuration to the archive
func (m *Manager) addConfigToArchive(tw *tar.Writer, metadata *Metadata) error {
	if settings := conf.Setting(); settings != nil {
		m.logger.Printf("🔄 Adding sanitized configuration to archive...")
		configData, err := yaml.Marshal(sanitizeConfig(settings))
		if err != nil {
			m.logger.Printf("⚠️ Failed to include configuration in backup: %v", err)
			return NewError(ErrIO, "failed to marshal configuration", err)
		}

		// Create header for config file
		header := &tar.Header{
			Name:    "config.yaml",
			Size:    int64(len(configData)),
			Mode:    0o644,
			ModTime: metadata.Timestamp,
		}

		if err := tw.WriteHeader(header); err != nil {
			return NewError(ErrIO, "failed to write config header", err)
		}
		if _, err := tw.Write(configData); err != nil {
			return NewError(ErrIO, "failed to write config data", err)
		}

		hash := sha256.Sum256(configData)
		metadata.ConfigHash = hex.EncodeToString(hash[:])
		m.logger.Printf("✅ Added configuration file to archive with hash: %s", metadata.ConfigHash)
	}
	return nil
}

// storeBackupInTargets stores the backup in all configured targets
func (m *Manager) storeBackupInTargets(ctx context.Context, archivePath string, metadata *Metadata) error {
	var errs []error
	m.logger.Printf("🔄 Storing backup in %d target(s)...", len(m.targets))
	for _, target := range m.targets {
		select {
		case <-ctx.Done():
			return NewError(ErrCanceled, "backup process cancelled", ctx.Err())
		default:
		}

		m.logger.Printf("🔄 Storing backup in target: %s", target.Name())
		storeCtx, storeCancel := context.WithTimeout(ctx, 15*time.Minute)
		err := target.Store(storeCtx, archivePath, metadata)
		storeCancel()
		if err != nil {
			if storeCtx.Err() != nil {
				m.logger.Printf("❌ Store operation timed out for target %s: %v", target.Name(), err)
				errs = append(errs, NewError(ErrTimeout, fmt.Sprintf("store operation timed out for target %s", target.Name()), err))
			} else {
				m.logger.Printf("❌ Failed to store backup in target %s: %v", target.Name(), err)
				errs = append(errs, NewError(ErrIO, fmt.Sprintf("failed to store backup in target %s", target.Name()), err))
			}
			continue
		}
		m.logger.Printf("✅ Successfully stored backup in target: %s", target.Name())
	}

	if len(errs) > 0 {
		return combineErrors(errs)
	}
	return nil
}

// performBackupCleanup handles the cleanup of old backups
func (m *Manager) performBackupCleanup(ctx context.Context) error {
	m.logger.Printf("Running cleanup of old backups...")
	cleanupCtx, cleanupCancel := context.WithTimeout(ctx, 10*time.Minute)
	defer cleanupCancel()

	err := m.cleanupOldBackups(cleanupCtx)
	if err != nil {
		if cleanupCtx.Err() != nil {
			m.logger.Printf("Warning: Cleanup operation timed out: %v", err)
			return NewError(ErrTimeout, "cleanup operation timed out", err)
		}
		m.logger.Printf("Warning: Failed to clean up old backups: %v", err)
		return NewError(ErrIO, "failed to clean up old backups", err)
	}
	return nil
}

// combineErrors combines multiple errors into a single error
func combineErrors(errs []error) error {
	if len(errs) == 0 {
		return nil
	}
	var errMsgs []string
	for _, err := range errs {
		errMsgs = append(errMsgs, err.Error())
	}
	return NewError(ErrUnknown, fmt.Sprintf("multiple errors occurred: %s", strings.Join(errMsgs, "; ")), nil)
}

// createArchive creates a backup archive, optionally encrypting it
func (m *Manager) createArchive(ctx context.Context, archivePath string, reader io.Reader, metadata *Metadata) error {
	// Create the archive file
	archiveFile, err := os.Create(archivePath)
	if err != nil {
		return NewError(ErrIO, "failed to create archive", err)
	}
	defer archiveFile.Close()

	var writer io.WriteCloser = archiveFile

	// If encryption is enabled, we'll encrypt after compression
	var encryptedBuf *writeCloserBuffer
	if m.config.Encryption {
		encryptedBuf = &writeCloserBuffer{bytes.NewBuffer(nil)}
		writer = encryptedBuf
	}

	// Create gzip writer
	gzipWriter := gzip.NewWriter(writer)
	defer gzipWriter.Close()

	// Create tar writer
	tarWriter := tar.NewWriter(gzipWriter)
	defer tarWriter.Close()

	// Add metadata
	if err := m.addMetadataToArchive(ctx, tarWriter, metadata); err != nil {
		return err
	}

	// Add the backup data
	if err := m.addBackupDataToArchive(ctx, tarWriter, reader, metadata); err != nil {
		return err
	}

	// Add configuration if available
	if err := m.addConfigToArchive(tarWriter, metadata); err != nil {
		return err
	}

	// Close writers in correct order
	if err := tarWriter.Close(); err != nil {
		return NewError(ErrIO, "failed to close tar writer", err)
	}
	if err := gzipWriter.Close(); err != nil {
		return NewError(ErrIO, "failed to close gzip writer", err)
	}

	// If encryption is enabled, encrypt the compressed data
	if m.config.Encryption {
		if err := m.encryptAndWriteArchive(ctx, archiveFile, encryptedBuf.Bytes()); err != nil {
			return err
		}
	}

	return nil
}

// addMetadataToArchive adds metadata to the archive
func (m *Manager) addMetadataToArchive(ctx context.Context, tw *tar.Writer, metadata *Metadata) error {
	metadataBytes, err := json.Marshal(metadata)
	if err != nil {
		return NewError(ErrIO, "failed to marshal metadata", err)
	}

	select {
	case <-ctx.Done():
		return NewError(ErrCanceled, "archive creation cancelled", ctx.Err())
	default:
	}

	header := &tar.Header{
		Name:    "metadata.json",
		Size:    int64(len(metadataBytes)),
		Mode:    0644,
		ModTime: metadata.Timestamp,
	}

	if err := tw.WriteHeader(header); err != nil {
		return NewError(ErrIO, "failed to write metadata header", err)
	}
	if _, err := tw.Write(metadataBytes); err != nil {
		return NewError(ErrIO, "failed to write metadata", err)
	}

	return nil
}

// addBackupDataToArchive adds the backup data to the archive
func (m *Manager) addBackupDataToArchive(ctx context.Context, tw *tar.Writer, reader io.Reader, metadata *Metadata) error {
	select {
	case <-ctx.Done():
		return NewError(ErrCanceled, "archive creation cancelled", ctx.Err())
	default:
	}

	header := &tar.Header{
		Name:    fmt.Sprintf("%s.db", metadata.Source),
		Mode:    0644,
		ModTime: metadata.Timestamp,
	}

	// Create a buffer to calculate the size
	var buf bytes.Buffer
	size, err := io.Copy(&buf, reader)
	if err != nil {
		return NewError(ErrIO, "failed to read backup data", err)
	}
	header.Size = size

	if err := tw.WriteHeader(header); err != nil {
		return NewError(ErrIO, "failed to write backup header", err)
	}
	if _, err := io.Copy(tw, &buf); err != nil {
		return NewError(ErrIO, "failed to write backup data", err)
	}

	return nil
}

// encryptAndWriteArchive encrypts and writes the archive data
func (m *Manager) encryptAndWriteArchive(ctx context.Context, writer io.Writer, data []byte) error {
	// Get or generate the encryption key
	key, err := m.getEncryptionKey()
	if err != nil {
		return err
	}

	select {
	case <-ctx.Done():
		return NewError(ErrCanceled, "archive encryption cancelled", ctx.Err())
	default:
	}

	// Encrypt the archive
	encryptedData, err := encryptData(data, key)
	if err != nil {
		return err
	}

	// Write the encrypted data
	if _, err := writer.Write(encryptedData); err != nil {
		return NewError(ErrIO, "failed to write encrypted archive", err)
	}

	return nil
}

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

// parseRetentionAge parses a retention age string (e.g., "30d", "6m", "1y") into a duration
func (m *Manager) parseRetentionAge(age string) (time.Duration, error) {
	if age == "" {
		return 0, nil
	}

	// Parse the number and unit
	var num int
	var unit string
	if _, err := fmt.Sscanf(age, "%d%s", &num, &unit); err != nil {
		return 0, NewError(ErrValidation, fmt.Sprintf("invalid retention age format: %s", age), err)
	}

	// Convert to duration
	switch unit {
	case "d":
		return time.Duration(num) * 24 * time.Hour, nil
	case "m":
		return time.Duration(num) * 30 * 24 * time.Hour, nil // approximate
	case "y":
		return time.Duration(num) * 365 * 24 * time.Hour, nil // approximate
	default:
		return 0, NewError(ErrValidation, fmt.Sprintf("invalid retention age unit: %s", unit), nil)
	}
}

// groupBackupsByTargetAndType organizes backups by target and type
func (m *Manager) groupBackupsByTargetAndType(backups []BackupInfo) map[string]map[string][]BackupInfo {
	backupsByTargetAndType := make(map[string]map[string][]BackupInfo)
	for i := range backups {
		backup := &backups[i]
		if _, ok := backupsByTargetAndType[backup.Target]; !ok {
			backupsByTargetAndType[backup.Target] = make(map[string][]BackupInfo)
		}
		backupsByTargetAndType[backup.Target][backup.Type] = append(backupsByTargetAndType[backup.Target][backup.Type], backups[i])
	}
	return backupsByTargetAndType
}

// getDailyBackups extracts and sorts daily backups from a backup list
func (m *Manager) getDailyBackups(backups []BackupInfo) []*BackupInfo {
	// Sort backups by timestamp (newest first)
	sort.Slice(backups, func(i, j int) bool {
		return backups[i].Timestamp.After(backups[j].Timestamp)
	})

	var dailyBackups []*BackupInfo
	for i := range backups {
		if backups[i].IsDaily {
			dailyBackups = append(dailyBackups, &backups[i])
		}
	}
	return dailyBackups
}

// shouldKeepBackup determines if a backup should be kept based on retention policy
func (m *Manager) shouldKeepBackup(index int, backup *BackupInfo, maxAge time.Duration, minBackups, maxBackups int) bool {
	// Always keep minimum number of backups
	if index < minBackups {
		return true
	}

	// Keep backups within max age
	if maxAge > 0 && time.Since(backup.Timestamp) < maxAge {
		return true
	}

	// Keep if within max backups limit
	if maxBackups > 0 && index < maxBackups {
		return true
	}

	return false
}

// deleteBackupWithTimeout attempts to delete a backup with a timeout
func (m *Manager) deleteBackupWithTimeout(ctx context.Context, backup *BackupInfo, targetName string) error {
	deleteCtx, deleteCancel := context.WithTimeout(ctx, 2*time.Minute)
	defer deleteCancel()

	err := m.DeleteBackup(deleteCtx, backup.ID)
	if err != nil {
		if deleteCtx.Err() != nil {
			return NewError(ErrTimeout, fmt.Sprintf("delete operation timed out for backup %s", backup.ID), err)
		}
		return NewError(ErrIO, fmt.Sprintf("failed to delete backup %s", backup.ID), err)
	}

	if m.config.Debug {
		m.logger.Printf("Deleted old backup %s from target %s", backup.ID, targetName)
	}
	return nil
}

// cleanupOldBackups removes old backups according to retention policy
func (m *Manager) cleanupOldBackups(ctx context.Context) error {
	// Get all backups
	backups, err := m.ListBackups(ctx)
	if err != nil {
		return NewError(ErrIO, "failed to list backups", err)
	}

	// Check context before proceeding
	select {
	case <-ctx.Done():
		return NewError(ErrCanceled, "cleanup operation cancelled", ctx.Err())
	default:
	}

	// Group backups by target and type
	backupsByTargetAndType := m.groupBackupsByTargetAndType(backups)

	// Parse retention policy
	maxAge, err := m.parseRetentionAge(m.config.Retention.MaxAge)
	if err != nil {
		m.logger.Printf("Warning: %v, using no maximum age", err)
		maxAge = 0
	}
	minBackups := m.config.Retention.MinBackups
	maxBackups := m.config.Retention.MaxBackups

	var errs []error
	// Process each target and type
	for targetName, typeBackups := range backupsByTargetAndType {
		// Check context before processing each target
		select {
		case <-ctx.Done():
			return NewError(ErrCanceled, "cleanup operation cancelled", ctx.Err())
		default:
		}

		for _, backups := range typeBackups {
			dailyBackups := m.getDailyBackups(backups)

			// Process daily backups
			for i, backup := range dailyBackups {
				// Check context before processing each backup
				select {
				case <-ctx.Done():
					return NewError(ErrCanceled, "cleanup operation cancelled", ctx.Err())
				default:
				}

				if m.shouldKeepBackup(i, backup, maxAge, minBackups, maxBackups) {
					continue
				}

				if err := m.deleteBackupWithTimeout(ctx, backup, targetName); err != nil {
					errs = append(errs, err)
					m.logger.Printf("Warning: Failed to delete old backup %s: %v", backup.ID, targetName)
				}
			}
		}
	}

	if len(errs) > 0 {
		return combineErrors(errs)
	}

	return nil
}

// ListBackups returns a list of all stored backups across all targets
func (m *Manager) ListBackups(ctx context.Context) ([]BackupInfo, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()

	if len(m.targets) == 0 {
		return nil, NewError(ErrValidation, "no backup targets registered", nil)
	}

	var allBackups []BackupInfo
	var errs []error
	for _, target := range m.targets {
		backups, err := target.List(ctx)
		if err != nil {
			m.logger.Printf("Failed to list backups from target %s: %v", target.Name(), err)
			errs = append(errs, NewError(ErrIO, fmt.Sprintf("failed to list backups from target %s", target.Name()), err))
			continue
		}
		allBackups = append(allBackups, backups...)
	}

	if len(errs) > 0 {
		// If we have some backups but also errors, log the errors but return the backups we have
		if len(allBackups) > 0 {
			m.logger.Printf("Warning: Some targets failed to list backups, returning partial results")
			return allBackups, nil
		}
		// If we have no backups and all targets failed, return an error
		var errMsgs []string
		for _, err := range errs {
			errMsgs = append(errMsgs, err.Error())
		}
		return nil, NewError(ErrIO, fmt.Sprintf("failed to list backups from all targets: %s", strings.Join(errMsgs, "; ")), nil)
	}

	return allBackups, nil
}

// DeleteBackup deletes a backup from all targets
func (m *Manager) DeleteBackup(ctx context.Context, id string) error {
	m.mu.RLock()
	defer m.mu.RUnlock()

	var lastErr error
	for _, target := range m.targets {
		if err := target.Delete(ctx, id); err != nil {
			lastErr = NewError(ErrIO, fmt.Sprintf("failed to delete backup %s from target %s", id, target.Name()), err)
			m.logger.Printf("Error: %v", lastErr)
		}
	}

	return lastErr
}

// defaultTimeouts defines default operation timeouts
var defaultTimeouts = struct {
	Backup  time.Duration
	Store   time.Duration
	Cleanup time.Duration
	Delete  time.Duration
}{
	Backup:  2 * time.Hour,
	Store:   15 * time.Minute,
	Cleanup: 10 * time.Minute,
	Delete:  2 * time.Minute,
}

// getBackupTimeout returns the configured backup timeout or the default
func (m *Manager) getBackupTimeout() time.Duration {
	if m.config.OperationTimeouts.Backup > 0 {
		return m.config.OperationTimeouts.Backup
	}
	return defaultTimeouts.Backup
}

// getStoreTimeout returns the configured store timeout or the default
func (m *Manager) getStoreTimeout() time.Duration {
	if m.config.OperationTimeouts.Store > 0 {
		return m.config.OperationTimeouts.Store
	}
	return defaultTimeouts.Store
}

// getCleanupTimeout returns the configured cleanup timeout or the default
func (m *Manager) getCleanupTimeout() time.Duration {
	if m.config.OperationTimeouts.Cleanup > 0 {
		return m.config.OperationTimeouts.Cleanup
	}
	return defaultTimeouts.Cleanup
}

// getDeleteTimeout returns the configured delete timeout or the default
func (m *Manager) getDeleteTimeout() time.Duration {
	if m.config.OperationTimeouts.Delete > 0 {
		return m.config.OperationTimeouts.Delete
	}
	return defaultTimeouts.Delete
}

// cleanupTempDirectories handles cleanup of temporary directories
func (m *Manager) cleanupTempDirectories(dirs []string) {
	for _, dir := range dirs {
		if err := os.RemoveAll(dir); err != nil {
			m.logger.Printf("⚠️ Failed to remove temporary directory %s: %v", dir, err)
		}
	}
}
