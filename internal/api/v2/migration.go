// internal/api/v2/migration.go
package api

import (
	"context"
	"fmt"
	"net/http"
	"sync"
	"time"

	"github.com/labstack/echo/v4"
	datastoreV2 "github.com/tphakala/birdnet-go/internal/datastore/v2"
	"github.com/tphakala/birdnet-go/internal/datastore/v2/entities"
	"github.com/tphakala/birdnet-go/internal/datastore/v2/migration"
	"github.com/tphakala/birdnet-go/internal/logger"
	"github.com/tphakala/birdnet-go/internal/notification"
)

// MigrationStatusResponse represents the migration status for the API.
type MigrationStatusResponse struct {
	State               string     `json:"state"`
	CurrentPhase        string     `json:"current_phase,omitempty"`   // Current migration phase (detections, predictions, etc.)
	PhaseNumber         int        `json:"phase_number,omitempty"`    // Current phase number (1-based)
	TotalPhases         int        `json:"total_phases,omitempty"`    // Total number of phases
	StartedAt           *time.Time `json:"started_at,omitempty"`
	CompletedAt         *time.Time `json:"completed_at,omitempty"`
	TotalRecords        int64      `json:"total_records"`
	MigratedRecords     int64      `json:"migrated_records"`
	ProgressPercent     float64    `json:"progress_percent"`
	LastMigratedID      uint       `json:"last_migrated_id"`
	ErrorMessage        string     `json:"error_message,omitempty"`
	RelatedDataError    string     `json:"related_data_error,omitempty"` // Error from related data migration (reviews, comments, locks, predictions)
	DirtyIDCount        int64      `json:"dirty_id_count"`
	RecordsPerSecond    float64    `json:"records_per_second,omitempty"`
	EstimatedRemaining  *string    `json:"estimated_remaining,omitempty"`
	WorkerRunning       bool       `json:"worker_running"`
	WorkerPaused        bool       `json:"worker_paused"`
	CanStart            bool       `json:"can_start"`
	CanPause            bool       `json:"can_pause"`
	CanResume           bool       `json:"can_resume"`
	CanCancel           bool       `json:"can_cancel"`
	CanRollback         bool       `json:"can_rollback"`
	IsDualWriteActive   bool       `json:"is_dual_write_active"`
	ShouldReadFromV2    bool       `json:"should_read_from_v2"`
	IsV2OnlyMode        bool       `json:"is_v2_only_mode"`

	// Cleanup state fields
	CleanupState           string   `json:"cleanup_state"`             // idle, in_progress, completed, failed
	CleanupError           string   `json:"cleanup_error,omitempty"`   // Error message if failed
	CleanupTablesRemaining []string `json:"cleanup_tables_remaining"`  // MySQL: tables that failed to drop
	CleanupSpaceReclaimed  int64    `json:"cleanup_space_reclaimed"`   // Bytes freed
}

// MigrationStartRequest represents the request to start migration.
type MigrationStartRequest struct {
	TotalRecords int64 `json:"total_records"`
}

// MigrationActionResponse represents the response for migration actions.
type MigrationActionResponse struct {
	Success bool   `json:"success"`
	Message string `json:"message"`
	State   string `json:"state,omitempty"`
}

// migrationMu protects concurrent access to package-level migration state.
// All reads and writes to the variables below must hold this lock.
var migrationMu sync.RWMutex

// StateManager and Worker are injected via controller fields.
// Access these via getter functions (getStateManager, getMigrationWorker, etc.)
// to ensure thread-safety.
var (
	stateManager          *datastoreV2.StateManager
	migrationWorker       *migration.Worker
	migrationWorkerCancel context.CancelFunc // Cancel function for worker context
	isV2OnlyMode          bool
)

// SetMigrationDependencies sets the migration-related dependencies.
// This should be called during server initialization.
func SetMigrationDependencies(sm *datastoreV2.StateManager, worker *migration.Worker) {
	migrationMu.Lock()
	defer migrationMu.Unlock()
	stateManager = sm
	migrationWorker = worker
}

