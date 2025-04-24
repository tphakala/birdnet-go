// internal/api/v2/media.go
package api

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"log"
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
	"golang.org/x/sync/singleflight"
)

// safeFilenamePattern is kept if needed elsewhere, but SecureFS handles validation now
// var safeFilenamePattern = regexp.MustCompile(`^[\p{L}\p{N}_\-.]+$`)

// Initialize media routes
func (c *Controller) initMediaRoutes() {
	// Original filename-based routes (keep for backward compatibility if needed, but ensure they use SFS)
	c.Group.GET("/media/audio/:filename", c.ServeAudioClip)
	c.Group.GET("/media/spectrogram/:filename", c.ServeSpectrogram)

	// ID-based routes using SFS
	c.Echo.GET("/api/v2/audio/:id", c.ServeAudioByID)
	c.Echo.GET("/api/v2/spectrogram/:id", c.ServeSpectrogramByID)

	// Convenient combined endpoint (redirects to ID-based internally)
	c.Group.GET("/media/audio", c.ServeAudioByQueryID)
}

// getContentType determines the content type based on file extension (can remain as helper)
func getContentType(filename string) string {
	ext := strings.ToLower(filepath.Ext(filename))
	switch ext {
	case ".mp3":
		return "audio/mpeg"
	case ".wav":
		return "audio/wav"
	case ".ogg":
		return "audio/ogg"
	case ".flac":
		return "audio/flac"
	default:
		// Default to octet-stream if unknown, letting ServeFile handle it
		return "application/octet-stream"
	}
}

// ServeAudioClip serves an audio clip file by filename using SecureFS
func (c *Controller) ServeAudioClip(ctx echo.Context) error {
	filename := ctx.Param("filename")
	// Construct the conceptual path within the SecureFS base directory
	// Note: We don't need the exportPath directly here, SecureFS handles it.
	// We pass the filename which SecureFS expects to find relative to its baseDir.
	return c.SFS.ServeFile(ctx, filename)
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

	// Serve the file using SecureFS. It handles path validation (relative/absolute within baseDir).
	// ServeFile internally calls relativePath which ensures the path is within the SecureFS baseDir.
	// Use ServeRelativeFile as clipPath is already relative to the baseDir
	return c.SFS.ServeRelativeFile(ctx, clipPath)
}

// ServeSpectrogramByID serves a spectrogram image based on note ID using SecureFS
func (c *Controller) ServeSpectrogramByID(ctx echo.Context) error {
	noteID := ctx.Param("id")
	if noteID == "" {
		return c.HandleError(ctx, fmt.Errorf("missing ID"), "Note ID is required", http.StatusBadRequest)
	}

	clipPath, err := c.DS.GetNoteClipPath(noteID)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) || strings.Contains(err.Error(), "not found") {
			return c.HandleError(ctx, err, "Clip path not found for note ID", http.StatusNotFound)
		}
		return c.HandleError(ctx, err, "Failed to get clip path for note", http.StatusInternalServerError)
	}

	if clipPath == "" {
		return c.HandleError(ctx, fmt.Errorf("no audio file found"), "No audio clip available for this note", http.StatusNotFound)
	}

	width := 800 // Default width
	widthStr := ctx.QueryParam("width")
	if widthStr != "" {
		parsedWidth, err := strconv.Atoi(widthStr)
		if err == nil && parsedWidth > 0 && parsedWidth <= 2000 {
			width = parsedWidth
		}
	}

	// Use the SecureFS GenerateSpectrogram method (to be implemented in securefs.go)
	// Update: Call the controller's generateSpectrogram method instead
	// Pass the request context for cancellation/timeout
	spectrogramPath, err := c.generateSpectrogram(ctx.Request().Context(), clipPath, width)
	if err != nil {
		// Handle specific errors if GenerateSpectrogram provides them (e.g., source audio not found)
		if os.IsNotExist(err) {
			return c.HandleError(ctx, err, "Source audio file not found", http.StatusNotFound)
		}
		// Check for context errors specifically
		if errors.Is(err, context.DeadlineExceeded) {
			return c.HandleError(ctx, err, "Spectrogram generation timed out", http.StatusRequestTimeout)
		}
		if errors.Is(err, context.Canceled) {
			return c.HandleError(ctx, err, "Spectrogram generation canceled by client", http.StatusRequestTimeout) // Or another appropriate code
		}
		return c.HandleError(ctx, err, "Failed to generate spectrogram", http.StatusInternalServerError)
	}

	// Serve the generated spectrogram using SecureFS
	// Pass the path returned by GenerateSpectrogram (which should be relative to SFS base)
	// Use ServeRelativeFile as spectrogramPath is already relative to the baseDir
	return c.SFS.ServeRelativeFile(ctx, spectrogramPath)
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
func (c *Controller) ServeSpectrogram(ctx echo.Context) error {
	filename := ctx.Param("filename")

	width := 800 // Default width
	widthStr := ctx.QueryParam("width")
	if widthStr != "" {
		parsedWidth, err := strconv.Atoi(widthStr)
		if err == nil && parsedWidth > 0 && parsedWidth <= 2000 {
			width = parsedWidth
		}
	}

	// Use SecureFS GenerateSpectrogram, passing the filename relative to the baseDir
	// Update: Call the controller's generateSpectrogram method instead
	// Pass the request context for cancellation/timeout
	spectrogramPath, err := c.generateSpectrogram(ctx.Request().Context(), filename, width)
	if err != nil {
		if os.IsNotExist(err) { // Check if the original audio file was not found
			return c.HandleError(ctx, err, "Source audio file not found", http.StatusNotFound)
		}
		// Check for context errors specifically
		if errors.Is(err, context.DeadlineExceeded) {
			return c.HandleError(ctx, err, "Spectrogram generation timed out", http.StatusRequestTimeout)
		}
		if errors.Is(err, context.Canceled) {
			return c.HandleError(ctx, err, "Spectrogram generation canceled by client", http.StatusRequestTimeout) // Or another appropriate code
		}
		return c.HandleError(ctx, err, "Failed to generate spectrogram", http.StatusInternalServerError)
	}

	// Serve the generated spectrogram using SecureFS
	// Use ServeRelativeFile as spectrogramPath is already relative to the baseDir
	return c.SFS.ServeRelativeFile(ctx, spectrogramPath)
}

