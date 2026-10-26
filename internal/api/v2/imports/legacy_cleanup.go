// internal/api/v2/imports/legacy_cleanup.go
package importsapi

import (
	"context"
	"fmt"
	"net/http"
	"os"
	"slices"
	"strings"
	"sync"
	"time"

	"github.com/labstack/echo/v4"
	datastoreV2 "github.com/tphakala/birdnet-go/internal/datastore/v2"
	"github.com/tphakala/birdnet-go/internal/datastore/v2/entities"
	"github.com/tphakala/birdnet-go/internal/formatutil"
	"github.com/tphakala/birdnet-go/internal/logger"
	"github.com/tphakala/birdnet-go/internal/notification"
	"gorm.io/gorm"
)

// LegacyStatusResponse represents legacy database status for cleanup UI.
type LegacyStatusResponse struct {
	Exists       bool              `json:"exists"`
	CanCleanup   bool              `json:"can_cleanup"`
	Reason       string            `json:"reason,omitempty"`
	ErrorKey     string            `json:"error_key,omitempty"`
	SizeBytes    int64             `json:"size_bytes"`
	TotalRecords int64             `json:"total_records"`
	LastModified *time.Time        `json:"last_modified,omitempty"`
	Location     string            `json:"location"`
	Tables       []LegacyTableInfo `json:"tables"`
}

// LegacyTableInfo provides details about a legacy table (MySQL).
type LegacyTableInfo struct {
	Name      string `json:"name"`
	SizeBytes int64  `json:"size_bytes"`
	RowCount  int64  `json:"row_count"`
}

// CleanupActionResponse represents the response for cleanup actions.
type CleanupActionResponse struct {
	Success  bool   `json:"success"`
	Message  string `json:"message"`
	ErrorKey string `json:"error_key,omitempty"`
}

// Cleanup state constants.
const (
	CleanupStateIdle       = "idle"
	CleanupStateInProgress = "in_progress"
	CleanupStateCompleted  = "completed"
	CleanupStateFailed     = "failed"
)

// legacyOnlyTables are legacy-schema tables that no v2 schema (bare or
// prefixed) ever has, in dependency order (children first).
var legacyOnlyTables = []string{
	"note_reviews",
	"note_comments",
	"note_locks",
	"results",
	"notes",
}

// legacySharedTables are legacy-schema tables whose names are also the live
// tables of an unprefixed (bare) v2 schema, because the v2 entities have no
// TableName overrides. They are legacy only when the active v2 schema carries a
// table prefix and no bare v2 schema exists beside it.
var legacySharedTables = []string{
	"hourly_weathers",
	"daily_events",
	"image_caches",
	"dynamic_thresholds",
	"threshold_events",
	"notification_histories",
}

// bareV2MarkerTables are bare v2 tables the legacy schema never has. Any of them
// in the database means an unprefixed v2 schema lives there. "detections" is the
// startup probe's own signal; "labels" is the foreign key parent of the bare
// "image_caches" and "notification_histories", so it survives a half-deleted
// bare schema; both spellings of the migration state table are listed because
// earlier releases used the singular name.
var bareV2MarkerTables = []string{
	"detections",
	"labels",
	"migration_states",
	"migration_state",
}

// mysqlLegacyCandidates returns the legacy table names that may be dropped for a
// v2 schema with the given table prefix, in drop order. A bare prefix means the
// legacy-named tables are live v2 tables, so nothing is offered. Beside bare v2
// tables the shared names are withheld. The result is a fresh slice.
func mysqlLegacyCandidates(tablePrefix string, bareV2Present bool) []string {
	if tablePrefix == "" {
		return nil
	}
	if bareV2Present {
		return slices.Clone(legacyOnlyTables)
	}
	return slices.Concat(legacyOnlyTables, legacySharedTables)
}

// legacyCleanupRefusal describes why the MySQL legacy cleanup is not offered.
type legacyCleanupRefusal struct {
	exists   bool
	reason   string
	errorKey string
}

// legacyTableListing is one row of the information_schema table listing.
type legacyTableListing struct {
	Name string
	Size int64
}

