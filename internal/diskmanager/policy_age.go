// policy_age.go - code for age retention policy
package diskmanager

import (
	"fmt"
	"log"
	"sort"
	"time"

	"github.com/tphakala/birdnet-go/internal/conf"
)

// AgeBasedCleanup removes clips from the filesystem based on their age and the number of clips per species.
// Returns a CleanupResult containing error, number of clips removed, and current disk utilization percentage.
func AgeBasedCleanup(quit <-chan struct{}, db Interface) CleanupResult { // Use common result type
	settings := conf.Setting()

	debug := settings.Realtime.Audio.Export.Retention.Debug
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

	// Create a map to keep track of the number of files per species per subdirectory
	speciesMonthCount := buildSpeciesSubDirCountMap(files)

	// Sort files: Oldest first, then lowest confidence first as a tie-breaker
	sort.SliceStable(files, func(i, j int) bool {
		if !files[i].Timestamp.Equal(files[j].Timestamp) {
			return files[i].Timestamp.Before(files[j].Timestamp) // Oldest first
		}
		return files[i].Confidence < files[j].Confidence // Lowest confidence first
	})

	// Calculate the expiration time for age-based cleanup
	expirationTime := time.Now().Add(-time.Duration(retentionPeriodInHours) * time.Hour)

	// Define the age-specific deletion check function
	ageCheck := func(file *FileInfo) (bool, string) {
		if file.Timestamp.Before(expirationTime) {
			return true, "older than retention period"
		}
		return false, ""
	}

	// Max deletions per run (moved from old processFiles)
	maxDeletions := 1000

	// Call the generic processing loop with the age check
	deletedCount, err := processFilesGeneric(files, speciesMonthCount,
		minClipsPerSpecies, maxDeletions, debug, quit,
		ageCheck)

	// Get current disk utilization after cleanup
	diskUsage, diskErr := GetDiskUsage(baseDir)
	if diskErr != nil {
		return CleanupResult{Err: fmt.Errorf("cleanup completed but failed to get disk usage: %w", diskErr), ClipsRemoved: deletedCount, DiskUtilization: 0} // Use common result type
	}

	return CleanupResult{Err: err, ClipsRemoved: deletedCount, DiskUtilization: int(diskUsage)} // Use common result type
}
