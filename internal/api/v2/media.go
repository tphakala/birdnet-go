// internal/api/v2/media.go
package api

import (
	"context"
	"fmt"
	"io/fs"
	"log/slog"
	"math"
	"net/http"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/labstack/echo/v4"
	"github.com/tphakala/birdnet-go/internal/errors"
	"github.com/tphakala/birdnet-go/internal/logging"
	"github.com/tphakala/birdnet-go/internal/myaudio"
	"github.com/tphakala/birdnet-go/internal/securefs"
	"github.com/tphakala/birdnet-go/internal/spectrogram"
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

// Audio MIME type constants for consistent handling across endpoints
const (
	MimeTypeFLAC = "audio/flac"
	MimeTypeWAV  = "audio/wav"
	MimeTypeMP3  = "audio/mpeg"
	MimeTypeM4A  = "audio/mp4"
	MimeTypeOGG  = "audio/ogg"
)

// isValidFilename checks if a filename is valid for use in Content-Disposition header
func isValidFilename(filename string) bool {
	// Reject empty, current dir, or root dir references
	if filename == "" || filename == "." || filename == "/" {
		return false
	}

	// Reject filenames with path separators (security check)
	if strings.ContainsAny(filename, "/\\") {
		return false
	}

	// Additional safety: reject control characters and null bytes
	for _, r := range filename {
		if r < 32 || r == 127 {
			return false
		}
	}

	return true
}

// AudioNotReadyError carries retry information for audio files that are not yet ready
type AudioNotReadyError struct {
	RetryAfter time.Duration
	Err        error
}

func (e *AudioNotReadyError) Error() string { return e.Err.Error() }
func (e *AudioNotReadyError) Unwrap() error { return e.Err }

// Sentinel errors for media operations
var (
	// Audio file errors
	ErrAudioFileNotFound    = errors.NewStd("audio file not found")
	ErrInvalidAudioPath     = errors.NewStd("invalid audio path")
	ErrPathTraversalAttempt = errors.NewStd("security error: path attempts to traverse")

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
	spectrogramStatusExists     = "exists"
	spectrogramStatusGenerated  = "generated"
	spectrogramStatusQueued     = "queued"
	spectrogramStatusGenerating = "generating"
	spectrogramStatusFailed     = "failed"
	spectrogramStatusNotStarted = "not_started"
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
	c.Echo.GET("/api/v2/spectrogram/:id/status", c.GetSpectrogramStatus)

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

	// Normalize and validate the path using the common helper
	normalizedFilename, err := c.normalizeAndValidatePathWithLogger(filename, c.apiLogger)
	if err != nil {
		if c.apiLogger != nil {
			c.apiLogger.Warn("Invalid file path detected",
				"original_filename", filename,
				"error", err.Error(),
				"path", ctx.Request().URL.Path,
				"ip", ctx.RealIP(),
			)
		}
		return c.HandleError(ctx, err, "Invalid file path", http.StatusBadRequest)
	}

	// Serve the file using SecureFS. It handles path validation and serves the file.
	// ServeRelativeFile is expected to return appropriate echo.HTTPErrors (400, 404, 500).
	err = c.SFS.ServeRelativeFile(ctx, normalizedFilename)

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

	// Normalize and validate the path using the common helper
	normalizedClipPath, err := c.normalizeAndValidatePathWithLogger(clipPath, c.apiLogger)
	if err != nil {
		return c.HandleError(ctx, err, "Invalid clip path", http.StatusBadRequest)
	}

	// Extract the original filename and extension
	originalFilename := filepath.Base(clipPath)
	ext := strings.ToLower(filepath.Ext(originalFilename))

	// Set proper Content-Type for audio files BEFORE ServeRelativeFile
	// This ensures Safari recognizes the file as audio
	switch ext {
	case ".flac":
		ctx.Response().Header().Set("Content-Type", MimeTypeFLAC)
	case ".wav":
		ctx.Response().Header().Set("Content-Type", MimeTypeWAV)
	case ".mp3":
		ctx.Response().Header().Set("Content-Type", MimeTypeMP3)
	case ".m4a":
		ctx.Response().Header().Set("Content-Type", MimeTypeM4A)
	case ".ogg":
		ctx.Response().Header().Set("Content-Type", MimeTypeOGG)
	default:
		// Let ServeRelativeFile handle the content type
	}

	// Set Content-Disposition as inline to enable playback in browser
	// Use filename* for proper UTF-8 filename encoding
	// Only set filename if we have a valid, non-empty filename
	if isValidFilename(originalFilename) {
		ctx.Response().Header().Set("Content-Disposition", fmt.Sprintf("inline; filename*=UTF-8''%s", url.QueryEscape(originalFilename)))
	}

	// Ensure Accept-Ranges header is set for iOS Safari
	// This might be set by middleware but we ensure it's present
	ctx.Response().Header().Set("Accept-Ranges", "bytes")

	// Serve the file using SecureFS. It handles path validation (relative/absolute within baseDir).
	// ServeFile internally calls relativePath which ensures the path is within the SecureFS baseDir.
	// Use ServeRelativeFile as clipPath is already relative to the baseDir
	err = c.SFS.ServeRelativeFile(ctx, normalizedClipPath)
	if err != nil {
		return c.translateSecureFSError(ctx, err, "Failed to serve audio clip due to an unexpected error")
	}

	return nil
}

// spectrogramHTTPError handles common spectrogram generation errors and converts them to appropriate HTTP responses
func (c *Controller) spectrogramHTTPError(ctx echo.Context, err error) error {
	switch {
	case errors.Is(err, myaudio.ErrAudioFileNotReady) || errors.Is(err, myaudio.ErrAudioFileIncomplete):
		// Audio file is not ready yet - client should retry
		// Check if we have a dynamic retry duration from validation
		var anr *AudioNotReadyError
		if errors.As(err, &anr) && anr.RetryAfter > 0 {
			// Use the dynamic retry duration from audio validation
			secs := int(math.Ceil(anr.RetryAfter.Seconds()))
			ctx.Response().Header().Set("Retry-After", strconv.Itoa(secs))
		} else {
			// Fall back to default retry duration
			ctx.Response().Header().Set("Retry-After", spectrogramRetryAfterSeconds)
		}
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
		if validWidth, err := spectrogram.SizeToPixels(sizeStr); err == nil {
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
// Route: GET /media/spectrogram/:filename
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
		if validWidth, err := spectrogram.SizeToPixels(sizeStr); err == nil {
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

// GetSpectrogramStatus returns the generation status of a spectrogram
//
// Route: GET /api/v2/spectrogram/:id/status
//
// Returns:
//   - status: "not_started", "queued", "generating", "generated", "failed", or "exists"
//   - queuePosition: Position in generation queue (0 if not queued)
//   - message: Additional status information
func (c *Controller) GetSpectrogramStatus(ctx echo.Context) error {
	noteID := ctx.Param("id")
	if noteID == "" {
		return c.HandleError(ctx, fmt.Errorf("missing ID"), "Note ID is required", http.StatusBadRequest)
	}

	// Get detection from database
	detection, err := c.DS.Get(noteID)
	if err != nil {
		return c.HandleError(ctx, err, "Detection not found", http.StatusNotFound)
	}

	// Calculate spectrogram parameters from query
	width := SpectrogramSizeMd // Default
	sizeStr := ctx.QueryParam("size")
	if sizeStr != "" {
		if validWidth, err := spectrogram.SizeToPixels(sizeStr); err == nil {
			width = validWidth
		}
	}

	raw := parseRawParameter(ctx.QueryParam("raw"))

	// Build spectrogram key for status lookup
	audioPath := detection.ClipName
	spectrogramKey := fmt.Sprintf("%s_%d_%t", audioPath, width, raw)

	// Check queue status first (more volatile state)
	spectrogramQueueMutex.RLock()
	status, existsInQueue := spectrogramQueue[spectrogramKey]
	spectrogramQueueMutex.RUnlock()

	// If it's actively being processed, return that status immediately
	if existsInQueue {
		return ctx.JSON(http.StatusOK, status)
	}

	// Not in queue, check if spectrogram already exists on disk
	clipsPrefix := c.Settings.Realtime.Audio.Export.Path
	normalizedPath := NormalizeClipPath(audioPath, clipsPrefix)
	relAudioPath, err := c.SFS.ValidateRelativePath(normalizedPath)
	if err == nil {
		// Build spectrogram path
		_, _, _, relSpectrogramPath := buildSpectrogramPaths(relAudioPath, width, raw)

		// Check if file exists
		if _, err := c.SFS.StatRel(relSpectrogramPath); err == nil {
			return ctx.JSON(http.StatusOK, SpectrogramQueueStatus{
				Status:        spectrogramStatusExists,
				QueuePosition: 0,
				Message:       "Spectrogram already exists",
			})
		}
	}

	// Not in queue and doesn't exist on disk
	return ctx.JSON(http.StatusOK, SpectrogramQueueStatus{
		Status:        spectrogramStatusNotStarted,
		QueuePosition: 0,
		Message:       "Spectrogram generation not started",
	})
}

// maxConcurrentSpectrograms limits concurrent spectrogram generations to avoid overloading the system.
// Set to 4 to match the number of CPU cores on Raspberry Pi 4/5, which is the most common
// deployment platform for BirdNET-Go. This prevents severe CPU contention and ensures
// responsive performance on resource-constrained devices.
const maxConcurrentSpectrograms = 4

// spectrogramRetryAfterSeconds is the suggested retry delay in seconds for 503 responses
// when audio files are not yet ready for processing
const spectrogramRetryAfterSeconds = "2"

var (
	spectrogramSemaphore = make(chan struct{}, maxConcurrentSpectrograms)
	spectrogramGroup     singleflight.Group // Prevents duplicate generations

	// Track spectrogram generation queue status
	spectrogramQueueMutex sync.RWMutex
	spectrogramQueue      = make(map[string]*SpectrogramQueueStatus)
)

// SpectrogramQueueStatus tracks the status of a spectrogram generation request
type SpectrogramQueueStatus struct {
	Status        string    `json:"status"`        // "queued", "generating", "generated", "failed", "exists", "not_started"
	QueuePosition int       `json:"queuePosition"` // Position in queue (0 if generating/generated)
	StartedAt     time.Time `json:"startedAt"`     // When generation started
	Message       string    `json:"message"`       // Additional status message
}

// Package-level logger for spectrogram generation
var (
	spectrogramLogger      *slog.Logger
	spectrogramLevelVar    = new(slog.LevelVar) // Dynamic level control
	closeSpectrogramLogger func() error
)

// getSpectrogramLogger returns the spectrogram logger, ensuring it's never nil
func getSpectrogramLogger() *slog.Logger {
	if spectrogramLogger != nil {
		return spectrogramLogger
	}
	// Emergency fallback if logger is somehow nil
	defaultLogger := slog.Default()
	if defaultLogger != nil {
		return defaultLogger
	}
	// Ultimate fallback: create emergency logger to stderr (should never happen)
	return slog.New(slog.NewTextHandler(os.Stderr, nil))
}

func init() {
	// Initialize spectrogram generation logger
	// This creates a dedicated log file at logs/spectrogram-generation.log
	var err error

	// Set log level based on global debug setting
	// Default to Info level, use Debug only when explicitly enabled
	spectrogramLevelVar.Set(slog.LevelInfo)

	spectrogramLogger, closeSpectrogramLogger, err = logging.NewFileLogger(
		"logs/spectrogram-generation.log",
		"spectrogram-generation",
		spectrogramLevelVar,
	)

	if err != nil || spectrogramLogger == nil {
		// Fallback to default logger if file logger creation fails
		defaultLogger := slog.Default()
		if defaultLogger != nil {
			spectrogramLogger = defaultLogger.With("service", "spectrogram-generation")
		} else {
			// Ultimate fallback: create a new logger to stdout
			spectrogramLogger = slog.New(slog.NewTextHandler(os.Stdout, &slog.HandlerOptions{
				Level: spectrogramLevelVar,
			})).With("service", "spectrogram-generation")
		}
		closeSpectrogramLogger = func() error { return nil }
		// Log the error so we know why the file logger failed
		if err != nil {
			getSpectrogramLogger().Error("Failed to initialize spectrogram generation file logger", "error", err)
		}
	}
}

// UpdateSpectrogramLogLevel updates the spectrogram logger level based on debug setting
func UpdateSpectrogramLogLevel(debugEnabled bool) {
	if spectrogramLevelVar != nil {
		if debugEnabled {
			spectrogramLevelVar.Set(slog.LevelDebug)
			getSpectrogramLogger().Info("Spectrogram logger set to DEBUG level")
		} else {
			spectrogramLevelVar.Set(slog.LevelInfo)
			getSpectrogramLogger().Info("Spectrogram logger set to INFO level")
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

// buildSpectrogramPaths constructs the spectrogram file paths from the audio path and parameters.
// It returns the base filename, audio directory, spectrogram filename, and full relative spectrogram path.
func buildSpectrogramPaths(relAudioPath string, width int, raw bool) (relBaseFilename, relAudioDir, spectrogramFilename, relSpectrogramPath string) {
	// Get the base filename and directory relative to the secure root
	relBaseFilename = strings.TrimSuffix(filepath.Base(relAudioPath), filepath.Ext(relAudioPath))
	relAudioDir = filepath.Dir(relAudioPath)

	// Generate spectrogram filename compatible with old HTMX API format
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
	relSpectrogramPath = filepath.Join(relAudioDir, spectrogramFilename)

	return relBaseFilename, relAudioDir, spectrogramFilename, relSpectrogramPath
}

// ffprobeCache provides a unified cache for all FFprobe operations (validation and duration)
// to avoid repeated expensive subprocess calls
var ffprobeCache = struct {
	sync.RWMutex
	validation map[string]*validationCacheEntry
	duration   map[string]*durationCacheEntry
}{
	validation: make(map[string]*validationCacheEntry),
	duration:   make(map[string]*durationCacheEntry),
}

type validationCacheEntry struct {
	result    *myaudio.AudioValidationResult
	timestamp time.Time
	fileSize  int64
	modTime   time.Time
}

type durationCacheEntry struct {
	duration  float64
	timestamp time.Time
	fileSize  int64
	modTime   time.Time
}

// validateSpectrogramInputs validates that the audio file is complete and ready for spectrogram generation.
// It returns the validation result and any error encountered during validation.
func (c *Controller) validateSpectrogramInputs(ctx context.Context, absAudioPath, audioPath, spectrogramKey string) (*myaudio.AudioValidationResult, error) {
	// Check cache first
	fileInfo, err := os.Stat(absAudioPath)
	if err != nil {
		return nil, fmt.Errorf("failed to stat audio file: %w", err)
	}

	cacheKey := fmt.Sprintf("%s:%d:%s", absAudioPath, fileInfo.Size(), fileInfo.ModTime().Format(time.RFC3339Nano))

	// Try to get from cache
	ffprobeCache.RLock()
	if entry, ok := ffprobeCache.validation[cacheKey]; ok {
		// Cache hit - check if still valid (cache for 5 minutes)
		if time.Since(entry.timestamp) < 5*time.Minute &&
			entry.fileSize == fileInfo.Size() &&
			entry.modTime.Equal(fileInfo.ModTime()) {
			ffprobeCache.RUnlock()
			getSpectrogramLogger().Debug("Audio validation cache hit",
				"abs_audio_path", absAudioPath,
				"cache_age_seconds", time.Since(entry.timestamp).Seconds(),
				"spectrogram_key", spectrogramKey)
			return entry.result, nil
		}
	}
	ffprobeCache.RUnlock()

	getSpectrogramLogger().Debug("Starting audio validation with FFprobe",
		"abs_audio_path", absAudioPath,
		"spectrogram_key", spectrogramKey)

	validationStart := time.Now()
	validationResult, err := myaudio.ValidateAudioFileWithRetry(ctx, absAudioPath)
	validationDuration := time.Since(validationStart)

	if err != nil {
		// Context errors should be propagated immediately
		if errors.Is(err, context.DeadlineExceeded) || errors.Is(err, context.Canceled) {
			getSpectrogramLogger().Warn("Audio validation canceled or timed out",
				"audio_path", audioPath,
				"abs_audio_path", absAudioPath,
				"error", err.Error(),
				"validation_duration_ms", validationDuration.Milliseconds(),
				"spectrogram_key", spectrogramKey)
			return nil, err
		}
		// Other validation errors
		getSpectrogramLogger().Error("Audio validation failed with FFprobe",
			"audio_path", audioPath,
			"abs_audio_path", absAudioPath,
			"error", err.Error(),
			"validation_duration_ms", validationDuration.Milliseconds(),
			"spectrogram_key", spectrogramKey)
		return nil, &AudioNotReadyError{
			RetryAfter: 2 * time.Second, // Default retry for validation errors
			Err:        fmt.Errorf("%w: %w", myaudio.ErrAudioFileNotReady, err),
		}
	}

	// Check if the file is ready
	if !validationResult.IsValid {
		getSpectrogramLogger().Info("Audio file not ready for processing, client should retry",
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
			return validationResult, &AudioNotReadyError{
				RetryAfter: validationResult.RetryAfter,
				Err:        fmt.Errorf("%w: %w", myaudio.ErrAudioFileNotReady, validationResult.Error),
			}
		}
		return validationResult, &AudioNotReadyError{
			RetryAfter: validationResult.RetryAfter,
			Err:        myaudio.ErrAudioFileNotReady,
		}
	}

	getSpectrogramLogger().Debug("Audio file validated successfully with FFprobe",
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

	// Cache the successful validation result
	ffprobeCache.Lock()
	// Clean old entries if cache is getting large
	if len(ffprobeCache.validation) > 100 {
		now := time.Now()
		for k, v := range ffprobeCache.validation {
			if now.Sub(v.timestamp) > 5*time.Minute {
				delete(ffprobeCache.validation, k)
			}
		}
	}
	ffprobeCache.validation[cacheKey] = &validationCacheEntry{
		result:    validationResult,
		timestamp: time.Now(),
		fileSize:  fileInfo.Size(),
		modTime:   fileInfo.ModTime(),
	}

	// Also cache the duration value for GetAudioDuration calls
	if validationResult.Duration > 0 {
		// Clean duration cache if needed
		if len(ffprobeCache.duration) > 100 {
			now := time.Now()
			for k, v := range ffprobeCache.duration {
				if now.Sub(v.timestamp) > 5*time.Minute {
					delete(ffprobeCache.duration, k)
				}
			}
		}
		ffprobeCache.duration[cacheKey] = &durationCacheEntry{
			duration:  validationResult.Duration,
			timestamp: time.Now(),
			fileSize:  fileInfo.Size(),
			modTime:   fileInfo.ModTime(),
		}
	}
	ffprobeCache.Unlock()

	return validationResult, nil
}

// getCachedAudioDuration retrieves audio duration from cache or calls FFprobe if not cached
func getCachedAudioDuration(ctx context.Context, audioPath string) float64 {
	// Check if file exists and get info
	fileInfo, err := os.Stat(audioPath)
	if err != nil {
		getSpectrogramLogger().Debug("Failed to stat audio file for duration cache",
			"audio_path", audioPath,
			"error", err)
		return 0
	}

	cacheKey := fmt.Sprintf("%s:%d:%s", audioPath, fileInfo.Size(), fileInfo.ModTime().Format(time.RFC3339Nano))

	// Try to get from cache
	ffprobeCache.RLock()
	if entry, ok := ffprobeCache.duration[cacheKey]; ok {
		// Cache hit - check if still valid (cache for 5 minutes)
		if time.Since(entry.timestamp) < 5*time.Minute &&
			entry.fileSize == fileInfo.Size() &&
			entry.modTime.Equal(fileInfo.ModTime()) {
			ffprobeCache.RUnlock()
			getSpectrogramLogger().Debug("Audio duration cache hit",
				"audio_path", audioPath,
				"duration", entry.duration,
				"cache_age_seconds", time.Since(entry.timestamp).Seconds())
			return entry.duration
		}
	}
	ffprobeCache.RUnlock()

	// Cache miss - call FFprobe
	getSpectrogramLogger().Debug("Audio duration cache miss, calling FFprobe",
		"audio_path", audioPath)

	// Use a timeout context to prevent hanging
	durationCtx, cancel := context.WithTimeout(ctx, 3*time.Second)
	defer cancel()

	duration, err := myaudio.GetAudioDuration(durationCtx, audioPath)
	if err != nil {
		getSpectrogramLogger().Warn("Failed to get audio duration with ffprobe",
			"error", err,
			"audio_path", audioPath)
		return 0
	}

	// Cache the result
	ffprobeCache.Lock()
	// Clean old entries if cache is getting large
	if len(ffprobeCache.duration) > 100 {
		now := time.Now()
		for k, v := range ffprobeCache.duration {
			if now.Sub(v.timestamp) > 5*time.Minute {
				delete(ffprobeCache.duration, k)
			}
		}
	}
	ffprobeCache.duration[cacheKey] = &durationCacheEntry{
		duration:  duration,
		timestamp: time.Now(),
		fileSize:  fileInfo.Size(),
		modTime:   fileInfo.ModTime(),
	}
	ffprobeCache.Unlock()

	getSpectrogramLogger().Debug("Audio duration retrieved and cached",
		"audio_path", audioPath,
		"duration", duration)

	return duration
}

// normalizeAndValidatePath handles path normalization and validation
func (c *Controller) normalizeAndValidatePath(audioPath string) (string, error) {
	return c.normalizeAndValidatePathWithLogger(audioPath, spectrogramLogger)
}

// normalizeAndValidatePathWithLogger is a reusable helper for path normalization and validation.
// It combines the common pattern of:
// 1. Getting the clips prefix from settings
// 2. Normalizing the path
// 3. Checking for empty/invalid results
// 4. Validating with SecureFS
//
// This reduces duplication across the codebase where this pattern is used.
func (c *Controller) normalizeAndValidatePathWithLogger(audioPath string, logger *slog.Logger) (string, error) {
	clipsPrefix := c.Settings.Realtime.Audio.Export.Path
	normalizedPath := NormalizeClipPath(audioPath, clipsPrefix)

	if logger != nil && normalizedPath != audioPath {
		logger.Debug("Normalized audio path",
			"original_path", audioPath,
			"normalized_path", normalizedPath,
			"clips_prefix", clipsPrefix)
	}

	if normalizedPath == "" {
		if logger != nil {
			logger.Warn("Invalid audio path detected",
				"original_path", audioPath,
				"clips_prefix", clipsPrefix)
		}
		return "", fmt.Errorf("%w: empty normalized path", ErrInvalidAudioPath)
	}

	relAudioPath, err := c.SFS.ValidateRelativePath(normalizedPath)
	if err != nil {
		if errors.Is(err, securefs.ErrPathTraversal) {
			return "", fmt.Errorf("%w: %w", ErrPathTraversalAttempt, err)
		}
		return "", fmt.Errorf("%w: %w", ErrInvalidAudioPath, err)
	}

	return relAudioPath, nil
}

// checkSpectrogramExists performs fast path check for existing spectrogram
func (c *Controller) checkSpectrogramExists(relSpectrogramPath, spectrogramKey string, start time.Time) (bool, error) {
	getSpectrogramLogger().Debug("Fast path check: checking if spectrogram exists",
		"spectrogram_key", spectrogramKey,
		"relative_spectrogram_path", relSpectrogramPath)

	// Build absolute path for direct filesystem check
	absSpectrogramPath := filepath.Join(c.SFS.BaseDir(), relSpectrogramPath)

	// Try direct filesystem check first (more reliable)
	if statInfo, err := os.Stat(absSpectrogramPath); err == nil {
		getSpectrogramLogger().Debug("Fast path HIT via direct check: spectrogram already exists",
			"spectrogram_key", spectrogramKey,
			"abs_path", absSpectrogramPath,
			"file_size", statInfo.Size(),
			"mod_time", statInfo.ModTime(),
			"total_duration_ms", time.Since(start).Milliseconds())
		return true, nil
	}

	// Fallback to SecureFS check (for consistency with security model)
	if statInfo, err := c.SFS.StatRel(relSpectrogramPath); err == nil {
		getSpectrogramLogger().Debug("Fast path HIT via SecureFS: spectrogram already exists",
			"spectrogram_key", spectrogramKey,
			"file_size", statInfo.Size(),
			"mod_time", statInfo.ModTime(),
			"total_duration_ms", time.Since(start).Milliseconds())
		return true, nil
	} else if !os.IsNotExist(err) {
		getSpectrogramLogger().Debug("Fast path: unexpected error checking existing spectrogram",
			"spectrogram_key", spectrogramKey,
			"error", err.Error())
	} else {
		getSpectrogramLogger().Debug("Fast path MISS: spectrogram does not exist",
			"spectrogram_key", spectrogramKey,
			"abs_path", absSpectrogramPath)
	}

	return false, nil
}

// updateQueueStatus updates the spectrogram generation queue status
func (c *Controller) updateQueueStatus(spectrogramKey, status string, queuePos int, message string) {
	spectrogramQueueMutex.Lock()
	defer spectrogramQueueMutex.Unlock()

	if queueStatus, exists := spectrogramQueue[spectrogramKey]; exists {
		queueStatus.Status = status
		queueStatus.QueuePosition = queuePos
		queueStatus.Message = message
	}
}

// checkAudioFileExists verifies the audio file exists
func (c *Controller) checkAudioFileExists(relAudioPath string) error {
	getSpectrogramLogger().Debug("Checking if audio file exists",
		"relative_audio_path", relAudioPath)

	if audioStat, err := c.SFS.StatRel(relAudioPath); err != nil {
		if os.IsNotExist(err) {
			getSpectrogramLogger().Debug("Audio file does not exist",
				"relative_audio_path", relAudioPath,
				"error", err.Error())
			return fmt.Errorf("%w: %w (path: %s)", ErrAudioFileNotFound, err, relAudioPath)
		}
		getSpectrogramLogger().Debug("Error checking audio file",
			"relative_audio_path", relAudioPath,
			"error", err.Error())
		return fmt.Errorf("error checking audio file '%s': %w", relAudioPath, err)
	} else {
		getSpectrogramLogger().Debug("Audio file exists",
			"relative_audio_path", relAudioPath,
			"size_bytes", audioStat.Size(),
			"mod_time", audioStat.ModTime().Format("2006-01-02 15:04:05"))
	}
	return nil
}

// initializeQueueStatus initializes the queue tracking for a spectrogram request
func (c *Controller) initializeQueueStatus(spectrogramKey string) {
	spectrogramQueueMutex.Lock()
	defer spectrogramQueueMutex.Unlock()

	// Calculate queue position - 0 if slot immediately available, otherwise position in queue
	var queuePosition int
	currentSlotsInUse := len(spectrogramSemaphore)

	// Log current semaphore state for debugging
	getSpectrogramLogger().Debug("Checking semaphore availability",
		"spectrogram_key", spectrogramKey,
		"current_slots_in_use", currentSlotsInUse,
		"max_concurrent", maxConcurrentSpectrograms,
		"semaphore_full", currentSlotsInUse >= maxConcurrentSpectrograms)

	if currentSlotsInUse >= maxConcurrentSpectrograms {
		// All slots are taken, this will be queued
		// Count how many are already waiting in queue
		waitingCount := 0
		for _, status := range spectrogramQueue {
			if status.Status == spectrogramStatusQueued {
				waitingCount++
			}
		}
		queuePosition = waitingCount + 1
	} else {
		// Slot is available, will run immediately
		queuePosition = 0
	}

	spectrogramQueue[spectrogramKey] = &SpectrogramQueueStatus{
		Status:        spectrogramStatusQueued,
		QueuePosition: queuePosition,
		StartedAt:     time.Now(),
		Message:       "Waiting for generation slot",
	}
}

// cleanupQueueStatus removes the queue entry for a spectrogram request
func (c *Controller) cleanupQueueStatus(spectrogramKey string) {
	spectrogramQueueMutex.Lock()
	defer spectrogramQueueMutex.Unlock()
	delete(spectrogramQueue, spectrogramKey)
}

// acquireSemaphoreSlot acquires a semaphore slot for spectrogram generation
func (c *Controller) acquireSemaphoreSlot(ctx context.Context, spectrogramKey string) error {
	slotsInUseBeforeAcquire := len(spectrogramSemaphore)
	availableSlots := maxConcurrentSpectrograms - slotsInUseBeforeAcquire

	getSpectrogramLogger().Debug("Attempting to acquire semaphore slot",
		"spectrogram_key", spectrogramKey,
		"slots_in_use", slotsInUseBeforeAcquire,
		"slots_available", availableSlots,
		"max_concurrent", maxConcurrentSpectrograms)

	select {
	case spectrogramSemaphore <- struct{}{}:
		// Successfully acquired a slot
		slotsInUseAfterAcquire := len(spectrogramSemaphore)
		slotsStillAvailable := maxConcurrentSpectrograms - slotsInUseAfterAcquire

		getSpectrogramLogger().Debug("Semaphore slot acquired successfully",
			"spectrogram_key", spectrogramKey,
			"slots_now_in_use", slotsInUseAfterAcquire,
			"slots_still_available", slotsStillAvailable,
			"max_concurrent", maxConcurrentSpectrograms)

		c.updateQueueStatus(spectrogramKey, spectrogramStatusGenerating, 0, "Generating spectrogram")
		return nil

	case <-ctx.Done():
		getSpectrogramLogger().Debug("Context canceled while waiting for semaphore",
			"spectrogram_key", spectrogramKey,
			"error", ctx.Err())

		c.updateQueueStatus(spectrogramKey, spectrogramStatusFailed, 0, "Generation canceled")
		return ctx.Err()
	}
}

// performSpectrogramGeneration executes the actual spectrogram generation logic
func (c *Controller) performSpectrogramGeneration(ctx context.Context, relSpectrogramPath, absAudioPath, absSpectrogramPath, spectrogramKey string, width int, raw bool) (any, error) {
	// Fast path inside the group – now race-free
	getSpectrogramLogger().Debug("Inside singleflight group, double-checking if spectrogram exists",
		"spectrogram_key", spectrogramKey)

	// Try direct filesystem check first (more reliable)
	if _, err := os.Stat(absSpectrogramPath); err == nil {
		getSpectrogramLogger().Debug("Spectrogram already exists via direct check (race condition avoided)",
			"abs_spectrogram_path", absSpectrogramPath,
			"spectrogram_key", spectrogramKey)
		return spectrogramStatusExists, nil
	}

	// Fallback to SecureFS check
	if _, err := c.SFS.StatRel(relSpectrogramPath); err == nil {
		getSpectrogramLogger().Debug("Spectrogram already exists via SecureFS (race condition avoided)",
			"spectrogram_path", relSpectrogramPath,
			"spectrogram_key", spectrogramKey)
		return spectrogramStatusExists, nil
	} else if !os.IsNotExist(err) {
		getSpectrogramLogger().Debug("Error checking existing spectrogram in singleflight",
			"spectrogram_path", relSpectrogramPath,
			"error", err)
		return nil, fmt.Errorf("error checking for existing spectrogram '%s': %w", relSpectrogramPath, err)
	}

	getSpectrogramLogger().Debug("Starting actual spectrogram generation (file does not exist)",
		"spectrogram_key", spectrogramKey,
		"abs_audio_path", absAudioPath,
		"abs_spectrogram_path", absSpectrogramPath,
		"width", width,
		"raw", raw,
		"generator", "shared_generator_with_sox_ffmpeg_fallback")

	// Note: Directory creation is handled by the shared generator

	// Log when we're about to start actual generation
	getSpectrogramLogger().Info("Starting SoX/FFmpeg generation",
		"spectrogram_key", spectrogramKey,
		"semaphore_slots_in_use", len(spectrogramSemaphore),
		"max_slots", maxConcurrentSpectrograms)

	// Generate the spectrogram with SoX or FFmpeg fallback
	if err := c.generateWithFallback(ctx, absAudioPath, absSpectrogramPath, spectrogramKey, width, raw); err != nil {
		return nil, err
	}

	getSpectrogramLogger().Info("Completed SoX/FFmpeg generation",
		"spectrogram_key", spectrogramKey,
		"semaphore_slots_in_use", len(spectrogramSemaphore),
		"max_slots", maxConcurrentSpectrograms)

	// Verify the spectrogram file exists
	// We'll use a direct filesystem check because os.Root may have issues with newly created files
	// Retry a few times to handle filesystem sync delays
	var statErr error
	for i := 0; i < 3; i++ {
		// Try direct filesystem check first (more reliable for newly created files)
		if _, err := os.Stat(absSpectrogramPath); err == nil {
			getSpectrogramLogger().Debug("Spectrogram verified via direct filesystem check",
				"abs_spectrogram_path", absSpectrogramPath,
				"attempt", i+1)
			statErr = nil
			break
		} else if !os.IsNotExist(err) {
			// Unexpected error, don't retry
			statErr = err
			break
		}

		// If direct check failed, try via SecureFS (may work better after delay)
		if _, statErr = c.SFS.StatRel(relSpectrogramPath); statErr == nil {
			getSpectrogramLogger().Debug("Spectrogram verified via SecureFS",
				"rel_spectrogram_path", relSpectrogramPath,
				"attempt", i+1)
			break
		}

		if !os.IsNotExist(statErr) {
			// Unexpected error, don't retry
			break
		}

		if i < 2 {
			// Wait a bit for filesystem to sync (50ms, then 100ms)
			time.Sleep(time.Duration((i+1)*50) * time.Millisecond)
			getSpectrogramLogger().Debug("Retrying spectrogram verification",
				"abs_path", absSpectrogramPath,
				"rel_path", relSpectrogramPath,
				"retry_attempt", i+1)
		}
	}

	if statErr != nil {
		// Double-check with direct filesystem access as last resort
		if _, finalErr := os.Stat(absSpectrogramPath); finalErr == nil {
			getSpectrogramLogger().Info("Spectrogram found via final direct check after SecureFS failed",
				"abs_spectrogram_path", absSpectrogramPath)
			// File exists, continue with success
		} else {
			getSpectrogramLogger().Error("Generated spectrogram missing after successful command",
				"rel_spectrogram_path", relSpectrogramPath,
				"abs_spectrogram_path", absSpectrogramPath,
				"securefs_error", statErr,
				"direct_check_error", finalErr)
			return nil, fmt.Errorf("%w: spectrogram file missing after generation: %w",
				ErrSpectrogramGeneration, statErr)
		}
	}

	return spectrogramStatusGenerated, nil
}

// ensureOutputDirectory creates the output directory if it doesn't exist.
//
// NOTE: This function is deprecated in favor of using the shared spectrogram generator,
// which handles directory creation internally via SecureFS.MkdirAll. This function remains
// for now to handle the directory creation before calling the generator, but will be
// removed once the generator is fully integrated.
//
// SECURITY NOTE: This function uses os.MkdirAll directly instead of c.SFS.MkdirAll
// for the following reasons:
//
//  1. PATH VALIDATION: The relSpectrogramPath has already been validated by SecureFS
//     earlier in the call chain (in generateSpectrogram via normalizeAndValidatePath).
//
//  2. SECUREFS LIMITATION: c.SFS.MkdirAll expects absolute paths or converts relative
//     paths using the current working directory.
//
//  3. SAFETY GUARANTEE: We construct absDir by joining c.SFS.BaseDir() with relDir
//     (derived from an already-validated path).
func (c *Controller) ensureOutputDirectory(relSpectrogramPath string) error {
	relDir := filepath.Dir(relSpectrogramPath)

	// Construct absolute path within the SecureFS base directory
	// This is safe because relSpectrogramPath has already been validated
	absDir := filepath.Join(c.SFS.BaseDir(), relDir)

	// Use os.MkdirAll directly for the reasons documented above
	if err := os.MkdirAll(absDir, 0o755); err != nil {
		getSpectrogramLogger().Error("Failed to create output directory for spectrogram",
			"rel_dir", relDir,
			"abs_dir", absDir,
			"error", err.Error())
		return fmt.Errorf("failed to create output directory %s: %w", relDir, err)
	}
	getSpectrogramLogger().Debug("Ensured output directory exists",
		"rel_dir", relDir,
		"abs_dir", absDir)
	return nil
}

// generateWithFallback attempts to generate a spectrogram with SoX, falling back to FFmpeg on failure
func (c *Controller) generateWithFallback(ctx context.Context, absAudioPath, absSpectrogramPath, spectrogramKey string, width int, raw bool) error {
	generationStart := time.Now()

	getSpectrogramLogger().Debug("Starting spectrogram generation using shared generator",
		"spectrogram_key", spectrogramKey,
		"abs_audio_path", absAudioPath,
		"width", width,
		"raw", raw)

	// Use shared generator which handles Sox→FFmpeg fallback internally
	if err := c.spectrogramGenerator.GenerateFromFile(ctx, absAudioPath, absSpectrogramPath, width, raw); err != nil {
		getSpectrogramLogger().Error("Spectrogram generation failed",
			"spectrogram_key", spectrogramKey,
			"error", err.Error(),
			"duration_ms", time.Since(generationStart).Milliseconds(),
			"abs_audio_path", absAudioPath,
			"abs_spectrogram_path", absSpectrogramPath)
		return err
	}

	getSpectrogramLogger().Debug("Spectrogram generation successful",
		"spectrogram_key", spectrogramKey,
		"abs_audio_path", absAudioPath,
		"generation_duration_ms", time.Since(generationStart).Milliseconds())
	return nil
}

// generateSpectrogram creates a spectrogram image for the given audio file path (relative to SecureFS root).
// It accepts a context for cancellation and timeout.
// It returns the relative path to the generated spectrogram, suitable for use with c.SFS.ServeFile.
// Optimized: Fast path check happens before expensive audio validation.
func (c *Controller) generateSpectrogram(ctx context.Context, audioPath string, width int, raw bool) (string, error) {
	start := time.Now()
	getSpectrogramLogger().Debug("Spectrogram generation requested",
		"audio_path", audioPath,
		"width", width,
		"raw", raw,
		"request_time", start.Format("2006-01-02 15:04:05"))

	// Step 1: Normalize and validate path
	relAudioPath, err := c.normalizeAndValidatePath(audioPath)
	if err != nil {
		return "", err
	}

	// Step 2: Calculate spectrogram paths early (needed for fast path check)
	relBaseFilename, relAudioDir, spectrogramFilename, relSpectrogramPath := buildSpectrogramPaths(relAudioPath, width, raw)

	getSpectrogramLogger().Debug("Spectrogram path constructed",
		"audio_path", audioPath,
		"audio_ext", filepath.Ext(relAudioPath),
		"base_filename", relBaseFilename,
		"audio_dir", relAudioDir,
		"spectrogram_filename", spectrogramFilename,
		"relative_spectrogram_path", relSpectrogramPath,
		"width", width,
		"raw", raw)

	// Generate a unique key for this spectrogram generation request
	spectrogramKey := fmt.Sprintf("%s:%d:%t", relSpectrogramPath, width, raw)

	// Step 3: Fast path - Check if spectrogram already exists
	exists, err := c.checkSpectrogramExists(relSpectrogramPath, spectrogramKey, start)
	if err != nil {
		return "", err
	}
	if exists {
		return relSpectrogramPath, nil
	}

	// Step 4: Check if audio file exists
	if err := c.checkAudioFileExists(relAudioPath); err != nil {
		return "", err
	}

	// Step 5: Validate that the audio file is complete and ready for processing
	absAudioPath := filepath.Join(c.SFS.BaseDir(), relAudioPath)
	_, err = c.validateSpectrogramInputs(ctx, absAudioPath, audioPath, spectrogramKey)
	if err != nil {
		return "", err
	}

	// Absolute path for the spectrogram on the host filesystem
	absSpectrogramPath := filepath.Join(c.SFS.BaseDir(), relSpectrogramPath)

	// Step 6: Proceed with generation (spectrogram doesn't exist)
	getSpectrogramLogger().Debug("Proceeding with spectrogram generation",
		"spectrogram_key", spectrogramKey,
		"abs_audio_path", absAudioPath,
		"abs_spectrogram_path", absSpectrogramPath,
		"width", width,
		"raw", raw)

	// Track this request in the queue
	c.initializeQueueStatus(spectrogramKey)

	// Clean up queue entry on exit
	defer c.cleanupQueueStatus(spectrogramKey)

	// Use singleflight to prevent duplicate generations; acquire semaphore only for the winner
	getSpectrogramLogger().Debug("Starting singleflight generation",
		"spectrogram_key", spectrogramKey)

	_, err, _ = spectrogramGroup.Do(spectrogramKey, func() (any, error) {
		// Acquire semaphore inside singleflight - only the actual worker gets a slot
		if err := c.acquireSemaphoreSlot(ctx, spectrogramKey); err != nil {
			return nil, err
		}
		defer func() {
			slotsBeforeRelease := len(spectrogramSemaphore)
			<-spectrogramSemaphore
			slotsAfterRelease := len(spectrogramSemaphore)
			getSpectrogramLogger().Debug("Semaphore slot released",
				"spectrogram_key", spectrogramKey,
				"slots_before_release", slotsBeforeRelease,
				"slots_after_release", slotsAfterRelease,
				"slots_now_available", maxConcurrentSpectrograms-slotsAfterRelease,
				"total_duration_ms", time.Since(start).Milliseconds())
		}()
		return c.performSpectrogramGeneration(ctx, relSpectrogramPath, absAudioPath, absSpectrogramPath, spectrogramKey, width, raw)
	})

	if err != nil {
		getSpectrogramLogger().Debug("Spectrogram generation failed",
			"spectrogram_key", spectrogramKey,
			"error", err.Error(),
			"total_duration_ms", time.Since(start).Milliseconds())
		return "", fmt.Errorf("failed to generate spectrogram: %w", err)
	}

	getSpectrogramLogger().Debug("Spectrogram generation completed successfully",
		"spectrogram_key", spectrogramKey,
		"relative_spectrogram_path", relSpectrogramPath,
		"total_duration_ms", time.Since(start).Milliseconds())

	// Return the relative path of the newly created spectrogram
	return relSpectrogramPath, nil
}

// --- Spectrogram Generation Helpers ---

// waitWithTimeout waits for a command to finish with a timeout to prevent zombie processes.
// This function ensures proper process cleanup even on resource-constrained devices.
// It uses a goroutine with timeout to prevent indefinite blocking on Wait().
//
// Channel buffer size of 1 is critical: it ensures the goroutine can exit even if the
// timeout fires, preventing goroutine leaks. Without the buffer, if timeout occurs before
// Wait() completes, the goroutine would block forever on the channel send.
func waitWithTimeout(cmd *exec.Cmd, timeout time.Duration, logger *slog.Logger) {
	// Store PID early to prevent potential nil pointer panic in logging
	pid := -1
	if cmd.Process != nil {
		pid = cmd.Process.Pid
	}

	// Buffer size 1 ensures goroutine can exit even if timeout fires (prevents leak)
	done := make(chan error, 1)
	go func() {
		done <- cmd.Wait()
	}()

	select {
	case err := <-done:
		if err != nil && logger != nil {
			logger.Debug("Process wait completed with error",
				"pid", pid,
				"error", err.Error())
		}
	case <-time.After(timeout):
		if logger != nil {
			logger.Warn("Process wait timed out, process may become zombie",
				"pid", pid,
				"timeout_seconds", timeout.Seconds())
		}
		// Even after timeout, try one more time with Kill to clean up
		if cmd.Process != nil {
			_ = cmd.Process.Kill()
			// Try to reap the zombie with a short timeout
			select {
			case <-done:
			case <-time.After(1 * time.Second):
				if logger != nil {
					logger.Error("Failed to reap process after kill, zombie process likely",
						"pid", pid)
				}
			}
		}
	}
}

// waitWithTimeoutErr is like waitWithTimeout but returns an error.
// This allows the caller to handle the error appropriately.
//
// Channel buffer size of 1 is critical: it ensures the goroutine can exit even if the
// timeout fires, preventing goroutine leaks. Without the buffer, if timeout occurs before
// Wait() completes, the goroutine would block forever on the channel send.
func waitWithTimeoutErr(cmd *exec.Cmd, timeout time.Duration, logger *slog.Logger) error {
	// Store PID early to prevent potential nil pointer panic in logging
	pid := -1
	if cmd.Process != nil {
		pid = cmd.Process.Pid
	}

	// Buffer size 1 ensures goroutine can exit even if timeout fires (prevents leak)
	done := make(chan error, 1)
	go func() {
		done <- cmd.Wait()
	}()

	select {
	case err := <-done:
		return err
	case <-time.After(timeout):
		if logger != nil {
			logger.Warn("Process wait timed out",
				"pid", pid,
				"timeout_seconds", timeout.Seconds())
		}
		// Try to kill the process
		if cmd.Process != nil {
			_ = cmd.Process.Kill()
			// Give it one more chance to exit
			select {
			case err := <-done:
				return fmt.Errorf("process wait timed out after %v (killed, exit error: %w)", timeout, err)
			case <-time.After(1 * time.Second):
				return fmt.Errorf("process wait timed out after %v and failed to kill", timeout)
			}
		}
		return fmt.Errorf("process wait timed out after %v", timeout)
	}
}

// Note: createSpectrogramWithSoX, getSoxSpectrogramArgs, and createSpectrogramWithFFmpeg
// have been removed. All spectrogram generation now uses the shared generator from
// internal/spectrogram/generator.go via c.spectrogramGenerator.GenerateFromFile().

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
