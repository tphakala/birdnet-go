// internal/api/v2/media.go
package api

import (
	"bytes"
	"context"
	"fmt"
	"io/fs"
	"log"
	"log/slog"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"time"

	"github.com/labstack/echo/v4"
	"github.com/tphakala/birdnet-go/internal/conf"
	"github.com/tphakala/birdnet-go/internal/errors"
	"github.com/tphakala/birdnet-go/internal/logging"
	"github.com/tphakala/birdnet-go/internal/myaudio"
	"github.com/tphakala/birdnet-go/internal/securefs"
	"golang.org/x/sync/singleflight"
)

// Non-standard HTTP status codes
const (
	StatusClientClosedRequest = 499 // Nginx's non-standard status for client closed connection
)

// Spectrogram size constants
// Sizes are optimized for different UI contexts:
// - sm (400px): Compact display in lists and dashboards
// - md (800px): Standard detail view and review modals
// - lg (1000px): Large display for detailed analysis
// - xl (1200px): Maximum quality for expert review
const (
	SpectrogramSizeSm = 400
	SpectrogramSizeMd = 800
	SpectrogramSizeLg = 1000
	SpectrogramSizeXl = 1200
)

// spectrogramSizes maps size names to pixel widths
var spectrogramSizes = map[string]int{
	"sm": SpectrogramSizeSm,
	"md": SpectrogramSizeMd,
	"lg": SpectrogramSizeLg,
	"xl": SpectrogramSizeXl,
}

// Sentinel errors for media operations
var (
	// Audio file errors
	ErrAudioFileNotFound    = errors.NewStd("audio file not found")
	ErrInvalidAudioPath     = errors.NewStd("invalid audio path")
	ErrPathTraversalAttempt = errors.NewStd("security error: path attempts to traverse")
	ErrAudioFileNotReady    = errors.NewStd("audio file is not ready for processing")

	// Configuration errors
	ErrFFmpegNotConfigured = errors.NewStd("ffmpeg path not set in settings")
	ErrSoxNotConfigured    = errors.NewStd("sox path not set in settings")

	// Generation errors
	ErrSpectrogramGeneration = errors.NewStd("failed to generate spectrogram")

	// Image errors
	ErrImageNotFound             = errors.NewStd("image not found")
	ErrImageProviderNotAvailable = errors.NewStd("image provider not available")

	// Sentinel errors for nilnil cases
	ErrSpectrogramExists       = errors.NewStd("spectrogram already exists")
	ErrSpectrogramNotGenerated = errors.NewStd("spectrogram not generated")
)

// safeFilenamePattern is kept if needed elsewhere, but SecureFS handles validation now
// var safeFilenamePattern = regexp.MustCompile(`^[\p{L}\p{N}_\-.]+$`)

// Constants for spectrogram generation status
const (
	spectrogramStatusExists    = "exists"
	spectrogramStatusGenerated = "generated"
)

// Initialize media routes
func (c *Controller) initMediaRoutes() {
	if c.apiLogger != nil {
		c.apiLogger.Info("Initializing media routes")
	}

	// Original filename-based routes (keep for backward compatibility if needed, but ensure they use SFS)
	c.Group.GET("/media/audio/:filename", c.ServeAudioClip)
	c.Group.GET("/media/spectrogram/:filename", c.ServeSpectrogram)

	// ID-based routes using SFS
	c.Echo.GET("/api/v2/audio/:id", c.ServeAudioByID)
	c.Echo.GET("/api/v2/spectrogram/:id", c.ServeSpectrogramByID)

	// Convenient combined endpoint (redirects to ID-based internally)
	c.Group.GET("/media/audio", c.ServeAudioByQueryID)

	// Bird image endpoint
	c.Group.GET("/media/species-image", c.GetSpeciesImage)

	if c.apiLogger != nil {
		c.apiLogger.Info("Media routes initialized successfully")
	}
}

// translateSecureFSError handles SecureFS errors consistently across handler methods.
// It checks if the error is already an HTTPError from SecureFS and returns it directly,
// or maps specific error types to appropriate HTTP status codes.
func (c *Controller) translateSecureFSError(ctx echo.Context, err error, userMsg string) error {
	var httpErr *echo.HTTPError
	if errors.As(err, &httpErr) {
		// If it's already an HTTPError from SecureFS, just pass it through
		ctx.Logger().Debugf("SecureFS httpErr=%d internal=%v msg=%v",
			httpErr.Code, httpErr.Internal, httpErr.Message)
		// Log this as an error since it represents a failed request from SFS
		if c.apiLogger != nil {
			c.apiLogger.Error("SecureFS returned HTTP error",
				"error", err.Error(),
				"status_code", httpErr.Code,
				"path", ctx.Request().URL.Path,
				"ip", ctx.RealIP(),
			)
		}
		return httpErr
	}

	// Get tunnel info for logging
	isTunneled, _ := ctx.Get("is_tunneled").(bool)
	tunnelProvider, _ := ctx.Get("tunnel_provider").(string)

	// Check for specific error types and map to appropriate status codes
	switch {
	case errors.Is(err, securefs.ErrPathTraversal) || errors.Is(err, ErrPathTraversalAttempt):
		if c.apiLogger != nil {
			c.apiLogger.Warn("Path traversal attempt detected",
				"error", err.Error(),
				"path", ctx.Request().URL.Path,
				"ip", ctx.RealIP(),
				"tunneled", isTunneled,
				"tunnel_provider", tunnelProvider,
			)
		}
		return c.HandleError(ctx, err, "Invalid file path: attempted path traversal", http.StatusBadRequest)
	case errors.Is(err, securefs.ErrInvalidPath) || errors.Is(err, ErrInvalidAudioPath):
		if c.apiLogger != nil {
			c.apiLogger.Warn("Invalid file path provided",
				"error", err.Error(),
				"path", ctx.Request().URL.Path,
				"ip", ctx.RealIP(),
				"tunneled", isTunneled,
				"tunnel_provider", tunnelProvider,
			)
		}
		return c.HandleError(ctx, err, "Invalid file path specification", http.StatusBadRequest)
	case errors.Is(err, securefs.ErrAccessDenied):
		if c.apiLogger != nil {
			c.apiLogger.Warn("Access denied to resource",
				"error", err.Error(),
				"path", ctx.Request().URL.Path,
				"ip", ctx.RealIP(),
				"tunneled", isTunneled,
				"tunnel_provider", tunnelProvider,
			)
		}
		return c.HandleError(ctx, err, "Access denied to requested resource", http.StatusForbidden)
	case errors.Is(err, securefs.ErrNotRegularFile):
		if c.apiLogger != nil {
			c.apiLogger.Warn("Requested resource is not a regular file",
				"error", err.Error(),
				"path", ctx.Request().URL.Path,
				"ip", ctx.RealIP(),
				"tunneled", isTunneled,
				"tunnel_provider", tunnelProvider,
			)
		}
		return c.HandleError(ctx, err, "Requested resource is not a regular file", http.StatusForbidden)
	case errors.Is(err, os.ErrNotExist) || errors.Is(err, fs.ErrNotExist) || errors.Is(err, ErrAudioFileNotFound) || errors.Is(err, ErrImageNotFound):
		if c.apiLogger != nil {
			c.apiLogger.Info("Resource not found", // Info level as 404 is common
				"error", err.Error(),
				"path", ctx.Request().URL.Path,
				"ip", ctx.RealIP(),
				"tunneled", isTunneled,
				"tunnel_provider", tunnelProvider,
			)
		}
		return c.HandleError(ctx, err, "Resource not found", http.StatusNotFound)
	case errors.Is(err, context.DeadlineExceeded):
		if c.apiLogger != nil {
			c.apiLogger.Warn("Request timed out",
				"error", err.Error(),
				"path", ctx.Request().URL.Path,
				"ip", ctx.RealIP(),
				"tunneled", isTunneled,
				"tunnel_provider", tunnelProvider,
			)
		}
		return c.HandleError(ctx, err, "Request timed out", http.StatusRequestTimeout)
	case errors.Is(err, context.Canceled):
		if c.apiLogger != nil {
			c.apiLogger.Info("Request canceled by client",
				"error", err.Error(),
				"path", ctx.Request().URL.Path,
				"ip", ctx.RealIP(),
				"tunneled", isTunneled,
				"tunnel_provider", tunnelProvider,
			)
		}
		return c.HandleError(ctx, err, "Request was canceled", StatusClientClosedRequest)
	}

	// For other errors, log as error and use the provided user message with a 500 status
	if c.apiLogger != nil {
		c.apiLogger.Error("Unhandled SecureFS/media error",
			"error", err.Error(),
			"user_message", userMsg,
			"path", ctx.Request().URL.Path,
			"ip", ctx.RealIP(),
			"tunneled", isTunneled,
			"tunnel_provider", tunnelProvider,
		)
	}
	return c.HandleError(ctx, err, userMsg, http.StatusInternalServerError)
}

