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

// UsageCleanupResult contains the results of a usage-based cleanup operation
type UsageCleanupResult struct {
	Err             error // Any error that occurred during cleanup
	ClipsRemoved    int   // Number of clips that were removed
	DiskUtilization int   // Current disk utilization percentage after cleanup
}

// UsageBasedCleanup cleans up old audio files based on the configuration and monitors for quit signals
// Returns a UsageCleanupResult containing error, number of clips removed, and current disk utilization percentage.
func UsageBasedCleanup(quitChan chan struct{}, db Interface) UsageCleanupResult {
	settings := conf.Setting()

	debug := settings.Realtime.Audio.Export.Retention.Debug
	baseDir := settings.Realtime.Audio.Export.Path
	minClipsPerSpecies := settings.Realtime.Audio.Export.Retention.MinClips
	thresholdStr := settings.Realtime.Audio.Export.Retention.MaxUsage

	threshold, err := conf.ParsePercentage(thresholdStr)
	if err != nil {
		// Try get usage
		initDiskUsage, diskErr := GetDiskUsage(baseDir)
		utilization := 0
		if diskErr == nil {
			utilization = int(initDiskUsage)
		}
		return UsageCleanupResult{Err: fmt.Errorf("failed to parse usage threshold '%s': %w", thresholdStr, err), ClipsRemoved: 0, DiskUtilization: utilization}
	}

	if debug {
		log.Printf("Starting usage-based cleanup process. Base directory: %s, Threshold: %.1f%%", baseDir, threshold)
	}

	// Get initial disk usage
	initDiskUsage, err := GetDiskUsage(baseDir)
	if err != nil {
		return UsageCleanupResult{Err: fmt.Errorf("failed to get initial disk usage for %s: %w", baseDir, err), ClipsRemoved: 0, DiskUtilization: 0}
	}

	// Only proceed if disk usage exceeds threshold initially
	if initDiskUsage <= threshold {
		if debug {
			log.Printf("Initial disk usage %.1f%% is at or below the %.1f%% threshold. No usage-based cleanup needed.", initDiskUsage, threshold)
		}
		return UsageCleanupResult{Err: nil, ClipsRemoved: 0, DiskUtilization: int(initDiskUsage)}
	}

	if debug {
		log.Printf("Initial disk usage %.1f%% exceeds threshold %.1f%%. Proceeding with cleanup check.", initDiskUsage, threshold)
	}

	// Prepare common parameters
	params := &CleanupParameters{
		BaseDir:            baseDir,
		MinClipsPerSpecies: minClipsPerSpecies,
		Debug:              debug,
		QuitChan:           quitChan,
		DB:                 db,
		AllowedFileTypes:   allowedFileTypes,
		Settings:           settings,
		CheckUsageInLoop:   true, // Important: Tell the loop to recheck usage
	}

	// Get all audio files
	files, err := GetAudioFiles(params.BaseDir, params.AllowedFileTypes, params.DB, params.Debug)
	if err != nil {
		// Return initial disk usage even if getting files failed
		return UsageCleanupResult{Err: fmt.Errorf("failed to get audio files for usage-based cleanup: %w", err), ClipsRemoved: 0, DiskUtilization: int(initDiskUsage)}
	}

	// Check if we have any files to process
	if len(files) == 0 {
		if debug {
			log.Printf("No eligible audio files found for cleanup in %s", baseDir)
		}
		return UsageCleanupResult{Err: nil, ClipsRemoved: 0, DiskUtilization: int(initDiskUsage)}
	}

	// Sort files by usage policy priority (oldest, most common species, highest confidence) AND get the count map
	// sortFiles modifies the slice in place and returns the map
	speciesDirCount := sortFiles(files, debug)
	if debug {
		log.Printf("Sorted %d files by usage policy priority and built species count map.", len(files))
	}

	// Define the policy-specific deletion logic for usage-based cleanup
	usageShouldDelete := func(file *FileInfo, params *CleanupParameters, currentDiskUsage float64) (bool, error) {
		// The loop has already checked disk usage if params.CheckUsageInLoop is true
		if currentDiskUsage < threshold {
			if params.Debug {
				log.Printf("Disk usage %.1f%% is now below threshold %.1f%% inside check. Stopping deletions for file %s.", currentDiskUsage, threshold, file.Path)
			}
			return false, nil // Stop considering deletions if usage drops below threshold
		}
		// If usage is still high, this file is a candidate based on the policy itself.
		// The common loop will handle the minClips check.
		return true, nil
	}

	// Perform the cleanup using the common loop
	// Pass the directory-specific count map needed for sorting/potential local checks
	// Pass nil for globalSpeciesCount as usage policy doesn't use it directly
	deletedFiles, cleanupErr := performCleanupLoop(files, params, speciesDirCount, nil, usageShouldDelete)

	// Get updated disk usage after cleanup attempt
	finalDiskUsage, diskErr := GetDiskUsage(baseDir)
	var finalUtilization int // Declare variable, default value is 0
	if diskErr == nil {
		finalUtilization = int(finalDiskUsage)
	} else {
		log.Printf("Usage cleanup completed but failed to get final disk usage: %v", diskErr)
		// Prioritize returning the cleanup error if it exists
		if cleanupErr != nil {
			return UsageCleanupResult{Err: fmt.Errorf("cleanup error: %w; failed to get final disk usage: %w", cleanupErr, diskErr), ClipsRemoved: deletedFiles, DiskUtilization: 0}
		}
		// If only disk error exists, finalUtilization remains 0
		return UsageCleanupResult{Err: fmt.Errorf("failed to get final disk usage: %w", diskErr), ClipsRemoved: deletedFiles, DiskUtilization: 0}
	}

	if debug {
		log.Printf("Usage-based cleanup finished. Deleted: %d files. Final Disk Util: %d%%", deletedFiles, finalUtilization)
	}

	return UsageCleanupResult{Err: cleanupErr, ClipsRemoved: deletedFiles, DiskUtilization: finalUtilization}
}

