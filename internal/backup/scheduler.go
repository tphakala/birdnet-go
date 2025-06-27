package backup

import (
	"context"
	"fmt"
	"log/slog"
	"strings"
	"sync"
	"time"

	"github.com/tphakala/birdnet-go/internal/conf"
)

// BackupSchedule represents a scheduled backup task
type BackupSchedule struct {
	Hour     int          // Hour to run backup (0-23)
	Minute   int          // Minute to run backup (0-59)
	Weekday  time.Weekday // Day of week for weekly backups (-1 for daily)
	LastRun  time.Time    // Last successful run time
	NextRun  time.Time    // Next scheduled run time
	IsWeekly bool         // true for weekly backups, false for daily
}

// Scheduler manages backup schedules and their execution
type Scheduler struct {
	manager   *Manager
	schedules []BackupSchedule
	isRunning bool
	cancel    context.CancelFunc
	mu        sync.RWMutex
	logger    *slog.Logger
	state     *StateManager
}

// NewScheduler creates a new backup scheduler
func NewScheduler(manager *Manager, logger *slog.Logger, stateManager *StateManager) (*Scheduler, error) {
	if logger == nil {
		logger = slog.Default()
	}
	if stateManager == nil {
		logger.Error("StateManager provided to NewScheduler cannot be nil")
		return nil, fmt.Errorf("state manager cannot be nil")
	}

	return &Scheduler{
		manager: manager,
		logger:  logger.With("service", "backup_scheduler"),
		state:   stateManager,
	}, nil
}

// AddSchedule adds a new backup schedule
func (s *Scheduler) AddSchedule(hour, minute int, weekday time.Weekday, isWeekly bool) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	// Validate time parameters
	if hour < 0 || hour > 23 {
		return fmt.Errorf("invalid hour: %d", hour)
	}
	if minute < 0 || minute > 59 {
		return fmt.Errorf("invalid minute: %d", minute)
	}

	// For daily backups, set weekday to -1
	if !isWeekly {
		weekday = -1
	}

	// Calculate next run time
	now := time.Now()
	nextRun := s.calculateNextRun(now, hour, minute, weekday, isWeekly)

	schedule := BackupSchedule{
		Hour:     hour,
		Minute:   minute,
		Weekday:  weekday,
		IsWeekly: isWeekly,
		NextRun:  nextRun,
	}

	s.schedules = append(s.schedules, schedule)
	return nil
}

// Start begins the scheduler
func (s *Scheduler) Start() {
	s.mu.Lock()
	defer s.mu.Unlock()

	if s.isRunning {
		return
	}

	ctx, cancel := context.WithCancel(context.Background())
	s.cancel = cancel
	s.isRunning = true

	go s.run(ctx)
	s.logger.Info("Backup scheduler started")
}

// Stop stops the scheduler
func (s *Scheduler) Stop() {
	s.mu.Lock()
	defer s.mu.Unlock()

	if !s.isRunning {
		return
	}

	s.cancel()
	s.isRunning = false
	s.logger.Info("Backup scheduler stopped")
}

// IsRunning returns whether the scheduler is currently running
func (s *Scheduler) IsRunning() bool {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.isRunning
}

// GetMissedRuns returns a list of missed backup times
func (s *Scheduler) GetMissedRuns() []time.Time {
	missed := s.state.GetMissedBackups()
	times := make([]time.Time, len(missed))
	for i, m := range missed {
		times[i] = m.ScheduledTime
	}
	return times
}

// ClearMissedRuns clears the list of missed runs
func (s *Scheduler) ClearMissedRuns() {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.schedules = nil
}

// run is the main scheduler loop
func (s *Scheduler) run(ctx context.Context) {
	ticker := time.NewTicker(1 * time.Minute)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			return
		case now := <-ticker.C:
			s.checkSchedules(now)
		}
	}
}

