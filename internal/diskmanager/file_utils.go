// file_utils.go - shared file management code
package diskmanager

import (
	"encoding/csv"
	"fmt"
	"log"
	"os"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"time"

	"github.com/tphakala/birdnet-go/internal/errors"
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
	file, err := os.Open(policyFile)
	if err != nil {
		descriptiveErr := errors.New(fmt.Errorf("diskmanager: failed to open policy file: %w", err)).
			Component("diskmanager").
			Category(errors.CategoryPolicyConfig).
			FileContext(policyFile, 0).
			Context("operation", "open_policy_file").
			Build()
		return nil, descriptiveErr
	}
	defer func() {
		if err := file.Close(); err != nil {
			log.Printf("Failed to close file: %v", err)
		}
	}()

	reader := csv.NewReader(file)
	records, err := reader.ReadAll()
	if err != nil {
		descriptiveErr := errors.New(fmt.Errorf("diskmanager: failed to parse policy CSV file: %w", err)).
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
			descriptiveErr := errors.New(fmt.Errorf("diskmanager: invalid policy CSV format at line %d: expected 2 fields, got %d", i+1, len(record))).
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

// GetAudioFiles returns a list of audio files in the directory and its subdirectories
func GetAudioFiles(baseDir string, allowedExts []string, db Interface, debug bool) ([]FileInfo, error) {
	// Fast path: empty allowed extensions
	if len(allowedExts) == 0 {
		return nil, nil
	}

	// Use pooled slice to reduce allocations
	filesPtr := getFileInfoSlice()
	shouldReturnToPool := true
	defer func() {
		if shouldReturnToPool {
			putFileInfoSlice(filesPtr)
		}
	}()

	// Work directly with the pooled slice
	files := (*filesPtr)[:0:cap(*filesPtr)]

	var parseErrorCount int
	var firstParseError error
	const maxParseErrors = 100 // Limit error tracking to prevent memory bloat

	// Get list of protected clips from database
	lockedClips, err := getLockedClips(db)
	if err != nil {
		descriptiveErr := errors.New(fmt.Errorf("diskmanager: failed to get locked clips from database: %w", err)).
			Component("diskmanager").
			Category(errors.CategoryDatabase).
			Context("operation", "get_locked_clips").
			Build()
		return nil, descriptiveErr
	}

	if debug {
		log.Printf("Found %d protected clips", len(lockedClips))
	}

	err = filepath.Walk(baseDir, func(path string, info os.FileInfo, err error) error {
		if err != nil {
			return err
		}
		if !info.IsDir() {
			fileName := info.Name()

			// Skip temporary files that are currently being written.
			// Audio files are written with .temp suffix during recording
			// and renamed upon completion to ensure atomic file operations.
			// Using case-insensitive check to handle edge cases.
			// Fast path: skip temp files early
			if strings.HasSuffix(strings.ToLower(fileName), tempFileExt) {
				return nil
			}

			// Fast path: check extension before expensive operations
			ext := filepath.Ext(fileName)
			if !contains(allowedExts, ext) {
				return nil
			}

			// Process valid file
			fileInfo, err := parseFileInfo(path, info)
			if err != nil {
				// Track first error and count without storing all errors
				if firstParseError == nil {
					firstParseError = err
				}
				parseErrorCount++
				if debug && parseErrorCount <= maxParseErrors {
					log.Printf("Error parsing file %s: %v", path, err)
				}
				return nil // Continue with next file
			}
			// Check if the file is protected
			fileInfo.Locked = isLockedClip(fileInfo.Path, lockedClips)
			files = append(files, fileInfo)
		}

		// Yield to other goroutines
		runtime.Gosched()

		return nil
	})

	if err != nil {
		descriptiveErr := errors.New(fmt.Errorf("diskmanager: failed to walk directory for audio files: %w", err)).
			Component("diskmanager").
			Category(errors.CategoryFileIO).
			Context("operation", "walk_directory").
			Context("base_dir", baseDir).
			Build()
		return nil, descriptiveErr
	}

	// If we encountered parse errors but still have some valid files, log a summary but continue
	if parseErrorCount > 0 {
		if debug {
			log.Printf("Encountered %d file parsing errors during cleanup", parseErrorCount)
			if parseErrorCount > maxParseErrors {
				log.Printf("Only first %d errors were logged", maxParseErrors)
			}
		}
		// If we have no valid files at all, return an error
		if len(files) == 0 && firstParseError != nil {
			descriptiveErr := errors.New(fmt.Errorf("diskmanager: failed to parse any audio files: %w", firstParseError)).
				Component("diskmanager").
				Category(errors.CategoryFileParsing).
				Context("base_dir", baseDir).
				Context("parse_errors_count", parseErrorCount).
				Build()
			return nil, descriptiveErr
		}
	}

	// Fast path: if no files found, return early
	if len(files) == 0 {
		return nil, nil
	}

	// Create result slice with exact capacity to avoid over-allocation
	result := make([]FileInfo, len(files))
	copy(result, files)

	// Transfer ownership - don't return to pool since we made a copy
	shouldReturnToPool = true // Pool will be returned by defer

	return result, nil
}

// parseFileInfo parses the file information from the file path and os.FileInfo
func parseFileInfo(path string, info os.FileInfo) (FileInfo, error) {
	name := filepath.Base(info.Name())

	// Check if the file extension is allowed
	ext := filepath.Ext(name)
	if !contains(allowedFileTypes, ext) {
		// Lightweight error with helpful context
		return FileInfo{}, fmt.Errorf("file type not eligible: %s (allowed: %v)", ext, allowedFileTypes)
	}

	// Remove the extension for parsing
	nameWithoutExt := strings.TrimSuffix(name, ext)

	// Handle special case for thumbnail suffixes like _400px
	nameWithoutExt = strings.TrimSuffix(nameWithoutExt, "_400px")

	parts := strings.Split(nameWithoutExt, "_")
	if len(parts) < 3 {
		// Lightweight error with format hint
		return FileInfo{}, fmt.Errorf("invalid filename format: %s (expected: species_confidence_timestamp)", name)
	}

	// The species name might contain underscores, so we need to handle the last two parts separately
	confidenceStr := parts[len(parts)-2]
	timestampStr := parts[len(parts)-1]
	species := strings.Join(parts[:len(parts)-2], "_")

	confidence, err := strconv.Atoi(strings.TrimSuffix(confidenceStr, "p"))
	if err != nil {
		// Lightweight error with specific issue
		return FileInfo{}, fmt.Errorf("invalid confidence value '%s' in filename: %s", confidenceStr, name)
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
			// Lightweight error with timestamp issue
			return FileInfo{}, fmt.Errorf("invalid timestamp '%s' in filename: %s", timestampStr, name)
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

	// Check common audio extensions first for better cache locality
	for _, s := range slice {
		if s == item {
			return true
		}
	}
	return false
}

// WriteSortedFilesToFile writes the sorted list of files to a text file for investigation
func WriteSortedFilesToFile(files []FileInfo, filePath string) error {
	file, err := os.Create(filePath)
	if err != nil {
		return fmt.Errorf("failed to create file: %w", err)
	}
	defer func() {
		if err := file.Close(); err != nil {
			log.Printf("Failed to close file: %v", err)
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

	log.Printf("Sorted files have been written to %s", filePath)
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
