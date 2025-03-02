// policy_usage.go - code for use retention policy
package diskmanager

import (
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
func UsageBasedCleanup(quitChan chan struct{}, db Interface) error {
	// Initialize logger if it hasn't been initialized
	if diskLogger == nil {
		InitLogger()
	}

	settings := conf.Setting()

	debug := settings.Realtime.Audio.Export.Retention.Debug
	baseDir := settings.Realtime.Audio.Export.Path
	minClipsPerSpecies := settings.Realtime.Audio.Export.Retention.MinClips

	// Convert 80% string etc. to 80.0 float64
	threshold, err := conf.ParsePercentage(settings.Realtime.Audio.Export.Retention.MaxUsage)
	if err != nil {
		return err
	}

	if debug {
		diskLogger.Debug("Starting cleanup process",
			"base_dir", baseDir,
			"threshold", threshold)
	}

	// Check handle disk usage
	diskUsage, err := GetDiskUsage(baseDir)
	if err != nil {
		return err
	}

	if diskUsage > threshold {
		if debug {
			diskLogger.Debug("Disk usage above threshold",
				"usage", diskUsage,
				"threshold", threshold)
		}

		// Get the list of audio files, limited to allowed file types defined in file_utils.go
		files, err := GetAudioFiles(baseDir, allowedFileTypes, db, debug)
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
		diskLogger.Debug("Disk usage below threshold, no cleanup needed",
			"usage", diskUsage,
			"threshold", threshold)
	}

	return nil
}

func performCleanup(files []FileInfo, baseDir string, threshold float64, minClipsPerSpecies int, speciesMonthCount map[string]map[string]int, debug bool, quitChan chan struct{}) error {
	// Initialize logger if it hasn't been initialized
	if diskLogger == nil {
		InitLogger()
	}

	// Delete files until disk usage is below the threshold or 100 files have been deleted
	deletedFiles := 0
	maxDeletions := 1000
	totalFreedSpace := int64(0)

	for _, file := range files {
		select {
		case <-quitChan:
			diskLogger.Info("Received quit signal, ending cleanup run")
			return nil
		default:
			// Skip locked files
			if file.Locked {
				if debug {
					diskLogger.Debug("Skipping locked file", "path", file.Path)
				}
				continue
			}

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
				diskLogger.Debug("Species clip count",
					"species", file.Species,
					"count", speciesMonthCount[file.Species][subDir],
					"directory", subDir)
			}

			if speciesMonthCount[file.Species][subDir] <= minClipsPerSpecies {
				if debug {
					diskLogger.Debug("Species clip count below minimum threshold, skipping deletion",
						"species", file.Species,
						"month", month,
						"directory", subDir,
						"min_threshold", minClipsPerSpecies)
				}
				continue
			}
			if debug {
				diskLogger.Debug("Deleting file", "path", file.Path)
			}

			// Delete the file deemed for cleanup
			err = os.Remove(file.Path)
			if err != nil {
				diskLogger.Error("Failed to remove file", "path", file.Path, "error", err)
				return err
			}

			// Increment deleted files count and update species count
			deletedFiles++
			speciesMonthCount[file.Species][subDir]--

			// Add file size to total freed space
			totalFreedSpace += file.Size

			if debug {
				diskLogger.Debug("File deleted",
					"remaining_clips", speciesMonthCount[file.Species][subDir],
					"species", file.Species,
					"directory", subDir)
			}

			// Yield to other goroutines
			runtime.Gosched()
		}
	}

	if debug {
		diskLogger.Info("Usage retention policy applied",
			"files_deleted", deletedFiles,
			"space_freed", totalFreedSpace)
	}

	return nil
}

func sortFiles(files []FileInfo, debug bool) map[string]map[string]int {
	// Initialize logger if it hasn't been initialized
	if diskLogger == nil {
		InitLogger()
	}

	if debug {
		diskLogger.Debug("Sorting files by cleanup priority")
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
		diskLogger.Debug("Files sorted")
	}

	return speciesMonthCount
}
