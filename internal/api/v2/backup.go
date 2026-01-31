// internal/api/v2/backup.go
// Async database backup with progress tracking.
package api

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/labstack/echo/v4"
	"github.com/shirou/gopsutil/v3/disk"
	"github.com/tphakala/birdnet-go/internal/datastore"
	"github.com/tphakala/birdnet-go/internal/logger"
	"gorm.io/gorm"
)

// Backup job status constants
const (
	BackupStatusPending    = "pending"
	BackupStatusInProgress = "in_progress"
	BackupStatusCompleted  = "completed"
	BackupStatusFailed     = "failed"
)

// Backup configuration constants
const (
	backupJobMaxAge       = 1 * time.Hour        // Jobs expire after 1 hour
	backupCleanupInterval = 5 * time.Minute      // Cleanup runs every 5 minutes
	backupTempFilePrefix  = "birdnet-backup-"    // Prefix for temp files
	backupDiskBuffer      = 100 * 1024 * 1024    // 100MB buffer for disk space check
	backupVacuumRetries   = 3                    // Number of retries for locked database
	backupVacuumRetryWait = 2 * time.Second      // Wait between retries
)

// BackupJob represents an async backup job.
type BackupJob struct {
	ID           string     `json:"job_id"`
	DBType       string     `json:"db_type"`
	Status       string     `json:"status"`
	Progress     int        `json:"progress"`
	BytesWritten int64      `json:"bytes_written"`
	TotalBytes   int64      `json:"total_bytes"`
	StartedAt    time.Time  `json:"started_at"`
	CompletedAt  *time.Time `json:"completed_at,omitempty"`
	Error        string     `json:"error,omitempty"`
	DownloadURL  string     `json:"download_url,omitempty"`

	// Internal fields (not exposed in JSON)
	tempPath string
	mu       sync.RWMutex
	cancel   context.CancelFunc
}

// BackupJobManager manages async backup jobs.
type BackupJobManager struct {
	jobs      map[string]*BackupJob
	mu        sync.RWMutex
	ctx       context.Context
	cancel    context.CancelFunc
	cleanupWg sync.WaitGroup
}

// BackupStartResponse is returned when starting a backup job.
type BackupStartResponse struct {
	JobID   string `json:"job_id"`
	Status  string `json:"status"`
	Message string `json:"message"`
}

// BackupJobListResponse is returned when listing backup jobs.
type BackupJobListResponse struct {
	Jobs []*BackupJob `json:"jobs"`
}

// NewBackupJobManager creates a new backup job manager.
func NewBackupJobManager() *BackupJobManager {
	ctx, cancel := context.WithCancel(context.Background())
	m := &BackupJobManager{
		jobs:   make(map[string]*BackupJob),
		ctx:    ctx,
		cancel: cancel,
	}

	// Start background cleanup goroutine
	m.cleanupWg.Add(1)
	go m.cleanupLoop()

	return m
}

// Shutdown gracefully shuts down the job manager.
func (m *BackupJobManager) Shutdown() {
	m.cancel()
	m.cleanupWg.Wait()
}

// backupLog is a module logger for backup operations.
var backupLog = logger.Global().Module("backup")

// CleanupOrphanedTempFiles removes any backup temp files that don't have active jobs.
// This should be called on server startup.
func (m *BackupJobManager) CleanupOrphanedTempFiles() {
	tempDir := os.TempDir()
	pattern := filepath.Join(tempDir, backupTempFilePrefix+"*.db")

	matches, err := filepath.Glob(pattern)
	if err != nil {
		backupLog.Warn("Backup cleanup: failed to glob temp files",
			logger.String("pattern", pattern),
			logger.Error(err))
		return
	}

	for _, path := range matches {
		if err := os.Remove(path); err != nil && !os.IsNotExist(err) {
			backupLog.Warn("Backup cleanup: failed to remove orphaned file",
				logger.String("path", path),
				logger.Error(err))
		} else {
			backupLog.Info("Backup cleanup: removed orphaned temp file",
				logger.String("path", path))
		}
	}
}

