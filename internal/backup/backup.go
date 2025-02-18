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
	"sort"
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
	Files    map[string][]byte // Map of filenames to their contents
	Metadata Metadata          // Metadata about the backup
}

// NewBackupArchive creates a new backup archive
func NewBackupArchive(metadata *Metadata) *BackupArchive {
	return &BackupArchive{
		Files:    make(map[string][]byte),
		Metadata: *metadata,
	}
}

// AddFile adds a file to the backup archive
func (ba *BackupArchive) AddFile(name string, data []byte) {
	ba.Files[name] = data
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
		return fmt.Errorf("invalid source configuration: %w", err)
	}

	m.sources[source.Name()] = source
	return nil
}

// RegisterTarget registers a backup target
func (m *Manager) RegisterTarget(target Target) error {
	m.mu.Lock()
	defer m.mu.Unlock()

	if err := target.Validate(); err != nil {
		return fmt.Errorf("invalid target configuration: %w", err)
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
		return 0, fmt.Errorf("invalid schedule format, expected '0 HOUR * * *': %w", err)
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
		m.logger.Println("Backup manager is disabled")
		return nil
	}

	// Validate that we have at least one source and target
	if len(m.sources) == 0 {
		return fmt.Errorf("no backup sources registered")
	}
	if len(m.targets) == 0 {
		return fmt.Errorf("no backup targets registered")
	}

	// Parse the schedule
	initialDelay, err := parseCronSchedule(m.config.Schedule)
	if err != nil {
		return fmt.Errorf("failed to parse schedule: %w", err)
	}

	// Start the backup scheduler
	go m.scheduleBackups(initialDelay)

	m.logger.Printf("Backup manager started with schedule: %s (next backup in %v)", m.config.Schedule, initialDelay)
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

	m.logger.Println("Starting backup process...")

	// Validate that we have at least one target
	if len(m.targets) == 0 {
		return fmt.Errorf("no backup targets registered, backup cannot proceed")
	}

	// Get current timestamp in UTC
	now := time.Now().UTC()
	isDaily := now.Hour() == 0 && now.Minute() < 15 // Consider it a daily backup if run between 00:00 and 00:15

	var tempDirs []string
	defer func() {
		m.logger.Printf("Cleaning up %d temporary directories...", len(tempDirs))
		for _, dir := range tempDirs {
			os.RemoveAll(dir)
		}
	}()

	var errs []error
	for sourceName, source := range m.sources {
		m.logger.Printf("Processing backup source: %s", sourceName)

		// Create backup file
		m.logger.Printf("Creating backup file for source: %s", sourceName)
		backupPath, err := source.Backup(ctx)
		if err != nil {
			m.logger.Printf("Failed to backup source %s: %v", sourceName, err)
			errs = append(errs, fmt.Errorf("failed to backup source %s: %w", sourceName, err))
			continue
		}
		m.logger.Printf("Successfully created backup file at: %s", backupPath)

		// Create a temporary directory for the archive
		m.logger.Printf("Creating temporary directory for archive...")
		tempDir, err := os.MkdirTemp("", "backup-*")
		if err != nil {
			m.logger.Printf("Failed to create temporary directory: %v", err)
			errs = append(errs, fmt.Errorf("failed to create temporary directory: %w", err))
			continue
		}
		tempDirs = append(tempDirs, tempDir)
		m.logger.Printf("Created temporary directory: %s", tempDir)

		// Read the backup file
		m.logger.Printf("Reading backup file: %s", backupPath)
		backupData, err := os.ReadFile(backupPath)
		if err != nil {
			m.logger.Printf("Failed to read backup file %s: %v", backupPath, err)
			errs = append(errs, fmt.Errorf("failed to read backup file: %w", err))
			continue
		}
		m.logger.Printf("Successfully read backup file, size: %d bytes", len(backupData))

		// Create metadata
		metadata := Metadata{
			ID:        fmt.Sprintf("birdnet-go-backup-%s", now.Format("20060102-150405")),
			Timestamp: now,
			Type:      sourceName,
			Source:    backupPath,
			IsDaily:   isDaily,
		}
		m.logger.Printf("Created backup metadata with ID: %s", metadata.ID)

		// Create backup archive
		archive := NewBackupArchive(&metadata)
		m.logger.Printf("Created backup archive for ID: %s", metadata.ID)

		// Add the backup file to the archive
		dbFilename := fmt.Sprintf("%s.db", sourceName)
		archive.AddFile(dbFilename, backupData)
		m.logger.Printf("Added database file to archive: %s", dbFilename)

		// Add sanitized configuration
		if settings := conf.Setting(); settings != nil {
			m.logger.Printf("Adding sanitized configuration to archive...")
			if configData, err := yaml.Marshal(sanitizeConfig(settings)); err == nil {
				archive.AddFile("config.yaml", configData)
				hash := sha256.Sum256(configData)
				archive.Metadata.ConfigHash = hex.EncodeToString(hash[:])
				m.logger.Printf("Added configuration file to archive with hash: %s", archive.Metadata.ConfigHash)
			} else {
				m.logger.Printf("Warning: Failed to include configuration in backup: %v", err)
			}
		}

		// Create the archive file
		archivePath := filepath.Join(tempDir, fmt.Sprintf("%s.zip", metadata.ID))
		m.logger.Printf("Creating archive file at: %s", archivePath)
		if err := m.createArchive(archivePath, archive); err != nil {
			m.logger.Printf("Failed to create archive: %v", err)
			errs = append(errs, fmt.Errorf("failed to create archive: %w", err))
			continue
		}
		m.logger.Printf("Successfully created archive file")

		// Store backup in all enabled targets
		m.logger.Printf("Storing backup in %d target(s)...", len(m.targets))
		for _, target := range m.targets {
			m.logger.Printf("Storing backup in target: %s", target.Name())
			if err := target.Store(ctx, archivePath, &metadata); err != nil {
				m.logger.Printf("Failed to store backup in target %s: %v", target.Name(), err)
				errs = append(errs, fmt.Errorf("failed to store backup in target %s: %w", target.Name(), err))
				continue
			}
			m.logger.Printf("Successfully stored backup in target: %s", target.Name())
		}

		// Clean up old backups if this is a daily backup
		if isDaily {
			m.logger.Printf("Running cleanup of old backups...")
			if err := m.cleanupOldBackups(ctx); err != nil {
				m.logger.Printf("Warning: Failed to clean up old backups: %v", err)
			}
		}
	}

	if len(errs) > 0 {
		return fmt.Errorf("backup errors occurred: %v", errs)
	}

	m.logger.Println("Backup process completed successfully")
	return nil
}

