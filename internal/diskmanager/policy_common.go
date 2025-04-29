// policy_common.go - shared code for cleanup policies
package diskmanager

import (
	"fmt"
	"log"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/tphakala/birdnet-go/internal/conf"
)

// buildSpeciesSubDirCountMap creates a map to track the number of files per species per subdirectory.
func buildSpeciesSubDirCountMap(files []FileInfo) map[string]map[string]int {
	speciesCount := make(map[string]map[string]int)
	for _, file := range files {
		subDir := filepath.Dir(file.Path)
		if _, exists := speciesCount[file.Species]; !exists {
			speciesCount[file.Species] = make(map[string]int)
		}
		speciesCount[file.Species][subDir]++
	}
	return speciesCount
}

// buildSpeciesTotalCountMap creates a map to track the total number of files per species across all subdirectories.
func buildSpeciesTotalCountMap(files []FileInfo) map[string]int {
	speciesTotalCount := make(map[string]int)
	for _, file := range files {
		speciesTotalCount[file.Species]++
	}
	return speciesTotalCount
}

// checkLocked checks if a file should be skipped because it's locked.
func checkLocked(file *FileInfo, debug bool) bool {
	if file.Locked {
		if debug {
			log.Printf("Skipping locked file: %s", file.Path)
		}
		return true // Indicates the file should be skipped
	}
	return false // Indicates the file should NOT be skipped
}

// checkMinClips checks if a file can be deleted based on the minimum clips per species constraint.
// Returns true if deletion is allowed, false otherwise.
func checkMinClips(file *FileInfo, subDir string, speciesCount map[string]map[string]int,
	minClipsPerSpecies int, debug bool) bool {

	// Ensure the species and subdirectory exist in the map
	if speciesMap, ok := speciesCount[file.Species]; ok {
		if count, ok := speciesMap[subDir]; ok {
			if count <= minClipsPerSpecies {
				if debug {
					log.Printf("Species clip count for %s in %s is at the minimum threshold (%d). Skipping file deletion.",
						file.Species, subDir, minClipsPerSpecies)
				}
				return false // Cannot delete
			}
		} else {
			// Should not happen if map is built correctly, but handle defensively
			if debug {
				log.Printf("Warning: Subdirectory %s not found in species count map for species %s.", subDir, file.Species)
			}
			return false // Cannot determine count, safer not to delete
		}
	} else {
		// Should not happen if map is built correctly, but handle defensively
		if debug {
			log.Printf("Warning: Species %s not found in species count map.", file.Species)
		}
		return false // Cannot determine count, safer not to delete
	}

	return true // Can delete
}

// deleteAudioFile removes a file from the filesystem and logs the action.
func deleteAudioFile(file *FileInfo, debug bool) error {
	err := os.Remove(file.Path)
	if err != nil {
		// Error logging happens in the calling function
		return err
	}

	if debug {
		log.Printf("File %s deleted", file.Path)
	}

	return nil
}

// deleteFileAndOptionalSpectrogram handles the deletion of the audio file
// and its associated spectrogram if required, including throttling and logging.
func deleteFileAndOptionalSpectrogram(file *FileInfo, reason string, keepSpectrograms, debug bool) error {
	// Throttle slightly before deletion
	time.Sleep(100 * time.Millisecond)

	// Log intent before deleting
	if debug {
		log.Printf("Deleting file based on policy (%s): %s (Size: %d)", reason, file.Path, file.Size)
	}

	// Delete the audio file (reuse common helper)
	if err := deleteAudioFile(file, debug); err != nil {
		// Error logging happens in the calling function
		return err // Return the error to be handled by the caller
	}

	// Optionally delete associated spectrogram PNG file
	if !keepSpectrograms {
		basePath := strings.TrimSuffix(file.Path, filepath.Ext(file.Path))
		pngPathLower := basePath + ".png"
		pngPathUpper := basePath + ".PNG"

		// Attempt to remove lowercase variant
		if pngErrLower := os.Remove(pngPathLower); pngErrLower != nil {
			// Log error only if it's not a "not exist" error (and debug is on)
			// These errors are considered non-critical as spectrograms are optional.
			if debug && !os.IsNotExist(pngErrLower) {
				log.Printf("Warning: Failed to remove associated spectrogram %s: %v", pngPathLower, pngErrLower)
			}
		} else if debug {
			log.Printf("Deleted associated spectrogram %s", pngPathLower)
		}

		// Attempt to remove uppercase variant (handles cases like .WAV -> .PNG)
		// No need to log success again if lowercase already succeeded and deleted the file on case-insensitive FS.
		if pngErrUpper := os.Remove(pngPathUpper); pngErrUpper != nil {
			// Log error only if it's not a "not exist" error (and debug is on)
			// These errors are considered non-critical as spectrograms are optional.
			if debug && !os.IsNotExist(pngErrUpper) {
				log.Printf("Warning: Failed to remove associated spectrogram %s: %v", pngPathUpper, pngErrUpper)
			}
		} else if debug && os.IsNotExist(os.Remove(pngPathLower)) { // Log success only if lowercase didn't already remove it
			log.Printf("Deleted associated spectrogram %s", pngPathUpper)
		}
	}

	return nil // Deletion successful
}

// handleDeletionErrorInLoop manages error counting and logging for deletion errors within processing loops.
// It returns true if the loop should stop due to too many errors, along with the formatted error.
func handleDeletionErrorInLoop(filePath string, delErr error, errorCount *int, maxErrors int) (shouldStop bool, loopErr error) {
	*errorCount++ // Increment the error count via pointer
	log.Printf("Failed to remove %s: %s\n", filePath, delErr)
	if *errorCount > maxErrors {
		loopErr = fmt.Errorf("too many errors (%d) during cleanup, last error: %w", *errorCount, delErr)
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

	files, err := GetAudioFiles(baseDir, allowedFileTypes, db, debug)
	if err != nil {
		// Try to get current disk usage for the result even if file listing failed
		currentUsage, diskErr := GetDiskUsage(baseDir)
		utilization := 0
		if diskErr == nil {
			utilization = int(currentUsage)
		}
		result = CleanupResult{Err: fmt.Errorf("failed to get audio files for cleanup: %w", err), ClipsRemoved: 0, DiskUtilization: utilization}
		return nil, baseDir, retention, false, result
	}

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
		return nil, baseDir, retention, false, result
	}

	// If we got here, proceed with cleanup
	return files, baseDir, retention, true, CleanupResult{}
}

// CleanupResult contains the results of a cleanup operation
type CleanupResult struct {
	Err             error // Any error that occurred during cleanup
	ClipsRemoved    int   // Number of clips that were removed
	DiskUtilization int   // Current disk utilization percentage after cleanup
}
