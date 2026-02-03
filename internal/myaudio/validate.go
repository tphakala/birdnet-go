package myaudio

import (
	"bytes"
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"time"

	"github.com/tphakala/birdnet-go/internal/conf"
	"github.com/tphakala/birdnet-go/internal/errors"
	"github.com/tphakala/birdnet-go/internal/logger"
)

// Validation constants
const (
	// MinValidAudioSize is the minimum size for a valid audio file (1KB)
	MinValidAudioSize int64 = 1024

	// MaxValidationAttempts is the maximum number of validation attempts
	MaxValidationAttempts = 3

	// ValidationRetryDelay is the delay between validation attempts
	ValidationRetryDelay = 100 * time.Millisecond

	// FileStabilityCheckDuration is how long to wait to confirm file size is stable
	FileStabilityCheckDuration = 50 * time.Millisecond

	// FFprobeTimeout is the maximum time to wait for ffprobe validation
	FFprobeTimeout = 3 * time.Second

	// RetryDelayMultiplier is the multiplier for exponential backoff
	RetryDelayMultiplier = 2

	// LongRetryDelayMultiplier is the multiplier for longer retry delays
	LongRetryDelayMultiplier = 3

	// BitsPerKilobit is the conversion factor from bits to kilobits
	BitsPerKilobit = 1000

	// AudioHeaderSize is the number of bytes to read for format detection
	AudioHeaderSize = 12

	// RIFFHeaderOffset is the offset for WAVE format check in RIFF files
	RIFFHeaderOffset = 8

	// MP3SyncByteMask is the mask for MP3 sync byte validation
	MP3SyncByteMask = 0xE0
)

// Error codes for audio validation (used for metrics and debugging)
const (
	ErrCodeFileNotReady   = "AUDIO_NOT_READY"
	ErrCodeFileTooSmall   = "AUDIO_TOO_SMALL"
	ErrCodeFileCorrupted  = "AUDIO_CORRUPTED"
	ErrCodeFileIncomplete = "AUDIO_INCOMPLETE"
	ErrCodeFileEmpty      = "AUDIO_EMPTY"
	ErrCodeFileInvalid    = "AUDIO_INVALID"
	ErrCodeTimeout        = "VALIDATION_TIMEOUT"
	ErrCodeValidationFail = "VALIDATION_FAILED"
	ErrCodeFFprobeMissing = "FFPROBE_MISSING"
)

// Sentinel errors for audio validation
var (
	// File state errors
	ErrAudioFileNotReady   = errors.NewStd("audio file is not ready for processing")
	ErrAudioFileTooSmall   = errors.NewStd("audio file is too small to be valid")
	ErrAudioFileCorrupted  = errors.NewStd("audio file appears to be corrupted")
	ErrAudioFileIncomplete = errors.NewStd("audio file is incomplete or still being written")
	ErrAudioFileEmpty      = errors.NewStd("audio file is empty")
	ErrAudioFileInvalid    = errors.NewStd("audio file format is invalid")

	// Validation errors
	ErrValidationTimeout   = errors.NewStd("audio validation timed out")
	ErrValidationFailed    = errors.NewStd("audio validation failed")
	ErrFFprobeNotAvailable = errors.NewStd("ffprobe is not available for validation")
)

// AudioValidationResult contains the result of audio file validation
type AudioValidationResult struct {
	IsValid    bool          // Whether the file is valid and ready
	IsComplete bool          // Whether the file appears to be completely written
	FileSize   int64         // Size of the file in bytes
	Duration   float64       // Duration in seconds (0 if cannot be determined)
	Format     string        // Audio format (e.g., "wav", "flac", "mp3")
	SampleRate int           // Sample rate in Hz
	Channels   int           // Number of channels
	BitRate    int           // Bit rate in kbps
	Error      error         // Any error encountered during validation
	RetryAfter time.Duration // Suggested retry duration if file is not ready
}

