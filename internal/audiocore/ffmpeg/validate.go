package ffmpeg

import (
	"bytes"
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"time"

	"github.com/tphakala/birdnet-go/internal/errors"
)

// Validation constants for audio file validation.
const (
	// MinValidAudioSize is the minimum size for a valid audio file (1KB).
	MinValidAudioSize int64 = 1024

	// MaxValidationAttempts is the maximum number of validation attempts.
	// Set to 5 to accommodate slow storage (SD cards, network drives) on
	// resource-constrained devices like Raspberry Pi / arm64 containers.
	MaxValidationAttempts = 5

	// ValidationRetryDelay is the base delay between validation attempts.
	// 500ms gives slow storage enough time between retries.
	ValidationRetryDelay = 500 * time.Millisecond

	// FileStabilityCheckDuration is how long to wait to confirm file size is stable.
	// 200ms handles slow I/O on SD cards and network drives.
	FileStabilityCheckDuration = 200 * time.Millisecond

	// FFprobeTimeout is the maximum time to wait for ffprobe validation.
	FFprobeTimeout = 3 * time.Second

	// RetryDelayMultiplier is the multiplier for exponential backoff.
	RetryDelayMultiplier = 2

	// BitsPerKilobit is the conversion factor from bits to kilobits.
	BitsPerKilobit = 1000

	// AudioHeaderSize is the number of bytes to read for format detection.
	AudioHeaderSize = 12

	// RIFFHeaderOffset is the offset for WAVE format check in RIFF files.
	RIFFHeaderOffset = 8

	// MP3SyncByteMask is the mask for MP3 sync byte validation.
	MP3SyncByteMask = 0xE0
)

// Error codes for audio validation (used for metrics and debugging).
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

// Sentinel errors for audio validation.
var (
	// File state errors.
	ErrAudioFileNotReady   = errors.NewStd("audio file is not ready for processing")
	ErrAudioFileTooSmall   = errors.NewStd("audio file is too small to be valid")
	ErrAudioFileCorrupted  = errors.NewStd("audio file appears to be corrupted")
	ErrAudioFileIncomplete = errors.NewStd("audio file is incomplete or still being written")
	ErrAudioFileEmpty      = errors.NewStd("audio file is empty")
	ErrAudioFileInvalid    = errors.NewStd("audio file format is invalid")

	// Validation errors.
	ErrValidationTimeout   = errors.NewStd("audio validation timed out")
	ErrValidationFailed    = errors.NewStd("audio validation failed")
	ErrFFprobeNotAvailable = errors.NewStd("ffprobe is not available for validation")
)

// ValidationResult contains the result of audio file validation.
type ValidationResult struct {
	// IsValid indicates whether the file is valid and ready.
	IsValid bool
	// IsComplete indicates whether the file appears to be completely written.
	IsComplete bool
	// FileSize is the size of the file in bytes.
	FileSize int64
	// Duration is the file duration in seconds (0 if undetermined).
	Duration float64
	// Format is the audio format (e.g., "wav", "flac", "mp3").
	Format string
	// SampleRate is the sample rate in Hz.
	SampleRate int
	// Channels is the number of channels.
	Channels int
	// BitRate is the bitrate in kbps.
	BitRate int
	// Error contains any error encountered during validation.
	Error error
	// RetryAfter is the suggested retry duration if the file is not ready.
	RetryAfter time.Duration
}

// ValidateOption is a functional option for ValidateFile.
type ValidateOption func(*validateConfig)

type validateConfig struct {
	// maxAttempts overrides the default retry count.
	maxAttempts int
	// retryDelay overrides the initial retry delay.
	retryDelay time.Duration
}

// WithMaxAttempts sets the maximum number of validation attempts.
func WithMaxAttempts(n int) ValidateOption {
	return func(c *validateConfig) {
		c.maxAttempts = n
	}
}

// WithRetryDelay sets the initial retry delay (used for exponential backoff).
func WithRetryDelay(d time.Duration) ValidateOption {
	return func(c *validateConfig) {
		c.retryDelay = d
	}
}