// checkSchedules checks if any backups need to be run
func (s *Scheduler) checkSchedules(now time.Time) {
	s.mu.Lock()
	defer s.mu.Unlock()

	for i := range s.schedules {
		schedule := &s.schedules[i]

		// Check if it's time to run this schedule
		if now.After(schedule.NextRun) || now.Equal(schedule.NextRun) {
			scheduleTime := schedule.NextRun // Store the scheduled time for logging
			// If we're more than 5 minutes past the scheduled time, consider it missed
			if now.Sub(scheduleTime) > 5*time.Minute {
				reason := fmt.Sprintf("System time %v was more than 5 minutes past scheduled time %v",
					now.Format(time.RFC3339),
					scheduleTime.Format(time.RFC3339))

				if err := s.state.AddMissedBackup(schedule, reason); err != nil {
					s.logger.Warn("Failed to record missed backup state", "scheduled_time", scheduleTime, "error", err)
				}
				s.logger.Warn("Missed backup schedule", "scheduled_time", scheduleTime, "current_time", now, "reason", reason)
			} else {
				// Run the backup
				go s.runBackup(schedule)
			}

			// Calculate next run time
			schedule.LastRun = now // Record the time we *checked* this schedule
			schedule.NextRun = s.calculateNextRun(now, schedule.Hour, schedule.Minute, schedule.Weekday, schedule.IsWeekly)
			s.logger.Debug("Calculated next run time for schedule", "schedule_type", s.getScheduleType(schedule), "next_run", schedule.NextRun)
		}
	}
}

// runBackup executes a backup task
func (s *Scheduler) runBackup(schedule *BackupSchedule) {
	scheduleType := s.getScheduleType(schedule)
	s.logger.Info("Running scheduled backup", "schedule_type", scheduleType)
	start := time.Now()

	// Use manager's backup timeout duration
	backupTimeout := s.manager.getBackupTimeout()
	ctx, cancel := context.WithTimeout(context.Background(), backupTimeout)
	defer cancel()

	// Run the backup
	if err := s.manager.RunBackup(ctx); err != nil {
		duration := time.Since(start)
		s.logger.Error("Scheduled backup failed", "schedule_type", scheduleType, "error", err, "duration_ms", duration.Milliseconds())

		// Record failure in state
		if errState := s.state.UpdateScheduleState(schedule, false); errState != nil {
			s.logger.Warn("Failed to update schedule state after failure", "schedule_type", scheduleType, "error", errState)
		}

		// Record as missed backup (since it failed)
		reason := fmt.Sprintf("Backup operation failed: %v", err)
		if errState := s.state.AddMissedBackup(schedule, reason); errState != nil {
			s.logger.Warn("Failed to record missed backup after failure", "schedule_type", scheduleType, "error", errState)
		}
		return
	}
	duration := time.Since(start)
	s.logger.Info("Scheduled backup completed successfully", "schedule_type", scheduleType, "duration_ms", duration.Milliseconds())

	// Update schedule state
	if err := s.state.UpdateScheduleState(schedule, true); err != nil {
		s.logger.Warn("Failed to update schedule state after success", "schedule_type", scheduleType, "error", err)
	}

	// Validate backup counts and retention policy (use a separate context)
	// Use manager's cleanup timeout as a reasonable duration for validation
	validationCtx, validationCancel := context.WithTimeout(context.Background(), s.manager.getCleanupTimeout())
	defer validationCancel()
	if err := s.manager.ValidateBackupCounts(validationCtx); err != nil {
		// Log warning or error depending on severity desired
		s.logger.Warn("Backup retention policy validation failed after backup", "schedule_type", scheduleType, "error", err)
	}

	// Get and log backup statistics (use a separate context)
	// Use manager's operation timeout
	statsCtx, statsCancel := context.WithTimeout(context.Background(), s.manager.getOperationTimeout())
	defer statsCancel()
	stats, err := s.manager.GetBackupStats(statsCtx)
	if err != nil {
		s.logger.Warn("Failed to get backup statistics after backup", "schedule_type", scheduleType, "error", err)
		return // Don't proceed if stats failed
	}

	// Update statistics in state
	if err := s.state.UpdateStats(stats); err != nil {
		s.logger.Warn("Failed to update backup statistics in state", "schedule_type", scheduleType, "error", err)
	}

	// Log statistics for each target
	for targetName := range stats {
		targetStats := stats[targetName]
		s.logger.Info("Backup stats",
			"schedule_type", scheduleType,
			"target_name", targetName,
			"total_backups", targetStats.TotalBackups,
			"daily_backups", targetStats.DailyBackups,
			"weekly_backups", targetStats.WeeklyBackups,
			"total_size_bytes", targetStats.TotalSize,
			"oldest_backup_ts", targetStats.OldestBackup.Format(time.RFC3339),
			"newest_backup_ts", targetStats.NewestBackup.Format(time.RFC3339),
		)
	}

	// Perform cleanup *after* a successful backup
	cleanupCtx, cleanupCancel := context.WithTimeout(context.Background(), s.manager.getCleanupTimeout())
	defer cleanupCancel()
	if err := s.manager.performBackupCleanup(cleanupCtx); err != nil {
		s.logger.Error("Failed to perform post-backup cleanup", "schedule_type", scheduleType, "error", err)
	}
}

