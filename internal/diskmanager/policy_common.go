// policy_common.go - shared code for retention policies
package diskmanager

import (
	"fmt"
	"log"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"time"

	// Needed for Gosched
	// Needed for time operations
	// Added for potential use in common loop
	"github.com/tphakala/birdnet-go/internal/conf"
)

// CleanupParameters holds common parameters for cleanup operations.
type CleanupParameters struct {
	BaseDir            string
	MinClipsPerSpecies int
	Debug              bool
	QuitChan           <-chan struct{}
	DB                 Interface
	AllowedFileTypes   []string
	Settings           *conf.Settings // Corrected type name
	CheckUsageInLoop   bool           // Flag for usage policy to recheck disk usage
}

// PolicyCleanupResult holds common results from cleanup operations.
type PolicyCleanupResult struct {
	Err             error // Any error that occurred during cleanup
	ClipsRemoved    int   // Number of clips that were removed
	DiskUtilization int   // Current disk utilization percentage after cleanup
}

// buildSpeciesCountMap creates a map to track the number of files per species per subdirectory (Month).
// It assumes the subdirectory structure represents the month (e.g., YYYY-MM/Species).
// NOTE: The original logic used filepath.Dir which might just be the species name if files are directly in the species folder.
// Assuming the intent is per-species count within its immediate parent directory.
func buildSpeciesCountMap(files []FileInfo) map[string]map[string]int {
	speciesDirCount := make(map[string]map[string]int)
	for _, file := range files {
		// Use filepath.Dir to get the immediate parent directory containing the file.
		// This directory likely corresponds to the species name folder.
		parentDir := filepath.Dir(file.Path)
		if _, exists := speciesDirCount[file.Species]; !exists {
			speciesDirCount[file.Species] = make(map[string]int)
		}
		speciesDirCount[file.Species][parentDir]++
	}
	return speciesDirCount
}

// buildGlobalSpeciesCountMap creates a map to track the total number of files per species.
func buildGlobalSpeciesCountMap(files []FileInfo) map[string]int {
	globalSpeciesCount := make(map[string]int)
	for _, file := range files {
		globalSpeciesCount[file.Species]++
	}
	return globalSpeciesCount
}

// ShouldDeleteFunc defines the signature for policy-specific deletion logic.
// Return `true` if the file should be deleted according to the policy.
// The second return value is an error, if policy evaluation failed.
type ShouldDeleteFunc func(file *FileInfo, params *CleanupParameters, currentDiskUsage float64) (shouldDelete bool, err error)

// deleteFile removes a file from the filesystem and attempts to remove its corresponding .png file
// based on the KeepSpectrograms setting.
func deleteFile(file *FileInfo, params *CleanupParameters) error {
	debug := params.Debug // Get debug flag from params
	if debug {
		log.Printf("Attempting to delete audio file: %s", file.Path)
	}
	audioPath := file.Path
	err := os.Remove(audioPath)
	if err != nil {
		log.Printf("Failed to remove audio file %s: %v", audioPath, err)
		return err // Return the error for the primary file
	}

	if debug {
		log.Printf("Audio file %s deleted successfully", audioPath)
	}

	// Attempt to delete the corresponding .png file only if KeepSpectrograms is false
	if !params.Settings.Realtime.Audio.Export.Retention.KeepSpectrograms {
		baseName := strings.TrimSuffix(audioPath, filepath.Ext(audioPath))
		pngPath := baseName + ".png"

		if _, err := os.Stat(pngPath); err == nil {
			if debug {
				log.Printf("Attempting to delete corresponding image file: %s (KeepSpectrograms=false)", pngPath)
			}
			if pngErr := os.Remove(pngPath); pngErr != nil {
				// Log error but don't return it as fatal for the overall deletion operation
				log.Printf("Warning: Failed to remove corresponding image file %s: %v", pngPath, pngErr)
			} else if debug {
				log.Printf("Image file %s deleted successfully", pngPath)
			}
		} else if debug && !os.IsNotExist(err) {
			// Log if stat failed for a reason other than not existing
			log.Printf("Warning: Could not stat corresponding image file %s: %v", pngPath, err)
		}
	} else if debug {
		log.Printf("Skipping image file deletion for %s (KeepSpectrograms=true)", audioPath)
	}

	return nil // Return nil as the primary audio file was deleted
}

