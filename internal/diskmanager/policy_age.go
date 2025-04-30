// policy_age.go - code for age retention policy
package diskmanager

import (
	"fmt"
	"log"
	"runtime"
	"sort"
	"strings"
	"time"

	"github.com/tphakala/birdnet-go/internal/conf"
)

// formatTimeForHumans converts a Unix timestamp to a human-readable string format.
// It returns both a formatted date/time string in local timezone and a human-readable duration.
// Uses the system's local timezone for display consistency with file timestamps.
func formatTimeForHumans(unixTime int64) (formattedTime, ageDuration string) {
	// Convert Unix timestamp to time.Time in local timezone
	t := time.Unix(unixTime, 0)

	// Format the time with timezone info (local timezone)
	formattedTime = t.Format("2006-01-02 15:04:05 MST")

	// Calculate and format duration since now
	// Using local time for both so the duration is accurate
	durSeconds := time.Now().Unix() - unixTime
	duration := time.Duration(durSeconds) * time.Second

	// Format the duration in a human-readable form
	ageDuration = formatDuration(duration)

	return formattedTime, ageDuration
}

// formatDuration formats a duration in a more human-readable form than the standard duration string.
// It handles negative durations (for future dates) and breaks down time into days, hours, minutes, and seconds.
// Returns a string like "2d 5h 3m 42s" or "-1h 20m 30s" for negative durations.
func formatDuration(d time.Duration) string {
	if d < 0 {
		return fmt.Sprintf("-%s", formatDuration(-d))
	}

	seconds := int64(d.Seconds())

	if seconds < 60 {
		return fmt.Sprintf("%ds", seconds)
	}

	minutes := seconds / 60
	seconds %= 60

	if minutes < 60 {
		return fmt.Sprintf("%dm %ds", minutes, seconds)
	}

	hours := minutes / 60
	minutes %= 60

	if hours < 24 {
		return fmt.Sprintf("%dh %dm %ds", hours, minutes, seconds)
	}

	days := hours / 24
	hours %= 24

	return fmt.Sprintf("%dd %dh %dm %ds", days, hours, minutes, seconds)
}