// SetMigrationWorkerCancel stores the cancel function for the migration worker context.
// This allows graceful shutdown to stop the worker by cancelling its context.
func SetMigrationWorkerCancel(cancel context.CancelFunc) {
	migrationMu.Lock()
	defer migrationMu.Unlock()
	migrationWorkerCancel = cancel
}

// SetV2OnlyMode indicates that the system is running in v2-only mode.
// In this mode, migration is complete and the legacy database is not available.
func SetV2OnlyMode() {
	migrationMu.Lock()
	defer migrationMu.Unlock()
	isV2OnlyMode = true
}

// StopMigrationWorker stops the migration worker if it's running.
// This should be called during graceful shutdown before closing the v2 database.
// It cancels the worker's context and then calls Stop() for immediate termination.
// Note: We snapshot the variables under lock then release before calling methods
// to prevent potential deadlocks if Stop() blocks.
func StopMigrationWorker() {
	// Snapshot variables under lock to prevent race conditions
	migrationMu.Lock()
	cancel := migrationWorkerCancel
	worker := migrationWorker
	migrationMu.Unlock()

	// Cancel the worker's context first - this signals shutdown through context.Done()
	if cancel != nil {
		cancel()
	}
	// Then call Stop() to close the stop channel for immediate termination
	if worker != nil && worker.IsRunning() {
		worker.Stop()
	}
}

// getStateManager returns the state manager with proper locking.
func getStateManager() *datastoreV2.StateManager {
	migrationMu.RLock()
	defer migrationMu.RUnlock()
	return stateManager
}

// getMigrationWorker returns the migration worker with proper locking.
func getMigrationWorker() *migration.Worker {
	migrationMu.RLock()
	defer migrationMu.RUnlock()
	return migrationWorker
}

// getIsV2OnlyMode returns whether v2-only mode is active with proper locking.
func getIsV2OnlyMode() bool {
	migrationMu.RLock()
	defer migrationMu.RUnlock()
	return isV2OnlyMode
}

// initMigrationRoutes registers the migration API routes.
func (c *Controller) initMigrationRoutes() {
	c.logInfoIfEnabled("Initializing migration routes")

	// Create migration API group under system/database
	migrationGroup := c.Group.Group("/system/database/migration")

	// Get the appropriate auth middleware
	authMiddleware := c.authMiddleware

	// Create auth-protected group
	protectedGroup := migrationGroup.Group("", authMiddleware)

	// Migration status and control routes
	protectedGroup.GET("/status", c.GetMigrationStatus)
	protectedGroup.GET("/prerequisites", c.GetPrerequisites)
	protectedGroup.POST("/start", c.StartMigration)
	protectedGroup.POST("/pause", c.PauseMigration)
	protectedGroup.POST("/resume", c.ResumeMigration)
	protectedGroup.POST("/cancel", c.CancelMigration)
	protectedGroup.POST("/rollback", c.RollbackMigration)

	c.logInfoIfEnabled("Migration routes initialized successfully")
}

