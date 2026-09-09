// Package v2 provides the v2 normalized database implementation.
package v2

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"time"

	"github.com/getsentry/sentry-go"
	"github.com/tphakala/birdnet-go/internal/logger"
	"github.com/tphakala/birdnet-go/internal/telemetry"
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

	data, err := json.MarshalIndent(state, "", "  ")
	if err != nil {
		return fmt.Errorf("failed to marshal consolidation state: %w", err)
	}

	// Write to a uniquely-named temp file, fsync it, then atomically rename into place. A
	// unique name avoids colliding with a concurrent writer's temp file, and the fsync makes
	// the contents durable before the rename so a crash mid-write cannot leave a truncated or
	// missing breadcrumb behind an intact filename.
	// Remove any temp files orphaned by a crash between CreateTemp and Rename in a prior run so
	// they cannot accumulate. Startup is single-threaded, so no concurrent writer races this.
	if stale, _ := filepath.Glob(filepath.Join(dataDir, StateFileName+".*.tmp")); len(stale) > 0 {
		for _, f := range stale {
			_ = os.Remove(f)
		}
	}

	tempFile, err := os.CreateTemp(dataDir, StateFileName+".*.tmp")
	if err != nil {
		return fmt.Errorf("failed to create temp state file: %w", err)
	}
	tempFilePath := tempFile.Name()
	// Best-effort cleanup if we return before the rename removes the temp file.
	defer func() { _ = os.Remove(tempFilePath) }()

	if _, err := tempFile.Write(data); err != nil {
		_ = tempFile.Close()
		return fmt.Errorf("failed to write temp state file: %w", err)
	}
	if err := tempFile.Sync(); err != nil {
		_ = tempFile.Close()
		return fmt.Errorf("failed to sync temp state file: %w", err)
	}
	if err := tempFile.Close(); err != nil {
		return fmt.Errorf("failed to close temp state file: %w", err)
	}

	if err := os.Rename(tempFilePath, stateFilePath); err != nil {
		return fmt.Errorf("failed to rename state file: %w", err)
	}

	// Best-effort fsync of the parent directory so the rename (a directory-entry change) is
	// itself durable across a power failure, not just the file contents. The rename already
	// succeeded, so a failure here only weakens power-loss durability of the breadcrumb, which
	// leaves the DB in its safe pre-consolidation state.
	if dir, err := os.Open(dataDir); err == nil {
		_ = dir.Sync()
		_ = dir.Close()
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

// reportConsolidationError reports a database consolidation failure to Sentry telemetry.
// File paths in the error message are anonymized via scrubErrorWithPaths.
func reportConsolidationError(operation string, err error, paths ...string) {
	sentry.WithScope(func(scope *sentry.Scope) {
		scope.SetTag("component", "datastore-consolidation")
		scope.SetTag("operation", operation)
		scope.SetFingerprint([]string{"datastore-consolidation", operation})

		scrubbedErr := scrubErrorWithPaths(err.Error(), paths...)
		scope.SetContext("consolidation_error", map[string]any{
			"operation": operation,
			"error":     scrubbedErr,
		})

		level := sentry.LevelError
		if operation == "rollbackFailed" {
			level = sentry.LevelFatal
		}

		telemetry.CaptureMessage(
			fmt.Sprintf("Database consolidation failed: %s", operation),
			level,
			"datastore-consolidation",
		)
	})
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

		// Fold any pending v2 WAL content into the main file before renaming, then move
		// the -wal/-shm sidecars alongside the database instead of deleting them. Blindly
		// removing the WAL here would discard transactions committed since the last
		// checkpoint if the original consolidation was interrupted by an unclean shutdown
		// (issue #3991), the same hazard fixed in CheckAndConsolidateAtStartup.
		if err := checkpointSQLiteWAL(state.V2Path, log); err != nil {
			log.Warn("failed to checkpoint v2 WAL before resuming consolidation", logger.Error(err))
		}

		if err := moveSQLiteDBFiles(state.V2Path, state.ConfiguredPath, log); err != nil {
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