// ValidateAudioFile validates that an audio file is complete and ready for processing.
// It performs multiple checks including file size, stability, and format validation.
// Returns detailed validation results including retry suggestions for incomplete files.
func ValidateAudioFile(ctx context.Context, audioPath string) (*AudioValidationResult, error) {
	result := &AudioValidationResult{
		IsValid:    false,
		IsComplete: false,
	}

	// Check if file exists
	fileInfo, err := os.Stat(audioPath)
	if err != nil {
		if os.IsNotExist(err) {
			result.Error = errors.New(err).
				Component("myaudio").
				Category(errors.CategoryFileIO).
				Context("operation", "validate_audio_file").
				Context("file_path", audioPath).
				Build()
			return result, result.Error
		}
		result.Error = errors.New(err).
			Component("myaudio").
			Category(errors.CategoryFileIO).
			Context("operation", "validate_audio_file").
			Context("file_path", audioPath).
			Build()
		return result, result.Error
	}

	result.FileSize = fileInfo.Size()

	// Check minimum file size
	if result.FileSize < MinValidAudioSize {
		if result.FileSize == 0 {
			result.Error = fmt.Errorf("%w: file size is 0 bytes", ErrAudioFileEmpty)
		} else {
			result.Error = fmt.Errorf("%w: size %d bytes < minimum %d bytes", ErrAudioFileTooSmall, result.FileSize, MinValidAudioSize)
		}
		result.RetryAfter = ValidationRetryDelay
		return result, nil // Return nil error to allow retry
	}

	// Check if file size is stable (not being written)
	if !isFileSizeStable(ctx, audioPath, fileInfo.Size()) {
		result.Error = fmt.Errorf("%w: file size is still changing", ErrAudioFileIncomplete)
		result.RetryAfter = ValidationRetryDelay * 2
		return result, nil // Return nil error to allow retry
	}

	// Extract format from extension
	ext := strings.ToLower(strings.TrimPrefix(filepath.Ext(audioPath), "."))
	result.Format = ext

	// Validate audio format using ffprobe
	if err := validateWithFFprobe(ctx, audioPath, result); err != nil {
		// Check if it's a context error (timeout/cancellation)
		if errors.Is(err, context.DeadlineExceeded) || errors.Is(err, context.Canceled) {
			result.Error = fmt.Errorf("%w: %w", ErrValidationTimeout, err)
			return result, result.Error
		}

		// For other errors, the file might be corrupted or invalid
		result.Error = fmt.Errorf("%w: ffprobe error: %w", ErrAudioFileInvalid, err)
		return result, nil // Return nil to indicate validation completed but file is invalid
	}

	// Mark file as complete - we don't validate against expected duration because:
	// 1. Users can change capture length at any time
	// 2. Existing files may have been captured with different settings
	// 3. Files from different sources may have various durations
	// The important thing is that the file is readable and has valid audio data
	result.IsComplete = true

	// All checks passed
	result.IsValid = true
	result.Error = nil
	return result, nil
}

