package backup

import (
	"context"
	"fmt"
	"log"
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
	logger    *log.Logger
	state     *StateManager
}

// NewScheduler creates a new backup scheduler
func NewScheduler(manager *Manager, logger *log.Logger) (*Scheduler, error) {
	if logger == nil {
		logger = log.Default()
	}

	// Initialize state manager
	stateManager, err := NewStateManager()
	if err != nil {
		return nil, fmt.Errorf("failed to initialize state manager: %w", err)
	}

	return &Scheduler{
		manager: manager,
		logger:  logger,
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
	s.logger.Println("✅ Backup scheduler started")
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
	s.logger.Println("⏹️ Backup scheduler stopped")
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
			// If we're more than 5 minutes past the scheduled time, consider it missed
			if now.Sub(schedule.NextRun) > 5*time.Minute {
				reason := fmt.Sprintf("System time %v was more than 5 minutes past scheduled time %v",
					now.Format(time.RFC3339),
					schedule.NextRun.Format(time.RFC3339))

				if err := s.state.AddMissedBackup(schedule, reason); err != nil {
					s.logger.Printf("⚠️ Failed to record missed backup: %v", err)
				}
				s.logger.Printf("⚠️ Missed backup schedule at %v", schedule.NextRun)
			} else {
				// Run the backup
				go s.runBackup(schedule)
			}

			// Calculate next run time
			schedule.LastRun = now
			schedule.NextRun = s.calculateNextRun(now, schedule.Hour, schedule.Minute, schedule.Weekday, schedule.IsWeekly)
		}
	}
}

// runBackup executes a backup task
func (s *Scheduler) runBackup(schedule *BackupSchedule) {
	s.logger.Printf("🔄 Running scheduled backup (Weekly: %v)", schedule.IsWeekly)

	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Hour)
	defer cancel()

	// Run the backup
	if err := s.manager.RunBackup(ctx); err != nil {
		s.logger.Printf("❌ Scheduled backup failed: %v", err)

		// Record failure in state
		if err := s.state.UpdateScheduleState(schedule, false); err != nil {
			s.logger.Printf("⚠️ Failed to update schedule state: %v", err)
		}

		// Record as missed backup
		reason := fmt.Sprintf("Backup operation failed: %v", err)
		if err := s.state.AddMissedBackup(schedule, reason); err != nil {
			s.logger.Printf("⚠️ Failed to record missed backup: %v", err)
		}
		return
	}

	s.logger.Printf("✅ Scheduled backup completed successfully")

	// Update schedule state
	if err := s.state.UpdateScheduleState(schedule, true); err != nil {
		s.logger.Printf("⚠️ Failed to update schedule state: %v", err)
	}

	// Validate backup counts and retention policy
	if err := s.manager.ValidateBackupCounts(ctx); err != nil {
		s.logger.Printf("⚠️ Backup retention policy validation failed: %v", err)
	}

	// Get and log backup statistics
	stats, err := s.manager.GetBackupStats(ctx)
	if err != nil {
		s.logger.Printf("⚠️ Failed to get backup statistics: %v", err)
		return
	}

	// Update statistics in state
	if err := s.state.UpdateStats(stats); err != nil {
		s.logger.Printf("⚠️ Failed to update backup statistics: %v", err)
	}

	// Log statistics for each target
	for targetName, targetStats := range stats {
		s.logger.Printf("📊 Backup stats for target %s:", targetName)
		s.logger.Printf("   - Total backups: %d (Daily: %d, Weekly: %d)",
			targetStats.TotalBackups,
			targetStats.DailyBackups,
			targetStats.WeeklyBackups)
		s.logger.Printf("   - Total size: %d bytes", targetStats.TotalSize)
		s.logger.Printf("   - Oldest backup: %v", targetStats.OldestBackup)
		s.logger.Printf("   - Newest backup: %v", targetStats.NewestBackup)
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
	case "sunday", "sun":
		return time.Sunday, nil
	case "monday", "mon":
		return time.Monday, nil
	case "tuesday", "tue":
		return time.Tuesday, nil
	case "wednesday", "wed":
		return time.Wednesday, nil
	case "thursday", "thu":
		return time.Thursday, nil
	case "friday", "fri":
		return time.Friday, nil
	case "saturday", "sat":
		return time.Saturday, nil
	case "":
		return -1, nil // For daily backups
	default:
		return -1, fmt.Errorf("invalid weekday: %s", weekday)
	}
}

// LoadFromConfig loads schedules from the backup configuration
func (s *Scheduler) LoadFromConfig(config *conf.BackupConfig) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	// Clear existing schedules
	s.schedules = nil

	// Process each schedule in the configuration
	for _, scheduleConfig := range config.Schedules {
		if !scheduleConfig.Enabled {
			continue
		}

		weekday, err := parseWeekday(scheduleConfig.Weekday)
		if err != nil {
			s.logger.Printf("⚠️ Invalid schedule configuration: %v", err)
			continue
		}

		// Calculate next run time
		now := time.Now()
		nextRun := s.calculateNextRun(now, scheduleConfig.Hour, scheduleConfig.Minute, weekday, scheduleConfig.IsWeekly)

		schedule := BackupSchedule{
			Hour:     scheduleConfig.Hour,
			Minute:   scheduleConfig.Minute,
			Weekday:  weekday,
			IsWeekly: scheduleConfig.IsWeekly,
			NextRun:  nextRun,
		}

		// Get existing state for this schedule
		state := s.state.GetScheduleState(&schedule)
		if !state.LastSuccessful.IsZero() {
			schedule.LastRun = state.LastSuccessful
		}

		s.schedules = append(s.schedules, schedule)
	}

	if len(s.schedules) == 0 {
		s.logger.Println("ℹ️ No backup schedules configured")
	} else {
		s.logger.Printf("✅ Loaded %d backup schedule(s)", len(s.schedules))
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
	s.logger.Printf("🔄 Starting on-demand backup...")

	// Create a context with timeout
	ctx, cancel := context.WithTimeout(ctx, s.manager.getBackupTimeout())
	defer cancel()

	// Run the backup
	if err := s.manager.RunBackup(ctx); err != nil {
		s.logger.Printf("❌ On-demand backup failed: %v", err)
		return fmt.Errorf("backup operation failed: %w", err)
	}

	// Get and log backup statistics
	stats, err := s.manager.GetBackupStats(ctx)
	if err != nil {
		s.logger.Printf("⚠️ Failed to get backup statistics: %v", err)
	} else {
		// Update statistics in state
		if err := s.state.UpdateStats(stats); err != nil {
			s.logger.Printf("⚠️ Failed to update backup statistics: %v", err)
		}

		// Log statistics for each target
		for targetName, targetStats := range stats {
			s.logger.Printf("📊 Backup stats for target %s:", targetName)
			s.logger.Printf("   - Total backups: %d (Daily: %d, Weekly: %d)",
				targetStats.TotalBackups,
				targetStats.DailyBackups,
				targetStats.WeeklyBackups)
			s.logger.Printf("   - Total size: %d bytes", targetStats.TotalSize)
			s.logger.Printf("   - Oldest backup: %v", targetStats.OldestBackup)
			s.logger.Printf("   - Newest backup: %v", targetStats.NewestBackup)
		}
	}

	s.logger.Printf("✅ On-demand backup completed successfully")
	return nil
}
