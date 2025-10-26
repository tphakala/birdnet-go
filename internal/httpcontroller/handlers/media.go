package handlers

import (
	"bytes"
	_ "embed"
	"errors"
	"fmt"
	"html"
	"html/template"
	"log"
	"log/slog"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"regexp"
	"runtime"
	"strings"
	"time"

	"github.com/labstack/echo/v4"
	"github.com/tphakala/birdnet-go/internal/conf"
	"github.com/tphakala/birdnet-go/internal/imageprovider"
	"github.com/tphakala/birdnet-go/internal/logging"
)

// MaxClipNameLength is the maximum allowed length for a clip name
const MaxClipNameLength = 255

// AllowedCharacters is a regex pattern for allowed characters in clip names
const AllowedCharacters = `^[a-zA-Z0-9_/.-]+$`

// Embed the spectrogram placeholder SVG to avoid CWD-dependent file access
//
//go:embed spectrogram-placeholder.svg
var spectrogramPlaceholderSVG []byte

var (
	ErrEmptyClipName     = errors.New("empty clip name")
	ErrClipNameTooLong   = errors.New("clip name too long")
	ErrInvalidCharacters = errors.New("invalid characters in clip name")
	ErrPathTraversal     = errors.New("path traversal attempt detected")
)

// Limit concurrent spectrogram generations to avoid overloading the system
const MaxConcurrentSpectrograms = 4

var spectrogramSemaphore = make(chan struct{}, MaxConcurrentSpectrograms)

// sanitizeClipName performs sanity checks on the clip name and ensures it's a relative path
func (h *Handlers) sanitizeClipName(clipName string) (string, error) {
	// Check if the clip name is empty
	if clipName == "" {
		return "", ErrEmptyClipName
	}

	// Decode the clip name
	decodedClipName, err := url.QueryUnescape(clipName)
	if err != nil {
		return "", fmt.Errorf("error decoding clip name: %w", err)
	}
	h.Debug("sanitizeClipName: Decoded clip name: %s", decodedClipName)

	// Check the length of the decoded clip name
	if len(decodedClipName) > MaxClipNameLength {
		return "", ErrClipNameTooLong
	}

	// Check for allowed characters
	if !regexp.MustCompile(AllowedCharacters).MatchString(decodedClipName) {
		h.Debug("sanitizeClipName: Invalid characters in clip name: %s", decodedClipName)
		return "", ErrInvalidCharacters
	}

	// Clean the path and ensure it's relative
	cleanPath := filepath.Clean(decodedClipName)

	// Convert to forward slashes and normalize multiple separators
	cleanPath = strings.ReplaceAll(cleanPath, "\\", "/")
	cleanPath = strings.ReplaceAll(cleanPath, "//", "/")
	h.Debug("sanitizeClipName: Cleaned path: %s", cleanPath)

	// Get absolute paths for comparison
	exportPath := conf.Setting().Realtime.Audio.Export.Path
	absExportPath, err := filepath.Abs(exportPath)
	if err != nil {
		return "", fmt.Errorf("failed to resolve export path: %w", err)
	}

	// Join with export path and get absolute path
	fullPath := filepath.Join(exportPath, cleanPath)
	absPath, err := filepath.Abs(fullPath)
	if err != nil {
		return "", fmt.Errorf("failed to resolve full path: %w", err)
	}

	// Check if the resolved path is within the export directory
	if !strings.HasPrefix(absPath, absExportPath) {
		h.Debug("sanitizeClipName: Path traversal attempt detected - path resolves outside export directory: %s", absPath)
		return "", ErrPathTraversal
	}

	// Remove 'clips/' prefix if present (case-insensitive for Windows compatibility)
	prefixLower := strings.ToLower(cleanPath)
	if strings.HasPrefix(prefixLower, "clips/") {
		cleanPath = cleanPath[6:] // Remove "clips/" (6 characters)
	}
	h.Debug("sanitizeClipName: Path after removing clips prefix: %s", cleanPath)

	// If the path is absolute, make it relative to the export path
	if filepath.IsAbs(cleanPath) {
		h.Debug("sanitizeClipName: Found absolute path: %s", cleanPath)
		exportPath := conf.Setting().Realtime.Audio.Export.Path
		h.Debug("sanitizeClipName: Export path from settings: %s", exportPath)

		// Case-insensitive check for Windows compatibility
		if runtime.GOOS == "windows" {
			cleanPathLower := strings.ToLower(cleanPath)
			exportPathLower := strings.ToLower(exportPath)
			if strings.HasPrefix(cleanPathLower, exportPathLower) {
				cleanPath = cleanPath[len(exportPath):]
				cleanPath = strings.TrimPrefix(cleanPath, string(os.PathSeparator))
			} else {
				return "", fmt.Errorf("invalid path: absolute path not under export directory")
			}
		} else {
			if strings.HasPrefix(cleanPath, exportPath) {
				cleanPath = strings.TrimPrefix(cleanPath, exportPath)
				cleanPath = strings.TrimPrefix(cleanPath, string(os.PathSeparator))
			} else {
				return "", fmt.Errorf("invalid path: absolute path not under export directory")
			}
		}
		h.Debug("sanitizeClipName: Converted to relative path: %s", cleanPath)
	}

	// Check final path length including the export path
	fullPath = filepath.Join(conf.Setting().Realtime.Audio.Export.Path, cleanPath)
	if len(fullPath) > 250 { // Safe limit for most OS
		return "", fmt.Errorf("final path length exceeds system limits")
	}

	// Convert to forward slashes for web URLs
	cleanPath = filepath.ToSlash(cleanPath)
	h.Debug("sanitizeClipName: Final path with forward slashes: %s", cleanPath)

	return cleanPath, nil
}

