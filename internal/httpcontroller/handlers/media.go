package handlers

import (
	"bytes"
	"errors"
	"fmt"
	"html"
	"html/template"
	"log"
	"log/slog"
	"net/http"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"runtime"
	"strconv"
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
		logger.Debug("Clip name sanitization failed, serving placeholder",
			slog.String("raw_clip_name", clipName),
			slog.String("error", err.Error()),
			slog.Duration("request_duration", time.Since(startTime)),
		)
		h.Debug("ServeSpectrogram: Error sanitizing clip name: %v", err)
		c.Response().Header().Set(echo.HeaderContentType, "image/svg+xml")
		return serveFileEfficiently(c, "assets/images/spectrogram-placeholder.svg")
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
		logger.Debug("Audio file existence check failed, serving placeholder",
			slog.String("full_path", fullPath),
			slog.String("error", err.Error()),
			slog.Duration("request_duration", time.Since(startTime)),
		)
		h.Debug("ServeSpectrogram: Error checking audio file: %v", err)
		c.Response().Header().Set(echo.HeaderContentType, "image/svg+xml")
		return serveFileEfficiently(c, "assets/images/spectrogram-placeholder.svg")
	}
	if !exists {
		logger.Debug("Audio file not found, serving placeholder",
			slog.String("full_path", fullPath),
			slog.Duration("request_duration", time.Since(startTime)),
		)
		h.Debug("ServeSpectrogram: Audio file not found: %s", fullPath)
		c.Response().Header().Set(echo.HeaderContentType, "image/svg+xml")
		return serveFileEfficiently(c, "assets/images/spectrogram-placeholder.svg")
	}
	logger.Debug("Audio file verified successfully",
		slog.String("full_path", fullPath),
	)
	h.Debug("ServeSpectrogram: Audio file exists at: %s", fullPath)

	// Construct the path to the spectrogram image
	spectrogramWidth := 400 // Default width for HTMX API
	spectrogramPath, err := h.getSpectrogramPath(fullPath, spectrogramWidth)
	if err != nil {
		logger.Debug("Spectrogram path generation failed, serving placeholder",
			slog.String("full_path", fullPath),
			slog.Int("width", spectrogramWidth),
			slog.String("error", err.Error()),
			slog.Duration("request_duration", time.Since(startTime)),
		)
		h.Debug("ServeSpectrogram: Error getting spectrogram path: %v", err)
		c.Response().Header().Set(echo.HeaderContentType, "image/svg+xml")
		return serveFileEfficiently(c, "assets/images/spectrogram-placeholder.svg")
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
		logger.Debug("Spectrogram existence check failed, serving placeholder",
			slog.String("spectrogram_path", spectrogramPath),
			slog.String("error", err.Error()),
			slog.Duration("request_duration", time.Since(startTime)),
		)
		h.Debug("ServeSpectrogram: Error checking spectrogram file: %v", err)
		c.Response().Header().Set(echo.HeaderContentType, "image/svg+xml")
		return serveFileEfficiently(c, "assets/images/spectrogram-placeholder.svg")
	}
	if !exists {
		logger.Debug("Spectrogram not found, initiating generation",
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

		// Try to create the spectrogram
		generationStartTime := time.Now()
		if err := createSpectrogramWithSoX(fullPath, spectrogramPath, spectrogramWidth); err != nil {
			generationDuration := time.Since(generationStartTime)
			logger.Debug("Spectrogram generation failed, serving placeholder",
				slog.String("audio_path", fullPath),
				slog.String("spectrogram_path", spectrogramPath),
				slog.Int("width", spectrogramWidth),
				slog.String("error", err.Error()),
				slog.Duration("generation_duration", generationDuration),
				slog.Duration("total_request_duration", time.Since(startTime)),
			)
			h.Debug("ServeSpectrogram: Failed to create spectrogram: %v", err)
			c.Response().Header().Set(echo.HeaderContentType, "image/svg+xml")
			return serveFileEfficiently(c, "assets/images/spectrogram-placeholder.svg")
		}
		generationDuration := time.Since(generationStartTime)
		logger.Debug("Spectrogram generated successfully",
			slog.String("audio_path", fullPath),
			slog.String("spectrogram_path", spectrogramPath),
			slog.Int("width", spectrogramWidth),
			slog.Duration("generation_duration", generationDuration),
			slog.Duration("semaphore_wait_duration", semaphoreWaitDuration),
		)
		h.Debug("ServeSpectrogram: Successfully created spectrogram at: %s", spectrogramPath)
	} else {
		logger.Debug("Existing spectrogram found, serving cached version",
			slog.String("spectrogram_path", spectrogramPath),
		)
	}

	// Final check if the spectrogram exists after potential creation
	exists, _ = fileExists(spectrogramPath)
	if !exists {
		logger.Debug("Spectrogram still not found after creation attempt, serving placeholder",
			slog.String("spectrogram_path", spectrogramPath),
			slog.Duration("total_request_duration", time.Since(startTime)),
		)
		h.Debug("ServeSpectrogram: Spectrogram still not found after creation attempt: %s", spectrogramPath)
		c.Response().Header().Set(echo.HeaderContentType, "image/svg+xml")
		return serveFileEfficiently(c, "assets/images/spectrogram-placeholder.svg")
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

// createSpectrogramWithSoX generates a spectrogram for an audio file using ffmpeg and SoX.
// It supports various audio formats by using ffmpeg to pipe the audio to SoX when necessary.
func createSpectrogramWithSoX(audioClipPath, spectrogramPath string, width int) error {
	startTime := time.Now()
	
	// Create structured logger for this operation
	logger := logging.ForService("htmx_media_handler")
	if logger == nil {
		logger = slog.Default().With("service", "htmx_media_handler")
	}
	
	logger.Debug("Starting spectrogram generation with SoX",
		slog.String("audio_path", audioClipPath),
		slog.String("output_path", spectrogramPath),
		slog.Int("width", width),
	)
	
	// Get ffmpeg and sox paths from settings
	ffmpegBinary := conf.Setting().Realtime.Audio.FfmpegPath
	soxBinary := conf.Setting().Realtime.Audio.SoxPath
	
	logger.Debug("Retrieved binary paths from configuration",
		slog.String("ffmpeg_path", ffmpegBinary),
		slog.String("sox_path", soxBinary),
	)

	// Verify ffmpeg and SoX paths
	if ffmpegBinary == "" {
		logger.Debug("FFmpeg path not configured",
			slog.Duration("setup_duration", time.Since(startTime)),
		)
		return fmt.Errorf("ffmpeg path not set in settings")
	}
	if soxBinary == "" {
		logger.Debug("SoX path not configured",
			slog.Duration("setup_duration", time.Since(startTime)),
		)
		return fmt.Errorf("SoX path not set in settings")
	}
	
	logger.Debug("Binary paths verified successfully")

	// Set height based on width
	heightStr := strconv.Itoa(width / 2)
	widthStr := strconv.Itoa(width)
	height := width / 2
	
	logger.Debug("Calculated spectrogram dimensions",
		slog.Int("width", width),
		slog.Int("height", height),
	)

	// Determine if we need to use ffmpeg based on file extension
	ext := strings.ToLower(filepath.Ext(audioClipPath))
	// remove prefix dot
	ext = strings.TrimPrefix(ext, ".")
	useFFmpeg := true
	supportedSoxTypes := conf.Setting().Realtime.Audio.SoxAudioTypes
	for _, soxType := range supportedSoxTypes {
		if strings.EqualFold(ext, soxType) {
			useFFmpeg = false
			break
		}
	}
	
	logger.Debug("Determined audio format processing method",
		slog.String("file_extension", ext),
		slog.Bool("use_ffmpeg", useFFmpeg),
		slog.Any("supported_sox_types", supportedSoxTypes),
	)

	var cmd *exec.Cmd
	var soxCmd *exec.Cmd
	
	commandSetupStart := time.Now()

	// Decode audio using ffmpeg and pipe to sox for spectrogram creation
	if useFFmpeg {
		logger.Debug("Preparing FFmpeg + SoX pipeline for audio processing")
		
		// Build ffmpeg command arguments
		ffmpegArgs := []string{"-hide_banner", "-i", audioClipPath, "-f", "sox", "-"}

		// Build SoX command arguments
		soxArgs := append([]string{"-t", "sox", "-"}, getSoxSpectrogramArgs(widthStr, heightStr, spectrogramPath)...)

		logger.Debug("Built command arguments for FFmpeg + SoX pipeline",
			slog.Any("ffmpeg_args", ffmpegArgs),
			slog.Any("sox_args", soxArgs),
		)

		// Set up commands
		if runtime.GOOS == "windows" {
			cmd = exec.Command(ffmpegBinary, ffmpegArgs...) // #nosec G204 -- ffmpegBinary validated via ValidateToolPath
			soxCmd = exec.Command(soxBinary, soxArgs...)    // #nosec G204 -- soxBinary validated via ValidateToolPath
		} else {
			cmd = exec.Command("nice", append([]string{"-n", "19", ffmpegBinary}, ffmpegArgs...)...) // #nosec G204 -- ffmpegBinary validated via ValidateToolPath
			soxCmd = exec.Command("nice", append([]string{"-n", "19", soxBinary}, soxArgs...)...)    // #nosec G204 -- soxBinary validated via ValidateToolPath
		}
		
		logger.Debug("Commands created for FFmpeg + SoX pipeline",
			slog.String("ffmpeg_command", cmd.String()),
			slog.String("sox_command", soxCmd.String()),
			slog.String("os", runtime.GOOS),
		)

		// Set up pipe between ffmpeg and sox
		var err error
		soxCmd.Stdin, err = cmd.StdoutPipe()
		if err != nil {
			logger.Debug("Failed to create pipe between FFmpeg and SoX",
				slog.String("error", err.Error()),
				slog.Duration("setup_duration", time.Since(startTime)),
			)
			return fmt.Errorf("error creating pipe: %w", err)
		}

		// Capture combined output
		var ffmpegOutput, soxOutput bytes.Buffer
		cmd.Stderr = &ffmpegOutput
		soxCmd.Stderr = &soxOutput
		
		commandSetupDuration := time.Since(commandSetupStart)
		logger.Debug("Pipeline setup completed",
			slog.Duration("command_setup_duration", commandSetupDuration),
		)

		// Allow other goroutines to run before starting SoX
		runtime.Gosched()

		// Start sox command
		soxStartTime := time.Now()
		if err := soxCmd.Start(); err != nil {
			logger.Debug("Failed to start SoX command",
				slog.String("sox_command", soxCmd.String()),
				slog.String("error", err.Error()),
				slog.Duration("total_duration", time.Since(startTime)),
			)
			log.Printf("SoX cmd: %s", soxCmd.String())
			return fmt.Errorf("error starting SoX command: %w", err)
		}
		
		logger.Debug("SoX command started successfully",
			slog.Duration("sox_start_duration", time.Since(soxStartTime)),
		)

		// Define error message template
		const errFFmpegSoxFailed = "ffmpeg command failed: %v\nffmpeg output: %s\nsox output: %s\n%s"

		// Run ffmpeg command
		ffmpegStartTime := time.Now()
		logger.Debug("Starting FFmpeg execution")
		if err := cmd.Run(); err != nil {
			ffmpegDuration := time.Since(ffmpegStartTime)
			
			// Stop the SoX command to clean up resources
			if killErr := soxCmd.Process.Kill(); killErr != nil {
				log.Printf("Failed to kill SoX process: %v", killErr)
			}

			// Wait for SoX to finish and collect its error, if any
			waitErr := soxCmd.Wait()

			// Prepare additional error information
			var additionalInfo string
			if waitErr != nil && !errors.Is(waitErr, os.ErrProcessDone) {
				additionalInfo = fmt.Sprintf("sox wait error: %v", waitErr)
			}

			logger.Debug("FFmpeg command failed",
				slog.String("ffmpeg_command", cmd.String()),
				slog.String("error", err.Error()),
				slog.Duration("ffmpeg_duration", ffmpegDuration),
				slog.Duration("total_duration", time.Since(startTime)),
				slog.String("ffmpeg_output", ffmpegOutput.String()),
				slog.String("sox_output", soxOutput.String()),
			)

			// Use fmt.Errorf with the constant format string
			return fmt.Errorf(errFFmpegSoxFailed, err, ffmpegOutput.String(), soxOutput.String(), additionalInfo)
		}
		
		ffmpegDuration := time.Since(ffmpegStartTime)
		logger.Debug("FFmpeg execution completed successfully",
			slog.Duration("ffmpeg_duration", ffmpegDuration),
		)

		// Allow other goroutines to run before waiting for SoX to finish
		runtime.Gosched()

		// Wait for sox command to finish
		soxWaitStartTime := time.Now()
		logger.Debug("Waiting for SoX command to complete")
		if err := soxCmd.Wait(); err != nil {
			soxWaitDuration := time.Since(soxWaitStartTime)
			logger.Debug("SoX command failed",
				slog.String("sox_command", soxCmd.String()),
				slog.String("error", err.Error()),
				slog.Duration("sox_wait_duration", soxWaitDuration),
				slog.Duration("ffmpeg_duration", ffmpegDuration),
				slog.Duration("total_duration", time.Since(startTime)),
				slog.String("ffmpeg_output", ffmpegOutput.String()),
				slog.String("sox_output", soxOutput.String()),
			)
			return fmt.Errorf("SoX command failed: %w\nffmpeg output: %s\nsox output: %s", err, ffmpegOutput.String(), soxOutput.String())
		}
		
		soxWaitDuration := time.Since(soxWaitStartTime)
		totalDuration := time.Since(startTime)
		
		logger.Debug("FFmpeg + SoX pipeline completed successfully",
			slog.Duration("ffmpeg_duration", ffmpegDuration),
			slog.Duration("sox_wait_duration", soxWaitDuration),
			slog.Duration("total_duration", totalDuration),
			slog.String("output_file", spectrogramPath),
		)

		// Allow other goroutines to run after SoX finishes
		runtime.Gosched()
	} else {
		// Use SoX directly for supported formats
		logger.Debug("Using SoX directly for supported audio format")
		
		soxArgs := append([]string{audioClipPath}, getSoxSpectrogramArgs(widthStr, heightStr, spectrogramPath)...)
		
		logger.Debug("Built SoX-only command arguments",
			slog.Any("sox_args", soxArgs),
		)

		if runtime.GOOS == "windows" {
			soxCmd = exec.Command(soxBinary, soxArgs...) // #nosec G204 -- soxBinary validated via ValidateToolPath
		} else {
			soxCmd = exec.Command("nice", append([]string{"-n", "19", soxBinary}, soxArgs...)...) // #nosec G204 -- soxBinary validated via ValidateToolPath
		}
		
		commandSetupDuration := time.Since(commandSetupStart)
		logger.Debug("SoX-only command created",
			slog.String("sox_command", soxCmd.String()),
			slog.String("os", runtime.GOOS),
			slog.Duration("command_setup_duration", commandSetupDuration),
		)

		// Capture output
		var soxOutput bytes.Buffer
		soxCmd.Stderr = &soxOutput
		soxCmd.Stdout = &soxOutput

		// Allow other goroutines to run before running SoX
		runtime.Gosched()

		// Run SoX command
		soxExecutionStartTime := time.Now()
		logger.Debug("Starting SoX-only execution")
		if err := soxCmd.Run(); err != nil {
			soxExecutionDuration := time.Since(soxExecutionStartTime)
			totalDuration := time.Since(startTime)
			
			logger.Debug("SoX-only command failed",
				slog.String("sox_command", soxCmd.String()),
				slog.String("error", err.Error()),
				slog.Duration("sox_execution_duration", soxExecutionDuration),
				slog.Duration("total_duration", totalDuration),
				slog.String("sox_output", soxOutput.String()),
			)
			return fmt.Errorf("SoX command failed: %w\nOutput: %s", err, soxOutput.String())
		}
		
		soxExecutionDuration := time.Since(soxExecutionStartTime)
		totalDuration := time.Since(startTime)
		
		logger.Debug("SoX-only execution completed successfully",
			slog.Duration("sox_execution_duration", soxExecutionDuration),
			slog.Duration("total_duration", totalDuration),
			slog.String("output_file", spectrogramPath),
		)

		// Allow other goroutines to run after SoX finishes
		runtime.Gosched()
	}

	// Add final completion log with file size if possible
	var fileSize int64
	if fileInfo, err := os.Stat(spectrogramPath); err == nil {
		fileSize = fileInfo.Size()
	}
	
	totalDuration := time.Since(startTime)
	logger.Debug("Spectrogram generation completed successfully",
		slog.String("input_path", audioClipPath),
		slog.String("output_path", spectrogramPath),
		slog.Int("width", width),
		slog.Int("height", height),
		slog.Int64("output_file_size_bytes", fileSize),
		slog.Duration("total_generation_duration", totalDuration),
		slog.Bool("used_ffmpeg", useFFmpeg),
		slog.String("api_handler", "htmx_spectrogram_generation"),
	)

	return nil
}

// getSoxSpectrogramArgs returns the common SoX arguments for generating a spectrogram
func getSoxSpectrogramArgs(widthStr, heightStr, spectrogramPath string) []string {
	// TODO: make these dynamic based on audio length and gain
	const audioLength = "15"
	const dynamicRange = "100"

	args := []string{"-n", "rate", "24k", "spectrogram", "-x", widthStr, "-y", heightStr, "-d", audioLength, "-z", dynamicRange, "-o", spectrogramPath}
	width, _ := strconv.Atoi(widthStr)
	if width < 800 {
		args = append(args, "-r")
	}
	return args
}

// createSpectrogramWithFFmpeg generates a spectrogram for an audio file using only ffmpeg.
// It supports various audio formats and applies the same practices as createSpectrogramWithSoX.
func createSpectrogramWithFFmpeg(audioClipPath, spectrogramPath string, width int) error {
	// Get ffmpeg path from settings
	ffmpegBinary := conf.Setting().Realtime.Audio.FfmpegPath

	// Verify ffmpeg path
	if ffmpegBinary == "" {
		return fmt.Errorf("ffmpeg path not set in settings")
	}

	// Set height based on width
	height := width / 2
	heightStr := strconv.Itoa(height)
	widthStr := strconv.Itoa(width)

	// Build ffmpeg command arguments
	ffmpegArgs := []string{
		"-hide_banner",
		"-y", // answer yes to overwriting the output file if it already exists
		"-i", audioClipPath,
		"-lavfi", fmt.Sprintf("showspectrumpic=s=%sx%s:legend=0:gain=3:drange=100", widthStr, heightStr),
		"-frames:v", "1", // Generate only one frame instead of animation
		spectrogramPath,
	}

	// Determine the command based on the OS
	var cmd *exec.Cmd
	if runtime.GOOS == "windows" {
		// Directly use ffmpeg command on Windows
		cmd = exec.Command(ffmpegBinary, ffmpegArgs...)
	} else {
		// Prepend 'nice' to the command on Unix-like systems
		cmd = exec.Command("nice", append([]string{"-n", "19", ffmpegBinary}, ffmpegArgs...)...) // #nosec G204 -- ffmpegBinary validated via ValidateToolPath
	}

	log.Printf("ffmpeg command: %s", cmd.String())

	// Capture combined output
	var output bytes.Buffer
	cmd.Stderr = &output
	cmd.Stdout = &output

	// Run ffmpeg command
	if err := cmd.Run(); err != nil {
		return fmt.Errorf("ffmpeg command failed: %w\nOutput: %s", err, output.String())
	}

	return nil
}

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
