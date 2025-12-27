package api

import (
	"context"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"time"

	"github.com/google/uuid"
	"github.com/labstack/echo/v4"
	"github.com/tphakala/birdnet-go/internal/conf"
	"github.com/tphakala/birdnet-go/internal/logger"
	"github.com/tphakala/birdnet-go/internal/support"
	"github.com/tphakala/birdnet-go/internal/telemetry"
)

// Support constants (file-local)
const (
	supportLogDurationWeeks = 4                 // Weeks of logs to collect
	supportMaxLogSizeMB     = 50                // Maximum log size in MB
	supportBytesPerKB       = 1024              // Bytes per kilobyte
	supportBytesPerMB       = 1024 * 1024       // Bytes per megabyte
)

// GenerateSupportDumpRequest represents the request for generating a support dump
type GenerateSupportDumpRequest struct {
	IncludeLogs       bool   `json:"include_logs"`
	IncludeConfig     bool   `json:"include_config"`
	IncludeSystemInfo bool   `json:"include_system_info"`
	UserMessage       string `json:"user_message"`
	UploadToSentry    bool   `json:"upload_to_sentry"`
}

// GenerateSupportDumpResponse represents the response for support dump generation
type GenerateSupportDumpResponse struct {
	Success     bool   `json:"success"`
	Message     string `json:"message"`
	DumpID      string `json:"dump_id,omitempty"`
	UploadedAt  string `json:"uploaded_at,omitempty"`
	FileSize    int    `json:"file_size,omitempty"`
	DownloadURL string `json:"download_url,omitempty"`
}

