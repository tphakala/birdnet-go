// policy_usage.go - code for use retention policy
package diskmanager

import (
	"fmt"
	"log"
	"path/filepath"
	"runtime"
	"sort"
	"time"

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

	// Convert 80% string etc. to 80.0 float64
	threshold, err := conf.ParsePercentage(settings.Realtime.Audio.Export.Retention.MaxUsage)
	if err != nil {
		return UsageCleanupResult{Err: fmt.Errorf("failed to parse usage threshold: %w", err), ClipsRemoved: 0, DiskUtilization: 0}
	}

	if debug {
		log.Printf("Starting cleanup process. Base directory: %s, Threshold: %.1f%%", baseDir, threshold)
	}

	// Get current disk usage
	diskUsage, err := GetDiskUsage(baseDir)
	if err != nil {
		return UsageCleanupResult{Err: fmt.Errorf("failed to get disk usage for %s: %w", baseDir, err), ClipsRemoved: 0, DiskUtilization: int(diskUsage)}
	}

	// Only perform cleanup if disk usage exceeds threshold
	var deletedFiles int
	if diskUsage > threshold {
		// Get all audio files
		files, err := GetAudioFiles(baseDir, allowedFileTypes, db, debug)
		if err != nil {
			return UsageCleanupResult{Err: fmt.Errorf("failed to get audio files for usage-based cleanup: %w", err), ClipsRemoved: 0, DiskUtilization: int(diskUsage)}
		}

		// Check if we have any files to process
		if len(files) == 0 {
			if debug {
				log.Printf("No eligible audio files found for cleanup in %s", baseDir)
			}
			return UsageCleanupResult{Err: nil, ClipsRemoved: 0, DiskUtilization: int(diskUsage)}
		}

		// Sort files by timestamp (oldest first) and build species count map
		// The sorting itself is specific to usage policy, but map building is common
		speciesMonthCount := buildSpeciesSubDirCountMap(files) // Use common function
		sortFilesForUsage(files, speciesMonthCount, debug)     // Keep usage-specific sorting logic separate

		// Perform the cleanup
		deletedFiles, err = performCleanup(files, baseDir, threshold, minClipsPerSpecies, speciesMonthCount, debug, quitChan)
		if err != nil {
			return UsageCleanupResult{Err: fmt.Errorf("error during usage-based cleanup: %w", err), ClipsRemoved: deletedFiles, DiskUtilization: int(diskUsage)}
		}

		// Get updated disk usage after cleanup
		diskUsage, err = GetDiskUsage(baseDir)
		if err != nil {
			return UsageCleanupResult{Err: fmt.Errorf("cleanup completed but failed to get updated disk usage: %w", err), ClipsRemoved: deletedFiles, DiskUtilization: 0}
		}
	} else if debug {
		log.Printf("Disk usage %.1f%% is below the %.1f%% threshold. No cleanup needed.", diskUsage, threshold)
	}

	return UsageCleanupResult{Err: nil, ClipsRemoved: deletedFiles, DiskUtilization: int(diskUsage)}
}

func performCleanup(files []FileInfo, baseDir string, threshold float64, minClipsPerSpecies int,
	speciesMonthCount map[string]map[string]int, debug bool,
	quitChan chan struct{}) (int, error) {
	// Delete files until disk usage is below the threshold or 100 files have been deleted
	deletedFiles := 0
	maxDeletions := 1000
	totalFreedSpace := int64(0)
	errorCount := 0 // Counter for deletion errors

	for _, file := range files {
		select {
		case <-quitChan:
			log.Println("Received quit signal, ending cleanup run.")
			return deletedFiles, nil
		default:
			// Skip locked files
			if file.Locked {
				if debug {
					log.Printf("Skipping locked file: %s", file.Path)
				}
				continue
			}

			// Get the subdirectory name
			subDir := filepath.Dir(file.Path)

			diskUsage, err := GetDiskUsage(baseDir)
			if err != nil {
				return deletedFiles, fmt.Errorf("failed to get disk usage during cleanup: %w", err)
			}

			// Check if disk usage is below threshold or max deletions reached
			if diskUsage < threshold || deletedFiles >= maxDeletions {
				// all done for now, exit select loop
				break
			}

			if debug {
				log.Printf("Species %s has %d clips in %s", file.Species, speciesMonthCount[file.Species][subDir], subDir)
			}

			// Use common function to check min clips constraint
			if !checkMinClips(&file, subDir, speciesMonthCount, minClipsPerSpecies, debug) {
				continue
			}

			// Sleep a while to throttle the cleanup
			time.Sleep(100 * time.Millisecond)

			// Delete the file using common function
			if debug {
				log.Printf("Deleting file based on usage policy: %s", file.Path)
			}

			err = deleteAudioFile(&file, debug)
			if err != nil {
				errorCount++
				log.Printf("Failed to remove %s: %s", file.Path, err)
				// Continue with other files instead of stopping the entire cleanup
				if errorCount > 10 {
					return deletedFiles, fmt.Errorf("too many errors (%d) during usage-based cleanup, last error: %w", errorCount, err)
				}
				continue
			}

			// Update counters
			deletedFiles++
			totalFreedSpace += file.Size

			if debug {
				log.Printf("Deleted file: %s, freed %d bytes", file.Path, file.Size)
			}

			// Yield to other goroutines
			runtime.Gosched()
		}
	}

	if debug {
		log.Printf("Cleanup completed. Deleted %d files, freed %d bytes", deletedFiles, totalFreedSpace)
	}

	return deletedFiles, nil
}

// sortFilesForUsage sorts files specifically for the usage-based policy and builds the species count map.
func sortFilesForUsage(files []FileInfo, speciesMonthCount map[string]map[string]int, debug bool) {
	if debug {
		log.Printf("Sorting files by usage cleanup priority.")
	}

	// Count the number of files for each species in each subdirectory - Moved to buildSpeciesSubDirCountMap
	/*
	   speciesMonthCount := make(map[string]map[string]int)
	   for _, file := range files {
	       subDir := filepath.Dir(file.Path)
	       if _, exists := speciesMonthCount[file.Species]; !exists {
	           speciesMonthCount[file.Species] = make(map[string]int)
	       }
	       speciesMonthCount[file.Species][subDir]++
	   }
	*/

	sort.Slice(files, func(i, j int) bool {
		// Defensive check for nil pointers - FileInfo is not a pointer slice
		/*
		   if files[i].Path == "" || files[j].Path == "" {
		       return false
		   }
		*/

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

	// Map is now built externally
	// return speciesMonthCount
}