// parseRawParameter parses the raw query parameter for spectrogram generation.
// It defaults to true for backward compatibility with existing cached spectrograms.
// Accepts: "true", "false", "1", "0", "t", "f", "yes", "no", "on", "off"
func parseRawParameter(rawParam string) bool {
	// Default to true for backward compatibility
	if rawParam == "" {
		return true
	}

	// Normalize the parameter to lowercase for consistent parsing
	normalizedParam := strings.ToLower(rawParam)

	// First try strconv.ParseBool for standard values
	if parsedRaw, err := strconv.ParseBool(normalizedParam); err == nil {
		return parsedRaw
	}

	// Handle additional common boolean representations
	switch normalizedParam {
	case "yes", "on":
		return true
	case "no", "off":
		return false
	default:
		// Default to true for invalid values
		return true
	}
}

// ServeAudioClip serves an audio clip file by filename using SecureFS
func (c *Controller) ServeAudioClip(ctx echo.Context) error {
	filename := ctx.Param("filename")
	if filename == "" {
		if c.apiLogger != nil {
			c.apiLogger.Error("Missing filename parameter for ServeAudioClip",
				"path", ctx.Request().URL.Path,
				"ip", ctx.RealIP(),
			)
		}
		return c.HandleError(ctx, fmt.Errorf("missing filename"), "Filename parameter is required", http.StatusBadRequest)
	}

	if c.apiLogger != nil {
		c.apiLogger.Info("Serving audio clip by filename",
			"filename", filename,
			"path", ctx.Request().URL.Path,
			"ip", ctx.RealIP(),
		)
	}

	// Serve the file using SecureFS. It handles path validation and serves the file.
	// ServeRelativeFile is expected to return appropriate echo.HTTPErrors (400, 404, 500).
	err := c.SFS.ServeRelativeFile(ctx, filename)

	if err != nil {
		// Error logging is handled within translateSecureFSError
		return c.translateSecureFSError(ctx, err, "Failed to serve audio clip due to an unexpected error")
	}

	if c.apiLogger != nil {
		c.apiLogger.Info("Successfully served audio clip by filename",
			"filename", filename,
			"path", ctx.Request().URL.Path,
			"ip", ctx.RealIP(),
		)
	}

	// If err is nil, ServeRelativeFile handled the response successfully
	return nil
}

// ServeAudioByID serves an audio clip file based on note ID using SecureFS
func (c *Controller) ServeAudioByID(ctx echo.Context) error {
	noteID := ctx.Param("id")
	if noteID == "" {
		return c.HandleError(ctx, fmt.Errorf("missing ID"), "Note ID is required", http.StatusBadRequest)
	}

	clipPath, err := c.DS.GetNoteClipPath(noteID)
	if err != nil {
		// Check if error is due to record not found
		if errors.Is(err, os.ErrNotExist) || strings.Contains(err.Error(), "not found") { // Adapt based on datastore error type
			return c.HandleError(ctx, err, "Clip path not found for note ID", http.StatusNotFound)
		}
		return c.HandleError(ctx, err, "Failed to get clip path for note", http.StatusInternalServerError)
	}

	if clipPath == "" {
		return c.HandleError(ctx, fmt.Errorf("no audio file found"), "No audio clip available for this note", http.StatusNotFound)
	}

	// Extract the original filename from the clip path for download
	originalFilename := filepath.Base(clipPath)
	if originalFilename != "" && originalFilename != "." && originalFilename != "/" {
		// Set Content-Disposition header to preserve original filename when downloading
		ctx.Response().Header().Set("Content-Disposition", fmt.Sprintf("attachment; filename=%q", originalFilename))
	}

	// Serve the file using SecureFS. It handles path validation (relative/absolute within baseDir).
	// ServeFile internally calls relativePath which ensures the path is within the SecureFS baseDir.
	// Use ServeRelativeFile as clipPath is already relative to the baseDir
	err = c.SFS.ServeRelativeFile(ctx, clipPath)
	if err != nil {
		return c.translateSecureFSError(ctx, err, "Failed to serve audio clip due to an unexpected error")
	}

	return nil
}

