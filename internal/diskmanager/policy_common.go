// policy_common.go - shared code for cleanup policies
package diskmanager

import (
	"fmt"
	"io"
	"log"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/tphakala/birdnet-go/internal/conf"
	"github.com/tphakala/birdnet-go/internal/logging"
)

// Package-level logger specific to diskmanager service
var (
	serviceLogger *slog.Logger
	closeLogger   func() error
)

func init() {
	var err error
	// Define log file path relative to working directory
	logFilePath := filepath.Join("logs", "diskmanager.log")

	// Initialize the service-specific file logger
	// Using Debug level for file logging to capture more detail
	serviceLogger, closeLogger, err = logging.NewFileLogger(logFilePath, "diskmanager", slog.LevelDebug)
	if err != nil {
		// Fallback: Log error to standard log and disable service logging
		log.Printf("FATAL: Failed to initialize diskmanager file logger at %s: %v. Service logging disabled.", logFilePath, err)
		// Set logger to a disabled handler to prevent nil panics
		serviceLogger = slog.New(slog.NewJSONHandler(io.Discard, nil))
		closeLogger = func() error { return nil } // No-op closer
	} else {
		// Use standard log for initial confirmation message
		log.Printf("Diskmanager service logging initialized to file: %s", logFilePath)
	}
}

// GetLogger returns the package-level logger for the diskmanager service.
func GetLogger() *slog.Logger {
	return serviceLogger
}

// buildSpeciesSubDirCountMap creates a map to track the number of files per species per subdirectory.
func buildSpeciesSubDirCountMap(files []FileInfo) map[string]map[string]int {
	serviceLogger.Debug("Building species subdirectory count map",
		"policy", "diskmanager", // Generic policy name since this function is called by both policies
		"file_count", len(files))
	speciesCount := make(map[string]map[string]int)
	for _, file := range files {
		subDir := filepath.Dir(file.Path)
		if _, exists := speciesCount[file.Species]; !exists {
			speciesCount[file.Species] = make(map[string]int)
		}
		speciesCount[file.Species][subDir]++
	}
	serviceLogger.Debug("Species subdirectory count map built",
		"policy", "diskmanager",
		"species_count", len(speciesCount))
	return speciesCount
}

// buildSpeciesTotalCountMap creates a map to track the total number of files per species across all subdirectories.
func buildSpeciesTotalCountMap(files []FileInfo) map[string]int {
	serviceLogger.Debug("Building species total count map",
		"policy", "diskmanager",
		"file_count", len(files))
	speciesTotalCount := make(map[string]int)
	for _, file := range files {
		speciesTotalCount[file.Species]++
	}
	serviceLogger.Debug("Species total count map built",
		"policy", "diskmanager",
		"species_count", len(speciesTotalCount))
	return speciesTotalCount
}

// checkLocked checks if a file should be skipped because it's locked.
func checkLocked(file *FileInfo, debug bool) bool {
	if file.Locked {
		if debug {
			log.Printf("Skipping locked file: %s", file.Path)
		}
		serviceLogger.Debug("Skipping locked file",
			"path", file.Path,
			"species", file.Species)
		return true // Indicates the file should be skipped
	}
	return false // Indicates the file should NOT be skipped
}

// checkMinClips checks if a file can be deleted based on the minimum clips per species constraint.
// Returns true if deletion is allowed, false otherwise.
func checkMinClips(file *FileInfo, subDir string, speciesCount map[string]map[string]int,
	minClipsPerSpecies int, debug bool, policy string) bool {

	// Ensure the species and subdirectory exist in the map
	if speciesMap, ok := speciesCount[file.Species]; ok {
		if count, ok := speciesMap[subDir]; ok {
			if count <= minClipsPerSpecies {
				if debug {
					log.Printf("Species clip count for %s in %s is at the minimum threshold (%d). Skipping file deletion.",
						file.Species, subDir, minClipsPerSpecies)
				}
				serviceLogger.Debug("Species count at minimum threshold, skipping deletion",
					"policy", policy,
					"species", file.Species,
					"subdirectory", subDir,
					"count", count,
					"min_threshold", minClipsPerSpecies,
					"path", file.Path)
				return false // Cannot delete
			}
		} else {
			// Should not happen if map is built correctly, but handle defensively
			if debug {
				log.Printf("Warning: Subdirectory %s not found in species count map for species %s.", subDir, file.Species)
			}
			serviceLogger.Warn("Subdirectory not found in species count map",
				"policy", policy,
				"subdirectory", subDir,
				"species", file.Species,
				"path", file.Path)
			return false // Cannot determine count, safer not to delete
		}
	} else {
		// Should not happen if map is built correctly, but handle defensively
		if debug {
			log.Printf("Warning: Species %s not found in species count map.", file.Species)
		}
		serviceLogger.Warn("Species not found in count map",
			"policy", policy,
			"species", file.Species,
			"path", file.Path)
		return false // Cannot determine count, safer not to delete
	}

	return true // Can delete
}

