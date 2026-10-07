// policy_age.go - code for age retention policy
package diskmanager

import (
	"fmt"
	"runtime"
	"sort"
	"strings"
	"time"

	"github.com/tphakala/birdnet-go/internal/conf"
	"github.com/tphakala/birdnet-go/internal/errors"
	"github.com/tphakala/birdnet-go/internal/logger"
)

// Skip/eligibility reasons for age-based cleanup. Named constants avoid
// stringly-typed duplication between isEligibleForAgeDeletion (which decides
// and logs the reason) and processAgeBasedDeletionLoop (which tallies the
// per-run cleanupStats summary from that same reason).
const (
	ageReasonLocked       = "locked"
	ageReasonNotOldEnough = "not old enough"
	ageReasonMinClips     = "minimum clip count reached"
	ageReasonEligible     = "older than retention period and minimum count allows"
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
// Deletion is paced and done in batches, and one run ends after at most maxCleanupRunDuration.
//
// Returns a CleanupResult containing error, number of clips removed, current disk utilization percentage,
// and whether a follow-up run should start soon (MoreWork).
func AgeBasedCleanup(quit <-chan struct{}, db Interface) CleanupResult {
	// Log the start of the cleanup run with structured logger
	GetLogger().Info("Age-based cleanup run started",
		logger.String("policy", "age"),
		logger.String("timestamp", time.Now().Format(time.RFC3339)))

	// Perform initial setup (get files, settings, check if proceed)
	files, baseDir, retention, proceed, initialResult := prepareInitialCleanup(quit, db)
	if !proceed {
		GetLogger().Info("Age-based cleanup run completed",
			logger.String("policy", "age"),
			logger.String("result", "no action needed"),
			logger.Int("files_removed", 0),
			logger.Int("disk_utilization", initialResult.DiskUtilization),
			logger.String("timestamp", time.Now().Format(time.RFC3339)),
			logger.Int64("duration_ms", 0))
		return initialResult
	}

	startTime := time.Now() // Track cleanup duration
	keepSpectrograms := retention.KeepSpectrograms
	minClipsPerSpecies := retention.MinClips
	retentionPeriodSetting := retention.MaxAge

	// Sanitize the input string first
	retentionPeriodTrimmed := strings.TrimSpace(retentionPeriodSetting)

	// Convert string retention period setting to hours
	// e.g., "48h", "30d", "2w", or "3m" into actual hours
	retentionPeriodInHours, err := conf.ParseRetentionPeriod(retentionPeriodTrimmed)
	if err != nil {
		GetLogger().Error("Invalid retention period",
			logger.String("policy", "age"),
			logger.String("retention_period", retentionPeriodSetting),
			logger.Error(err))
		// Try to get current disk usage for the result
		currentUsage, diskErr := GetDiskUsage(baseDir)
		utilization := 0
		if diskErr == nil {
			utilization = int(currentUsage)
		}
		// Log the original, untrimmed setting in the error message
		return CleanupResult{Err: fmt.Errorf("invalid retention period '%s': %w", retentionPeriodSetting, err), ClipsRemoved: 0, DiskUtilization: utilization}
	}

	GetLogger().Debug("Starting age-based cleanup",
		logger.String("policy", "age"),
		logger.String("base_dir", baseDir),
		logger.String("retention_period", retentionPeriodSetting),
		logger.Int("retention_hours", retentionPeriodInHours))

	// Create a map to keep track of the *total* number of files per species
	// This is used to enforce the minimum clips per species constraint
	speciesTotalCount := buildSpeciesTotalCountMap(files)

	// Sort files: Oldest first, then lowest confidence first as a tie-breaker
	// This ensures we delete the oldest, least confident files first
	sort.SliceStable(files, func(i, j int) bool {
		// Use time.Time methods for consistent comparison
		if !files[i].Timestamp.Equal(files[j].Timestamp) {
			return files[i].Timestamp.Before(files[j].Timestamp) // Oldest first
		}
		return files[i].Confidence < files[j].Confidence // Lowest confidence first
	})

	// Calculate the expiration time (oldest files that should be kept) in local time
	// Any file with a timestamp BEFORE this cutoff is considered for deletion
	// Files newer than this cutoff will be preserved regardless of other factors
	retentionCutoff := time.Now().Add(-time.Duration(retentionPeriodInHours) * time.Hour)
	retentionCutoffUnix := retentionCutoff.Unix()

	cutoffFormatted, cutoffAge := formatTimeForHumans(retentionCutoffUnix)
	GetLogger().Debug("Retention cutoff calculated",
		logger.String("policy", "age"),
		logger.String("cutoff_time", cutoffFormatted),
		logger.String("cutoff_age", cutoffAge),
		logger.String("system_time", time.Now().Format("2006-01-02 15:04:05 MST")),
		logger.String("timezone", time.Now().Format("MST")))

	startSnapshot := newRetentionSnapshot(baseDir, &retention)
	run := newDeletionRun("age", quit, db, baseDir, keepSpectrograms, &startSnapshot)

	// Call the helper function to process files
	deletedCount, _, stats, loopErr := processAgeBasedDeletionLoop(files, speciesTotalCount,
		minClipsPerSpecies, keepSpectrograms, run, retentionCutoffUnix)

	// Release the clip_name references of the last, partial batch; earlier
	// batches were released at their boundaries.
	run.finish()
	stats.addRunTotals(run)

	// Get final disk utilization
	diskUsage, diskErr := GetDiskUsage(baseDir)

	// Always emit the per-run summary, independent of the disk-usage lookup below,
	// so the outcome of every file (deleted, locked, min-clips-blocked, not old
	// enough, or errored) is visible even when the final usage check fails.
	// The age policy has no usage target, so usageBefore/usageThreshold are not
	// applicable; usageAfter is reported when the final lookup succeeded.
	usageAfter := unknownUsagePercent
	if diskErr == nil {
		usageAfter = int(diskUsage)
	}
	logCleanupSummary(&cleanupSummary{
		policy:           "age",
		stats:            stats,
		duration:         time.Since(startTime),
		keepSpectrograms: keepSpectrograms,
		usageBefore:      unknownUsagePercent,
		usageAfter:       usageAfter,
		usageThreshold:   unknownUsagePercent,
	})

	if diskErr != nil {
		// Combine errors if getting disk usage failed after the loop
		finalErr := fmt.Errorf("cleanup completed but failed to get disk usage: %w (loop error: %w)", diskErr, loopErr)

		// Log the completion with error
		duration := time.Since(startTime)
		GetLogger().Error("Age-based cleanup run completed with errors",
			logger.String("policy", "age"),
			logger.Int("files_removed", deletedCount),
			logger.Int("disk_utilization", 0),
			logger.Error(finalErr),
			logger.String("timestamp", time.Now().Format(time.RFC3339)),
			logger.Int64("duration_ms", duration.Milliseconds()))

		return CleanupResult{Err: finalErr, ClipsRemoved: deletedCount, DiskUtilization: 0, MoreWork: stats.MoreWork}
	}

	// Log the successful completion
	duration := time.Since(startTime)
	GetLogger().Info("Age-based cleanup run completed",
		logger.String("policy", "age"),
		logger.Int("files_removed", deletedCount),
		logger.Int("disk_utilization", int(diskUsage)),
		logger.String("timestamp", time.Now().Format(time.RFC3339)),
		logger.Int64("duration_ms", duration.Milliseconds()))

	// Return the final result, including any error encountered during the loop
	return CleanupResult{Err: loopErr, ClipsRemoved: deletedCount, DiskUtilization: int(diskUsage), MoreWork: stats.MoreWork}
}

// processSingleAgeFile handles eligibility check and deletion for a single file in age-based cleanup.
// Returns: deleted (bool), the eligibility/skip reason (for cleanupStats tallying), how long the
// deletion took (zero when the file was not eligible, i.e. reason is not ageReasonEligible), and
// any deletion error.
func processSingleAgeFile(file *FileInfo, retentionCutoffUnix int64, speciesTotalCount map[string]int,
	minClipsPerSpecies int, keepSpectrograms bool, now func() time.Time) (deleted bool, reason string, latency time.Duration, err error) {
	// Check eligibility
	eligible, reason := isEligibleForAgeDeletion(file, retentionCutoffUnix, speciesTotalCount, minClipsPerSpecies)
	if !eligible {
		log := GetLogger()
		if reason != ageReasonLocked && reason != ageReasonNotOldEnough {
			log.Debug("Skipping file",
				logger.String("policy", "age"),
				logger.String("path", file.Path),
				logger.String("reason", reason))
		}
		return false, reason, 0, nil
	}

	// Perform deletion
	start := now()
	delErr := deleteFileAndOptionalSpectrogram(file, reason, keepSpectrograms, "age")
	latency = now().Sub(start)
	if delErr != nil {
		return false, reason, latency, delErr
	}

	return true, reason, latency, nil
}

// decrementSpeciesCount lowers the remaining-clip count of a species by one,
// never below zero.
func decrementSpeciesCount(speciesTotalCount map[string]int, species string) {
	speciesTotalCount[species]--
	if speciesTotalCount[species] < 0 {
		speciesTotalCount[species] = 0
	}
}

// processAgeBasedDeletionLoop handles the core logic of iterating through files,
// checking age and minimum species counts, and deleting files in paced batches.
// The loop continues until one of these conditions is met (stats.StopReason names it):
//  1. All files have been processed (stopExhausted)
//  2. The run's time budget is spent (stopTimeBudget)
//  3. The retention settings changed (stopSettingsChanged)
//  4. The locked clip list could not be refreshed (stopLockRefreshFailed)
//  5. A quit signal is received (stopQuit)
//  6. Too many deletion errors occurred (stopTooManyErrors)
//
// A run that stops at 2 or 3 after deleting something while old files remain
// sets stats.MoreWork.
func processAgeBasedDeletionLoop(files []FileInfo, speciesTotalCount map[string]int,
	minClipsPerSpecies int, keepSpectrograms bool,
	run *deletionRun, retentionCutoffUnix int64) (deletedCount int, deletedNames []string, stats cleanupStats, loopErr error) {

	deletedCount = 0
	errorCount := 0
	stats.Scanned = len(files)

	log := GetLogger()

	if !run.begin(files) {
		stats.StopReason = run.stopReason
		return deletedCount, deletedNames, stats, nil
	}

	for i := range files {
		select {
		case <-run.quit:
			log.Info("Age-based cleanup loop interrupted by quit signal",
				logger.String("policy", "age"),
				logger.Int("files_deleted", deletedCount))
			stats.StopReason = stopQuit
			return deletedCount, deletedNames, stats, nil // Indicate interruption, but not necessarily an error from the loop itself
		default:
		}

		if run.boundaryDue() && !run.endBatch(files[i:]) {
			stats.StopReason = run.stopReason
			// files is sorted oldest first, so if the next file is not older
			// than the cutoff, no age-eligible work remains.
			stats.MoreWork = run.moreWork(files[i].Timestamp.Unix() < retentionCutoffUnix)
			log.Debug("Age-based cleanup run stopped at a batch boundary",
				logger.String("policy", "age"),
				logger.String("stop_reason", stats.StopReason),
				logger.Bool("more_work", stats.MoreWork))
			return deletedCount, deletedNames, stats, nil
		}

		file := &files[i]
		deleted, reason, latency, delErr := processSingleAgeFile(file, retentionCutoffUnix, speciesTotalCount, minClipsPerSpecies, keepSpectrograms, run.now)

		if errors.Is(delErr, errFileAlreadyGone) {
			// Removed elsewhere since the scan: it no longer counts toward its
			// species total, and nothing was written, so no pacing wait.
			stats.AlreadyGone++
			decrementSpeciesCount(speciesTotalCount, file.Species)
			continue
		}

		if delErr != nil {
			stats.Errors++
			shouldStop, loopErrTmp := handleDeletionErrorInLoop(file.Path, delErr, &errorCount, 10, "age")
			if shouldStop {
				stats.StopReason = stopTooManyErrors
				return deletedCount, deletedNames, stats, loopErrTmp
			}
			if !run.afterDeletionAttempt(file.Path, false, latency) {
				stats.StopReason = run.stopReason
				return deletedCount, deletedNames, stats, nil
			}
			continue
		}

		switch {
		case deleted:
			stats.Deleted++
			stats.BytesFreed += file.Size
			decrementSpeciesCount(speciesTotalCount, file.Species)
			deletedNames = append(deletedNames, file.Path)
			deletedCount++
			if !run.afterDeletionAttempt(file.Path, true, latency) {
				stats.StopReason = run.stopReason
				return deletedCount, deletedNames, stats, nil
			}
		case reason == ageReasonLocked:
			stats.LockedSkipped++
		case reason == ageReasonMinClips:
			stats.MinClipsBlocked++
		default: // ageReasonNotOldEnough
			stats.NotEligible++
		}

		runtime.Gosched()
	}

	stats.StopReason = stopExhausted
	return deletedCount, deletedNames, stats, loopErr
}

// isEligibleForAgeDeletion checks if a file meets the criteria for deletion based on age policy.
// Files are eligible for deletion when ALL of these conditions are met:
// 1. File is not locked (protected from deletion)
// 2. File is older than the retention cutoff time (using Unix timestamps in local timezone)
// 3. There are more than minClipsPerSpecies files for this species
func isEligibleForAgeDeletion(file *FileInfo, retentionCutoffUnix int64, speciesTotalCount map[string]int,
	minClipsPerSpecies int) (eligible bool, reason string) {

	// 1. Check if locked (reuse common helper)
	if checkLocked(file) {
		return false, ageReasonLocked
	}

	log := GetLogger()

	// 2. Check if older than retention period using Unix epochs in local timezone
	// Files older than retentionCutoff should be deleted
	// Logic: If file timestamp is NOT before the cutoff, it's too new to delete
	fileUnix := file.Timestamp.Unix()
	if fileUnix >= retentionCutoffUnix {
		// Debug logging handled by log level
		fileFormatted, fileAge := formatTimeForHumans(fileUnix)
		cutoffFormatted, cutoffAge := formatTimeForHumans(retentionCutoffUnix)

		log.Debug("Skipping file (not old enough)",
			logger.String("policy", "age"),
			logger.String("path", file.Path),
			logger.String("file_created", fileFormatted),
			logger.String("file_age", fileAge),
			logger.String("cutoff_time", cutoffFormatted),
			logger.String("cutoff_age", cutoffAge))
		return false, ageReasonNotOldEnough
	}

	// 3. Check minimum *total* clips constraint
	// We must maintain at least minClipsPerSpecies clips for each species
	// If current count is at or below minimum, we can't delete more
	if count, exists := speciesTotalCount[file.Species]; exists && count <= minClipsPerSpecies {
		fileFormatted, fileAge := formatTimeForHumans(fileUnix)
		log.Debug("Total clip count at minimum threshold",
			logger.String("policy", "age"),
			logger.String("species", file.Species),
			logger.Int("min_threshold", minClipsPerSpecies),
			logger.String("path", file.Path),
			logger.String("file_created", fileFormatted),
			logger.String("file_age", fileAge))
		return false, ageReasonMinClips
	}

	// If all checks pass, the file is eligible
	fileFormatted, fileAge := formatTimeForHumans(fileUnix)
	log.Debug("File eligible for deletion",
		logger.String("policy", "age"),
		logger.String("path", file.Path),
		logger.String("file_created", fileFormatted),
		logger.String("file_age", fileAge),
		logger.String("species", file.Species))

	// If all checks pass, the file is eligible
	return true, ageReasonEligible
}