// calculateNextRun determines the next run time for a schedule
func (s *Scheduler) calculateNextRun(now time.Time, hour, minute int, weekday time.Weekday, isWeekly bool) time.Time {
	var next time.Time

	if isWeekly {
		// For weekly backups
		next = time.Date(
			now.Year(), now.Month(), now.Day(),
			hour, minute, 0, 0, now.Location(),
		)

		// Adjust to next occurrence of weekday
		daysUntilWeekday := int(weekday - next.Weekday())
		if daysUntilWeekday <= 0 {
			daysUntilWeekday += 7
		}
		next = next.AddDate(0, 0, daysUntilWeekday)
	} else {
		// For daily backups
		next = time.Date(
			now.Year(), now.Month(), now.Day(),
			hour, minute, 0, 0, now.Location(),
		)

		// If we're past the time today, schedule for tomorrow
		if now.After(next) {
			next = next.AddDate(0, 0, 1)
		}
	}

	return next
}

// UpdateSchedules updates all schedules based on new configuration
func (s *Scheduler) UpdateSchedules(schedules []BackupSchedule) {
	s.mu.Lock()
	defer s.mu.Unlock()

	s.schedules = make([]BackupSchedule, len(schedules))
	copy(s.schedules, schedules)

	// Recalculate next run times for all schedules
	now := time.Now()
	for i := range s.schedules {
		schedule := &s.schedules[i]
		schedule.NextRun = s.calculateNextRun(
			now,
			schedule.Hour,
			schedule.Minute,
			schedule.Weekday,
			schedule.IsWeekly,
		)
	}
}

// parseWeekday converts a weekday string to time.Weekday
func parseWeekday(weekday string) (time.Weekday, error) {
	switch strings.ToLower(weekday) {
	case "sunday":
		return time.Sunday, nil
	case "monday":
		return time.Monday, nil
	case "tuesday":
		return time.Tuesday, nil
	case "wednesday":
		return time.Wednesday, nil
	case "thursday":
		return time.Thursday, nil
	case "friday":
		return time.Friday, nil
	case "saturday":
		return time.Saturday, nil
	default:
		return -1, fmt.Errorf("invalid weekday: %s", weekday)
	}
}