// AgeBasedCleanup removes clips from the filesystem based on their age and the number of clips per species.
// Age-based policy retains files that are newer than the specified retention period, while also
// ensuring the minimum clips per species constraint is respected.
// All time calculations use system local time to match file timestamps.
//
// IMPORTANT: File timestamps (including those in the filename with 'Z' suffix) are in local system time,
// despite the 'Z' suffix normally indicating UTC/Zulu time. All time comparisons are done in local time.
//
// Returns a CleanupResult containing error, number of clips removed, and current disk utilization percentage.
func AgeBasedCleanup(quit <-chan struct{}, db Interface) CleanupResult {
	// Log the start of the cleanup run with structured logger
	serviceLogger.Info("Age-based cleanup run started",
		"policy", "age",
		"timestamp", time.Now().Format(time.RFC3339))

	// Perform initial setup (get files, settings, check if proceed)
	files, baseDir, retention, proceed, initialResult := prepareInitialCleanup(db)
	if !proceed {
		serviceLogger.Info("Age-based cleanup run completed",
			"policy", "age",
			"result", "no action needed",
			"files_removed", 0,
			"disk_utilization", initialResult.DiskUtilization,
			"timestamp", time.Now().Format(time.RFC3339),
			"duration_ms", 0)
		return initialResult
	}

	startTime := time.Now() // Track cleanup duration
	debug := retention.Debug
	keepSpectrograms := retention.KeepSpectrograms
	minClipsPerSpecies := retention.MinClips
	retentionPeriodSetting := retention.MaxAge

	// Sanitize the input string first
	retentionPeriodTrimmed := strings.TrimSpace(retentionPeriodSetting)

	// Convert string retention period setting to hours
	// e.g., "48h", "30d", "2w", or "3m" into actual hours
	retentionPeriodInHours, err := conf.ParseRetentionPeriod(retentionPeriodTrimmed)
	if err != nil {
		log.Printf("Invalid retention period '%s': %s\n", retentionPeriodSetting, err)
		// Try to get current disk usage for the result
		currentUsage, diskErr := GetDiskUsage(baseDir)
		utilization := 0
		if diskErr == nil {
			utilization = int(currentUsage)
		}
		// Log the original, untrimmed setting in the error message
		return CleanupResult{Err: fmt.Errorf("invalid retention period '%s': %w", retentionPeriodSetting, err), ClipsRemoved: 0, DiskUtilization: utilization}
	}

	if debug {
		log.Printf("Starting age-based cleanup. Base directory: %s, Retention period: %s (%d hours)", baseDir, retentionPeriodSetting, retentionPeriodInHours)
		log.Printf("Note: File timestamps (including in filenames with 'Z' suffix like '20250429T160252Z.wav') are in local time, not UTC")
	}

	// Create a map to keep track of the *total* number of files per species
	// This is used to enforce the minimum clips per species constraint
	speciesTotalCount := buildSpeciesTotalCountMap(files)

	// Sort files: Oldest first, then lowest confidence first as a tie-breaker
	// This ensures we delete the oldest, least confident files first
	sort.SliceStable(files, func(i, j int) bool {
		// Use Unix timestamps for consistent comparison
		if files[i].Timestamp.Unix() != files[j].Timestamp.Unix() {
			return files[i].Timestamp.Unix() < files[j].Timestamp.Unix() // Oldest first
		}
		return files[i].Confidence < files[j].Confidence // Lowest confidence first
	})

	// Calculate the expiration time (oldest files that should be kept) in local time
	// Any file with a timestamp BEFORE this cutoff is considered for deletion
	// Files newer than this cutoff will be preserved regardless of other factors
	retentionCutoff := time.Now().Add(-time.Duration(retentionPeriodInHours) * time.Hour)
	retentionCutoffUnix := retentionCutoff.Unix()

	if debug {
		cutoffFormatted, cutoffAge := formatTimeForHumans(retentionCutoffUnix)
		log.Printf("Files older than %s (%s ago) will be considered for deletion", cutoffFormatted, cutoffAge)
		log.Printf("Current system time: %s, Timezone: %s", time.Now().Format("2006-01-02 15:04:05 MST"), time.Now().Format("MST"))
	}

	// Max deletions per run to prevent excessive I/O impact in a single run
	maxDeletions := 1000

	// Call the helper function to process files
	deletedCount, loopErr := processAgeBasedDeletionLoop(files, speciesTotalCount,
		minClipsPerSpecies, maxDeletions, debug, keepSpectrograms,
		quit, retentionCutoffUnix)

	// Get final disk utilization
	diskUsage, diskErr := GetDiskUsage(baseDir)
	if diskErr != nil {
		// Combine errors if getting disk usage failed after the loop
		finalErr := fmt.Errorf("cleanup completed but failed to get disk usage: %w (loop error: %w)", diskErr, loopErr)

		// Log the completion with error
		duration := time.Since(startTime)
		serviceLogger.Error("Age-based cleanup run completed with errors",
			"policy", "age",
			"files_removed", deletedCount,
			"disk_utilization", 0,
			"error", finalErr,
			"timestamp", time.Now().Format(time.RFC3339),
			"duration_ms", duration.Milliseconds())

		return CleanupResult{Err: finalErr, ClipsRemoved: deletedCount, DiskUtilization: 0}
	}

	// Log the successful completion
	duration := time.Since(startTime)
	serviceLogger.Info("Age-based cleanup run completed",
		"policy", "age",
		"files_removed", deletedCount,
		"disk_utilization", int(diskUsage),
		"timestamp", time.Now().Format(time.RFC3339),
		"duration_ms", duration.Milliseconds())

	// Return the final result, including any error encountered during the loop
	return CleanupResult{Err: loopErr, ClipsRemoved: deletedCount, DiskUtilization: int(diskUsage)}
}

