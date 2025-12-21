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
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/labstack/echo/v4"
	"github.com/tphakala/birdnet-go/internal/conf"
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
	c.Echo.POST("/api/v2/spectrogram/:id/generate", c.GenerateSpectrogramByID)

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
			return c.HandleError(ctx, err, "No audio clip available for this note", http.StatusNotFound)
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

// spectrogramParameters holds parsed query parameters for spectrogram requests.
// This struct is reusable across multiple endpoints.
type spectrogramParameters struct {
	width   int    // Pixel width for spectrogram
	sizeStr string // Size parameter value (for URL generation)
	raw     bool   // Whether to generate raw spectrogram without axes
}

// parseSpectrogramParameters extracts and validates spectrogram parameters from the request.
// This is a reusable helper used by multiple endpoints.
//
// Parameters:
//   - size: Spectrogram size - "sm" (400px), "md" (800px), "lg" (1000px), "xl" (1200px)
//   - width: Legacy parameter for custom width (1-2000px). Ignored if 'size' is present.
//   - raw: Whether to generate raw spectrogram without axes/legends
func parseSpectrogramParameters(ctx echo.Context) spectrogramParameters {
	params := spectrogramParameters{
		width:   SpectrogramSizeMd, // Default width (md)
		sizeStr: ctx.QueryParam("size"),
		raw:     parseRawParameter(ctx.QueryParam("raw")),
	}

	// Parse size parameter
	if params.sizeStr != "" {
		if validWidth, err := spectrogram.SizeToPixels(params.sizeStr); err == nil {
			params.width = validWidth
		}
		// Invalid size parameter falls back to width parameter or default
	}

	// Legacy width parameter support (only if size not specified)
	widthStr := ctx.QueryParam("width")
	if widthStr != "" && params.sizeStr == "" {
		if parsedWidth, err := strconv.Atoi(widthStr); err == nil && parsedWidth > 0 && parsedWidth <= 2000 {
			params.width = parsedWidth
		}
	}

	return params
}

// validateNoteIDAndGetClipPath validates the note ID parameter and retrieves the clip path.
// Returns the noteID and clipPath, or an error if validation fails.
func (c *Controller) validateNoteIDAndGetClipPath(ctx echo.Context) (noteID, clipPath string, err error) {
	noteID = ctx.Param("id")
	if noteID == "" {
		if c.apiLogger != nil {
			c.apiLogger.Error("Missing note ID for spectrogram request",
				"path", ctx.Request().URL.Path,
				"ip", ctx.RealIP())
		}
		err = c.HandleError(ctx, fmt.Errorf("missing ID"), "Note ID is required", http.StatusBadRequest)
		return
	}

	clipPath, err = c.DS.GetNoteClipPath(noteID)
	if err != nil {
		if c.apiLogger != nil {
			c.apiLogger.Error("Failed to get clip path from database",
				"note_id", noteID,
				"error", err.Error(),
				"path", ctx.Request().URL.Path,
				"ip", ctx.RealIP())
		}
		if errors.Is(err, os.ErrNotExist) || strings.Contains(err.Error(), "not found") {
			err = c.HandleError(ctx, err, "No audio clip available for this note", http.StatusNotFound)
			return
		}
		err = c.HandleError(ctx, err, "Failed to get clip path for note", http.StatusInternalServerError)
		return
	}

	if clipPath == "" {
		if c.apiLogger != nil {
			c.apiLogger.Warn("Empty clip path for note",
				"note_id", noteID,
				"path", ctx.Request().URL.Path,
				"ip", ctx.RealIP())
		}
		err = c.HandleError(ctx, fmt.Errorf("no audio file found"), "No audio clip available for this note", http.StatusNotFound)
		return
	}

	return
}