// createArchive creates a backup archive, optionally encrypting it
func (m *Manager) createArchive(archivePath string, archive *BackupArchive) error {
	// Create a temporary file for the unencrypted archive
	tempArchive, err := os.CreateTemp(filepath.Dir(archivePath), "temp-archive-*.zip")
	if err != nil {
		return fmt.Errorf("failed to create temporary archive: %w", err)
	}
	tempArchivePath := tempArchive.Name()
	defer os.Remove(tempArchivePath)

	// Create a new zip writer for the temporary file
	zw := zip.NewWriter(tempArchive)

	// Add metadata
	metadataBytes, err := json.Marshal(archive.Metadata)
	if err != nil {
		return fmt.Errorf("failed to marshal metadata: %w", err)
	}
	if err := addFileToZip(zw, "metadata.json", metadataBytes); err != nil {
		return err
	}

	// Add all files to the archive
	for name, data := range archive.Files {
		if err := addFileToZip(zw, name, data); err != nil {
			return err
		}
	}

	// Close the zip writer
	if err := zw.Close(); err != nil {
		return fmt.Errorf("failed to close zip writer: %w", err)
	}

	// Close the temporary file
	if err := tempArchive.Close(); err != nil {
		return fmt.Errorf("failed to close temporary archive: %w", err)
	}

	// Read the temporary archive
	archiveData, err := os.ReadFile(tempArchivePath)
	if err != nil {
		return fmt.Errorf("failed to read temporary archive: %w", err)
	}

	var finalData []byte
	if m.config.Encryption {
		// Get or generate the encryption key
		key, err := m.getEncryptionKey()
		if err != nil {
			return fmt.Errorf("failed to get encryption key: %w", err)
		}

		// Encrypt the archive
		finalData, err = encryptData(archiveData, key)
		if err != nil {
			return fmt.Errorf("failed to encrypt archive: %w", err)
		}
	} else {
		finalData = archiveData
	}

	// Write the final data to the archive file
	if err := os.WriteFile(archivePath, finalData, 0o644); err != nil {
		return fmt.Errorf("failed to write archive: %w", err)
	}

	return nil
}

// getEncryptionKey returns the encryption key, generating it if necessary
func (m *Manager) getEncryptionKey() ([]byte, error) {
	if !m.config.Encryption {
		return nil, fmt.Errorf("encryption is not enabled")
	}

	if m.config.EncryptionKey == "" {
		// Generate a new key if none exists
		key := make([]byte, 32) // 256 bits
		if _, err := rand.Read(key); err != nil {
			return nil, fmt.Errorf("failed to generate encryption key: %w", err)
		}
		m.config.EncryptionKey = hex.EncodeToString(key)

		// Save the key to the configuration
		if err := conf.SaveSettings(); err != nil {
			return nil, fmt.Errorf("failed to save encryption key: %w", err)
		}
		return key, nil
	}

	// Decode existing key
	key, err := hex.DecodeString(m.config.EncryptionKey)
	if err != nil {
		return nil, fmt.Errorf("failed to decode encryption key: %w", err)
	}
	return key, nil
}

// encryptData encrypts data using AES-256-GCM
func encryptData(data, key []byte) ([]byte, error) {
	block, err := aes.NewCipher(key)
	if err != nil {
		return nil, fmt.Errorf("failed to create cipher: %w", err)
	}

	gcm, err := cipher.NewGCM(block)
	if err != nil {
		return nil, fmt.Errorf("failed to create GCM: %w", err)
	}

	// Generate a random nonce
	nonce := make([]byte, gcm.NonceSize())
	if _, err := rand.Read(nonce); err != nil {
		return nil, fmt.Errorf("failed to generate nonce: %w", err)
	}

	// Encrypt the data
	ciphertext := gcm.Seal(nonce, nonce, data, nil)
	return ciphertext, nil
}