// getFullPath returns the full filesystem path for a relative clip path
func getFullPath(relativePath string) string {
	// Clean the relative path first
	relativePath = filepath.Clean(relativePath)

	// Get the export path
	exportPath := conf.Setting().Realtime.Audio.Export.Path

	// If relativePath already starts with the export path, return it cleaned
	if strings.HasPrefix(strings.ToLower(relativePath), strings.ToLower(exportPath)) {
		return relativePath
	}

	// Join and clean the paths
	fullPath := filepath.Join(exportPath, relativePath)
	return filepath.Clean(fullPath)
}

// getWebPath converts a filesystem path to a web-safe path
func getWebPath(path string) string {
	// Convert absolute path to relative path if it starts with the export path
	exportPath := conf.Setting().Realtime.Audio.Export.Path
	if strings.HasPrefix(path, exportPath) {
		path = strings.TrimPrefix(path, exportPath)
		path = strings.TrimPrefix(path, string(os.PathSeparator))
	}

	// Convert path separators to forward slashes for web URLs
	return filepath.ToSlash(path)
}

// selectBirdImage handles the logic for selecting a bird image based on user preferences and fallback policies
func (h *Handlers) selectBirdImage(scientificName string) (*imageprovider.BirdImage, error) {
	// Guard against empty input
	if scientificName == "" {
		return nil, fmt.Errorf("scientific name cannot be empty")
	}

	// Get user's preferred image provider from settings
	settings := conf.Setting()
	preferredProvider := settings.Realtime.Dashboard.Thumbnails.ImageProvider
	fallbackPolicy := settings.Realtime.Dashboard.Thumbnails.FallbackPolicy

	h.Debug("Image request for %s - Preferred provider: %s, Fallback policy: %s",
		scientificName, preferredProvider, fallbackPolicy)

	// If the BirdImageCache is nil, return early
	if h.BirdImageCache == nil {
		return nil, fmt.Errorf("bird image cache not available")
	}

	// If we have access to a registry and a specific provider is requested
	if preferredProvider != "auto" && h.BirdImageCache.GetRegistry() != nil {
		registry := h.BirdImageCache.GetRegistry()

		// Log available providers
		var providers []string
		registry.RangeProviders(func(name string, cache *imageprovider.BirdImageCache) bool {
			providers = append(providers, name)
			return true
		})
		h.Debug("Available providers: %v", providers)

		if cache, ok := registry.GetCache(preferredProvider); ok {
			h.Debug("Found preferred provider cache: %s", preferredProvider)

			// Try to get the image from the preferred provider
			if birdImage, err := cache.Get(scientificName); err == nil {
				h.Debug("Successfully got image from %s for %s: %s",
					preferredProvider, scientificName, birdImage.URL)
				return &birdImage, nil
			} else {
				h.Debug("Failed to get image from %s: %v", preferredProvider, err)
			}

			// If preferred provider fails and fallback is disabled, return empty
			if fallbackPolicy != "all" {
				h.Debug("No fallback allowed (policy: %s), returning empty", fallbackPolicy)
				return nil, fmt.Errorf("preferred provider failed and fallback is disabled")
			}
		} else {
			h.Debug("Preferred provider '%s' not found in registry", preferredProvider)
			// If preferred provider doesn't exist and fallback is disabled, return empty
			if fallbackPolicy != "all" {
				h.Debug("No fallback allowed (policy: %s) and provider not found, returning empty", fallbackPolicy)
				return nil, fmt.Errorf("preferred provider not found and fallback is disabled")
			}
		}
	} else {
		h.Debug("Using default provider - Provider set to auto: %v, Registry available: %v",
			preferredProvider == "auto", h.BirdImageCache.GetRegistry() != nil)
	}

	// At this point, either we're using "auto" mode or fallback is allowed
	// Try all available providers
	if registry := h.BirdImageCache.GetRegistry(); registry != nil {
		var lastError error
		var foundImage *imageprovider.BirdImage

		registry.RangeProviders(func(name string, cache *imageprovider.BirdImageCache) bool {
			// Skip if this is the preferred provider that already failed
			if name == preferredProvider && preferredProvider != "auto" {
				return true // Continue to next provider
			}

			birdImage, err := cache.Get(scientificName)
			if err == nil {
				h.Debug("Successfully got image from fallback provider %s for %s: %s",
					name, scientificName, birdImage.URL)
				// Found a valid image, store and stop iteration
				// Copy the value to avoid capturing loop variable address
				imgCopy := birdImage
				foundImage = &imgCopy
				lastError = nil
				return false // Stop iteration
			}

			h.Debug("Fallback provider %s failed for %s: %v", name, scientificName, err)
			lastError = err
			return true // Continue to next provider
		})

		if foundImage != nil {
			return foundImage, nil
		}

		if lastError != nil {
			h.Debug("All providers failed for %s", scientificName)
			return nil, lastError
		}
	} else {
		h.Debug("No image provider registry available")
		return nil, fmt.Errorf("no image provider registry available")
	}

	return nil, fmt.Errorf("no image found for %s", scientificName)
}