// mysqlCleanupConn returns the connection the MySQL cleanup runs on. It is
// the repository's GORM connection, bound to ctx, and a refusal when the
// repository does not provide one.
func (c *Handler) mysqlCleanupConn(ctx context.Context) (*gorm.DB, *legacyCleanupRefusal) {
	provider, ok := c.Repo.(gormDBProvider)
	if !ok {
		return nil, &legacyCleanupRefusal{reason: "Cannot access database connection", errorKey: notification.MsgErrCleanupAccessFailed}
	}
	db := provider.GetDB()
	if db == nil {
		return nil, &legacyCleanupRefusal{reason: "Cannot access database connection", errorKey: notification.MsgErrCleanupAccessFailed}
	}
	return db.WithContext(ctx), nil
}

// resolveMySQLLegacyTables decides which legacy tables the MySQL cleanup may
// list and drop, from the v2 manager's actual schema. It is the single decision
// shared by the status call, the POST pre-check and the background drop, and it
// fails closed: anything it cannot read refuses. db carries the request or
// cleanup context and is the connection the caller will also run its DROPs on.
// withCounts adds an exact row count per table, which only the status call
// reports; the POST pre-check and the drop skip it because COUNT(*) can scan a
// whole large table.
func (c *Handler) resolveMySQLLegacyTables(ctx context.Context, db *gorm.DB, withCounts bool) ([]LegacyTableInfo, *legacyCleanupRefusal) {
	accessFailed := func(reason string) *legacyCleanupRefusal {
		return &legacyCleanupRefusal{reason: reason, errorKey: notification.MsgErrCleanupAccessFailed}
	}

	mgr := c.V2Manager
	if mgr == nil || !mgr.IsMySQL() || mgr.DB() == nil {
		return nil, accessFailed("MySQL v2 database manager is not available")
	}

	prefix := mgr.TablePrefix()
	if prefix == "" {
		return nil, &legacyCleanupRefusal{reason: "No legacy tables found", errorKey: notification.MsgErrCleanupNoLegacyTables}
	}

	migrationState, err := datastoreV2.NewStateManager(mgr.DB().WithContext(ctx)).GetState()
	if err != nil {
		c.LogWarnIfEnabled("Legacy cleanup: failed to read migration state", logger.Error(err))
		return nil, accessFailed("Cannot read migration state")
	}
	if migrationState.State != entities.MigrationStatusCompleted {
		return nil, &legacyCleanupRefusal{
			exists:   true,
			reason:   "Migration has not completed, cleanup is not available",
			errorKey: notification.MsgErrCleanupRestartRequired,
		}
	}
	if migrationState.RelatedDataError != "" {
		return nil, &legacyCleanupRefusal{
			exists:   true,
			reason:   "Migration recorded an error in related data, cleanup is not allowed for safety",
			errorKey: notification.MsgErrCleanupSafetyCheck,
		}
	}

	var listing []legacyTableListing
	err = db.Raw(`
		SELECT table_name AS name, COALESCE(data_length, 0) + COALESCE(index_length, 0) AS size
		FROM information_schema.tables
		WHERE table_schema = DATABASE() AND table_type = 'BASE TABLE'`).Scan(&listing).Error
	if err != nil {
		c.LogWarnIfEnabled("Legacy cleanup: failed to list tables", logger.Error(err))
		return nil, accessFailed("Cannot list database tables")
	}
	// MySQL 8 and MariaDB differ in the case of returned names.
	sizes := make(map[string]int64, len(listing))
	for _, row := range listing {
		sizes[strings.ToLower(row.Name)] = row.Size
	}

	bareV2Present := slices.ContainsFunc(bareV2MarkerTables, func(name string) bool {
		_, ok := sizes[name]
		return ok
	})

	var tables []LegacyTableInfo
	for _, name := range mysqlLegacyCandidates(prefix, bareV2Present) {
		size, ok := sizes[name]
		if !ok {
			continue
		}
		info := LegacyTableInfo{Name: name, SizeBytes: size}
		if withCounts {
			if err := db.Raw("SELECT COUNT(*) FROM `" + name + "`").Scan(&info.RowCount).Error; err != nil {
				if ctx.Err() != nil {
					return nil, accessFailed("Cleanup was cancelled")
				}
				// The count is informational and never decides what is dropped.
				c.LogWarnIfEnabled("Legacy cleanup: failed to query row count",
					logger.String("table", name),
					logger.Error(err))
				info.RowCount = 0
			}
		}
		tables = append(tables, info)
	}
	if len(tables) == 0 {
		return nil, &legacyCleanupRefusal{reason: "No legacy tables found", errorKey: notification.MsgErrCleanupNoLegacyTables}
	}
	return tables, nil
}