// handleUserRequestedMode handles spectrogram serving in user-requested mode.
// Returns true if the request was handled (either success or error response sent).
func (c *Controller) handleUserRequestedMode(ctx echo.Context, noteID, clipPath string, params spectrogramParameters) (bool, error) {
	// Normalize and validate the audio path
	clipsPrefix := c.Settings.Realtime.Audio.Export.Path
	normalizedPath := NormalizeClipPath(clipPath, clipsPrefix)
	relAudioPath, err := c.SFS.ValidateRelativePath(normalizedPath)

	if err == nil {
		// Build spectrogram path
		_, _, _, relSpectrogramPath := buildSpectrogramPaths(relAudioPath, params.width, params.raw)

		// Check if spectrogram already exists
		if _, statErr := c.SFS.StatRel(relSpectrogramPath); statErr == nil {
			// Spectrogram exists, serve it
			if c.apiLogger != nil {
				c.apiLogger.Debug("Serving existing spectrogram in user-requested mode",
					"note_id", noteID,
					"spectrogram_path", relSpectrogramPath,
					"path", ctx.Request().URL.Path,
					"ip", ctx.RealIP())
			}

			err = c.SFS.ServeRelativeFile(ctx, relSpectrogramPath)
			if err != nil {
				return true, c.translateSecureFSError(ctx, err, "Failed to serve spectrogram image")
			}
			return true, nil
		}
	}

	// Spectrogram doesn't exist in user-requested mode - return 404 with helpful message
	if c.apiLogger != nil {
		c.apiLogger.Debug("Spectrogram not found in user-requested mode",
			"note_id", noteID,
			"mode", conf.SpectrogramModeUserRequested,
			"path", ctx.Request().URL.Path,
			"ip", ctx.RealIP())
	}

	return c.returnSpectrogramNotGeneratedError(ctx)
}

// returnSpectrogramNotGeneratedError returns a standardized 404 response for user-requested mode
// when a spectrogram hasn't been generated yet.
func (c *Controller) returnSpectrogramNotGeneratedError(ctx echo.Context) (bool, error) {
	// Return JSON response with mode information using standard v2 error envelope.
	// Flow: <img> element's onerror handler triggers -> frontend makes fetch() call to same URL
	// -> this JSON response is parsed by frontend -> mode field triggers UI to show "Generate" button
	// Note: The <img> element doesn't parse this JSON; the error handler's fetch() call does.
	errorResp := NewErrorResponse(
		fmt.Errorf("spectrogram not generated"),
		"Spectrogram has not been generated yet. Click 'Generate Spectrogram' to create it.",
		http.StatusNotFound,
	)

	// Log the error with structured logging
	if c.apiLogger != nil {
		c.apiLogger.Error("Spectrogram not generated",
			"correlation_id", errorResp.CorrelationID,
			"mode", conf.SpectrogramModeUserRequested,
			"path", ctx.Request().URL.Path,
			"ip", ctx.RealIP())
	}

	// Return standard error response with mode in data field (API v2 envelope)
	// Mode is placed in data object to maintain envelope consistency
	return true, ctx.JSON(http.StatusNotFound, map[string]any{
		"error":          errorResp.Error,
		"message":        errorResp.Message,
		"code":           errorResp.Code,
		"correlation_id": errorResp.CorrelationID,
		"data": map[string]any{
			"mode": conf.SpectrogramModeUserRequested,
		},
	})
}