// spectrogramHTTPError handles common spectrogram generation errors and converts them to appropriate HTTP responses
func (c *Controller) spectrogramHTTPError(ctx echo.Context, err error) error {
	switch {
	case errors.Is(err, ErrAudioFileNotReady) || errors.Is(err, myaudio.ErrAudioFileIncomplete):
		// Audio file is not ready yet - client should retry
		// Set Retry-After header to suggest when to retry (in seconds)
		ctx.Response().Header().Set("Retry-After", spectrogramRetryAfterSeconds)
		// Use 503 Service Unavailable to indicate temporary unavailability
		return c.HandleError(ctx, err, "Audio file is still being processed, please retry", http.StatusServiceUnavailable)
	case errors.Is(err, ErrAudioFileNotFound) || errors.Is(err, os.ErrNotExist):
		// Handle cases where the source audio file doesn't exist
		return c.HandleError(ctx, err, "Source audio file not found", http.StatusNotFound)
	case errors.Is(err, ErrInvalidAudioPath) || errors.Is(err, ErrPathTraversalAttempt):
		// Handle path traversal or invalid path errors
		return c.HandleError(ctx, err, "Invalid audio file path specified", http.StatusBadRequest)
	case errors.Is(err, context.DeadlineExceeded):
		return c.HandleError(ctx, err, "Spectrogram generation timed out", http.StatusRequestTimeout)
	case errors.Is(err, context.Canceled):
		// Use StatusClientClosedRequest (non-standard, but common for Nginx)
		return c.HandleError(ctx, err, "Spectrogram generation canceled by client", StatusClientClosedRequest)
	case errors.Is(err, ErrFFmpegNotConfigured) || errors.Is(err, ErrSoxNotConfigured):
		// Handle configuration errors
		return c.HandleError(ctx, err, "Server configuration error preventing spectrogram generation", http.StatusInternalServerError)
	default:
		// Default to internal server error for other generation failures
		return c.HandleError(ctx, err, "Failed to generate spectrogram", http.StatusInternalServerError)
	}
}

// ServeSpectrogramByID serves a spectrogram image based on note ID using SecureFS
//
// Route: GET /api/v2/spectrogram/:id
//
// Query Parameters:
//   - size: Spectrogram size - "sm" (400px), "md" (800px), "lg" (1000px), "xl" (1200px)
//     Default: "md"
//   - width: Legacy parameter for custom width (1-2000px). Ignored if 'size' is present.
//   - raw: Whether to generate raw spectrogram without axes/legends
//     Default: true (for backward compatibility with cached spectrograms)
//     Accepts: "true", "false", "1", "0", "t", "f", "yes", "no", "on", "off"
//
// The raw parameter defaults to true to maintain compatibility with existing cached
// spectrograms from the old HTMX API which generated raw spectrograms by default.
func (c *Controller) ServeSpectrogramByID(ctx echo.Context) error {
	noteID := ctx.Param("id")
	if noteID == "" {
		if c.apiLogger != nil {
			c.apiLogger.Error("Missing note ID for spectrogram request",
				"path", ctx.Request().URL.Path,
				"ip", ctx.RealIP())
		}
		return c.HandleError(ctx, fmt.Errorf("missing ID"), "Note ID is required", http.StatusBadRequest)
	}

	if c.apiLogger != nil {
		c.apiLogger.Debug("Spectrogram requested by ID",
			"note_id", noteID,
			"query_params", ctx.QueryString(),
			"path", ctx.Request().URL.Path,
			"ip", ctx.RealIP())
	}

	clipPath, err := c.DS.GetNoteClipPath(noteID)
	if err != nil {
		if c.apiLogger != nil {
			c.apiLogger.Error("Failed to get clip path from database",
				"note_id", noteID,
				"error", err.Error(),
				"path", ctx.Request().URL.Path,
				"ip", ctx.RealIP())
		}
		if errors.Is(err, os.ErrNotExist) || strings.Contains(err.Error(), "not found") {
			return c.HandleError(ctx, err, "Clip path not found for note ID", http.StatusNotFound)
		}
		return c.HandleError(ctx, err, "Failed to get clip path for note", http.StatusInternalServerError)
	}

	if clipPath == "" {
		if c.apiLogger != nil {
			c.apiLogger.Warn("Empty clip path for note",
				"note_id", noteID,
				"path", ctx.Request().URL.Path,
				"ip", ctx.RealIP())
		}
		return c.HandleError(ctx, fmt.Errorf("no audio file found"), "No audio clip available for this note", http.StatusNotFound)
	}

	// Parse size parameter
	width := SpectrogramSizeMd // Default width (md)
	sizeStr := ctx.QueryParam("size")
	if sizeStr != "" {
		if validWidth, ok := spectrogramSizes[sizeStr]; ok {
			width = validWidth
		}
		// Invalid size parameter falls back to width parameter or default
	}

	// Legacy width parameter support
	widthStr := ctx.QueryParam("width")
	if widthStr != "" && sizeStr == "" {
		parsedWidth, err := strconv.Atoi(widthStr)
		if err == nil && parsedWidth > 0 && parsedWidth <= 2000 {
			width = parsedWidth
		}
	}

	// Parse raw spectrogram parameter
	raw := parseRawParameter(ctx.QueryParam("raw"))

	if c.apiLogger != nil {
		c.apiLogger.Debug("Spectrogram parameters parsed",
			"note_id", noteID,
			"clip_path", clipPath,
			"width", width,
			"raw", raw,
			"size_param", sizeStr,
			"width_param", widthStr,
			"path", ctx.Request().URL.Path,
			"ip", ctx.RealIP())
	}

	// Pass the request context for cancellation/timeout
	generationStart := time.Now()
	spectrogramPath, err := c.generateSpectrogram(ctx.Request().Context(), clipPath, width, raw)
	generationDuration := time.Since(generationStart)

	if err != nil {
		if c.apiLogger != nil {
			c.apiLogger.Error("Spectrogram generation failed",
				"note_id", noteID,
				"clip_path", clipPath,
				"error", err.Error(),
				"duration_ms", generationDuration.Milliseconds(),
				"path", ctx.Request().URL.Path,
				"ip", ctx.RealIP())
		}
		return c.spectrogramHTTPError(ctx, err)
	}

	if c.apiLogger != nil {
		c.apiLogger.Debug("Spectrogram path determined",
			"note_id", noteID,
			"spectrogram_path", spectrogramPath,
			"duration_ms", generationDuration.Milliseconds(),
			"path", ctx.Request().URL.Path,
			"ip", ctx.RealIP())
	}

	// Serve the generated spectrogram using SecureFS
	serveStart := time.Now()
	err = c.SFS.ServeRelativeFile(ctx, spectrogramPath)
	serveDuration := time.Since(serveStart)

	if err != nil {
		if c.apiLogger != nil {
			c.apiLogger.Error("Failed to serve spectrogram file",
				"note_id", noteID,
				"spectrogram_path", spectrogramPath,
				"error", err.Error(),
				"serve_duration_ms", serveDuration.Milliseconds(),
				"path", ctx.Request().URL.Path,
				"ip", ctx.RealIP())
		}
		return c.translateSecureFSError(ctx, err, "Failed to serve spectrogram image")
	}

	if c.apiLogger != nil {
		c.apiLogger.Debug("Spectrogram served successfully",
			"note_id", noteID,
			"spectrogram_path", spectrogramPath,
			"serve_duration_ms", serveDuration.Milliseconds(),
			"total_duration_ms", time.Since(generationStart).Milliseconds(),
			"path", ctx.Request().URL.Path,
			"ip", ctx.RealIP())
	}
	return nil
}

