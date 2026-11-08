// internal/api/v2/imports/migration.go
package importsapi

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
	"github.com/tphakala/birdnet-go/internal/errors"
	"github.com/tphakala/birdnet-go/internal/logger"
	"github.com/tphakala/birdnet-go/internal/notification"
	"github.com/tphakala/birdnet-go/internal/telemetry"
)

const (
	// migrationWorkerStopTimeout bounds how long rollback and cancel wait for the
	// worker to exit. It stays below the server write timeout (api.DefaultWriteTimeout)
	// and the frontend request timeout, both 30 s.
	migrationWorkerStopTimeout = 20 * time.Second

	// startPreflightTimeout bounds the checks a start re-runs. It stays below the
	// server write timeout and the frontend request timeout, both 30 s: the write
	// timeout does not cancel the request context, so this bound is what limits the work.
	startPreflightTimeout = 20 * time.Second

	// preFlightCheckParam is the error parameter that names the blocking check.
	preFlightCheckParam = "check"

	// msgNoStoredResult is the message of a reused check the stored run did not contain.
	msgNoStoredResult = "No stored result for this check"

	// migrationWorkerShutdownWait bounds how long shutdown waits for the worker
	// (including its final tail sync drain) before the databases are closed. It
	// stays inside the 3 s core shutdown tier budget and leaves time for WAL
	// checkpoints.
	migrationWorkerShutdownWait = 2 * time.Second

	// msgMigrationWorkerStopping is shown when the worker did not stop in time.
	msgMigrationWorkerStopping = "Migration worker is still stopping, try again shortly"

	// msgPreflightInterrupted prefixes the error when the server stopped during the pre-flight checks.
	msgPreflightInterrupted = "pre-flight checks interrupted"
)