// Thumbnail returns the URL for a thumbnail image of the specified bird
func (h *Handlers) Thumbnail(scientificName string) string {
	if h.BirdImageCache == nil {
		h.Debug("BirdImageCache is nil, cannot get thumbnail")
		return ""
	}

	birdImage, err := h.selectBirdImage(scientificName)
	if err != nil || birdImage == nil {
		h.Debug("Failed to select image for %s: %v", scientificName, err)
		return ""
	}

	return birdImage.URL
}

// ThumbnailAttribution returns the attribution for a thumbnail image of the specified bird
func (h *Handlers) ThumbnailAttribution(scientificName string) template.HTML {
	if h.BirdImageCache == nil {
		h.Debug("BirdImageCache is nil, cannot get thumbnail attribution")
		return template.HTML("")
	}

	birdImage, err := h.selectBirdImage(scientificName)
	if err != nil || birdImage == nil {
		h.Debug("Failed to select image for attribution %s: %v", scientificName, err)
		return template.HTML("")
	}

	// Format the attribution string
	if birdImage.AuthorName == "" || birdImage.LicenseName == "" {
		return template.HTML("")
	}

	var attribution string
	if birdImage.AuthorURL == "" {
		attribution = fmt.Sprintf("© %s / <a href=\"%q\">%s</a>",
			html.EscapeString(birdImage.AuthorName),
			html.EscapeString(birdImage.LicenseURL),
			html.EscapeString(birdImage.LicenseName))
	} else {
		attribution = fmt.Sprintf("© <a href=\"%q\">%s</a> / <a href=\"%q\">%s</a>",
			html.EscapeString(birdImage.AuthorURL),
			html.EscapeString(birdImage.AuthorName),
			html.EscapeString(birdImage.LicenseURL),
			html.EscapeString(birdImage.LicenseName))
	}

	return template.HTML(attribution)
}

