package processor

import (
	"fmt"
	"io/ioutil"
	"math"
	"os"
	"path/filepath"
	"testing"

	"github.com/tphakala/birdnet-go/internal/conf"
)

type MockDiskUsage struct {
	UsedValue uint64
	SizeValue uint64
}

func (m MockDiskUsage) Used(basePath string) (uint64, error) {
	return m.UsedValue, nil
}

func (m MockDiskUsage) Size(basePath string) (uint64, error) {
	return m.SizeValue, nil
}

func initData(t testing.TB, fileSize int, numberOfFiles int) string {
	// Create a temporary directory for testing
	tempDir := t.TempDir()

	// Create some files in the directory
	for i := 0; i < numberOfFiles; i++ {
		fileName := filepath.Join(tempDir, fmt.Sprintf("testfile%d.txt", i))
		file, err := os.Create(fileName)
		if err != nil {
			t.Fatalf("Error creating file: %v", err)
		}

		// Write data to the file to reach the desired file size
		data := make([]byte, fileSize)
		_, err = file.Write(data)
		if err != nil {
			t.Fatalf("Error writing to file: %v", err)
		}

		// Sync the file to ensure data is written to disk
		err = file.Sync()
		if err != nil {
			t.Fatalf("Error syncing file: %v", err)
		}
	}
	return tempDir
}

func countFiles(t testing.TB, filePath string) int {
	fileCount := 0
	files, err := ioutil.ReadDir(filePath)
	if err != nil {
		t.Fatalf("Error reading directory after cleanup: %v", err)
	}
	for _, fileInfo := range files {
		if fileInfo.Mode().IsRegular() {
			fileCount++
		}
	}
	return fileCount
}

func TestDiskCleanUpFullDisk(t *testing.T) {
	// Define the size of each file in bytes
	const fileSize = 8
	const numberOfFiles = 140

	tempDir := initData(t, fileSize, numberOfFiles)

	// Create a mock instance of DatabaseAction with a custom path
	settings := &conf.Settings{}
	settings.Realtime.AudioExport.Path = tempDir

	action := DatabaseAction{
		Settings: settings,
	}

	// Create a mock DiskUsageCalculator with mock values
	mockCalculator := MockDiskUsage{
		UsedValue: numberOfFiles * fileSize, // Mock used space
		SizeValue: numberOfFiles * fileSize, // Mock total disk size
	}

	err := action.DiskCleanUp(mockCalculator)
	if err != nil {
		t.Errorf("DiskCleanUp failed unexpectedly: %v", err)
	}

	// Count the number of files after cleanup
	numFilesAfter := countFiles(t, tempDir)

	// Assert that the actual number of files after cleanup matches the expected number
	bytesToRemove := int(mockCalculator.UsedValue) - int(math.Ceil(float64(mockCalculator.SizeValue)*0.9))
	filesToRemove := int(math.Ceil(float64(bytesToRemove) / fileSize))
	expectedNumFilesAfter := numberOfFiles - filesToRemove

	if numFilesAfter != expectedNumFilesAfter {
		t.Errorf("Expected %d files after cleanup, got %d", expectedNumFilesAfter, numFilesAfter)
	}
}

func TestDiskCleanUpLessThanTreshold(t *testing.T) {
	// Define the size of each file in bytes
	const fileSize = 8
	const numberOfFiles = 140

	tempDir := initData(t, fileSize, numberOfFiles)

	// Create a mock instance of DatabaseAction with a custom path
	settings := &conf.Settings{}
	settings.Realtime.AudioExport.Path = tempDir

	action := DatabaseAction{
		Settings: settings,
	}

	// Create a mock DiskUsageCalculator with mock values
	mockCalculator := MockDiskUsage{
		UsedValue: numberOfFiles * fileSize,     // Mock used space
		SizeValue: numberOfFiles * fileSize * 2, // Mock total disk size
	}

	err := action.DiskCleanUp(mockCalculator)
	if err != nil {
		t.Errorf("DiskCleanUp failed unexpectedly: %v", err)
	}

	// Count the number of files after cleanup
	numFilesAfter := countFiles(t, tempDir)

	if numFilesAfter != numberOfFiles {
		t.Errorf("Expected 0 files to be removed. But %d were removed.", numberOfFiles-numFilesAfter)
	}
}

// TODO add a test that expects an error if too many files have to be removed at once.
