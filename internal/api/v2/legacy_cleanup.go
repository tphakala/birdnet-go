// internal/api/v2/legacy_cleanup.go
package api

import (
	"context"
	"fmt"
	"net/http"
	"os"
	"sync"
	"time"

	"github.com/labstack/echo/v4"
	datastoreV2 "github.com/tphakala/birdnet-go/internal/datastore/v2"
	"github.com/tphakala/birdnet-go/internal/logger"
	"github.com/tphakala/birdnet-go/internal/notification"
)

// LegacyStatusResponse represents legacy database status for cleanup UI.
type LegacyStatusResponse struct {
	Exists       bool              `json:"exists"`
	CanCleanup   bool              `json:"can_cleanup"`
	Reason       string            `json:"reason,omitempty"`
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
	Success bool   `json:"success"`
	Message string `json:"message"`
}

// Cleanup state constants.
const (
	CleanupStateIdle       = "idle"
	CleanupStateInProgress = "in_progress"
	CleanupStateCompleted  = "completed"
	CleanupStateFailed     = "failed"
)

// Legacy tables to clean up (in dependency order - children first).
var legacyTables = []string{
	"note_reviews",
	"note_comments",
	"note_locks",
	"results",
	"notes",
	"hourly_weathers",
	"daily_events",
	"image_caches",
	"dynamic_thresholds",
	"threshold_events",
	"notification_histories",
}

// Cleanup state (in-memory, not persisted).
var (
	cleanupState           = CleanupStateIdle
	cleanupError           = ""
	cleanupTablesRemaining []string
	cleanupSpaceReclaimed  int64
	cleanupMu              sync.RWMutex
)

// getCleanupState returns the current cleanup state safely.
func getCleanupState() (state, errMsg string, remaining []string, reclaimed int64) {
	cleanupMu.RLock()
	defer cleanupMu.RUnlock()
	return cleanupState, cleanupError, cleanupTablesRemaining, cleanupSpaceReclaimed
}

// setCleanupState sets the cleanup state safely.
func setCleanupState(state, errMsg string, remaining []string, reclaimed int64) {
	cleanupMu.Lock()
	defer cleanupMu.Unlock()
	cleanupState = state
	cleanupError = errMsg
	cleanupTablesRemaining = remaining
	cleanupSpaceReclaimed = reclaimed
}

// resetCleanupState resets the cleanup state to idle.
func resetCleanupState() {
	setCleanupState(CleanupStateIdle, "", nil, 0)
}

// tryStartCleanup atomically checks if cleanup can start and sets state to in_progress.
// Returns true if cleanup was started, false if already in progress.
// This prevents race conditions between concurrent cleanup requests.
func tryStartCleanup() bool {
	cleanupMu.Lock()
	defer cleanupMu.Unlock()
	if cleanupState == CleanupStateInProgress {
		return false
	}
	cleanupState = CleanupStateInProgress
	cleanupError = ""
	cleanupTablesRemaining = nil
	cleanupSpaceReclaimed = 0
	return true
}

// initLegacyCleanupRoutes registers the legacy cleanup API routes.
func (c *Controller) initLegacyCleanupRoutes() {
	c.logInfoIfEnabled("Initializing legacy cleanup routes")

	// Create legacy API group under system/database
	legacyGroup := c.Group.Group("/system/database/legacy")

	// Get the appropriate auth middleware
	authMiddleware := c.authMiddleware

	// Create auth-protected group
	protectedGroup := legacyGroup.Group("", authMiddleware)

	// Legacy status and cleanup routes
	protectedGroup.GET("/status", c.GetLegacyStatus)
	protectedGroup.POST("/cleanup", c.StartLegacyCleanup)

	c.logInfoIfEnabled("Legacy cleanup routes initialized successfully")
}

