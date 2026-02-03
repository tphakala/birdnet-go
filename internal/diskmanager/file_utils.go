// file_utils.go - shared file management code
package diskmanager

import (
	"context"
	"encoding/csv"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"slices"
	"strconv"
	"strings"
	"time"

	"github.com/tphakala/birdnet-go/internal/errors"
	"github.com/tphakala/birdnet-go/internal/logger"
)

// tempFileExt is the temporary file extension used during audio file creation.
// Audio files are written with this suffix during recording and renamed upon
// completion to ensure atomic file operations. This must match the TempExt
// constant in the myaudio package.
const tempFileExt = ".temp"

// allowedFileTypes is the list of file extensions that are allowed to be deleted
var allowedFileTypes = []string{".wav", ".flac", ".aac", ".opus", ".mp3", ".m4a"}

// FileInfo holds information about a file
type FileInfo struct {
	Path       string
	Species    string
	Confidence int
	Timestamp  time.Time
	Size       int64
	Locked     bool
}

// Interface represents the minimal database interface needed for diskmanager
type Interface interface {
	GetLockedNotesClipPaths() ([]string, error)
}

// LoadPolicy loads the cleanup policies from a CSV file
func LoadPolicy(policyFile string) (*Policy, error) {
	file, err := os.Open(policyFile) //nolint:gosec // G304: policyFile is from application settings
	if err != nil {
		descriptiveErr := errors.Newf("diskmanager: failed to open policy file: %w", err).
			Component("diskmanager").
			Category(errors.CategoryPolicyConfig).
			FileContext(policyFile, 0).
			Context("operation", "open_policy_file").
			Build()
		return nil, descriptiveErr
	}
	defer func() {
		if err := file.Close(); err != nil {
			GetLogger().Warn("Failed to close policy file",
				logger.String("file", policyFile),
				logger.Error(err))
		}
	}()

	reader := csv.NewReader(file)
	records, err := reader.ReadAll()
	if err != nil {
		descriptiveErr := errors.Newf("diskmanager: failed to parse policy CSV file: %w", err).
			Component("diskmanager").
			Category(errors.CategoryPolicyConfig).
			FileContext(policyFile, 0).
			Context("operation", "parse_csv").
			Build()
		return nil, descriptiveErr
	}

	policy := &Policy{
		AlwaysCleanupFirst: make(map[string]bool),
		NeverCleanup:       make(map[string]bool),
	}

	for i, record := range records {
		if len(record) != 2 {
			descriptiveErr := errors.Newf("diskmanager: invalid policy CSV format at line %d: expected 2 fields, got %d", i+1, len(record)).
				Component("diskmanager").
				Category(errors.CategoryPolicyConfig).
				FileContext(policyFile, 0).
				Context("line_number", i+1).
				Context("fields_count", len(record)).
				Build()
			return nil, descriptiveErr
		}
		switch record[1] {
		case "always":
			policy.AlwaysCleanupFirst[record[0]] = true
		case "never":
			policy.NeverCleanup[record[0]] = true
		}
	}

	return policy, nil
}

// walkState holds the state for directory walking operations
type walkState struct {
	ctx             context.Context
	allowedExts     []string
	lockedSet       map[string]struct{}
	files           []FileInfo
	parseErrorCount int
	firstParseError error
	maxParseErrors  int
	debug           bool
}

// GetAudioFiles returns a list of audio files in the directory and its subdirectories
func GetAudioFiles(baseDir string, allowedExts []string, db Interface, debug bool) ([]FileInfo, error) {
	return GetAudioFilesContext(context.Background(), baseDir, allowedExts, db, debug)
}

// createWalkFunc creates the filepath.Walk function with all necessary context
func createWalkFunc(state *walkState) filepath.WalkFunc {
	return func(path string, info os.FileInfo, err error) error {
		// Check for context cancellation
		select {
		case <-state.ctx.Done():
			return state.ctx.Err()
		default:
		}

		if err != nil {
			return handleWalkError(err, path, state.debug)
		}

		if !info.IsDir() {
			processFile(path, info, state)
		}

		// Yield to other goroutines
		runtime.Gosched()
		return nil
	}
}

// handleWalkError handles errors during filepath.Walk, specifically temp file race conditions
func handleWalkError(err error, path string, debug bool) error {
	// Handle race condition where temp files are renamed between directory
	// listing and lstat call. If the error is "no such file or directory"
	// and the path appears to be a temp file, continue walking.
	if os.IsNotExist(err) && strings.HasSuffix(strings.ToLower(path), tempFileExt) {
		GetLogger().Debug("Skipping missing temp file (likely renamed during processing)",
			logger.String("path", path))
		return nil // Continue walking
	}
	return err
}

