// policy_usage.go - code for use retention policy
package diskmanager

import (
	"fmt"
	"log"
	"path/filepath"
	"sort"

	"github.com/tphakala/birdnet-go/internal/conf"
)

// Policy defines cleanup policies
type Policy struct {
	AlwaysCleanupFirst map[string]bool // Species to always cleanup first
	NeverCleanup       map[string]bool // Species to never cleanup
}

// UsageBasedCleanup cleans up old audio files based on the configuration and monitors for quit signals
// Returns a CleanupResult containing error, number of clips removed, and current disk utilization percentage.
func UsageBasedCleanup(quitChan chan struct{}, db Interface) CleanupResult { // Use common result type
	settings := conf.Setting()

	debug := settings.Realtime.Audio.Export.Retention.Debug
	baseDir := settings.Realtime.Audio.Export.Path
	minClipsPerSpecies := settings.Realtime.Audio.Export.Retention.MinClips

	// Convert 80% string etc. to 80.0 float64
	threshold, err := conf.ParsePercentage(settings.Realtime.Audio.Export.Retention.MaxUsage)
	if err != nil {
		return CleanupResult{Err: fmt.Errorf("failed to parse usage threshold: %w", err), ClipsRemoved: 0, DiskUtilization: 0} // Use common result type
	}

	if debug {
		log.Printf("Starting cleanup process. Base directory: %s, Threshold: %.1f%%", baseDir, threshold)
	}

	// Get current disk usage
	diskUsage, err := GetDiskUsage(baseDir)
	if err != nil {
		return CleanupResult{Err: fmt.Errorf("failed to get disk usage for %s: %w", baseDir, err), ClipsRemoved: 0, DiskUtilization: int(diskUsage)} // Use common result type
	}

	// Only perform cleanup if disk usage exceeds threshold
	var deletedFiles int
	if diskUsage > threshold {
		// Get all audio files
		files, err := GetAudioFiles(baseDir, allowedFileTypes, db, debug)
		if err != nil {
			return CleanupResult{Err: fmt.Errorf("failed to get audio files for usage-based cleanup: %w", err), ClipsRemoved: 0, DiskUtilization: int(diskUsage)} // Use common result type
		}

		// Check if we have any files to process
		if len(files) == 0 {
			if debug {
				log.Printf("No eligible audio files found for cleanup in %s", baseDir)
			}
			return CleanupResult{Err: nil, ClipsRemoved: 0, DiskUtilization: int(diskUsage)} // Use common result type
		}

		// Sort files by timestamp (oldest first) and build species count map
		// The sorting itself is specific to usage policy, but map building is common
		speciesMonthCount := buildSpeciesSubDirCountMap(files) // Use common function
		sortFilesForUsage(files, speciesMonthCount, debug)     // Keep usage-specific sorting logic separate

		// Perform the cleanup
		// Define the usage-specific deletion check function
		// Note: This check captures the 'threshold' and 'baseDir'.
		// It re-fetches disk usage on each check, which might be inefficient.
		// Consider optimizing if this becomes a performance issue.
		usageCheck := func(file *FileInfo) (bool, string) {
			currentUsage, err := GetDiskUsage(baseDir)
			if err != nil {
				log.Printf("Warning: Failed to get disk usage during usage check for %s: %v", file.Path, err)
				return false, "disk usage check failed" // Don't delete if we can't check usage
			}
			if currentUsage > threshold {
				// Also need to ensure we don't exceed max deletions, but that's handled in the generic loop
				return true, fmt.Sprintf("disk usage %.1f%% > threshold %.1f%%", currentUsage, threshold)
			}
			return false, "disk usage below threshold"
		}

		// Max deletions per run (consistent with age policy)
		maxDeletions := 1000

		// Call the generic processing loop with the usage check
		deletedFiles, err = processFilesGeneric(files, speciesMonthCount,
			minClipsPerSpecies, maxDeletions, debug, quitChan,
			usageCheck)

		if err != nil {
			return CleanupResult{Err: fmt.Errorf("error during usage-based cleanup: %w", err), ClipsRemoved: deletedFiles, DiskUtilization: int(diskUsage)} // Use common result type
		}

		// Get updated disk usage after cleanup
		diskUsage, err = GetDiskUsage(baseDir)
		if err != nil {
			return CleanupResult{Err: fmt.Errorf("cleanup completed but failed to get updated disk usage: %w", err), ClipsRemoved: deletedFiles, DiskUtilization: 0} // Use common result type
		}
	} else if debug {
		log.Printf("Disk usage %.1f%% is below the %.1f%% threshold. No cleanup needed.", diskUsage, threshold)
	}

	return CleanupResult{Err: nil, ClipsRemoved: deletedFiles, DiskUtilization: int(diskUsage)} // Use common result type
}

// sortFilesForUsage sorts files specifically for the usage-based policy.
// It uses the pre-built species count map.
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