// ValidateAudioFileWithRetry validates an audio file with automatic retry logic.
// It will retry up to MaxValidationAttempts times with exponential backoff.
func ValidateAudioFileWithRetry(ctx context.Context, audioPath string) (*AudioValidationResult, error) {
	var lastResult *AudioValidationResult
	retryDelay := ValidationRetryDelay
	startTime := time.Now()

	for attempt := 1; attempt <= MaxValidationAttempts; attempt++ {
		// Check context before each attempt
		if ctx.Err() != nil {
			return nil, ctx.Err()
		}

		result, err := ValidateAudioFile(ctx, audioPath)

		if err != nil {
			// Hard error, don't retry
			// Log the failure
			if m := getFileMetrics(); m != nil {
				m.RecordAudioDataValidationError("validate_with_retry", "hard_error")
			}
			return result, err
		}

		lastResult = result

		// If valid, return immediately
		if result.IsValid {
			// Log successful validation with retry info
			if m := getFileMetrics(); m != nil && attempt > 1 {
				m.RecordFileOperation("validate_with_retry", result.Format, "success_after_retry")
			}
			return result, nil
		}

		// Log retry attempt if metrics available
		if m := getFileMetrics(); m != nil {
			m.RecordFileOperation("validate_retry", result.Format, fmt.Sprintf("attempt_%d", attempt))
		}

		// If there's a specific retry suggestion, use it
		if result.RetryAfter > 0 {
			retryDelay = result.RetryAfter
		}

		// Don't retry on the last attempt
		if attempt < MaxValidationAttempts {
			// Wait before retry with exponential backoff
			select {
			case <-time.After(retryDelay):
				retryDelay *= RetryDelayMultiplier // Exponential backoff
			case <-ctx.Done():
				return lastResult, ctx.Err()
			}
		}
	}

	// All attempts exhausted
	totalDuration := time.Since(startTime)
	if m := getFileMetrics(); m != nil {
		m.RecordFileOperationError("validate_with_retry", "unknown",
			fmt.Sprintf("exhausted_after_%dms", totalDuration.Milliseconds()))
	}

	if lastResult != nil && lastResult.Error != nil {
		return lastResult, lastResult.Error
	}
	return lastResult, ErrValidationFailed
}

// isFileSizeStable checks if a file's size is stable (not being written)
// It uses a ticker with context support for cancellation instead of blocking sleep
func isFileSizeStable(ctx context.Context, path string, initialSize int64) bool {
	// Create a ticker for the stability check duration
	ticker := time.NewTicker(FileStabilityCheckDuration)
	defer ticker.Stop()

	// Wait for the duration or context cancellation
	select {
	case <-ticker.C:
		// Check the file size after waiting
		info, err := os.Stat(path)
		if err != nil {
			return false
		}
		return info.Size() == initialSize
	case <-ctx.Done():
		// Context was cancelled, return false
		return false
	}
}

// executeFFprobe runs ffprobe command and returns output
func executeFFprobe(ctx context.Context, audioPath string) (string, error) {
	ffprobeBinary := conf.GetFfprobeBinaryName()
	if ffprobeBinary == "" {
		return "", ErrFFprobeNotAvailable
	}

	// Build ffprobe command to get format and stream information
	cmd := exec.CommandContext(ctx, ffprobeBinary, //nolint:gosec // G204: ffprobeBinary from conf.GetFfprobeBinaryName(), args are fixed
		"-v", "error",
		"-show_entries", "format=duration,bit_rate:stream=sample_rate,channels,codec_name",
		"-of", "csv=p=0",
		audioPath)

	var out bytes.Buffer
	var stderr bytes.Buffer
	cmd.Stdout = &out
	cmd.Stderr = &stderr

	if err := cmd.Run(); err != nil {
		// Check for context errors
		if ctx.Err() != nil {
			return "", ctx.Err()
		}
		// Check stderr for specific errors
		stderrStr := stderr.String()
		if strings.Contains(stderrStr, "Invalid data found") ||
			strings.Contains(stderrStr, "could not find codec parameters") {
			return "", fmt.Errorf("invalid audio format: %s", stderrStr)
		}
		return "", fmt.Errorf("ffprobe validation failed: %w (stderr: %s)", err, stderrStr)
	}

	output := strings.TrimSpace(out.String())
	if output == "" {
		return "", fmt.Errorf("ffprobe returned no data")
	}

	return output, nil
}

