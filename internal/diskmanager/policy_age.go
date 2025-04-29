// policy_age.go - code for age retention policy
package diskmanager

import (
	"fmt"
	"log"
	"os"
	"path/filepath"
	"runtime"
	"sort"
	"strings"
	"time"

	"github.com/tphakala/birdnet-go/internal/conf"
)

// AgeBasedCleanup removes clips from the filesystem based on their age and the number of clips per species.
// Returns a CleanupResult containing error, number of clips removed, and current disk utilization percentage.
func AgeBasedCleanup(quit <-chan struct{}, db Interface) CleanupResult { // Use common result type
	settings := conf.Setting()

	debug := settings.Realtime.Audio.Export.Retention.Debug
	keepSpectrograms := settings.Realtime.Audio.Export.Retention.KeepSpectrograms // Get the setting
	baseDir := settings.Realtime.Audio.Export.Path
	minClipsPerSpecies := settings.Realtime.Audio.Export.Retention.MinClips
	retentionPeriod := settings.Realtime.Audio.Export.Retention.MaxAge

	retentionPeriodInHours, err := conf.ParseRetentionPeriod(retentionPeriod)
	if err != nil {
		log.Printf("Invalid retention period: %s\n", err)
		return CleanupResult{Err: err, ClipsRemoved: 0, DiskUtilization: 0} // Use common result type
	}

	if debug {
		log.Printf("Starting age-based cleanup process. Base directory: %s, Retention period: %s", baseDir, retentionPeriod)
	}

	// Get the list of audio files, limited to allowed file types defined in file_utils.go
	files, err := GetAudioFiles(baseDir, allowedFileTypes, db, debug)
	if err != nil {
		return CleanupResult{Err: fmt.Errorf("failed to get audio files for age-based cleanup: %w", err), ClipsRemoved: 0, DiskUtilization: 0} // Use common result type
	}

	if len(files) == 0 {
		if debug {
			log.Printf("No eligible audio files found for cleanup in %s", baseDir)
		}

		// Get current disk utilization even if no files were processed
		diskUsage, err := GetDiskUsage(baseDir)
		if err != nil {
			return CleanupResult{Err: fmt.Errorf("failed to get disk usage: %w", err), ClipsRemoved: 0, DiskUtilization: 0} // Use common result type
		}

		return CleanupResult{Err: nil, ClipsRemoved: 0, DiskUtilization: int(diskUsage)} // Use common result type
	}

	// Create a map to keep track of the *total* number of files per species
	speciesTotalCount := buildSpeciesTotalCountMap(files) // Use the new function

	// Sort files: Oldest first, then lowest confidence first as a tie-breaker
	sort.SliceStable(files, func(i, j int) bool {
		if !files[i].Timestamp.Equal(files[j].Timestamp) {
			return files[i].Timestamp.Before(files[j].Timestamp) // Oldest first
		}
		return files[i].Confidence < files[j].Confidence // Lowest confidence first
	})

	// Calculate the expiration time for age-based cleanup
	expirationTime := time.Now().Add(-time.Duration(retentionPeriodInHours) * time.Hour)

	// Max deletions per run
	maxDeletions := 1000

	// Call the helper function to process files
	deletedCount, loopErr := processAgeBasedDeletionLoop(files, speciesTotalCount,
		minClipsPerSpecies, maxDeletions, debug, keepSpectrograms,
		quit, expirationTime)

	// Get final disk utilization
	diskUsage, diskErr := GetDiskUsage(baseDir)
	if diskErr != nil {
		// Combine errors if getting disk usage failed after the loop
		finalErr := fmt.Errorf("cleanup completed but failed to get disk usage: %w (loop error: %w)", diskErr, loopErr)
		return CleanupResult{Err: finalErr, ClipsRemoved: deletedCount, DiskUtilization: 0}
	}

	// Return the final result, including any error encountered during the loop
	return CleanupResult{Err: loopErr, ClipsRemoved: deletedCount, DiskUtilization: int(diskUsage)}
}