// ServeAudioByQueryID serves an audio clip using query parameter for ID
func (c *Controller) ServeAudioByQueryID(ctx echo.Context) error {
	noteID := ctx.QueryParam("id")
	if noteID == "" {
		return c.HandleError(ctx, fmt.Errorf("missing ID"), "Note ID is required as query parameter", http.StatusBadRequest)
	}

	// Delegate to the ID handler
	ctx.SetParamNames("id")
	ctx.SetParamValues(noteID)
	return c.ServeAudioByID(ctx)
}

// ServeSpectrogram serves a spectrogram image by filename using SecureFS
//
// Route: GET /api/v2/media/spectrogram/:filename
//
// Query Parameters:
//   - size: Spectrogram size - "sm" (400px), "md" (800px), "lg" (1000px), "xl" (1200px)
//     Default: "md"
//   - width: Legacy parameter for custom width (1-2000px). Ignored if 'size' is present.
//   - raw: Whether to generate raw spectrogram without axes/legends
//     Default: true (for backward compatibility with cached spectrograms)
//     Accepts: "true", "false", "1", "0", "t", "f", "yes", "no", "on", "off"
//
// The raw parameter defaults to true to maintain compatibility with existing cached
// spectrograms from the old HTMX API which generated raw spectrograms by default.
func (c *Controller) ServeSpectrogram(ctx echo.Context) error {
	filename := ctx.Param("filename")

	// Parse size parameter
	width := SpectrogramSizeMd // Default width (md)
	sizeStr := ctx.QueryParam("size")
	if sizeStr != "" {
		if validWidth, ok := spectrogramSizes[sizeStr]; ok {
			width = validWidth
		}
		// Invalid size parameter falls back to width parameter or default
	}

	// Legacy width parameter support
	widthStr := ctx.QueryParam("width")
	if widthStr != "" && sizeStr == "" {
		parsedWidth, err := strconv.Atoi(widthStr)
		if err == nil && parsedWidth > 0 && parsedWidth <= 2000 {
			width = parsedWidth
		}
	}

	// Parse raw spectrogram parameter
	raw := parseRawParameter(ctx.QueryParam("raw"))

	// Pass the request context for cancellation/timeout
	spectrogramPath, err := c.generateSpectrogram(ctx.Request().Context(), filename, width, raw)
	if err != nil {
		return c.spectrogramHTTPError(ctx, err)
	}

	// Serve the generated spectrogram using SecureFS
	err = c.SFS.ServeRelativeFile(ctx, spectrogramPath)
	if err != nil {
		return c.translateSecureFSError(ctx, err, "Failed to serve spectrogram image")
	}
	return nil
}

// maxConcurrentSpectrograms limits concurrent spectrogram generations to avoid overloading the system.
// Set to 4 to match the number of CPU cores on Raspberry Pi 4/5, which is the most common
// deployment platform for BirdNET-Go. This prevents memory exhaustion and ensures
// responsive performance on resource-constrained devices.
const maxConcurrentSpectrograms = 4

// spectrogramRetryAfterSeconds is the suggested retry delay in seconds for 503 responses
// when audio files are not yet ready for processing
const spectrogramRetryAfterSeconds = "2"

var (
	spectrogramSemaphore = make(chan struct{}, maxConcurrentSpectrograms)
	spectrogramGroup     singleflight.Group // Prevents duplicate generations
)

// Package-level logger for spectrogram generation
var (
	spectrogramLogger      *slog.Logger
	spectrogramLevelVar    = new(slog.LevelVar) // Dynamic level control
	closeSpectrogramLogger func() error
)

func init() {
	// Initialize spectrogram generation logger
	// This creates a dedicated log file at logs/spectrogram-generation.log
	var err error

	// Set initial level to Debug for comprehensive logging
	spectrogramLevelVar.Set(slog.LevelDebug)

	spectrogramLogger, closeSpectrogramLogger, err = logging.NewFileLogger(
		"logs/spectrogram-generation.log",
		"spectrogram-generation",
		spectrogramLevelVar,
	)

	if err != nil || spectrogramLogger == nil {
		// Fallback to default logger if file logger creation fails
		spectrogramLogger = slog.Default().With("service", "spectrogram-generation")
		closeSpectrogramLogger = func() error { return nil }
		// Log the error so we know why the file logger failed
		if err != nil {
			spectrogramLogger.Error("Failed to initialize spectrogram generation file logger", "error", err)
		}
	}
}

// CloseSpectrogramLogger releases the file logger resources to prevent resource leaks.
// This should be called during application shutdown.
func CloseSpectrogramLogger() error {
	if closeSpectrogramLogger != nil {
		return closeSpectrogramLogger()
	}
	return nil
}