// canDeleteBasedOnMinClips checks if a file can be deleted based on species count constraints within its directory.
// Kept for potential use by usage policy if needed, but age policy will use global check.
func canDeleteBasedOnMinClips(file *FileInfo, parentDir string, speciesDirCount map[string]map[string]int,
	minClipsPerSpecies int, debug bool) bool {

	// Check if the species and directory exist in the map
	if dirCount, speciesExists := speciesDirCount[file.Species]; speciesExists {
		if count, dirExists := dirCount[parentDir]; dirExists {
			if count <= minClipsPerSpecies {
				if debug {
					log.Printf("Species clip count for %s in %s is at or below the minimum threshold (%d). Skipping file deletion.",
						file.Species, parentDir, minClipsPerSpecies)
				}
				return false // Cannot delete, count is too low
			}
			// If count > minClipsPerSpecies, proceed
		} else {
			// This case should ideally not happen if the map was built correctly from the files
			if debug {
				log.Printf("Warning: Directory %s not found in species count map for species %s. Allowing deletion attempt.", parentDir, file.Species)
			}
			return true // Allow deletion attempt, maybe map is stale or file is unique?
		}
	} else {
		// Species not found in map, should not happen if map built correctly
		if debug {
			log.Printf("Warning: Species %s not found in species count map. Allowing deletion attempt.", file.Species)
		}
		return true // Allow deletion attempt
	}

	// If we reached here, the count is > minClipsPerSpecies
	return true
}

// canDeleteBasedOnGlobalMinClips checks if a file can be deleted based on global species count.
func canDeleteBasedOnGlobalMinClips(file *FileInfo, globalSpeciesCount map[string]int,
	minClipsPerSpecies int, debug bool) bool {

	currentGlobalCount, speciesExists := globalSpeciesCount[file.Species]
	if !speciesExists {
		// Should not happen if map is built correctly from all files
		if debug {
			log.Printf("Warning: Species %s not found in global species count map. Allowing deletion attempt.", file.Species)
		}
		return true // Allow deletion attempt
	}

	if currentGlobalCount <= minClipsPerSpecies {
		if debug {
			log.Printf("Global species count for %s is %d, which is at or below the minimum threshold (%d). Skipping file deletion: %s",
				file.Species, currentGlobalCount, minClipsPerSpecies, file.Path)
		}
		return false // Cannot delete, global count is too low
	}

	// If we reached here, the global count is > minClipsPerSpecies
	return true
}

// handleDeletionAttempt performs the actual file deletion, PNG cleanup, and updates counts.
func handleDeletionAttempt(file *FileInfo, params *CleanupParameters,
	speciesDirCount map[string]map[string]int, globalSpeciesCount map[string]int) (deleted bool, freedSpace int64, err error) {

	if err := deleteFile(file, params); err != nil {
		// Error already logged in deleteFile if debug is on
		return false, 0, err // Return error to be handled by the main loop's error counter
	}

	freedSpace = file.Size
	parentDir := filepath.Dir(file.Path)

	// Decrement directory count map
	if speciesDirCount != nil {
		if dirCount, ok := speciesDirCount[file.Species]; ok {
			if _, ok2 := dirCount[parentDir]; ok2 {
				speciesDirCount[file.Species][parentDir]--
			}
		}
	}

	// Decrement global count map
	if globalSpeciesCount != nil {
		if _, ok := globalSpeciesCount[file.Species]; ok {
			globalSpeciesCount[file.Species]--
		}
	}

	if params.Debug {
		log.Printf("Successfully processed deletion for: %s, freed %d bytes", file.Path, freedSpace)
	}

	return true, freedSpace, nil
}