// sumLegacyTableSizes returns the total size of the resolved tables.
func sumLegacyTableSizes(tables []LegacyTableInfo) int64 {
	var total int64
	for _, t := range tables {
		total += t.SizeBytes
	}
	return total
}

// CleanupStatus tracks the state of legacy database cleanup.
// Thread-safe for concurrent access.
type CleanupStatus struct {
	mu              sync.RWMutex
	state           string
	errorMsg        string
	tablesRemaining []string
	spaceReclaimed  int64
}

// NewCleanupStatus creates a new cleanup status tracker initialized to idle.
func NewCleanupStatus() *CleanupStatus {
	return &CleanupStatus{
		state: CleanupStateIdle,
	}
}

// Get returns the current cleanup state safely.
// Returns a copy of tablesRemaining to prevent callers from observing concurrent mutations.
func (cs *CleanupStatus) Get() (state, errMsg string, remaining []string, reclaimed int64) {
	cs.mu.RLock()
	defer cs.mu.RUnlock()
	remainingCopy := slices.Clone(cs.tablesRemaining)
	return cs.state, cs.errorMsg, remainingCopy, cs.spaceReclaimed
}

// Set sets the cleanup state safely.
func (cs *CleanupStatus) Set(state, errMsg string, remaining []string, reclaimed int64) {
	cs.mu.Lock()
	defer cs.mu.Unlock()
	cs.state = state
	cs.errorMsg = errMsg
	cs.tablesRemaining = remaining
	cs.spaceReclaimed = reclaimed
}

// Reset resets the cleanup state to idle.
func (cs *CleanupStatus) Reset() {
	cs.Set(CleanupStateIdle, "", nil, 0)
}

// TryStart atomically checks if cleanup can start and sets state to in_progress.
// Returns true if cleanup was started, false if already in progress.
// This prevents race conditions between concurrent cleanup requests.
func (cs *CleanupStatus) TryStart() bool {
	cs.mu.Lock()
	defer cs.mu.Unlock()
	if cs.state == CleanupStateInProgress {
		return false
	}
	cs.state = CleanupStateInProgress
	cs.errorMsg = ""
	cs.tablesRemaining = nil
	cs.spaceReclaimed = 0
	return true
}

// RegisterLegacyCleanupRoutes registers the legacy cleanup API routes on the supplied v2 group.
func (c *Handler) RegisterLegacyCleanupRoutes(g *echo.Group) {
	c.LogInfoIfEnabled("Initializing legacy cleanup routes")

	// Initialize cleanup status tracker
	c.cleanupStatus = NewCleanupStatus()

	// Create legacy API group under system/database
	legacyGroup := g.Group("/system/database/legacy")

	// Get the appropriate auth middleware
	authMiddleware := c.AuthMiddleware

	// Create auth-protected group
	protectedGroup := legacyGroup.Group("", authMiddleware)

	// Legacy status and cleanup routes
	protectedGroup.GET("/status", c.GetLegacyStatus)
	protectedGroup.POST("/cleanup", c.StartLegacyCleanup)

	c.LogInfoIfEnabled("Legacy cleanup routes initialized successfully")
}

// GetLegacyStatus handles GET /api/v2/system/database/legacy/status
// Returns information about the legacy database for the cleanup UI.
func (c *Handler) GetLegacyStatus(ctx echo.Context) error {
	ip, path := ctx.RealIP(), ctx.Request().URL.Path
	c.LogInfoIfEnabled("Getting legacy database status", logger.String("path", path), logger.String("ip", ip))

	response := LegacyStatusResponse{
		Tables: []LegacyTableInfo{},
	}

	// Check if using MySQL
	if c.isUsingMySQL() {
		return c.getLegacyStatusMySQL(ctx, &response)
	}

	return c.getLegacyStatusSQLite(ctx, &response)
}

