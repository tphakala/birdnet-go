// policy_common.go - shared code for cleanup policies
package diskmanager

import (
	"log"
	"os"
	"path/filepath"
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
