// policy_usage.go - code for usage retention policy
package diskmanager

import (
	"fmt"
	"log"
	"path/filepath"
	"runtime"
	"sort"

	"github.com/tphakala/birdnet-go/internal/conf"
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
	// Perform initial setup (get files, settings, check if proceed)
	files, baseDir, retention, proceed, initialResult := prepareInitialCleanup(db)
	if !proceed {
		return initialResult
	}
	debug := retention.Debug
	keepSpectrograms := retention.KeepSpectrograms
	minClipsPerSpecies := retention.MinClips
	usageThresholdSetting := retention.MaxUsage

	// Convert usage threshold string (e.g., "80%") to float64
	// This determines at what disk usage percentage the cleanup should activate
	usageThresholdFloat, err := conf.ParsePercentage(usageThresholdSetting)
	if err != nil {
		// Use the utilization from the initial result if available
		return CleanupResult{Err: fmt.Errorf("failed to parse usage threshold '%s': %w", usageThresholdSetting, err), ClipsRemoved: 0, DiskUtilization: initialResult.DiskUtilization}
	}
	usageThreshold := int(usageThresholdFloat)

	if debug {
		log.Printf("Starting usage-based cleanup. Base directory: %s, Usage threshold: %d%% (from %.1f%%)", baseDir, usageThreshold, usageThresholdFloat)
	}

	// Get initial detailed disk usage and check if cleanup is needed *now*
	// Only proceed if current usage is above the threshold
	initialUsagePercent, diskInfo, proceedAfterUsageCheck, err := checkInitialUsage(baseDir, usageThreshold, debug)
	if !proceedAfterUsageCheck {
		// If checkInitialUsage returned an error, use that. Otherwise, use the initial result's utilization.
		finalUtilization := initialResult.DiskUtilization
		if err == nil { // If no error from checkInitialUsage, it means usage was below threshold
			finalUtilization = initialUsagePercent
		}
		return CleanupResult{Err: err, ClipsRemoved: 0, DiskUtilization: finalUtilization}
	}

	// Create a map to keep track of the number of files per species per subdirectory (Month folder)
	// This differs from AgeBasedCleanup which uses global species counts
	// Usage policy preserves diversity per month/directory to maintain temporal spread
	speciesMonthCount := buildSpeciesSubDirCountMap(files)

	// Sort files using the dedicated usage policy sorting logic
	// This sorts by age, species count in subdirectories, and confidence
	sortFilesForUsage(files, speciesMonthCount, debug)

	// --- Run the main processing loop ---
	loopParams := &usageLoopParams{
		diskInfo:            diskInfo,
		initialUsagePercent: initialUsagePercent,
		usageThreshold:      usageThreshold,
		minClipsPerSpecies:  minClipsPerSpecies,
		maxDeletions:        1000, // Maximum number of files to delete in one run
		refreshInterval:     50,   // Refresh actual disk usage every N deletions
		keepSpectrograms:    keepSpectrograms,
		debug:               debug,
	}
	deletedCount, lastKnownGoodUsagePercent, loopErr := processUsageDeletionLoop(files, speciesMonthCount,
		loopParams, baseDir, // Pass the struct pointer and baseDir
		quit)

	// --- Calculate Final Usage & Return ---
	finalUsagePercent := getFinalUsagePercent(baseDir, lastKnownGoodUsagePercent, debug)

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
	debug               bool          // Enable verbose logging
}

