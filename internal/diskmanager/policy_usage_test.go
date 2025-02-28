package diskmanager

import (
	"os"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
)

// MockFileInfo implements os.FileInfo for testing
type MockFileInfo struct {
	FileName    string
	FileSize    int64
	FileMode    os.FileMode
	FileModTime time.Time
	FileIsDir   bool
	FileSys     interface{}
}

func (m *MockFileInfo) Name() string       { return m.FileName }
func (m *MockFileInfo) Size() int64        { return m.FileSize }
func (m *MockFileInfo) Mode() os.FileMode  { return m.FileMode }
func (m *MockFileInfo) ModTime() time.Time { return m.FileModTime }
func (m *MockFileInfo) IsDir() bool        { return m.FileIsDir }
func (m *MockFileInfo) Sys() interface{}   { return m.FileSys }

// Helper function to create a mock FileInfo
func createMockFileInfo(filename string, size int64) os.FileInfo {
	return &MockFileInfo{
		FileName:    filename,
		FileSize:    size,
		FileMode:    0o644,
		FileModTime: time.Now(),
		FileIsDir:   false,
	}
}

// Helper function to parse time string
func parseTime(timeStr string) time.Time {
	t, _ := time.Parse("20060102T150405Z", timeStr)
	return t
}

// TestFileTypesEligibleForDeletion tests which file types are eligible for deletion
func TestFileTypesEligibleForDeletion(t *testing.T) {
	// Test cases with various file extensions
	testCases := []struct {
		filename            string
		extension           string
		eligibleForDeletion bool
		description         string
	}{
		// Allowed file types (should be eligible for deletion)
		{"owl_80p_20210102T150405Z.wav", ".wav", true, "WAV files should be eligible for deletion"},
		{"owl_80p_20210102T150405Z.mp3", ".mp3", true, "MP3 files should be eligible for deletion"},
		{"owl_80p_20210102T150405Z.flac", ".flac", true, "FLAC files should be eligible for deletion"},
		{"owl_80p_20210102T150405Z.aac", ".aac", true, "AAC files should be eligible for deletion"},
		{"owl_80p_20210102T150405Z.opus", ".opus", true, "OPUS files should be eligible for deletion"},

		// Disallowed file types (should not be eligible for deletion)
		{"owl_80p_20210102T150405Z.txt", ".txt", false, "TXT files should not be eligible for deletion"},
		{"owl_80p_20210102T150405Z.jpg", ".jpg", false, "JPG files should not be eligible for deletion"},
		{"owl_80p_20210102T150405Z.png", ".png", false, "PNG files should not be eligible for deletion"},
		{"owl_80p_20210102T150405Z.db", ".db", false, "DB files should not be eligible for deletion"},
		{"owl_80p_20210102T150405Z.csv", ".csv", false, "CSV files should not be eligible for deletion"},
		{"system_80p_20210102T150405Z.exe", ".exe", false, "EXE files should not be eligible for deletion"},
	}

	for _, tc := range testCases {
		t.Run(tc.filename, func(t *testing.T) {
			mockInfo := createMockFileInfo(tc.filename, 1024)
			fileInfo, err := parseFileInfo("/test/"+tc.filename, mockInfo)

			if tc.eligibleForDeletion {
				assert.NoError(t, err, "File should be eligible for deletion: %s", tc.description)
				assert.Equal(t, "owl", fileInfo.Species, "Species should be correctly parsed")
				assert.Equal(t, 80, fileInfo.Confidence, "Confidence should be correctly parsed")

				// Check that the timestamp was parsed correctly
				expectedTime := parseTime("20210102T150405Z")
				assert.Equal(t, expectedTime, fileInfo.Timestamp, "Timestamp should be correctly parsed")
			} else {
				// For disallowed files, we must ensure they would be rejected from deletion
				// We'll fail the test if they would be processed (which indicates a security issue)

				// Check if this file extension is in the allowedFileTypes list
				isAllowedExt := contains(allowedFileTypes, tc.extension)

				// Check if parseFileInfo returned an error
				hasError := (err != nil)

				// If the file has a disallowed extension but would be processed for deletion,
				// fail the test with a security warning
				if !isAllowedExt && !hasError {
					t.Errorf("SECURITY ISSUE: %s file would be processed for deletion but should be protected: %s",
						tc.extension, tc.description)
				}

				// If the function returned an error, validate it's the right kind of error
				if hasError {
					assert.Contains(t, err.Error(), "file type not eligible for cleanup operation",
						"Error message should indicate file is not eligible for cleanup")
				}
			}
		})
	}
}

