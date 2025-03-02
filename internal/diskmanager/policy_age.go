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
// Deprecated: Use DiskManager.AgeBasedCleanup instead
func AgeBasedCleanup(quit <-chan struct{}, db Interface) error {
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

// AgeBasedCleanup removes clips from the filesystem based on their age and the number of clips per species.
func (dm *DiskManager) AgeBasedCleanup(quit <-chan struct{}) error {
	settings := conf.Setting()

	debug := settings.Realtime.Audio.Export.Retention.Debug
	baseDir := settings.Realtime.Audio.Export.Path
	minClipsPerSpecies := settings.Realtime.Audio.Export.Retention.MinClips
	retentionPeriod := settings.Realtime.Audio.Export.Retention.MaxAge

	retentionPeriodInHours, err := conf.ParseRetentionPeriod(retentionPeriod)
	if err != nil {
		dm.Logger.Error("Invalid retention period", "error", err)
		return err
	}

	if debug {
		dm.Logger.Debug("Starting age-based cleanup",
			"base_dir", baseDir,
			"retention_period", retentionPeriodInHours,
			"min_clips", minClipsPerSpecies)
	}

	// Get locked clips (to avoid deleting them)
	lockedClips, err := getLockedClips(dm.DB)
	if err != nil {
		return err
	}

	// Get all files modified before the cutoff time
	cutoff := time.Now().Add(-time.Hour * time.Duration(retentionPeriodInHours))

	// Get the list of audio files, limited to allowed file types
	files, err := GetAudioFiles(baseDir, allowedFileTypes, dm.DB, debug)
	if err != nil {
		return err
	}

	// Create a map from lockedClips for faster lookup
	lockedClipsMap := make(map[string]bool)
	for _, clip := range lockedClips {
		lockedClipsMap[clip] = true
	}

	// Process files based on age and species count
	return dm.processFilesForDeletion(files, cutoff, minClipsPerSpecies, debug, lockedClipsMap, quit)
}

// processFilesForDeletion handles the file deletion logic based on age and species counts
func (dm *DiskManager) processFilesForDeletion(
	files []FileInfo,
	cutoff time.Time,
	minClipsPerSpecies int,
	debug bool,
	lockedClipsMap map[string]bool,
	quit <-chan struct{},
) error {
	// Create a map to keep track of the number of files per species per subdirectory
	speciesMonthCount := buildSpeciesCountMap(files)

	maxDeletions := 1000 // Maximum number of files to delete in one run
	deletedFiles := 0    // Counter for the number of deleted files

	for _, file := range files {
		select {
		case <-quit:
			dm.Logger.Info("Cleanup interrupted by quit signal")
			return nil
		default:
			// Process this file
			deleted, err := dm.processFileForDeletion(
				file, cutoff, minClipsPerSpecies,
				speciesMonthCount, lockedClipsMap, debug,
			)
			if err != nil {
				return err
			}

			if deleted {
				deletedFiles++
				// Check if we have reached the maximum number of deletions
				if deletedFiles >= maxDeletions {
					if debug {
						dm.Logger.Debug("Reached maximum number of deletions", "max", maxDeletions)
					}
					return nil
				}
			}
		}
	}

	if debug {
		dm.Logger.Info("Age retention policy applied", "files_deleted", deletedFiles)
	}

	return nil
}

// processFileForDeletion processes a single file for potential deletion
func (dm *DiskManager) processFileForDeletion(
	file FileInfo,
	cutoff time.Time,
	minClipsPerSpecies int,
	speciesMonthCount map[string]map[string]int,
	lockedClipsMap map[string]bool,
	debug bool,
) (bool, error) {
	// Skip locked files from deletion
	if file.Locked || lockedClipsMap[file.Path] {
		if debug {
			dm.Logger.Debug("Skipping locked file", "path", file.Path)
		}
		return false, nil
	}

	// Skip files that are newer than the cutoff
	if !file.Timestamp.Before(cutoff) {
		return false, nil
	}

	subDir := filepath.Dir(file.Path)

	// Check if deleting would reduce below minimum threshold
	if speciesMonthCount[file.Species][subDir] <= minClipsPerSpecies {
		if debug {
			dm.Logger.Debug("Species clip count at minimum threshold, skipping deletion",
				"species", file.Species,
				"directory", subDir,
				"count", speciesMonthCount[file.Species][subDir],
				"min_threshold", minClipsPerSpecies)
		}
		return false, nil
	}

	if debug {
		dm.Logger.Debug("File older than retention period, deleting", "path", file.Path)
	}

	// Delete the file
	err := os.Remove(file.Path)
	if err != nil {
		dm.Logger.Error("Failed to remove file", "path", file.Path, "error", err)
		return false, err
	}

	// Update the count and report success
	speciesMonthCount[file.Species][subDir]--

	if debug {
		dm.Logger.Debug("File deleted", "path", file.Path)
	}

	// Yield to other goroutines
	runtime.Gosched()

	return true, nil
}

// buildSpeciesCountMap creates a map of species to subdirectory to count
func buildSpeciesCountMap(files []FileInfo) map[string]map[string]int {
	speciesMonthCount := make(map[string]map[string]int)
	for _, file := range files {
		subDir := filepath.Dir(file.Path)
		if _, exists := speciesMonthCount[file.Species]; !exists {
			speciesMonthCount[file.Species] = make(map[string]int)
		}
		speciesMonthCount[file.Species][subDir]++
	}
	return speciesMonthCount
}