// httpRange specifies the byte range to be sent to the client
type httpRange struct {
	start, length int64
}

// parseRange parses a Range header string as per RFC 7233
func parseRange(rangeHeader string, size int64) ([]httpRange, error) {
	if !strings.HasPrefix(rangeHeader, "bytes=") {
		return nil, fmt.Errorf("invalid range header format")
	}
	rangeHeader = strings.TrimPrefix(rangeHeader, "bytes=")

	var ranges []httpRange
	for _, r := range strings.Split(rangeHeader, ",") {
		r = strings.TrimSpace(r)
		if r == "" {
			continue
		}

		parts := strings.Split(r, "-")
		if len(parts) != 2 {
			return nil, fmt.Errorf("invalid range format")
		}

		var start, end int64
		var err error

		if parts[0] == "" {
			// suffix range: -N
			end, err = strconv.ParseInt(parts[1], 10, 64)
			if err != nil {
				return nil, fmt.Errorf("invalid range format")
			}
			if end > size {
				end = size
			}
			start = size - end
			end = size - 1
		} else {
			// normal range: N-M or N-
			start, err = strconv.ParseInt(parts[0], 10, 64)
			if err != nil {
				return nil, fmt.Errorf("invalid range format")
			}

			if parts[1] == "" {
				// range: N-
				end = size - 1
			} else {
				// range: N-M
				end, err = strconv.ParseInt(parts[1], 10, 64)
				if err != nil {
					return nil, fmt.Errorf("invalid range format")
				}
			}
		}

		if start > end || start < 0 || end >= size {
			// Invalid range
			continue
		}

		ranges = append(ranges, httpRange{start: start, length: end - start + 1})
	}

	if len(ranges) == 0 {
		return nil, fmt.Errorf("no valid ranges found")
	}

	return ranges, nil
}

// Limit concurrent spectrogram generations to avoid overloading the system
const maxConcurrentSpectrograms = 4

var (
	spectrogramSemaphore = make(chan struct{}, maxConcurrentSpectrograms)
	spectrogramGroup     singleflight.Group // Prevents duplicate generations
)