// cleanupLoop runs periodically to remove expired jobs.
func (m *BackupJobManager) cleanupLoop() {
	defer m.cleanupWg.Done()

	ticker := time.NewTicker(backupCleanupInterval)
	defer ticker.Stop()

	for {
		select {
		case <-m.ctx.Done():
			return
		case <-ticker.C:
			m.cleanupExpiredJobs()
		}
	}
}

// cleanupExpiredJobs removes jobs older than backupJobMaxAge.
func (m *BackupJobManager) cleanupExpiredJobs() {
	m.mu.Lock()
	defer m.mu.Unlock()

	now := time.Now()
	for id, job := range m.jobs {
		if now.Sub(job.StartedAt) > backupJobMaxAge {
			// Cancel if still running
			if job.cancel != nil {
				job.cancel()
			}
			// Remove temp file
			if job.tempPath != "" {
				_ = os.Remove(job.tempPath)
			}
			delete(m.jobs, id)
			backupLog.Info("Backup cleanup: removed expired job",
				logger.String("job_id", id),
				logger.String("db_type", job.DBType))
		}
	}
}

// generateJobID creates a unique job ID.
func generateJobID(dbType string) string {
	randomBytes := make([]byte, 4)
	_, _ = rand.Read(randomBytes)
	return fmt.Sprintf("backup-%s-%d-%s", dbType, time.Now().UnixNano(), hex.EncodeToString(randomBytes))
}

// GetJob returns a job by ID.
func (m *BackupJobManager) GetJob(id string) (*BackupJob, bool) {
	m.mu.RLock()
	defer m.mu.RUnlock()
	job, ok := m.jobs[id]
	return job, ok
}

// GetJobsByType returns all jobs for a specific database type.
func (m *BackupJobManager) GetJobsByType(dbType string) []*BackupJob {
	m.mu.RLock()
	defer m.mu.RUnlock()

	var result []*BackupJob
	for _, job := range m.jobs {
		if job.DBType == dbType {
			result = append(result, job)
		}
	}
	return result
}

// GetActiveJobByType returns the active (pending/in_progress) job for a database type, if any.
func (m *BackupJobManager) GetActiveJobByType(dbType string) (*BackupJob, bool) {
	m.mu.RLock()
	defer m.mu.RUnlock()

	for _, job := range m.jobs {
		if job.DBType == dbType && (job.Status == BackupStatusPending || job.Status == BackupStatusInProgress) {
			return job, true
		}
	}
	return nil, false
}

// CreateJob creates a new backup job.
func (m *BackupJobManager) CreateJob(dbType string, totalBytes int64) (*BackupJob, error) {
	m.mu.Lock()
	defer m.mu.Unlock()

	// Check for existing active job
	for _, job := range m.jobs {
		if job.DBType == dbType && (job.Status == BackupStatusPending || job.Status == BackupStatusInProgress) {
			return nil, fmt.Errorf("backup already in progress: %s", job.ID)
		}
	}

	ctx, cancel := context.WithCancel(m.ctx)
	jobID := generateJobID(dbType)
	timestamp := time.Now().Format("20060102-150405")
	tempPath := filepath.Join(os.TempDir(), fmt.Sprintf("%s%s-%s.db", backupTempFilePrefix, dbType, timestamp))

	job := &BackupJob{
		ID:         jobID,
		DBType:     dbType,
		Status:     BackupStatusPending,
		TotalBytes: totalBytes,
		StartedAt:  time.Now(),
		tempPath:   tempPath,
		cancel:     cancel,
	}

	// Store cancel context for the job
	_ = ctx // Used by the goroutine that runs the backup

	m.jobs[jobID] = job
	return job, nil
}

