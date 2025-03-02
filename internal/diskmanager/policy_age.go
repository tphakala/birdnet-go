// policy_age.go - code for age retention policy
package diskmanager

import (
	"os"
	"path/filepath"
	"runtime"
	"time"

	"github.com/tphakala/birdnet-go/internal/conf"
)

// AgeBasedCleanup removes clips from the filesystem based on their age and the number of clips per species.
func AgeBasedCleanup(quit <-chan struct{}, db Interface) error {
	// Initialize logger if it hasn't been initialized
	if diskLogger == nil {
		InitLogger()
	}

	settings := conf.Setting()

	debug := settings.Realtime.Audio.Export.Retention.Debug
	baseDir := settings.Realtime.Audio.Export.Path
	minClipsPerSpecies := settings.Realtime.Audio.Export.Retention.MinClips
	retentionPeriod := settings.Realtime.Audio.Export.Retention.MaxAge

	retentionPeriodInHours, err := conf.ParseRetentionPeriod(retentionPeriod)
	if err != nil {
		diskLogger.Error("Invalid retention period", "error", err)
		return err
	}

	if debug {
		diskLogger.Debug("Starting age-based cleanup",
			"base_dir", baseDir,
			"retention_period", retentionPeriod)
	}

	// Get the list of audio files, limited to allowed file types defined in file_utils.go
	files, err := GetAudioFiles(baseDir, allowedFileTypes, db, debug)
	if err != nil {
		return err
	}

	// Create a map to keep track of the number of files per species per subdirectory
	speciesMonthCount := make(map[string]map[string]int)
	for _, file := range files {
		subDir := filepath.Dir(file.Path)
		if _, exists := speciesMonthCount[file.Species]; !exists {
			speciesMonthCount[file.Species] = make(map[string]int)
		}
		speciesMonthCount[file.Species][subDir]++
	}

	expirationTime := time.Now().Add(-time.Duration(retentionPeriodInHours) * time.Hour)

	maxDeletions := 1000 // Maximum number of files to delete in one run
	deletedFiles := 0    // Counter for the number of deleted files

	for _, file := range files {
		select {
		case <-quit:
			diskLogger.Info("Cleanup interrupted by quit signal")
			return nil
		default:
			// Skip locked files from deletion
			if file.Locked {
				if debug {
					diskLogger.Debug("Skipping locked file", "path", file.Path)
				}
				continue
			}

			if file.Timestamp.Before(expirationTime) {
				subDir := filepath.Dir(file.Path)

				if speciesMonthCount[file.Species][subDir] <= minClipsPerSpecies {
					if debug {
						diskLogger.Debug("Species clip count at minimum threshold, skipping deletion",
							"species", file.Species,
							"directory", subDir,
							"count", speciesMonthCount[file.Species][subDir],
							"min_threshold", minClipsPerSpecies)
					}
					continue
				}

				if debug {
					diskLogger.Debug("File older than retention period, deleting", "path", file.Path)
				}

				err = os.Remove(file.Path)
				if err != nil {
					diskLogger.Error("Failed to remove file", "path", file.Path, "error", err)
					return err
				}

				speciesMonthCount[file.Species][subDir]--
				deletedFiles++

				if debug {
					diskLogger.Debug("File deleted", "path", file.Path)
				}

				// Yield to other goroutines
				runtime.Gosched()

				// Check if we have reached the maximum number of deletions
				if deletedFiles >= maxDeletions {
					if debug {
						diskLogger.Debug("Reached maximum number of deletions", "max", maxDeletions)
					}
					return nil
				}
			}
		}
	}

	if debug {
		diskLogger.Info("Age retention policy applied", "files_deleted", deletedFiles)
	}

	return nil
}