// GenerateSupportDump handles the generation and optional upload of support dumps
func (c *Controller) GenerateSupportDump(ctx echo.Context) error {
	c.apiLogger.Debug("Support dump generation started")

	// Parse JSON request
	var req GenerateSupportDumpRequest
	if err := ctx.Bind(&req); err != nil {
		c.apiLogger.Error("Failed to parse support dump request", logger.Error(err))
		return ctx.JSON(http.StatusBadRequest, ErrorResponse{
			Error:   "Failed to parse request",
			Message: err.Error(),
		})
	}

	c.apiLogger.Debug("Support dump request parsed",
		logger.Bool("include_logs", req.IncludeLogs),
		logger.Bool("include_config", req.IncludeConfig),
		logger.Bool("include_system_info", req.IncludeSystemInfo),
		logger.Bool("upload_to_sentry", req.UploadToSentry),
		logger.Bool("has_user_message", req.UserMessage != ""))

	// Set defaults if nothing is selected
	if !req.IncludeLogs && !req.IncludeConfig && !req.IncludeSystemInfo {
		req.IncludeLogs = true
		req.IncludeConfig = true
		req.IncludeSystemInfo = true
		c.apiLogger.Debug("Set default options for support dump")
	}

	// Get current settings
	settings := conf.GetSettings()
	if settings == nil {
		return ctx.JSON(http.StatusInternalServerError, ErrorResponse{
			Error: "Settings not available",
		})
	}

	// Get config directory path
	configPath, err := conf.GetDefaultConfigPaths()
	if err != nil || len(configPath) == 0 {
		configPath = []string{"."}
	}

	// Create collector with proper paths
	collector := support.NewCollector(
		configPath[0], // Use first config path
		".",           // Data directory (current directory)
		settings.SystemID,
		settings.Version,
	)

	// Set collection options
	opts := support.CollectorOptions{
		IncludeLogs:       req.IncludeLogs,
		IncludeConfig:     req.IncludeConfig,
		IncludeSystemInfo: req.IncludeSystemInfo,
		LogDuration:       supportLogDurationWeeks * daysPerWeek * HoursPerDay * time.Hour, // 4 weeks
		MaxLogSize:        supportMaxLogSizeMB * supportBytesPerMB,                       // 50MB to accommodate more logs
		ScrubSensitive:    true,
	}

	// Collect data
	c.apiLogger.Debug("Starting support data collection", logger.String("system_id", settings.SystemID))
	dump, err := collector.Collect(ctx.Request().Context(), opts)
	if err != nil {
		c.apiLogger.Error("Failed to collect support data",
			logger.Error(err),
			logger.String("system_id", settings.SystemID),
			logger.Any("opts", opts),
		)
		return ctx.JSON(http.StatusInternalServerError, ErrorResponse{
			Error:   "Failed to collect support data",
			Message: err.Error(),
		})
	}
	c.apiLogger.Debug("Support data collected successfully", logger.String("dump_id", dump.ID))

	// Create archive
	c.apiLogger.Debug("Creating support archive", logger.String("dump_id", dump.ID))
	archiveData, err := collector.CreateArchive(ctx.Request().Context(), dump, opts)
	if err != nil {
		c.apiLogger.Error("Failed to create support archive",
			logger.Error(err),
			logger.String("dump_id", dump.ID),
			logger.Any("context_err", ctx.Request().Context().Err()),
		)
		return ctx.JSON(http.StatusInternalServerError, ErrorResponse{
			Error:   "Failed to create support archive",
			Message: err.Error(),
		})
	}
	c.apiLogger.Debug("Support archive created successfully",
		logger.String("dump_id", dump.ID),
		logger.Int("archive_size", len(archiveData)))

	response := GenerateSupportDumpResponse{
		Success:  true,
		DumpID:   dump.ID,
		FileSize: len(archiveData),
	}

	// Upload to Sentry if requested (works regardless of telemetry setting)
	if req.UploadToSentry {
		// Initialize minimal Sentry if needed
		if !settings.Sentry.Enabled {
			if err := telemetry.InitMinimalSentryForSupport(settings.SystemID, settings.Version); err != nil {
				c.apiLogger.Error("Failed to initialize minimal Sentry for support upload",
					logger.Error(err),
					logger.String("dump_id", dump.ID),
				)
				response.Message = "Support dump generated successfully but upload initialization failed"
				req.UploadToSentry = false // Fall back to download
			}
		}

		// Proceed with upload if still requested
		if req.UploadToSentry {
			uploader := telemetry.GetAttachmentUploader()
			if err := uploader.UploadSupportDump(ctx.Request().Context(), archiveData, settings.SystemID, req.UserMessage); err != nil {
				// Log error but don't fail the request
				c.apiLogger.Error("Failed to upload support dump to Sentry",
					logger.Error(err),
					logger.String("dump_id", dump.ID),
				)
				response.Message = "Support dump generated successfully but upload failed"
			} else {
				response.UploadedAt = time.Now().UTC().Format(time.RFC3339)
				response.Message = "Support dump generated and uploaded successfully"
				c.apiLogger.Info("Support dump uploaded to Sentry",
					logger.String("dump_id", dump.ID),
					logger.String("system_id", settings.SystemID),
					logger.Bool("telemetry_enabled", settings.Sentry.Enabled),
				)
			}
		}
	} else {
		response.Message = "Support dump generated successfully"
	}

	// If not uploading, provide download option
	if !req.UploadToSentry {
		// Store temporarily for download
		tempFile := filepath.Join(os.TempDir(), fmt.Sprintf("birdnet-go-support-%s.zip", dump.ID))
		if err := os.WriteFile(tempFile, archiveData, FilePermOwnerOnly); err != nil {
			c.apiLogger.Error("Failed to store temporary file",
				logger.Error(err),
				logger.String("path", tempFile),
			)
		} else {
			response.DownloadURL = fmt.Sprintf("/api/v2/support/download/%s", dump.ID)
		}
	}

	// Log successful generation
	c.apiLogger.Info("Support dump generated",
		logger.String("dump_id", dump.ID),
		logger.Int("size", len(archiveData)),
		logger.Bool("uploaded", req.UploadToSentry && settings.Sentry.Enabled),
	)

	return ctx.JSON(http.StatusOK, response)
}

