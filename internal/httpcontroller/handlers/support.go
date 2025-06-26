package handlers

import (
	"context"
	"fmt"
	"net/http"
	"os"
	"time"

	"github.com/labstack/echo/v4"
	"github.com/tphakala/birdnet-go/internal/conf"
	"github.com/tphakala/birdnet-go/internal/support"
	"github.com/tphakala/birdnet-go/internal/telemetry"
)

// GenerateSupportDumpRequest represents the request for generating a support dump
type GenerateSupportDumpRequest struct {
	IncludeLogs       bool   `json:"include_logs" form:"include_logs"`
	IncludeConfig     bool   `json:"include_config" form:"include_config"`
	IncludeSystemInfo bool   `json:"include_system_info" form:"include_system_info"`
	UserMessage       string `json:"user_message" form:"user_message"`
	UploadToSentry    bool   `json:"upload_to_sentry" form:"upload_to_sentry"`
}

// GenerateSupportDumpResponse represents the response for support dump generation
type GenerateSupportDumpResponse struct {
	Success    bool   `json:"success"`
	Message    string `json:"message"`
	DumpID     string `json:"dump_id,omitempty"`
	UploadedAt string `json:"uploaded_at,omitempty"`
	FileSize   int    `json:"file_size,omitempty"`
	DownloadURL string `json:"download_url,omitempty"`
}

// GenerateSupportDump handles the generation and optional upload of support dumps
func (h *Handlers) GenerateSupportDump(c echo.Context) error {
	// Parse request
	var req GenerateSupportDumpRequest
	if err := c.Bind(&req); err != nil {
		return h.NewHandlerError(err, "Failed to parse request", http.StatusBadRequest)
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
		return h.NewHandlerError(fmt.Errorf("settings not available"), "Settings not available", http.StatusInternalServerError)
	}

	// Create collector
	collector := support.NewCollector(
		h.Settings.ConfigDir,
		h.Settings.DataDir,
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
	ctx := context.Background()
	dump, err := collector.Collect(ctx, opts)
	if err != nil {
		return h.NewHandlerError(err, "Failed to collect support data", http.StatusInternalServerError)
	}

	// Create archive
	archiveData, err := collector.CreateArchive(ctx, dump, opts)
	if err != nil {
		return h.NewHandlerError(err, "Failed to create support archive", http.StatusInternalServerError)
	}

	response := GenerateSupportDumpResponse{
		Success:  true,
		DumpID:   dump.ID,
		FileSize: len(archiveData),
	}

	// Upload to Sentry if requested and telemetry is enabled
	if req.UploadToSentry && settings.Sentry.Enabled {
		uploader := telemetry.GetAttachmentUploader()
		if err := uploader.UploadSupportDump(ctx, archiveData, settings.SystemID, req.UserMessage); err != nil {
			// Log error but don't fail the request
			h.logError(&HandlerError{
				Err:     err,
				Message: "Failed to upload support dump to Sentry",
				Code:    http.StatusInternalServerError,
			})
			response.Message = "Support dump generated successfully but upload failed"
		} else {
			response.UploadedAt = time.Now().UTC().Format(time.RFC3339)
			response.Message = "Support dump generated and uploaded successfully"
		}
	} else if req.UploadToSentry && !settings.Sentry.Enabled {
		response.Message = "Support dump generated successfully (upload skipped - telemetry disabled)"
	} else {
		response.Message = "Support dump generated successfully"
	}

	// If not uploading, provide download option
	if !req.UploadToSentry || !settings.Sentry.Enabled {
		// Store temporarily for download
		tempFile := fmt.Sprintf("/tmp/birdnet-go-support-%s.zip", dump.ID)
		if err := h.storeTempFile(tempFile, archiveData); err != nil {
			h.logError(&HandlerError{
				Err:     err,
				Message: "Failed to store temporary file",
				Code:    http.StatusInternalServerError,
			})
		} else {
			response.DownloadURL = fmt.Sprintf("/api/v1/support/download/%s", dump.ID)
		}
	}

	// Send notification
	if h.notificationChan != nil {
		notification := Notification{
			Message: response.Message,
			Type:    "success",
		}
		select {
		case h.notificationChan <- notification:
		default:
			// Channel full, skip notification
		}
	}

	return c.JSON(http.StatusOK, response)
}

// DownloadSupportDump handles downloading a generated support dump
func (h *Handlers) DownloadSupportDump(c echo.Context) error {
	dumpID := c.Param("id")
	if dumpID == "" {
		return h.NewHandlerError(fmt.Errorf("missing dump ID"), "Missing dump ID", http.StatusBadRequest)
	}

	// Construct temp file path
	tempFile := fmt.Sprintf("/tmp/birdnet-go-support-%s.zip", dumpID)
	
	// Set headers for download
	c.Response().Header().Set("Content-Type", "application/zip")
	c.Response().Header().Set("Content-Disposition", fmt.Sprintf("attachment; filename=\"birdnet-go-support-%s.zip\"", dumpID))
	
	// Serve the file and delete after serving
	err := c.File(tempFile)
	
	// Clean up temp file
	go func() {
		time.Sleep(5 * time.Second) // Give time for download to complete
		_ = h.deleteTempFile(tempFile)
	}()
	
	return err
}

// GetSupportStatus returns the current support/telemetry configuration status
func (h *Handlers) GetSupportStatus(c echo.Context) error {
	settings := conf.GetSettings()
	if settings == nil {
		return h.NewHandlerError(fmt.Errorf("settings not available"), "Settings not available", http.StatusInternalServerError)
	}

	status := map[string]interface{}{
		"telemetry_enabled": settings.Sentry.Enabled,
		"system_id":        settings.SystemID,
		"version":          settings.Version,
	}

	return c.JSON(http.StatusOK, status)
}

// storeTempFile stores data to a temporary file
func (h *Handlers) storeTempFile(path string, data []byte) error {
	return os.WriteFile(path, data, 0644)
}

// deleteTempFile deletes a temporary file
func (h *Handlers) deleteTempFile(path string) error {
	return os.Remove(path)
}