// getLegacyStatusSQLite handles legacy status for SQLite deployments.
func (c *Handler) getLegacyStatusSQLite(ctx echo.Context, response *LegacyStatusResponse) error {
	legacyPath := c.CurrentSettings().Output.SQLite.Path
	response.Location = legacyPath

	// Check if legacy file exists
	fileInfo, err := os.Stat(legacyPath)
	if os.IsNotExist(err) {
		response.Exists = false
		response.CanCleanup = false
		response.Reason = "No legacy database found"
		response.ErrorKey = notification.MsgErrCleanupNoLegacyDB
		return ctx.JSON(http.StatusOK, response)
	}
	if err != nil {
		response.Exists = false
		response.CanCleanup = false
		response.Reason = fmt.Sprintf("Cannot access legacy database: %v", err)
		response.ErrorKey = notification.MsgErrCleanupAccessFailed
		return ctx.JSON(http.StatusOK, response)
	}

	response.Exists = true
	response.SizeBytes = fileInfo.Size()
	modTime := fileInfo.ModTime()
	response.LastModified = &modTime

	// Add WAL and SHM file sizes if they exist
	if walInfo, err := os.Stat(legacyPath + "-wal"); err == nil {
		response.SizeBytes += walInfo.Size()
	}
	if shmInfo, err := os.Stat(legacyPath + "-shm"); err == nil {
		response.SizeBytes += shmInfo.Size()
	}

	// Check if we're in v2-only mode (required for cleanup)
	if !isV2OnlyMode {
		response.CanCleanup = false
		response.Reason = "Application must be restarted after migration before cleanup is available"
		response.ErrorKey = notification.MsgErrCleanupRestartRequired
		return ctx.JSON(http.StatusOK, response)
	}

	// CRITICAL SAFETY CHECK: Ensure target is not a V2 database
	if datastoreV2.CheckSQLiteHasV2Schema(legacyPath) {
		response.CanCleanup = false
		response.Reason = "Target file appears to be a V2 database - cleanup not allowed for safety"
		response.ErrorKey = notification.MsgErrCleanupSafetyCheck
		return ctx.JSON(http.StatusOK, response)
	}

	response.CanCleanup = true
	response.Tables = []LegacyTableInfo{
		{Name: "notes"},
		{Name: "results"},
		{Name: "note_reviews"},
		{Name: "note_comments"},
		{Name: "note_locks"},
		{Name: "daily_events"},
		{Name: "hourly_weathers"},
		{Name: "image_caches"},
		{Name: "dynamic_thresholds"},
		{Name: "threshold_events"},
		{Name: "notification_histories"},
	}

	return ctx.JSON(http.StatusOK, response)
}

// getLegacyStatusMySQL handles legacy status for MySQL deployments.
func (c *Handler) getLegacyStatusMySQL(ctx echo.Context, response *LegacyStatusResponse) error {
	response.Location = c.CurrentSettings().Output.MySQL.Database

	// Check if we're in v2-only mode (required for cleanup)
	if !isV2OnlyMode {
		response.Exists = true // Assume exists for MySQL
		response.CanCleanup = false
		response.Reason = "Application must be restarted after migration before cleanup is available"
		response.ErrorKey = notification.MsgErrCleanupRestartRequired
		return ctx.JSON(http.StatusOK, response)
	}

	// Bind the request context so the schema queries below abort if the
	// client disconnects, rather than running unbounded on the request thread.
	reqCtx := ctx.Request().Context()
	db, refusal := c.mysqlCleanupConn(reqCtx)
	if refusal == nil {
		var tables []LegacyTableInfo
		tables, refusal = c.resolveMySQLLegacyTables(reqCtx, db, true)
		if refusal == nil {
			var totalRows int64
			for _, t := range tables {
				totalRows += t.RowCount
			}
			response.Exists = true
			response.CanCleanup = true
			response.SizeBytes = sumLegacyTableSizes(tables)
			response.TotalRecords = totalRows
			response.Tables = tables
			return ctx.JSON(http.StatusOK, response)
		}
	}

	response.Exists = refusal.exists
	response.CanCleanup = false
	response.Reason = refusal.reason
	response.ErrorKey = refusal.errorKey
	return ctx.JSON(http.StatusOK, response)
}

