package api

import (
	"context"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"strconv"
	"time"

	"github.com/google/uuid"
	"github.com/labstack/echo/v4"
	"github.com/tphakala/birdnet-go/internal/conf"
	"github.com/tphakala/birdnet-go/internal/datastore"
	"github.com/tphakala/birdnet-go/internal/logger"
	"github.com/tphakala/birdnet-go/internal/support"
	"github.com/tphakala/birdnet-go/internal/telemetry"
)

// Support constants (file-local)
const (
	supportLogDurationWeeks = 4           // Weeks of logs to collect
	supportMaxLogSizeMB     = 50          // Maximum log size in MB
	supportBytesPerMB       = 1024 * 1024 // Bytes per megabyte
	supportDumpTimeout      = 120 * time.Second
)

// valueContext wraps a cancellation-bearing context while delegating
// Value lookups to a separate context. This lets the support dump
// handler use the server lifecycle context (c.Context()) for cancellation
// while still reading trace IDs from the HTTP request context.
type valueContext struct {
	context.Context
	values context.Context
}

func (vc valueContext) Value(key any) any {
	if val := vc.values.Value(key); val != nil {
		return val
	}
	return vc.Context.Value(key)
}

// GenerateSupportDumpRequest represents the request for generating a support dump
type GenerateSupportDumpRequest struct {
	IncludeLogs         bool   `json:"include_logs"`
	IncludeConfig       bool   `json:"include_config"`
	IncludeSystemInfo   bool   `json:"include_system_info"`
	IncludeDatabaseInfo bool   `json:"include_database_info"`
	IncludeAppEvents    bool   `json:"include_app_events"`
	UserMessage         string `json:"user_message"`
	UploadToSentry      bool   `json:"upload_to_sentry"`
	GitHubIssueNumber   string `json:"github_issue_number"`
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

// sanitizeGitHubIssueNumber returns the issue number if it is a valid positive
// integer, or an empty string otherwise.
func sanitizeGitHubIssueNumber(issueNum string) string {
	if _, err := strconv.ParseUint(issueNum, 10, 64); err != nil {
		return ""
	}
	return issueNum
}

// GenerateSupportDump handles the generation and optional upload of support dumps
func (c *Controller) GenerateSupportDump(ctx echo.Context) error {
	c.LogDebugIfEnabled("Support dump generation started")

	// Use the server lifecycle context for cancellation so the dump
	// stops on shutdown, but delegate Value lookups to the request
	// context so trace IDs remain accessible.
	parentCtx := c.Context()
	if parentCtx == nil {
		parentCtx = context.Background()
	}
	dumpCtx, cancel := context.WithTimeout(
		valueContext{Context: parentCtx, values: ctx.Request().Context()},
		supportDumpTimeout,
	)
	defer cancel()

	// Extend the HTTP write deadline so the server does not close the
	// TCP connection before the handler finishes (default WriteTimeout
	// is 30s, but the dump can take much longer).
	rc := http.NewResponseController(ctx.Response().Writer)
	if err := rc.SetWriteDeadline(time.Now().Add(supportDumpTimeout)); err != nil {
		c.LogDebugIfEnabled("Failed to extend write deadline for support dump", logger.Error(err))
	}

	// Parse JSON request
	var req GenerateSupportDumpRequest
	if err := ctx.Bind(&req); err != nil {
		return c.HandleError(ctx, err, "Failed to parse request", http.StatusBadRequest)
	}

	// Sanitize GitHub issue number: must be digits only (frontend strips '#' prefix)
	req.GitHubIssueNumber = sanitizeGitHubIssueNumber(req.GitHubIssueNumber)

	c.LogDebugIfEnabled("Support dump request parsed",
		logger.Bool("include_logs", req.IncludeLogs),
		logger.Bool("include_config", req.IncludeConfig),
		logger.Bool("include_system_info", req.IncludeSystemInfo),
		logger.Bool("include_database_info", req.IncludeDatabaseInfo),
		logger.Bool("include_app_events", req.IncludeAppEvents),
		logger.Bool("upload_to_sentry", req.UploadToSentry),
		logger.String("github_issue", req.GitHubIssueNumber),
		logger.Bool("has_user_message", req.UserMessage != ""))

	// Set defaults if nothing is selected
	if !req.IncludeLogs && !req.IncludeConfig && !req.IncludeSystemInfo && !req.IncludeDatabaseInfo && !req.IncludeAppEvents {
		req.IncludeLogs = true
		req.IncludeConfig = true
		req.IncludeSystemInfo = true
		req.IncludeDatabaseInfo = true
		req.IncludeAppEvents = true
		c.LogDebugIfEnabled("Set default options for support dump")
	}

	// Read the live global snapshot (race-free, hot-reloading) via
	// CurrentSettings() so out-of-band republishes are seen and the read never
	// races the Settings.Store in UpdateSettings.
	settings := c.CurrentSettings()
	if settings == nil {
		return c.HandleError(ctx, nil, "Settings not available", http.StatusInternalServerError)
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

	// Wire database info provider if V2Manager is available
	if req.IncludeDatabaseInfo && c.V2Manager != nil && c.DS != nil {
		dialect := datastore.DialectSQLite
		if c.V2Manager.IsMySQL() {
			dialect = datastore.DialectMySQL
		}
		dbCollector := support.NewGormDatabaseInfoCollector(
			c.V2Manager.DB(),
			dialect,
			c.V2Manager.Path(),
			c.DS.SchemaVersion(),
			c.V2Manager.TablePrefix(),
		)
		collector.SetDatabaseInfoProvider(dbCollector)
	}

	// Wire app events provider
	if req.IncludeAppEvents && c.DS != nil {
		collector.SetAppEventsProvider(support.NewDatastoreAppEventsProvider(c.DS, nil))
	}

	// Set collection options
	opts := support.CollectorOptions{
		IncludeLogs:           req.IncludeLogs,
		IncludeConfig:         req.IncludeConfig,
		IncludeSystemInfo:     req.IncludeSystemInfo,
		IncludeDatabaseInfo:   req.IncludeDatabaseInfo,
		IncludeDeploymentInfo: true,
		IncludeAppEvents:      req.IncludeAppEvents,
		LogDuration:           supportLogDurationWeeks * daysPerWeek * HoursPerDay * time.Hour, // 4 weeks
		MaxLogSize:            supportMaxLogSizeMB * supportBytesPerMB,                         // 50MB to accommodate more logs
		ScrubSensitive:        true,
		AnonymizePII:          true,
	}

	// Collect data
	c.LogDebugIfEnabled("Starting support data collection", logger.String("system_id", settings.SystemID))
	dump, err := collector.Collect(dumpCtx, opts)
	if err != nil {
		c.LogErrorIfEnabled("Failed to collect support data",
			logger.Error(err),
			logger.String("system_id", settings.SystemID),
			logger.Any("opts", opts),
		)
		return c.HandleError(ctx, err, "Failed to collect support data", http.StatusInternalServerError)
	}
	c.LogDebugIfEnabled("Support data collected successfully", logger.String("dump_id", dump.ID))

	// Create archive
	c.LogDebugIfEnabled("Creating support archive", logger.String("dump_id", dump.ID))
	archiveData, err := collector.CreateArchive(dumpCtx, dump, opts)
	if err != nil {
		c.LogErrorIfEnabled("Failed to create support archive",
			logger.Error(err),
			logger.String("dump_id", dump.ID),
			logger.Any("context_err", dumpCtx.Err()),
		)
		return c.HandleError(ctx, err, "Failed to create support archive", http.StatusInternalServerError)
	}
	c.LogDebugIfEnabled("Support archive created successfully",
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
				c.LogErrorIfEnabled("Failed to initialize minimal Sentry for support upload",
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
			if err := uploader.UploadSupportDump(dumpCtx, archiveData, settings.SystemID, req.UserMessage, req.GitHubIssueNumber); err != nil {
				// Log error but don't fail the request. Fall back to download so
				// the user can still retrieve the dump: builds without a Sentry
				// DSN (e.g. from-source) have a disabled uploader, and a
				// transient upload failure should not strand the dump.
				c.LogErrorIfEnabled("Failed to upload support dump to Sentry",
					logger.Error(err),
					logger.String("dump_id", dump.ID),
				)
				response.Message = "Support dump generated successfully but upload failed"
				req.UploadToSentry = false // fall back to download
			} else {
				response.UploadedAt = time.Now().UTC().Format(time.RFC3339)
				response.Message = "Support dump generated and uploaded successfully"
				c.LogInfoIfEnabled("Support dump uploaded to Sentry",
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
			c.LogErrorIfEnabled("Failed to store temporary file",
				logger.Error(err),
				logger.String("path", tempFile),
			)
		} else {
			response.DownloadURL = fmt.Sprintf("/api/v2/support/download/%s", dump.ID)
		}
	}

	// Log successful generation
	c.LogInfoIfEnabled("Support dump generated",
		logger.String("dump_id", dump.ID),
		logger.Int("size", len(archiveData)),
		logger.Bool("uploaded", response.UploadedAt != ""),
	)

	return ctx.JSON(http.StatusOK, response)
}

// DownloadSupportDump handles downloading a generated support dump
func (c *Controller) DownloadSupportDump(ctx echo.Context) error {
	dumpID := ctx.Param("id")
	if dumpID == "" {
		return c.HandleError(ctx, nil, "Missing dump ID", http.StatusBadRequest)
	}

	// Validate dumpID is a valid UUID to prevent path traversal
	if _, err := uuid.Parse(dumpID); err != nil {
		return c.HandleError(ctx, err, "Invalid dump ID format", http.StatusBadRequest)
	}

	// Construct temp file path
	tempFile := filepath.Join(os.TempDir(), fmt.Sprintf("birdnet-go-support-%s.zip", dumpID))

	// Check if file exists
	if _, err := os.Stat(tempFile); os.IsNotExist(err) {
		return c.HandleError(ctx, err, "Support dump not found", http.StatusNotFound)
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
	// Read the live global snapshot (race-free, hot-reloading) via
	// CurrentSettings() so out-of-band republishes are seen and the read never
	// races the Settings.Store in UpdateSettings.
	settings := c.CurrentSettings()
	if settings == nil {
		return c.HandleError(ctx, nil, "Settings not available", http.StatusInternalServerError)
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
	supportGroup := c.Group.Group("/support", c.AuthMiddleware)
	supportGroup.POST("/generate", c.GenerateSupportDump)
	supportGroup.GET("/download/:id", c.DownloadSupportDump)
	supportGroup.GET("/status", c.GetSupportStatus)

	// Start cleanup goroutine for old support dumps with proper context
	// Go 1.25: Using WaitGroup.Go() for automatic Add/Done management
	if c.Context() != nil {
		c.Go(func() {
			c.startSupportDumpCleanup(c.Context())
		})

		c.Go(func() {
			c.startAppEventPruning(c.Context())
		})

	}
}

// startSupportDumpCleanup runs a periodic cleanup of old temporary support dump files
func (c *Controller) startSupportDumpCleanup(ctx context.Context) {
	// Ensure we have a valid context
	if ctx == nil {
		c.LogErrorIfEnabled("Cannot start support dump cleanup with nil context")
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
			c.LogInfoIfEnabled("Support dump cleanup goroutine stopping due to context cancellation")
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
		c.LogErrorIfEnabled("Failed to list support dump files for cleanup",
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
		c.LogInfoIfEnabled("Cleaned up old support dump files", logger.Int("count", removedCount))
	}
}

// tryRemoveOldFile attempts to remove a file if it's older than cutoff.
// Returns true if the file was successfully removed.
func (c *Controller) tryRemoveOldFile(file string, cutoff time.Time) bool {
	info, err := os.Stat(file)
	if err != nil {
		if !os.IsNotExist(err) {
			c.LogWarnIfEnabled("Failed to stat support dump file", logger.String("path", file), logger.Error(err))
		}
		return false
	}

	if !info.ModTime().Before(cutoff) {
		return false // File is not old enough
	}

	if err := os.Remove(file); err != nil && !os.IsNotExist(err) {
		c.LogWarnIfEnabled("Failed to remove old support dump file",
			logger.String("path", file), logger.Duration("age", time.Since(info.ModTime())), logger.Error(err))
		return false
	}
	return true
}

// appEventRetentionDays is the default retention period for application events.
const appEventRetentionDays = 90

// startAppEventPruning runs periodic pruning of old application events.
func (c *Controller) startAppEventPruning(ctx context.Context) {
	if ctx == nil || c.DS == nil {
		return
	}

	// Prune once at startup, but check for early cancellation first
	select {
	case <-ctx.Done():
		return
	default:
		c.pruneAppEvents(ctx)
	}

	// Then prune daily
	ticker := time.NewTicker(24 * time.Hour)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			c.pruneAppEvents(ctx)
		}
	}
}

// pruneAppEvents removes application events older than the retention period.
func (c *Controller) pruneAppEvents(ctx context.Context) {
	if c.DS == nil {
		return
	}
	pruned, err := c.DS.PruneAppEvents(ctx, appEventRetentionDays)
	if err != nil {
		c.LogWarnIfEnabled("Failed to prune app events", logger.Error(err))
		return
	}
	if pruned > 0 {
		c.LogInfoIfEnabled("Pruned old app events", logger.Int64("count", pruned))
	}
}
