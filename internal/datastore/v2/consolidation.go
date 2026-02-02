// Package v2 provides the v2 normalized database implementation.
package v2

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"time"

	"github.com/tphakala/birdnet-go/internal/datastore"
	"github.com/tphakala/birdnet-go/internal/errors"
	"github.com/tphakala/birdnet-go/internal/logger"
)

// StateFileName is the name of the consolidation state file.
const StateFileName = ".v2_consolidation_state"

// ConsolidationState represents the state of an in-progress consolidation.
type ConsolidationState struct {
	LegacyPath     string    `json:"legacy_path"`
	V2Path         string    `json:"v2_path"`
	BackupPath     string    `json:"backup_path"`
	ConfiguredPath string    `json:"configured_path"`
	StartedAt      time.Time `json:"started_at"`
}

// WriteConsolidationState writes the consolidation state file atomically.
// It writes to a temp file first, then renames to ensure atomic write.
func WriteConsolidationState(dataDir string, state *ConsolidationState) error {
	stateFilePath := filepath.Join(dataDir, StateFileName)
	tempFilePath := stateFilePath + ".tmp"

	data, err := json.MarshalIndent(state, "", "  ")
	if err != nil {
		return fmt.Errorf("failed to marshal consolidation state: %w", err)
	}

	// Write to temp file first
	if err := os.WriteFile(tempFilePath, data, 0o600); err != nil {
		return fmt.Errorf("failed to write temp state file: %w", err)
	}

	// Atomic rename
	if err := os.Rename(tempFilePath, stateFilePath); err != nil {
		// Clean up temp file on failure
		_ = os.Remove(tempFilePath)
		return fmt.Errorf("failed to rename state file: %w", err)
	}

	return nil
}

// ReadConsolidationState reads the consolidation state file if it exists.
// Returns nil, nil if the file does not exist (intentional - not an error condition).
func ReadConsolidationState(dataDir string) (*ConsolidationState, error) {
	stateFilePath := filepath.Join(dataDir, StateFileName)

	data, err := os.ReadFile(stateFilePath) //nolint:gosec // Path is constructed from trusted dataDir
	if os.IsNotExist(err) {
		return nil, nil //nolint:nilnil // Intentional: nil state with no error means "not found"
	}
	if err != nil {
		return nil, fmt.Errorf("failed to read consolidation state file: %w", err)
	}

	var state ConsolidationState
	if err := json.Unmarshal(data, &state); err != nil {
		return nil, fmt.Errorf("failed to unmarshal consolidation state: %w", err)
	}

	return &state, nil
}

// DeleteConsolidationState removes the consolidation state file.
// Returns nil if the file doesn't exist.
func DeleteConsolidationState(dataDir string) error {
	stateFilePath := filepath.Join(dataDir, StateFileName)
	if err := os.Remove(stateFilePath); err != nil && !os.IsNotExist(err) {
		return fmt.Errorf("failed to delete consolidation state file: %w", err)
	}
	return nil
}

// GenerateBackupPath generates a timestamped backup path for the legacy database.
// Example: /data/birdnet.db → /data/birdnet.db.20260202-120000.old
func GenerateBackupPath(legacyPath string) string {
	timestamp := time.Now().Format("20060102-150405")
	return fmt.Sprintf("%s.%s.old", legacyPath, timestamp)
}

// cleanupWALFiles removes WAL and SHM files for a given database path.
// This is a defensive cleanup - files may not exist after proper checkpoint.
func cleanupWALFiles(dbPath string) {
	for _, suffix := range []string{"-wal", "-shm"} {
		_ = os.Remove(dbPath + suffix)
	}
}