// handleAutoPreRenderMode handles spectrogram generation and serving in auto/prerender modes.
func (c *Controller) handleAutoPreRenderMode(ctx echo.Context, noteID, clipPath string, params spectrogramParameters) error {
	// Auto or prerender mode - generate on-demand if needed
	generationStart := time.Now()
	spectrogramPath, err := c.generateSpectrogram(ctx.Request().Context(), clipPath, params.width, params.raw)
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
// Response Format:
// The response format varies based on the spectrogram generation mode setting and availability.
// Clients MUST check Content-Type header to handle the response correctly.
//
// 1. Success - Spectrogram exists (Auto/Prerender Mode or already generated):
//   - Content-Type: image/png
//   - Body: Binary PNG image data
//   - Status: 200 OK
//
// 2. Error - User-Requested Mode (spectrogram not generated):
//   - Content-Type: application/json
//   - Status: 404 Not Found
//   - Body (API v2 envelope):
//     {
//     "error": "spectrogram not generated",
//     "message": "Spectrogram has not been generated yet. Click 'Generate Spectrogram' to create it.",
//     "code": 404,
//     "correlation_id": "abc12345",
//     "data": {
//     "mode": "user-requested"
//     }
//     }
//
// IMPORTANT: Clients must check Content-Type header to determine response format:
//   - image/png: Binary image data (display image)
//   - application/json: Error response (handle error, show generate button if data.mode=user-requested)
//
// TODO: Consider adding a dedicated endpoint or format query parameter for cleaner API design:
//
//	Option A: GET /api/v2/spectrogram/:id/info - Returns JSON metadata including mode and status
//	Option B: GET /api/v2/spectrogram/:id?format=json - Explicit format parameter
//
// This would eliminate Content-Type-based response type detection and provide a cleaner separation
// between image serving and metadata/status queries.
//
// The raw parameter defaults to true to maintain compatibility with existing cached
// spectrograms from the old HTMX API which generated raw spectrograms by default.
func (c *Controller) ServeSpectrogramByID(ctx echo.Context) error {
	// Validate note ID and get clip path
	noteID, clipPath, err := c.validateNoteIDAndGetClipPath(ctx)
	if err != nil {
		return err // Error already handled and logged
	}

	// Parse query parameters
	params := parseSpectrogramParameters(ctx)

	// Log request details
	if c.apiLogger != nil {
		c.apiLogger.Debug("Spectrogram requested by ID",
			"note_id", noteID,
			"clip_path", clipPath,
			"width", params.width,
			"raw", params.raw,
			"size_param", params.sizeStr,
			"path", ctx.Request().URL.Path,
			"ip", ctx.RealIP())
	}

	// Check spectrogram generation mode
	spectrogramMode := c.Settings.Realtime.Dashboard.Spectrogram.GetMode()

	// Handle user-requested mode
	if spectrogramMode == conf.SpectrogramModeUserRequested {
		handled, err := c.handleUserRequestedMode(ctx, noteID, clipPath, params)
		if handled {
			return err
		}
	}

	// Handle auto or prerender mode
	return c.handleAutoPreRenderMode(ctx, noteID, clipPath, params)
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
// Response Format (API v2 envelope):
//
//	{
//	  "data": {
//	    "status": "not_started|queued|generating|generated|failed|exists",
//	    "queuePosition": 0,  // Position in queue (0 if not queued)
//	    "startedAt": "2025-10-20T...",  // When generation started (if in progress)
//	    "message": "Additional status information"
//	  },
//	  "error": "",
//	  "message": "Status retrieved successfully"
//	}
//
// Status Values:
//   - "not_started": Spectrogram generation has not been requested
//   - "queued": Waiting in queue for generation slot
//   - "generating": Currently being generated
//   - "generated": Successfully generated (in queue cache)
//   - "failed": Generation failed
//   - "exists": Already exists on disk
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

	// Parse query parameters using shared helper
	params := parseSpectrogramParameters(ctx)

	// Build spectrogram path and key for status lookup
	// Must compute relSpectrogramPath BEFORE checking queue to ensure consistent key format
	audioPath := detection.ClipName
	clipsPrefix := c.Settings.Realtime.Audio.Export.Path
	normalizedPath := NormalizeClipPath(audioPath, clipsPrefix)
	relAudioPath, err := c.SFS.ValidateRelativePath(normalizedPath)
	if err != nil {
		// Path validation failed - return not_started status
		return ctx.JSON(http.StatusOK, map[string]any{
			"data": map[string]any{
				"status":        spectrogramStatusNotStarted,
				"queuePosition": 0,
				"message":       "Invalid audio path",
			},
			"error":   "",
			"message": "Spectrogram generation not started",
		})
	}

	// Build spectrogram path and key
	_, _, _, relSpectrogramPath := buildSpectrogramPaths(relAudioPath, params.width, params.raw)
	spectrogramKey := buildSpectrogramKey(relSpectrogramPath, params.width, params.raw)

	// Check queue status first (more volatile state)
	// Check if it's actively being processed using sync.Map (lock-free)
	if statusValue, existsInQueue := spectrogramQueue.Load(spectrogramKey); existsInQueue {
		// Type-safe cast with check
		status, ok := statusValue.(*SpectrogramQueueStatus)
		if !ok {
			getSpectrogramLogger().Error("Invalid queue status type",
				"key", spectrogramKey,
				"type", fmt.Sprintf("%T", statusValue))
			return c.HandleError(ctx, fmt.Errorf("invalid queue status type for key %s", spectrogramKey),
				"Invalid status data type", http.StatusInternalServerError)
		}
		return ctx.JSON(http.StatusOK, map[string]any{
			"data":    status.Get(), // Thread-safe snapshot
			"error":   "",
			"message": "Spectrogram generation status retrieved",
		})
	}

	// Not in queue, check if spectrogram already exists on disk
	// Check if file exists
	if _, err := c.SFS.StatRel(relSpectrogramPath); err == nil {
		return ctx.JSON(http.StatusOK, map[string]any{
			"data": map[string]any{
				"status":        spectrogramStatusExists,
				"queuePosition": 0,
				"message":       "Spectrogram already exists",
			},
			"error":   "",
			"message": "Spectrogram exists on disk",
		})
	}

	// Not in queue and doesn't exist on disk
	return ctx.JSON(http.StatusOK, map[string]any{
		"data": map[string]any{
			"status":        spectrogramStatusNotStarted,
			"queuePosition": 0,
			"message":       "Spectrogram generation not started",
		},
		"error":   "",
		"message": "Spectrogram not yet generated",
	})
}

// GenerateSpectrogramByID triggers spectrogram generation for a specific detection
//
// Route: POST /api/v2/spectrogram/:id/generate
//
// This endpoint is designed for "user-requested" mode where spectrograms are only
// generated when explicitly requested by the user clicking a button in the UI.
//
// Query Parameters:
//   - size: Spectrogram size - "sm" (400px), "md" (800px), "lg" (1000px), "xl" (1200px)
//     Default: "md"
//   - raw: Whether to generate raw spectrogram without axes/legends
//     Default: true (for backward compatibility)
//
// Response Format (API v2 envelope):
//
//	{
//	  "data": {
//	    "status": "generated",
//	    "path": "/api/v2/spectrogram/:id?raw=true"
//	  },
//	  "error": "",
//	  "message": "Spectrogram generated successfully"
//	}
//
// HTTP Status Codes:
//   - 200 OK: Spectrogram generated successfully
//   - 503 Service Unavailable: Audio file not ready (includes Retry-After header)
//   - 404 Not Found: Audio file not found
//   - 408 Request Timeout: Generation timed out
//   - 500 Internal Server Error: Generation failed
func (c *Controller) GenerateSpectrogramByID(ctx echo.Context) error {
	// Validate note ID and get clip path using shared helper
	noteID, clipPath, err := c.validateNoteIDAndGetClipPath(ctx)
	if err != nil {
		return err // Error already handled and logged
	}

	// Parse query parameters using shared helper
	params := parseSpectrogramParameters(ctx)

	// Log request details
	if c.apiLogger != nil {
		c.apiLogger.Debug("Spectrogram generation requested by user",
			"note_id", noteID,
			"clip_path", clipPath,
			"width", params.width,
			"raw", params.raw,
			"size_param", params.sizeStr,
			"path", ctx.Request().URL.Path,
			"ip", ctx.RealIP())
	}

	// Check if spectrogram already exists (fast path)
	// Also compute spectrogramKey for queue management
	clipsPrefix := c.Settings.Realtime.Audio.Export.Path
	normalizedPath := NormalizeClipPath(clipPath, clipsPrefix)
	relAudioPath, err := c.SFS.ValidateRelativePath(normalizedPath)
	if err != nil {
		// Path validation failed - return error immediately before spawning goroutine
		if c.apiLogger != nil {
			c.apiLogger.Error("Invalid audio path for spectrogram generation",
				"note_id", noteID,
				"clip_path", clipPath,
				"normalized_path", normalizedPath,
				"error", err.Error())
		}
		return c.HandleError(ctx, err, "Invalid audio path", http.StatusBadRequest)
	}

	// Build spectrogram paths and key (path is validated at this point)
	_, _, _, relSpectrogramPath := buildSpectrogramPaths(relAudioPath, params.width, params.raw)
	spectrogramKey := buildSpectrogramKey(relSpectrogramPath, params.width, params.raw)

	// Check if file already exists on disk
	if _, err := c.SFS.StatRel(relSpectrogramPath); err == nil {
		// Already exists, return immediately with generated status
		queryParams := url.Values{}
		sizeParam := params.sizeStr
		if sizeParam == "" {
			switch params.width {
			case SpectrogramSizeSm:
				sizeParam = "sm"
			case SpectrogramSizeMd:
				sizeParam = "md"
			case SpectrogramSizeLg:
				sizeParam = "lg"
			case SpectrogramSizeXl:
				sizeParam = "xl"
			default:
				sizeParam = "md"
			}
		}
		queryParams.Set("size", sizeParam)
		queryParams.Set("raw", strconv.FormatBool(params.raw))
		spectrogramURL := fmt.Sprintf("/api/v2/spectrogram/%s?%s", url.PathEscape(noteID), queryParams.Encode())

		return ctx.JSON(http.StatusOK, map[string]any{
			"data": map[string]any{
				"status": spectrogramStatusExists,
				"path":   spectrogramURL,
			},
			"error":   "",
			"message": "Spectrogram already exists",
		})
	}

	// Check if generation is already in progress (prevents spawning duplicate goroutines)
	if statusValue, exists := spectrogramQueue.Load(spectrogramKey); exists {
		if status, ok := statusValue.(*SpectrogramQueueStatus); ok {
			currentStatus := status.GetStatus()
			if currentStatus == spectrogramStatusQueued || currentStatus == spectrogramStatusGenerating {
				return ctx.JSON(http.StatusAccepted, map[string]any{
					"data":    status.Get(),
					"error":   "",
					"message": "Generation already in progress",
				})
			}
		}
	}

	// Initialize queue status BEFORE spawning goroutine (prevents "not_started" flicker)
	c.initializeQueueStatus(spectrogramKey)

	// Start async generation in background with proper cleanup and panic recovery
	// Track goroutine lifecycle for graceful shutdown
	c.wg.Go(func() {
		// Ensure cleanup even if panic occurs (prevents memory leaks)
		defer func() {
			if r := recover(); r != nil {
				if c.apiLogger != nil {
					c.apiLogger.Error("Panic in async spectrogram generation",
						"note_id", noteID,
						"panic", r)
				}
			}
		}()

		// Use controller context (respects shutdown signals) with timeout
		bgCtx, cancel := context.WithTimeout(c.ctx, spectrogramGenerationTimeout)
		defer cancel()

		spectrogramPath, err := c.generateSpectrogram(bgCtx, clipPath, params.width, params.raw)

		if err != nil {
			// Update queue status so polling clients see the failure
			// Use spectrogramKey computed earlier (if available)
			if spectrogramKey != "" {
				c.updateQueueStatus(spectrogramKey, spectrogramStatusFailed, 0, "Generation failed: "+err.Error())
			}

			if c.apiLogger != nil {
				c.apiLogger.Error("Async spectrogram generation failed",
					"note_id", noteID,
					"clip_path", clipPath,
					"error", err.Error())
			}
		} else if c.apiLogger != nil {
			c.apiLogger.Info("Async spectrogram generated successfully",
				"note_id", noteID,
				"spectrogram_path", spectrogramPath)
		}
	})

	// Return 202 Accepted immediately - client should poll status endpoint
	// If we initialized queue status, return it; otherwise return generic queued response
	responseData := map[string]any{
		"status":        spectrogramStatusQueued,
		"queuePosition": 0,
		"message":       "Generation queued",
	}

	if spectrogramKey != "" {
		if statusValue, exists := spectrogramQueue.Load(spectrogramKey); exists {
			if status, ok := statusValue.(*SpectrogramQueueStatus); ok {
				responseData = status.Get()
			}
		}
	}

	return ctx.JSON(http.StatusAccepted, map[string]any{
		"data":    responseData,
		"error":   "",
		"message": "Generation request accepted. Poll /api/v2/spectrogram/:id/status for progress.",
	})
}

// maxConcurrentSpectrograms limits concurrent spectrogram generations to avoid overloading the system.
// Set to 4 to match the number of CPU cores on Raspberry Pi 4/5, which is the most common
// deployment platform for BirdNET-Go. This prevents severe CPU contention and ensures
// responsive performance on resource-constrained devices.
const maxConcurrentSpectrograms = 4

// semaphoreAcquireTimeout is the maximum time to wait for a semaphore slot before timing out
const semaphoreAcquireTimeout = 30 * time.Second

// spectrogramRetryAfterSeconds is the suggested retry delay in seconds for 503 responses
// when audio files are not yet ready for processing
const spectrogramRetryAfterSeconds = "2"

// Spectrogram generation timing and cache constants
const (
	spectrogramGenerationTimeout = 5 * time.Minute  // Max time for async spectrogram generation
	spectrogramRetryDelay        = 2 * time.Second  // Default retry delay for validation errors
	ffprobeDurationTimeout       = 3 * time.Second  // Timeout for ffprobe duration queries
	failedStatusRetentionTime    = 30 * time.Second // How long to retain failed statuses for polling
	ffprobeCacheMaxEntries       = 100              // Maximum entries in ffprobe cache before cleanup
	spectrogramVerifyRetries     = 3                // Number of verification retries after generation
	spectrogramVerifyBaseDelay   = 50               // Base delay in milliseconds for verification retries
)

var (
	spectrogramSemaphore = make(chan struct{}, maxConcurrentSpectrograms)
	spectrogramGroup     singleflight.Group // Prevents duplicate generations

	// Track spectrogram generation queue status
	// Using sync.Map for lock-free concurrent access (fixes race condition with multiple browsers)
	spectrogramQueue sync.Map // map[string]*SpectrogramQueueStatus
)

// SpectrogramQueueStatus tracks the status of a spectrogram generation request
// Thread-safe: uses internal mutex to prevent race conditions during concurrent updates
type SpectrogramQueueStatus struct {
	mu            sync.RWMutex
	status        string    // "queued", "generating", "generated", "failed", "exists", "not_started"
	queuePosition int       // Position in queue (0 if generating/generated)
	startedAt     time.Time // When generation started
	message       string    // Additional status message
}

// Update atomically updates all fields
// Only sets startedAt when transitioning to "generating" state to preserve accurate timing
func (s *SpectrogramQueueStatus) Update(status string, queuePos int, message string) {
	s.mu.Lock()
	defer s.mu.Unlock()

	// Capture previous status to detect state transitions
	previousStatus := s.status

	// Update fields
	s.status = status
	s.queuePosition = queuePos
	s.message = message

	// Only set startedAt when transitioning into "generating" state
	// This preserves accurate generation start time across multiple updates
	if status == spectrogramStatusGenerating &&
		(previousStatus != spectrogramStatusGenerating || s.startedAt.IsZero()) {
		s.startedAt = time.Now()
	}
}

// Get returns a snapshot of the current status (safe for JSON marshaling)
func (s *SpectrogramQueueStatus) Get() map[string]any {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return map[string]any{
		"status":        s.status,
		"queuePosition": s.queuePosition,
		"startedAt":     s.startedAt,
		"message":       s.message,
	}
}

// GetStatus returns just the status string (thread-safe)
func (s *SpectrogramQueueStatus) GetStatus() string {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.status
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

// buildSpectrogramKey generates a consistent unique key for spectrogram queue management.
// This key is used to track generation status across POST /generate and GET /status endpoints.
// Format: "path:width:raw" (e.g., "clips/2025/01/audio_123.wav:400:true")
func buildSpectrogramKey(relSpectrogramPath string, width int, raw bool) string {
	return fmt.Sprintf("%s:%d:%t", relSpectrogramPath, width, raw)
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
			RetryAfter: spectrogramRetryDelay, // Default retry for validation errors
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
	if len(ffprobeCache.validation) > ffprobeCacheMaxEntries {
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
		if len(ffprobeCache.duration) > ffprobeCacheMaxEntries {
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
	durationCtx, cancel := context.WithTimeout(ctx, ffprobeDurationTimeout)
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
	if len(ffprobeCache.duration) > ffprobeCacheMaxEntries {
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

// updateQueueStatus updates the spectrogram generation queue status (thread-safe)
func (c *Controller) updateQueueStatus(spectrogramKey, status string, queuePos int, message string) {
	// Using sync.Map for lock-free lookups + struct mutex for safe updates
	if statusValue, exists := spectrogramQueue.Load(spectrogramKey); exists {
		if queueStatus, ok := statusValue.(*SpectrogramQueueStatus); ok {
			queueStatus.Update(status, queuePos, message) // Thread-safe update
		} else {
			getSpectrogramLogger().Error("Invalid queue status type in update",
				"key", spectrogramKey,
				"type", fmt.Sprintf("%T", statusValue))
		}
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
// Optimized to minimize lock hold time - calculation done outside lock, only write is locked
func (c *Controller) initializeQueueStatus(spectrogramKey string) {
	// Step 1: Calculate queue position OUTSIDE the lock to minimize contention
	currentSlotsInUse := len(spectrogramSemaphore)

	// Log current semaphore state for debugging
	getSpectrogramLogger().Debug("Checking semaphore availability",
		"spectrogram_key", spectrogramKey,
		"current_slots_in_use", currentSlotsInUse,
		"max_concurrent", maxConcurrentSpectrograms,
		"semaphore_full", currentSlotsInUse >= maxConcurrentSpectrograms)

	var queuePosition int
	if currentSlotsInUse >= maxConcurrentSpectrograms {
		// All slots are taken, need to count waiting requests
		// Use sync.Map.Range for lock-free iteration
		waitingCount := 0
		spectrogramQueue.Range(func(key, value any) bool {
			if status, ok := value.(*SpectrogramQueueStatus); ok {
				if status.GetStatus() == spectrogramStatusQueued {
					waitingCount++
				}
			}
			return true // continue iteration
		})
		queuePosition = waitingCount + 1
	} else {
		// Slot is available, will run immediately
		queuePosition = 0
	}

	// Step 2: Create and store status in sync.Map (lock-free operation)
	status := &SpectrogramQueueStatus{}
	status.Update(spectrogramStatusQueued, queuePosition, "Waiting for generation slot")
	spectrogramQueue.Store(spectrogramKey, status)
}

// cleanupQueueStatus removes the queue entry for a spectrogram request
// Failed statuses are retained briefly (30s) so polling clients can see the error
func (c *Controller) cleanupQueueStatus(spectrogramKey string) {
	// Check if this is a failed status that should be retained temporarily
	if statusValue, ok := spectrogramQueue.Load(spectrogramKey); ok {
		if status, ok := statusValue.(*SpectrogramQueueStatus); ok {
			if status.GetStatus() == spectrogramStatusFailed {
				// Keep failed status for a brief period so clients can poll and see the error
				// After that, clean it up automatically
				time.AfterFunc(failedStatusRetentionTime, func() {
					spectrogramQueue.Delete(spectrogramKey)
					getSpectrogramLogger().Debug("Cleaned up failed spectrogram status after TTL",
						"spectrogram_key", spectrogramKey)
				})
				return
			}
		}
	}

	// For non-failed statuses (success, exists, etc.), delete immediately
	spectrogramQueue.Delete(spectrogramKey)
}

// acquireSemaphoreSlot acquires a semaphore slot for spectrogram generation
// With timeout handling to prevent indefinite blocking
func (c *Controller) acquireSemaphoreSlot(ctx context.Context, spectrogramKey string) error {
	slotsInUseBeforeAcquire := len(spectrogramSemaphore)
	availableSlots := maxConcurrentSpectrograms - slotsInUseBeforeAcquire

	getSpectrogramLogger().Debug("Attempting to acquire semaphore slot",
		"spectrogram_key", spectrogramKey,
		"slots_in_use", slotsInUseBeforeAcquire,
		"slots_available", availableSlots,
		"max_concurrent", maxConcurrentSpectrograms)

	// Add explicit timeout for semaphore acquisition
	timeoutCtx, cancel := context.WithTimeout(ctx, semaphoreAcquireTimeout)
	defer cancel()

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

	case <-timeoutCtx.Done():
		err := timeoutCtx.Err()
		if err == context.DeadlineExceeded {
			getSpectrogramLogger().Warn("Timeout waiting for semaphore slot",
				"spectrogram_key", spectrogramKey,
				"timeout_seconds", int(semaphoreAcquireTimeout.Seconds()),
				"slots_in_use", len(spectrogramSemaphore))
			c.updateQueueStatus(spectrogramKey, spectrogramStatusFailed, 0, "Request timeout - server busy, please retry")
			return fmt.Errorf("timeout waiting for generation slot: %w", err)
		}

		getSpectrogramLogger().Debug("Context canceled while waiting for semaphore",
			"spectrogram_key", spectrogramKey,
			"error", err)

		c.updateQueueStatus(spectrogramKey, spectrogramStatusFailed, 0, "Generation canceled")
		return err
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
	for i := range spectrogramVerifyRetries {
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

		if i < spectrogramVerifyRetries-1 {
			// Wait a bit for filesystem to sync (50ms, then 100ms)
			time.Sleep(time.Duration((i+1)*spectrogramVerifyBaseDelay) * time.Millisecond)
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

// generateWithFallback attempts to generate a spectrogram with SoX, falling back to FFmpeg on failure
func (c *Controller) generateWithFallback(ctx context.Context, absAudioPath, absSpectrogramPath, spectrogramKey string, width int, raw bool) error {
	generationStart := time.Now()

	getSpectrogramLogger().Debug("Starting spectrogram generation via shared generator",
		"spectrogram_key", spectrogramKey,
		"abs_audio_path", absAudioPath,
		"width", width,
		"raw", raw)

	// Use shared generator which handles Sox→FFmpeg fallback internally
	if err := c.spectrogramGenerator.GenerateFromFile(ctx, absAudioPath, absSpectrogramPath, width, raw); err != nil {
		// Check if this is an expected operational error (context canceled, process killed)
		// These are normal events during shutdown, timeout, or resource management
		isOperationalError := errors.Is(err, context.Canceled) ||
			errors.Is(err, context.DeadlineExceeded) ||
			strings.Contains(err.Error(), "signal: killed")

		if isOperationalError {
			// Log at Debug level for expected operational events
			getSpectrogramLogger().Debug("Spectrogram generation canceled or interrupted",
				"spectrogram_key", spectrogramKey,
				"error", err.Error(),
				"duration_ms", time.Since(generationStart).Milliseconds(),
				"abs_audio_path", absAudioPath,
				"abs_spectrogram_path", absSpectrogramPath)
		} else {
			// Log at Error level for unexpected failures
			getSpectrogramLogger().Error("Spectrogram generation failed",
				"spectrogram_key", spectrogramKey,
				"error", err.Error(),
				"duration_ms", time.Since(generationStart).Milliseconds(),
				"abs_audio_path", absAudioPath,
				"abs_spectrogram_path", absSpectrogramPath)
		}
		return err
	}

	getSpectrogramLogger().Debug("Spectrogram generation completed via shared generator",
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
	spectrogramKey := buildSpectrogramKey(relSpectrogramPath, width, raw)

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

// Note: createSpectrogramWithSoX, getSoxSpectrogramArgs, createSpectrogramWithFFmpeg,
// waitWithTimeout, and waitWithTimeoutErr have been removed. All spectrogram generation
// now uses the shared generator from internal/spectrogram/generator.go via
// c.spectrogramGenerator.GenerateFromFile().

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
