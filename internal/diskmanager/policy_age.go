// policy_age.go - code for age retention policy
package diskmanager

import (
	"fmt"
	"log"
	"runtime"
	"sort"
	"time"

	"github.com/tphakala/birdnet-go/internal/conf"
)

// AgeBasedCleanup removes clips from the filesystem based on their age and the number of clips per species.
// Returns a CleanupResult containing error, number of clips removed, and current disk utilization percentage.
func AgeBasedCleanup(quit <-chan struct{}, db Interface) CleanupResult {
	// Perform initial setup (get files, settings, check if proceed)
	files, baseDir, retention, proceed, initialResult := prepareInitialCleanup(db)
	if !proceed {
		return initialResult
	}
	debug := retention.Debug
	keepSpectrograms := retention.KeepSpectrograms
	minClipsPerSpecies := retention.MinClips
	retentionPeriodSetting := retention.MaxAge

	retentionPeriodInHours, err := conf.ParseRetentionPeriod(retentionPeriodSetting)
	if err != nil {
		log.Printf("Invalid retention period '%s': %s\n", retentionPeriodSetting, err)
		// Try to get current disk usage for the result
		currentUsage, diskErr := GetDiskUsage(baseDir)
		utilization := 0
		if diskErr == nil {
			utilization = int(currentUsage)
		}
		return CleanupResult{Err: fmt.Errorf("invalid retention period '%s': %w", retentionPeriodSetting, err), ClipsRemoved: 0, DiskUtilization: utilization}
	}

	if debug {
		log.Printf("Starting age-based cleanup. Base directory: %s, Retention period: %s (%d hours)", baseDir, retentionPeriodSetting, retentionPeriodInHours)
	}

	// Create a map to keep track of the *total* number of files per species
	speciesTotalCount := buildSpeciesTotalCountMap(files)

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

			// 2. Perform deletion using the common helper
			if delErr := deleteFileAndOptionalSpectrogram(file, reason, keepSpectrograms, debug); delErr != nil {
				// Use the common error handler
				shouldStop, loopErrTmp := handleDeletionErrorInLoop(file.Path, delErr, &errorCount, 10)
				if shouldStop {
					loopErr = loopErrTmp         // Assign the final error
					return deletedCount, loopErr // Stop processing
				}
				continue // Continue with the next file
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