// processAgeBasedDeletionLoop handles the core logic of iterating through files,
// checking age and minimum species counts, and deleting files.
func processAgeBasedDeletionLoop(files []FileInfo, speciesTotalCount map[string]int,
	minClipsPerSpecies int, maxDeletions int, debug bool, keepSpectrograms bool,
	quit <-chan struct{}, expirationTime time.Time) (deletedCount int, loopErr error) {

	deletedCount = 0
	errorCount := 0

	for i := range files {
		select {
		case <-quit:
			log.Printf("Age-based cleanup loop interrupted by quit signal\n")
			return deletedCount, nil // Indicate interruption, but not necessarily an error from the loop itself
		default:
			// Check if max deletions reached
			if deletedCount >= maxDeletions {
				if debug {
					log.Printf("Reached maximum number of deletions (%d) for age-based cleanup.", maxDeletions)
				}
				return deletedCount, nil // Max deletions reached is not an error
			}

			file := &files[i] // Use pointer

			// 1. Check eligibility using the helper function
			eligible, reason := isEligibleForAgeDeletion(file, expirationTime, speciesTotalCount, minClipsPerSpecies, debug)
			if !eligible {
				// Log reason if debug enabled and not simply locked or not old enough (those are common)
				if debug && reason != "locked" && reason != "not old enough" {
					log.Printf("Skipping file %s: %s", file.Path, reason)
				}
				continue
			}

			// 2. Perform deletion using the helper function
			if delErr := deleteFileAndOptionalSpectrogram(file, reason, keepSpectrograms, debug); delErr != nil {
				errorCount++
				log.Printf("Failed to remove %s: %s\n", file.Path, delErr) // Log error here
				if errorCount > 10 {
					// Assign error to be returned by the function
					loopErr = fmt.Errorf("too many errors (%d) during age-based cleanup, last error: %w", errorCount, delErr)
					return deletedCount, loopErr // Stop processing after too many errors
				}
				continue // Continue with the next file even if one fails
			}

			// 3. Update state *after* successful deletion
			speciesTotalCount[file.Species]-- // Decrement total count for the species
			deletedCount++

			// 4. Yield to other goroutines
			runtime.Gosched()
		}
	}

	// Loop finished normally or due to max deletions
	return deletedCount, loopErr
}

// isEligibleForAgeDeletion checks if a file meets the criteria for deletion based on age policy.
func isEligibleForAgeDeletion(file *FileInfo, expirationTime time.Time, speciesTotalCount map[string]int,
	minClipsPerSpecies int, debug bool) (eligible bool, reason string) {

	// 1. Check if locked (reuse common helper)
	if checkLocked(file, debug) {
		return false, "locked"
	}

	// 2. Check if older than retention period
	if !file.Timestamp.Before(expirationTime) {
		return false, "not old enough"
	}

	// 3. Check minimum *total* clips constraint
	if count, exists := speciesTotalCount[file.Species]; exists && count <= minClipsPerSpecies {
		if debug {
			log.Printf("Total clip count for %s is at or below the minimum threshold (%d). Cannot delete older file: %s",
				file.Species, minClipsPerSpecies, file.Path)
		}
		return false, "minimum clip count reached"
	}

	// If all checks pass, the file is eligible
	return true, "older than retention period and minimum count allows"
}

// deleteFileAndOptionalSpectrogram handles the deletion of the audio file
// and its associated spectrogram if required.
func deleteFileAndOptionalSpectrogram(file *FileInfo, reason string, keepSpectrograms, debug bool) error {
	// Throttle slightly before deletion
	time.Sleep(100 * time.Millisecond)

	// Log intent before deleting
	if debug {
		log.Printf("Deleting file based on age policy (%s): %s", reason, file.Path)
	}

	// Delete the audio file (reuse common helper)
	if err := deleteAudioFile(file, debug); err != nil {
		// Error logging happens in the calling function (processAgeBasedDeletionLoop)
		return err // Return the error to be handled by the caller
	}

	// Optionally delete associated spectrogram PNG file
	if !keepSpectrograms {
		pngPath := strings.TrimSuffix(file.Path, filepath.Ext(file.Path)) + ".png"
		if pngErr := os.Remove(pngPath); pngErr != nil {
			// Log if the PNG deletion fails, but don't treat as critical error for the main deletion process
			if debug && !os.IsNotExist(pngErr) {
				log.Printf("Warning: Failed to remove associated spectrogram %s: %v", pngPath, pngErr)
			}
		} else if debug {
			log.Printf("Deleted associated spectrogram %s", pngPath)
		}
	}

	return nil // Deletion successful
}
