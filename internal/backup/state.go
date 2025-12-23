package backup

import (
	"encoding/json"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"sync"
	"time"

	"github.com/tphakala/birdnet-go/internal/conf"
)

// BackupState represents the persistent state of the backup system
type BackupState struct {
	LastUpdate time.Time                `json:"last_update"`
	Schedules  map[string]ScheduleState `json:"schedules"` // Key is "daily" or "weekly-{weekday}"
	Targets    map[string]TargetState   `json:"targets"`   // Key is target name
	MissedRuns []MissedBackup           `json:"missed_runs"`
	Stats      map[string]BackupStats   `json:"stats"` // Key is target name
}

// ScheduleState represents the state of a backup schedule
type ScheduleState struct {
	LastSuccessful time.Time `json:"last_successful"`
	LastAttempted  time.Time `json:"last_attempted"`
	NextScheduled  time.Time `json:"next_scheduled"`
	FailureCount   int       `json:"failure_count"`
}

// TargetState represents the state of a backup target
type TargetState struct {
	LastBackupID     string    `json:"last_backup_id"`
	LastBackupTime   time.Time `json:"last_backup_time"`
	LastBackupStatus string    `json:"last_backup_status"`
	LastBackupSize   int64     `json:"last_backup_size"`
	TotalBackups     int       `json:"total_backups"`
	TotalSize        int64     `json:"total_size"`
	LastValidation   time.Time `json:"last_validation"`
	ValidationStatus string    `json:"validation_status"`
}

// MissedBackup represents a missed backup event
type MissedBackup struct {
	ScheduledTime time.Time `json:"scheduled_time"`
	Reason        string    `json:"reason"`
	IsWeekly      bool      `json:"is_weekly"`
	Weekday       string    `json:"weekday,omitempty"`
}

// StateManager handles persistence of backup states
type StateManager struct {
	state     *BackupState
	statePath string
	mu        sync.RWMutex
	logger    *slog.Logger
}

// NewStateManager creates a new state manager
func NewStateManager(logger *slog.Logger) (*StateManager, error) {
	// Get config directory
	configPaths, err := conf.GetDefaultConfigPaths()
	if err != nil {
		return nil, fmt.Errorf("failed to get config paths: %w", err)
	}
	if len(configPaths) == 0 {
		return nil, fmt.Errorf("no config paths available")
	}

	// Create state file path
	statePath := filepath.Join(configPaths[0], "backup-state.json")

	// Initialize logger if nil (fallback)
	if logger == nil {
		logger = slog.Default()
	}

	sm := &StateManager{
		statePath: statePath,
		state: &BackupState{
			Schedules:  make(map[string]ScheduleState),
			Targets:    make(map[string]TargetState),
			Stats:      make(map[string]BackupStats),
			MissedRuns: make([]MissedBackup, 0),
		},
		logger: logger.With("service", "backup_statemanager"),
	}

	// Load existing state if available
	if err := sm.loadState(); err != nil {
		// If file doesn't exist, that's fine - we'll create it on first save
		if !os.IsNotExist(err) {
			sm.logger.Error("Failed to load existing backup state file", "path", statePath, "error", err)
		} else {
			sm.logger.Info("No existing backup state file found, creating new one", "path", statePath)
		}
	} else {
		sm.logger.Info("Successfully loaded existing backup state", "path", statePath)
	}

	return sm, nil
}

// loadState loads the backup state from disk
func (sm *StateManager) loadState() error {
	sm.mu.Lock()
	defer sm.mu.Unlock()

	data, err := os.ReadFile(sm.statePath)
	if err != nil {
		return err
	}

	return json.Unmarshal(data, sm.state)
}

// saveState saves the current backup state to disk
func (sm *StateManager) saveState() error {
	start := time.Now()

	// Copy state under lock
	sm.mu.Lock()
	stateSnapshot := *sm.state
	sm.mu.Unlock()

	// Update last update time (on the snapshot)
	stateSnapshot.LastUpdate = time.Now()

	// Create state directory if it doesn't exist (internal config path)
	dirPath := filepath.Dir(sm.statePath)
	if err := os.MkdirAll(dirPath, DefaultDirectoryPermissions()); err != nil {
		sm.logger.Error("Failed to create state directory", "path", dirPath, "error", err)
		return fmt.Errorf("failed to create state directory: %w", err)
	}

	// Marshal state to JSON with pretty printing
	data, err := json.MarshalIndent(stateSnapshot, "", "  ")
	if err != nil {
		sm.logger.Error("Failed to marshal backup state to JSON", "error", err)
		return fmt.Errorf("failed to marshal state: %w", err)
	}

	// Write to temporary file first
	tempFile := sm.statePath + ".tmp"
	if err := os.WriteFile(tempFile, data, PermSecureFile); err != nil {
		sm.logger.Error("Failed to write temporary state file", "path", tempFile, "error", err)
		return fmt.Errorf("failed to write temporary state file: %w", err)
	}

	// Rename temporary file to actual state file (atomic operation)
	if err := os.Rename(tempFile, sm.statePath); err != nil {
		if removeErr := os.Remove(tempFile); removeErr != nil {
			sm.logger.Warn("Failed to clean up temp file after rename failure", "temp_path", tempFile, "error", removeErr)
		}
		sm.logger.Error("Failed to save state file (rename failed)", "temp_path", tempFile, "final_path", sm.statePath, "error", err)
		return fmt.Errorf("failed to save state file: %w", err)
	}

	sm.logger.Debug("Backup state saved successfully", "path", sm.statePath, "duration_ms", time.Since(start).Milliseconds())
	return nil
}