// GetMigrationStatus handles GET /api/v2/system/database/migration/status
func (c *Controller) GetMigrationStatus(ctx echo.Context) error {
	ip, path := ctx.RealIP(), ctx.Request().URL.Path
	c.logInfoIfEnabled("Getting migration status", logger.String("path", path), logger.String("ip", ip))

	// Snapshot state under lock for thread-safety
	sm := getStateManager()
	worker := getMigrationWorker()
	v2Only := getIsV2OnlyMode()

	// Check if state manager is available
	if sm == nil {
		// In enhanced database mode, migration is complete and state manager is not needed
		if v2Only {
			c.logInfoIfEnabled("Running in enhanced database mode, migration is complete",
				logger.String("path", path), logger.String("ip", ip))
			// Get cleanup state for v2-only mode
			var cleanupState, cleanupErr string
			var cleanupRemaining []string
			var cleanupReclaimed int64
			if c.cleanupStatus != nil {
				cleanupState, cleanupErr, cleanupRemaining, cleanupReclaimed = c.cleanupStatus.Get()
			} else {
				cleanupState = CleanupStateIdle
			}
			return ctx.JSON(http.StatusOK, MigrationStatusResponse{
				State:                  string(entities.MigrationStatusCompleted),
				ProgressPercent:        100.0,
				WorkerRunning:          false,
				WorkerPaused:           false,
				CanStart:               false,
				CanPause:               false,
				CanResume:              false,
				CanCancel:              false,
				CanRollback:            false, // No rollback for fresh v2 installs
				IsDualWriteActive:      false,
				ShouldReadFromV2:       true,
				IsV2OnlyMode:           true,
				CleanupState:           cleanupState,
				CleanupError:           cleanupErr,
				CleanupTablesRemaining: cleanupRemaining,
				CleanupSpaceReclaimed:  cleanupReclaimed,
			})
		}
		c.logWarnIfEnabled("Migration state manager not available",
			logger.String("path", path), logger.String("ip", ip))
		return c.HandleError(ctx, fmt.Errorf("migration not configured"),
			"Migration is not configured", http.StatusServiceUnavailable)
	}

	// Get migration state
	state, err := sm.GetState()
	if err != nil {
		c.logErrorIfEnabled("Failed to get migration state",
			logger.Error(err), logger.String("path", path), logger.String("ip", ip))
		return c.HandleError(ctx, err, "Failed to get migration status", http.StatusInternalServerError)
	}

	// Get dirty ID count
	dirtyCount, err := sm.GetDirtyIDCount()
	if err != nil {
		c.logWarnIfEnabled("Failed to get dirty ID count",
			logger.Error(err), logger.String("path", path), logger.String("ip", ip))
		dirtyCount = 0
	}

	// Check dual-write and read modes
	isDualWriteActive, _ := sm.IsInDualWriteMode()
	shouldReadFromV2, _ := sm.ShouldReadFromV2()

	// Calculate progress percentage
	var progressPercent float64
	if state.TotalRecords > 0 {
		progressPercent = float64(state.MigratedRecords) / float64(state.TotalRecords) * 100
	}

	// Get worker status
	workerRunning := worker != nil && worker.IsRunning()
	workerPaused := worker != nil && worker.IsPaused()

	// Calculate rate from cumulative progress (more stable than sliding window)
	// This works correctly for both phase 1 (detections) and phase 2 (predictions)
	var recordsPerSec float64
	var estimatedRemaining *string
	if workerRunning && !workerPaused && state.PhaseStartedAt != nil && state.MigratedRecords > 0 {
		elapsed := time.Since(*state.PhaseStartedAt).Seconds()
		if elapsed > 0 {
			recordsPerSec = float64(state.MigratedRecords) / elapsed

			// Calculate estimated remaining time
			remaining := state.TotalRecords - state.MigratedRecords
			if remaining > 0 && recordsPerSec > 0 {
				seconds := float64(remaining) / recordsPerSec
				duration := time.Duration(seconds * float64(time.Second))
				formatted := formatDuration(duration)
				estimatedRemaining = &formatted
			} else if remaining <= 0 {
				zero := "0s"
				estimatedRemaining = &zero
			}
		}
	}

	// Determine available actions based on state
	canStart := state.State == entities.MigrationStatusIdle
	canPause := state.State == entities.MigrationStatusDualWrite ||
		state.State == entities.MigrationStatusMigrating
	canResume := state.State == entities.MigrationStatusPaused
	canCancel := state.State != entities.MigrationStatusIdle &&
		state.State != entities.MigrationStatusCompleted
	canRollback := state.State == entities.MigrationStatusCompleted

	// Get cleanup state
	var cleanupState, cleanupErr string
	var cleanupRemaining []string
	var cleanupReclaimed int64
	if c.cleanupStatus != nil {
		cleanupState, cleanupErr, cleanupRemaining, cleanupReclaimed = c.cleanupStatus.Get()
	} else {
		cleanupState = CleanupStateIdle
	}

	response := MigrationStatusResponse{
		State:              string(state.State),
		CurrentPhase:       string(state.CurrentPhase),
		PhaseNumber:        state.PhaseNumber,
		TotalPhases:        state.TotalPhases,
		StartedAt:          state.StartedAt,
		CompletedAt:        state.CompletedAt,
		TotalRecords:       state.TotalRecords,
		MigratedRecords:    state.MigratedRecords,
		ProgressPercent:    progressPercent,
		LastMigratedID:     state.LastMigratedID,
		ErrorMessage:       state.ErrorMessage,
		RelatedDataError:   state.RelatedDataError,
		DirtyIDCount:       dirtyCount,
		RecordsPerSecond:   recordsPerSec,
		EstimatedRemaining: estimatedRemaining,
		WorkerRunning:      workerRunning,
		WorkerPaused:       workerPaused,
		CanStart:           canStart,
		CanPause:           canPause,
		CanResume:          canResume,
		CanCancel:          canCancel,
		CanRollback:        canRollback,
		IsDualWriteActive:      isDualWriteActive,
		ShouldReadFromV2:       shouldReadFromV2,
		IsV2OnlyMode:           v2Only,
		CleanupState:           cleanupState,
		CleanupError:           cleanupErr,
		CleanupTablesRemaining: cleanupRemaining,
		CleanupSpaceReclaimed:  cleanupReclaimed,
	}

	c.logInfoIfEnabled("Migration status retrieved",
		logger.String("state", response.State),
		logger.String("phase", response.CurrentPhase),
		logger.Int("phase_number", response.PhaseNumber),
		logger.Int("total_phases", response.TotalPhases),
		logger.Int64("migrated", response.MigratedRecords),
		logger.Int64("total", response.TotalRecords),
		logger.Float64("percent", response.ProgressPercent),
		logger.Bool("worker_running", response.WorkerRunning),
		logger.String("path", path), logger.String("ip", ip))

	return ctx.JSON(http.StatusOK, response)
}

