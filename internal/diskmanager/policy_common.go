// policy_common.go - shared code for cleanup policies
package diskmanager

import (
	"fmt"
	"log"
	"os"
	"path/filepath"
	"runtime"
	"time"
)

// buildSpeciesSubDirCountMap creates a map to track the number of files per species per subdirectory.
func buildSpeciesSubDirCountMap(files []FileInfo) map[string]map[string]int {
	speciesCount := make(map[string]map[string]int)
	for _, file := range files {
		subDir := filepath.Dir(file.Path)
		if _, exists := speciesCount[file.Species]; !exists {
			speciesCount[file.Species] = make(map[string]int)
		}
		speciesCount[file.Species][subDir]++
	}
	return speciesCount
}

// checkLocked checks if a file should be skipped because it's locked.
func checkLocked(file *FileInfo, debug bool) bool {
	if file.Locked {
		if debug {
			log.Printf("Skipping locked file: %s", file.Path)
		}
		return true // Indicates the file should be skipped
	}
	return false // Indicates the file should NOT be skipped
}

// checkMinClips checks if a file can be deleted based on the minimum clips per species constraint.
// Returns true if deletion is allowed, false otherwise.
func checkMinClips(file *FileInfo, subDir string, speciesCount map[string]map[string]int,
	minClipsPerSpecies int, debug bool) bool {

	// Ensure the species and subdirectory exist in the map
	if speciesMap, ok := speciesCount[file.Species]; ok {
		if count, ok := speciesMap[subDir]; ok {
			if count <= minClipsPerSpecies {
				if debug {
					log.Printf("Species clip count for %s in %s is at the minimum threshold (%d). Skipping file deletion.",
						file.Species, subDir, minClipsPerSpecies)
				}
				return false // Cannot delete
			}
		} else {
			// Should not happen if map is built correctly, but handle defensively
			if debug {
				log.Printf("Warning: Subdirectory %s not found in species count map for species %s.", subDir, file.Species)
			}
			return false // Cannot determine count, safer not to delete
		}
	} else {
		// Should not happen if map is built correctly, but handle defensively
		if debug {
			log.Printf("Warning: Species %s not found in species count map.", file.Species)
		}
		return false // Cannot determine count, safer not to delete
	}

	return true // Can delete
}

// deleteAudioFile removes a file from the filesystem and logs the action.
func deleteAudioFile(file *FileInfo, debug bool) error {
	err := os.Remove(file.Path)
	if err != nil {
		// Error logging happens in the calling function
		return err
	}

	if debug {
		log.Printf("File %s deleted", file.Path)
	}

	return nil
}

// CleanupResult contains the results of a cleanup operation
type CleanupResult struct {
	Err             error // Any error that occurred during cleanup
	ClipsRemoved    int   // Number of clips that were removed
	DiskUtilization int   // Current disk utilization percentage after cleanup
}

// ShouldDeleteFunc defines the signature for policy-specific deletion checks.
// It should return true if the file should be deleted based on the policy.
// It can optionally return a reason string for logging.
type ShouldDeleteFunc func(file *FileInfo) (shouldDelete bool, reason string)

// processFilesGeneric provides a common loop structure for processing and potentially deleting files.
// It takes a list of files, common parameters, the species count map, and a policy-specific
// function (shouldDeleteCheck) to determine if a file meets the criteria for deletion.
func processFilesGeneric(files []FileInfo, speciesCount map[string]map[string]int,
	minClipsPerSpecies int, maxDeletions int, debug bool, quit <-chan struct{},
	shouldDeleteCheck ShouldDeleteFunc) (int, error) {

	deletedFiles := 0 // Counter for the number of deleted files
	errorCount := 0   // Counter for deletion errors

	for i := range files {
		select {
		case <-quit:
			log.Printf("Cleanup interrupted by quit signal\n")
			return deletedFiles, nil // Return immediately on quit signal
		default:
			// Check if max deletions reached (common exit condition)
			if deletedFiles >= maxDeletions {
				if debug {
					log.Printf("Reached maximum number of deletions (%d). Ending generic cleanup loop.", maxDeletions)
				}
				return deletedFiles, nil
			}

			file := &files[i] // Use pointer to modify original slice elements if needed (though not currently used)

			// 1. Check if locked (common check)
			if checkLocked(file, debug) {
				continue
			}

			// 2. Perform policy-specific check
			shouldDelete, reason := shouldDeleteCheck(file)
			if !shouldDelete {
				continue // Skip if policy check fails
			}

			// 3. Check minimum clips constraint (common check)
			subDir := filepath.Dir(file.Path)
			if !checkMinClips(file, subDir, speciesCount, minClipsPerSpecies, debug) {
				continue
			}

			// Throttle slightly before deletion
			time.Sleep(100 * time.Millisecond)

			// Log intent before deleting (using policy-specific reason)
			if debug {
				log.Printf("Deleting file based on policy (%s): %s", reason, file.Path)
			}

			// 4. Delete the file (common action)
			if err := deleteAudioFile(file, debug); err != nil {
				errorCount++
				log.Printf("Failed to remove %s: %s\n", file.Path, err)
				// Common error handling: stop after too many errors
				if errorCount > 10 {
					return deletedFiles, fmt.Errorf("too many errors (%d) during cleanup, last error: %w", errorCount, err)
				}
				continue // Continue with the next file even if one fails
			}

			// 5. Update state (common updates)
			speciesCount[file.Species][subDir]-- // Decrement count *after* successful deletion
			deletedFiles++

			// Yield to other goroutines (common)
			runtime.Gosched()
		}
	}

	if debug {
		log.Printf("Generic cleanup loop finished. Total files deleted in this run: %d", deletedFiles)
	}

	return deletedFiles, nil
}