// ServeSpectrogram serves or generates a spectrogram for a given clip.
// API: GET /api/v1/media/spectrogram
func (h *Handlers) ServeSpectrogram(c echo.Context) error {
	startTime := time.Now()

	// Create structured logger for this operation
	logger := logging.ForService("htmx_media_handler")
	if logger == nil {
		logger = slog.Default().With("service", "htmx_media_handler")
	}

	logger.Debug("HTMX API spectrogram request initiated",
		slog.String("url", c.Request().URL.String()),
		slog.String("method", c.Request().Method),
		slog.String("user_agent", c.Request().UserAgent()),
		slog.String("remote_addr", c.RealIP()),
	)

	h.Debug("ServeSpectrogram: Handler called with URL: %s", c.Request().URL.String())

	// Extract clip name from the query parameters
	clipName := c.QueryParam("clip")
	h.Debug("ServeSpectrogram: Raw clip name from query: %s", clipName)
	logger.Debug("Extracted clip name from query parameters",
		slog.String("raw_clip_name", clipName),
	)

	// Sanitize the clip name
	sanitizedClipName, err := h.sanitizeClipName(clipName)
	if err != nil {
		logger.Error("Clip name sanitization failed, serving placeholder",
			slog.String("raw_clip_name", clipName),
			slog.String("error", err.Error()),
			slog.Duration("request_duration", time.Since(startTime)),
		)
		h.Debug("ServeSpectrogram: Error sanitizing clip name: %v", err)
		return serveSpectrogramPlaceholder(c)
	}
	logger.Debug("Clip name sanitized successfully",
		slog.String("raw_clip_name", clipName),
		slog.String("sanitized_clip_name", sanitizedClipName),
	)
	h.Debug("ServeSpectrogram: Sanitized clip name: %s", sanitizedClipName)

	// Get the full path to the audio file using consistent path handling
	fullPath := getFullPath(sanitizedClipName)
	h.Debug("ServeSpectrogram: Full audio path: %s", fullPath)
	logger.Debug("Constructed full audio file path",
		slog.String("sanitized_clip_name", sanitizedClipName),
		slog.String("full_path", fullPath),
	)

	// Verify that the audio file exists
	exists, err := fileExists(fullPath)
	if err != nil {
		logger.Error("Audio file existence check failed, serving placeholder",
			slog.String("full_path", fullPath),
			slog.String("error", err.Error()),
			slog.Duration("request_duration", time.Since(startTime)),
		)
		h.Debug("ServeSpectrogram: Error checking audio file: %v", err)
		return serveSpectrogramPlaceholder(c)
	}
	if !exists {
		logger.Error("Audio file not found, serving placeholder",
			slog.String("full_path", fullPath),
			slog.Duration("request_duration", time.Since(startTime)),
		)
		h.Debug("ServeSpectrogram: Audio file not found: %s", fullPath)
		return serveSpectrogramPlaceholder(c)
	}
	logger.Debug("Audio file verified successfully",
		slog.String("full_path", fullPath),
	)
	h.Debug("ServeSpectrogram: Audio file exists at: %s", fullPath)

	// Construct the path to the spectrogram image
	spectrogramWidth := 400 // Default width for HTMX API
	spectrogramPath, err := h.getSpectrogramPath(fullPath, spectrogramWidth)
	if err != nil {
		logger.Error("Spectrogram path generation failed, serving placeholder",
			slog.String("full_path", fullPath),
			slog.Int("width", spectrogramWidth),
			slog.String("error", err.Error()),
			slog.Duration("request_duration", time.Since(startTime)),
		)
		h.Debug("ServeSpectrogram: Error getting spectrogram path: %v", err)
		return serveSpectrogramPlaceholder(c)
	}
	logger.Debug("Spectrogram path generated successfully",
		slog.String("audio_path", fullPath),
		slog.String("spectrogram_path", spectrogramPath),
		slog.Int("width", spectrogramWidth),
	)
	h.Debug("ServeSpectrogram: Final spectrogram path: %s", spectrogramPath)

	// Verify the spectrogram exists
	exists, err = fileExists(spectrogramPath)
	if err != nil {
		logger.Error("Spectrogram existence check failed, serving placeholder",
			slog.String("spectrogram_path", spectrogramPath),
			slog.String("error", err.Error()),
			slog.Duration("request_duration", time.Since(startTime)),
		)
		h.Debug("ServeSpectrogram: Error checking spectrogram file: %v", err)
		return serveSpectrogramPlaceholder(c)
	}
	if !exists {
		logger.Info("Spectrogram not found, initiating on-demand generation",
			slog.String("spectrogram_path", spectrogramPath),
			slog.String("audio_path", fullPath),
			slog.Int("width", spectrogramWidth),
		)
		h.Debug("ServeSpectrogram: Spectrogram file not found, attempting to create it")

		// Acquire semaphore before generating spectrogram
		logger.Debug("Acquiring semaphore for spectrogram generation",
			slog.Int("max_concurrent", MaxConcurrentSpectrograms),
		)
		h.Debug("ServeSpectrogram: waiting for available slot for spectrogram generation")
		semaphoreStartTime := time.Now()
		spectrogramSemaphore <- struct{}{}
		semaphoreWaitDuration := time.Since(semaphoreStartTime)

		logger.Debug("Semaphore acquired for spectrogram generation",
			slog.Duration("semaphore_wait_duration", semaphoreWaitDuration),
		)

		defer func() {
			<-spectrogramSemaphore
			logger.Debug("Released semaphore slot for spectrogram generation")
			h.Debug("ServeSpectrogram: released semaphore slot")
		}()

		// Try to create the spectrogram using shared generator
		// HTMX handler always uses auto mode (on-demand generation) regardless of config
		generationStartTime := time.Now()
		ctx := c.Request().Context()

		// Defensive check for nil generator
		if h.spectrogramGenerator == nil {
			logger.Error("Spectrogram generator is not initialized",
				slog.String("audio_path", fullPath),
				slog.String("spectrogram_path", spectrogramPath),
			)
			h.Debug("ServeSpectrogram: Generator is nil!")
			return serveSpectrogramPlaceholder(c)
		}

		// Ensure paths are absolute before calling generator
		// The generator requires absolute paths for security validation
		absAudioPath, err := filepath.Abs(fullPath)
		if err != nil {
			logger.Error("Failed to get absolute path for audio file",
				slog.String("full_path", fullPath),
				slog.String("error", err.Error()),
			)
			return serveSpectrogramPlaceholder(c)
		}

		absSpectrogramPath, err := filepath.Abs(spectrogramPath)
		if err != nil {
			logger.Error("Failed to get absolute path for spectrogram",
				slog.String("spectrogram_path", spectrogramPath),
				slog.String("error", err.Error()),
			)
			return serveSpectrogramPlaceholder(c)
		}

		logger.Info("Calling spectrogram generator",
			slog.String("audio_path", absAudioPath),
			slog.String("output_path", absSpectrogramPath),
			slog.Int("width", spectrogramWidth),
			slog.Bool("raw", true),
		)

		// HTMX UI always uses raw=true (no axes/legends) for backward compatibility
		err = h.spectrogramGenerator.GenerateFromFile(ctx, absAudioPath, absSpectrogramPath, spectrogramWidth, true)
		generationDuration := time.Since(generationStartTime)

		if err != nil {
			logger.Error("Spectrogram generation returned error",
				slog.String("audio_path", absAudioPath),
				slog.String("spectrogram_path", absSpectrogramPath),
				slog.Int("width", spectrogramWidth),
				slog.String("error", err.Error()),
				slog.Duration("generation_duration", generationDuration),
				slog.Duration("total_request_duration", time.Since(startTime)),
			)
			h.Debug("ServeSpectrogram: Failed to create spectrogram: %v", err)
			return serveSpectrogramPlaceholder(c)
		}

		logger.Info("Spectrogram generation returned success",
			slog.String("audio_path", absAudioPath),
			slog.String("spectrogram_path", absSpectrogramPath),
			slog.Int("width", spectrogramWidth),
			slog.Duration("generation_duration", generationDuration),
			slog.Duration("semaphore_wait_duration", semaphoreWaitDuration),
		)
		h.Debug("ServeSpectrogram: Successfully created spectrogram at: %s", absSpectrogramPath)

		// Update spectrogramPath to absolute path for final check and serving
		spectrogramPath = absSpectrogramPath
	} else {
		logger.Debug("Existing spectrogram found, serving cached version",
			slog.String("spectrogram_path", spectrogramPath),
		)
	}

	// Final check if the spectrogram exists after potential creation
	exists, _ = fileExists(spectrogramPath)
	if !exists {
		logger.Error("Spectrogram still not found after creation attempt, serving placeholder",
			slog.String("spectrogram_path", spectrogramPath),
			slog.Duration("total_request_duration", time.Since(startTime)),
		)
		h.Debug("ServeSpectrogram: Spectrogram still not found after creation attempt: %s", spectrogramPath)
		return serveSpectrogramPlaceholder(c)
	}

	// Get file size for logging
	var fileSize int64
	if fileInfo, err := os.Stat(spectrogramPath); err == nil {
		fileSize = fileInfo.Size()
	}

	totalDuration := time.Since(startTime)
	logger.Debug("Serving spectrogram file successfully",
		slog.String("spectrogram_path", spectrogramPath),
		slog.Int64("file_size_bytes", fileSize),
		slog.Duration("total_request_duration", totalDuration),
		slog.String("api_handler", "htmx_media_spectrogram"),
		slog.Int("width", spectrogramWidth),
	)

	h.Debug("ServeSpectrogram: Serving spectrogram file: %s", spectrogramPath)
	// Set the correct Content-Type header for PNG images
	c.Response().Header().Set(echo.HeaderContentType, "image/png")
	c.Response().Header().Set("Cache-Control", "public, max-age=2592000, immutable") // Cache spectrograms for 30 days
	// Use efficient file serving to prevent buffer accumulation
	return serveFileEfficiently(c, spectrogramPath)
}

