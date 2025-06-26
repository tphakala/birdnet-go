package api

import (
	"context"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"time"

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
	// Parse JSON request
	var req GenerateSupportDumpRequest
	if err := ctx.Bind(&req); err != nil {
		return ctx.JSON(http.StatusBadRequest, ErrorResponse{
			Error:   "Failed to parse request",
			Message: err.Error(),
		})
	}

	// Set defaults if nothing is selected
	if !req.IncludeLogs && !req.IncludeConfig && !req.IncludeSystemInfo {
		req.IncludeLogs = true
		req.IncludeConfig = true
		req.IncludeSystemInfo = true
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
		configPath[0],  // Use first config path
		".",            // Data directory (current directory)
		settings.SystemID,
		settings.Version,
	)

	// Set collection options
	opts := support.CollectorOptions{
		IncludeLogs:       req.IncludeLogs,
		IncludeConfig:     req.IncludeConfig,
		IncludeSystemInfo: req.IncludeSystemInfo,
		LogDuration:       24 * time.Hour,
		MaxLogSize:        10 * 1024 * 1024, // 10MB
		ScrubSensitive:    true,
	}

	// Collect data
	dump, err := collector.Collect(context.Background(), opts)
	if err != nil {
		c.apiLogger.Error("Failed to collect support data",
			"error", err,
			"system_id", settings.SystemID,
		)
		return ctx.JSON(http.StatusInternalServerError, ErrorResponse{
			Error:   "Failed to collect support data",
			Message: err.Error(),
		})
	}

	// Create archive
	archiveData, err := collector.CreateArchive(context.Background(), dump, opts)
	if err != nil {
		c.apiLogger.Error("Failed to create support archive",
			"error", err,
			"dump_id", dump.ID,
		)
		return ctx.JSON(http.StatusInternalServerError, ErrorResponse{
			Error:   "Failed to create support archive",
			Message: err.Error(),
		})
	}

	response := GenerateSupportDumpResponse{
		Success:  true,
		DumpID:   dump.ID,
		FileSize: len(archiveData),
	}

	// Upload to Sentry if requested and telemetry is enabled
	if req.UploadToSentry && settings.Sentry.Enabled {
		uploader := telemetry.GetAttachmentUploader()
		if err := uploader.UploadSupportDump(context.Background(), archiveData, settings.SystemID, req.UserMessage); err != nil {
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
				"system_id", settings.SystemID,
			)
		}
	} else if req.UploadToSentry && !settings.Sentry.Enabled {
		response.Message = "Support dump generated successfully (upload skipped - telemetry disabled)"
	} else {
		response.Message = "Support dump generated successfully"
	}

	// If not uploading, provide download option
	if !req.UploadToSentry || !settings.Sentry.Enabled {
		// Store temporarily for download
		tempFile := filepath.Join(os.TempDir(), fmt.Sprintf("birdnet-go-support-%s.zip", dump.ID))
		if err := os.WriteFile(tempFile, archiveData, 0644); err != nil {
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
	
	// Clean up temp file after serving
	go func() {
		time.Sleep(5 * time.Second) // Give time for download to complete
		if err := os.Remove(tempFile); err != nil {
			c.apiLogger.Warn("Failed to remove temporary support file",
				"path", tempFile,
				"error", err,
			)
		}
	}()
	
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
		"system_id":        settings.SystemID,
		"version":          settings.Version,
	}

	return ctx.JSON(http.StatusOK, status)
}

// initSupportRoutes registers support-related routes
func (c *Controller) initSupportRoutes() {
	// Support endpoints require authentication
	c.Group.POST("/support/generate", c.GenerateSupportDump, c.authMiddlewareFn)
	c.Group.GET("/support/download/:id", c.DownloadSupportDump, c.authMiddlewareFn)
	c.Group.GET("/support/status", c.GetSupportStatus, c.authMiddlewareFn)
}