// parseFFprobeLine parses a single line of FFprobe CSV output
func parseFFprobeLine(line string, result *AudioValidationResult) {
	// Collect fields from the CSV line using Go 1.24 iterator
	fields := slices.Collect(strings.SplitSeq(line, ","))

	if len(fields) < 2 {
		return
	}

	// Check if this is format data (duration,bit_rate)
	if duration, err := strconv.ParseFloat(fields[0], 64); err == nil && strings.Contains(fields[0], ".") {
		result.Duration = duration
		if len(fields) > 1 {
			if bitRate, err := strconv.Atoi(fields[1]); err == nil {
				result.BitRate = bitRate / BitsPerKilobit
			}
		}
		return
	}

	// Check if this is stream data (codec_name,sample_rate,channels)
	if len(fields) >= 3 {
		if sampleRate, err := strconv.Atoi(fields[1]); err == nil && sampleRate > 0 {
			result.SampleRate = sampleRate
			if channels, err := strconv.Atoi(fields[2]); err == nil {
				result.Channels = channels
			}
			if fields[0] != "" {
				result.Format = fields[0]
			}
		}
		return
	}

	// Handle truncated stream data (sample_rate,channels only)
	if len(fields) == 2 {
		if val1, err1 := strconv.Atoi(fields[0]); err1 == nil {
			if val2, err2 := strconv.Atoi(fields[1]); err2 == nil {
				// Both integers, likely sample_rate,channels
				if val1 > 1000 { // Sample rates are typically > 1000
					result.SampleRate = val1
					result.Channels = val2
				}
			}
		}
	}
}

// validateWithFFprobe uses ffprobe to validate audio file format and extract metadata
func validateWithFFprobe(ctx context.Context, audioPath string, result *AudioValidationResult) error {
	// Create context with timeout if not provided
	if ctx == nil {
		var cancel context.CancelFunc
		ctx, cancel = context.WithTimeout(context.Background(), FFprobeTimeout)
		defer cancel()
	}

	// Execute ffprobe command
	output, err := executeFFprobe(ctx, audioPath)
	if err != nil {
		return err
	}

	// Parse the CSV output line by line
	// FFprobe outputs stream data first (codec_name,sample_rate,channels)
	// then format data (duration,bit_rate) on separate lines
	for line := range strings.Lines(output) {
		if line == "" {
			continue
		}
		parseFFprobeLine(line, result)
	}

	// Basic validation of parsed data
	if result.Duration <= 0 && result.SampleRate <= 0 {
		return fmt.Errorf("unable to extract valid audio metadata")
	}

	return nil
}

// QuickValidateAudioFile performs a quick validation check without detailed analysis.
// This is useful for fast path checks before expensive operations.
func QuickValidateAudioFile(audioPath string) (bool, error) {
	fileInfo, err := os.Stat(audioPath)
	if err != nil {
		if os.IsNotExist(err) {
			return false, nil
		}
		return false, err
	}

	// Quick size check
	if fileInfo.Size() < MinValidAudioSize {
		return false, nil
	}

	// Check if file can be opened (basic accessibility check)
	file, err := os.Open(audioPath) //nolint:gosec // G304: audioPath is from directory walking
	if err != nil {
		return false, nil
	}
	defer func() {
		if err := file.Close(); err != nil {
			// Log the close error for debugging purposes
			log := GetLogger()
			log.Debug("failed to close audio file during validation",
				logger.String("path", audioPath),
				logger.Error(err))
		}
	}()

	// Read first few bytes to check for valid header (basic format check)
	header := make([]byte, AudioHeaderSize)
	n, err := file.Read(header)
	if err != nil || n < AudioHeaderSize {
		return false, nil
	}

	// Check for common audio file signatures
	// WAV: "RIFF"
	if bytes.HasPrefix(header, []byte("RIFF")) && bytes.Contains(header[RIFFHeaderOffset:AudioHeaderSize], []byte("WAVE")) {
		return true, nil
	}
	// FLAC: "fLaC"
	if bytes.HasPrefix(header, []byte("fLaC")) {
		return true, nil
	}
	// MP3: ID3 or FF FB/FF FA
	if bytes.HasPrefix(header, []byte("ID3")) ||
		(header[0] == 0xFF && (header[1]&MP3SyncByteMask) == MP3SyncByteMask) {
		return true, nil
	}
	// OGG: "OggS"
	if bytes.HasPrefix(header, []byte("OggS")) {
		return true, nil
	}

	// Unknown format, but file exists and has content
	return true, nil
}