// UpdateScheduleState updates the state for a specific schedule
func (sm *StateManager) UpdateScheduleState(schedule *BackupSchedule, successful bool) error {
	sm.mu.Lock()
	defer sm.mu.Unlock()

	// Generate schedule key
	key := "daily"
	if schedule.IsWeekly {
		key = fmt.Sprintf("weekly-%s", schedule.Weekday)
	}
	sm.logger.Debug("Updating schedule state", "schedule_key", key, "successful", successful)

	state := sm.state.Schedules[key]
	state.LastAttempted = time.Now()
	if successful {
		state.LastSuccessful = time.Now()
		state.FailureCount = 0
	} else {
		state.FailureCount++
	}
	state.NextScheduled = schedule.NextRun

	sm.state.Schedules[key] = state

	// Save state changes
	if err := sm.saveState(); err != nil {
		sm.logger.Error("Failed to save state after updating schedule", "schedule_key", key, "error", err)
		return err
	}
	return nil
}

// AddMissedBackup records a missed backup
func (sm *StateManager) AddMissedBackup(schedule *BackupSchedule, reason string) error {
	sm.mu.Lock()
	defer sm.mu.Unlock()

	missed := MissedBackup{
		ScheduledTime: schedule.NextRun,
		Reason:        reason,
		IsWeekly:      schedule.IsWeekly,
	}
	if schedule.IsWeekly {
		missed.Weekday = schedule.Weekday.String()
	}

	sm.logger.Warn("Recording missed backup", "scheduled_time", missed.ScheduledTime, "reason", reason, "is_weekly", missed.IsWeekly, "weekday", missed.Weekday)
	sm.state.MissedRuns = append(sm.state.MissedRuns, missed)

	if err := sm.saveState(); err != nil {
		sm.logger.Error("Failed to save state after adding missed backup", "error", err)
		return err
	}
	return nil
}

// UpdateTargetState updates the state for a specific backup target
func (sm *StateManager) UpdateTargetState(targetName string, metadata *Metadata, status string) error {
	sm.mu.Lock()
	defer sm.mu.Unlock()
	sm.logger.Debug("Updating target state", "target_name", targetName, "backup_id", metadata.ID, "status", status)

	state := sm.state.Targets[targetName]
	state.LastBackupID = metadata.ID
	state.LastBackupTime = metadata.Timestamp
	state.LastBackupStatus = status
	state.LastBackupSize = metadata.Size
	if status == "success" {
		state.TotalBackups++
		state.TotalSize += metadata.Size
	}

	sm.state.Targets[targetName] = state

	if err := sm.saveState(); err != nil {
		sm.logger.Error("Failed to save state after updating target state", "target_name", targetName, "error", err)
		return err
	}
	return nil
}

// UpdateStats updates the backup statistics
func (sm *StateManager) UpdateStats(stats map[string]BackupStats) error {
	sm.mu.Lock()
	defer sm.mu.Unlock()
	sm.logger.Debug("Updating overall backup stats")

	sm.state.Stats = stats

	if err := sm.saveState(); err != nil {
		sm.logger.Error("Failed to save state after updating stats", "error", err)
		return err
	}
	return nil
}

// GetScheduleState returns the state of a specific schedule
func (sm *StateManager) GetScheduleState(schedule *BackupSchedule) ScheduleState {
	sm.mu.RLock()
	defer sm.mu.RUnlock()

	key := "daily"
	if schedule.IsWeekly {
		key = fmt.Sprintf("weekly-%s", schedule.Weekday)
	}

	return sm.state.Schedules[key]
}

// GetTargetState returns the state of a specific target
func (sm *StateManager) GetTargetState(targetName string) TargetState {
	sm.mu.RLock()
	defer sm.mu.RUnlock()

	return sm.state.Targets[targetName]
}

// GetMissedBackups returns all missed backups
func (sm *StateManager) GetMissedBackups() []MissedBackup {
	sm.mu.RLock()
	defer sm.mu.RUnlock()

	// Return a copy to prevent modification
	missed := make([]MissedBackup, len(sm.state.MissedRuns))
	copy(missed, sm.state.MissedRuns)
	return missed
}

// ClearMissedBackups clears the list of missed backups
func (sm *StateManager) ClearMissedBackups() error {
	sm.mu.Lock()
	defer sm.mu.Unlock()
	sm.logger.Info("Clearing missed backups list", "previous_count", len(sm.state.MissedRuns))

	sm.state.MissedRuns = make([]MissedBackup, 0)

	if err := sm.saveState(); err != nil {
		sm.logger.Error("Failed to save state after clearing missed backups", "error", err)
		return err
	}
	return nil
}

// GetStats returns the current overall statistics.
func (sm *StateManager) GetStats() map[string]BackupStats {
	sm.mu.RLock()
	defer sm.mu.RUnlock()

	// Return a deep copy to prevent modification of the internal state map
	statsCopy := make(map[string]BackupStats, len(sm.state.Stats))
	for k := range sm.state.Stats {
		statsCopy[k] = sm.state.Stats[k]
	}
	return statsCopy
}