// Consolidate performs the database consolidation after migration completes.
// It renames the legacy database to .old and moves v2 to the configured path.
//
// The consolidation sequence is:
//  1. Write consolidation state file (atomic marker)
//  2. Checkpoint WAL on v2 (TRUNCATE)
//  3. Close v2 connection
//  4. Checkpoint WAL on legacy (TRUNCATE)
//  5. Close legacy connection
//  6. Delete any leftover WAL/SHM files (defensive cleanup)
//  7. Rename: legacy → legacy.TIMESTAMP.old
//  8. Rename: v2 → configured path
//  9. Delete consolidation state file
//  10. Reopen v2 at configured path
//  11. Mark migration state as COMPLETED in database
//
// Parameters:
//   - v2Manager: the v2 database manager (will be closed and reopened)
//   - legacyStore: the legacy datastore interface (will be closed)
//   - configuredPath: the target path for the consolidated database
//   - dataDir: directory for state file
//   - log: logger for progress
//
// Returns the reopened v2 manager at the new path, or error.
func Consolidate(
	v2Manager *SQLiteManager,
	legacyStore datastore.Interface,
	configuredPath string,
	dataDir string,
	log logger.Logger,
) (*SQLiteManager, error) {
	if log == nil {
		return nil, fmt.Errorf("logger is required")
	}

	v2Path := v2Manager.Path()
	backupPath := GenerateBackupPath(configuredPath)

	log.Info("starting database consolidation",
		logger.String("v2_path", v2Path),
		logger.String("configured_path", configuredPath),
		logger.String("backup_path", backupPath))

	// Step 1: Write consolidation state file
	state := &ConsolidationState{
		LegacyPath:     configuredPath,
		V2Path:         v2Path,
		BackupPath:     backupPath,
		ConfiguredPath: configuredPath,
		StartedAt:      time.Now(),
	}
	if err := WriteConsolidationState(dataDir, state); err != nil {
		return nil, fmt.Errorf("failed to write consolidation state: %w", err)
	}

	// Step 2: Checkpoint WAL on v2
	log.Debug("checkpointing v2 database WAL")
	if err := v2Manager.CheckpointWAL(); err != nil {
		_ = DeleteConsolidationState(dataDir)
		return nil, fmt.Errorf("failed to checkpoint v2 WAL: %w", err)
	}

	// Step 3: Close v2 connection
	log.Debug("closing v2 database connection")
	if err := v2Manager.Close(); err != nil {
		_ = DeleteConsolidationState(dataDir)
		return nil, fmt.Errorf("failed to close v2 database: %w", err)
	}

	// Step 4: Checkpoint WAL on legacy (via type assertion)
	log.Debug("checkpointing legacy database WAL")
	if sqliteStore, ok := legacyStore.(*datastore.SQLiteStore); ok {
		if err := sqliteStore.CheckpointWAL(); err != nil {
			_ = DeleteConsolidationState(dataDir)
			return nil, fmt.Errorf("failed to checkpoint legacy WAL: %w", err)
		}
	}

	// Step 5: Close legacy connection
	log.Debug("closing legacy database connection")
	if err := legacyStore.Close(); err != nil {
		_ = DeleteConsolidationState(dataDir)
		return nil, fmt.Errorf("failed to close legacy database: %w", err)
	}

	// Step 6: Delete any leftover WAL/SHM files (defensive cleanup)
	log.Debug("cleaning up WAL/SHM files")
	cleanupWALFiles(configuredPath) // legacy
	cleanupWALFiles(v2Path)         // v2

	// Step 7: Rename legacy → backup
	log.Debug("renaming legacy database to backup",
		logger.String("from", configuredPath),
		logger.String("to", backupPath))
	if err := os.Rename(configuredPath, backupPath); err != nil {
		_ = DeleteConsolidationState(dataDir)
		return nil, fmt.Errorf("failed to rename legacy database to backup: %w", err)
	}

	// Step 8: Rename v2 → configured path
	log.Debug("renaming v2 database to configured path",
		logger.String("from", v2Path),
		logger.String("to", configuredPath))
	if err := os.Rename(v2Path, configuredPath); err != nil {
		// Rollback: restore legacy from backup
		log.Warn("v2 rename failed, rolling back legacy rename",
			logger.Error(err))
		if rollbackErr := os.Rename(backupPath, configuredPath); rollbackErr != nil {
			log.Error("rollback failed - manual intervention required",
				logger.Error(rollbackErr),
				logger.String("backup_path", backupPath),
				logger.String("configured_path", configuredPath))
			return nil, errors.Join(
			fmt.Errorf("failed to rename v2 database: %w", err),
			fmt.Errorf("rollback also failed: %w", rollbackErr),
		)
		}
		_ = DeleteConsolidationState(dataDir)
		return nil, fmt.Errorf("failed to rename v2 database (rolled back): %w", err)
	}

	// Step 9: Delete consolidation state file
	log.Debug("deleting consolidation state file")
	if err := DeleteConsolidationState(dataDir); err != nil {
		// Log warning but continue - orphaned state file is harmless
		log.Warn("failed to delete consolidation state file",
			logger.Error(err))
	}

	// Step 10: Reopen v2 at configured path
	log.Debug("reopening v2 database at configured path",
		logger.String("path", configuredPath))
	newManager, err := NewSQLiteManager(Config{DirectPath: configuredPath})
	if err != nil {
		return nil, fmt.Errorf("failed to reopen v2 database at configured path: %w", err)
	}

	log.Info("database consolidation completed successfully",
		logger.String("database_path", configuredPath),
		logger.String("backup_path", backupPath))

	return newManager, nil
}