// getSpectrogramPath generates the path to the spectrogram image file for a given audio file
func (h *Handlers) getSpectrogramPath(audioFileName string, width int) (string, error) {
	startTime := time.Now()

	// Create structured logger for this operation
	logger := logging.ForService("htmx_media_handler")
	if logger == nil {
		logger = slog.Default().With("service", "htmx_media_handler")
	}

	logger.Debug("Generating spectrogram path",
		slog.String("input_audio_path", audioFileName),
		slog.Int("width", width),
	)

	// Clean the audio file path first
	audioFileName = filepath.Clean(audioFileName)
	h.Debug("getSpectrogramPath: Input audio path: %s", audioFileName)

	// Get the export path
	exportPath := conf.Setting().Realtime.Audio.Export.Path
	h.Debug("getSpectrogramPath: Export path: %s", exportPath)
	logger.Debug("Retrieved export path from configuration",
		slog.String("export_path", exportPath),
	)

	// Convert both paths to forward slashes for consistent comparison
	audioFileNameSlash := strings.ReplaceAll(audioFileName, "\\", "/")
	exportPathSlash := strings.ReplaceAll(exportPath, "\\", "/")

	// Ensure we're working with the correct base directory
	if !strings.HasPrefix(strings.ToLower(audioFileNameSlash), strings.ToLower(exportPathSlash)) {
		// If the path doesn't already include the export path, add it
		audioFileName = filepath.Clean(filepath.Join(exportPath, audioFileName))
		logger.Debug("Added export path to audio filename",
			slog.String("original_path", audioFileNameSlash),
			slog.String("full_path", audioFileName),
		)
	}
	h.Debug("getSpectrogramPath: Full audio path: %s", audioFileName)

	// Generate file paths using the same directory as the audio file
	dir := filepath.Dir(audioFileName)
	h.Debug("getSpectrogramPath: Directory path: %s", dir)

	baseNameWithoutExt := strings.TrimSuffix(filepath.Base(audioFileName), filepath.Ext(audioFileName))
	h.Debug("getSpectrogramPath: Base name without extension: %s", baseNameWithoutExt)
	logger.Debug("Extracted audio file components",
		slog.String("directory", dir),
		slog.String("base_name", baseNameWithoutExt),
	)

	spectrogramFileName := fmt.Sprintf("%s_%dpx.png", baseNameWithoutExt, width)
	h.Debug("getSpectrogramPath: Spectrogram filename: %s", spectrogramFileName)

	// Join paths using OS-specific separators and clean the result
	spectrogramPath := filepath.Clean(filepath.Join(dir, spectrogramFileName))
	h.Debug("getSpectrogramPath: Final spectrogram path: %s", spectrogramPath)
	logger.Debug("Generated spectrogram filename and path",
		slog.String("spectrogram_filename", spectrogramFileName),
		slog.String("spectrogram_path", spectrogramPath),
	)

	// Check if the spectrogram already exists
	exists, err := fileExists(spectrogramPath)
	if err != nil {
		logger.Debug("Error checking spectrogram file existence",
			slog.String("spectrogram_path", spectrogramPath),
			slog.String("error", err.Error()),
			slog.Duration("path_generation_duration", time.Since(startTime)),
		)
		h.Debug("getSpectrogramPath: Error checking spectrogram existence: %v", err)
		return "", fmt.Errorf("error checking spectrogram file: %w", err)
	}
	if exists {
		logger.Debug("Existing spectrogram found",
			slog.String("spectrogram_path", spectrogramPath),
			slog.Duration("path_generation_duration", time.Since(startTime)),
		)
		h.Debug("getSpectrogramPath: Existing spectrogram found at: %s", spectrogramPath)
		return spectrogramPath, nil
	}
	logger.Debug("No existing spectrogram found, checking audio file",
		slog.String("spectrogram_path", spectrogramPath),
	)
	h.Debug("getSpectrogramPath: No existing spectrogram found at: %s", spectrogramPath)

	// Check if the original audio file exists
	exists, err = fileExists(audioFileName)
	if err != nil {
		logger.Debug("Error checking audio file existence",
			slog.String("audio_path", audioFileName),
			slog.String("error", err.Error()),
			slog.Duration("path_generation_duration", time.Since(startTime)),
		)
		h.Debug("getSpectrogramPath: Error checking audio file: %v", err)
		return "", fmt.Errorf("error checking audio file: %w", err)
	}
	if !exists {
		logger.Debug("Audio file does not exist",
			slog.String("audio_path", audioFileName),
			slog.Duration("path_generation_duration", time.Since(startTime)),
		)
		h.Debug("getSpectrogramPath: Audio file does not exist at: %s", audioFileName)
		return "", fmt.Errorf("audio file does not exist: %s", audioFileName)
	}
	logger.Debug("Audio file verified, spectrogram path ready",
		slog.String("audio_path", audioFileName),
		slog.String("spectrogram_path", spectrogramPath),
		slog.Duration("path_generation_duration", time.Since(startTime)),
	)
	h.Debug("getSpectrogramPath: Audio file exists at: %s", audioFileName)

	return spectrogramPath, nil
}