// DeleteJob removes a job and its temp file.
func (m *BackupJobManager) DeleteJob(id string) bool {
	m.mu.Lock()
	defer m.mu.Unlock()

	job, ok := m.jobs[id]
	if !ok {
		return false
	}

	// Cancel if still running
	if job.cancel != nil {
		job.cancel()
	}
	// Remove temp file
	if job.tempPath != "" {
		_ = os.Remove(job.tempPath)
	}
	delete(m.jobs, id)
	return true
}

// updateProgress updates the job's progress based on temp file size.
func (job *BackupJob) updateProgress() {
	job.mu.Lock()
	defer job.mu.Unlock()

	if job.tempPath == "" {
		return
	}

	info, err := os.Stat(job.tempPath)
	if err != nil {
		return
	}

	job.BytesWritten = info.Size()
	if job.TotalBytes > 0 {
		job.Progress = min(int((job.BytesWritten*100)/job.TotalBytes), 100)
	}
}

// setStatus updates the job status atomically.
func (job *BackupJob) setStatus(status, errMsg string) {
	job.mu.Lock()
	defer job.mu.Unlock()

	job.Status = status
	if errMsg != "" {
		job.Error = errMsg
	}
	if status == BackupStatusCompleted || status == BackupStatusFailed {
		now := time.Now()
		job.CompletedAt = &now
	}
}

// setDownloadURL sets the download URL for a completed job.
func (job *BackupJob) setDownloadURL(url string) {
	job.mu.Lock()
	defer job.mu.Unlock()
	job.DownloadURL = url
}

// Controller methods for backup endpoints

// backupJobManager is the singleton job manager (initialized in initBackupRoutes).
var backupJobManager *BackupJobManager

// initBackupRoutes initializes backup-related routes.
func (c *Controller) initBackupRoutes() {
	// Initialize job manager if not already done
	if backupJobManager == nil {
		backupJobManager = NewBackupJobManager()
		// Clean up any orphaned temp files from previous runs
		backupJobManager.CleanupOrphanedTempFiles()
	}

	// Create backup API group under system/database
	backupGroup := c.Group.Group("/system/database/backup/jobs")

	// Get the appropriate auth middleware
	authMiddleware := c.authMiddleware

	// Create auth-protected group
	protectedGroup := backupGroup.Group("", authMiddleware)

	// Register backup job routes
	protectedGroup.POST("", c.StartBackupJob)
	protectedGroup.GET("", c.ListBackupJobs)
	protectedGroup.GET("/:id", c.GetBackupJobStatus)
	protectedGroup.GET("/:id/download", c.DownloadBackupFile)
	protectedGroup.DELETE("/:id", c.CancelBackupJob)
}

