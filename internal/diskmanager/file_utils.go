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
	var files []FileInfo
	var parseErrors []string

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
			ext := filepath.Ext(info.Name())
			if contains(allowedExts, ext) {
				fileInfo, err := parseFileInfo(path, info)
				if err != nil {
					// Log the error but continue processing other files
					errMsg := fmt.Sprintf("Error parsing file %s: %v", path, err)
					parseErrors = append(parseErrors, errMsg)
					if debug {
						log.Println(errMsg)
					}
					return nil // Continue with next file
				}
				// Check if the file is protected
				fileInfo.Locked = isLockedClip(fileInfo.Path, lockedClips)
				files = append(files, fileInfo)
			}
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
	if len(parseErrors) > 0 {
		if debug {
			log.Printf("Encountered %d file parsing errors during cleanup", len(parseErrors))
		}
		// If we have no valid files at all, return an error
		if len(files) == 0 {
			descriptiveErr := errors.New(fmt.Errorf("diskmanager: failed to parse any audio files: %s", parseErrors[0])).
				Component("diskmanager").
				Category(errors.CategoryFileParsing).
				Context("base_dir", baseDir).
				Context("parse_errors_count", len(parseErrors)).
				Build()
			return nil, descriptiveErr
		}
	}

	return files, nil
}

// parseFileInfo parses the file information from the file path and os.FileInfo
func parseFileInfo(path string, info os.FileInfo) (FileInfo, error) {
	name := filepath.Base(info.Name())

	// Check if the file extension is allowed
	ext := filepath.Ext(name)
	if !contains(allowedFileTypes, ext) {
		descriptiveErr := errors.New(fmt.Errorf("diskmanager: file type not eligible for cleanup: %s", ext)).
			Component("diskmanager").
			Category(errors.CategoryFileParsing).
			FileContext(path, info.Size()).
			Context("file_extension", ext).
			Build()
		return FileInfo{}, descriptiveErr
	}

	// Remove the extension for parsing
	nameWithoutExt := strings.TrimSuffix(name, ext)

	// Handle special case for thumbnail suffixes like _400px
	nameWithoutExt = strings.TrimSuffix(nameWithoutExt, "_400px")

	parts := strings.Split(nameWithoutExt, "_")
	if len(parts) < 3 {
		descriptiveErr := errors.New(fmt.Errorf("diskmanager: invalid audio filename format '%s' (has %d parts, expected at least 3)", name, len(parts))).
			Component("diskmanager").
			Category(errors.CategoryFileParsing).
			FileContext(path, info.Size()).
			Context("parts_count", len(parts)).
			Context("filename", name).
			Build()
		return FileInfo{}, descriptiveErr
	}

	// The species name might contain underscores, so we need to handle the last two parts separately
	confidenceStr := parts[len(parts)-2]
	timestampStr := parts[len(parts)-1]
	species := strings.Join(parts[:len(parts)-2], "_")

	confidence, err := strconv.Atoi(strings.TrimSuffix(confidenceStr, "p"))
	if err != nil {
		descriptiveErr := errors.New(fmt.Errorf("diskmanager: failed to parse confidence from filename '%s': %w", name, err)).
			Component("diskmanager").
			Category(errors.CategoryFileParsing).
			FileContext(path, info.Size()).
			Context("confidence_string", confidenceStr).
			Context("filename", name).
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
			descriptiveErr := errors.New(fmt.Errorf("diskmanager: failed to parse timestamp from filename '%s': %w", name, err)).
				Component("diskmanager").
				Category(errors.CategoryFileParsing).
				FileContext(path, info.Size()).
				Context("timestamp_string", timestampStr).
				Context("filename", name).
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
func contains(slice []string, item string) bool {
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