// fileExists checks if a file exists and is not a directory
func fileExists(filename string) (bool, error) {
	info, err := os.Stat(filename)
	if os.IsNotExist(err) {
		return false, nil
	}
	if err != nil {
		return false, err
	}
	return !info.IsDir(), nil
}

// Note: Spectrogram generation logic has been moved to internal/spectrogram/generator.go
// This eliminates code duplication with API v2 and pre-renderer implementations.
// sanitizeContentDispositionFilename sanitizes a filename for use in Content-Disposition header
func sanitizeContentDispositionFilename(filename string) string {
	// Remove any characters that could cause issues in headers
	// Replace quotes with single quotes, remove control characters, and escape special characters
	sanitized := strings.Map(func(r rune) rune {
		switch {
		case r == '"':
			return '\''
		case r < 32: // Control characters
			return -1
		case r == '\\' || r == '/' || r == ':' || r == '*' || r == '?' || r == '<' || r == '>' || r == '|':
			return '_'
		default:
			return r
		}
	}, filename)

	// URL encode the filename to handle non-ASCII characters
	encoded := url.QueryEscape(sanitized)

	return encoded
}

// ServeAudioClip serves an audio clip file
// API: GET /api/v1/media/audio
func (h *Handlers) ServeAudioClip(c echo.Context) error {
	h.Debug("ServeAudioClip: Starting to handle request for path: %s", c.Request().URL.String())

	// Extract clip name from the query parameters
	clipName := c.QueryParam("clip")
	h.Debug("ServeAudioClip: Raw clip name from query: %s", clipName)

	// Sanitize the clip name
	sanitizedClipName, err := h.sanitizeClipName(clipName)
	if err != nil {
		h.Debug("ServeAudioClip: Error sanitizing clip name: %v", err)
		c.Response().Header().Set(echo.HeaderContentType, "text/plain")
		return c.String(http.StatusNotFound, "Audio file not found")
	}
	h.Debug("ServeAudioClip: Sanitized clip name: %s", sanitizedClipName)

	// Get the full path to the audio file
	fullPath := getFullPath(sanitizedClipName)
	h.Debug("ServeAudioClip: Full path: %s", fullPath)

	// Verify that the full path is within the export directory
	absFullPath, err := filepath.Abs(fullPath)
	if err != nil {
		h.Debug("ServeAudioClip: Error obtaining absolute path: %v", err)
		return c.String(http.StatusInternalServerError, "Internal server error")
	}
	absExportPath, err := filepath.Abs(conf.Setting().Realtime.Audio.Export.Path)
	if err != nil {
		h.Debug("ServeAudioClip: Error obtaining absolute export path: %v", err)
		return c.String(http.StatusInternalServerError, "Internal server error")
	}
	if !strings.HasPrefix(absFullPath, absExportPath) {
		h.Debug("ServeAudioClip: Resolved path outside export directory: %s", absFullPath)
		return c.String(http.StatusForbidden, "Forbidden")
	}

	// Check if the file exists
	if _, err := os.Stat(fullPath); err != nil {
		if os.IsNotExist(err) {
			h.Debug("ServeAudioClip: Audio file not found: %s", fullPath)
		} else {
			h.Debug("ServeAudioClip: Error checking audio file: %v", err)
		}
		c.Response().Header().Set(echo.HeaderContentType, "text/plain")
		return c.String(http.StatusNotFound, "Audio file not found")
	}
	h.Debug("ServeAudioClip: File exists at path: %s", fullPath)

	// Get the filename for Content-Disposition
	filename := filepath.Base(sanitizedClipName)
	safeFilename := sanitizeContentDispositionFilename(filename)
	h.Debug("ServeAudioClip: Using filename for disposition: %s (safe: %s)", filename, safeFilename)

	// Get MIME type
	mimeType := getAudioMimeType(fullPath)
	h.Debug("ServeAudioClip: MIME type for file: %s", mimeType)

	// Set response headers
	c.Response().Header().Set(echo.HeaderContentType, mimeType)
	c.Response().Header().Set("Content-Transfer-Encoding", "binary")
	c.Response().Header().Set("Content-Description", "File Transfer")
	// Set both ASCII and UTF-8 versions of the filename for better browser compatibility
	c.Response().Header().Set(echo.HeaderContentDisposition,
		fmt.Sprintf(`attachment; filename="%s"; filename*=UTF-8''%s`, //nolint:gocritic // %s is correct here, %q will mangle filename
			safeFilename,
			safeFilename))

	h.Debug("ServeAudioClip: Set headers - Content-Type: %s, Content-Disposition: %s",
		c.Response().Header().Get(echo.HeaderContentType),
		c.Response().Header().Get(echo.HeaderContentDisposition))

	// Serve the file using efficient buffer management
	h.Debug("ServeAudioClip: Attempting to serve file efficiently: %s", fullPath)
	return serveFileEfficiently(c, fullPath)
}