// generateSpectrogram creates a spectrogram image for the given audio file path (relative to SecureFS root).
// It accepts a context for cancellation and timeout.
// It returns the relative path to the generated spectrogram, suitable for use with c.SFS.ServeFile.
// Optimized: Fast path check happens before expensive audio validation.
//
//nolint:gocognit // Complex but necessary for comprehensive spectrogram generation with fallbacks
func (c *Controller) generateSpectrogram(ctx context.Context, audioPath string, width int, raw bool) (string, error) {
	start := time.Now()
	spectrogramLogger.Debug("Spectrogram generation requested",
		"audio_path", audioPath,
		"width", width,
		"raw", raw,
		"request_time", start.Format("2006-01-02 15:04:05"))

	// Step 1: Validate the audio path for security (must happen first)
	relAudioPath, err := c.SFS.ValidateRelativePath(audioPath)
	if err != nil {
		// Use proper error type checking instead of string matching
		if errors.Is(err, securefs.ErrPathTraversal) {
			combined := errors.Join(ErrPathTraversalAttempt, err)
			return "", fmt.Errorf("%w", combined)
		}
		combined := errors.Join(ErrInvalidAudioPath, err)
		return "", fmt.Errorf("%w", combined)
	}

	// Step 2: Calculate spectrogram paths early (needed for fast path check)
	// Get the base filename and directory relative to the secure root
	relBaseFilename := strings.TrimSuffix(filepath.Base(relAudioPath), filepath.Ext(relAudioPath))
	relAudioDir := filepath.Dir(relAudioPath)

	// Generate spectrogram filename compatible with old HTMX API format
	var spectrogramFilename string
	if raw {
		// Raw spectrograms use old API format: filename_400px.png (for cache compatibility)
		spectrogramFilename = fmt.Sprintf("%s_%dpx.png", relBaseFilename, width)
	} else {
		// Spectrograms with legends use new suffix: filename_400px-legend.png
		spectrogramFilename = fmt.Sprintf("%s_%dpx-legend.png", relBaseFilename, width)
	}

	// Since we're constructing the spectrogram path from an already-validated audio path
	// and appending a simple formatted filename, we can safely construct the path without
	// re-validating. The path components are all known to be safe.
	relSpectrogramPath := filepath.Join(relAudioDir, spectrogramFilename)

	spectrogramLogger.Debug("Spectrogram path constructed",
		"audio_path", audioPath,
		"audio_ext", filepath.Ext(relAudioPath),
		"base_filename", relBaseFilename,
		"audio_dir", relAudioDir,
		"spectrogram_filename", spectrogramFilename,
		"relative_spectrogram_path", relSpectrogramPath,
		"width", width,
		"raw", raw)

	// Generate a unique key for this spectrogram generation request
	// Include both the path and width to ensure uniqueness
	spectrogramKey := fmt.Sprintf("%s:%d:%t", relSpectrogramPath, width, raw)

	// Step 3: FAST PATH - Check if spectrogram already exists
	// This happens BEFORE any expensive operations like audio validation
	spectrogramLogger.Debug("Fast path check: checking if spectrogram exists",
		"spectrogram_key", spectrogramKey,
		"relative_spectrogram_path", relSpectrogramPath,
		"width", width,
		"raw", raw)

	if _, err := c.SFS.StatRel(relSpectrogramPath); err == nil {
		spectrogramLogger.Debug("Fast path HIT: spectrogram already exists, returning immediately",
			"spectrogram_key", spectrogramKey,
			"relative_spectrogram_path", relSpectrogramPath,
			"total_duration_ms", time.Since(start).Milliseconds())
		return relSpectrogramPath, nil
	} else if !os.IsNotExist(err) {
		// Unexpected error checking file - let's log it but continue with generation
		spectrogramLogger.Debug("Fast path: unexpected error checking existing spectrogram, proceeding with generation",
			"spectrogram_key", spectrogramKey,
			"error", err.Error())
	} else {
		spectrogramLogger.Debug("Fast path MISS: spectrogram does not exist, will need to generate",
			"spectrogram_key", spectrogramKey,
			"relative_spectrogram_path", relSpectrogramPath)
	}

	// Step 4: Spectrogram doesn't exist, now check if audio file exists
	// This check was moved after the fast path to avoid unnecessary stat calls
	spectrogramLogger.Debug("Checking if audio file exists",
		"relative_audio_path", relAudioPath)

	if audioStat, err := c.SFS.StatRel(relAudioPath); err != nil {
		// Handle file not found specifically, otherwise wrap
		if os.IsNotExist(err) {
			spectrogramLogger.Debug("Audio file does not exist",
				"relative_audio_path", relAudioPath,
				"error", err.Error())
			combined := errors.Join(ErrAudioFileNotFound, err)
			return "", fmt.Errorf("%w at '%s'", combined, relAudioPath)
		}
		spectrogramLogger.Debug("Error checking audio file",
			"relative_audio_path", relAudioPath,
			"error", err.Error())
		return "", fmt.Errorf("error checking audio file '%s': %w", relAudioPath, err)
	} else {
		spectrogramLogger.Debug("Audio file exists",
			"relative_audio_path", relAudioPath,
			"size_bytes", audioStat.Size(),
			"mod_time", audioStat.ModTime().Format("2006-01-02 15:04:05"))
	}

	// Step 5: Validate that the audio file is complete and ready for processing
	// This expensive operation only happens if we need to generate a spectrogram
	absAudioPath := filepath.Join(c.SFS.BaseDir(), relAudioPath)

	spectrogramLogger.Debug("Starting audio validation with FFprobe",
		"abs_audio_path", absAudioPath,
		"spectrogram_key", spectrogramKey)

	validationStart := time.Now()
	validationResult, err := myaudio.ValidateAudioFileWithRetry(ctx, absAudioPath)
	validationDuration := time.Since(validationStart)

	if err != nil {
		// Context errors should be propagated immediately
		if errors.Is(err, context.DeadlineExceeded) || errors.Is(err, context.Canceled) {
			spectrogramLogger.Warn("Audio validation canceled or timed out",
				"audio_path", audioPath,
				"abs_audio_path", absAudioPath,
				"error", err.Error(),
				"validation_duration_ms", validationDuration.Milliseconds(),
				"spectrogram_key", spectrogramKey)
			return "", err
		}
		// Other validation errors
		spectrogramLogger.Error("Audio validation failed with FFprobe",
			"audio_path", audioPath,
			"abs_audio_path", absAudioPath,
			"error", err.Error(),
			"validation_duration_ms", validationDuration.Milliseconds(),
			"spectrogram_key", spectrogramKey)
		combined := errors.Join(ErrAudioFileNotReady, err)
		return "", fmt.Errorf("%w", combined)
	}

	// Check if the file is ready
	if !validationResult.IsValid {
		spectrogramLogger.Info("Audio file not ready for processing, client should retry",
			"audio_path", audioPath,
			"abs_audio_path", absAudioPath,
			"file_size", validationResult.FileSize,
			"is_complete", validationResult.IsComplete,
			"is_valid", validationResult.IsValid,
			"retry_after_ms", validationResult.RetryAfter.Milliseconds(),
			"validation_duration_ms", validationDuration.Milliseconds(),
			"validation_error", validationResult.Error,
			"spectrogram_key", spectrogramKey)

		// Track retry metrics
		if c.apiLogger != nil {
			c.apiLogger.Info("Spectrogram generation deferred - audio not ready",
				"audio_path", audioPath,
				"file_size", validationResult.FileSize,
				"retry_after_ms", validationResult.RetryAfter.Milliseconds(),
				"component", "media.spectrogram",
				"metric_type", "audio_not_ready")
		}

		// Return a specific error that indicates the file is not ready
		// This will be handled by the HTTP handler to return 503
		if validationResult.Error != nil {
			combined := errors.Join(ErrAudioFileNotReady, validationResult.Error)
			return "", fmt.Errorf("%w", combined)
		}
		return "", ErrAudioFileNotReady
	}

	spectrogramLogger.Debug("Audio file validated successfully with FFprobe",
		"audio_path", audioPath,
		"abs_audio_path", absAudioPath,
		"duration_seconds", validationResult.Duration,
		"format", validationResult.Format,
		"file_size_bytes", validationResult.FileSize,
		"sample_rate", validationResult.SampleRate,
		"channels", validationResult.Channels,
		"bitrate", validationResult.BitRate,
		"is_valid", validationResult.IsValid,
		"is_complete", validationResult.IsComplete,
		"validation_duration_ms", validationDuration.Milliseconds(),
		"spectrogram_key", spectrogramKey)

	// Absolute path for the spectrogram on the host filesystem
	absSpectrogramPath := filepath.Join(c.SFS.BaseDir(), relSpectrogramPath)

	// Step 6: Proceed with generation (spectrogram doesn't exist)
	spectrogramLogger.Debug("Proceeding with spectrogram generation",
		"spectrogram_key", spectrogramKey,
		"abs_audio_path", absAudioPath,
		"abs_spectrogram_path", absSpectrogramPath,
		"width", width,
		"raw", raw)

	// Acquire semaphore BEFORE singleflight to eliminate double bottleneck
	// This prevents the old issue where requests would queue at singleflight AND then at semaphore
	spectrogramLogger.Debug("Attempting to acquire semaphore slot",
		"spectrogram_key", spectrogramKey,
		"current_slots_used", len(spectrogramSemaphore),
		"max_slots", maxConcurrentSpectrograms)

	select {
	case spectrogramSemaphore <- struct{}{}:
		// Successfully acquired semaphore slot
		spectrogramLogger.Debug("Semaphore slot acquired successfully",
			"spectrogram_key", spectrogramKey,
			"remaining_slots", maxConcurrentSpectrograms-len(spectrogramSemaphore)-1,
			"slots_in_use", len(spectrogramSemaphore)+1)
	case <-ctx.Done():
		// Context canceled while waiting for semaphore
		spectrogramLogger.Debug("Context canceled while waiting for semaphore",
			"spectrogram_key", spectrogramKey,
			"error", ctx.Err())
		return "", ctx.Err()
	}

	defer func() {
		<-spectrogramSemaphore
		spectrogramLogger.Debug("Semaphore slot released",
			"spectrogram_key", spectrogramKey,
			"total_duration_ms", time.Since(start).Milliseconds())
	}()

	// Use singleflight to prevent duplicate generations (now with semaphore already acquired)
	spectrogramLogger.Debug("Starting singleflight generation",
		"spectrogram_key", spectrogramKey)

	_, err, _ = spectrogramGroup.Do(spectrogramKey, func() (any, error) {
		// Fast path inside the group – now race-free
		spectrogramLogger.Debug("Inside singleflight group, double-checking if spectrogram exists",
			"spectrogram_key", spectrogramKey)

		if _, err := c.SFS.StatRel(relSpectrogramPath); err == nil {
			spectrogramLogger.Debug("Spectrogram already exists (race condition avoided)",
				"spectrogram_path", relSpectrogramPath,
				"spectrogram_key", spectrogramKey)
			return spectrogramStatusExists, nil // File exists, no need to generate
		} else if !os.IsNotExist(err) {
			// An unexpected error occurred checking for the spectrogram
			spectrogramLogger.Debug("Error checking existing spectrogram in singleflight",
				"spectrogram_path", relSpectrogramPath,
				"error", err)
			return nil, fmt.Errorf("error checking for existing spectrogram '%s': %w", relSpectrogramPath, err)
		}

		spectrogramLogger.Debug("Starting actual spectrogram generation (file does not exist)",
			"spectrogram_key", spectrogramKey,
			"abs_audio_path", absAudioPath,
			"abs_spectrogram_path", absSpectrogramPath,
			"width", width,
			"raw", raw,
			"generator", "sox_with_ffmpeg_fallback")

		generationStart := time.Now()

		// --- Generate Spectrogram ---
		spectrogramLogger.Debug("Attempting SoX spectrogram generation",
			"spectrogram_key", spectrogramKey,
			"abs_audio_path", absAudioPath)

		if err := createSpectrogramWithSoX(ctx, absAudioPath, absSpectrogramPath, width, raw, c.Settings); err != nil {
			spectrogramLogger.Debug("SoX generation failed, will try FFmpeg fallback",
				"spectrogram_key", spectrogramKey,
				"sox_error", err.Error(),
				"sox_duration_ms", time.Since(generationStart).Milliseconds(),
				"abs_audio_path", absAudioPath)

			fallbackStart := time.Now()
			spectrogramLogger.Debug("Attempting FFmpeg fallback for spectrogram generation",
				"spectrogram_key", spectrogramKey,
				"abs_audio_path", absAudioPath)

			// Pass the context down to the fallback function as well.
			if err2 := createSpectrogramWithFFmpeg(ctx, absAudioPath, absSpectrogramPath, width, raw, c.Settings); err2 != nil {
				spectrogramLogger.Error("Both SoX and FFmpeg generation failed",
					"spectrogram_key", spectrogramKey,
					"sox_error", err.Error(),
					"ffmpeg_error", err2.Error(),
					"sox_duration_ms", fallbackStart.Sub(generationStart).Milliseconds(),
					"ffmpeg_duration_ms", time.Since(fallbackStart).Milliseconds(),
					"total_generation_duration_ms", time.Since(generationStart).Milliseconds(),
					"abs_audio_path", absAudioPath,
					"abs_spectrogram_path", absSpectrogramPath)

				// Check for context errors specifically (propagate them up)
				if errors.Is(err, context.DeadlineExceeded) || errors.Is(err2, context.DeadlineExceeded) {
					// Return the specific context error to be handled by the caller
					if errors.Is(err, context.DeadlineExceeded) {
						return nil, err
					}
					return nil, err2
				}
				if errors.Is(err, context.Canceled) || errors.Is(err2, context.Canceled) {
					// Return the specific context error to be handled by the caller
					if errors.Is(err, context.Canceled) {
						return nil, err
					}
					return nil, err2
				}
				// Return a combined error for general failures
				return nil, fmt.Errorf("%w: SoX error: %w, FFmpeg error: %w",
					ErrSpectrogramGeneration, err, err2)
			}
			spectrogramLogger.Debug("Spectrogram generation successful using FFmpeg fallback",
				"spectrogram_key", spectrogramKey,
				"abs_audio_path", absAudioPath,
				"sox_duration_ms", fallbackStart.Sub(generationStart).Milliseconds(),
				"ffmpeg_duration_ms", time.Since(fallbackStart).Milliseconds(),
				"total_generation_duration_ms", time.Since(generationStart).Milliseconds())
		} else {
			spectrogramLogger.Debug("Spectrogram generation successful using SoX",
				"spectrogram_key", spectrogramKey,
				"abs_audio_path", absAudioPath,
				"generation_duration_ms", time.Since(generationStart).Milliseconds())
		}
		return spectrogramStatusGenerated, nil // Successfully generated, no error
	})

	if err != nil {
		spectrogramLogger.Debug("Spectrogram generation failed",
			"spectrogram_key", spectrogramKey,
			"error", err.Error(),
			"total_duration_ms", time.Since(start).Milliseconds())
		return "", fmt.Errorf("failed to generate spectrogram: %w", err)
	}

	spectrogramLogger.Debug("Spectrogram generation completed successfully",
		"spectrogram_key", spectrogramKey,
		"relative_spectrogram_path", relSpectrogramPath,
		"total_duration_ms", time.Since(start).Milliseconds())

	// Return the relative path of the newly created spectrogram
	return relSpectrogramPath, nil
}