// StartMigration handles POST /api/v2/system/database/migration/start
func (c *Controller) StartMigration(ctx echo.Context) error {
	ip, path := ctx.RealIP(), ctx.Request().URL.Path
	c.logInfoIfEnabled("Starting migration", logger.String("path", path), logger.String("ip", ip))

	// Snapshot state under lock for thread-safety
	sm := getStateManager()
	worker := getMigrationWorker()

	if sm == nil {
		return c.HandleError(ctx, fmt.Errorf("migration not configured"),
			"Migration is not configured", http.StatusServiceUnavailable)
	}

	// Run pre-flight checks
	if err := c.runPreflightChecks(); err != nil {
		c.logErrorIfEnabled("Pre-flight checks failed",
			logger.Error(err), logger.String("path", path), logger.String("ip", ip))
		return c.HandleError(ctx, err, "Pre-flight checks failed", http.StatusBadRequest)
	}

	// Parse request body
	var req MigrationStartRequest
	if err := ctx.Bind(&req); err != nil {
		c.logErrorIfEnabled("Failed to parse start request",
			logger.Error(err), logger.String("path", path), logger.String("ip", ip))
		return c.HandleError(ctx, err, "Invalid request body", http.StatusBadRequest)
	}

	// Count records if not provided
	// Note: Frontend should pass total_records from the already-fetched stats
	// to avoid this slower fallback
	totalRecords := req.TotalRecords
	if totalRecords <= 0 {
		c.logWarnIfEnabled("Total records not provided, counting from database",
			logger.String("path", path), logger.String("ip", ip))

		count, err := c.Repo.CountAll(ctx.Request().Context())
		if err != nil {
			c.logErrorIfEnabled("Failed to count legacy records",
				logger.Error(err), logger.String("path", path), logger.String("ip", ip))
			return c.HandleError(ctx, err, "Failed to determine total records", http.StatusInternalServerError)
		}
		totalRecords = count
	}

	// Start migration
	if err := sm.StartMigration(totalRecords); err != nil {
		c.logErrorIfEnabled("Failed to start migration",
			logger.Error(err), logger.String("path", path), logger.String("ip", ip))
		return c.HandleError(ctx, err, "Failed to start migration", http.StatusConflict)
	}

	// Transition to dual-write
	if err := sm.TransitionToDualWrite(); err != nil {
		c.logErrorIfEnabled("Failed to transition to dual-write",
			logger.Error(err), logger.String("path", path), logger.String("ip", ip))
		// Try to cancel since we couldn't complete initialization
		if cancelErr := sm.Cancel(); cancelErr != nil {
			c.logWarnIfEnabled("Failed to cancel after transition failure",
				logger.Error(cancelErr))
		}
		return c.HandleError(ctx, err, "Failed to initialize migration", http.StatusInternalServerError)
	}

	// Start the migration worker if available
	// Use a cancellable context so shutdown can stop the worker gracefully
	if worker != nil {
		workerCtx, workerCancel := context.WithCancel(context.Background())
		SetMigrationWorkerCancel(workerCancel)
		if err := worker.Start(workerCtx); err != nil {
			workerCancel() // Clean up on failure
			c.logWarnIfEnabled("Failed to start migration worker",
				logger.Error(err), logger.String("path", path), logger.String("ip", ip))
			// Migration state is still valid, worker can be started later
		}
	}

	c.logInfoIfEnabled("Migration started successfully",
		logger.Int64("total_records", totalRecords),
		logger.String("path", path), logger.String("ip", ip))

	// Send notification that migration has started
	if notifService := notification.GetService(); notifService != nil {
		if _, err := notifService.CreateWithComponent(
			notification.TypeSystem,
			notification.PriorityMedium,
			"Database Migration Started",
			"Database migration has started. This process may take some time depending on the number of detections.",
			"database",
		); err != nil {
			c.logWarnIfEnabled("Failed to send migration start notification",
				logger.Error(err), logger.String("path", path), logger.String("ip", ip))
		}
	}

	return ctx.JSON(http.StatusOK, MigrationActionResponse{
		Success: true,
		Message: fmt.Sprintf("Migration started with %d records", totalRecords),
		State:   string(entities.MigrationStatusDualWrite),
	})
}