// ValidateFile validates an audio file with optional retry and backoff.
// It performs multiple checks: file size, stability, and ffprobe format validation.
// By default it retries up to MaxValidationAttempts times with exponential backoff.
func ValidateFile(ctx context.Context, path string, opts ...ValidateOption) (*ValidationResult, error) {
	cfg := validateConfig{
		maxAttempts: MaxValidationAttempts,
		retryDelay:  ValidationRetryDelay,
	}
	for _, o := range opts {
		o(&cfg)
	}

	var lastResult *ValidationResult
	retryDelay := cfg.retryDelay
	startTime := time.Now()

	for attempt := 1; attempt <= cfg.maxAttempts; attempt++ {
		if ctx.Err() != nil {
			return lastResult, ctx.Err()
		}

		result, err := validateFile(ctx, path)
		if err != nil {
			// Hard error (e.g. context deadline, permissions) — do not retry.
			return result, err
		}

		lastResult = result

		if result.IsValid {
			return result, nil
		}

		// Don't wait after the last attempt.
		if attempt < cfg.maxAttempts {
			if result.RetryAfter > retryDelay {
				retryDelay = result.RetryAfter
			}
			select {
			case <-time.After(retryDelay):
				retryDelay *= RetryDelayMultiplier
			case <-ctx.Done():
				return lastResult, ctx.Err()
			}
		}
	}

	// All attempts exhausted.
	totalDuration := time.Since(startTime)

	var fileExists bool
	var fileSizeBytes int64 = -1
	if info, statErr := os.Stat(path); statErr == nil {
		fileExists = true
		fileSizeBytes = info.Size()
	}

	baseErr := ErrValidationFailed
	if lastResult != nil && lastResult.Error != nil {
		switch {
		case errors.Is(lastResult.Error, ErrAudioFileEmpty):
			baseErr = ErrAudioFileEmpty
		case errors.Is(lastResult.Error, ErrAudioFileTooSmall):
			baseErr = ErrAudioFileTooSmall
		case errors.Is(lastResult.Error, ErrAudioFileIncomplete):
			baseErr = ErrAudioFileIncomplete
		case errors.Is(lastResult.Error, ErrAudioFileInvalid):
			baseErr = ErrAudioFileInvalid
		}
	}

	exhaustionErr := errors.New(baseErr).
		Component("audiocore").
		Category(errors.CategoryTimeout).
		Context("operation", "validate_audio_file").
		Context("file_exists", fileExists).
		Context("file_size_bytes", fileSizeBytes).
		Context("total_duration_ms", totalDuration.Milliseconds()).
		Context("max_attempts", cfg.maxAttempts).
		Build()

	return lastResult, exhaustionErr
}

// validateFile performs a single validation pass without retry.
func validateFile(ctx context.Context, path string) (*ValidationResult, error) {
	result := &ValidationResult{}

	fileInfo, err := os.Stat(path)
	if err != nil {
		result.Error = errors.New(err).
			Component("audiocore").
			Category(errors.CategoryFileIO).
			Context("operation", "validate_audio_file").
			Context("file_path", path).
			Build()
		return result, result.Error
	}

	result.FileSize = fileInfo.Size()

	// Check minimum file size.
	if result.FileSize < MinValidAudioSize {
		if result.FileSize == 0 {
			result.Error = fmt.Errorf("%w: file size is 0 bytes", ErrAudioFileEmpty)
		} else {
			result.Error = fmt.Errorf("%w: size %d bytes < minimum %d bytes", ErrAudioFileTooSmall, result.FileSize, MinValidAudioSize)
		}
		result.RetryAfter = ValidationRetryDelay
		return result, nil // return nil to allow retry
	}

	// Check if the file is still being written.
	if !isFileSizeStable(ctx, path, fileInfo.Size()) {
		result.Error = fmt.Errorf("%w: file size is still changing", ErrAudioFileIncomplete)
		result.RetryAfter = ValidationRetryDelay * 2
		return result, nil
	}

	// Extract format from file extension.
	ext := strings.ToLower(strings.TrimPrefix(filepath.Ext(path), "."))
	result.Format = ext

	// Use ffprobe for format and metadata validation.
	if err := validateWithFFprobe(ctx, path, result); err != nil {
		if errors.Is(err, context.DeadlineExceeded) || errors.Is(err, context.Canceled) {
			result.Error = fmt.Errorf("%w: %w", ErrValidationTimeout, err)
			return result, result.Error
		}
		result.Error = fmt.Errorf("%w: ffprobe error: %w", ErrAudioFileInvalid, err)
		return result, nil
	}

	result.IsComplete = true
	result.IsValid = true
	result.Error = nil
	return result, nil
}