// TestParseFileInfoWithDifferentExtensions tests that parseFileInfo correctly handles different file extensions
func TestParseFileInfoWithDifferentExtensions(t *testing.T) {
	// Test cases for each allowed file type (.wav, .flac, .aac, .opus, .mp3)
	testCases := []struct {
		filename      string
		expectedExt   string
		shouldSucceed bool
	}{
		{"owl_80p_20210102T150405Z.wav", ".wav", true},
		{"owl_80p_20210102T150405Z.mp3", ".mp3", true},
		{"owl_80p_20210102T150405Z.flac", ".flac", true},
		{"owl_80p_20210102T150405Z.aac", ".aac", true},
		{"owl_80p_20210102T150405Z.opus", ".opus", true},
		{"owl_80p_20210102T150405Z.txt", ".txt", false}, // Unsupported extension
	}

	for _, tc := range testCases {
		t.Run(tc.filename, func(t *testing.T) {
			mockInfo := createMockFileInfo(tc.filename, 1024)
			fileInfo, err := parseFileInfo("/test/"+tc.filename, mockInfo)

			if tc.shouldSucceed {
				assert.NoError(t, err, "Should parse successfully")
				assert.Equal(t, "owl", fileInfo.Species)
				assert.Equal(t, 80, fileInfo.Confidence)

				// Check that the timestamp was parsed correctly
				expectedTime := parseTime("20210102T150405Z")
				assert.Equal(t, expectedTime, fileInfo.Timestamp)
			} else {
				assert.Error(t, err, "Should return an error")
			}
		})
	}
}

// TestParseFileInfoMp3Extension specifically tests the MP3 extension bug
func TestParseFileInfoMp3Extension(t *testing.T) {
	// This test specifically targets the bug in the error message
	mockInfo := createMockFileInfo("owl_80p_20250130T184446Z.mp3", 1024)

	fileInfo, err := parseFileInfo("/test/owl_80p_20250130T184446Z.mp3", mockInfo)

	// The bug would cause an error here because it only trims .wav extension
	assert.NoError(t, err, "Should parse MP3 files correctly")
	assert.Equal(t, "owl", fileInfo.Species)
	assert.Equal(t, 80, fileInfo.Confidence)

	expectedTime := parseTime("20250130T184446Z")
	assert.Equal(t, expectedTime, fileInfo.Timestamp)
}

// TestSortFiles tests the sortFiles function
func TestSortFiles(t *testing.T) {
	// Create a set of files with different timestamps, species counts, and confidence levels
	files := []FileInfo{
		{Path: "/base/dir1/owl_80p_20210102T150405Z.wav", Species: "owl", Confidence: 80, Timestamp: parseTime("20210102T150405Z")},
		{Path: "/base/dir1/owl_90p_20210103T150405Z.wav", Species: "owl", Confidence: 90, Timestamp: parseTime("20210103T150405Z")},
		{Path: "/base/dir1/duck_70p_20210101T150405Z.wav", Species: "duck", Confidence: 70, Timestamp: parseTime("20210101T150405Z")},
	}

	// Sort the files
	speciesCount := sortFiles(files, true)

	// Verify sorting order (oldest first)
	assert.Equal(t, "duck", files[0].Species, "Duck should be first (oldest)")
	assert.Equal(t, "owl", files[1].Species, "Owl (oldest) should be second")
	assert.Equal(t, "owl", files[2].Species, "Owl (newest) should be third")

	// Verify the count map is correct
	assert.Equal(t, 1, speciesCount["duck"]["/base/dir1"])
	assert.Equal(t, 2, speciesCount["owl"]["/base/dir1"])
}