// --- Spectrogram Generation Helpers ---

// createSpectrogramWithSoX generates a spectrogram using ffmpeg and SoX.
// Accepts a context for timeout and cancellation.
// Requires absolute paths for external commands.
func createSpectrogramWithSoX(ctx context.Context, absAudioClipPath, absSpectrogramPath string, width int, raw bool, settings *conf.Settings) error {
	start := time.Now()
	spectrogramLogger.Debug("Starting SoX spectrogram generation",
		"abs_audio_path", absAudioClipPath,
		"abs_spectrogram_path", absSpectrogramPath,
		"width", width,
		"raw", raw)

	ffmpegBinary := settings.Realtime.Audio.FfmpegPath
	soxBinary := settings.Realtime.Audio.SoxPath

	// Check if the file extension is supported directly by SoX without needing FFmpeg
	ext := strings.ToLower(filepath.Ext(absAudioClipPath))
	ext = strings.TrimPrefix(ext, ".")
	useFFmpeg := true
	for _, soxType := range settings.Realtime.Audio.SoxAudioTypes {
		soxType = strings.TrimPrefix(strings.ToLower(soxType), ".")
		if ext == soxType {
			useFFmpeg = false
			break
		}
	}

	// Only check for FFmpeg if we need to use it
	if useFFmpeg && ffmpegBinary == "" {
		return ErrFFmpegNotConfigured
	}

	// SoX is always required
	if soxBinary == "" {
		return ErrSoxNotConfigured
	}

	// Create context with timeout (use the passed-in context as parent)
	ctx, cancel := context.WithTimeout(ctx, 60*time.Second)
	defer cancel()

	// NOTE: Semaphore is now acquired in generateSpectrogram() to fix double bottleneck
	// No longer acquiring semaphore here

	heightStr := strconv.Itoa(width / 2)
	widthStr := strconv.Itoa(width)

	var cmd *exec.Cmd
	var soxCmd *exec.Cmd

	if useFFmpeg {
		ffmpegArgs := []string{"-hide_banner", "-i", absAudioClipPath, "-f", "sox", "-"}
		soxArgs := append([]string{"-t", "sox", "-"}, getSoxSpectrogramArgs(ctx, widthStr, heightStr, absSpectrogramPath, absAudioClipPath, raw)...)

		if runtime.GOOS == "windows" {
			// #nosec G204 - ffmpegBinary and soxBinary are validated by ValidateToolPath/exec.LookPath
			cmd = exec.CommandContext(ctx, ffmpegBinary, ffmpegArgs...)
			// #nosec G204 - soxBinary is validated by exec.LookPath during config initialization
			soxCmd = exec.CommandContext(ctx, soxBinary, soxArgs...)
		} else {
			// #nosec G204 - ffmpegBinary is validated by ValidateToolPath/exec.LookPath
			cmd = exec.CommandContext(ctx, "nice", append([]string{"-n", "19", ffmpegBinary}, ffmpegArgs...)...)
			// #nosec G204 - soxBinary is validated by exec.LookPath during config initialization
			soxCmd = exec.CommandContext(ctx, "nice", append([]string{"-n", "19", soxBinary}, soxArgs...)...)
		}

		var err error
		soxCmd.Stdin, err = cmd.StdoutPipe()
		if err != nil {
			return fmt.Errorf("error creating pipe: %w", err)
		}

		var ffmpegOutput, soxOutput bytes.Buffer
		cmd.Stderr = &ffmpegOutput
		soxCmd.Stderr = &soxOutput

		runtime.Gosched()

		if err := soxCmd.Start(); err != nil {
			return fmt.Errorf("error starting SoX command: %w", err)
		}

		if err := cmd.Run(); err != nil {
			if killErr := soxCmd.Process.Kill(); killErr != nil {
				log.Printf("Failed to kill SoX process: %v", killErr)
			}
			waitErr := soxCmd.Wait()
			var additionalInfo string
			if waitErr != nil && !os.IsNotExist(waitErr) {
				additionalInfo = fmt.Sprintf("sox wait error: %v", waitErr)
			}
			return fmt.Errorf("ffmpeg command failed: %w\nffmpeg output: %s\nsox output: %s\n%s",
				err, ffmpegOutput.String(), soxOutput.String(), additionalInfo)
		}

		runtime.Gosched()
		if err := soxCmd.Wait(); err != nil {
			return fmt.Errorf("SoX command failed: %w\nffmpeg output: %s\nsox output: %s",
				err, ffmpegOutput.String(), soxOutput.String())
		}
		runtime.Gosched()
	} else {
		soxArgs := append([]string{absAudioClipPath}, getSoxSpectrogramArgs(ctx, widthStr, heightStr, absSpectrogramPath, absAudioClipPath, raw)...)

		if runtime.GOOS == "windows" {
			// #nosec G204 - soxBinary is validated by exec.LookPath during config initialization
			soxCmd = exec.CommandContext(ctx, soxBinary, soxArgs...)
		} else {
			// #nosec G204 - soxBinary is validated by exec.LookPath during config initialization
			soxCmd = exec.CommandContext(ctx, "nice", append([]string{"-n", "19", soxBinary}, soxArgs...)...)
		}

		var soxOutput bytes.Buffer
		soxCmd.Stderr = &soxOutput
		soxCmd.Stdout = &soxOutput

		runtime.Gosched()
		if err := soxCmd.Run(); err != nil {
			return fmt.Errorf("SoX command failed: %w\nOutput: %s", err, soxOutput.String())
		}
		runtime.Gosched()
	}

	spectrogramLogger.Debug("SoX spectrogram generation completed successfully",
		"abs_audio_path", absAudioClipPath,
		"duration_ms", time.Since(start).Milliseconds(),
		"use_ffmpeg", useFFmpeg)

	return nil
}