// StartLegacyCleanup handles POST /api/v2/system/database/legacy/cleanup
// Initiates asynchronous legacy database cleanup. For MySQL the tables are
// resolved first and a refusal answers 400 without starting anything; the
// background drop resolves again before it drops.
func (c *Handler) StartLegacyCleanup(ctx echo.Context) error {
	ip, path := ctx.RealIP(), ctx.Request().URL.Path
	c.LogInfoIfEnabled("Starting legacy database cleanup", logger.String("path", path), logger.String("ip", ip))

	// Check if we're in v2-only mode (check before trying to start)
	if !isV2OnlyMode {
		return ctx.JSON(http.StatusBadRequest, CleanupActionResponse{
			Success:  false,
			Message:  "Cannot cleanup: Application must be restarted after migration",
			ErrorKey: notification.MsgErrCleanupRestartNeeded,
		})
	}

	// Resolve the MySQL tables before scheduling anything, so a refusal is a
	// synchronous answer and leaves the cleanup state idle.
	usingMySQL := c.isUsingMySQL()
	var mysqlSize int64
	if usingMySQL {
		reqCtx := ctx.Request().Context()
		db, refusal := c.mysqlCleanupConn(reqCtx)
		if refusal == nil {
			var tables []LegacyTableInfo
			tables, refusal = c.resolveMySQLLegacyTables(reqCtx, db, false)
			mysqlSize = sumLegacyTableSizes(tables)
		}
		if refusal != nil {
			return ctx.JSON(http.StatusBadRequest, CleanupActionResponse{
				Success:  false,
				Message:  refusal.reason,
				ErrorKey: refusal.errorKey,
			})
		}
	}

	// Atomically check and set cleanup state to prevent race conditions
	if !c.cleanupStatus.TryStart() {
		return ctx.JSON(http.StatusConflict, CleanupActionResponse{
			Success:  false,
			Message:  "Cleanup already in progress",
			ErrorKey: notification.MsgErrCleanupAlreadyRunning,
		})
	}

	// Get size before deletion for reporting
	sizeBytes := mysqlSize
	if !usingMySQL {
		sizeBytes = c.getSQLiteLegacySize()
	}

	// Start async cleanup using WaitGroup for proper lifecycle management
	c.Go(func() {

		cleanupCtx := c.Context()
		if cleanupCtx == nil {
			cleanupCtx = context.Background()
		}

		var err error
		var remaining []string

		if c.isUsingMySQL() {
			remaining, err = c.cleanupMySQLLegacy(cleanupCtx)
		} else {
			err = c.cleanupSQLiteLegacy(cleanupCtx)
		}

		if err != nil {
			c.LogErrorIfEnabled("Legacy cleanup failed", logger.Error(err))
			c.cleanupStatus.Set(CleanupStateFailed, err.Error(), remaining, 0)
			c.sendCleanupNotification(false, 0, err.Error())
		} else {
			c.LogInfoIfEnabled("Legacy cleanup completed successfully",
				logger.Int64("space_reclaimed_bytes", sizeBytes),
				logger.String("space_reclaimed", formatutil.Bytes(sizeBytes)))
			c.cleanupStatus.Set(CleanupStateCompleted, "", nil, sizeBytes)
			c.sendCleanupNotification(true, sizeBytes, "")
		}
	})

	c.LogInfoIfEnabled("Legacy cleanup started", logger.String("path", path), logger.String("ip", ip))

	return ctx.JSON(http.StatusOK, CleanupActionResponse{
		Success: true,
		Message: "Legacy database cleanup started",
	})
}

// cleanupSQLiteLegacy deletes the legacy SQLite database files.
func (c *Handler) cleanupSQLiteLegacy(ctx context.Context) error {
	// Check for cancellation before starting
	select {
	case <-ctx.Done():
		return ctx.Err()
	default:
	}

	legacyPath := c.CurrentSettings().Output.SQLite.Path

	c.LogInfoIfEnabled("Starting SQLite legacy database cleanup",
		logger.String("path", legacyPath))

	// CRITICAL SAFETY CHECK: Ensure we're not deleting a V2 database
	if datastoreV2.CheckSQLiteHasV2Schema(legacyPath) {
		c.LogErrorIfEnabled("Safety check failed: target is a V2 database",
			logger.String("path", legacyPath))
		return fmt.Errorf("target file appears to be a V2 database - cleanup aborted for safety")
	}

	c.LogInfoIfEnabled("Safety check passed: confirmed legacy database",
		logger.String("path", legacyPath))

	// Delete main database file
	if err := os.Remove(legacyPath); err != nil && !os.IsNotExist(err) {
		return fmt.Errorf("failed to delete legacy database: %w", err)
	}
	c.LogInfoIfEnabled("Deleted legacy database file",
		logger.String("file", legacyPath))

	// Delete WAL and SHM files if they exist (ignore errors)
	walDeleted := os.Remove(legacyPath+"-wal") == nil
	shmDeleted := os.Remove(legacyPath+"-shm") == nil

	c.LogInfoIfEnabled("SQLite legacy cleanup completed",
		logger.String("path", legacyPath),
		logger.Bool("wal_deleted", walDeleted),
		logger.Bool("shm_deleted", shmDeleted))

	return nil
}

