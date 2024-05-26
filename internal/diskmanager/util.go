package diskmanager

import (
	"fmt"
	"log"
	"os"
	"time"
)

// WriteSortedFilesToFile writes the sorted list of files to a text file for investigation
func WriteSortedFilesToFile(files []FileInfo, filePath string) error {
	file, err := os.Create(filePath)
	if err != nil {
		return fmt.Errorf("failed to create file: %w", err)
	}
	defer file.Close()

	for _, fileInfo := range files {
		line := fmt.Sprintf("Path: %s, Species: %s, Confidence: %d, Timestamp: %s, Size: %d\n",
			fileInfo.Path, fileInfo.Species, fileInfo.Confidence, fileInfo.Timestamp.Format(time.RFC3339), fileInfo.Size)
		_, err := file.WriteString(line)
		if err != nil {
			return fmt.Errorf("failed to write to file: %w", err)
		}
	}

	if err := file.Sync(); err != nil {
		return fmt.Errorf("failed to sync file: %w", err)
	}

	log.Printf("Sorted files have been written to %s", filePath)
	return nil
}