// processFile processes a single file during directory walking
func processFile(path string, info os.FileInfo, state *walkState) {
	fileName := info.Name()

	// Skip temporary files that are currently being written.
	// Audio files are written with .temp suffix during recording
	// and renamed upon completion to ensure atomic file operations.
	// Using case-insensitive check to handle edge cases.
	// Fast path: skip temp files early
	if strings.HasSuffix(strings.ToLower(fileName), tempFileExt) {
		return
	}

	// Fast path: check extension before expensive operations (case-insensitive)
	ext := strings.ToLower(filepath.Ext(fileName))
	if !contains(state.allowedExts, ext) {
		return
	}

	// Process valid file
	fileInfo, err := parseFileInfo(path, info, state.allowedExts)
	if err != nil {
		// Track first error and count without storing all errors
		if state.firstParseError == nil {
			state.firstParseError = err
		}
		state.parseErrorCount++
		if state.parseErrorCount <= state.maxParseErrors {
			GetLogger().Debug("Error parsing file",
				logger.String("path", path),
				logger.Error(err))
		}
		return // Continue with next file
	}

	// Check if the file is protected using O(1) lookup
	_, fileInfo.Locked = state.lockedSet[filepath.Base(fileInfo.Path)]
	state.files = append(state.files, fileInfo)
}

// GetAudioFilesContext returns a list of audio files in the directory and its subdirectories with context support for cancellation
func GetAudioFilesContext(ctx context.Context, baseDir string, allowedExts []string, db Interface, debug bool) ([]FileInfo, error) {
	// Fast path: empty allowed extensions
	if len(allowedExts) == 0 {
		return nil, nil
	}

	// Normalize extensions to lowercase once for case-insensitive matching
	allowedExts = slices.Clone(allowedExts)
	for i := range allowedExts {
		allowedExts[i] = strings.ToLower(allowedExts[i])
	}

	// Use pooled slice to reduce allocations
	pooledSlice := getPooledSlice()
	defer func() {
		// Handle panics and ensure pool cleanup
		if r := recover(); r != nil {
			// Always return to pool on panic
			pooledSlice.Release()
			panic(r) // Re-panic after cleanup
		}
	}()

	// Work directly with the pooled slice
	filesPtr := pooledSlice.Data()
	files := (*filesPtr)[:0:cap(*filesPtr)]
	// We'll update the pooled slice reference after appending

	var parseErrorCount int
	var firstParseError error
	maxParseErrors := loadPoolConfig().MaxParseErrors // Use configurable limit

	// Get list of protected clips from database
	lockedClips, err := getLockedClips(db)
	if err != nil {
		descriptiveErr := errors.Newf("diskmanager: failed to get locked clips from database: %w", err).
			Component("diskmanager").
			Category(errors.CategoryDatabase).
			Context("operation", "get_locked_clips").
			Build()
		return nil, descriptiveErr
	}

	log := GetLogger()
	log.Debug("Found protected clips",
		logger.Int("count", len(lockedClips)))

	// Build a set of basenames for fast O(1) membership checks
	lockedSet := make(map[string]struct{}, len(lockedClips))
	for _, p := range lockedClips {
		lockedSet[filepath.Base(p)] = struct{}{}
	}

	// Create walk state to hold all the parameters
	state := &walkState{
		ctx:             ctx,
		allowedExts:     allowedExts,
		lockedSet:       lockedSet,
		files:           files,
		parseErrorCount: parseErrorCount,
		firstParseError: firstParseError,
		maxParseErrors:  maxParseErrors,
		debug:           debug,
	}

	err = filepath.Walk(baseDir, createWalkFunc(state))

	if err != nil {
		// Check if it was a context cancellation
		if errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) {
			return nil, err // Return context error directly for proper handling
		}

		descriptiveErr := errors.Newf("diskmanager: failed to walk directory for audio files: %w", err).
			Component("diskmanager").
			Category(errors.CategoryFileIO).
			Context("operation", "walk_directory").
			Context("base_dir", baseDir).
			Build()
		return nil, descriptiveErr
	}

	// If we encountered parse errors but still have some valid files, log a summary but continue
	if state.parseErrorCount > 0 {
		log.Debug("Encountered file parsing errors during cleanup",
			logger.Int("error_count", state.parseErrorCount),
			logger.Int("max_logged", maxParseErrors))
		// If we have no valid files at all, return an error
		if len(state.files) == 0 && state.firstParseError != nil {
			descriptiveErr := errors.Newf("diskmanager: failed to parse any audio files: %w", state.firstParseError).
				Component("diskmanager").
				Category(errors.CategoryFileParsing).
				Context("base_dir", baseDir).
				Context("parse_errors_count", state.parseErrorCount).
				Build()
			return nil, descriptiveErr
		}
	}

	// Update the pooled slice with the final data while preserving the backing array
	pooledSlice.SetData(state.files)

	// Fast path: if no files found, return early
	if len(state.files) == 0 {
		pooledSlice.Release() // Release the empty pooled slice
		return nil, nil
	}

	// Transfer ownership of the data and automatically release the pooled slice
	return pooledSlice.TakeOwnership(), nil
}