// GetLegacyStatus handles GET /api/v2/system/database/legacy/status
// Returns information about the legacy database for the cleanup UI.
func (c *Controller) GetLegacyStatus(ctx echo.Context) error {
	ip, path := ctx.RealIP(), ctx.Request().URL.Path
	c.logInfoIfEnabled("Getting legacy database status", logger.String("path", path), logger.String("ip", ip))

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
func (c *Controller) getLegacyStatusSQLite(ctx echo.Context, response *LegacyStatusResponse) error {
	legacyPath := c.Settings.Output.SQLite.Path
	response.Location = legacyPath

	// Check if legacy file exists
	fileInfo, err := os.Stat(legacyPath)
	if os.IsNotExist(err) {
		response.Exists = false
		response.CanCleanup = false
		response.Reason = "No legacy database found"
		return ctx.JSON(http.StatusOK, response)
	}
	if err != nil {
		response.Exists = false
		response.CanCleanup = false
		response.Reason = fmt.Sprintf("Cannot access legacy database: %v", err)
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
		return ctx.JSON(http.StatusOK, response)
	}

	// CRITICAL SAFETY CHECK: Ensure target is not a V2 database
	if datastoreV2.CheckSQLiteHasV2Schema(legacyPath) {
		response.CanCleanup = false
		response.Reason = "Target file appears to be a V2 database - cleanup not allowed for safety"
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
func (c *Controller) getLegacyStatusMySQL(ctx echo.Context, response *LegacyStatusResponse) error {
	response.Location = c.Settings.Output.MySQL.Database

	// Check if we're in v2-only mode (required for cleanup)
	if !isV2OnlyMode {
		response.Exists = true // Assume exists for MySQL
		response.CanCleanup = false
		response.Reason = "Application must be restarted after migration before cleanup is available"
		return ctx.JSON(http.StatusOK, response)
	}

	// Get GORM DB for MySQL queries
	dbProvider, ok := c.Repo.(gormDBProvider)
	if !ok {
		response.Exists = false
		response.CanCleanup = false
		response.Reason = "Cannot access database connection"
		return ctx.JSON(http.StatusOK, response)
	}
	db := dbProvider.GetDB()

	// Check each legacy table and get its size
	var totalSize int64
	var totalRows int64
	existingTables := make([]LegacyTableInfo, 0, len(legacyTables))

	for _, tableName := range legacyTables {
		var tableInfo LegacyTableInfo
		tableInfo.Name = tableName

		// Check if table exists and get row count
		var count int64
		err := db.Raw("SELECT COUNT(*) FROM information_schema.tables WHERE table_schema = ? AND table_name = ?",
			c.Settings.Output.MySQL.Database, tableName).Scan(&count).Error
		if err != nil || count == 0 {
			continue // Table doesn't exist, skip
		}

		// Get row count
		err = db.Raw(fmt.Sprintf("SELECT COUNT(*) FROM %s", tableName)).Scan(&tableInfo.RowCount).Error
		if err != nil {
			tableInfo.RowCount = 0
		}
		totalRows += tableInfo.RowCount

		// Get table size
		var dataLength, indexLength int64
		err = db.Raw(`
			SELECT COALESCE(data_length, 0), COALESCE(index_length, 0)
			FROM information_schema.tables
			WHERE table_schema = ? AND table_name = ?`,
			c.Settings.Output.MySQL.Database, tableName).Row().Scan(&dataLength, &indexLength)
		if err == nil {
			tableInfo.SizeBytes = dataLength + indexLength
			totalSize += tableInfo.SizeBytes
		}

		existingTables = append(existingTables, tableInfo)
	}

	if len(existingTables) == 0 {
		response.Exists = false
		response.CanCleanup = false
		response.Reason = "No legacy tables found"
		return ctx.JSON(http.StatusOK, response)
	}

	response.Exists = true
	response.CanCleanup = true
	response.SizeBytes = totalSize
	response.TotalRecords = totalRows
	response.Tables = existingTables

	return ctx.JSON(http.StatusOK, response)
}

// StartLegacyCleanup handles POST /api/v2/system/database/legacy/cleanup
// Initiates asynchronous legacy database cleanup.
func (c *Controller) StartLegacyCleanup(ctx echo.Context) error {
	ip, path := ctx.RealIP(), ctx.Request().URL.Path
	c.logInfoIfEnabled("Starting legacy database cleanup", logger.String("path", path), logger.String("ip", ip))

	// Check if we're in v2-only mode (check before trying to start)
	if !isV2OnlyMode {
		return ctx.JSON(http.StatusBadRequest, CleanupActionResponse{
			Success: false,
			Message: "Cannot cleanup: Application must be restarted after migration",
		})
	}

	// Atomically check and set cleanup state to prevent race conditions
	if !tryStartCleanup() {
		return ctx.JSON(http.StatusConflict, CleanupActionResponse{
			Success: false,
			Message: "Cleanup already in progress",
		})
	}

	// Get size before deletion for reporting
	var sizeBytes int64
	if c.isUsingMySQL() {
		sizeBytes = c.getMySQLLegacySize()
	} else {
		sizeBytes = c.getSQLiteLegacySize()
	}

	// Start async cleanup using WaitGroup for proper lifecycle management
	c.wg.Go(func() {
		var err error
		var remaining []string

		if c.isUsingMySQL() {
			remaining, err = c.cleanupMySQLLegacy(context.Background())
		} else {
			err = c.cleanupSQLiteLegacy(context.Background())
		}

		if err != nil {
			c.logErrorIfEnabled("Legacy cleanup failed", logger.Error(err))
			setCleanupState(CleanupStateFailed, err.Error(), remaining, 0)
			c.sendCleanupNotification(false, 0, err.Error())
		} else {
			c.logInfoIfEnabled("Legacy cleanup completed successfully",
				logger.Int64("space_reclaimed", sizeBytes))
			setCleanupState(CleanupStateCompleted, "", nil, sizeBytes)
			c.sendCleanupNotification(true, sizeBytes, "")
		}
	})

	c.logInfoIfEnabled("Legacy cleanup started", logger.String("path", path), logger.String("ip", ip))

	return ctx.JSON(http.StatusOK, CleanupActionResponse{
		Success: true,
		Message: "Legacy database cleanup started",
	})
}

// cleanupSQLiteLegacy deletes the legacy SQLite database files.
func (c *Controller) cleanupSQLiteLegacy(_ context.Context) error {
	legacyPath := c.Settings.Output.SQLite.Path

	// CRITICAL SAFETY CHECK: Ensure we're not deleting a V2 database
	if datastoreV2.CheckSQLiteHasV2Schema(legacyPath) {
		return fmt.Errorf("target file appears to be a V2 database - cleanup aborted for safety")
	}

	// Delete main database file
	if err := os.Remove(legacyPath); err != nil && !os.IsNotExist(err) {
		return fmt.Errorf("failed to delete legacy database: %w", err)
	}

	// Delete WAL and SHM files if they exist (ignore errors)
	_ = os.Remove(legacyPath + "-wal")
	_ = os.Remove(legacyPath + "-shm")

	return nil
}

// cleanupMySQLLegacy drops legacy tables from MySQL.
func (c *Controller) cleanupMySQLLegacy(_ context.Context) ([]string, error) {
	dbProvider, ok := c.Repo.(gormDBProvider)
	if !ok {
		return legacyTables, fmt.Errorf("cannot access database connection")
	}
	db := dbProvider.GetDB()

	var remaining []string

	for i, tableName := range legacyTables {
		// Check if table exists first
		var count int64
		err := db.Raw("SELECT COUNT(*) FROM information_schema.tables WHERE table_schema = ? AND table_name = ?",
			c.Settings.Output.MySQL.Database, tableName).Scan(&count).Error
		if err != nil || count == 0 {
			continue // Table doesn't exist, skip
		}

		// Drop the table
		err = db.Exec(fmt.Sprintf("DROP TABLE IF EXISTS %s", tableName)).Error
		if err != nil {
			// Stop on first error, return remaining tables
			remaining = legacyTables[i:]
			return remaining, fmt.Errorf("failed to drop table %s: %w", tableName, err)
		}
	}

	return nil, nil
}

// getSQLiteLegacySize returns the total size of SQLite legacy files.
func (c *Controller) getSQLiteLegacySize() int64 {
	legacyPath := c.Settings.Output.SQLite.Path
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

// getMySQLLegacySize returns the total size of MySQL legacy tables.
func (c *Controller) getMySQLLegacySize() int64 {
	dbProvider, ok := c.Repo.(gormDBProvider)
	if !ok {
		return 0
	}
	db := dbProvider.GetDB()

	var totalSize int64
	for _, tableName := range legacyTables {
		var dataLength, indexLength int64
		err := db.Raw(`
			SELECT COALESCE(data_length, 0), COALESCE(index_length, 0)
			FROM information_schema.tables
			WHERE table_schema = ? AND table_name = ?`,
			c.Settings.Output.MySQL.Database, tableName).Row().Scan(&dataLength, &indexLength)
		if err == nil {
			totalSize += dataLength + indexLength
		}
	}

	return totalSize
}

// sendCleanupNotification sends a notification about cleanup status.
func (c *Controller) sendCleanupNotification(success bool, spaceReclaimed int64, errMsg string) {
	notifService := notification.GetService()
	if notifService == nil {
		return
	}

	var title, body string
	var priority notification.Priority

	if success {
		title = "Legacy Database Cleanup Complete"
		body = fmt.Sprintf("Successfully removed legacy database. %s disk space reclaimed.",
			formatBytes(spaceReclaimed))
		priority = notification.PriorityMedium
	} else {
		title = "Legacy Database Cleanup Failed"
		body = fmt.Sprintf("Failed to remove legacy database: %s", errMsg)
		priority = notification.PriorityHigh
	}

	if _, err := notifService.CreateWithComponent(
		notification.TypeSystem,
		priority,
		title,
		body,
		"database",
	); err != nil {
		c.logWarnIfEnabled("Failed to send cleanup notification", logger.Error(err))
	}
}

// formatBytes converts bytes to human-readable format.
func formatBytes(bytes int64) string {
	const unit = 1024
	if bytes < unit {
		return fmt.Sprintf("%d B", bytes)
	}
	div, exp := int64(unit), 0
	for n := bytes / unit; n >= unit; n /= unit {
		div *= unit
		exp++
	}
	return fmt.Sprintf("%.1f %cB", float64(bytes)/float64(div), "KMGTPE"[exp])
}