// processAgeBasedDeletionLoop handles the core logic of iterating through files,
// checking age and minimum species counts, and deleting files.
// The loop continues until one of these conditions is met:
// 1. All files have been processed
// 2. Maximum deletion count is reached
// 3. A quit signal is received
func processAgeBasedDeletionLoop(files []FileInfo, speciesTotalCount map[string]int,
	minClipsPerSpecies int, maxDeletions int, debug bool, keepSpectrograms bool,
	quit <-chan struct{}, retentionCutoffUnix int64) (deletedCount int, loopErr error) {

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
			eligible, reason := isEligibleForAgeDeletion(file, retentionCutoffUnix, speciesTotalCount, minClipsPerSpecies, debug)
			if !eligible {
				// Log reason if debug enabled and not simply locked or not old enough (those are common)
				if debug && reason != "locked" && reason != "not old enough" {
					log.Printf("Skipping file %s: %s", file.Path, reason)
				}
				continue
			}

			// 2. Perform deletion using the common helper
			if delErr := deleteFileAndOptionalSpectrogram(file, reason, keepSpectrograms, debug, "age"); delErr != nil {
				// Use the common error handler
				shouldStop, loopErrTmp := handleDeletionErrorInLoop(file.Path, delErr, &errorCount, 10, "age")
				if shouldStop {
					loopErr = loopErrTmp         // Assign the final error
					return deletedCount, loopErr // Stop processing
				}
				continue // Continue with the next file
			}

			// 3. Update state *after* successful deletion
			// Important: We must update counts after deletion to maintain accurate
			// constraints for minimum clips per species
			speciesTotalCount[file.Species]-- // Decrement total count for the species
			// Prevent count from going negative (safety check)
			// This should never happen with proper bookkeeping, but serves as a safeguard
			if speciesTotalCount[file.Species] < 0 {
				speciesTotalCount[file.Species] = 0
			}
			deletedCount++

			// 4. Yield to other goroutines
			// This prevents the cleanup process from monopolizing CPU resources
			runtime.Gosched()
		}
	}

	// Loop finished normally or due to max deletions
	return deletedCount, loopErr
}

// isEligibleForAgeDeletion checks if a file meets the criteria for deletion based on age policy.
// Files are eligible for deletion when ALL of these conditions are met:
// 1. File is not locked (protected from deletion)
// 2. File is older than the retention cutoff time (using Unix timestamps in local timezone)
// 3. There are more than minClipsPerSpecies files for this species
func isEligibleForAgeDeletion(file *FileInfo, retentionCutoffUnix int64, speciesTotalCount map[string]int,
	minClipsPerSpecies int, debug bool) (eligible bool, reason string) {

	// 1. Check if locked (reuse common helper)
	if checkLocked(file, debug) {
		return false, "locked"
	}

	// 2. Check if older than retention period using Unix epochs in local timezone
	// Files older than retentionCutoff should be deleted
	// Logic: If file timestamp is NOT before the cutoff, it's too new to delete
	fileUnix := file.Timestamp.Unix()
	if fileUnix >= retentionCutoffUnix {
		if debug {
			// Get human-readable timestamp and age for file in local timezone
			fileFormatted, fileAge := formatTimeForHumans(fileUnix)

			// Get human-readable timestamp and age for cutoff in local timezone
			cutoffFormatted, cutoffAge := formatTimeForHumans(retentionCutoffUnix)

			log.Printf("[DEBUG] Skipping file (not old enough): %s (Created: %s, %s old)",
				file.Path,
				fileFormatted,
				fileAge)
			log.Printf("[DEBUG] Retention cutoff: %s (%s ago)", cutoffFormatted, cutoffAge)
		}
		return false, "not old enough"
	}

	// 3. Check minimum *total* clips constraint
	// We must maintain at least minClipsPerSpecies clips for each species
	// If current count is at or below minimum, we can't delete more
	if count, exists := speciesTotalCount[file.Species]; exists && count <= minClipsPerSpecies {
		if debug {
			// Include file timestamp in log for better context (in local timezone)
			fileFormatted, fileAge := formatTimeForHumans(fileUnix)

			log.Printf("Total clip count for %s is at or below the minimum threshold (%d). Cannot delete file: %s (Created: %s, %s old)",
				file.Species, minClipsPerSpecies, file.Path, fileFormatted, fileAge)
		}
		return false, "minimum clip count reached"
	}

	// If all checks pass, the file is eligible
	if debug {
		// Log which files are eligible for deletion with human-readable dates in local timezone
		fileFormatted, fileAge := formatTimeForHumans(fileUnix)
		log.Printf("[DEBUG] Eligible for deletion: %s (Created: %s, %s old, Species: %s)",
			file.Path, fileFormatted, fileAge, file.Species)
	}

	// If all checks pass, the file is eligible
	return true, "older than retention period and minimum count allows"
}