// processUsageDeletionLoop contains the core logic for iterating through files and deleting based on usage.
// It continues until one of these conditions is met:
// 1. Disk usage falls below the threshold
// 2. Maximum number of deletions is reached
// 3. All eligible files have been processed
// 4. A quit signal is received
func processUsageDeletionLoop(files []FileInfo, speciesMonthCount map[string]map[string]int,
	params *usageLoopParams, baseDir string, // Add baseDir parameter
	quit <-chan struct{}) (deletedCount, lastKnownGoodUsagePercent int, loopErr error) {

	deletedCount = 0
	errorCount := 0
	// Track estimated usage to avoid checking disk frequently
	// This is updated after each deletion based on file size
	estimatedUsedBytes := params.diskInfo.UsedBytes
	lastKnownGoodUsagePercent = params.initialUsagePercent

	for i := range files {
		select {
		case <-quit:
			log.Printf("Usage-based cleanup loop interrupted by quit signal\n")
			return deletedCount, lastKnownGoodUsagePercent, loopErr // Return current state on quit
		default:
			// Refresh disk usage periodically using helper
			// This ensures our usage estimates don't drift too far from reality
			params.diskInfo, estimatedUsedBytes = refreshUsageDataIfNeeded(deletedCount, params.refreshInterval, baseDir, params.diskInfo, estimatedUsedBytes, params.debug)

			// Calculate current estimated usage percentage
			// We update this after each deletion to avoid checking disk usage too frequently
			currentUsagePercent := 0
			if params.diskInfo.TotalBytes > 0 {
				currentUsagePercent = int((estimatedUsedBytes * 100) / params.diskInfo.TotalBytes)
				lastKnownGoodUsagePercent = currentUsagePercent
			}

			// Check if we should stop the loop using helper
			// Stops if usage is below threshold or max deletions reached
			if shouldStopUsageCleanup(currentUsagePercent, params.usageThreshold, deletedCount, params.maxDeletions, params.debug) {
				return deletedCount, lastKnownGoodUsagePercent, loopErr // Stop deleting files
			}

			file := &files[i]

			// Handle eligibility checks and deletion for this file
			deleted, deletionErr := handleUsageDeletionIteration(file, speciesMonthCount, params.minClipsPerSpecies, params.keepSpectrograms, currentUsagePercent, params.usageThreshold, params.debug)

			if deletionErr != nil {
				// Use the common error handler
				shouldStop, loopErrTmp := handleDeletionErrorInLoop(file.Path, deletionErr, &errorCount, 10)
				if shouldStop {
					loopErr = loopErrTmp                                    // Assign the final error
					return deletedCount, lastKnownGoodUsagePercent, loopErr // Stop processing
				}
				continue // Continue with the next file
			}

			// Update state *after* successful deletion (if deletion occurred)
			if deleted {
				// Track which directory the file is in (typically month-based)
				subDir := filepath.Dir(file.Path)
				// Decrement the count for this species in this subdirectory
				speciesMonthCount[file.Species][subDir]--
				deletedCount++
				// Update our estimate of used bytes based on the deleted file's size
				estimatedUsedBytes -= uint64(file.Size)
				if estimatedUsedBytes > params.diskInfo.TotalBytes { // Prevent underflow/wrap-around (safety check)
					estimatedUsedBytes = 0
				}
			}

			// Yield to other goroutines to avoid monopolizing CPU
			runtime.Gosched()
		}
	}
	// Loop finished normally
	return deletedCount, lastKnownGoodUsagePercent, loopErr
}

// getFinalUsagePercent calculates the final disk usage percentage, falling back if necessary.
// This provides an accurate final measurement after cleanup has completed.
func getFinalUsagePercent(baseDir string, lastKnownGoodUsagePercent int, debug bool) int {
	finalDiskInfo, finalDiskErr := GetDetailedDiskUsage(baseDir)
	if finalDiskErr != nil {
		log.Printf("Warning: Failed to get final accurate disk usage after cleanup: %v. Using last known value: %d%%", finalDiskErr, lastKnownGoodUsagePercent)
		return lastKnownGoodUsagePercent // Use fallback
	}

	if finalDiskInfo.TotalBytes > 0 {
		finalUsagePercent := int((finalDiskInfo.UsedBytes * 100) / finalDiskInfo.TotalBytes)
		if debug {
			log.Printf("Final accurate disk usage: %d%%", finalUsagePercent)
		}
		return finalUsagePercent
	}

	// Fallback if total bytes is 0 (should be rare)
	log.Printf("Warning: Final disk info reported zero total bytes. Using last known value: %d%%", lastKnownGoodUsagePercent)
	return lastKnownGoodUsagePercent
}

// checkInitialUsage performs the initial disk usage check and determines if cleanup should proceed.
// Usage-based cleanup only starts if current usage is above threshold.
// Returns:
// - initialUsagePercent: current disk usage percentage
// - diskInfo: detailed disk space information
// - proceed: whether cleanup should proceed based on usage
// - err: any error encountered getting disk info
func checkInitialUsage(baseDir string, usageThreshold int, debug bool) (initialUsagePercent int, diskInfo DiskSpaceInfo, proceed bool, err error) {
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
			if debug {
				log.Printf("Initial disk usage (fallback %.2f%%) is below threshold (%d%%). No usage-based cleanup needed.", initialUsagePercentFloat, usageThreshold)
			}
			return initialUsagePercent, DiskSpaceInfo{}, false, nil // No error, just don't proceed
		}
		// Fallback succeeded, but usage is high. Cannot proceed reliably without detailed info.
		err = fmt.Errorf("failed to get initial detailed disk usage (%w), cannot proceed reliably", err)
		return initialUsagePercent, DiskSpaceInfo{}, false, err
	}

	// Detailed usage obtained successfully
	if diskInfo.TotalBytes > 0 {
		initialUsagePercent = int((diskInfo.UsedBytes * 100) / diskInfo.TotalBytes)
	}

	if initialUsagePercent < usageThreshold {
		if debug {
			log.Printf("Initial disk usage (%d%%) is below threshold (%d%%). No usage-based cleanup needed.", initialUsagePercent, usageThreshold)
		}
		return initialUsagePercent, diskInfo, false, nil // No error, just don't proceed
	}

	// Usage is above threshold, proceed with cleanup
	return initialUsagePercent, diskInfo, true, nil
}