// decryptData decrypts data using AES-256-GCM
func decryptData(encryptedData, key []byte) ([]byte, error) {
	block, err := aes.NewCipher(key)
	if err != nil {
		return nil, fmt.Errorf("failed to create cipher: %w", err)
	}

	gcm, err := cipher.NewGCM(block)
	if err != nil {
		return nil, fmt.Errorf("failed to create GCM: %w", err)
	}

	if len(encryptedData) < gcm.NonceSize() {
		return nil, fmt.Errorf("encrypted data too short")
	}

	nonce := encryptedData[:gcm.NonceSize()]
	ciphertext := encryptedData[gcm.NonceSize():]

	plaintext, err := gcm.Open(nil, nonce, ciphertext, nil)
	if err != nil {
		return nil, fmt.Errorf("failed to decrypt data: %w", err)
	}

	return plaintext, nil
}

// addFileToZip adds a file to a zip archive
func addFileToZip(zw *zip.Writer, name string, data []byte) error {
	w, err := zw.Create(name)
	if err != nil {
		return fmt.Errorf("failed to create file in zip: %w", err)
	}
	if _, err := w.Write(data); err != nil {
		return fmt.Errorf("failed to write file to zip: %w", err)
	}
	return nil
}

// cleanupOldBackups removes old backups according to retention policy
func (m *Manager) cleanupOldBackups(ctx context.Context) error {
	// Get all backups
	backups, err := m.ListBackups(ctx)
	if err != nil {
		return fmt.Errorf("failed to list backups: %w", err)
	}

	// Group backups by target and type
	backupsByTargetAndType := make(map[string]map[string][]BackupInfo)
	for i := range backups {
		backup := &backups[i]
		if _, ok := backupsByTargetAndType[backup.Target]; !ok {
			backupsByTargetAndType[backup.Target] = make(map[string][]BackupInfo)
		}
		backupsByTargetAndType[backup.Target][backup.Type] = append(backupsByTargetAndType[backup.Target][backup.Type], backups[i])
	}

	// Process each target and type
	for targetName, typeBackups := range backupsByTargetAndType {
		for _, backups := range typeBackups {
			// Sort backups by timestamp (newest first)
			sort.Slice(backups, func(i, j int) bool {
				return backups[i].Timestamp.After(backups[j].Timestamp)
			})

			// Keep track of daily backups
			var dailyBackups []*BackupInfo
			for i := range backups {
				if backups[i].IsDaily {
					dailyBackups = append(dailyBackups, &backups[i])
				}
			}

			// Apply retention policy
			maxAge := m.parseRetentionAge(m.config.Retention.MaxAge)
			minBackups := m.config.Retention.MinBackups
			maxBackups := m.config.Retention.MaxBackups

			// Process daily backups
			for i, backup := range dailyBackups {
				// Always keep minimum number of backups
				if i < minBackups {
					continue
				}

				// Keep backups within max age
				if maxAge > 0 && time.Since(backup.Timestamp) < maxAge {
					continue
				}

				// Remove if we have more than max backups
				if maxBackups > 0 && i >= maxBackups {
					if err := m.DeleteBackup(ctx, backup.ID); err != nil {
						m.logger.Printf("Warning: Failed to delete old backup %s: %v", backup.ID, err)
					} else if m.config.Debug {
						m.logger.Printf("Deleted old backup %s from target %s", backup.ID, targetName)
					}
				}
			}
		}
	}

	return nil
}

// parseRetentionAge parses a retention age string (e.g., "30d", "6m", "1y") into a duration
func (m *Manager) parseRetentionAge(age string) time.Duration {
	if age == "" {
		return 0
	}

	// Parse the number and unit
	var num int
	var unit string
	if _, err := fmt.Sscanf(age, "%d%s", &num, &unit); err != nil {
		m.logger.Printf("Warning: Invalid retention age format: %s", age)
		return 0
	}

	// Convert to duration
	switch unit {
	case "d":
		return time.Duration(num) * 24 * time.Hour
	case "m":
		return time.Duration(num) * 30 * 24 * time.Hour // approximate
	case "y":
		return time.Duration(num) * 365 * 24 * time.Hour // approximate
	default:
		m.logger.Printf("Warning: Invalid retention age unit: %s", unit)
		return 0
	}
}

// ListBackups returns a list of all stored backups across all targets
func (m *Manager) ListBackups(ctx context.Context) ([]BackupInfo, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()

	var allBackups []BackupInfo
	for _, target := range m.targets {
		backups, err := target.List(ctx)
		if err != nil {
			m.logger.Printf("Failed to list backups from target %s: %v", target.Name(), err)
			continue
		}
		allBackups = append(allBackups, backups...)
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
			lastErr = fmt.Errorf("failed to delete backup %s from target %s: %w", id, target.Name(), err)
			m.logger.Printf("Error: %v", lastErr)
		}
	}

	return lastErr
}