// PauseMigration handles POST /api/v2/system/database/migration/pause
func (c *Controller) PauseMigration(ctx echo.Context) error {
	// Snapshot state under lock for thread-safety
	sm := getStateManager()
	worker := getMigrationWorker()

	return c.executeMigrationAction(ctx, &migrationActionParams{
		logStart:          "Pausing migration",
		workerAction:      func() { worker.Pause() },
		stateAction:       func() error { return sm.Pause() },
		logFailure:        "Failed to pause migration",
		logSuccess:        "Migration paused successfully",
		responseMessage:   "Migration paused",
		responseState:     entities.MigrationStatusPaused,
		notificationTitle: "Database Migration Paused",
		notificationBody:  "Database migration has been paused. You can resume it at any time from the Database settings.",
		stateManager:      sm,
		worker:            worker,
	})
}

// ResumeMigration handles POST /api/v2/system/database/migration/resume
func (c *Controller) ResumeMigration(ctx echo.Context) error {
	ip, path := ctx.RealIP(), ctx.Request().URL.Path
	c.logInfoIfEnabled("Resuming migration", logger.String("path", path), logger.String("ip", ip))

	// Snapshot state under lock for thread-safety
	sm := getStateManager()
	worker := getMigrationWorker()

	if sm == nil {
		return c.HandleError(ctx, fmt.Errorf("migration not configured"),
			"Migration is not configured", http.StatusServiceUnavailable)
	}

	// Resume the state
	if err := sm.Resume(); err != nil {
		c.logErrorIfEnabled("Failed to resume migration",
			logger.Error(err), logger.String("path", path), logger.String("ip", ip))
		return c.HandleError(ctx, err, "Failed to resume migration", http.StatusConflict)
	}

	// Resume the worker if it was paused
	if worker != nil {
		if worker.IsPaused() {
			worker.Resume()
		} else if !worker.IsRunning() {
			// Worker was stopped, restart it with a cancellable context
			workerCtx, workerCancel := context.WithCancel(context.Background())
			SetMigrationWorkerCancel(workerCancel)
			if err := worker.Start(workerCtx); err != nil {
				workerCancel() // Clean up on failure
				c.logWarnIfEnabled("Failed to restart migration worker",
					logger.Error(err), logger.String("path", path), logger.String("ip", ip))
			}
		}
	}

	// Clear any previous error
	if err := sm.ClearError(); err != nil {
		c.logWarnIfEnabled("Failed to clear error message",
			logger.Error(err), logger.String("path", path), logger.String("ip", ip))
	}

	c.logInfoIfEnabled("Migration resumed successfully",
		logger.String("path", path), logger.String("ip", ip))

	// Get the actual state after resume
	currentState, _ := sm.GetState()
	actualState := entities.MigrationStatusDualWrite
	if currentState != nil {
		actualState = currentState.State
	}

	return ctx.JSON(http.StatusOK, MigrationActionResponse{
		Success: true,
		Message: "Migration resumed",
		State:   string(actualState),
	})
}

