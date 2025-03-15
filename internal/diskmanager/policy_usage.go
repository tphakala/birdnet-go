// policy_usage.go - code for use retention policy
package diskmanager

import (
	"fmt"
	"log"
	"os"
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

// UsageBasedCleanup cleans up old audio files based on the configuration and monitors for quit signals
// Returns error, number of clips removed, and current disk utilization percentage.
func UsageBasedCleanup(quitChan chan struct{}, db Interface) (err error, clipsRemoved, diskUtilization int) {
	settings := conf.Setting()

	debug := settings.Realtime.Audio.Export.Retention.Debug
	baseDir := settings.Realtime.Audio.Export.Path
	minClipsPerSpecies := settings.Realtime.Audio.Export.Retention.MinClips

	// Convert 80% string etc. to 80.0 float64
	threshold, err := conf.ParsePercentage(settings.Realtime.Audio.Export.Retention.MaxUsage)
	if err != nil {
		return fmt.Errorf("failed to parse usage threshold: %w", err), 0, 0
	}

	if debug {
		log.Printf("Starting cleanup process. Base directory: %s, Threshold: %.1f%%", baseDir, threshold)
	}

	// Check handle disk usage
	diskUsage, err := GetDiskUsage(baseDir)
	if err != nil {
		return fmt.Errorf("failed to get disk usage for %s: %w", baseDir, err), 0, 0
	}

	deletedFiles := 0

	if diskUsage > threshold {
		if debug {
			log.Printf("Disk usage %.1f%% is above the %.1f%% threshold. Cleanup needed.", diskUsage, threshold)
		}

		// Get the list of audio files, limited to allowed file types defined in file_utils.go
		files, err := GetAudioFiles(baseDir, allowedFileTypes, db, debug)
		if err != nil {
			return fmt.Errorf("failed to get audio files for usage-based cleanup: %w", err), 0, int(diskUsage)
		}

		if len(files) == 0 {
			if debug {
				log.Printf("No eligible audio files found for cleanup in %s", baseDir)
			}
			return nil, 0, int(diskUsage)
		}

		// Sort files by priority and get species count per directory
		speciesMonthCount := sortFiles(files, debug)

		// Perform the cleanup
		deletedFiles, err = performCleanup(files, baseDir, threshold, minClipsPerSpecies, speciesMonthCount, debug, quitChan)
		if err != nil {
			return fmt.Errorf("error during usage-based cleanup: %w", err), deletedFiles, int(diskUsage)
		}

		// Get updated disk usage after cleanup
		diskUsage, err = GetDiskUsage(baseDir)
		if err != nil {
			return fmt.Errorf("cleanup completed but failed to get updated disk usage: %w", err), deletedFiles, 0
		}
	} else if debug {
		log.Printf("Disk usage %.1f%% is below the %.1f%% threshold. No cleanup needed.", diskUsage, threshold)
	}

	return nil, deletedFiles, int(diskUsage)
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
			month := file.Timestamp.Format("2006-01")

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

			if speciesMonthCount[file.Species][subDir] <= minClipsPerSpecies {
				if debug {
					log.Printf("Species clip count for %s in %s/%s is below the minimum threshold (%d). Skipping file deletion.", file.Species, month, subDir, minClipsPerSpecies)
				}
				continue
			}

			// Delete the file
			if debug {
				log.Printf("Deleting file: %s", file.Path)
			}

			err = os.Remove(file.Path)
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
			speciesMonthCount[file.Species][subDir]--

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

func sortFiles(files []FileInfo, debug bool) map[string]map[string]int {
	if debug {
		log.Printf("Sorting files by cleanup priority.")
	}

	// Count the number of files for each species in each subdirectory
	speciesMonthCount := make(map[string]map[string]int)
	for _, file := range files {
		subDir := filepath.Dir(file.Path)
		if _, exists := speciesMonthCount[file.Species]; !exists {
			speciesMonthCount[file.Species] = make(map[string]int)
		}
		speciesMonthCount[file.Species][subDir]++
	}

	sort.Slice(files, func(i, j int) bool {
		// Defensive check for nil pointers
		if files[i].Path == "" || files[j].Path == "" {
			return false
		}

		// Priority 1: Oldest files first
		if files[i].Timestamp != files[j].Timestamp {
			return files[i].Timestamp.Before(files[j].Timestamp)
		}

		// Priority 3: Species with the most occurrences in the subdirectory
		subDirI := filepath.Dir(files[i].Path)
		subDirJ := filepath.Dir(files[j].Path)
		if speciesMonthCount[files[i].Species][subDirI] != speciesMonthCount[files[j].Species][subDirJ] {
			return speciesMonthCount[files[i].Species][subDirI] > speciesMonthCount[files[j].Species][subDirJ]
		}

		// Priority 4: Confidence level
		if files[i].Confidence != files[j].Confidence {
			return files[i].Confidence > files[j].Confidence
		}

		// Default to oldest timestamp
		return files[i].Timestamp.Before(files[j].Timestamp)
	})

	if debug {
		log.Printf("Files sorted.")
	}

	return speciesMonthCount
}