// generateSpectrogram creates a spectrogram image for the given audio file path (relative to SecureFS root).
// It accepts a context for cancellation and timeout.
// It returns the relative path to the generated spectrogram, suitable for use with c.SFS.ServeFile.
func (c *Controller) generateSpectrogram(ctx context.Context, audioPath string, width int) (string, error) {
	// The audioPath from the DB is already relative to the baseDir. Validate it.
	relAudioPath, err := c.SFS.ValidateRelativePath(audioPath) // Use the new validator
	if err != nil {
		// Wrap the error for clarity
		return "", fmt.Errorf("invalid audio path '%s': %w", audioPath, err)
	}

	// Check if the audio file exists within the secure context using the validated relative path
	// Use StatRel as relAudioPath is already validated and relative to baseDir
	if _, err := c.SFS.StatRel(relAudioPath); err != nil {
		// Handle file not found specifically, otherwise wrap
		if os.IsNotExist(err) {
			return "", fmt.Errorf("audio file not found at '%s': %w", relAudioPath, err)
		}
		return "", fmt.Errorf("error checking audio file '%s': %w", relAudioPath, err)
	}

	// --- Calculate paths ---
	// Absolute path on the host filesystem required for external commands (sox, ffmpeg)
	// Construct using BaseDir and the validated relative path
	absAudioPath := filepath.Join(c.SFS.BaseDir(), relAudioPath)

	// Get the base filename and directory relative to the secure root
	relBaseFilename := strings.TrimSuffix(filepath.Base(relAudioPath), filepath.Ext(relAudioPath))
	relAudioDir := filepath.Dir(relAudioPath)

	// Generate spectrogram filename with width (relative path)
	spectrogramFilename := fmt.Sprintf("%s_%d.png", relBaseFilename, width)
	// Join relative directory and filename
	relSpectrogramPath := filepath.Join(relAudioDir, spectrogramFilename)
	// Ensure the resulting path is clean and still relative
	relSpectrogramPath = filepath.Clean(relSpectrogramPath)
	relSpectrogramPath = strings.TrimPrefix(relSpectrogramPath, string(filepath.Separator))

	// Absolute path for the spectrogram on the host filesystem
	absSpectrogramPath := filepath.Join(c.SFS.BaseDir(), relSpectrogramPath)

	// Generate a unique key for this spectrogram generation request
	// Include both the path and width to ensure uniqueness
	spectrogramKey := fmt.Sprintf("%s:%d", relSpectrogramPath, width)

	// Use singleflight to prevent duplicate generations
	_, err, _ = spectrogramGroup.Do(spectrogramKey, func() (interface{}, error) {
		// Fast path inside the group – now race-free
		if _, err := c.SFS.StatRel(relSpectrogramPath); err == nil {
			return nil, nil // File exists, no need to generate
		} else if !os.IsNotExist(err) {
			// An unexpected error occurred checking for the spectrogram
			return nil, fmt.Errorf("error checking for existing spectrogram '%s': %w", relSpectrogramPath, err)
		}

		// --- Generate Spectrogram ---
		if err := createSpectrogramWithSoX(ctx, absAudioPath, absSpectrogramPath, width, c.Settings); err != nil {
			log.Printf("SoX failed for '%s', falling back to FFmpeg: %v", absAudioPath, err)
			// Pass the context down to the fallback function as well.
			if err2 := createSpectrogramWithFFmpeg(ctx, absAudioPath, absSpectrogramPath, width, c.Settings); err2 != nil {
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
				return nil, fmt.Errorf("failed to generate spectrogram with SoX for '%s': %w, and with FFmpeg: %w", absAudioPath, err, err2)
			}
			log.Printf("Successfully generated spectrogram for '%s' using FFmpeg fallback", absAudioPath)
		} else {
			log.Printf("Successfully generated spectrogram for '%s' using SoX", absAudioPath)
		}
		return nil, nil
	})

	if err != nil {
		return "", fmt.Errorf("failed to generate spectrogram: %w", err)
	}

	// Return the relative path of the newly created spectrogram
	return relSpectrogramPath, nil
}

// --- Spectrogram Generation Helpers ---

