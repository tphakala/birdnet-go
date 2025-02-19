// Package backup provides functionality for backing up application data
package backup

import (
	"archive/zip"
	"context"
	"crypto/aes"
	"crypto/cipher"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"log"
	"os"
	"path/filepath"
	"runtime"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/tphakala/birdnet-go/internal/conf"
	"gopkg.in/yaml.v3"
)

// Source represents a data source that needs to be backed up
type Source interface {
	// Name returns the name of the source
	Name() string
	// Backup performs the backup operation and returns the path to the backup file
	Backup(ctx context.Context) (string, error)
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

// BackupArchive represents a backup archive containing multiple files
type BackupArchive struct {
	Files    map[string]FileEntry // Map of filenames to their contents and metadata
	Metadata Metadata             // Metadata about the backup
}

// FileMetadata contains platform-specific file metadata
type FileMetadata struct {
	Mode   os.FileMode // File mode and permission bits
	UID    int         // User ID (Unix only)
	GID    int         // Group ID (Unix only)
	IsUnix bool        // Whether this metadata is from a Unix system
}

// FileEntry represents a file in the backup archive
type FileEntry struct {
	Data     []byte       // File contents
	ModTime  time.Time    // File modification time
	Metadata FileMetadata // Platform-specific metadata
}

// NewBackupArchive creates a new backup archive
func NewBackupArchive(metadata *Metadata) *BackupArchive {
	return &BackupArchive{
		Files:    make(map[string]FileEntry),
		Metadata: *metadata,
	}
}

// AddFile adds a file to the backup archive
func (ba *BackupArchive) AddFile(name string, data []byte, modTime time.Time, metadata FileMetadata) {
	ba.Files[name] = FileEntry{
		Data:     data,
		ModTime:  modTime,
		Metadata: metadata,
	}
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
	done    chan struct{}
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
		done:    make(chan struct{}),
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

// parseCronSchedule parses a cron-like schedule string (e.g., "0 0 * * *") and returns a duration
// This is a simple implementation that only supports daily schedules at a specific hour
func parseCronSchedule(schedule string) (time.Duration, error) {
	// Split the schedule into fields
	var hour int
	_, err := fmt.Sscanf(schedule, "%d %d * * *", nil, &hour)
	if err != nil {
		return 0, NewError(ErrValidation, "invalid schedule format, expected '0 HOUR * * *'", err)
	}

	// Get current time
	now := time.Now()
	next := time.Date(now.Year(), now.Month(), now.Day(), hour, 0, 0, 0, now.Location())
	if next.Before(now) {
		next = next.Add(24 * time.Hour)
	}

	return next.Sub(now), nil
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

	// Parse the schedule
	initialDelay, err := parseCronSchedule(m.config.Schedule)
	if err != nil {
		return NewError(ErrConfig, "failed to parse schedule", err)
	}

	// Start the backup scheduler
	go m.scheduleBackups(initialDelay)

	m.logger.Printf("✅ Backup manager started with schedule: %s (next backup in %v)", m.config.Schedule, initialDelay)
	return nil
}

// scheduleBackups runs the backup scheduler
func (m *Manager) scheduleBackups(initialDelay time.Duration) {
	// Wait for the initial delay
	timer := time.NewTimer(initialDelay)
	defer timer.Stop()

	for {
		select {
		case <-m.done:
			return
		case <-timer.C:
			// Run the backup
			if err := m.RunBackup(context.Background()); err != nil {
				m.logger.Printf("Scheduled backup failed: %v", err)
			}

			// Reset the timer for the next day
			timer.Reset(24 * time.Hour)
		}
	}
}

// Stop stops the backup manager
func (m *Manager) Stop() {
	close(m.done)
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
	backupPath, err := m.createSourceBackup(ctx, sourceName, source)
	if err != nil {
		return tempDirs, err
	}
	m.logger.Printf("✅ Successfully created backup file at: %s", backupPath)

	// Create and prepare archive
	tempDir, archive, err := m.prepareBackupArchive(ctx, backupPath, sourceName, now, isDaily)
	if err != nil {
		// Don't add tempDir to tempDirs if preparation failed
		if tempDir != "" {
			os.RemoveAll(tempDir)
		}
		return tempDirs, err
	}
	tempDirs = append(tempDirs, tempDir)

	// Create the archive file
	archivePath := filepath.Join(tempDir, fmt.Sprintf("%s.zip", archive.Metadata.ID))
	m.logger.Printf("🔄 Creating archive file at: %s", archivePath)
	if err := m.createArchive(ctx, archivePath, archive); err != nil {
		return tempDirs, NewError(ErrIO, "failed to create archive", err)
	}
	m.logger.Printf("✅ Successfully created archive file")

	// Store backup in all targets
	if err := m.storeBackupInTargets(ctx, archivePath, &archive.Metadata); err != nil {
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

// createSourceBackup creates a backup of a single source with timeout
func (m *Manager) createSourceBackup(ctx context.Context, sourceName string, source Source) (string, error) {
	backupCtx, backupCancel := context.WithTimeout(ctx, 30*time.Minute)
	defer backupCancel()

	backupPath, err := source.Backup(backupCtx)
	if err != nil {
		if backupCtx.Err() != nil {
			m.logger.Printf("Backup source %s timed out: %v", sourceName, err)
			return "", NewError(ErrTimeout, fmt.Sprintf("backup source %s timed out", sourceName), err)
		}
		m.logger.Printf("Failed to backup source %s: %v", sourceName, err)
		return "", NewError(ErrDatabase, fmt.Sprintf("failed to backup source %s", sourceName), err)
	}
	return backupPath, nil
}

// getFileMetadata gets platform-specific file metadata
func getFileMetadata(info os.FileInfo) FileMetadata {
	metadata := FileMetadata{
		Mode:   info.Mode(),
		IsUnix: runtime.GOOS != "windows",
	}

	// Get Unix-specific metadata on Unix systems
	if metadata.IsUnix {
		getUnixMetadata(&metadata, info)
	}

	return metadata
}

// prepareBackupArchive creates and prepares a backup archive
func (m *Manager) prepareBackupArchive(ctx context.Context, backupPath, sourceName string, now time.Time, isDaily bool) (string, *BackupArchive, error) {
	// Create temporary directory
	tempDir, err := os.MkdirTemp("", "backup-*")
	if err != nil {
		return "", nil, NewError(ErrIO, "failed to create temporary directory", err)
	}
	m.logger.Printf("Created temporary directory: %s", tempDir)

	select {
	case <-ctx.Done():
		return tempDir, nil, NewError(ErrCanceled, "backup archive preparation cancelled", ctx.Err())
	default:
	}

	// Get file info for modification time and metadata
	fileInfo, err := os.Stat(backupPath)
	if err != nil {
		return tempDir, nil, NewError(ErrIO, fmt.Sprintf("failed to get backup file info %s", backupPath), err)
	}

	// Read the backup file
	backupData, err := os.ReadFile(backupPath)
	if err != nil {
		return tempDir, nil, NewError(ErrIO, fmt.Sprintf("failed to read backup file %s", backupPath), err)
	}
	m.logger.Printf("Successfully read backup file, size: %d bytes", len(backupData))

	select {
	case <-ctx.Done():
		return tempDir, nil, NewError(ErrCanceled, "backup archive preparation cancelled", ctx.Err())
	default:
	}

	// Create metadata and archive
	metadata := Metadata{
		ID:        fmt.Sprintf("birdnet-go-backup-%s", now.Format("20060102-150405")),
		Timestamp: now,
		Type:      sourceName,
		Source:    backupPath,
		IsDaily:   isDaily,
	}
	m.logger.Printf("Created backup metadata with ID: %s", metadata.ID)

	archive := NewBackupArchive(&metadata)
	m.logger.Printf("Created backup archive for ID: %s", metadata.ID)

	// Add the backup file to the archive with its original modification time and metadata
	dbFilename := fmt.Sprintf("%s.db", sourceName)
	archive.AddFile(dbFilename, backupData, fileInfo.ModTime(), getFileMetadata(fileInfo))
	m.logger.Printf("Added database file to archive: %s", dbFilename)

	// Add configuration if available
	if err := m.addConfigToArchive(archive); err != nil {
		return tempDir, archive, err
	}

	return tempDir, archive, nil
}

// addConfigToArchive adds sanitized configuration to the archive
func (m *Manager) addConfigToArchive(archive *BackupArchive) error {
	if settings := conf.Setting(); settings != nil {
		m.logger.Printf("🔄 Adding sanitized configuration to archive...")
		configData, err := yaml.Marshal(sanitizeConfig(settings))
		if err != nil {
			m.logger.Printf("⚠️ Failed to include configuration in backup: %v", err)
			return NewError(ErrIO, "failed to marshal configuration", err)
		}

		// Create default metadata for config file
		metadata := FileMetadata{
			Mode:   0o644,
			IsUnix: runtime.GOOS != "windows",
		}

		archive.AddFile("config.yaml", configData, archive.Metadata.Timestamp, metadata)
		hash := sha256.Sum256(configData)
		archive.Metadata.ConfigHash = hex.EncodeToString(hash[:])
		m.logger.Printf("✅ Added configuration file to archive with hash: %s", archive.Metadata.ConfigHash)
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
func (m *Manager) createArchive(ctx context.Context, archivePath string, archive *BackupArchive) error {
	// Create a temporary file for the unencrypted archive
	tempArchive, err := os.CreateTemp(filepath.Dir(archivePath), "temp-archive-*.zip")
	if err != nil {
		return NewError(ErrIO, "failed to create temporary archive", err)
	}
	tempArchivePath := tempArchive.Name()
	defer os.Remove(tempArchivePath)

	// Create a new zip writer for the temporary file
	zw := zip.NewWriter(tempArchive)

	// Add metadata
	metadataBytes, err := json.Marshal(archive.Metadata)
	if err != nil {
		return NewError(ErrIO, "failed to marshal metadata", err)
	}

	// Check context before proceeding
	select {
	case <-ctx.Done():
		return NewError(ErrCanceled, "archive creation cancelled", ctx.Err())
	default:
	}

	metadataEntry := &FileEntry{
		Data:    metadataBytes,
		ModTime: archive.Metadata.Timestamp,
	}
	if err := addFileToZip(zw, "metadata.json", metadataEntry); err != nil {
		return err
	}

	// Add all files to the archive
	for name, entry := range archive.Files {
		// Check context before each file
		select {
		case <-ctx.Done():
			return NewError(ErrCanceled, "archive creation cancelled", ctx.Err())
		default:
		}

		if err := addFileToZip(zw, name, &entry); err != nil {
			return err
		}
	}

	// Close the zip writer
	if err := zw.Close(); err != nil {
		return NewError(ErrIO, "failed to close zip writer", err)
	}

	// Close the temporary file
	if err := tempArchive.Close(); err != nil {
		return NewError(ErrIO, "failed to close temporary archive", err)
	}

	// Check context before reading archive
	select {
	case <-ctx.Done():
		return NewError(ErrCanceled, "archive creation cancelled", ctx.Err())
	default:
	}

	// Read the temporary archive
	archiveData, err := os.ReadFile(tempArchivePath)
	if err != nil {
		return NewError(ErrIO, "failed to read temporary archive", err)
	}

	var finalData []byte
	if m.config.Encryption {
		// Get or generate the encryption key
		key, err := m.getEncryptionKey()
		if err != nil {
			return err
		}

		// Check context before encryption
		select {
		case <-ctx.Done():
			return NewError(ErrCanceled, "archive encryption cancelled", ctx.Err())
		default:
		}

		// Encrypt the archive
		finalData, err = encryptData(archiveData, key)
		if err != nil {
			return err
		}
	} else {
		finalData = archiveData
	}

	// Check context before writing final archive
	select {
	case <-ctx.Done():
		return NewError(ErrCanceled, "archive creation cancelled", ctx.Err())
	default:
	}

	// Write the final data to the archive file
	if err := os.WriteFile(archivePath, finalData, 0o644); err != nil {
		return NewError(ErrIO, "failed to write archive", err)
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

// addFileToZip adds a file to a zip archive
func addFileToZip(zw *zip.Writer, name string, entry *FileEntry) error {
	// Create a new file header
	header := &zip.FileHeader{
		Name:     name,
		Method:   zip.Deflate,
		Modified: entry.ModTime,
	}

	// Set the file mode, preserving Unix permissions if available
	if entry.Metadata.IsUnix {
		header.SetMode(entry.Metadata.Mode)
	} else {
		header.SetMode(0o644)
	}

	w, err := zw.CreateHeader(header)
	if err != nil {
		return NewError(ErrIO, fmt.Sprintf("failed to create file %s in zip", name), err)
	}
	if _, err := w.Write(entry.Data); err != nil {
		return NewError(ErrIO, fmt.Sprintf("failed to write file %s to zip", name), err)
	}
	return nil
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