// ResumeConsolidation checks for interrupted consolidation and resumes if needed.
// Called at startup before normal database initialization.
//
// Returns:
//   - resumed: true if consolidation was resumed and completed
//   - newPath: the path where the v2 database now lives (if resumed)
//   - error: any error that occurred during resume
func ResumeConsolidation(dataDir string, log logger.Logger) (resumed bool, newPath string, err error) {
	state, err := ReadConsolidationState(dataDir)
	if err != nil {
		return false, "", fmt.Errorf("failed to read consolidation state: %w", err)
	}

	if state == nil {
		// No interrupted consolidation
		return false, "", nil
	}

	log.Info("detected interrupted consolidation, attempting to resume",
		logger.String("v2_path", state.V2Path),
		logger.String("configured_path", state.ConfiguredPath),
		logger.String("backup_path", state.BackupPath))

	// Determine state based on file existence
	configuredExists, err := fileExists(state.ConfiguredPath)
	if err != nil {
		return false, "", fmt.Errorf("failed to stat configured path: %w", err)
	}
	v2Exists, err := fileExists(state.V2Path)
	if err != nil {
		return false, "", fmt.Errorf("failed to stat v2 path: %w", err)
	}
	backupExists, err := fileExists(state.BackupPath)
	if err != nil {
		return false, "", fmt.Errorf("failed to stat backup path: %w", err)
	}

	// Check if configured path has v2 schema (consolidation completed but state file not deleted)
	if configuredExists && CheckSQLiteHasV2Schema(state.ConfiguredPath) {
		log.Info("consolidation already complete, cleaning up state file")
		_ = DeleteConsolidationState(dataDir)
		return true, state.ConfiguredPath, nil
	}

	// Case: Only backup and v2 exist → resume at step 8 (rename v2 → configured)
	if backupExists && v2Exists && !configuredExists {
		log.Info("resuming consolidation: renaming v2 to configured path")

		// Clean up any WAL/SHM files
		cleanupWALFiles(state.V2Path)
		cleanupWALFiles(state.ConfiguredPath)

		if err := os.Rename(state.V2Path, state.ConfiguredPath); err != nil {
			return false, "", fmt.Errorf("failed to resume: rename v2 to configured path: %w", err)
		}

		_ = DeleteConsolidationState(dataDir)
		log.Info("consolidation resumed successfully")
		return true, state.ConfiguredPath, nil
	}

	// Case: Both legacy and v2 exist → need to restart consolidation
	// This shouldn't happen often, but handle it by cleaning up state and letting normal flow handle it
	if configuredExists && v2Exists {
		log.Warn("both legacy and v2 databases exist - consolidation state inconsistent, cleaning up")
		_ = DeleteConsolidationState(dataDir)
		// Return false to let normal startup flow handle this
		return false, "", nil
	}

	// Unknown state - clean up and let normal flow handle it
	log.Warn("unknown consolidation state, cleaning up state file")
	_ = DeleteConsolidationState(dataDir)
	return false, "", nil
}

// fileExists checks if a file exists, distinguishing between not-found and I/O errors.
func fileExists(path string) (bool, error) {
	_, err := os.Stat(path)
	if err == nil {
		return true, nil
	}
	if os.IsNotExist(err) {
		return false, nil
	}
	return false, err
}
