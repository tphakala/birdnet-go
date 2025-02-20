package backup

import (
	"encoding/json"
	"fmt"
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
}

// NewStateManager creates a new state manager
func NewStateManager() (*StateManager, error) {
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

	sm := &StateManager{
		statePath: statePath,
		state: &BackupState{
			Schedules:  make(map[string]ScheduleState),
			Targets:    make(map[string]TargetState),
			Stats:      make(map[string]BackupStats),
			MissedRuns: make([]MissedBackup, 0),
		},
	}

	// Load existing state if available
	if err := sm.loadState(); err != nil {
		// If file doesn't exist, that's fine - we'll create it on first save
		if !os.IsNotExist(err) {
			return nil, fmt.Errorf("failed to load state: %w", err)
		}
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
	sm.mu.Lock()
	defer sm.mu.Unlock()

	// Update last update time
	sm.state.LastUpdate = time.Now()

	// Create state directory if it doesn't exist
	if err := os.MkdirAll(filepath.Dir(sm.statePath), 0o755); err != nil {
		return fmt.Errorf("failed to create state directory: %w", err)
	}

	// Marshal state to JSON with pretty printing
	data, err := json.MarshalIndent(sm.state, "", "  ")
	if err != nil {
		return fmt.Errorf("failed to marshal state: %w", err)
	}

	// Write to temporary file first
	tempFile := sm.statePath + ".tmp"
	if err := os.WriteFile(tempFile, data, 0o600); err != nil {
		return fmt.Errorf("failed to write temporary state file: %w", err)
	}

	// Rename temporary file to actual state file (atomic operation)
	if err := os.Rename(tempFile, sm.statePath); err != nil {
		os.Remove(tempFile) // Clean up temp file if rename fails
		return fmt.Errorf("failed to save state file: %w", err)
	}

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

	return sm.saveState()
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

	sm.state.MissedRuns = append(sm.state.MissedRuns, missed)
	return sm.saveState()
}

// UpdateTargetState updates the state for a specific backup target
func (sm *StateManager) UpdateTargetState(targetName string, metadata *Metadata, status string) error {
	sm.mu.Lock()
	defer sm.mu.Unlock()

	state := sm.state.Targets[targetName]
	state.LastBackupID = metadata.ID
	state.LastBackupTime = metadata.Timestamp
	state.LastBackupStatus = status
	state.LastBackupSize = metadata.Size
	state.TotalBackups++
	state.TotalSize += metadata.Size

	sm.state.Targets[targetName] = state
	return sm.saveState()
}

// UpdateStats updates the backup statistics
func (sm *StateManager) UpdateStats(stats map[string]BackupStats) error {
	sm.mu.Lock()
	defer sm.mu.Unlock()

	sm.state.Stats = stats
	return sm.saveState()
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

	sm.state.MissedRuns = make([]MissedBackup, 0)
	return sm.saveState()
}

// GetStats returns the current backup statistics
func (sm *StateManager) GetStats() map[string]BackupStats {
	sm.mu.RLock()
	defer sm.mu.RUnlock()

	// Return a copy to prevent modification
	stats := make(map[string]BackupStats)
	for k, v := range sm.state.Stats {
		stats[k] = v
	}
	return stats
}