// parseFileInfo parses the file information from the file path and os.FileInfo
func parseFileInfo(path string, info os.FileInfo, allowedExts []string) (FileInfo, error) {
	name := filepath.Base(info.Name())

	// Check if the file extension is allowed (case-insensitive)
	ext := strings.ToLower(filepath.Ext(name))
	if !contains(allowedExts, ext) {
		// Lightweight error with essential context
		descriptiveErr := errors.Newf("diskmanager: file type %s not eligible for cleanup", ext).
			Component("diskmanager").
			Context("allowed_types", allowedExts).
			Build()
		return FileInfo{}, descriptiveErr
	}

	// Remove the extension for parsing
	nameWithoutExt := strings.TrimSuffix(name, ext)

	// Handle special case for thumbnail suffixes like _400px
	nameWithoutExt = strings.TrimSuffix(nameWithoutExt, "_400px")

	parts := strings.Split(nameWithoutExt, "_")
	if len(parts) < 3 {
		// Lightweight error with format guidance
		descriptiveErr := errors.Newf("diskmanager: invalid filename format: %s", name).
			Component("diskmanager").
			Context("expected_format", "species_confidence_timestamp").
			Build()
		return FileInfo{}, descriptiveErr
	}

	// The species name might contain underscores, so we need to handle the last two parts separately
	confidenceStr := parts[len(parts)-2]
	timestampStr := parts[len(parts)-1]
	species := strings.Join(parts[:len(parts)-2], "_")

	confidence, err := strconv.Atoi(strings.TrimSuffix(confidenceStr, "p"))
	if err != nil {
		// Lightweight error for confidence parsing
		descriptiveErr := errors.Newf("diskmanager: invalid confidence value in %s", name).
			Component("diskmanager").
			Context("confidence_string", confidenceStr).
			Build()
		return FileInfo{}, descriptiveErr
	}

	// IMPORTANT: Despite the Z suffix in the filename (which normally indicates UTC),
	// the timestamps in the filenames are actually in local time.
	// So we need to parse it in a special way to get the correct local time.

	// First, parse the string but ignore the timezone (by removing the Z)
	timestampStrLocal := strings.TrimSuffix(timestampStr, "Z")

	// Parse it using a format string without timezone indicator
	timestamp, err := time.ParseInLocation("20060102T150405", timestampStrLocal, time.Local)
	if err != nil {
		// Fallback to original method if this fails, for backward compatibility
		timestamp, err = time.Parse("20060102T150405Z", timestampStr)
		if err != nil {
			// Lightweight error for timestamp parsing
			descriptiveErr := errors.Newf("diskmanager: invalid timestamp in %s", name).
				Component("diskmanager").
				Context("timestamp_string", timestampStr).
				Build()
			return FileInfo{}, descriptiveErr
		}
		// Convert UTC time to local time explicitly if needed
		timestamp = timestamp.In(time.Local)
	}

	return FileInfo{
		Path:       path,
		Species:    species,
		Confidence: confidence,
		Timestamp:  timestamp,
		Size:       info.Size(),
	}, nil
}

// contains checks if a string is in a slice
// Optimized with early exit and common case first
func contains(slice []string, item string) bool {
	// Fast path: empty slice or item
	if len(slice) == 0 || item == "" {
		return false
	}

	// Use standard library slices.Contains for optimal performance
	return slices.Contains(slice, item)
}

// WriteSortedFilesToFile writes the sorted list of files to a text file for investigation
func WriteSortedFilesToFile(files []FileInfo, filePath string) error {
	log := GetLogger()

	file, err := os.Create(filePath) //nolint:gosec // G304: filePath is programmatically constructed
	if err != nil {
		return fmt.Errorf("failed to create file: %w", err)
	}
	defer func() {
		if err := file.Close(); err != nil {
			log.Warn("Failed to close file",
				logger.String("path", filePath),
				logger.Error(err))
		}
	}()

	for _, fileInfo := range files {
		line := fmt.Sprintf("Path: %s, Species: %s, Confidence: %d, Timestamp: %s, Size: %d\n",
			fileInfo.Path, fileInfo.Species, fileInfo.Confidence, fileInfo.Timestamp.Format(time.RFC3339), fileInfo.Size)
		_, err := file.WriteString(line)
		if err != nil {
			return fmt.Errorf("failed to write to file: %w", err)
		}
	}

	if err := file.Sync(); err != nil {
		return fmt.Errorf("failed to sync file: %w", err)
	}

	log.Info("Sorted files have been written",
		logger.String("path", filePath),
		logger.Int("file_count", len(files)))
	return nil
}

// getLockedClips retrieves the list of locked clip paths from the database
func getLockedClips(db Interface) ([]string, error) {
	if db == nil {
		return nil, fmt.Errorf("database interface is nil")
	}
	return db.GetLockedNotesClipPaths()
}

// isLockedClip checks if a file path is in the list of locked clips
func isLockedClip(path string, lockedClips []string) bool {
	filename := filepath.Base(path)
	for _, lockedPath := range lockedClips {
		if filepath.Base(lockedPath) == filename {
			return true
		}
	}
	return false
}