// performCleanupLoop iterates through files and deletes them based on the provided
// shouldDelete function and common constraints like minimum clips per species.
func performCleanupLoop(files []FileInfo, params *CleanupParameters,
	speciesDirCount map[string]map[string]int, // Kept for potential usage policy needs
	globalSpeciesCount map[string]int, // Added global count map
	shouldDelete ShouldDeleteFunc) (int, error) {

	deletedFiles := 0
	maxDeletions := 1000 // Limit deletions per run to avoid excessive I/O or holding locks
	totalFreedSpace := int64(0)
	errorCount := 0
	const maxErrorCount = 10
	throttleDuration := 100 * time.Millisecond // Throttling between deletions
	var currentDiskUsage float64 = -1.0        // Initialize disk usage
	var diskCheckErr error

	if params.Debug {
		log.Printf("Starting cleanup loop. Max deletions per run: %d", maxDeletions)
	}

	for i := range files { // Iterate using index to modify file info if needed (though not currently)
		select {
		case <-params.QuitChan:
			log.Println("Received quit signal, ending cleanup loop.")
			return deletedFiles, nil // Return successfully as it's a requested stop
		default:
			// Non-blocking check, continue processing
		}

		file := &files[i] // Use pointer for potential modifications, though mainly for consistency

		// Skip locked files
		if file.Locked {
			if params.Debug {
				log.Printf("Skipping locked file: %s", file.Path)
			}
			continue
		}

		// --- Check Disk Usage (if required by policy) ---
		if params.CheckUsageInLoop {
			currentDiskUsage, diskCheckErr = GetDiskUsage(params.BaseDir)
			if diskCheckErr != nil {
				// Log error and potentially stop or continue? Let's stop if we can't check usage.
				log.Printf("Error getting disk usage during cleanup loop for %s: %v. Aborting loop.", params.BaseDir, diskCheckErr)
				return deletedFiles, fmt.Errorf("failed to get disk usage during cleanup loop: %w", diskCheckErr)
			}
		}

		// --- Policy-Specific Check ---
		considerDelete, err := shouldDelete(file, params, currentDiskUsage)
		if err != nil {
			// Log the error from the policy check, but potentially continue
			log.Printf("Error during policy check for file %s: %v", file.Path, err)
			// Depending on the error, might want to stop or continue. Let's continue for now.
			// Could increment errorCount here if policy check errors should count towards the limit.
			continue
		}

		if !considerDelete {
			// Policy determined this file should not be deleted (e.g., too new, or usage below threshold)
			continue
		}

		// --- Global Constraint Check (Min Clips - Primarily for Age Policy) ---
		// We check this *after* the policy check. An expired file might still be kept
		// if it's needed to satisfy the global minimum count.
		if globalSpeciesCount != nil { // Only perform global check if map is provided
			if !canDeleteBasedOnGlobalMinClips(file, globalSpeciesCount, params.MinClipsPerSpecies, params.Debug) {
				continue // Cannot delete due to global minimum clips constraint
			}
		}

		// --- Deletion --- (Throttle first)
		time.Sleep(throttleDuration)

		if params.Debug {
			// Report the disk usage that triggered the check if relevant
			usageStr := "N/A"
			if currentDiskUsage >= 0 {
				usageStr = fmt.Sprintf("%.1f%%", currentDiskUsage)
			}
			log.Printf("Policy conditions met, attempting deletion of: %s (Disk Usage: %s)", file.Path, usageStr)
		}

		deleted, freed, err := handleDeletionAttempt(file, params, speciesDirCount, globalSpeciesCount)
		if err != nil {
			errorCount++
			// Error logging potentially redundant if already logged in handleDeletionAttempt/deleteFile
			// log.Printf("Failed to remove %s: %v (Error count: %d)", file.Path, err, errorCount)
			if errorCount > maxErrorCount {
				// Return the last error that caused us to exceed the threshold
				return deletedFiles, fmt.Errorf("too many errors (%d) during cleanup loop, last error: %w", errorCount, err)
			}
			continue // Continue with the next file even if one fails
		}

		if deleted { // Only update counters if deletion was successful
			deletedFiles++
			totalFreedSpace += freed
			// Counts are already updated within handleDeletionAttempt

			if params.Debug {
				// Simplified logging here as detailed log is in handleDeletionAttempt
				log.Printf("Deletion processed for: %s. Total deleted so far: %d", file.Path, deletedFiles)
			}
		}

		// Check if max deletions reached for this run
		if deletedFiles >= maxDeletions {
			if params.Debug {
				log.Printf("Reached maximum deletion limit (%d) for this run.", maxDeletions)
			}
			break // Exit the loop for this run
		}

		// Yield to other goroutines periodically
		runtime.Gosched()
	}

	if params.Debug {
		log.Printf("Cleanup loop finished. Deleted %d files, freed %d bytes. Error count: %d", deletedFiles, totalFreedSpace, errorCount)
	}

	return deletedFiles, nil // Return total deleted and nil error if loop completed or quit normally
}