// LoadFromConfig loads schedules from the backup configuration
func (s *Scheduler) LoadFromConfig(config *conf.BackupConfig) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	s.schedules = nil // Clear existing schedules before loading
	s.logger.Info("Loading backup schedules from configuration", "schedule_count", len(config.Schedules))

	for _, scheduleConf := range config.Schedules {
		if !scheduleConf.Enabled {
			s.logger.Debug("Skipping disabled schedule", "hour", scheduleConf.Hour, "minute", scheduleConf.Minute, "weekday", scheduleConf.Weekday)
			continue
		}

		// Validate time
		if scheduleConf.Hour < 0 || scheduleConf.Hour > 23 {
			errMsg := fmt.Sprintf("invalid hour %d in schedule config", scheduleConf.Hour)
			s.logger.Error(errMsg, "config", scheduleConf)
			return NewError(ErrConfig, errMsg, nil)
		}
		if scheduleConf.Minute < 0 || scheduleConf.Minute > 59 {
			errMsg := fmt.Sprintf("invalid minute %d in schedule config", scheduleConf.Minute)
			s.logger.Error(errMsg, "config", scheduleConf)
			return NewError(ErrConfig, errMsg, nil)
		}

		var weekday time.Weekday
		var isWeekly bool

		// Determine weekday and weekly status based on configuration
		switch {
		case scheduleConf.IsWeekly:
			// Explicitly marked as weekly
			isWeekly = true
			if scheduleConf.Weekday == "" { // No weekday specified but marked as weekly
				s.logger.Warn("Schedule marked as weekly but no weekday specified, defaulting to Sunday", "config", scheduleConf)
				weekday = time.Sunday
			} else {
				// Parse the specified weekday
				parsedDay, err := parseWeekday(scheduleConf.Weekday)
				if err != nil {
					errMsg := fmt.Sprintf("invalid weekday '%s' in schedule config", scheduleConf.Weekday)
					s.logger.Error(errMsg, "config", scheduleConf, "error", err)
					return NewError(ErrConfig, errMsg, err)
				}
				weekday = parsedDay
			}
		case scheduleConf.Weekday != "":
			// If weekday is specified but not explicitly marked as weekly, still make it weekly
			isWeekly = true
			parsedDay, err := parseWeekday(scheduleConf.Weekday)
			if err != nil {
				errMsg := fmt.Sprintf("invalid weekday '%s' in schedule config", scheduleConf.Weekday)
				s.logger.Error(errMsg, "config", scheduleConf, "error", err)
				return NewError(ErrConfig, errMsg, err)
			}
			weekday = parsedDay
		default:
			// Neither IsWeekly nor Weekday specified, it's a daily schedule
			isWeekly = false
			weekday = -1 // Explicitly set weekday to -1 for daily schedules
		}

		// Calculate next run time based on current time
		now := time.Now()
		nextRun := s.calculateNextRun(now, scheduleConf.Hour, scheduleConf.Minute, weekday, isWeekly)

		schedule := BackupSchedule{
			Hour:     scheduleConf.Hour,
			Minute:   scheduleConf.Minute,
			Weekday:  weekday,
			IsWeekly: isWeekly,
			NextRun:  nextRun,
		}

		s.schedules = append(s.schedules, schedule)
		s.logger.Info("Loaded schedule",
			"hour", schedule.Hour,
			"minute", schedule.Minute,
			"weekday", s.formatWeekday(schedule.Weekday),
			"is_weekly", schedule.IsWeekly,
			"next_run", schedule.NextRun.Format(time.RFC3339),
		)
	}

	if len(s.schedules) == 0 {
		s.logger.Warn("No enabled backup schedules loaded from configuration.")
	}

	return nil
}

// GetMissedBackups returns all missed backups
func (s *Scheduler) GetMissedBackups() []MissedBackup {
	return s.state.GetMissedBackups()
}

// ClearMissedBackups clears the list of missed backups
func (s *Scheduler) ClearMissedBackups() error {
	return s.state.ClearMissedBackups()
}

// GetBackupStats returns current backup statistics
func (s *Scheduler) GetBackupStats() map[string]BackupStats {
	return s.state.GetStats()
}

// TriggerBackup initiates an immediate backup operation
func (s *Scheduler) TriggerBackup(ctx context.Context) error {
	s.logger.Info("Manually triggering backup run...")
	// Run the backup process directly (might block depending on implementation)
	if err := s.manager.RunBackup(ctx); err != nil {
		s.logger.Error("Manual backup trigger failed", "error", err)
		return err
	}
	s.logger.Info("Manual backup trigger completed successfully")
	return nil
}

// getScheduleType returns a string representation of the schedule type
func (s *Scheduler) getScheduleType(schedule *BackupSchedule) string {
	if schedule.IsWeekly {
		return fmt.Sprintf("Weekly (%s)", s.formatWeekday(schedule.Weekday))
	}
	return "Daily"
}

// formatWeekday returns a string representation of the weekday
func (s *Scheduler) formatWeekday(wd time.Weekday) string {
	// Check if weekday is valid (Sunday=0 to Saturday=6)
	if wd < time.Sunday || wd > time.Saturday {
		// Handle -1 case specifically for "Daily"
		if wd == -1 {
			return "Daily"
		}
		return "Invalid"
	}
	return wd.String() // Call String() on the value wd
}
