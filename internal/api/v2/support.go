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
	"github.com/tphakala/birdnet-go/internal/support"
	"github.com/tphakala/birdnet-go/internal/telemetry"
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
		c.apiLogger.Error("Failed to parse support dump request", "error", err)
		return ctx.JSON(http.StatusBadRequest, ErrorResponse{
			Error:   "Failed to parse request",
			Message: err.Error(),
		})
	}

	c.apiLogger.Debug("Support dump request parsed",
		"include_logs", req.IncludeLogs,
		"include_config", req.IncludeConfig,
		"include_system_info", req.IncludeSystemInfo,
		"upload_to_sentry", req.UploadToSentry,
		"has_user_message", req.UserMessage != "")

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

	// Create collector with proper paths using BuildInfo interface methods
	systemID := "unknown"
	version := "unknown" 
	if c.Runtime != nil {
		systemID = c.Runtime.GetSystemID()
		version = c.Runtime.GetVersion()
	}
	
	collector := support.NewCollector(
		configPath[0], // Use first config path
		".",           // Data directory (current directory)
		systemID,
		version,
	)

	// Set collection options
	opts := support.CollectorOptions{
		IncludeLogs:       req.IncludeLogs,
		IncludeConfig:     req.IncludeConfig,
		IncludeSystemInfo: req.IncludeSystemInfo,
		LogDuration:       4 * 7 * 24 * time.Hour, // 4 weeks
		MaxLogSize:        50 * 1024 * 1024,       // 50MB to accommodate more logs
		ScrubSensitive:    true,
	}

	// Collect data
	c.apiLogger.Debug("Starting support data collection", "system_id", systemID)
	dump, err := collector.Collect(ctx.Request().Context(), opts)
	if err != nil {
		c.apiLogger.Error("Failed to collect support data",
			"error", err,
			"system_id", systemID,
			"opts", opts,
		)
		return ctx.JSON(http.StatusInternalServerError, ErrorResponse{
			Error:   "Failed to collect support data",
			Message: err.Error(),
		})
	}
	c.apiLogger.Debug("Support data collected successfully", "dump_id", dump.ID)

	// Create archive
	c.apiLogger.Debug("Creating support archive", "dump_id", dump.ID)
	archiveData, err := collector.CreateArchive(ctx.Request().Context(), dump, opts)
	if err != nil {
		c.apiLogger.Error("Failed to create support archive",
			"error", err,
			"dump_id", dump.ID,
			"context_err", ctx.Request().Context().Err(),
		)
		return ctx.JSON(http.StatusInternalServerError, ErrorResponse{
			Error:   "Failed to create support archive",
			Message: err.Error(),
		})
	}
	c.apiLogger.Debug("Support archive created successfully", 
		"dump_id", dump.ID,
		"archive_size", len(archiveData))

	response := GenerateSupportDumpResponse{
		Success:  true,
		DumpID:   dump.ID,
		FileSize: len(archiveData),
	}

	// Upload to Sentry if requested (works regardless of telemetry setting)
	if req.UploadToSentry {
		// Initialize minimal Sentry if needed
		if !settings.Sentry.Enabled {
			if err := telemetry.InitMinimalSentryForSupport(systemID, version); err != nil {
				c.apiLogger.Error("Failed to initialize minimal Sentry for support upload",
					"error", err,
					"dump_id", dump.ID,
				)
				response.Message = "Support dump generated successfully but upload initialization failed"
				req.UploadToSentry = false // Fall back to download
			}
		}

		// Proceed with upload if still requested
		if req.UploadToSentry {
			uploader := telemetry.GetAttachmentUploader()
			if err := uploader.UploadSupportDump(ctx.Request().Context(), archiveData, systemID, req.UserMessage); err != nil {
				// Log error but don't fail the request
				c.apiLogger.Error("Failed to upload support dump to Sentry",
					"error", err,
					"dump_id", dump.ID,
				)
				response.Message = "Support dump generated successfully but upload failed"
			} else {
				response.UploadedAt = time.Now().UTC().Format(time.RFC3339)
				response.Message = "Support dump generated and uploaded successfully"
				c.apiLogger.Info("Support dump uploaded to Sentry",
					"dump_id", dump.ID,
					"system_id", systemID,
					"telemetry_enabled", settings.Sentry.Enabled,
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
		if err := os.WriteFile(tempFile, archiveData, 0o600); err != nil {
			c.apiLogger.Error("Failed to store temporary file",
				"error", err,
				"path", tempFile,
			)
		} else {
			response.DownloadURL = fmt.Sprintf("/api/v2/support/download/%s", dump.ID)
		}
	}

	// Log successful generation
	c.apiLogger.Info("Support dump generated",
		"dump_id", dump.ID,
		"size", len(archiveData),
		"uploaded", req.UploadToSentry && settings.Sentry.Enabled,
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

	// Use BuildInfo interface methods for safe access
	systemID := "unknown"
	version := "unknown"
	if c.Runtime != nil {
		systemID = c.Runtime.GetSystemID()
		version = c.Runtime.GetVersion()
	}
	
	status := map[string]any{
		"telemetry_enabled": settings.Sentry.Enabled,
		"system_id":         systemID,
		"version":           version,
	}

	return ctx.JSON(http.StatusOK, status)
}

// initSupportRoutes registers support-related routes
func (c *Controller) initSupportRoutes() {
	// Support endpoints require authentication
	c.Group.POST("/support/generate", c.GenerateSupportDump, c.authMiddlewareFn)
	c.Group.GET("/support/download/:id", c.DownloadSupportDump, c.authMiddlewareFn)
	c.Group.GET("/support/status", c.GetSupportStatus, c.authMiddlewareFn)

	// Start cleanup goroutine for old support dumps with proper context
	if c.ctx != nil {
		c.wg.Add(1)
		go func() {
			defer c.wg.Done()
			c.startSupportDumpCleanup(c.ctx)
		}()
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
			"pattern", pattern,
			"error", err,
		)
		return
	}

	// Changed to 1 hour to clean up files more aggressively
	cutoffTime := time.Now().Add(-1 * time.Hour)
	removedCount := 0

	for _, file := range files {
		info, err := os.Stat(file)
		if err != nil {
			// File might have been already removed
			if os.IsNotExist(err) {
				continue
			}
			c.apiLogger.Warn("Failed to stat support dump file",
				"path", file,
				"error", err,
			)
			continue
		}

		// Remove files older than 1 hour
		if info.ModTime().Before(cutoffTime) {
			if err := os.Remove(file); err != nil {
				// Check if file was already removed
				if !os.IsNotExist(err) {
					c.apiLogger.Warn("Failed to remove old support dump file",
						"path", file,
						"age", time.Since(info.ModTime()),
						"error", err,
					)
				}
			} else {
				removedCount++
			}
		}
	}

	if removedCount > 0 {
		c.apiLogger.Info("Cleaned up old support dump files",
			"count", removedCount,
		)
	}
}