// deleteAudioFile removes a file from the filesystem and logs the action.
func deleteAudioFile(file *FileInfo, debug bool, policy string) error {
	serviceLogger.Info("Deleting audio file",
		"policy", policy,
		"path", file.Path,
		"size", file.Size,
		"species", file.Species)
	err := os.Remove(file.Path)
	if err != nil {
		// Error logging happens in the calling function
		serviceLogger.Error("Failed to delete audio file",
			"policy", policy,
			"path", file.Path,
			"error", err)
		return err
	}

	if debug {
		log.Printf("File %s deleted", file.Path)
	}
	serviceLogger.Info("Audio file deleted successfully",
		"policy", policy,
		"path", file.Path)

	return nil
}

// deleteFileAndOptionalSpectrogram handles the deletion of the audio file
// and its associated spectrogram if required, including throttling and logging.
func deleteFileAndOptionalSpectrogram(file *FileInfo, reason string, keepSpectrograms, debug bool, policy string) error {
	// Throttle slightly before deletion
	time.Sleep(100 * time.Millisecond)

	// Log intent before deleting
	if debug {
		log.Printf("Deleting file based on policy (%s): %s (Size: %d)", reason, file.Path, file.Size)
	}
	serviceLogger.Info("Deleting file based on policy",
		"policy", policy,
		"reason", reason,
		"path", file.Path,
		"size", file.Size,
		"species", file.Species,
		"keep_spectrograms", keepSpectrograms)

	// Delete the audio file (reuse common helper)
	if err := deleteAudioFile(file, debug, policy); err != nil {
		// Error logging happens in the calling function
		return err // Return the error to be handled by the caller
	}

	// Optionally delete associated spectrogram PNG file
	if !keepSpectrograms {
		basePath := strings.TrimSuffix(file.Path, filepath.Ext(file.Path))
		pngPathLower := basePath + ".png"
		pngPathUpper := basePath + ".PNG"

		serviceLogger.Debug("Checking for associated spectrograms",
			"policy", policy,
			"lower_case", pngPathLower,
			"upper_case", pngPathUpper)

		// Attempt to remove lowercase variant
		if pngErrLower := os.Remove(pngPathLower); pngErrLower != nil {
			// Log error only if it's not a "not exist" error (and debug is on)
			// These errors are considered non-critical as spectrograms are optional.
			if debug && !os.IsNotExist(pngErrLower) {
				log.Printf("Warning: Failed to remove associated spectrogram %s: %v", pngPathLower, pngErrLower)
			}
			if !os.IsNotExist(pngErrLower) {
				serviceLogger.Warn("Failed to remove associated spectrogram (lowercase)",
					"policy", policy,
					"path", pngPathLower,
					"error", pngErrLower)
			}
		} else if debug {
			log.Printf("Deleted associated spectrogram %s", pngPathLower)
			serviceLogger.Info("Deleted associated spectrogram (lowercase)",
				"policy", policy,
				"path", pngPathLower)
		}

		// Attempt to remove uppercase variant (handles cases like .WAV -> .PNG)
		// No need to log success again if lowercase already succeeded and deleted the file on case-insensitive FS.
		if pngErrUpper := os.Remove(pngPathUpper); pngErrUpper != nil {
			// Log error only if it's not a "not exist" error (and debug is on)
			// These errors are considered non-critical as spectrograms are optional.
			if debug && !os.IsNotExist(pngErrUpper) {
				log.Printf("Warning: Failed to remove associated spectrogram %s: %v", pngPathUpper, pngErrUpper)
			}
			if !os.IsNotExist(pngErrUpper) {
				serviceLogger.Warn("Failed to remove associated spectrogram (uppercase)",
					"policy", policy,
					"path", pngPathUpper,
					"error", pngErrUpper)
			}
		} else if debug {
			// Check if the lowercase file still exists (without deleting it again)
			// Log success for uppercase only if lowercase wasn't successfully removed.
			if _, statErr := os.Stat(pngPathLower); os.IsNotExist(statErr) {
				log.Printf("Deleted associated spectrogram %s", pngPathUpper)
				serviceLogger.Info("Deleted associated spectrogram (uppercase)",
					"policy", policy,
					"path", pngPathUpper)
			}
		}
	}

	serviceLogger.Info("File deletion completed",
		"policy", policy,
		"path", file.Path,
		"reason", reason)
	return nil // Deletion successful
}