// StartBackupJob handles POST /api/v2/system/database/backup/jobs
func (c *Controller) StartBackupJob(ctx echo.Context) error {
	dbType := ctx.QueryParam("type")
	if dbType != dbTypeLegacy && dbType != dbTypeV2 {
		return c.HandleError(ctx, fmt.Errorf("invalid type"),
			"Type must be 'legacy' or 'v2'", http.StatusBadRequest)
	}

	// Check for existing active job
	if existingJob, exists := backupJobManager.GetActiveJobByType(dbType); exists {
		return ctx.JSON(http.StatusConflict, map[string]any{
			"error":           "Backup already in progress",
			"existing_job_id": existingJob.ID,
			"status":          existingJob.Status,
		})
	}

	// Get database info
	dbPath, gormDB, err := c.getBackupDBInfo(dbType)
	if err != nil {
		return c.HandleError(ctx, err, err.Error(), http.StatusBadRequest)
	}

	// Get source database size
	fileInfo, err := os.Stat(dbPath)
	if err != nil {
		return c.HandleError(ctx, err, "Failed to get database info", http.StatusInternalServerError)
	}
	dbSize := fileInfo.Size()

	// Check disk space
	tempDir := os.TempDir()
	usage, err := disk.Usage(tempDir)
	if err != nil {
		return c.HandleError(ctx, err, "Failed to check disk space", http.StatusInternalServerError)
	}
	// #nosec G115 -- dbSize from os.FileInfo.Size() is always non-negative
	requiredSpace := uint64(dbSize) + backupDiskBuffer
	if usage.Free < requiredSpace {
		return c.HandleError(ctx, fmt.Errorf("insufficient space"),
			fmt.Sprintf("Not enough disk space. Need %s, have %s",
				formatBytesUint64(requiredSpace), formatBytesUint64(usage.Free)),
			http.StatusInsufficientStorage)
	}

	// Create the job
	job, err := backupJobManager.CreateJob(dbType, dbSize)
	if err != nil {
		// Job already exists
		if strings.Contains(err.Error(), "already in progress") {
			existingJob, _ := backupJobManager.GetActiveJobByType(dbType)
			return ctx.JSON(http.StatusConflict, map[string]any{
				"error":           err.Error(),
				"existing_job_id": existingJob.ID,
			})
		}
		return c.HandleError(ctx, err, "Failed to create backup job", http.StatusInternalServerError)
	}

	c.logInfoIfEnabled("Backup job started",
		logger.String("job_id", job.ID),
		logger.String("db_type", dbType),
		logger.Int64("db_size", dbSize))

	// Start the backup in a goroutine
	go c.runBackupJob(job, gormDB)

	return ctx.JSON(http.StatusAccepted, BackupStartResponse{
		JobID:   job.ID,
		Status:  job.Status,
		Message: "Backup job started",
	})
}

// ListBackupJobs handles GET /api/v2/system/database/backup/jobs
func (c *Controller) ListBackupJobs(ctx echo.Context) error {
	dbType := ctx.QueryParam("type")

	var jobs []*BackupJob
	if dbType != "" {
		jobs = backupJobManager.GetJobsByType(dbType)
	} else {
		// Return all jobs
		backupJobManager.mu.RLock()
		for _, job := range backupJobManager.jobs {
			jobs = append(jobs, job)
		}
		backupJobManager.mu.RUnlock()
	}

	// Update progress for in-progress jobs before returning
	for _, job := range jobs {
		if job.Status == BackupStatusInProgress {
			job.updateProgress()
		}
	}

	return ctx.JSON(http.StatusOK, BackupJobListResponse{Jobs: jobs})
}

// GetBackupJobStatus handles GET /api/v2/system/database/backup/jobs/:id
func (c *Controller) GetBackupJobStatus(ctx echo.Context) error {
	jobID := ctx.Param("id")

	job, exists := backupJobManager.GetJob(jobID)
	if !exists {
		return c.HandleError(ctx, fmt.Errorf("job not found"),
			"Backup job not found or expired", http.StatusNotFound)
	}

	// Update progress if still running
	if job.Status == BackupStatusInProgress {
		job.updateProgress()
	}

	// Set download URL if completed
	if job.Status == BackupStatusCompleted {
		job.setDownloadURL(fmt.Sprintf("/api/v2/system/database/backup/jobs/%s/download", job.ID))
	}

	return ctx.JSON(http.StatusOK, job)
}

// DownloadBackupFile handles GET /api/v2/system/database/backup/jobs/:id/download
func (c *Controller) DownloadBackupFile(ctx echo.Context) error {
	jobID := ctx.Param("id")

	job, exists := backupJobManager.GetJob(jobID)
	if !exists {
		return c.HandleError(ctx, fmt.Errorf("job not found"),
			"Backup job not found or expired", http.StatusNotFound)
	}

	if job.Status != BackupStatusCompleted {
		return ctx.JSON(http.StatusConflict, map[string]any{
			"error":  "Backup not ready for download",
			"status": job.Status,
		})
	}

	// Verify temp file exists
	if _, err := os.Stat(job.tempPath); err != nil {
		return c.HandleError(ctx, err,
			"Backup file not found - job may have expired", http.StatusGone)
	}

	// Set headers and serve file
	filename := fmt.Sprintf("birdnet-%s-backup-%s.db", job.DBType, job.StartedAt.Format("20060102-150405"))
	ctx.Response().Header().Set("Content-Disposition", fmt.Sprintf("attachment; filename=%q", filename))
	ctx.Response().Header().Set("Content-Type", "application/octet-stream")

	c.logInfoIfEnabled("Backup download started",
		logger.String("job_id", jobID),
		logger.String("db_type", job.DBType),
		logger.String("filename", filename))

	return ctx.File(job.tempPath)
}