// getSoxSpectrogramArgs returns the common SoX arguments compatible with old HTMX API.
func getSoxSpectrogramArgs(ctx context.Context, widthStr, heightStr, absSpectrogramPath, audioPath string, raw bool) []string {
	const dynamicRange = "100"

	// Get actual audio duration instead of using hardcoded capture length
	// Use a timeout context to prevent hanging
	durationCtx, cancel := context.WithTimeout(ctx, 3*time.Second)
	defer cancel()

	duration, err := myaudio.GetAudioDuration(durationCtx, audioPath)
	if err != nil {
		// Fall back to capture length from settings if ffprobe fails
		spectrogramLogger.Warn("Failed to get audio duration with ffprobe, falling back to capture length",
			"error", err,
			"audio_path", audioPath)
		captureLength := conf.Setting().Realtime.Audio.Export.Length
		duration = float64(captureLength)
	}

	// Convert duration to string, rounding to nearest integer
	captureLengthStr := strconv.Itoa(int(duration + 0.5))

	args := []string{"-n", "rate", "24k", "spectrogram", "-x", widthStr, "-y", heightStr, "-d", captureLengthStr, "-z", dynamicRange, "-o", absSpectrogramPath}

	// For compatibility with old HTMX API: add -r flag for raw spectrograms (which is now the default)
	if raw {
		// Raw mode: no axes, labels, or legends for clean display (old API default behavior)
		args = append(args, "-r")
	}
	// Note: Non-raw spectrograms (with legends) will have axes and legends visible
	return args
}

