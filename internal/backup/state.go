package backup

import (
	"encoding/json"
	"fmt"
	"maps"
	"os"
	"path/filepath"
	"sync"
	"time"

	"github.com/tphakala/birdnet-go/internal/conf"
	"github.com/tphakala/birdnet-go/internal/errors"
	"github.com/tphakala/birdnet-go/internal/logger"
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

// StateManager handles persistence of backup states.
//
// mu guards state. saveMu serializes writes of the state file and is always
// taken before mu, never while mu is held.
type StateManager struct {
	state     *BackupState
	statePath string
	mu        sync.RWMutex
	saveMu    sync.Mutex
	logger    logger.Logger
}

// NewStateManager creates a new state manager
func NewStateManager(_ logger.Logger) (*StateManager, error) {
	// Get config directory
	configPaths, err := conf.GetDefaultConfigPaths()
	if err != nil {
		return nil, errors.New(err).
			Component("backup").
			Category(errors.CategoryConfiguration).
			Context("operation", "get_config_paths").
			Build()
	}
	if len(configPaths) == 0 {
		return nil, errors.Newf("no config paths available").
			Component("backup").
			Category(errors.CategoryConfiguration).
			Build()
	}

	// Create state file path
	statePath := filepath.Join(configPaths[0], "backup-state.json")

	return newStateManagerAt(statePath), nil
}

// newStateManagerAt builds a manager for the given state file and loads it if present.
func newStateManagerAt(statePath string) *StateManager {
	sm := &StateManager{
		statePath: statePath,
		state: &BackupState{
			Schedules:  make(map[string]ScheduleState),
			Targets:    make(map[string]TargetState),
			Stats:      make(map[string]BackupStats),
			MissedRuns: make([]MissedBackup, 0),
		},
		logger: GetLogger().Module("statemanager"),
	}

	// Load existing state if available
	if err := sm.loadState(); err != nil {
		// If file doesn't exist, that's fine - we'll create it on first save
		if !os.IsNotExist(err) {
			sm.logger.Error("Failed to load existing backup state file", logger.String("path", statePath), logger.Error(err))
		} else {
			sm.logger.Info("No existing backup state file found, creating new one", logger.String("path", statePath))
		}
	} else {
		sm.logger.Info("Successfully loaded existing backup state", logger.String("path", statePath))
	}

	return sm
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

// marshalState renders the current state as indented JSON.
// The marshal runs under the read lock because the shallow copy of the state
// shares its maps and slice with the live state, which mutators write.
func (sm *StateManager) marshalState() ([]byte, error) {
	sm.mu.RLock()
	defer sm.mu.RUnlock()

	snapshot := *sm.state
	snapshot.LastUpdate = time.Now()
	return json.MarshalIndent(&snapshot, "", "  ")
}

// saveState saves the current backup state to disk.
// It must be called without sm.mu held: it takes saveMu first and then mu.
// Holding saveMu for the whole save keeps concurrent saves from sharing the
// temporary file, and makes the last save snapshot every earlier mutation.
func (sm *StateManager) saveState() error {
	sm.saveMu.Lock()
	defer sm.saveMu.Unlock()

	start := time.Now()

	// Marshal state to JSON with pretty printing
	data, err := sm.marshalState()
	if err != nil {
		sm.logger.Error("Failed to marshal backup state to JSON", logger.Error(err))
		return errors.New(err).
			Component("backup").
			Category(errors.CategoryFileIO).
			Context("operation", "marshal_state").
			Build()
	}

	// Create state directory if it doesn't exist (internal config path)
	dirPath := filepath.Dir(sm.statePath)
	if err := os.MkdirAll(dirPath, DefaultDirectoryPermissions()); err != nil {
		sm.logger.Error("Failed to create state directory", logger.String("path", dirPath), logger.Error(err))
		return errors.New(err).
			Component("backup").
			Category(errors.CategoryFileIO).
			Context("operation", "create_state_directory").
			Build()
	}

	// Write to temporary file first
	tempFile := sm.statePath + ".tmp"
	if err := os.WriteFile(tempFile, data, PermSecureFile); err != nil {
		sm.logger.Error("Failed to write temporary state file", logger.String("path", tempFile), logger.Error(err))
		return errors.New(err).
			Component("backup").
			Category(errors.CategoryFileIO).
			Context("operation", "write_temp_state_file").
			Build()
	}

	// Rename temporary file to actual state file (atomic operation)
	if err := os.Rename(tempFile, sm.statePath); err != nil {
		if removeErr := os.Remove(tempFile); removeErr != nil {
			sm.logger.Warn("Failed to clean up temp file after rename failure", logger.String("temp_path", tempFile), logger.Error(removeErr))
		}
		sm.logger.Error("Failed to save state file (rename failed)", logger.String("temp_path", tempFile), logger.String("final_path", sm.statePath), logger.Error(err))
		return errors.New(err).
			Component("backup").
			Category(errors.CategoryFileIO).
			Context("operation", "save_state_file").
			Build()
	}

	sm.logger.Debug("Backup state saved successfully", logger.String("path", sm.statePath), logger.Int64("duration_ms", time.Since(start).Milliseconds()))
	return nil
}

// update applies mutate under the write lock, then persists with the lock released.
// The unlock is deferred inside the closure so a panic in mutate cannot leave mu held.
func (sm *StateManager) update(mutate func(state *BackupState)) error {
	func() {
		sm.mu.Lock()
		defer sm.mu.Unlock()
		mutate(sm.state)
	}()
	return sm.saveState()
}

// UpdateScheduleState updates the state for a specific schedule
func (sm *StateManager) UpdateScheduleState(schedule *BackupSchedule, successful bool) error {
	// Generate schedule key
	key := "daily"
	if schedule.IsWeekly {
		key = fmt.Sprintf("weekly-%s", schedule.Weekday)
	}
	sm.logger.Debug("Updating schedule state", logger.String("schedule_key", key), logger.Bool("successful", successful))

	nextRun := schedule.NextRun
	err := sm.update(func(st *BackupState) {
		state := st.Schedules[key]
		state.LastAttempted = time.Now()
		if successful {
			state.LastSuccessful = time.Now()
			state.FailureCount = 0
		} else {
			state.FailureCount++
		}
		state.NextScheduled = nextRun

		st.Schedules[key] = state
	})
	if err != nil {
		sm.logger.Error("Failed to save state after updating schedule", logger.String("schedule_key", key), logger.Error(err))
		return err
	}
	return nil
}

// AddMissedBackup records a missed backup
func (sm *StateManager) AddMissedBackup(schedule *BackupSchedule, reason string) error {
	missed := MissedBackup{
		ScheduledTime: schedule.NextRun,
		Reason:        reason,
		IsWeekly:      schedule.IsWeekly,
	}
	if schedule.IsWeekly {
		missed.Weekday = schedule.Weekday.String()
	}

	sm.logger.Warn("Recording missed backup",
		logger.Time("scheduled_time", missed.ScheduledTime),
		logger.String("reason", reason),
		logger.Bool("is_weekly", missed.IsWeekly),
		logger.String("weekday", missed.Weekday))

	err := sm.update(func(st *BackupState) {
		st.MissedRuns = append(st.MissedRuns, missed)
	})
	if err != nil {
		sm.logger.Error("Failed to save state after adding missed backup", logger.Error(err))
		return err
	}
	return nil
}

// UpdateTargetState updates the state for a specific backup target
func (sm *StateManager) UpdateTargetState(targetName string, metadata *Metadata, status string) error {
	sm.logger.Debug("Updating target state",
		logger.String("target_name", targetName),
		logger.String("backup_id", metadata.ID),
		logger.String("status", status))

	err := sm.update(func(st *BackupState) {
		state := st.Targets[targetName]
		state.LastBackupID = metadata.ID
		state.LastBackupTime = metadata.Timestamp
		state.LastBackupStatus = status
		state.LastBackupSize = metadata.Size
		if status == "success" {
			state.TotalBackups++
			state.TotalSize += metadata.Size
		}

		st.Targets[targetName] = state
	})
	if err != nil {
		sm.logger.Error("Failed to save state after updating target state", logger.String("target_name", targetName), logger.Error(err))
		return err
	}
	return nil
}

// UpdateStats updates the backup statistics.
// The map is cloned so the state never aliases a map the caller keeps using.
func (sm *StateManager) UpdateStats(stats map[string]BackupStats) error {
	sm.logger.Debug("Updating overall backup stats")

	statsCopy := maps.Clone(stats)
	err := sm.update(func(st *BackupState) {
		st.Stats = statsCopy
	})
	if err != nil {
		sm.logger.Error("Failed to save state after updating stats", logger.Error(err))
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
	var previousCount int
	err := sm.update(func(st *BackupState) {
		previousCount = len(st.MissedRuns)
		st.MissedRuns = make([]MissedBackup, 0)
	})
	sm.logger.Info("Cleared missed backups list", logger.Int("previous_count", previousCount))
	if err != nil {
		sm.logger.Error("Failed to save state after clearing missed backups", logger.Error(err))
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