// CancelBackupJob handles DELETE /api/v2/system/database/backup/jobs/:id
func (c *Controller) CancelBackupJob(ctx echo.Context) error {
	jobID := ctx.Param("id")

	if !backupJobManager.DeleteJob(jobID) {
		return c.HandleError(ctx, fmt.Errorf("job not found"),
			"Backup job not found", http.StatusNotFound)
	}

	c.logInfoIfEnabled("Backup job cancelled",
		logger.String("job_id", jobID))

	return ctx.JSON(http.StatusOK, map[string]string{
		"message": "Backup job cancelled and cleaned up",
	})
}

// runBackupJob executes the VACUUM INTO operation in a goroutine.
func (c *Controller) runBackupJob(job *BackupJob, gormDB *gorm.DB) {
	job.setStatus(BackupStatusInProgress, "")

	vacuumSQL := fmt.Sprintf("VACUUM INTO '%s'", job.tempPath)
	var lastErr error

	// Retry loop for database lock
	for attempt := 1; attempt <= backupVacuumRetries; attempt++ {
		c.logInfoIfEnabled("Backup VACUUM INTO starting",
			logger.String("job_id", job.ID),
			logger.Int("attempt", attempt),
			logger.String("temp_path", job.tempPath))

		lastErr = gormDB.Exec(vacuumSQL).Error
		if lastErr == nil {
			break
		}

		// Check if it's a lock error
		if !strings.Contains(lastErr.Error(), "locked") {
			break // Non-recoverable error
		}

		c.logWarnIfEnabled("Backup VACUUM INTO database locked, retrying",
			logger.String("job_id", job.ID),
			logger.Int("attempt", attempt),
			logger.Error(lastErr))

		time.Sleep(backupVacuumRetryWait)
	}

	if lastErr != nil {
		c.logWarnIfEnabled("Backup VACUUM INTO failed",
			logger.String("job_id", job.ID),
			logger.Error(lastErr))
		job.setStatus(BackupStatusFailed, lastErr.Error())
		return
	}

	// Final progress update
	job.updateProgress()
	job.setStatus(BackupStatusCompleted, "")

	c.logInfoIfEnabled("Backup job completed",
		logger.String("job_id", job.ID),
		logger.String("db_type", job.DBType),
		logger.Int64("bytes_written", job.BytesWritten))
}

// getBackupDBInfo returns the database path and GORM DB for the given type.
func (c *Controller) getBackupDBInfo(dbType string) (string, *gorm.DB, error) {
	if dbType == dbTypeLegacy {
		if c.Settings.Output.SQLite.Path == "" {
			return "", nil, fmt.Errorf("backup only available for SQLite databases")
		}
		if c.DS == nil {
			return "", nil, fmt.Errorf("database not configured")
		}
		sqliteStore, ok := c.DS.(*datastore.SQLiteStore)
		if !ok {
			return "", nil, fmt.Errorf("cannot perform backup on this datastore type")
		}
		return c.Settings.Output.SQLite.Path, sqliteStore.DB, nil
	}

	// V2 database
	if c.V2Manager == nil {
		return "", nil, fmt.Errorf("v2 database not initialized")
	}
	if c.V2Manager.IsMySQL() {
		return "", nil, fmt.Errorf("backup only available for SQLite databases")
	}
	return c.V2Manager.Path(), c.V2Manager.DB(), nil
}
