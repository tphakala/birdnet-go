// priority.go priority based cleanup code
package diskmanager

import (
	"encoding/csv"
	"errors"
	"log"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/tphakala/birdnet-go/internal/conf"
)

// Policy defines cleanup policies
type Policy struct {
	AlwaysCleanupFirst map[string]bool // Species to always cleanup first
	NeverCleanup       map[string]bool // Species to never cleanup
}

// FileInfo holds information about a file
type FileInfo struct {
	Path       string
	Species    string
	Confidence int
	Timestamp  time.Time
	Size       int64
}

// LoadPolicy loads the cleanup policies from a CSV file
func LoadPolicy(policyFile string) (*Policy, error) {
	file, err := os.Open(policyFile)
	if err != nil {
		return nil, err
	}
	defer file.Close()

	reader := csv.NewReader(file)
	records, err := reader.ReadAll()
	if err != nil {
		return nil, err
	}

	policy := &Policy{
		AlwaysCleanupFirst: make(map[string]bool),
		NeverCleanup:       make(map[string]bool),
	}

	for _, record := range records {
		if len(record) != 2 {
			return nil, errors.New("invalid policy record")
		}
		if record[1] == "always" {
			policy.AlwaysCleanupFirst[record[0]] = true
		} else if record[1] == "never" {
			policy.NeverCleanup[record[0]] = true
		}
	}

	return policy, nil
}

// GetAudioFiles returns a list of audio files in the directory and its subdirectories
func GetAudioFiles(baseDir string, allowedExts []string, debug bool) ([]FileInfo, error) {
	var files []FileInfo

	err := filepath.Walk(baseDir, func(path string, info os.FileInfo, err error) error {
		if err != nil {
			return err
		}
		if !info.IsDir() {
			ext := filepath.Ext(info.Name())
			if contains(allowedExts, ext) {
				fileInfo, err := parseFileInfo(path, info)
				if err != nil {
					return err
				}
				if debug {
					log.Printf("Found file: %s, Species: %s, Confidence: %d, Timestamp: %s", fileInfo.Path, fileInfo.Species, fileInfo.Confidence, fileInfo.Timestamp)
				}
				files = append(files, fileInfo)
			}
		}
		return nil
	})

	return files, err
}

// parseFileInfo parses the file information from the file path and os.FileInfo
func parseFileInfo(path string, info os.FileInfo) (FileInfo, error) {
	name := filepath.Base(info.Name())
	parts := strings.Split(name, "_")
	if len(parts) < 3 {
		return FileInfo{}, errors.New("invalid file name format")
	}

	// The species name might contain underscores, so we need to handle the last two parts separately
	confidenceStr := parts[len(parts)-2]
	timestampStr := parts[len(parts)-1]
	species := strings.Join(parts[:len(parts)-2], "_")

	confidence, err := strconv.Atoi(strings.TrimSuffix(confidenceStr, "p"))
	if err != nil {
		return FileInfo{}, err
	}

	timestamp, err := time.Parse("20060102T150405Z", strings.TrimSuffix(timestampStr, ".wav"))
	if err != nil {
		return FileInfo{}, err
	}

	return FileInfo{
		Path:       path,
		Species:    species,
		Confidence: confidence,
		Timestamp:  timestamp,
		Size:       info.Size(),
	}, nil
}

// PriorityBasedCleanup cleans up old audio files based on the configuration and monitors for quit signals
func PriorityBasedCleanup(quitChan chan struct{}) error {
	settings := conf.Setting()

	debug := settings.Realtime.Audio.Export.Debug
	baseDir := settings.Realtime.Audio.Export.Path
	thresholdStr := settings.Realtime.Audio.Export.Retention.DiskUsageLimit
	minClipsPerSpecies := settings.Realtime.Audio.Export.Retention.MinClipsPerSpecies

	// Convert 80% string etc. to 80.0 float64
	threshold, err := conf.ParsePercentage(thresholdStr)
	if err != nil {
		return err
	}

	// Only remove files with extensions in this list
	allowedExts := []string{".wav"}

	if debug {
		log.Printf("Starting cleanup process. Base directory: %s, Threshold: %.1f%%", baseDir, threshold)
	}

	// Check and handle disk usage
	if err := handleDiskUsage(baseDir, threshold, debug); err != nil {
		return err
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
}

func handleDiskUsage(baseDir string, threshold float64, debug bool) error {
	// Get the current disk usage
	diskUsage, err := GetDiskUsage(baseDir)
	if err != nil {
		return err
	}

	if debug {
		log.Printf("Current disk usage: %.1f%%", diskUsage)
	}

	// If disk usage is below the threshold, no cleanup is needed
	if diskUsage < threshold {
		if debug {
			log.Printf("Disk usage %.1f%% is below the %.1f%% threshold. No cleanup needed.", diskUsage, threshold)
		}
		return nil
	} else {
		if debug {
			log.Printf("Disk usage %.1f%% is above the %.1f%% threshold. Cleanup needed.", diskUsage, threshold)
		}
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
			if debug {
				log.Println("Received quit signal, exiting cleanup loop.")
			}
			return nil
		default:
			// Get the subdirectory name
			subDir := filepath.Dir(file.Path)
			diskUsage, err := GetDiskUsage(baseDir)
			if err != nil {
				return err
			}
			if diskUsage < threshold || deletedFiles >= maxDeletions || speciesMonthCount[file.Species][subDir] <= minClipsPerSpecies {
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
		}
	}

	if debug {
		log.Printf("Cleanup process completed. %d files deleted. Total space freed: %.2f MB", deletedFiles, float64(totalFreedSpace)/(1024*1024))
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
