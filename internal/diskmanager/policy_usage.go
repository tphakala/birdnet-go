// policy_usage.go - code for use retention policy
package diskmanager

import (
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
func UsageBasedCleanup(quitChan chan struct{}) error {
	settings := conf.Setting()

	debug := settings.Realtime.Audio.Export.Retention.Debug
	baseDir := settings.Realtime.Audio.Export.Path
	minClipsPerSpecies := settings.Realtime.Audio.Export.Retention.MinClips

	// Convert 80% string etc. to 80.0 float64
	threshold, err := conf.ParsePercentage(settings.Realtime.Audio.Export.Retention.MaxUsage)
	if err != nil {
		return err
	}

	// Only remove files with extensions in this list
	allowedExts := []string{".wav"}

	if debug {
		log.Printf("Starting cleanup process. Base directory: %s, Threshold: %.1f%%", baseDir, threshold)
	}

	// Check handle disk usage
	diskUsage, err := GetDiskUsage(baseDir)
	if err != nil {
		return err
	}

	if diskUsage > threshold {
		if debug {
			log.Printf("Disk usage %.1f%% is above the %.1f%% threshold. Cleanup needed.", diskUsage, threshold)
		}

		// Get the list of audio files
		files, err := GetAudioFiles(baseDir, allowedExts, debug)
		if err != nil {
			return err
		}

		// Sort files by the cleanup priority and get the initial count of files per species per subdirectory
		speciesMonthCount := sortFiles(files, debug)

		// Debug: write sorted files to a file
		if debug {
			if err := WriteSortedFilesToFile(files, "file_cleanup_order.txt"); err != nil {
				return err
			}
		}

		// Perform the cleanup
		return performCleanup(files, baseDir, threshold, minClipsPerSpecies, speciesMonthCount, debug, quitChan)
	} else if debug {
		log.Printf("Disk usage %.1f%% is below the %.1f%% threshold. No cleanup needed.", diskUsage, threshold)
	}

	return nil
}

func performCleanup(files []FileInfo, baseDir string, threshold float64, minClipsPerSpecies int, speciesMonthCount map[string]map[string]int, debug bool, quitChan chan struct{}) error {
	// Delete files until disk usage is below the threshold or 100 files have been deleted
	deletedFiles := 0
	maxDeletions := 1000
	totalFreedSpace := int64(0)

	for _, file := range files {
		select {
		case <-quitChan:
			log.Println("Received quit signal, ending cleanup run.")
			return nil
		default:
			// Get the subdirectory name
			subDir := filepath.Dir(file.Path)
			month := file.Timestamp.Format("2006-01")

			diskUsage, err := GetDiskUsage(baseDir)
			if err != nil {
				return err
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
			if debug {
				log.Printf("Deleting file: %s", file.Path)
			}

			// Delete the file deemed for cleanup
			err = os.Remove(file.Path)
			if err != nil {
				return err
			}

			// Increment deleted files count and update species count
			deletedFiles++
			speciesMonthCount[file.Species][subDir]--

			// Add file size to total freed space
			totalFreedSpace += file.Size

			if debug {
				log.Printf("File deleted. %d clips left for species %s in %s", speciesMonthCount[file.Species][subDir], file.Species, subDir)
			}

			// Yield to other goroutines
			runtime.Gosched()
		}
	}

	if debug {
		log.Printf("Usage retention policy applied, total files deleted: %d", deletedFiles)
	}

	return nil
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
