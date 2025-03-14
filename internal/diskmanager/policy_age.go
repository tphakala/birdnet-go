// policy_age.go - code for age retention policy
package diskmanager

import (
	"fmt"
	"log"
	"os"
	"path/filepath"
	"runtime"
	"time"

	"github.com/tphakala/birdnet-go/internal/conf"
)

// AgeBasedCleanup removes clips from the filesystem based on their age and the number of clips per species.
func AgeBasedCleanup(quit <-chan struct{}, db Interface) error {
	settings := conf.Setting()

	debug := settings.Realtime.Audio.Export.Retention.Debug
	baseDir := settings.Realtime.Audio.Export.Path
	minClipsPerSpecies := settings.Realtime.Audio.Export.Retention.MinClips
	retentionPeriod := settings.Realtime.Audio.Export.Retention.MaxAge

	retentionPeriodInHours, err := conf.ParseRetentionPeriod(retentionPeriod)
	if err != nil {
		log.Printf("Invalid retention period: %s\n", err)
		return err
	}

	if debug {
		log.Printf("Starting age-based cleanup process. Base directory: %s, Retention period: %s", baseDir, retentionPeriod)
	}

	// Get the list of audio files, limited to allowed file types defined in file_utils.go
	files, err := GetAudioFiles(baseDir, allowedFileTypes, db, debug)
	if err != nil {
		return fmt.Errorf("failed to get audio files for age-based cleanup: %w", err)
	}

	if len(files) == 0 {
		if debug {
			log.Printf("No eligible audio files found for cleanup in %s", baseDir)
		}
		return nil
	}

	// Create a map to keep track of the number of files per species per subdirectory
	speciesMonthCount := buildSpeciesCountMap(files)

	expirationTime := time.Now().Add(-time.Duration(retentionPeriodInHours) * time.Hour)

	return processFiles(files, speciesMonthCount, expirationTime, minClipsPerSpecies, debug, quit)
}

// buildSpeciesCountMap creates a map to track the number of files per species per subdirectory
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

// processFiles handles the deletion of expired files while respecting constraints
func processFiles(files []FileInfo, speciesMonthCount map[string]map[string]int,
	expirationTime time.Time, minClipsPerSpecies int, debug bool, quit <-chan struct{}) error {

	maxDeletions := 1000 // Maximum number of files to delete in one run
	deletedFiles := 0    // Counter for the number of deleted files
	errorCount := 0      // Counter for deletion errors

	for i := range files {
		select {
		case <-quit:
			log.Printf("Cleanup interrupted by quit signal\n")
			return nil
		default:
			file := &files[i]
			if shouldSkipFile(file, debug) {
				continue
			}

			if !file.Timestamp.Before(expirationTime) {
				continue
			}

			subDir := filepath.Dir(file.Path)
			if !canDeleteFile(file, subDir, speciesMonthCount, minClipsPerSpecies, debug) {
				continue
			}

			if err := deleteFile(file, debug); err != nil {
				errorCount++
				log.Printf("Failed to remove %s: %s\n", file.Path, err)
				if errorCount > 10 {
					return fmt.Errorf("too many errors (%d) during age-based cleanup, last error: %w", errorCount, err)
				}
				continue
			}

			speciesMonthCount[file.Species][subDir]--
			deletedFiles++

			// Yield to other goroutines
			runtime.Gosched()

			if deletedFiles >= maxDeletions {
				if debug {
					log.Printf("Reached maximum number of deletions (%d). Ending cleanup.", maxDeletions)
				}
				return nil
			}
		}
	}

	if debug {
		log.Printf("Age retention policy applied, total files deleted: %d", deletedFiles)
	}

	return nil
}

// shouldSkipFile checks if a file should be skipped (e.g., if it's locked)
func shouldSkipFile(file *FileInfo, debug bool) bool {
	if file.Locked {
		if debug {
			log.Printf("Skipping locked file: %s", file.Path)
		}
		return true
	}
	return false
}

// canDeleteFile checks if a file can be deleted based on species count constraints
func canDeleteFile(file *FileInfo, subDir string, speciesMonthCount map[string]map[string]int,
	minClipsPerSpecies int, debug bool) bool {

	if speciesMonthCount[file.Species][subDir] <= minClipsPerSpecies {
		if debug {
			log.Printf("Species clip count for %s in %s is at the minimum threshold (%d). Skipping file deletion.",
				file.Species, subDir, minClipsPerSpecies)
		}
		return false
	}

	if debug {
		log.Printf("File %s is older than retention period, deleting.", file.Path)
	}

	return true
}

// deleteFile removes a file from the filesystem
func deleteFile(file *FileInfo, debug bool) error {
	err := os.Remove(file.Path)
	if err != nil {
		return err
	}

	if debug {
		log.Printf("File %s deleted", file.Path)
	}

	return nil
}
