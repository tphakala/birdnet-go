// agemode.go age based cleanup code
package diskmanager

import (
	"log"
	"os"

	"github.com/tphakala/birdnet-go/internal/conf"
	"github.com/tphakala/birdnet-go/internal/datastore"
)

// AgeBasedCleanup removes clips from the filesystem based on their age and the number of clips per species.
// TODO: handle quit channel properly if it happens during cleanup
func AgeBasedCleanup(dataStore datastore.Interface) error {
	MinEvictionHours := conf.Setting().Realtime.Audio.Export.Retention.MinEvictionHours
	MinClipsPerSpecies := conf.Setting().Realtime.Audio.Export.Retention.MinClipsPerSpecies

	// Perform cleanup operation on every tick
	clipsForRemoval, err := dataStore.GetClipsQualifyingForRemoval(MinEvictionHours, MinClipsPerSpecies)
	if err != nil {
		log.Printf("Error retrieving clips for removal: %s\n", err)
		return err
	}

	log.Printf("Found %d clips to remove\n", len(clipsForRemoval))

	for _, clip := range clipsForRemoval {
		// Attempt to remove the clip file from the filesystem
		if err := os.Remove(clip.ClipName); err != nil {
			if os.IsNotExist(err) {
				// Attempt to delete the database record if the clip file aleady doesn't exist
				if err := dataStore.DeleteNoteClipPath(clip.ID); err != nil {
					log.Printf("Failed to delete clip path for %s: %s\n", clip.ID, err)

				} else {
					log.Printf("Cleared clip path of missing clip for %s\n", clip.ID)
				}
			} else {
				log.Printf("Failed to remove %s: %s\n", clip.ClipName, err)
			}
		} else {
			log.Printf("Removed %s\n", clip.ClipName)
			// Attempt to delete the database record if the file removal was successful
			if err := dataStore.DeleteNoteClipPath(clip.ID); err != nil {
				log.Printf("Failed to delete clip path for %s: %s\n", clip.ID, err)
			}
		}
		return err
	}

	return nil
}