// CancelMigration handles POST /api/v2/system/database/migration/cancel
func (c *Controller) CancelMigration(ctx echo.Context) error {
	ip, path := ctx.RealIP(), ctx.Request().URL.Path
	c.logInfoIfEnabled("Cancelling migration", logger.String("path", path), logger.String("ip", ip))

	// Snapshot state under lock for thread-safety
	sm := getStateManager()
	worker := getMigrationWorker()

	if sm == nil {
		return c.HandleError(ctx, fmt.Errorf("migration not configured"),
			"Migration is not configured", http.StatusServiceUnavailable)
	}

	// Stop the worker first if running
	if worker != nil && worker.IsRunning() {
		worker.Stop()
	}

	// Cancel the state
	if err := sm.Cancel(); err != nil {
		c.logErrorIfEnabled("Failed to cancel migration",
			logger.Error(err), logger.String("path", path), logger.String("ip", ip))
		return c.HandleError(ctx, err, "Failed to cancel migration", http.StatusConflict)
	}

	// Clear dirty IDs since we're cancelling
	if err := sm.ClearDirtyIDs(); err != nil {
		c.logWarnIfEnabled("Failed to clear dirty IDs",
			logger.Error(err), logger.String("path", path), logger.String("ip", ip))
	}

	c.logInfoIfEnabled("Migration cancelled successfully",
		logger.String("path", path), logger.String("ip", ip))

	// Send notification that migration was cancelled
	if notifService := notification.GetService(); notifService != nil {
		if _, err := notifService.CreateWithComponent(
			notification.TypeSystem,
			notification.PriorityMedium,
			"Database Migration Cancelled",
			"Database migration has been cancelled. You can start a new migration at any time from the Database settings.",
			"database",
		); err != nil {
			c.logWarnIfEnabled("Failed to send migration cancel notification",
				logger.Error(err), logger.String("path", path), logger.String("ip", ip))
		}
	}

	return ctx.JSON(http.StatusOK, MigrationActionResponse{
		Success: true,
		Message: "Migration cancelled",
		State:   string(entities.MigrationStatusIdle),
	})
}

// RollbackMigration handles POST /api/v2/system/database/migration/rollback
// Rolls back a completed migration to use the legacy database.
func (c *Controller) RollbackMigration(ctx echo.Context) error {
	// Snapshot state under lock for thread-safety
	sm := getStateManager()
	worker := getMigrationWorker()

	return c.executeMigrationAction(ctx, &migrationActionParams{
		logStart:        "Rolling back migration",
		workerAction:    func() { worker.Stop() },
		stateAction:     func() error { return sm.Rollback() },
		logFailure:      "Failed to rollback migration",
		logSuccess:      "Migration rolled back successfully",
		responseMessage: "Migration rolled back to legacy database",
		responseState:   entities.MigrationStatusIdle,
		stateManager:    sm,
		worker:          worker,
	})
}

