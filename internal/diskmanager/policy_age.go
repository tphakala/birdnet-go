// policy_age.go - code for age retention policy
package diskmanager

import (
	"fmt"
	"log"
	"sort" // Added for sorting
	"time"

	"github.com/tphakala/birdnet-go/internal/conf"
)

// AgeCleanupResult contains the results of an age-based cleanup operation
type AgeCleanupResult struct {
	Err             error // Any error that occurred during cleanup
	ClipsRemoved    int   // Number of clips that were removed
	DiskUtilization int   // Current disk utilization percentage after cleanup
}

// AgeBasedCleanup removes clips from the filesystem based on their age and the number of clips per species.
// Returns an AgeCleanupResult containing error, number of clips removed, and current disk utilization percentage.
func AgeBasedCleanup(quit <-chan struct{}, db Interface) AgeCleanupResult {
	settings := conf.Setting() // Use the singleton getter

	debug := settings.Realtime.Audio.Export.Retention.Debug
	baseDir := settings.Realtime.Audio.Export.Path
	minClipsPerSpecies := settings.Realtime.Audio.Export.Retention.MinClips
	retentionPeriodStr := settings.Realtime.Audio.Export.Retention.MaxAge

	retentionPeriodInHours, err := conf.ParseRetentionPeriod(retentionPeriodStr)
	if err != nil {
		log.Printf("Invalid retention period format '%s': %v\n", retentionPeriodStr, err)
		// Attempt to get disk usage even on config error, if possible
		diskUsage, diskErr := GetDiskUsage(baseDir)
		utilization := 0
		if diskErr == nil {
			utilization = int(diskUsage)
		}
		return AgeCleanupResult{Err: fmt.Errorf("invalid retention period format '%s': %w", retentionPeriodStr, err), ClipsRemoved: 0, DiskUtilization: utilization}
	}

	expirationTime := time.Now().Add(-time.Duration(retentionPeriodInHours) * time.Hour)

	if debug {
		log.Printf("Starting age-based cleanup process. Base directory: %s, Retention period: %s (expires before %s)",
			baseDir, retentionPeriodStr, expirationTime.Format(time.RFC3339))
	}

	// Prepare common parameters
	params := &CleanupParameters{
		BaseDir:            baseDir,
		MinClipsPerSpecies: minClipsPerSpecies,
		Debug:              debug,
		QuitChan:           quit,
		DB:                 db,
		AllowedFileTypes:   allowedFileTypes, // Assumes allowedFileTypes is accessible (defined in file_utils.go)
		Settings:           settings,
	}

	// Get the list of audio files
	files, err := GetAudioFiles(params.BaseDir, params.AllowedFileTypes, params.DB, params.Debug)
	if err != nil {
		// Try to get disk usage even if getting files failed
		diskUsage, diskErr := GetDiskUsage(baseDir)
		utilization := 0
		if diskErr == nil {
			utilization = int(diskUsage)
		}
		return AgeCleanupResult{Err: fmt.Errorf("failed to get audio files for age-based cleanup: %w", err), ClipsRemoved: 0, DiskUtilization: utilization}
	}

	if len(files) == 0 {
		if debug {
			log.Printf("No eligible audio files found for age-based cleanup in %s", baseDir)
		}
		diskUsage, diskErr := GetDiskUsage(baseDir)
		utilization := 0
		if diskErr == nil {
			utilization = int(diskUsage)
		}
		return AgeCleanupResult{Err: nil, ClipsRemoved: 0, DiskUtilization: utilization}
	}

	// --- Sort files by timestamp (oldest first) ---
	// This addresses the potential bug where newer files might be deleted before older ones if both met the age criteria.
	sort.Slice(files, func(i, j int) bool {
		return files[i].Timestamp.Before(files[j].Timestamp)
	})
	if debug {
		log.Printf("Sorted %d files by timestamp (oldest first).", len(files))
		// Optionally log the first few files to confirm sorting
		// for k := 0; k < min(5, len(files)); k++ {
		//  log.Printf("  - File %d: %s (%s)", k, files[k].Path, files[k].Timestamp)
		// }
	}

	// Create a map to keep track of the number of files per species per subdirectory
	// Use the common function now - // Correction: Age policy needs GLOBAL count.
	// speciesDirCount := buildSpeciesCountMap(files) // Not needed for age policy's global check
	globalSpeciesCount := buildGlobalSpeciesCountMap(files)
	if debug {
		log.Printf("Built global species count map. Found %d distinct species overall.", len(globalSpeciesCount))
		// Optional: Log counts for a few species
		// count := 0
		// for species, num := range globalSpeciesCount {
		//  log.Printf("  - Species: %s, Count: %d", species, num)
		//  count++
		//  if count >= 5 { break }
		// }
	}

	// Define the policy-specific deletion logic for age-based cleanup
	// Update signature to match ShouldDeleteFunc
	ageShouldDelete := func(file *FileInfo, params *CleanupParameters, currentDiskUsage float64) (bool, error) {
		isExpired := file.Timestamp.Before(expirationTime)
		if params.Debug && isExpired {
			log.Printf("File %s is older than retention period (%s). Candidate for deletion.", file.Path, expirationTime.Format(time.RFC3339))
		}
		// Age policy doesn't use currentDiskUsage, but must match signature
		return isExpired, nil
	}

	// Call the common cleanup loop
	// Pass nil for speciesDirCount as age policy uses global count check inside the loop
	deletedCount, cleanupErr := performCleanupLoop(files, params, nil, globalSpeciesCount, ageShouldDelete)

	// Get final disk utilization after cleanup attempt
	finalDiskUsage, diskErr := GetDiskUsage(baseDir)
	var finalUtilization int // Declare variable, default value is 0
	if diskErr == nil {
		finalUtilization = int(finalDiskUsage)
	} else {
		log.Printf("Age cleanup completed but failed to get final disk usage: %v", diskErr)
		// Return the cleanup error if it exists, otherwise the disk usage error
		if cleanupErr != nil {
			return AgeCleanupResult{Err: fmt.Errorf("cleanup error: %w; failed to get final disk usage: %w", cleanupErr, diskErr), ClipsRemoved: deletedCount, DiskUtilization: 0}
		}
		// If only disk error exists, finalUtilization remains 0
		return AgeCleanupResult{Err: fmt.Errorf("failed to get final disk usage: %w", diskErr), ClipsRemoved: deletedCount, DiskUtilization: 0}
	}

	if debug {
		log.Printf("Age-based cleanup finished. Deleted: %d files. Final Disk Util: %d%%", deletedCount, finalUtilization)
	}

	// Return the result, prioritizing the cleanup error if one occurred
	return AgeCleanupResult{Err: cleanupErr, ClipsRemoved: deletedCount, DiskUtilization: finalUtilization}
}