// handleDeletionErrorInLoop manages error counting and logging for deletion errors within processing loops.
// It returns true if the loop should stop due to too many errors, along with the formatted error.
func handleDeletionErrorInLoop(filePath string, delErr error, errorCount *int, maxErrors int, policy string) (shouldStop bool, loopErr error) {
	*errorCount++ // Increment the error count via pointer
	log.Printf("Failed to remove %s: %s\n", filePath, delErr)
	serviceLogger.Error("Failed to remove file during cleanup loop",
		"policy", policy,
		"path", filePath,
		"error", delErr,
		"error_count", *errorCount,
		"max_errors", maxErrors)

	if *errorCount > maxErrors {
		loopErr = fmt.Errorf("too many errors (%d) during cleanup, last error: %w", *errorCount, delErr)
		serviceLogger.Error("Cleanup loop stopping due to too many errors",
			"policy", policy,
			"error_count", *errorCount,
			"max_errors", maxErrors,
			"last_error", delErr)
		return true, loopErr // Stop processing
	}
	return false, nil // Continue processing
}

// prepareInitialCleanup fetches settings, audio files, and performs initial checks.
// It returns the files, base directory, retention settings, and a boolean indicating if cleanup should proceed.
// If proceed is false, it also returns a completed CleanupResult.
func prepareInitialCleanup(db Interface) (files []FileInfo, baseDir string, retention conf.RetentionSettings, proceed bool, result CleanupResult) {
	settings := conf.Setting()
	debug := settings.Realtime.Audio.Export.Retention.Debug
	baseDir = settings.Realtime.Audio.Export.Path
	retention = settings.Realtime.Audio.Export.Retention // Return the whole retention struct

	serviceLogger.Info("Preparing initial cleanup",
		"base_dir", baseDir,
		"policy", retention.Policy,
		"debug", debug)

	files, err := GetAudioFiles(baseDir, allowedFileTypes, db, debug)
	if err != nil {
		// Try to get current disk usage for the result even if file listing failed
		currentUsage, diskErr := GetDiskUsage(baseDir)
		utilization := 0
		if diskErr == nil {
			utilization = int(currentUsage)
		}
		result = CleanupResult{Err: fmt.Errorf("failed to get audio files for cleanup: %w", err), ClipsRemoved: 0, DiskUtilization: utilization}

		serviceLogger.Error("Failed to get audio files for cleanup",
			"policy", retention.Policy,
			"base_dir", baseDir,
			"error", err,
			"disk_utilization", utilization)
		return nil, baseDir, retention, false, result
	}

	serviceLogger.Info("Retrieved audio files for cleanup consideration",
		"policy", retention.Policy,
		"file_count", len(files),
		"base_dir", baseDir)

	if len(files) == 0 {
		if debug {
			log.Printf("No eligible audio files found for cleanup in %s", baseDir)
		}
		// Get current disk utilization even if no files were processed
		currentUsage, diskErr := GetDiskUsage(baseDir)
		utilization := 0
		if diskErr == nil {
			utilization = int(currentUsage)
		}
		result = CleanupResult{Err: nil, ClipsRemoved: 0, DiskUtilization: utilization}

		serviceLogger.Info("No eligible audio files found for cleanup",
			"policy", retention.Policy,
			"base_dir", baseDir,
			"disk_utilization", utilization)
		return nil, baseDir, retention, false, result
	}

	// If we got here, proceed with cleanup
	serviceLogger.Info("Proceeding with cleanup process",
		"policy", retention.Policy,
		"file_count", len(files),
		"base_dir", baseDir)
	return files, baseDir, retention, true, CleanupResult{}
}

// CleanupResult contains the results of a cleanup operation
type CleanupResult struct {
	Err             error // Any error that occurred during cleanup
	ClipsRemoved    int   // Number of clips that were removed
	DiskUtilization int   // Current disk utilization percentage after cleanup
}

// CloseLogger closes the diskmanager service logger
func CloseLogger() error {
	if closeLogger != nil {
		serviceLogger.Debug("Closing diskmanager log file",
			"policy", "diskmanager")
		err := closeLogger()
		closeLogger = nil // Prevent multiple closes
		return err
	}
	return nil
}
