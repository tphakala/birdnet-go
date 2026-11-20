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

// AgeBasedCleanup removes clips older than maxage while preserving at least minclips per species.
func AgeBasedCleanup(quit <-chan struct{}, db Interface) CleanupResult {
	return cleanupByAgePolicy(quit, db, conf.RetentionPolicyAge)
}

// cleanupByAgePolicy runs the shared age-window cleanup used by age and best-samples policies.
// File timestamps are compared in local time to match how clip filenames are parsed.
// Deletion is paced and done in batches, and one run ends after at most maxCleanupRunDuration.
func cleanupByAgePolicy(quit <-chan struct{}, db Interface, policy string) CleanupResult {
	policyName := "Age-based"
	if policy == conf.RetentionPolicyBestSamples {
		policyName = "Best-samples"
	}

	// Log the start of the cleanup run with structured logger
	GetLogger().Info(fmt.Sprintf("%s cleanup run started", policyName),
		logger.String("policy", policy),
		logger.String("timestamp", time.Now().Format(time.RFC3339)))

	// Perform initial setup (get files, settings, check if proceed)
	files, baseDir, retention, proceed, initialResult := prepareInitialCleanup(quit, db)
	if !proceed {
		GetLogger().Info(fmt.Sprintf("%s cleanup run completed", policyName),
			logger.String("policy", policy),
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
			logger.String("policy", policy),
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

	GetLogger().Debug(fmt.Sprintf("Starting %s cleanup", strings.ToLower(policyName)),
		logger.String("policy", policy),
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
		logger.String("policy", policy),
		logger.String("cutoff_time", cutoffFormatted),
		logger.String("cutoff_age", cutoffAge),
		logger.String("system_time", time.Now().Format("2006-01-02 15:04:05 MST")),
		logger.String("timezone", time.Now().Format("MST")))

	startSnapshot := newRetentionSnapshot(baseDir, &retention)
	run := newDeletionRun(policy, quit, db, baseDir, keepSpectrograms, &startSnapshot)

	var bestClipPaths map[string]struct{}
	if policy == conf.RetentionPolicyBestSamples {
		bestClipPaths = selectBestClipsPerSpecies(files, minClipsPerSpecies)
	}

	// Call the helper function to process files
	deletedCount, _, stats, loopErr := processAgeDeletionLoop(files, speciesTotalCount,
		minClipsPerSpecies, keepSpectrograms, run, retentionCutoffUnix, policy, bestClipPaths)

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
		policy:           policy,
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
		GetLogger().Error(fmt.Sprintf("%s cleanup run completed with errors", policyName),
			logger.String("policy", policy),
			logger.Int("files_removed", deletedCount),
			logger.Int("disk_utilization", 0),
			logger.Error(finalErr),
			logger.String("timestamp", time.Now().Format(time.RFC3339)),
			logger.Int64("duration_ms", duration.Milliseconds()))

		return CleanupResult{Err: finalErr, ClipsRemoved: deletedCount, DiskUtilization: 0, MoreWork: stats.MoreWork}
	}

	// Log the successful completion
	duration := time.Since(startTime)
	GetLogger().Info(fmt.Sprintf("%s cleanup run completed", policyName),
		logger.String("policy", policy),
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
	minClipsPerSpecies int, policy string, bestClipPaths map[string]struct{}, keepSpectrograms bool,
	now func() time.Time, dirs *renderDirCache) (deleted bool, reason string, latency time.Duration, err error) {
	// Check eligibility
	eligible, reason := isEligibleForAgeDeletion(file, retentionCutoffUnix, speciesTotalCount, minClipsPerSpecies, policy, bestClipPaths)
	if !eligible {
		return false, reason, 0, nil
	}

	// Perform deletion
	start := now()
	delErr := deleteFileAndOptionalSpectrogram(file, reason, keepSpectrograms, policy, dirs)
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
// A run that stops at 3 sets stats.MoreWork, and one that stops at 2 does when it
// deleted something and old files remain.
func processAgeBasedDeletionLoop(files []FileInfo, speciesTotalCount map[string]int,
	minClipsPerSpecies int, keepSpectrograms bool,
	run *deletionRun, retentionCutoffUnix int64) (deletedCount int, deletedNames []string, stats cleanupStats, loopErr error) {
	return processAgeDeletionLoop(files, speciesTotalCount, minClipsPerSpecies, keepSpectrograms, run, retentionCutoffUnix, conf.RetentionPolicyAge, nil)
}

func processAgeDeletionLoop(files []FileInfo, speciesTotalCount map[string]int,
	minClipsPerSpecies int, keepSpectrograms bool,
	run *deletionRun, retentionCutoffUnix int64, policy string,
	bestClipPaths map[string]struct{}) (deletedCount int, deletedNames []string, stats cleanupStats, loopErr error) {

	deletedCount = 0
	errorCount := 0
	stats.Scanned = len(files)

	log := GetLogger()
	policyName := "Age-based"
	if policy == conf.RetentionPolicyBestSamples {
		policyName = "Best-samples"
	}

	if !run.begin() {
		stats.StopReason = run.stopReason
		stats.MoreWork = run.moreWork(false)
		return deletedCount, deletedNames, stats, nil
	}

	for i := range files {
		select {
		case <-run.quit:
			log.Info(fmt.Sprintf("%s cleanup loop interrupted by quit signal", policyName),
				logger.String("policy", policy),
				logger.Int("files_deleted", deletedCount))
			stats.StopReason = stopQuit
			return deletedCount, deletedNames, stats, nil // Indicate interruption, but not necessarily an error from the loop itself
		default:
		}

		if run.boundaryDue() && !run.endBatch() {
			stats.StopReason = run.stopReason
			// files is sorted oldest first, so if the next file is not older
			// than the cutoff, no age-eligible work remains.
			stats.MoreWork = run.moreWork(files[i].Timestamp.Unix() < retentionCutoffUnix)
			log.Debug(fmt.Sprintf("%s cleanup run stopped at a batch boundary", policyName),
				logger.String("policy", policy),
				logger.String("stop_reason", stats.StopReason),
				logger.Bool("more_work", stats.MoreWork))
			return deletedCount, deletedNames, stats, nil
		}

		file := &files[i]
		run.markLocked(file)
		deleted, reason, latency, delErr := processSingleAgeFile(file, retentionCutoffUnix,
			speciesTotalCount, minClipsPerSpecies, policy, bestClipPaths, keepSpectrograms, run.now, &run.renderDirs)

		if errors.Is(delErr, errFileAlreadyGone) {
			// Removed elsewhere since the scan: it no longer counts toward its
			// species total, and nothing was written, so no pacing wait.
			stats.AlreadyGone++
			decrementSpeciesCount(speciesTotalCount, file.Species)
			continue
		}

		if delErr != nil {
			stats.Errors++
			shouldStop, loopErrTmp := handleDeletionErrorInLoop(file.Path, delErr, &errorCount, 10, policy)
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
// 3. The policy's clip-selection rule allows deletion and more than minClipsPerSpecies remain
func isEligibleForAgeDeletion(file *FileInfo, retentionCutoffUnix int64, speciesTotalCount map[string]int,
	minClipsPerSpecies int, policy string, bestClipPaths map[string]struct{}) (eligible bool, reason string) {

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
		fileFormatted, fileAge := formatTimeForHumans(fileUnix)
		cutoffFormatted, cutoffAge := formatTimeForHumans(retentionCutoffUnix)

		log.Trace("Skipping file (not old enough)",
			logger.String("policy", policy),
			logger.String("path", file.Path),
			logger.String("file_created", fileFormatted),
			logger.String("file_age", fileAge),
			logger.String("cutoff_time", cutoffFormatted),
			logger.String("cutoff_age", cutoffAge))
		return false, ageReasonNotOldEnough
	}

	// Best-samples policy keeps the highest-confidence clips regardless of age.
	if _, keep := bestClipPaths[file.Path]; keep {
		log.Trace("Keeping clip among the best samples for species",
			logger.String("policy", policy),
			logger.String("species", file.Species),
			logger.String("path", file.Path))
		return false, ageReasonMinClips
	}

	// Check minimum *total* clips constraint. Age policy uses this floor to
	// retain the newest clips; best-samples uses it as an additional guard.
	if count, exists := speciesTotalCount[file.Species]; exists && count <= minClipsPerSpecies {
		fileFormatted, fileAge := formatTimeForHumans(fileUnix)
		log.Trace("Total clip count at minimum threshold",
			logger.String("policy", policy),
			logger.String("species", file.Species),
			logger.Int("min_threshold", minClipsPerSpecies),
			logger.String("path", file.Path),
			logger.String("file_created", fileFormatted),
			logger.String("file_age", fileAge))
		return false, ageReasonMinClips
	}

	// If all checks pass, the file is eligible
	fileFormatted, fileAge := formatTimeForHumans(fileUnix)
	log.Trace("File eligible for deletion",
		logger.String("policy", policy),
		logger.String("path", file.Path),
		logger.String("file_created", fileFormatted),
		logger.String("file_age", fileAge),
		logger.String("species", file.Species))

	// If all checks pass, the file is eligible
	return true, ageReasonEligible
}