// DownloadSupportDump handles downloading a generated support dump
func (c *Controller) DownloadSupportDump(ctx echo.Context) error {
	dumpID := ctx.Param("id")
	if dumpID == "" {
		return ctx.JSON(http.StatusBadRequest, ErrorResponse{
			Error: "Missing dump ID",
		})
	}

	// Validate dumpID is a valid UUID to prevent path traversal
	if _, err := uuid.Parse(dumpID); err != nil {
		return ctx.JSON(http.StatusBadRequest, ErrorResponse{
			Error: "Invalid dump ID format",
		})
	}

	// Construct temp file path
	tempFile := filepath.Join(os.TempDir(), fmt.Sprintf("birdnet-go-support-%s.zip", dumpID))

	// Check if file exists
	if _, err := os.Stat(tempFile); os.IsNotExist(err) {
		return ctx.JSON(http.StatusNotFound, ErrorResponse{
			Error: "Support dump not found",
		})
	}

	// Set headers for download
	ctx.Response().Header().Set("Content-Type", "application/zip")
	ctx.Response().Header().Set("Content-Disposition", fmt.Sprintf("attachment; filename=\"birdnet-go-support-%s.zip\"", dumpID))

	// Serve the file
	err := ctx.File(tempFile)

	// Schedule cleanup of temp file after serving
	// Note: The file will be removed during the periodic cleanup routine
	// This avoids potential race conditions with the download

	return err
}

// GetSupportStatus returns the current support/telemetry configuration status
func (c *Controller) GetSupportStatus(ctx echo.Context) error {
	settings := conf.GetSettings()
	if settings == nil {
		return ctx.JSON(http.StatusInternalServerError, ErrorResponse{
			Error: "Settings not available",
		})
	}

	status := map[string]any{
		"telemetry_enabled": settings.Sentry.Enabled,
		"system_id":         settings.SystemID,
		"version":           settings.Version,
	}

	return ctx.JSON(http.StatusOK, status)
}

// initSupportRoutes registers support-related routes
func (c *Controller) initSupportRoutes() {
	// Create protected group for support endpoints (consistent with other route files)
	supportGroup := c.Group.Group("/support", c.authMiddleware)
	supportGroup.POST("/generate", c.GenerateSupportDump)
	supportGroup.GET("/download/:id", c.DownloadSupportDump)
	supportGroup.GET("/status", c.GetSupportStatus)

	// Start cleanup goroutine for old support dumps with proper context
	// Go 1.25: Using WaitGroup.Go() for automatic Add/Done management
	if c.ctx != nil {
		c.wg.Go(func() {
			c.startSupportDumpCleanup(c.ctx)
		})
	}
}

// startSupportDumpCleanup runs a periodic cleanup of old temporary support dump files
func (c *Controller) startSupportDumpCleanup(ctx context.Context) {
	// Ensure we have a valid context
	if ctx == nil {
		c.apiLogger.Error("Cannot start support dump cleanup with nil context")
		return
	}

	// Run cleanup immediately on startup
	c.cleanupOldSupportDumps()

	// Then run every hour
	ticker := time.NewTicker(1 * time.Hour)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			// Context cancelled, exit gracefully
			c.apiLogger.Info("Support dump cleanup goroutine stopping due to context cancellation")
			return
		case <-ticker.C:
			c.cleanupOldSupportDumps()
		}
	}
}

// cleanupOldSupportDumps removes temporary support dump files older than 1 hour
func (c *Controller) cleanupOldSupportDumps() {
	tempDir := os.TempDir()
	pattern := filepath.Join(tempDir, "birdnet-go-support-*.zip")

	files, err := filepath.Glob(pattern)
	if err != nil {
		c.apiLogger.Error("Failed to list support dump files for cleanup",
			logger.String("pattern", pattern), logger.Error(err))
		return
	}

	cutoffTime := time.Now().Add(-1 * time.Hour)
	removedCount := 0

	for _, file := range files {
		if c.tryRemoveOldFile(file, cutoffTime) {
			removedCount++
		}
	}

	if removedCount > 0 {
		c.apiLogger.Info("Cleaned up old support dump files", logger.Int("count", removedCount))
	}
}

// tryRemoveOldFile attempts to remove a file if it's older than cutoff.
// Returns true if the file was successfully removed.
func (c *Controller) tryRemoveOldFile(file string, cutoff time.Time) bool {
	info, err := os.Stat(file)
	if err != nil {
		if !os.IsNotExist(err) {
			c.apiLogger.Warn("Failed to stat support dump file", logger.String("path", file), logger.Error(err))
		}
		return false
	}

	if !info.ModTime().Before(cutoff) {
		return false // File is not old enough
	}

	if err := os.Remove(file); err != nil && !os.IsNotExist(err) {
		c.apiLogger.Warn("Failed to remove old support dump file",
			logger.String("path", file), logger.Duration("age", time.Since(info.ModTime())), logger.Error(err))
		return false
	}
	return true
}