// runPreflightChecks verifies the system is ready for migration.
// This runs the same critical checks as GetPrerequisites and fails if any are not passed.
func (c *Controller) runPreflightChecks() error {
	// Run all critical prerequisite checks
	checks := c.runCriticalPrerequisiteChecks()

	// Check for any failures
	for _, check := range checks {
		if check.Status == CheckStatusFailed || check.Status == CheckStatusError {
			return fmt.Errorf("prerequisite check '%s' failed: %s", check.Name, check.Message)
		}
	}

	return nil
}

// runCriticalPrerequisiteChecks runs all critical checks that must pass before migration.
// This is used by both GetPrerequisites and runPreflightChecks to ensure consistency.
func (c *Controller) runCriticalPrerequisiteChecks() []PrerequisiteCheck {
	checks := make([]PrerequisiteCheck, 0, 6)

	// Common critical checks
	checks = append(checks,
		c.checkStateIdle(),
		c.checkDiskSpace(),
		c.checkLegacyAccessible(),
	)

	// Database-specific critical checks
	if c.isUsingMySQL() {
		checks = append(checks,
			c.checkMySQLTableHealth(),
			c.checkMySQLPermissions(),
		)
	} else {
		checks = append(checks,
			c.checkSQLiteIntegrity(),
			c.checkWritePermission(),
		)
	}

	// Record count is also critical
	checks = append(checks, c.checkRecordCount())

	return checks
}

// migrationActionParams defines parameters for a migration action.
type migrationActionParams struct {
	logStart          string
	workerAction      func()
	stateAction       func() error
	logFailure        string
	logSuccess        string
	responseMessage   string
	responseState     entities.MigrationStatus
	notificationTitle string // Optional: if set, sends a notification
	notificationBody  string
	stateManager      *datastoreV2.StateManager // Thread-safe snapshot
	worker            *migration.Worker         // Thread-safe snapshot
}

// executeMigrationAction handles common migration action logic.
func (c *Controller) executeMigrationAction(ctx echo.Context, params *migrationActionParams) error {
	ip, path := ctx.RealIP(), ctx.Request().URL.Path
	c.logInfoIfEnabled(params.logStart, logger.String("path", path), logger.String("ip", ip))

	if params.stateManager == nil {
		return c.HandleError(ctx, fmt.Errorf("migration not configured"),
			"Migration is not configured", http.StatusServiceUnavailable)
	}

	// Perform worker action if worker is running
	if params.worker != nil && params.worker.IsRunning() {
		params.workerAction()
	}

	// Perform state action
	if err := params.stateAction(); err != nil {
		c.logErrorIfEnabled(params.logFailure,
			logger.Error(err), logger.String("path", path), logger.String("ip", ip))
		return c.HandleError(ctx, err, params.logFailure, http.StatusConflict)
	}

	c.logInfoIfEnabled(params.logSuccess,
		logger.String("path", path), logger.String("ip", ip))

	// Send notification if configured
	if params.notificationTitle != "" {
		if notifService := notification.GetService(); notifService != nil {
			if _, err := notifService.CreateWithComponent(
				notification.TypeSystem,
				notification.PriorityMedium,
				params.notificationTitle,
				params.notificationBody,
				"database",
			); err != nil {
				c.logWarnIfEnabled("Failed to send migration notification",
					logger.Error(err), logger.String("path", path), logger.String("ip", ip))
			}
		}
	}

	return ctx.JSON(http.StatusOK, MigrationActionResponse{
		Success: true,
		Message: params.responseMessage,
		State:   string(params.responseState),
	})
}

// formatDuration formats a duration into a human-readable string.
func formatDuration(d time.Duration) string {
	if d < time.Minute {
		return fmt.Sprintf("%ds", int(d.Seconds()))
	}
	if d < time.Hour {
		minutes := int(d.Minutes())
		seconds := int(d.Seconds()) % 60
		return fmt.Sprintf("%dm %ds", minutes, seconds)
	}
	hours := int(d.Hours())
	minutes := int(d.Minutes()) % 60
	return fmt.Sprintf("%dh %dm", hours, minutes)
}
