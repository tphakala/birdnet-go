// policy_usage.go - code for usage retention policy
package diskmanager

import (
	"fmt"
	"path/filepath"
	"runtime"
	"sort"
	"time"

	"github.com/tphakala/birdnet-go/internal/conf"
	"github.com/tphakala/birdnet-go/internal/logger"
)

// Policy defines cleanup policies
type Policy struct {
	AlwaysCleanupFirst map[string]bool // Species to always cleanup first
	NeverCleanup       map[string]bool // Species to never cleanup
}

// UsageBasedCleanup removes clips from the filesystem based on disk usage and the number of clips per species.
// This policy activates when disk usage exceeds a configured threshold percentage.
// It prioritizes deletion based on:
// 1. Oldest files first
// 2. Species with most occurrences in their subdirectory (maintaining diversity)
// 3. Lowest confidence files as a tie-breaker
// This function ensures minimum counts per species are preserved to maintain diversity.
// Returns a CleanupResult containing error, number of clips removed, and current disk utilization percentage.
func UsageBasedCleanup(quit <-chan struct{}, db Interface) CleanupResult {
	// Log the start of the cleanup run with structured logger
	GetLogger().Info("Usage-based cleanup run started",
		logger.String("policy", "usage"))

	// Perform initial setup (get files, settings, check if proceed)
	files, baseDir, retention, proceed, initialResult := prepareInitialCleanup(db)
	if !proceed {
		GetLogger().Info("Usage-based cleanup run completed",
			logger.String("policy", "usage"),
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
	usageThresholdSetting := retention.MaxUsage

	// Convert usage threshold string (e.g., "80%") to float64
	// This determines at what disk usage percentage the cleanup should activate
	usageThresholdFloat, err := conf.ParsePercentage(usageThresholdSetting)
	if err != nil {
		// Use the utilization from the initial result if available
		GetLogger().Error("Usage-based cleanup failed",
			logger.String("policy", "usage"),
			logger.String("threshold_setting", usageThresholdSetting),
			logger.Error(err),
			logger.Int("files_removed", 0),
			logger.Int("disk_utilization", initialResult.DiskUtilization),
			logger.String("timestamp", time.Now().Format(time.RFC3339)),
			logger.Int64("duration_ms", time.Since(startTime).Milliseconds()))
		return CleanupResult{Err: fmt.Errorf("failed to parse usage threshold '%s': %w", usageThresholdSetting, err), ClipsRemoved: 0, DiskUtilization: initialResult.DiskUtilization}
	}
	usageThreshold := int(usageThresholdFloat)

	GetLogger().Debug("Starting usage-based cleanup",
		logger.String("policy", "usage"),
		logger.String("base_dir", baseDir),
		logger.Int("usage_threshold", usageThreshold))

	// Get initial detailed disk usage and check if cleanup is needed *now*
	// Only proceed if current usage is above the threshold
	initialUsagePercent, diskInfo, proceedAfterUsageCheck, err := checkInitialUsage(baseDir, usageThreshold)
	if !proceedAfterUsageCheck {
		// If checkInitialUsage returned an error, use that. Otherwise, use the initial result's utilization.
		finalUtilization := initialResult.DiskUtilization
		if err == nil { // If no error from checkInitialUsage, it means usage was below threshold
			finalUtilization = initialUsagePercent
		}

		// Early completion - disk usage already below threshold
		duration := time.Since(startTime)
		GetLogger().Info("Usage-based cleanup run completed",
			logger.String("policy", "usage"),
			logger.String("result", "usage below threshold"),
			logger.Int("files_removed", 0),
			logger.Int("disk_utilization", finalUtilization),
			logger.Int("usage_threshold", usageThreshold),
			logger.String("timestamp", time.Now().Format(time.RFC3339)),
			logger.Int64("duration_ms", duration.Milliseconds()))

		return CleanupResult{Err: err, ClipsRemoved: 0, DiskUtilization: finalUtilization}
	}

	// Create a map to keep track of the number of files per species per subdirectory (Month folder)
	// This differs from AgeBasedCleanup which uses global species counts
	// Usage policy preserves diversity per month/directory to maintain temporal spread
	speciesMonthCount := buildSpeciesSubDirCountMap(files)

	// Sort files using the dedicated usage policy sorting logic
	// This sorts by age, species count in subdirectories, and confidence
	sortFilesForUsage(files, speciesMonthCount)

	// --- Run the main processing loop ---
	loopParams := &usageLoopParams{
		diskInfo:            diskInfo,
		initialUsagePercent: initialUsagePercent,
		usageThreshold:      usageThreshold,
		minClipsPerSpecies:  minClipsPerSpecies,
		maxDeletions:        maxDeletionsPerRun, // Use package-level constant
		refreshInterval:     50,                 // Refresh actual disk usage every N deletions
		keepSpectrograms:    keepSpectrograms,
	}
	deletedCount, lastKnownGoodUsagePercent, loopErr := processUsageDeletionLoop(files, speciesMonthCount,
		loopParams, baseDir, // Pass the struct pointer and baseDir
		quit)

	// --- Calculate Final Usage & Return ---
	finalUsagePercent := getFinalUsagePercent(baseDir, lastKnownGoodUsagePercent)

	// Log completion with results
	duration := time.Since(startTime)
	if loopErr != nil {
		GetLogger().Error("Usage-based cleanup run completed with errors",
			logger.String("policy", "usage"),
			logger.Int("files_removed", deletedCount),
			logger.Int("disk_utilization", finalUsagePercent),
			logger.Error(loopErr),
			logger.Duration("duration", duration))
	} else {
		GetLogger().Info("Usage-based cleanup run completed",
			logger.String("policy", "usage"),
			logger.Int("files_removed", deletedCount),
			logger.Int("disk_utilization", finalUsagePercent),
			logger.Int("usage_threshold", usageThreshold),
			logger.Duration("duration", duration))
	}

	return CleanupResult{Err: loopErr, ClipsRemoved: deletedCount, DiskUtilization: finalUsagePercent}
}

// usageLoopParams holds the parameters for the usage-based deletion loop.
// This struct helps organize parameters and makes function signatures cleaner.
type usageLoopParams struct {
	diskInfo            DiskSpaceInfo // Holds total/used/free disk space
	initialUsagePercent int           // Starting disk usage percentage
	usageThreshold      int           // Target usage percentage (cleanup stops when below this)
	minClipsPerSpecies  int           // Minimum number of clips to preserve per species per directory
	maxDeletions        int           // Maximum number of files to delete in one run
	refreshInterval     int           // How often to refresh actual disk usage (every N deletions)
	keepSpectrograms    bool          // Whether to keep spectrograms when deleting audio files
}

// calculateUsagePercent computes the current estimated disk usage percentage.
// Returns the calculated percentage and updates lastKnownGoodUsagePercent if valid.
func calculateUsagePercent(estimatedUsedBytes, totalBytes uint64) int {
	if totalBytes > 0 {
		return int((estimatedUsedBytes * 100) / totalBytes) // #nosec G115 -- percentage calculation, result bounded by 100
	}
	return 0
}

// updateUsageStateAfterDeletion updates tracking state after a successful file deletion.
// Returns the updated estimatedUsedBytes value.
func updateUsageStateAfterDeletion(file *FileInfo, speciesMonthCount map[string]map[string]int,
	estimatedUsedBytes, totalBytes uint64) uint64 {
	_ = totalBytes // unused but kept for API stability
	// Track which directory the file is in (typically month-based)
	subDir := filepath.Dir(file.Path)
	// Decrement the count for this species in this subdirectory
	speciesMonthCount[file.Species][subDir]--
	if speciesMonthCount[file.Species][subDir] < 0 {
		speciesMonthCount[file.Species][subDir] = 0
	}
	// Update our estimate of used bytes based on the deleted file's size
	// Check before subtraction to prevent underflow
	fileSize := uint64(file.Size) // #nosec G115 -- file size conversion safe
	if fileSize > estimatedUsedBytes {
		estimatedUsedBytes = 0
	} else {
		estimatedUsedBytes -= fileSize
	}
	return estimatedUsedBytes
}

// processUsageDeletionLoop contains the core logic for iterating through files and deleting based on usage.
// It continues until one of these conditions is met:
// 1. Disk usage falls below the threshold
// 2. Maximum number of deletions is reached
// 3. All eligible files have been processed
// 4. A quit signal is received
func processUsageDeletionLoop(files []FileInfo, speciesMonthCount map[string]map[string]int,
	params *usageLoopParams, baseDir string,
	quit <-chan struct{}) (deletedCount, lastKnownGoodUsagePercent int, loopErr error) {

	deletedCount = 0
	errorCount := 0
	estimatedUsedBytes := params.diskInfo.UsedBytes
	lastKnownGoodUsagePercent = params.initialUsagePercent

	log := GetLogger()

	for i := range files {
		select {
		case <-quit:
			log.Info("Usage-based cleanup loop interrupted by quit signal",
				logger.String("policy", "usage"),
				logger.Int("files_deleted", deletedCount))
			return deletedCount, lastKnownGoodUsagePercent, loopErr
		default:
			params.diskInfo, estimatedUsedBytes = refreshUsageDataIfNeeded(deletedCount, params.refreshInterval, baseDir, params.diskInfo, estimatedUsedBytes)

			currentUsagePercent := calculateUsagePercent(estimatedUsedBytes, params.diskInfo.TotalBytes)
			if params.diskInfo.TotalBytes > 0 {
				lastKnownGoodUsagePercent = currentUsagePercent
			}

			if shouldStopUsageCleanup(currentUsagePercent, params.usageThreshold, deletedCount, params.maxDeletions) {
				return deletedCount, lastKnownGoodUsagePercent, loopErr
			}

			file := &files[i]
			deleted, deletionErr := handleUsageDeletionIteration(file, speciesMonthCount, params.minClipsPerSpecies, params.keepSpectrograms, currentUsagePercent, params.usageThreshold)

			if deletionErr != nil {
				shouldStop, loopErrTmp := handleDeletionErrorInLoop(file.Path, deletionErr, &errorCount, 10, "usage")
				if shouldStop {
					return deletedCount, lastKnownGoodUsagePercent, loopErrTmp
				}
				continue
			}

			if deleted {
				estimatedUsedBytes = updateUsageStateAfterDeletion(file, speciesMonthCount, estimatedUsedBytes, params.diskInfo.TotalBytes)
				deletedCount++
			}

			runtime.Gosched()
		}
	}
	return deletedCount, lastKnownGoodUsagePercent, loopErr
}

// getFinalUsagePercent calculates the final disk usage percentage, falling back if necessary.
// This provides an accurate final measurement after cleanup has completed.
func getFinalUsagePercent(baseDir string, lastKnownGoodUsagePercent int) int {
	log := GetLogger()

	finalDiskInfo, finalDiskErr := GetDetailedDiskUsage(baseDir)
	if finalDiskErr != nil {
		log.Warn("Failed to get final accurate disk usage after cleanup, using last known value",
			logger.String("policy", "usage"),
			logger.Error(finalDiskErr),
			logger.Int("last_known_usage", lastKnownGoodUsagePercent))
		return lastKnownGoodUsagePercent // Use fallback
	}

	if finalDiskInfo.TotalBytes > 0 {
		finalUsagePercent := int((finalDiskInfo.UsedBytes * 100) / finalDiskInfo.TotalBytes) // #nosec G115 -- percentage calculation, result bounded by 100
		log.Debug("Final accurate disk usage calculated",
			logger.String("policy", "usage"),
			logger.Int("final_usage", finalUsagePercent))
		return finalUsagePercent
	}

	// Fallback if total bytes is 0 (should be rare)
	log.Warn("Final disk info reported zero total bytes, using last known value",
		logger.String("policy", "usage"),
		logger.Int("last_known_usage", lastKnownGoodUsagePercent))
	return lastKnownGoodUsagePercent
}

// checkInitialUsage performs the initial disk usage check and determines if cleanup should proceed.
// Usage-based cleanup only starts if current usage is above threshold.
// Returns:
// - initialUsagePercent: current disk usage percentage
// - diskInfo: detailed disk space information
// - proceed: whether cleanup should proceed based on usage
// - err: any error encountered getting disk info
func checkInitialUsage(baseDir string, usageThreshold int) (initialUsagePercent int, diskInfo DiskSpaceInfo, proceed bool, err error) {
	// Try to get detailed disk usage info first
	diskInfo, err = GetDetailedDiskUsage(baseDir)
	if err != nil {
		// Try fallback to percentage-only method if detailed info fails
		initialUsagePercentFloat, fallbackErr := GetDiskUsage(baseDir)
		if fallbackErr != nil {
			err = fmt.Errorf("failed to get initial disk usage (both detailed and percentage): %w, %w", err, fallbackErr)
			return 0, DiskSpaceInfo{}, false, err
		}
		initialUsagePercent = int(initialUsagePercentFloat)
		if initialUsagePercent < usageThreshold {
			GetLogger().Debug("Initial disk usage (fallback) below threshold, no cleanup needed",
				logger.String("policy", "usage"),
				logger.Int("initial_usage", initialUsagePercent),
				logger.Int("threshold", usageThreshold))
			return initialUsagePercent, DiskSpaceInfo{}, false, nil // No error, just don't proceed
		}
		// Fallback succeeded, but usage is high. Cannot proceed reliably without detailed info.
		err = fmt.Errorf("failed to get initial detailed disk usage (%w), cannot proceed reliably", err)
		return initialUsagePercent, DiskSpaceInfo{}, false, err
	}

	// Detailed usage obtained successfully
	if diskInfo.TotalBytes > 0 {
		initialUsagePercent = int((diskInfo.UsedBytes * 100) / diskInfo.TotalBytes) // #nosec G115 -- percentage calculation, result bounded by 100
	}
	
	// Update disk usage metrics
	updateDiskUsageMetrics(diskInfo)

	if initialUsagePercent < usageThreshold {
		GetLogger().Debug("Initial disk usage below threshold, no cleanup needed",
			logger.String("policy", "usage"),
			logger.Int("initial_usage", initialUsagePercent),
			logger.Int("threshold", usageThreshold))
		return initialUsagePercent, diskInfo, false, nil // No error, just don't proceed
	}

	// Usage is above threshold, proceed with cleanup
	return initialUsagePercent, diskInfo, true, nil
}

// handleUsageDeletionIteration processes a single file for potential deletion based on usage policy rules.
// It returns whether the file was deleted and any critical error encountered during deletion.
func handleUsageDeletionIteration(file *FileInfo, speciesMonthCount map[string]map[string]int, minClipsPerSpecies int, keepSpectrograms bool, currentUsagePercent, usageThreshold int) (deleted bool, deletionErr error) {
	// Check if locked
	if checkLocked(file) {
		return false, nil
	}

	// Check minimum clips constraint (per species per month dir)
	// This differs from age-based policy by preserving diversity within each time period (directory)
	subDir := filepath.Dir(file.Path)
	if !checkMinClips(file, subDir, speciesMonthCount, minClipsPerSpecies, "usage") {
		return false, nil
	}

	// Reason for deletion (used in logging)
	reason := fmt.Sprintf("usage %d%% >= threshold %d%%", currentUsagePercent, usageThreshold)

	// Call the common deletion function
	if delErr := deleteFileAndOptionalSpectrogram(file, reason, keepSpectrograms, "usage"); delErr != nil {
		// Return the error to be handled by the main loop (e.g., increment error count)
		return false, delErr
	}

	// Deletion successful
	return true, nil
}

// sortFilesForUsage sorts files specifically for the usage-based policy.
// It uses the pre-built species count map.
// Sorting priorities:
// 1. Oldest files first (to preserve recent recordings)
// 2. Species with most occurrences in each subdirectory (to maintain diversity)
// 3. Lower confidence recordings first (to keep higher quality recordings)
func sortFilesForUsage(files []FileInfo, speciesMonthCount map[string]map[string]int) {
	GetLogger().Debug("Sorting files by usage cleanup priority",
		logger.String("policy", "usage"),
		logger.Int("file_count", len(files)))

	sort.SliceStable(files, func(i, j int) bool {
		// Priority 1: Oldest files first
		if !files[i].Timestamp.Equal(files[j].Timestamp) { // Use !Equal for clarity
			return files[i].Timestamp.Before(files[j].Timestamp)
		}

		// Priority 2: Species with the most occurrences in the subdirectory
		// This helps maintain species diversity by keeping the last few recordings of each species
		subDirI := filepath.Dir(files[i].Path)
		subDirJ := filepath.Dir(files[j].Path)
		countI := 0
		if speciesMapI, okI := speciesMonthCount[files[i].Species]; okI {
			if sCountI, okSubDirI := speciesMapI[subDirI]; okSubDirI {
				countI = sCountI
			}
		}
		countJ := 0
		if speciesMapJ, okJ := speciesMonthCount[files[j].Species]; okJ {
			if sCountJ, okSubDirJ := speciesMapJ[subDirJ]; okSubDirJ {
				countJ = sCountJ
			}
		}

		if countI != countJ {
			return countI > countJ // Higher count means higher priority for deletion (if older)
		}

		// Priority 3: Lower Confidence level first (change from original)
		// Rationale: Keep higher confidence clips if timestamps and counts are equal.
		if files[i].Confidence != files[j].Confidence {
			return files[i].Confidence < files[j].Confidence // Delete lower confidence first
		}

		// Default to oldest timestamp (already handled in priority 1)
		return false // Keep original order if all else is equal
	})

	GetLogger().Debug("Files sorted for usage cleanup",
		logger.String("policy", "usage"))
}

// refreshUsageDataIfNeeded periodically refreshes the disk usage information.
// It returns the potentially updated DiskSpaceInfo and estimatedUsedBytes.
func refreshUsageDataIfNeeded(deletedCount, refreshInterval int, baseDir string, currentDiskInfo DiskSpaceInfo, currentEstimatedUsedBytes uint64) (updatedDiskInfo DiskSpaceInfo, updatedEstimatedUsedBytes uint64) {
	log := GetLogger()

	// Only refresh every refreshInterval deletions to minimize I/O impact
	if deletedCount > 0 && deletedCount%refreshInterval == 0 && baseDir != "" {
		refreshedDiskInfo, refreshErr := GetDetailedDiskUsage(baseDir)
		if refreshErr != nil {
			log.Warn("Failed to refresh disk usage during cleanup, continuing with estimated usage",
				logger.String("policy", "usage"),
				logger.Error(refreshErr))
			// Keep using the old info and estimate
			return currentDiskInfo, currentEstimatedUsedBytes
		}
		log.Debug("Refreshed disk usage",
			logger.String("policy", "usage"),
			logger.Uint64("total_bytes", refreshedDiskInfo.TotalBytes),
			logger.Uint64("used_bytes", refreshedDiskInfo.UsedBytes))
		// Update disk usage metrics
		updateDiskUsageMetrics(refreshedDiskInfo)
		// Return updated info and reset estimate to actual
		return refreshedDiskInfo, refreshedDiskInfo.UsedBytes
	}
	// No refresh needed or possible, return current values
	return currentDiskInfo, currentEstimatedUsedBytes
}

// shouldStopUsageCleanup checks if the cleanup loop should terminate based on usage threshold or max deletions.
// Returns true if cleanup should stop, false if it should continue.
func shouldStopUsageCleanup(currentUsagePercent, usageThreshold, deletedCount, maxDeletions int) bool {
	log := GetLogger()

	// Check if usage is still above threshold
	if currentUsagePercent < usageThreshold {
		log.Debug("Disk usage now below threshold, stopping cleanup",
			logger.String("policy", "usage"),
			logger.Int("current_usage", currentUsagePercent),
			logger.Int("threshold", usageThreshold))
		return true // Stop deleting files
	}

	// Check if max deletions reached
	if deletedCount >= maxDeletions {
		log.Debug("Reached maximum number of deletions for usage-based cleanup",
			logger.String("policy", "usage"),
			logger.Int("max_deletions", maxDeletions))
		return true // Stop deleting files
	}

	return false // Continue cleanup
}