// QuickValidateFile performs a quick validation check without ffprobe analysis.
// Useful for fast path checks before expensive operations.
func QuickValidateFile(path string) (bool, error) {
	fileInfo, err := os.Stat(path)
	if err != nil {
		if os.IsNotExist(err) {
			return false, nil
		}
		return false, err
	}

	if fileInfo.Size() < MinValidAudioSize {
		return false, nil
	}

	file, err := os.Open(path) //nolint:gosec // G304: path comes from directory walking / caller
	if err != nil {
		return false, nil
	}
	defer func() { _ = file.Close() }()

	header := make([]byte, AudioHeaderSize)
	n, err := file.Read(header)
	if err != nil || n < AudioHeaderSize {
		return false, nil
	}

	// Check for common audio file signatures.
	switch {
	case bytes.HasPrefix(header, []byte("RIFF")) &&
		bytes.Contains(header[RIFFHeaderOffset:AudioHeaderSize], []byte("WAVE")):
		// WAV
		return true, nil
	case bytes.HasPrefix(header, []byte("fLaC")):
		// FLAC
		return true, nil
	case bytes.HasPrefix(header, []byte("ID3")) ||
		(header[0] == 0xFF && (header[1]&MP3SyncByteMask) == MP3SyncByteMask):
		// MP3
		return true, nil
	case bytes.HasPrefix(header, []byte("OggS")):
		// OGG (Opus)
		return true, nil
	default:
		// Unknown format but file exists and has content.
		return true, nil
	}
}

// isFileSizeStable checks if a file's size is stable (not being written).
// It waits FileStabilityCheckDuration and then re-reads the size.
func isFileSizeStable(ctx context.Context, path string, initialSize int64) bool {
	select {
	case <-time.After(FileStabilityCheckDuration):
		info, err := os.Stat(path)
		if err != nil {
			return false
		}
		return info.Size() == initialSize
	case <-ctx.Done():
		return false
	}
}

// getFfprobeBinaryName returns the platform-appropriate ffprobe binary name.
func getFfprobeBinaryName() string {
	if runtime.GOOS == "windows" {
		return "ffprobe.exe"
	}
	return "ffprobe"
}

// executeFFprobe runs ffprobe and returns its CSV output.
func executeFFprobe(ctx context.Context, path string) (string, error) {
	ffprobeBinary := getFfprobeBinaryName()

	// Ensure ffprobe is available before attempting to run it.
	if _, err := exec.LookPath(ffprobeBinary); err != nil {
		return "", ErrFFprobeNotAvailable
	}

	cmd := exec.CommandContext(ctx, ffprobeBinary, //nolint:gosec // G204: ffprobeBinary is a fixed platform constant, path validated by caller
		"-v", "error",
		"-show_entries", "format=duration,bit_rate:stream=sample_rate,channels,codec_name",
		"-of", "csv=p=0",
		path)

	var out bytes.Buffer
	var stderr bytes.Buffer
	cmd.Stdout = &out
	cmd.Stderr = &stderr

	if err := cmd.Run(); err != nil {
		if ctx.Err() != nil {
			return "", ctx.Err()
		}
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

// parseFFprobeLine parses a single line of ffprobe CSV output into a ValidationResult.
func parseFFprobeLine(line string, result *ValidationResult) {
	fields := make([]string, 0, 3)
	for field := range strings.SplitSeq(line, ",") {
		fields = append(fields, field)
	}

	if len(fields) < 2 {
		return
	}

	// Format line: duration,bit_rate (the duration field contains a decimal point).
	if duration, err := strconv.ParseFloat(fields[0], 64); err == nil && strings.Contains(fields[0], ".") {
		result.Duration = duration
		if len(fields) > 1 {
			if bitRate, err := strconv.Atoi(fields[1]); err == nil {
				result.BitRate = bitRate / BitsPerKilobit
			}
		}
		return
	}

	// Stream line: codec_name,sample_rate,channels
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

	// Truncated stream line: sample_rate,channels
	if len(fields) == 2 {
		if val1, err1 := strconv.Atoi(fields[0]); err1 == nil {
			if val2, err2 := strconv.Atoi(fields[1]); err2 == nil {
				if val1 > 1000 { // sample rates are > 1000 Hz
					result.SampleRate = val1
					result.Channels = val2
				}
			}
		}
	}
}

// validateWithFFprobe uses ffprobe to validate audio file format and extract metadata.
func validateWithFFprobe(ctx context.Context, path string, result *ValidationResult) error {
	// Apply a per-call timeout if the context has no deadline.
	if _, hasDeadline := ctx.Deadline(); !hasDeadline {
		var cancel context.CancelFunc
		ctx, cancel = context.WithTimeout(ctx, FFprobeTimeout)
		defer cancel()
	}

	output, err := executeFFprobe(ctx, path)
	if err != nil {
		return err
	}

	for line := range strings.Lines(output) {
		if line == "" {
			continue
		}
		parseFFprobeLine(line, result)
	}

	if result.Duration <= 0 && result.SampleRate <= 0 {
		return fmt.Errorf("unable to extract valid audio metadata")
	}

	return nil
}