// createSpectrogramWithFFmpeg generates a spectrogram using only ffmpeg.
// Accepts a context for timeout and cancellation.
func createSpectrogramWithFFmpeg(ctx context.Context, absAudioClipPath, absSpectrogramPath string, width int, raw bool, settings *conf.Settings) error {
	start := time.Now()
	spectrogramLogger.Debug("Starting FFmpeg spectrogram generation",
		"abs_audio_path", absAudioClipPath,
		"abs_spectrogram_path", absSpectrogramPath,
		"width", width,
		"raw", raw)

	ffmpegBinary := settings.Realtime.Audio.FfmpegPath
	if ffmpegBinary == "" {
		return ErrFFmpegNotConfigured
	}

	// Create context with timeout (use the passed-in context as parent)
	ctx, cancel := context.WithTimeout(ctx, 60*time.Second)
	defer cancel()

	// NOTE: Semaphore is now acquired in generateSpectrogram() to fix double bottleneck
	// No longer acquiring semaphore here

	height := width / 2
	heightStr := strconv.Itoa(height)
	widthStr := strconv.Itoa(width)

	var filterStr string
	if raw {
		// Raw spectrogram without frequency/time axes and legends for clean display (old API default)
		filterStr = fmt.Sprintf("showspectrumpic=s=%sx%s:legend=0:gain=3:drange=100", widthStr, heightStr)
	} else {
		// Standard spectrogram with frequency/time axes and legends for detailed analysis
		filterStr = fmt.Sprintf("showspectrumpic=s=%sx%s:legend=1:gain=3:drange=100", widthStr, heightStr)
	}

	ffmpegArgs := []string{
		"-hide_banner",
		"-y",
		"-i", absAudioClipPath,
		"-lavfi", filterStr,
		"-frames:v", "1",
		absSpectrogramPath,
	}

	var cmd *exec.Cmd
	if runtime.GOOS == "windows" {
		// #nosec G204 - ffmpegBinary is validated by ValidateToolPath/exec.LookPath
		cmd = exec.CommandContext(ctx, ffmpegBinary, ffmpegArgs...)
	} else {
		// #nosec G204 - ffmpegBinary is validated by ValidateToolPath/exec.LookPath
		cmd = exec.CommandContext(ctx, "nice", append([]string{"-n", "19", ffmpegBinary}, ffmpegArgs...)...)
	}

	var output bytes.Buffer
	cmd.Stderr = &output
	cmd.Stdout = &output

	if err := cmd.Run(); err != nil {
		spectrogramLogger.Debug("FFmpeg spectrogram generation failed",
			"abs_audio_path", absAudioClipPath,
			"duration_ms", time.Since(start).Milliseconds(),
			"error", err.Error(),
			"output", output.String())
		return fmt.Errorf("%w: %w (output: %s)", ErrSpectrogramGeneration, err, output.String())
	}

	spectrogramLogger.Debug("FFmpeg spectrogram generation completed successfully",
		"abs_audio_path", absAudioClipPath,
		"duration_ms", time.Since(start).Milliseconds())

	return nil
}

// GetSpeciesImage serves an image for a bird species by scientific name
func (c *Controller) GetSpeciesImage(ctx echo.Context) error {
	scientificName := ctx.QueryParam("name")
	if scientificName == "" {
		return c.HandleError(ctx, fmt.Errorf("missing scientific name"), "Scientific name is required", http.StatusBadRequest)
	}

	// Trim whitespace to prevent empty strings with spaces
	scientificName = strings.TrimSpace(scientificName)
	if scientificName == "" {
		return c.HandleError(ctx, fmt.Errorf("scientific name contains only whitespace"), "Valid scientific name is required", http.StatusBadRequest)
	}

	// Check if BirdImageCache is available
	if c.BirdImageCache == nil {
		return c.HandleError(ctx, ErrImageProviderNotAvailable, "Image service unavailable", http.StatusServiceUnavailable)
	}

	// Fetch the image from cache (which will use AviCommons if available)
	birdImage, err := c.BirdImageCache.Get(scientificName)
	if err != nil {
		// Check for "not found" errors using errors.Is
		if errors.Is(err, ErrImageNotFound) {
			// For images not found, still set cache headers to prevent repeated lookups
			// Cache the 404 response to reduce load
			ctx.Response().Header().Set("Cache-Control", fmt.Sprintf("public, max-age=%d", NotFoundCacheSeconds))
			return c.HandleError(ctx, err, "Image not found for species", http.StatusNotFound)
		}
		// For other errors, return 500
		return c.HandleError(ctx, err, "Failed to fetch species image", http.StatusInternalServerError)
	}

	// Set aggressive cache headers for species images since they rarely change
	// These are external images from wikimedia/flickr that are stable
	// Cache with immutable flag to prevent revalidation
	ctx.Response().Header().Set("Cache-Control", fmt.Sprintf("public, max-age=%d, immutable", ImageCacheSeconds))

	// Redirect to the image URL
	return ctx.Redirect(http.StatusFound, birdImage.URL)
}

// HandleError method should exist on Controller, typically defined in controller.go or api.go