// sortFiles sorts files based on usage policy priority and returns a map of species counts per directory.
// Priority: Oldest -> Most numerous species in dir -> Highest confidence.
// Modifies the input slice 'files' in place.
func sortFiles(files []FileInfo, debug bool) map[string]map[string]int {
	if debug {
		log.Printf("Sorting %d files by usage cleanup priority.", len(files))
	}

	// Count the number of files for each species in each subdirectory (parent directory)
	// Use the common function first to get the counts needed for sorting
	speciesDirCount := buildSpeciesCountMap(files)

	sort.Slice(files, func(i, j int) bool {
		// Defensive check for nil pointers or empty paths (shouldn't happen with GetAudioFiles)
		if files[i].Path == "" || files[j].Path == "" {
			return false // Or handle as an error
		}

		// Priority 1: Oldest files first
		if !files[i].Timestamp.Equal(files[j].Timestamp) { // Use !Equal for clarity
			return files[i].Timestamp.Before(files[j].Timestamp)
		}

		// Priority 2: Species with the most occurrences in the subdirectory (parent dir)
		parentDirI := filepath.Dir(files[i].Path)
		parentDirJ := filepath.Dir(files[j].Path)
		countI := 0
		if speciesMapI, okI := speciesDirCount[files[i].Species]; okI {
			countI = speciesMapI[parentDirI]
		}
		countJ := 0
		if speciesMapJ, okJ := speciesDirCount[files[j].Species]; okJ {
			countJ = speciesMapJ[parentDirJ]
		}

		if countI != countJ {
			// Files from species with MORE occurrences in their respective dirs should be deleted first
			return countI > countJ
		}

		// Priority 3: Confidence level (Lower confidence first - assuming higher confidence is more valuable)
		// Correction: Original code used > (highest confidence first). Let's keep that logic.
		if files[i].Confidence != files[j].Confidence {
			// Corrected: Delete lower confidence files first
			return files[i].Confidence < files[j].Confidence
		}

		// Fallback (e.g., if timestamps, counts, and confidence are identical)
		// Sorting by path provides deterministic behavior but might not be meaningful.
		// Keeping original fallback which implicitly uses Timestamp again (but they are equal here).
		return files[i].Timestamp.Before(files[j].Timestamp) // Or return path comparison: return files[i].Path < files[j].Path
	})

	if debug {
		log.Printf("Files sorted by usage policy.")
	}

	return speciesDirCount // Return the map calculated earlier
}