// getAudioMimeType returns the MIME type for an audio file based on its extension
func getAudioMimeType(filename string) string {
	ext := strings.ToLower(filepath.Ext(filename))
	switch ext {
	case ".mp3":
		return "audio/mpeg"
	case ".ogg", ".opus":
		return "audio/ogg"
	case ".wav":
		return "audio/wav"
	case ".flac":
		return "audio/flac"
	case ".aac":
		return "audio/aac"
	case ".m4a":
		return "audio/mp4"
	case ".alac":
		return "audio/x-alac"
	default:
		return "application/octet-stream"
	}
}

// serveFileEfficiently serves a file using http.ServeContent for efficient buffer management
// This prevents memory leaks by properly handling buffers and supporting range requests
func serveFileEfficiently(c echo.Context, filePath string) error {
	// Open the file
	file, err := os.Open(filePath)
	if err != nil {
		if os.IsNotExist(err) {
			return echo.NewHTTPError(http.StatusNotFound, "File not found")
		}
		return echo.NewHTTPError(http.StatusInternalServerError, "Failed to open file")
	}
	defer func() {
		if err := file.Close(); err != nil {
			// Log error but don't fail the request
			log.Printf("Error closing file %s: %v", filePath, err)
		}
	}()

	// Get file info
	stat, err := file.Stat()
	if err != nil {
		return echo.NewHTTPError(http.StatusInternalServerError, "Failed to get file info")
	}

	// Only serve regular files
	if !stat.Mode().IsRegular() {
		return echo.NewHTTPError(http.StatusForbidden, "Not a regular file")
	}

	// Use http.ServeContent for efficient serving
	// This handles Range requests, caching, and proper buffer management
	http.ServeContent(c.Response(), c.Request(), filepath.Base(filePath), stat.ModTime(), file)
	return nil
}

// serveSpectrogramPlaceholder serves the embedded spectrogram placeholder SVG
func serveSpectrogramPlaceholder(c echo.Context) error {
	// Set appropriate headers for SVG
	c.Response().Header().Set(echo.HeaderContentType, "image/svg+xml")
	c.Response().Header().Set("Cache-Control", "public, max-age=86400") // Cache for 24 hours

	// Create a reader from the embedded bytes
	reader := bytes.NewReader(spectrogramPlaceholderSVG)

	// Use http.ServeContent for proper HTTP semantics
	http.ServeContent(
		c.Response(),
		c.Request(),
		"spectrogram-placeholder.svg",
		time.Time{}, // No modification time for embedded content
		reader,
	)
	return nil
}