// createSpectrogramWithSoX generates a spectrogram using ffmpeg and SoX.
// Accepts a context for timeout and cancellation.
// Requires absolute paths for external commands.
func createSpectrogramWithSoX(ctx context.Context, absAudioClipPath, absSpectrogramPath string, width int, settings *conf.Settings) error {
	ffmpegBinary := settings.Realtime.Audio.FfmpegPath
	soxBinary := settings.Realtime.Audio.SoxPath
	if ffmpegBinary == "" {
		return fmt.Errorf("ffmpeg path not set in settings")
	}
	if soxBinary == "" {
		return fmt.Errorf("SoX path not set in settings")
	}

	// Create context with timeout (use the passed-in context as parent)
	ctx, cancel := context.WithTimeout(ctx, 60*time.Second)
	defer cancel()

	spectrogramSemaphore <- struct{}{}
	defer func() { <-spectrogramSemaphore }()

	heightStr := strconv.Itoa(width / 2)
	widthStr := strconv.Itoa(width)

	ext := strings.ToLower(filepath.Ext(absAudioClipPath))
	ext = strings.TrimPrefix(ext, ".")
	useFFmpeg := true
	for _, soxType := range settings.Realtime.Audio.SoxAudioTypes {
		if strings.EqualFold(ext, soxType) {
			useFFmpeg = false
			break
		}
	}

	var cmd *exec.Cmd
	var soxCmd *exec.Cmd

	if useFFmpeg {
		ffmpegArgs := []string{"-hide_banner", "-i", absAudioClipPath, "-f", "sox", "-"}
		soxArgs := append([]string{"-t", "sox", "-"}, getSoxSpectrogramArgs(widthStr, heightStr, absSpectrogramPath)...)

		if runtime.GOOS == "windows" {
			cmd = exec.CommandContext(ctx, ffmpegBinary, ffmpegArgs...)
			soxCmd = exec.CommandContext(ctx, soxBinary, soxArgs...)
		} else {
			cmd = exec.CommandContext(ctx, "nice", append([]string{"-n", "19", ffmpegBinary}, ffmpegArgs...)...)
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
		soxArgs := append([]string{absAudioClipPath}, getSoxSpectrogramArgs(widthStr, heightStr, absSpectrogramPath)...)

		if runtime.GOOS == "windows" {
			soxCmd = exec.CommandContext(ctx, soxBinary, soxArgs...)
		} else {
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
	return nil
}

// getSoxSpectrogramArgs returns the common SoX arguments.
func getSoxSpectrogramArgs(widthStr, heightStr, absSpectrogramPath string) []string {
	const audioLength = "15"
	const dynamicRange = "100"
	args := []string{"-n", "rate", "24k", "spectrogram", "-x", widthStr, "-y", heightStr, "-d", audioLength, "-z", dynamicRange, "-o", absSpectrogramPath}
	width, _ := strconv.Atoi(widthStr)
	if width < 800 {
		args = append(args, "-r")
	}
	return args
}

// createSpectrogramWithFFmpeg generates a spectrogram using only ffmpeg.
// Accepts a context for timeout and cancellation.
func createSpectrogramWithFFmpeg(ctx context.Context, absAudioClipPath, absSpectrogramPath string, width int, settings *conf.Settings) error {
	ffmpegBinary := settings.Realtime.Audio.FfmpegPath
	if ffmpegBinary == "" {
		return fmt.Errorf("ffmpeg path not set in settings")
	}

	// Create context with timeout (use the passed-in context as parent)
	ctx, cancel := context.WithTimeout(ctx, 60*time.Second)
	defer cancel()

	spectrogramSemaphore <- struct{}{}
	defer func() { <-spectrogramSemaphore }()

	height := width / 2
	heightStr := strconv.Itoa(height)
	widthStr := strconv.Itoa(width)

	ffmpegArgs := []string{
		"-hide_banner",
		"-y",
		"-i", absAudioClipPath,
		"-lavfi", fmt.Sprintf("showspectrumpic=s=%sx%s:legend=0:gain=3:drange=100", widthStr, heightStr),
		"-frames:v", "1",
		absSpectrogramPath,
	}

	var cmd *exec.Cmd
	if runtime.GOOS == "windows" {
		cmd = exec.CommandContext(ctx, ffmpegBinary, ffmpegArgs...)
	} else {
		cmd = exec.CommandContext(ctx, "nice", append([]string{"-n", "19", ffmpegBinary}, ffmpegArgs...)...)
	}

	var output bytes.Buffer
	cmd.Stderr = &output
	cmd.Stdout = &output

	if err := cmd.Run(); err != nil {
		return fmt.Errorf("ffmpeg command failed: %w\nOutput: %s", err, output.String())
	}
	return nil
}

// HandleError method should exist on Controller, typically defined in controller.go or api.go