// cleanupMySQLLegacy drops the legacy tables the resolver allows. It resolves
// again, because the database may have changed since the POST was accepted, and
// drops nothing when the resolver refuses. On a failure or cancellation it
// returns the names not dropped.
func (c *Handler) cleanupMySQLLegacy(ctx context.Context) ([]string, error) {
	// Bind the cleanup context to the GORM session so the listing and the DROP
	// TABLE Exec below observe cancellation. Without this, an in-flight DROP
	// cannot abort on shutdown, stalling graceful shutdown and leaking the
	// cleanup goroutine.
	db, refusal := c.mysqlCleanupConn(ctx)
	if refusal == nil {
		var tables []LegacyTableInfo
		tables, refusal = c.resolveMySQLLegacyTables(ctx, db, false)
		if refusal == nil {
			return c.dropMySQLLegacyTables(ctx, db, tables)
		}
	}
	return nil, fmt.Errorf("legacy cleanup refused: %s", refusal.reason)
}

// dropMySQLLegacyTables drops the resolved tables in order and stops at the first
// error or cancellation, returning the names left behind.
func (c *Handler) dropMySQLLegacyTables(ctx context.Context, db *gorm.DB, tables []LegacyTableInfo) ([]string, error) {
	names := make([]string, len(tables))
	for i, t := range tables {
		names[i] = t.Name
	}
	c.LogInfoIfEnabled("Starting MySQL legacy tables cleanup",
		logger.Int("total_tables", len(names)))

	for i, tableName := range names {
		// Check for cancellation before each table
		select {
		case <-ctx.Done():
			c.LogWarnIfEnabled("MySQL cleanup cancelled",
				logger.Int("tables_dropped", i),
				logger.Int("tables_remaining", len(names)-i))
			return names[i:], ctx.Err()
		default:
		}

		// Names come only from the fixed legacy lists.
		if err := db.Exec("DROP TABLE IF EXISTS `" + tableName + "`").Error; err != nil {
			c.LogErrorIfEnabled("Failed to drop legacy table",
				logger.String("table", tableName),
				logger.Error(err))
			// Stop on first error, return remaining tables
			return names[i:], fmt.Errorf("failed to drop table %s: %w", tableName, err)
		}

		c.LogInfoIfEnabled("Dropped legacy table",
			logger.String("table", tableName))
	}

	c.LogInfoIfEnabled("MySQL legacy cleanup completed",
		logger.Int("tables_dropped", len(names)))

	return nil, nil
}

// getSQLiteLegacySize returns the total size of SQLite legacy files.
func (c *Handler) getSQLiteLegacySize() int64 {
	legacyPath := c.CurrentSettings().Output.SQLite.Path
	var totalSize int64

	if info, err := os.Stat(legacyPath); err == nil {
		totalSize += info.Size()
	}
	if info, err := os.Stat(legacyPath + "-wal"); err == nil {
		totalSize += info.Size()
	}
	if info, err := os.Stat(legacyPath + "-shm"); err == nil {
		totalSize += info.Size()
	}

	return totalSize
}

// sendCleanupNotification sends a notification about cleanup status.
func (c *Handler) sendCleanupNotification(success bool, spaceReclaimed int64, errMsg string) {
	notifService := c.getNotificationService()
	if notifService == nil {
		return
	}

	var title, body, titleKey, messageKey string
	var messageParams map[string]any
	var priority notification.Priority

	if success {
		title = "Legacy Database Cleanup Complete"
		body = fmt.Sprintf("Successfully removed legacy database. %s disk space reclaimed.",
			formatutil.Bytes(spaceReclaimed))
		priority = notification.PriorityMedium
		titleKey = notification.MsgCleanupCompleteTitle
		messageKey = notification.MsgCleanupCompleteMessage
		messageParams = map[string]any{"space": formatutil.Bytes(spaceReclaimed)}
	} else {
		title = "Legacy Database Cleanup Failed"
		body = fmt.Sprintf("Failed to remove legacy database: %s", errMsg)
		priority = notification.PriorityHigh
		titleKey = notification.MsgCleanupFailedTitle
		messageKey = notification.MsgCleanupFailedMessage
		messageParams = nil
	}

	// Build notification fully before broadcast to ensure SSE subscribers see translation keys
	notif := notification.NewNotification(
		notification.TypeSystem,
		priority,
		title,
		body,
	).WithComponent("database").
		WithTitleKey(titleKey, nil).
		WithMessageKey(messageKey, messageParams)

	if err := notifService.CreateWithMetadata(notif); err != nil {
		c.LogWarnIfEnabled("Failed to send cleanup notification", logger.Error(err))
	}
}