// handleUsageDeletionIteration processes a single file for potential deletion based on usage policy rules.
// It returns whether the file was deleted and any critical error encountered during deletion.
func handleUsageDeletionIteration(file *FileInfo, speciesMonthCount map[string]map[string]int, minClipsPerSpecies int, keepSpectrograms bool, currentUsagePercent, usageThreshold int, debug bool) (deleted bool, deletionErr error) {
	// Check if locked
	if checkLocked(file, debug) {
		return false, nil
	}

	// Check minimum clips constraint (per species per month dir)
	// This differs from age-based policy by preserving diversity within each time period (directory)
	subDir := filepath.Dir(file.Path)
	if !checkMinClips(file, subDir, speciesMonthCount, minClipsPerSpecies, debug) {
		return false, nil
	}

	// Reason for deletion (used in logging)
	reason := fmt.Sprintf("usage %d%% >= threshold %d%%", currentUsagePercent, usageThreshold)

	// Call the common deletion function
	if delErr := deleteFileAndOptionalSpectrogram(file, reason, keepSpectrograms, debug); delErr != nil {
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
func sortFilesForUsage(files []FileInfo, speciesMonthCount map[string]map[string]int, debug bool) {
	if debug {
		log.Printf("Sorting files by usage cleanup priority.")
	}

	sort.Slice(files, func(i, j int) bool {
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

	if debug {
		log.Printf("Files sorted for usage cleanup.")
	}
}

// refreshUsageDataIfNeeded periodically refreshes the disk usage information.
// It returns the potentially updated DiskSpaceInfo and estimatedUsedBytes.
func refreshUsageDataIfNeeded(deletedCount, refreshInterval int, baseDir string, currentDiskInfo DiskSpaceInfo, currentEstimatedUsedBytes uint64, debug bool) (updatedDiskInfo DiskSpaceInfo, updatedEstimatedUsedBytes uint64) {
	// Only refresh every refreshInterval deletions to minimize I/O impact
	if deletedCount > 0 && deletedCount%refreshInterval == 0 && baseDir != "" {
		refreshedDiskInfo, refreshErr := GetDetailedDiskUsage(baseDir)
		if refreshErr != nil {
			log.Printf("Warning: Failed to refresh disk usage during cleanup: %v. Continuing with estimated usage.", refreshErr)
			// Keep using the old info and estimate
			return currentDiskInfo, currentEstimatedUsedBytes
		} else {
			if debug {
				log.Printf("Refreshed disk usage. Total: %d, Used: %d", refreshedDiskInfo.TotalBytes, refreshedDiskInfo.UsedBytes)
			}
			// Return updated info and reset estimate to actual
			return refreshedDiskInfo, refreshedDiskInfo.UsedBytes
		}
	}
	// No refresh needed or possible, return current values
	return currentDiskInfo, currentEstimatedUsedBytes
}

// shouldStopUsageCleanup checks if the cleanup loop should terminate based on usage threshold or max deletions.
// Returns true if cleanup should stop, false if it should continue.
func shouldStopUsageCleanup(currentUsagePercent, usageThreshold, deletedCount, maxDeletions int, debug bool) bool {
	// Check if usage is still above threshold
	if currentUsagePercent < usageThreshold {
		if debug {
			log.Printf("Disk usage (%d%%) is now below threshold (%d%%). Stopping usage-based cleanup.", currentUsagePercent, usageThreshold)
		}
		return true // Stop deleting files
	}

	// Check if max deletions reached
	if deletedCount >= maxDeletions {
		if debug {
			log.Printf("Reached maximum number of deletions (%d) for usage-based cleanup.", maxDeletions)
		}
		return true // Stop deleting files
	}

	return false // Continue cleanup
}