// MigrationStatusResponse represents the migration status for the API.
type MigrationStatusResponse struct {
	State            string     `json:"state"`
	CurrentPhase     string     `json:"current_phase,omitempty"` // Current migration phase (detections, predictions, etc.)
	PhaseNumber      int        `json:"phase_number,omitempty"`  // Current phase number (1-based)
	TotalPhases      int        `json:"total_phases,omitempty"`  // Total number of phases
	StartedAt        *time.Time `json:"started_at,omitempty"`
	CompletedAt      *time.Time `json:"completed_at,omitempty"`
	TotalRecords     int64      `json:"total_records"`
	MigratedRecords  int64      `json:"migrated_records"`
	ProgressPercent  float64    `json:"progress_percent"`
	LastMigratedID   uint       `json:"last_migrated_id"`
	ErrorMessage     string     `json:"error_message,omitempty"`
	RelatedDataError string     `json:"related_data_error,omitempty"` // Error from related data migration (reviews, comments, locks, predictions)
	// AuxiliaryDataStatus is the recorded auxiliary data outcome (succeeded, failed,
	// not_required); absent when unknown. AuxiliaryDataError summarises a failure.
	AuxiliaryDataStatus string  `json:"auxiliary_data_status,omitempty"`
	AuxiliaryDataError  string  `json:"auxiliary_data_error,omitempty"`
	DirtyIDCount        int64   `json:"dirty_id_count"`
	RecordsPerSecond    float64 `json:"records_per_second,omitempty"`
	EstimatedRemaining  *string `json:"estimated_remaining,omitempty"`
	WorkerRunning       bool    `json:"worker_running"`
	WorkerPaused        bool    `json:"worker_paused"`
	CanStart            bool    `json:"can_start"`
	CanPause            bool    `json:"can_pause"`
	CanResume           bool    `json:"can_resume"`
	CanRetryValidation  bool    `json:"can_retry_validation"`
	CanCancel           bool    `json:"can_cancel"`
	CanRollback         bool    `json:"can_rollback"`
	IsDualWriteActive   bool    `json:"is_dual_write_active"`
	ShouldReadFromV2    bool    `json:"should_read_from_v2"`
	IsV2OnlyMode        bool    `json:"is_v2_only_mode"`
	// LegacyNotice is the legacy-schema notice kind (migration.LegacyNoticeKind)
	// for the dashboard banner: "none" unless the process runs the legacy schema.
	LegacyNotice string `json:"legacy_notice"`

	// Cleanup state fields
	CleanupState           string   `json:"cleanup_state"`            // idle, in_progress, completed, failed
	CleanupError           string   `json:"cleanup_error,omitempty"`  // Error message if failed
	CleanupTablesRemaining []string `json:"cleanup_tables_remaining"` // MySQL: tables that failed to drop
	CleanupSpaceReclaimed  int64    `json:"cleanup_space_reclaimed"`  // Bytes freed
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
	migrationTelemetry    *migration.MigrationTelemetry
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
// It cancels the worker's context, then calls Stop() and waits, bounded by
// migrationWorkerShutdownWait and ctx. A final tail sync drain that finishes
// within that wait completes before the databases close; after it the caller
// closes them regardless. It returns an error wrapping
// migration.ErrWorkerStopTimeout when the worker is still running at the deadline.
// Note: We snapshot the variables under lock then release before calling methods
// to prevent potential deadlocks if Stop() blocks.
func StopMigrationWorker(ctx context.Context) error {
	// Snapshot variables under lock to prevent race conditions
	migrationMu.Lock()
	cancel := migrationWorkerCancel
	worker := migrationWorker
	migrationMu.Unlock()

	// Cancel the worker's context first - this signals shutdown through context.Done()
	if cancel != nil {
		cancel()
	}
	if worker == nil {
		return nil
	}
	// Then call Stop() to close the stop channel and wait for the goroutine to exit
	waitCtx, waitCancel := context.WithTimeout(ctx, migrationWorkerShutdownWait)
	defer waitCancel()
	return worker.Stop(waitCtx)
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

// SetMigrationTelemetry sets the telemetry reporter for migration events.
func SetMigrationTelemetry(mt *migration.MigrationTelemetry) {
	migrationMu.Lock()
	defer migrationMu.Unlock()
	migrationTelemetry = mt
}

// getMigrationTelemetry returns the migration telemetry reporter with proper locking.
func getMigrationTelemetry() *migration.MigrationTelemetry {
	migrationMu.RLock()
	defer migrationMu.RUnlock()
	return migrationTelemetry
}

// getIsV2OnlyMode returns whether v2-only mode is active with proper locking.
func getIsV2OnlyMode() bool {
	migrationMu.RLock()
	defer migrationMu.RUnlock()
	return isV2OnlyMode
}

// migrationNotConfiguredMessage is the 503 message for a request that needs
// the migration state manager when the install has none.
const migrationNotConfiguredMessage = "Migration is not configured"

// errMigrationNotConfigured is the error behind migrationNotConfiguredMessage.
var errMigrationNotConfigured = errors.NewStd("migration not configured")

// errPrerequisitesNotReady is the error behind a start without a usable prerequisites run.
var errPrerequisitesNotReady = errors.NewStd("prerequisites run missing, running, stale or for other settings")

// requireMigrationStateManager is middleware that returns 503 if the migration state manager is not available.
func (c *Handler) requireMigrationStateManager(next echo.HandlerFunc) echo.HandlerFunc {
	return func(ctx echo.Context) error {
		if getStateManager() == nil {
			return c.HandleErrorWithKey(ctx, errMigrationNotConfigured,
				migrationNotConfiguredMessage, http.StatusServiceUnavailable, notification.MsgErrMigrationNotConfigured, nil)
		}
		return next(ctx)
	}
}

// RegisterMigrationRoutes registers the migration API routes on the supplied v2 group.
func (c *Handler) RegisterMigrationRoutes(g *echo.Group) {
	c.LogInfoIfEnabled("Initializing migration routes")

	// Create migration API group under system/database
	migrationGroup := g.Group("/system/database/migration")

	// Get the appropriate auth middleware
	authMiddleware := c.AuthMiddleware

	// Create auth-protected group
	protectedGroup := migrationGroup.Group("", authMiddleware)

	// Status and prerequisites have special handling (e.g., v2-only mode) and don't require the state manager
	protectedGroup.GET("/status", c.GetMigrationStatus)
	protectedGroup.GET("/prerequisites", c.GetPrerequisites)
	protectedGroup.POST("/prerequisites/run", c.RunPrerequisites)

	// POST operations require the migration state manager to be available
	smGroup := protectedGroup.Group("", c.requireMigrationStateManager)
	smGroup.POST("/start", c.StartMigration)
	smGroup.POST("/pause", c.PauseMigration)
	smGroup.POST("/resume", c.ResumeMigration)
	smGroup.POST("/retry-validation", c.RetryValidation)
	smGroup.POST("/cancel", c.CancelMigration)
	smGroup.POST("/rollback", c.RollbackMigration)

	c.LogInfoIfEnabled("Migration routes initialized successfully")
}

// GetMigrationStatus handles GET /api/v2/system/database/migration/status
func (c *Handler) GetMigrationStatus(ctx echo.Context) error {
	ip, path := ctx.RealIP(), ctx.Request().URL.Path
	c.LogInfoIfEnabled("Getting migration status", logger.String("path", path), logger.String("ip", ip))

	// Snapshot state under lock for thread-safety
	sm := getStateManager()
	worker := getMigrationWorker()
	v2Only := getIsV2OnlyMode()

	// Check if state manager is available
	if sm == nil {
		// In enhanced database mode, migration is complete and state manager is not needed
		if v2Only {
			c.LogInfoIfEnabled("Running in enhanced database mode, migration is complete",
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
				LegacyNotice:           string(migration.LegacyNoticeNone),
				CleanupState:           cleanupState,
				CleanupError:           cleanupErr,
				CleanupTablesRemaining: cleanupRemaining,
				CleanupSpaceReclaimed:  cleanupReclaimed,
			})
		}
		// A legacy install without a state manager answers 503 on every request
		// here, and the dashboard banner polls this endpoint on each page load.
		// The condition is expected and was already reported once when migration
		// setup failed at startup, so respond without the error log and the
		// server error report that HandleErrorWithKey would add.
		c.LogDebugIfEnabled("Migration state manager not available",
			logger.String("path", path), logger.String("ip", ip))
		resp := c.NewErrorResponse(errMigrationNotConfigured, migrationNotConfiguredMessage, http.StatusServiceUnavailable)
		resp.ErrorKey = notification.MsgErrMigrationNotConfigured
		return ctx.JSON(http.StatusServiceUnavailable, resp)
	}

	// Get migration state
	state, err := sm.GetState()
	if err != nil {
		c.LogErrorIfEnabled("Failed to get migration state",
			logger.Error(err), logger.String("path", path), logger.String("ip", ip))
		return c.HandleError(ctx, err, "Failed to get migration status", http.StatusInternalServerError)
	}

	// Get dirty ID count
	dirtyCount, err := sm.GetDirtyIDCount()
	if err != nil {
		c.LogWarnIfEnabled("Failed to get dirty ID count",
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
	canPause := state.CanPause()
	canResume := state.CanResume()
	canRetryValidation := state.CanRetryValidation()
	canCancel := state.CanCancel()
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

	auxStatus := state.AuxiliaryDataOutcome()
	auxError := ""
	if auxStatus == entities.AuxiliaryDataStatusFailed {
		auxError = state.AuxiliaryDataError
	}

	response := MigrationStatusResponse{
		State:                  string(state.State),
		CurrentPhase:           string(state.CurrentPhase),
		PhaseNumber:            state.PhaseNumber,
		TotalPhases:            state.TotalPhases,
		StartedAt:              state.StartedAt,
		CompletedAt:            state.CompletedAt,
		TotalRecords:           state.TotalRecords,
		MigratedRecords:        state.MigratedRecords,
		ProgressPercent:        progressPercent,
		LastMigratedID:         state.LastMigratedID,
		ErrorMessage:           state.ErrorMessage,
		RelatedDataError:       state.RelatedDataError,
		AuxiliaryDataStatus:    string(auxStatus),
		AuxiliaryDataError:     auxError,
		DirtyIDCount:           dirtyCount,
		RecordsPerSecond:       recordsPerSec,
		EstimatedRemaining:     estimatedRemaining,
		WorkerRunning:          workerRunning,
		WorkerPaused:           workerPaused,
		CanStart:               canStart,
		CanPause:               canPause,
		CanResume:              canResume,
		CanRetryValidation:     canRetryValidation,
		CanCancel:              canCancel,
		CanRollback:            canRollback,
		IsDualWriteActive:      isDualWriteActive,
		ShouldReadFromV2:       shouldReadFromV2,
		IsV2OnlyMode:           v2Only,
		LegacyNotice:           string(migration.LegacyNotice(!v2Only, state.State)),
		CleanupState:           cleanupState,
		CleanupError:           cleanupErr,
		CleanupTablesRemaining: cleanupRemaining,
		CleanupSpaceReclaimed:  cleanupReclaimed,
	}

	c.LogInfoIfEnabled("Migration status retrieved",
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
func (c *Handler) StartMigration(ctx echo.Context) error {
	ip, path := ctx.RealIP(), ctx.Request().URL.Path
	c.LogInfoIfEnabled("Starting migration", logger.String("path", path), logger.String("ip", ip))

	// Snapshot state under lock for thread-safety
	sm := getStateManager()
	worker := getMigrationWorker()

	// Parse request body first, so a slow check can never run into the read timeout
	var req MigrationStartRequest
	if err := ctx.Bind(&req); err != nil {
		c.LogErrorIfEnabled("Failed to parse start request",
			logger.Error(err), logger.String("path", path), logger.String("ip", ip))
		return c.HandleErrorWithKey(ctx, err, "Invalid request body", http.StatusBadRequest, notification.MsgErrMigrationInvalidBody, nil)
	}

	// The checks that remain after the stored run are cheap; bound them below the server
	// write timeout. A request context does not end when the server stops, so follow the
	// lifecycle too. A client that has gone or a server that is stopping is answered as
	// such before anything else looks at the stored run.
	limit := c.startPreflightLimit
	if limit == 0 {
		limit = startPreflightTimeout
	}
	pctx, cancel := context.WithTimeout(ctx.Request().Context(), limit)
	defer cancel()
	defer context.AfterFunc(c.lifecycleContext(), cancel)()
	if ctx.Request().Context().Err() != nil || c.lifecycleContext().Err() != nil {
		return c.abandonedStartResponse(ctx, path, ip)
	}

	// The expensive checks come from the prerequisites run the page started
	stored, reason := c.prereqRunner().passingResult(c.dbFingerprint())
	if reason != prereqReuseOK && reason != prereqReuseFailedCheck {
		c.LogInfoIfEnabled("Start refused: no usable prerequisites run",
			logger.String("reason", string(reason)), logger.String("path", path), logger.String("ip", ip))
		return c.prerequisitesNotReady(ctx)
	}

	if reason == prereqReuseFailedCheck {
		return c.storedRunFailed(ctx, stored.Response.Checks, path, ip)
	}

	reused := make(map[string]PrerequisiteCheck, len(stored.Response.Checks))
	for _, check := range stored.Response.Checks {
		reused[check.ID] = check
	}
	checks := c.runCriticalPrerequisiteChecks(pctx, reused)
	if pctx.Err() != nil {
		return c.abandonedStartResponse(ctx, path, ip)
	}
	if check := firstBlockingCheck(checks); check != nil {
		return c.preflightFailed(ctx, check, path, ip)
	}

	// Count records if not provided by the request or the stored run
	totalRecords := req.TotalRecords
	if totalRecords <= 0 && stored.RecordCountKnown {
		totalRecords = stored.RecordCount
	}
	if totalRecords <= 0 {
		c.LogWarnIfEnabled("Total records not provided, counting from database",
			logger.String("path", path), logger.String("ip", ip))

		count, err := c.Repo.CountAll(pctx)
		if pctx.Err() != nil {
			return c.abandonedStartResponse(ctx, path, ip)
		}
		if err != nil {
			c.LogErrorIfEnabled("Failed to count legacy records",
				logger.Error(err), logger.String("path", path), logger.String("ip", ip))
			return c.HandleErrorWithKey(ctx, err, "Failed to determine total records", http.StatusInternalServerError, notification.MsgErrMigrationRecordCount, nil)
		}
		totalRecords = count
	}

	// A client that left, or a shutdown that began, after the checks must not start a migration.
	if pctx.Err() != nil {
		return c.abandonedStartResponse(ctx, path, ip)
	}
	if err := c.lifecycleContext().Err(); err != nil {
		return c.rejectInterruptedStart(ctx, err)
	}

	// Start migration
	if err := sm.StartMigration(totalRecords); err != nil {
		c.LogErrorIfEnabled("Failed to start migration",
			logger.Error(err), logger.String("path", path), logger.String("ip", ip))
		return c.HandleErrorWithKey(ctx, err, "Failed to start migration", http.StatusConflict, notification.MsgErrMigrationStartFailed, nil)
	}

	// Transition to dual-write
	if err := sm.TransitionToDualWrite(); err != nil {
		c.LogErrorIfEnabled("Failed to transition to dual-write",
			logger.Error(err), logger.String("path", path), logger.String("ip", ip))
		// The state moved even though the start failed, so drop the stored result
		// whether or not the cancel below succeeds.
		c.prereqRunner().reset()
		// Try to cancel since we couldn't complete initialization
		if cancelErr := sm.Cancel(); cancelErr != nil {
			c.LogWarnIfEnabled("Failed to cancel after transition failure",
				logger.Error(cancelErr))
		}
		return c.HandleErrorWithKey(ctx, err, "Failed to initialize migration", http.StatusInternalServerError, notification.MsgErrMigrationInitFailed, nil)
	}

	// The state moved on, so a stored passing result no longer describes it.
	c.prereqRunner().reset()

	// A shutdown that began since the transition would never stop a worker started
	// now, so undo the start instead.
	if err := c.lifecycleContext().Err(); err != nil {
		if cancelErr := sm.Cancel(); cancelErr != nil {
			c.LogWarnIfEnabled("Failed to cancel after shutdown during start",
				logger.Error(cancelErr))
		}
		return c.rejectInterruptedStart(ctx, err)
	}

	// Start the migration worker if available
	// Use a cancellable context so shutdown can stop the worker gracefully
	if worker != nil {
		workerCtx, workerCancel := context.WithCancel(context.Background())
		SetMigrationWorkerCancel(workerCancel)
		if err := worker.Start(workerCtx); err != nil {
			workerCancel() // Clean up on failure
			c.LogWarnIfEnabled("Failed to start migration worker",
				logger.Error(err), logger.String("path", path), logger.String("ip", ip))
			// Migration state is still valid, worker can be started later
		}
	}

	c.LogInfoIfEnabled("Migration started successfully",
		logger.Int64("total_records", totalRecords),
		logger.String("path", path), logger.String("ip", ip))

	// Report migration start to telemetry
	if mt := getMigrationTelemetry(); mt != nil {
		mt.ReportStarted(totalRecords)
	}

	// Send notification that migration has started
	if notifService := c.getNotificationService(); notifService != nil {
		// Build notification fully before broadcast to ensure SSE subscribers see translation keys
		notif := notification.NewNotification(
			notification.TypeSystem,
			notification.PriorityMedium,
			"Database Migration Started",
			"Migration has started. This may take a while depending on the number of detections.",
		).WithComponent("database").
			WithTitleKey(notification.MsgMigrationStartedTitle, nil).
			WithMessageKey(notification.MsgMigrationStartedMessage, nil)

		if err := notifService.CreateWithMetadata(notif); err != nil {
			c.LogWarnIfEnabled("Failed to send migration start notification",
				logger.Error(err), logger.String("path", path), logger.String("ip", ip))
		}
	}

	return ctx.JSON(http.StatusOK, MigrationActionResponse{
		Success: true,
		Message: fmt.Sprintf("Migration started with %d records", totalRecords),
		State:   string(entities.MigrationStatusDualWrite),
	})
}

// rejectInterruptedStart answers a start request that the server shutdown
// interrupted. It is not a failed check, so it does not use the preFlightFailed
// key, which names a check and makes the page run the checks again.
func (c *Handler) rejectInterruptedStart(ctx echo.Context, cause error) error {
	err := fmt.Errorf("%s: %w", msgPreflightInterrupted, cause)
	c.LogErrorIfEnabled("Migration start interrupted by shutdown",
		logger.Error(err), logger.String("path", ctx.Request().URL.Path), logger.String("ip", ctx.RealIP()))
	return c.HandleErrorWithKey(ctx, err, "The server is stopping", http.StatusServiceUnavailable, notification.MsgErrMigrationStartFailed, nil)
}

// PauseMigration handles POST /api/v2/system/database/migration/pause
func (c *Handler) PauseMigration(ctx echo.Context) error {
	// Snapshot state under lock for thread-safety
	sm := getStateManager()
	worker := getMigrationWorker()

	return c.executeMigrationAction(ctx, &migrationActionParams{
		logStart:          "Pausing migration",
		workerAction:      func(context.Context) error { worker.Pause(); return nil },
		stateAction:       func() error { return sm.Pause() },
		logFailure:        "Failed to pause migration",
		logSuccess:        "Migration paused successfully",
		responseMessage:   "Migration paused",
		responseState:     entities.MigrationStatusPaused,
		notificationTitle: "Database Migration Paused",
		notificationBody:  "Database migration has been paused. You can resume it at any time from the Database settings.",
		titleKey:          notification.MsgMigrationPausedTitle,
		messageKey:        notification.MsgMigrationPausedMessage,

		worker: worker,
	})
}

// ResumeMigration handles POST /api/v2/system/database/migration/resume
func (c *Handler) ResumeMigration(ctx echo.Context) error {
	ip, path := ctx.RealIP(), ctx.Request().URL.Path
	c.LogInfoIfEnabled("Resuming migration", logger.String("path", path), logger.String("ip", ip))

	// Snapshot state under lock for thread-safety
	sm := getStateManager()
	worker := getMigrationWorker()

	// Resume the state
	if err := sm.Resume(); err != nil {
		c.LogErrorIfEnabled("Failed to resume migration",
			logger.Error(err), logger.String("path", path), logger.String("ip", ip))
		return c.HandleErrorWithKey(ctx, err, "Failed to resume migration", http.StatusConflict, notification.MsgErrMigrationResumeFailed, nil)
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
				c.LogWarnIfEnabled("Failed to restart migration worker",
					logger.Error(err), logger.String("path", path), logger.String("ip", ip))
			}
		}
	}

	// Clear any previous error
	if err := sm.ClearError(); err != nil {
		c.LogWarnIfEnabled("Failed to clear error message",
			logger.Error(err), logger.String("path", path), logger.String("ip", ip))
	}

	c.LogInfoIfEnabled("Migration resumed successfully",
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

// RetryValidation handles POST /api/v2/system/database/migration/retry-validation
func (c *Handler) RetryValidation(ctx echo.Context) error {
	ip, path := ctx.RealIP(), ctx.Request().URL.Path
	c.LogInfoIfEnabled("Retrying migration validation", logger.String("path", path), logger.String("ip", ip))

	sm := getStateManager()
	worker := getMigrationWorker()

	// Transition FAILED → VALIDATING
	if err := sm.RetryValidation(); err != nil {
		c.LogErrorIfEnabled("Failed to retry validation",
			logger.Error(err), logger.String("path", path), logger.String("ip", ip))
		return c.HandleError(ctx, err, "Failed to retry validation", http.StatusConflict)
	}

	// Resume or restart the worker so it picks up the VALIDATING state
	if worker != nil {
		if worker.IsPaused() {
			worker.Resume()
		} else if !worker.IsRunning() {
			workerCtx, workerCancel := context.WithCancel(context.Background())
			SetMigrationWorkerCancel(workerCancel)
			if err := worker.Start(workerCtx); err != nil {
				workerCancel()
				c.LogWarnIfEnabled("Failed to restart migration worker",
					logger.Error(err), logger.String("path", path), logger.String("ip", ip))
			}
		}
	}

	// Clear any previous error
	if err := sm.ClearError(); err != nil {
		c.LogWarnIfEnabled("Failed to clear error message",
			logger.Error(err), logger.String("path", path), logger.String("ip", ip))
	}

	c.LogInfoIfEnabled("Migration validation retry initiated",
		logger.String("path", path), logger.String("ip", ip))

	return ctx.JSON(http.StatusOK, MigrationActionResponse{
		Success: true,
		Message: "Validation retry initiated",
		State:   string(entities.MigrationStatusValidating),
	})
}

// isMigrationCompleted reports whether the migration state is COMPLETED. A state
// read error counts as not completed.
func isMigrationCompleted(sm *datastoreV2.StateManager) bool {
	state, err := sm.GetState()
	return err == nil && state.State == entities.MigrationStatusCompleted
}

// CancelMigration handles POST /api/v2/system/database/migration/cancel
func (c *Handler) CancelMigration(ctx echo.Context) error {
	ip, path := ctx.RealIP(), ctx.Request().URL.Path
	c.LogInfoIfEnabled("Cancelling migration", logger.String("path", path), logger.String("ip", ip))

	// Snapshot state under lock for thread-safety
	sm := getStateManager()
	worker := getMigrationWorker()

	// Stop the worker first and wait for it, so its last batch cannot write
	// after the state is reset. A stop that times out leaves the state untouched.
	// A COMPLETED migration cannot be cancelled, so its tail sync worker is left
	// running and sm.Cancel rejects the request below.
	if worker != nil && !isMigrationCompleted(sm) {
		stopCtx, stopCancel := context.WithTimeout(ctx.Request().Context(), migrationWorkerStopTimeout)
		defer stopCancel()
		if err := worker.Abandon(stopCtx); err != nil {
			c.LogErrorIfEnabled("Migration worker did not stop in time",
				logger.Error(err), logger.String("path", path), logger.String("ip", ip))
			return c.HandleError(ctx, err, msgMigrationWorkerStopping, http.StatusConflict)
		}
	}

	// Snapshot progress before cancel resets counters
	var migratedSoFar, snapshotTotal int64
	if state, stateErr := sm.GetState(); stateErr == nil {
		migratedSoFar = state.MigratedRecords
		snapshotTotal = state.TotalRecords
	}

	// Cancel the state
	if err := sm.Cancel(); err != nil {
		c.LogErrorIfEnabled("Failed to cancel migration",
			logger.Error(err), logger.String("path", path), logger.String("ip", ip))
		return c.HandleError(ctx, err, "Failed to cancel migration", http.StatusConflict)
	}

	// The state moved on, so a stored passing result no longer describes it.
	c.prereqRunner().reset()

	// Clear dirty IDs since we're cancelling
	if err := sm.ClearDirtyIDs(); err != nil {
		c.LogWarnIfEnabled("Failed to clear dirty IDs",
			logger.Error(err), logger.String("path", path), logger.String("ip", ip))
	}

	c.LogInfoIfEnabled("Migration cancelled successfully",
		logger.String("path", path), logger.String("ip", ip))

	// Report cancellation to telemetry
	if mt := getMigrationTelemetry(); mt != nil {
		mt.ReportCancelled(migratedSoFar, snapshotTotal)
	}

	// Send notification that migration was cancelled
	if notifService := c.getNotificationService(); notifService != nil {
		// Build notification fully before broadcast to ensure SSE subscribers see translation keys
		notif := notification.NewNotification(
			notification.TypeSystem,
			notification.PriorityMedium,
			"Database Migration Cancelled",
			"Database migration has been cancelled. You can start a new migration at any time from the Database settings.",
		).WithComponent("database").
			WithTitleKey(notification.MsgMigrationCancelledTitle, nil).
			WithMessageKey(notification.MsgMigrationCancelledMessage, nil)

		if err := notifService.CreateWithMetadata(notif); err != nil {
			c.LogWarnIfEnabled("Failed to send migration cancel notification",
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
// Rolls back a completed migration to use the legacy database. A running worker
// is abandoned (no final drain) and waited for before the state is reset; if it
// does not exit within migrationWorkerStopTimeout the request fails with 409 and
// the state is left unchanged. The state reset also clears the dirty-ID set and
// drops the stored prerequisites result.
func (c *Handler) RollbackMigration(ctx echo.Context) error {
	// Snapshot state under lock for thread-safety
	sm := getStateManager()
	worker := getMigrationWorker()

	return c.executeMigrationAction(ctx, &migrationActionParams{
		logStart:     "Rolling back migration",
		workerAction: worker.Abandon,
		stateAction: func() error {
			if err := sm.Rollback(); err != nil {
				return err
			}
			// The state moved on, so a stored passing result no longer describes it.
			c.prereqRunner().reset()
			return nil
		},
		logFailure:      "Failed to rollback migration",
		logSuccess:      "Migration rolled back successfully",
		responseMessage: "Migration rolled back to legacy database",
		responseState:   entities.MigrationStatusIdle,
		worker:          worker,
	})
}

// prerequisitesNotReady answers a start that has no usable prerequisites run.
func (c *Handler) prerequisitesNotReady(ctx echo.Context) error {
	return c.HandleErrorWithKey(ctx, errPrerequisitesNotReady, "Run the pre-migration checks first",
		http.StatusConflict, notification.MsgErrMigrationPrereqNotReady, nil)
}

// firstBlockingCheck returns the first critical check that failed or errored, or nil.
func firstBlockingCheck(checks []PrerequisiteCheck) *PrerequisiteCheck {
	for i := range checks {
		if isBlockingCheck(&checks[i]) {
			return &checks[i]
		}
	}
	return nil
}

// isBlockingCheck reports whether a check blocks the migration: a critical check
// that failed or errored.
func isBlockingCheck(check *PrerequisiteCheck) bool {
	return check.Severity == CheckSeverityCritical &&
		(check.Status == CheckStatusFailed || check.Status == CheckStatusError)
}

// storedRunFailed answers a start whose stored prerequisites run has a failed
// critical check, naming that check.
func (c *Handler) storedRunFailed(ctx echo.Context, checks []PrerequisiteCheck, path, ip string) error {
	if check := firstBlockingCheck(checks); check != nil {
		return c.preflightFailed(ctx, check, path, ip)
	}
	return c.prerequisitesNotReady(ctx)
}

// preflightFailed answers a start blocked by check with a 400 that names it.
func (c *Handler) preflightFailed(ctx echo.Context, check *PrerequisiteCheck, path, ip string) error {
	err := fmt.Errorf("prerequisite check '%s' failed: %s", check.Name, check.Message)
	c.LogErrorIfEnabled("Pre-flight checks failed",
		logger.Error(err), logger.String("path", path), logger.String("ip", ip))
	return c.HandleErrorWithKey(ctx, err, "Pre-flight checks failed", http.StatusBadRequest,
		notification.MsgErrMigrationPreFlight, map[string]any{preFlightCheckParam: check.Name})
}

// abandonedStartResponse ends a start that was abandoned. A client that is gone
// gets nothing; one whose server is stopping gets the shutdown answer; one that is
// still connected after the start's own work outlasted the preflight limit gets a
// 504. The stored prerequisites are still valid, so the answer is not "run the
// checks again", and it wraps the deadline error, which the error handler does not
// report to telemetry.
func (c *Handler) abandonedStartResponse(ctx echo.Context, path, ip string) error {
	c.LogInfoIfEnabled("Client went away, the server is stopping or the preflight timed out before the migration started",
		logger.String("path", path), logger.String("ip", ip))
	select {
	case <-ctx.Request().Context().Done():
		return nil
	default:
	}
	if err := c.lifecycleContext().Err(); err != nil {
		return c.rejectInterruptedStart(ctx, err)
	}
	return c.HandleErrorWithKey(ctx, fmt.Errorf("start preflight: %w", context.DeadlineExceeded),
		"The migration start took too long", http.StatusGatewayTimeout,
		notification.MsgErrMigrationStartFailed, nil)
}

// criticalPrerequisiteSteps lists the checks that must pass before migration,
// in run order. In the MySQL branch the server version check runs first, so the
// telemetry tag names it when the server is unsupported. sink, which may be
// nil, receives the legacy record count from the record_count step.
func (c *Handler) criticalPrerequisiteSteps(sink *recordCountSink) []prereqStep {
	steps := []prereqStep{
		singleCheckStep(checkIDStateIdle, checkNameStateIdle, false, func(context.Context) PrerequisiteCheck { return c.checkStateIdle() }),
		singleCheckStep(checkIDDiskSpace, checkNameDiskSpace, false, func(context.Context) PrerequisiteCheck { return c.checkDiskSpace() }),
		singleCheckStep(checkIDLegacyAccessible, checkNameLegacyAccessible, true, c.checkLegacyAccessible),
	}

	// Database-specific critical checks
	if c.isUsingMySQL() {
		steps = append(steps,
			singleCheckStep(checkIDMySQLServerVersion, checkNameMySQLServerVersion, false, c.checkMySQLServerVersion),
			singleCheckStep(checkIDMySQLCharset, checkNameMySQLCharset, false, c.checkMySQLCharset),
			singleCheckStep(checkIDMySQLTableHealth, checkNameMySQLTableHealth, true, c.checkMySQLTableHealth),
			singleCheckStep(checkIDMySQLPermissions, checkNameMySQLPermissions, true, c.checkMySQLPermissions),
		)
	} else {
		steps = append(steps,
			singleCheckStep(checkIDSQLiteIntegrity, checkNameSQLiteIntegrity, true, c.checkSQLiteIntegrity),
			singleCheckStep(checkIDWritePermission, checkNameWritePermission, false, func(context.Context) PrerequisiteCheck { return c.checkWritePermission() }),
		)
	}

	// Record count is also critical
	steps = append(steps, singleCheckStep(checkIDRecordCount, checkNameRecordCount, true, func(ctx context.Context) PrerequisiteCheck {
		count, check := c.countLegacyRecords(ctx)
		if sink != nil && check.Status == CheckStatusPassed {
			sink.count, sink.known = count, true
		}
		return check
	}))
	return steps
}

// runCriticalPrerequisiteChecks runs the critical checks that must pass before
// migration, in order, and returns their results. A step marked reuseAtStart
// takes its result from reused (a missing entry is an error check, so the gate
// fails closed); a nil reused runs every step. A step that ignores ctx is
// abandoned when ctx ends and no later step starts, so the result is then short
// and the caller must check ctx. It records the outcome in the migration_prereq
// telemetry tag unless ctx ended, so an abandoned start does not overwrite the
// tag with a cancellation error.
func (c *Handler) runCriticalPrerequisiteChecks(ctx context.Context, reused map[string]PrerequisiteCheck) []PrerequisiteCheck {
	steps := c.criticalPrerequisiteSteps(nil)
	checks := make([]PrerequisiteCheck, 0, len(steps))
	for i := range steps {
		step := &steps[i]
		if ctx.Err() != nil {
			break
		}
		if reused == nil || !step.reuseAtStart {
			// runStep abandons a check that ignores ctx, so a stalled mount cannot hold the start
			checks = append(checks, runStep(ctx, step)...)
			continue
		}
		if check, ok := reused[step.id]; ok {
			checks = append(checks, check)
			continue
		}
		checks = append(checks, PrerequisiteCheck{
			ID: step.id, Name: step.name, Status: CheckStatusError, Severity: CheckSeverityCritical,
			Message: msgNoStoredResult,
		})
	}

	if ctx.Err() == nil {
		telemetry.SetMigrationPrereqTag(prerequisiteTagValue(checks))
	}

	return checks
}

// migrationActionParams defines parameters for a migration action.
type migrationActionParams struct {
	logStart          string
	workerAction      func(context.Context) error
	stateAction       func() error
	logFailure        string
	logSuccess        string
	responseMessage   string
	responseState     entities.MigrationStatus
	notificationTitle string // Optional: if set, sends a notification
	notificationBody  string
	titleKey          string            // Optional: i18n translation key for title
	messageKey        string            // Optional: i18n translation key for message
	worker            *migration.Worker // Thread-safe snapshot
}

// executeMigrationAction handles common migration action logic.
// The worker action runs first when the worker is running, bounded by
// migrationWorkerStopTimeout; if it fails the state action is skipped and the
// request fails with 409.
// All callers are behind the requireMigrationStateManager middleware.
func (c *Handler) executeMigrationAction(ctx echo.Context, params *migrationActionParams) error {
	ip, path := ctx.RealIP(), ctx.Request().URL.Path
	c.LogInfoIfEnabled(params.logStart, logger.String("path", path), logger.String("ip", ip))

	// Perform worker action if worker is running
	if params.worker != nil && params.worker.IsRunning() {
		stopCtx, stopCancel := context.WithTimeout(ctx.Request().Context(), migrationWorkerStopTimeout)
		defer stopCancel()
		if err := params.workerAction(stopCtx); err != nil {
			c.LogErrorIfEnabled(params.logFailure,
				logger.Error(err), logger.String("path", path), logger.String("ip", ip))
			return c.HandleError(ctx, err, msgMigrationWorkerStopping, http.StatusConflict)
		}
	}

	// Perform state action
	if err := params.stateAction(); err != nil {
		c.LogErrorIfEnabled(params.logFailure,
			logger.Error(err), logger.String("path", path), logger.String("ip", ip))
		return c.HandleError(ctx, err, params.logFailure, http.StatusConflict)
	}

	c.LogInfoIfEnabled(params.logSuccess,
		logger.String("path", path), logger.String("ip", ip))

	// Send notification if configured
	if params.notificationTitle != "" {
		if notifService := c.getNotificationService(); notifService != nil {
			// Build notification fully before broadcast to ensure SSE subscribers see translation keys
			notif := notification.NewNotification(
				notification.TypeSystem,
				notification.PriorityMedium,
				params.notificationTitle,
				params.notificationBody,
			).WithComponent("database")

			if params.titleKey != "" {
				notif.WithTitleKey(params.titleKey, nil)
			}
			if params.messageKey != "" {
				notif.WithMessageKey(params.messageKey, nil)
			}

			if err := notifService.CreateWithMetadata(notif); err != nil {
				c.LogWarnIfEnabled("Failed to send migration notification",
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
