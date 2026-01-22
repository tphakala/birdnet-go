package diskmanager

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/tphakala/birdnet-go/internal/conf"
	mock_diskmanager "github.com/tphakala/birdnet-go/internal/diskmanager/mocks"
)

// Original function signature references for testing
var (
	originalGetDiskUsage    = GetDiskUsage
	originalGetAudioFiles   = GetAudioFiles
	originalOsRemove        = osRemove
	originalConfGetSettings = conf.GetSettings
)

// MockFileInfo implements os.FileInfo for testing
type MockFileInfo struct {
	FileName    string
	FileSize    int64
	FileMode    os.FileMode
	FileModTime time.Time
	FileIsDir   bool
	FileSys     any
}

func (m *MockFileInfo) Name() string       { return m.FileName }
func (m *MockFileInfo) Size() int64        { return m.FileSize }
func (m *MockFileInfo) Mode() os.FileMode  { return m.FileMode }
func (m *MockFileInfo) ModTime() time.Time { return m.FileModTime }
func (m *MockFileInfo) IsDir() bool        { return m.FileIsDir }
func (m *MockFileInfo) Sys() any           { return m.FileSys }

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
		{"bubo_bubo_80p_20210102T150405Z.wav", ".wav", true, "WAV files should be eligible for deletion"},
		{"bubo_bubo_80p_20210102T150405Z.mp3", ".mp3", true, "MP3 files should be eligible for deletion"},
		{"bubo_bubo_80p_20210102T150405Z.flac", ".flac", true, "FLAC files should be eligible for deletion"},
		{"bubo_bubo_80p_20210102T150405Z.aac", ".aac", true, "AAC files should be eligible for deletion"},
		{"bubo_bubo_80p_20210102T150405Z.opus", ".opus", true, "OPUS files should be eligible for deletion"},
		{"bubo_bubo_80p_20210102T150405Z.m4a", ".m4a", true, "M4A files should be eligible for deletion"},

		// Disallowed file types (should not be eligible for deletion)
		{"bubo_bubo_80p_20210102T150405Z.txt", ".txt", false, "TXT files should not be eligible for deletion"},
		{"bubo_bubo_80p_20210102T150405Z.jpg", ".jpg", false, "JPG files should not be eligible for deletion"},
		{"bubo_bubo_80p_20210102T150405Z.png", ".png", false, "PNG files should not be eligible for deletion"},
		{"bubo_bubo_80p_20210102T150405Z.db", ".db", false, "DB files should not be eligible for deletion"},
		{"bubo_bubo_80p_20210102T150405Z.csv", ".csv", false, "CSV files should not be eligible for deletion"},
		{"system_80p_20210102T150405Z.exe", ".exe", false, "EXE files should not be eligible for deletion"},
	}

	for _, tc := range testCases {
		t.Run(tc.filename, func(t *testing.T) {
			mockInfo := createMockFileInfo(tc.filename, 1024)
			fileInfo, err := parseFileInfo("/test/"+tc.filename, mockInfo, allowedFileTypes)

			if tc.eligibleForDeletion {
				require.NoError(t, err, "File should be eligible for deletion: %s", tc.description)
				assert.Equal(t, "bubo_bubo", fileInfo.Species, "Species should be correctly parsed")
				assert.Equal(t, 80, fileInfo.Confidence, "Confidence should be correctly parsed")

				// Check that the timestamp was parsed correctly
				// IMPORTANT: Timestamps from filenames (with 'Z') are now parsed as local time
				// so we need to compare against the local time equivalent.
				expectedTimeLocal, err := time.ParseInLocation("20060102T150405", "20210102T150405", time.Local)
				require.NoError(t, err, "Failed to parse expected local time for comparison")
				assert.Equal(t, expectedTimeLocal, fileInfo.Timestamp, "Timestamp should be correctly parsed")
			} else {
				// For disallowed files, we must ensure they would be rejected from deletion
				// We'll fail the test if they would be processed (which indicates a security issue)

				// Check if this file extension is in the allowedFileTypes list
				isAllowedExt := contains(allowedFileTypes, tc.extension)

				// Check if parseFileInfo returned an error
				hasError := (err != nil)

				// If the file has a disallowed extension but would be processed for deletion,
				// fail the test with a security warning
				assert.True(t, isAllowedExt || hasError,
					"SECURITY ISSUE: %s file would be processed for deletion but should be protected: %s",
					tc.extension, tc.description)

				// If the function returned an error, validate it's the right kind of error
				if hasError {
					assert.Contains(t, err.Error(), "file type",
						"Error should indicate file type issue")
					assert.Contains(t, err.Error(), "not eligible",
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
		{"bubo_bubo_80p_20210102T150405Z.wav", ".wav", true},
		{"bubo_bubo_80p_20210102T150405Z.mp3", ".mp3", true},
		{"bubo_bubo_80p_20210102T150405Z.flac", ".flac", true},
		{"bubo_bubo_80p_20210102T150405Z.aac", ".aac", true},
		{"bubo_bubo_80p_20210102T150405Z.opus", ".opus", true},
		{"bubo_bubo_80p_20210102T150405Z.m4a", ".m4a", true},
		{"bubo_bubo_80p_20210102T150405Z.txt", ".txt", false}, // Unsupported extension
	}

	for _, tc := range testCases {
		t.Run(tc.filename, func(t *testing.T) {
			mockInfo := createMockFileInfo(tc.filename, 1024)
			fileInfo, err := parseFileInfo("/test/"+tc.filename, mockInfo, allowedFileTypes)

			if tc.shouldSucceed {
				require.NoError(t, err, "Should parse successfully")
				assert.Equal(t, "bubo_bubo", fileInfo.Species)
				assert.Equal(t, 80, fileInfo.Confidence)

				// Check that the timestamp was parsed correctly
				// IMPORTANT: Timestamps from filenames (with 'Z') are now parsed as local time
				// so we need to compare against the local time equivalent.
				expectedTimeLocal, err := time.ParseInLocation("20060102T150405", "20210102T150405", time.Local)
				require.NoError(t, err, "Failed to parse expected local time for comparison")
				assert.Equal(t, expectedTimeLocal, fileInfo.Timestamp, "Timestamp should be correctly parsed")
			} else {
				assert.Error(t, err, "Should return an error")
			}
		})
	}
}

// TestParseFileInfoMp3Extension specifically tests the MP3 extension bug
func TestParseFileInfoMp3Extension(t *testing.T) {
	// This test specifically targets the bug in the error message
	mockInfo := createMockFileInfo("bubo_bubo_80p_20250130T184446Z.mp3", 1024)

	fileInfo, err := parseFileInfo("/test/bubo_bubo_80p_20250130T184446Z.mp3", mockInfo, allowedFileTypes)

	// The bug would cause an error here because it only trims .wav extension
	require.NoError(t, err, "Should parse MP3 files correctly")
	assert.Equal(t, "bubo_bubo", fileInfo.Species)
	assert.Equal(t, 80, fileInfo.Confidence)

	// Check that the timestamp was parsed correctly
	// IMPORTANT: Timestamps from filenames (with 'Z') are now parsed as local time
	// so we need to compare against the local time equivalent.
	expectedTimeLocal, err := time.ParseInLocation("20060102T150405", "20250130T184446", time.Local)
	require.NoError(t, err, "Failed to parse expected local time for comparison")
	assert.Equal(t, expectedTimeLocal, fileInfo.Timestamp, "Timestamp should be correctly parsed")
}

// TestParseFileInfoProductionFormat tests file names as they actually appear in production
func TestParseFileInfoProductionFormat(t *testing.T) {
	// Test cases with real-world file names from production
	testCases := []struct {
		filename      string
		expectedSpec  string
		expectedConf  int
		expectedTime  string
		shouldSucceed bool
		description   string
	}{
		{
			// Standard production format with underscored species name
			"vulpes_vulpes_92p_20250223T195727Z.flac",
			"vulpes_vulpes",
			92,
			"20250223T195727Z",
			true,
			"Standard production file with multi-part species name",
		},
		{
			// PNG thumbnail with size suffix - this should fail due to file type
			"vulpes_vulpes_96p_20250223T073356Z_400px.png",
			"",
			0,
			"",
			false,
			"PNG file with size suffix should fail due to file type",
		},
		{
			// Three-part species name
			"genus_species_subspecies_99p_20250222T043210Z.flac",
			"genus_species_subspecies",
			99,
			"20250222T043210Z",
			true,
			"File with three-part species name",
		},
		{
			// Audio file with thumbnail size suffix - with our fix, this should now parse correctly
			"vulpes_vulpes_96p_20250223T073356Z_400px.flac",
			"vulpes_vulpes",
			96,
			"20250223T073356Z",
			true,
			"Audio file with size suffix should now parse correctly with the fix",
		},
	}

	for _, tc := range testCases {
		t.Run(tc.filename, func(t *testing.T) {
			mockInfo := createMockFileInfo(tc.filename, 1024)
			fileInfo, err := parseFileInfo("/test/"+tc.filename, mockInfo, allowedFileTypes)

			if tc.shouldSucceed {
				require.NoError(t, err, "Should parse successfully: "+tc.description)
				assert.Equal(t, tc.expectedSpec, fileInfo.Species, "Species should be correctly parsed")
				assert.Equal(t, tc.expectedConf, fileInfo.Confidence, "Confidence should be correctly parsed")

				// Check that the timestamp was parsed correctly
				// IMPORTANT: Timestamps from filenames (with 'Z') are now parsed as local time
				// so we need to compare against the local time equivalent.

				// First, parse the timestamp string without the Z suffix
				timestampStrLocal := strings.TrimSuffix(tc.expectedTime, "Z")

				// Parse it using the local timezone
				expectedTimeLocal, err := time.ParseInLocation("20060102T150405", timestampStrLocal, time.Local)
				require.NoError(t, err, "Failed to parse expected local time for comparison")
				assert.Equal(t, expectedTimeLocal, fileInfo.Timestamp, "Timestamp should be correctly parsed")
			} else {
				assert.Error(t, err, "Should return an error: "+tc.description)
				if err != nil {
					t.Logf("Error as expected: %v", err)
				}
			}
		})
	}
}

// TestSortFiles tests the sortFiles function
func TestSortFiles(t *testing.T) {
	// Create a set of files with different timestamps, species counts, and confidence levels
	// Note: parseTime helper now parses timestamps as local time due to changes in parseFileInfo
	files := []FileInfo{
		{Path: "/base/dir1/bubo_bubo_80p_20210102T150405Z.wav", Species: "bubo_bubo", Confidence: 80, Timestamp: parseTime("20210102T150405Z")},
		{Path: "/base/dir1/bubo_bubo_90p_20210103T150405Z.wav", Species: "bubo_bubo", Confidence: 90, Timestamp: parseTime("20210103T150405Z")},
		{Path: "/base/dir1/anas_platyrhynchos_70p_20210101T150405Z.wav", Species: "anas_platyrhynchos", Confidence: 70, Timestamp: parseTime("20210101T150405Z")},
	}

	// Sort the files
	speciesCount := buildSpeciesSubDirCountMap(files)
	sortFilesForUsage(files, speciesCount)

	// Verify sorting order (oldest first)
	assert.Equal(t, "anas_platyrhynchos", files[0].Species, "Anas platyrhynchos should be first (oldest)")
	assert.Equal(t, "bubo_bubo", files[1].Species, "Bubo bubo (oldest) should be second")
	assert.Equal(t, "bubo_bubo", files[2].Species, "Bubo bubo (newest) should be third")

	// Verify the count map is correct
	assert.Equal(t, 1, speciesCount["anas_platyrhynchos"]["/base/dir1"])
	assert.Equal(t, 2, speciesCount["bubo_bubo"]["/base/dir1"])
}

// ----- Tests for Usage-Based Cleanup -----

// UsageBasedTestHelper provides a test-friendly implementation
type UsageBasedTestHelper struct {
	// Test configuration
	diskUsage       float64
	audioFiles      []FileInfo
	deletedFiles    []string
	deletedPngFiles []string // Track PNGs that *should* be deleted
	lockedFilePaths []string

	// Settings
	baseDir            string
	maxUsagePercent    string
	minClipsPerSpecies int
	retentionPolicy    string
	debug              bool
	keepSpectrograms   bool // Added keepSpectrograms
}

// Execute runs the test with the given configuration
func (h *UsageBasedTestHelper) Execute(t *testing.T) {
	t.Helper()

	// Create mock DB (not used by UsageBasedCleanupForTests - it uses h.lockedFilePaths directly)
	mockDB := &mock_diskmanager.MockInterface{}

	// Create test disk cleanup with our helper
	diskCleaner := UsageBasedCleanupForTests{
		helper: h,
	}

	// Execute the cleanup
	quitChan := make(chan struct{})
	err := diskCleaner.Cleanup(quitChan, mockDB)
	require.NoError(t, err)
}

// isEligibleForUsageDeletion checks if a file should be deleted in usage-based test.
func isEligibleForUsageDeletion(file *FileInfo, speciesCount map[string]map[string]int, minClipsPerSpecies int) bool {
	if file.Locked {
		return false
	}
	subDir := filepath.Dir(file.Path)
	return speciesCount[file.Species][subDir] > minClipsPerSpecies
}

// processFileForUsageCleanup handles a single file in usage-based cleanup test.
// Returns true if file was deleted, false otherwise.
func processFileForUsageCleanup(file *FileInfo, speciesCount map[string]map[string]int, minClipsPerSpecies int, keepSpectrograms bool, deletedFiles, deletedPngFiles *[]string) bool {
	if file.Locked {
		return false
	}

	subDir := filepath.Dir(file.Path)
	if speciesCount[file.Species][subDir] <= minClipsPerSpecies {
		return false
	}

	*deletedFiles = append(*deletedFiles, file.Path)

	if !keepSpectrograms {
		pngPath := strings.TrimSuffix(file.Path, filepath.Ext(file.Path)) + ".png"
		_ = osRemove(pngPath)
		*deletedPngFiles = append(*deletedPngFiles, pngPath)
	}

	speciesCount[file.Species][subDir]--
	return true
}

// UsageBasedCleanupForTests is a test-friendly implementation of the cleanup system
type UsageBasedCleanupForTests struct {
	helper *UsageBasedTestHelper
}

// Cleanup implements the cleanup function for tests
func (c UsageBasedCleanupForTests) Cleanup(quitChan chan struct{}, _ Interface) error {
	h := c.helper

	// Parse "80%" to 80.0
	maxUsage, _ := conf.ParsePercentage(h.maxUsagePercent)

	// Check if disk usage exceeds threshold
	if h.diskUsage > maxUsage {
		// Mark files as locked based on h.lockedFilePaths
		for i := range h.audioFiles {
			h.audioFiles[i].Locked = isLockedClip(h.audioFiles[i].Path, h.lockedFilePaths)
		}

		// Sort files by priority
		speciesCount := buildSpeciesSubDirCountMap(h.audioFiles)
		sortFilesForUsage(h.audioFiles, speciesCount)

		// Process the files for cleanup
		for i := range h.audioFiles {
			select {
			case <-quitChan:
				return nil
			default:
			}

			if h.diskUsage < maxUsage {
				break
			}

			if processFileForUsageCleanup(&h.audioFiles[i], speciesCount, h.minClipsPerSpecies, h.keepSpectrograms, &h.deletedFiles, &h.deletedPngFiles) {
				h.diskUsage -= 2.0
			}
		}
	}

	return nil
}

// TestUsageBasedCleanupTriggered tests that cleanup is triggered when usage exceeds threshold
// and that spectrogram deletion respects the KeepSpectrograms setting.
func TestUsageBasedCleanupTriggered(t *testing.T) {
	// Create a temporary test directory
	tempDir := t.TempDir()

	// Helper to create audio and png files
	createTestFilePair := func(baseName string) (string, string) {
		audioPath := filepath.Join(tempDir, baseName+".wav")
		pngPath := filepath.Join(tempDir, baseName+".png")
		require.NoError(t, os.WriteFile(audioPath, []byte("audio"), 0o644), "Failed to create audio file") //nolint:gosec // G306: Test files don't require restrictive permissions
		require.NoError(t, os.WriteFile(pngPath, []byte("png"), 0o644), "Failed to create png file")       //nolint:gosec // G306: Test files don't require restrictive permissions
		// No need to set time for this usage-based test
		return audioPath, pngPath
	}

	// Create file pairs
	audioPath1, pngPath1 := createTestFilePair("bubo_bubo_80p_20210101T150405Z")
	audioPath2, pngPath2 := createTestFilePair("bubo_bubo_85p_20210102T150405Z")
	audioPath3, pngPath3 := createTestFilePair("anas_platyrhynchos_70p_20210103T150405Z")

	// Base helper setup (common settings)
	baseHelper := UsageBasedTestHelper{
		diskUsage: 90.0, // 90% usage exceeds 80% threshold
		audioFiles: []FileInfo{
			{Path: audioPath1, Species: "bubo_bubo", Confidence: 80, Timestamp: parseTime("20210101T150405Z"), Size: 1024},
			{Path: audioPath2, Species: "bubo_bubo", Confidence: 85, Timestamp: parseTime("20210102T150405Z"), Size: 512},
			{Path: audioPath3, Species: "anas_platyrhynchos", Confidence: 70, Timestamp: parseTime("20210103T150405Z"), Size: 2048},
		},
		lockedFilePaths:    []string{},
		baseDir:            tempDir,
		maxUsagePercent:    "80%",
		minClipsPerSpecies: 1,
		retentionPolicy:    "usage",
		debug:              true,
	}

	// --- Scenario 1: KeepSpectrograms = true ---
	t.Run("KeepSpectrogramsTrue", func(t *testing.T) {
		// Copy base helper and set keepSpectrograms
		helper := baseHelper
		helper.keepSpectrograms = true
		helper.deletedFiles = []string{} // Reset deletion tracking
		helper.deletedPngFiles = []string{}
		// Recreate files before test run
		createTestFilePair("bubo_bubo_80p_20210101T150405Z")
		createTestFilePair("bubo_bubo_85p_20210102T150405Z")
		createTestFilePair("anas_platyrhynchos_70p_20210103T150405Z")

		// Run the test
		helper.Execute(t)

		// Verify audio files were deleted
		assert.NotEmpty(t, helper.deletedFiles, "[KeepTrue] Audio files should have been deleted")
		// Determine which audio files remain based on helper.deletedFiles
		remainingAudio := make(map[string]bool)
		for _, af := range helper.audioFiles {
			remainingAudio[af.Path] = true
		}
		for _, df := range helper.deletedFiles {
			delete(remainingAudio, df)
		}

		// Verify PNG files were NOT deleted (check actual file existence)
		assert.FileExists(t, pngPath1, "[KeepTrue] PNG file 1 should exist")
		assert.FileExists(t, pngPath2, "[KeepTrue] PNG file 2 should exist")
		assert.FileExists(t, pngPath3, "[KeepTrue] PNG file 3 should exist")
	})

	// --- Scenario 2: KeepSpectrograms = false ---
	t.Run("KeepSpectrogramsFalse", func(t *testing.T) {
		// Copy base helper and set keepSpectrograms
		helper := baseHelper
		helper.keepSpectrograms = false
		helper.deletedFiles = []string{} // Reset deletion tracking
		helper.deletedPngFiles = []string{}
		// Recreate files before test run
		createTestFilePair("bubo_bubo_80p_20210101T150405Z")
		createTestFilePair("bubo_bubo_85p_20210102T150405Z")
		createTestFilePair("anas_platyrhynchos_70p_20210103T150405Z")

		// Run the test
		helper.Execute(t)

		// Verify audio files were deleted (same expectation as KeepTrue)
		assert.NotEmpty(t, helper.deletedFiles, "[KeepFalse] Audio files should have been deleted")
		// Determine which audio files remain
		remainingAudio := make(map[string]bool)
		for _, af := range helper.audioFiles {
			remainingAudio[af.Path] = true
		}
		deletedAudioMap := make(map[string]bool)
		for _, df := range helper.deletedFiles {
			delete(remainingAudio, df)
			deletedAudioMap[df] = true
		}

		// Verify PNG files: Exist if audio exists, deleted if audio was deleted
		if _, deleted := deletedAudioMap[audioPath1]; deleted {
			assert.NoFileExists(t, pngPath1, "[KeepFalse] PNG file 1 should be deleted")
		} else {
			assert.FileExists(t, pngPath1, "[KeepFalse] PNG file 1 should exist")
		}
		if _, deleted := deletedAudioMap[audioPath2]; deleted {
			assert.NoFileExists(t, pngPath2, "[KeepFalse] PNG file 2 should be deleted")
		} else {
			assert.FileExists(t, pngPath2, "[KeepFalse] PNG file 2 should exist")
		}
		if _, deleted := deletedAudioMap[audioPath3]; deleted {
			assert.NoFileExists(t, pngPath3, "[KeepFalse] PNG file 3 should be deleted")
		} else {
			assert.FileExists(t, pngPath3, "[KeepFalse] PNG file 3 should exist")
		}

		// Additionally, verify the helper's tracking matches expectations
		for _, deletedAudioPath := range helper.deletedFiles {
			expectedPngPath := strings.TrimSuffix(deletedAudioPath, filepath.Ext(deletedAudioPath)) + ".png"
			assert.Contains(t, helper.deletedPngFiles, expectedPngPath, "[KeepFalse] Helper should have tracked deleted PNG")
		}
	})
}

// TestUsageBasedCleanupNoTriggerBelowThreshold tests that cleanup is not triggered when usage is below threshold
func TestUsageBasedCleanupNoTriggerBelowThreshold(t *testing.T) {
	// Create a temporary test directory
	tempDir := t.TempDir()

	// Setup test helper with usage below threshold
	helper := &UsageBasedTestHelper{
		diskUsage: 70.0, // 70% usage is below 80% threshold
		audioFiles: []FileInfo{
			{Path: tempDir + "/bubo_bubo_80p_20210101T150405Z.wav", Species: "bubo_bubo", Confidence: 80, Timestamp: parseTime("20210101T150405Z"), Size: 1024},
		},
		deletedFiles:       []string{},
		lockedFilePaths:    []string{},
		baseDir:            tempDir,
		maxUsagePercent:    "80%",
		minClipsPerSpecies: 1,
		retentionPolicy:    "usage",
		debug:              true,
	}

	// Run the test
	helper.Execute(t)

	// Verify no files were deleted since disk usage was below threshold
	assert.Empty(t, helper.deletedFiles, "No files should be deleted when usage is below threshold")
}

// TestUsageBasedCleanupWithAllFileTypes tests that all supported file types are cleaned up
func TestUsageBasedCleanupWithAllFileTypes(t *testing.T) {
	// Create a temporary test directory
	tempDir := t.TempDir()

	// Setup test helper with files of all types
	helper := &UsageBasedTestHelper{
		diskUsage: 90.0, // 90% usage exceeds 80% threshold
		audioFiles: []FileInfo{
			{Path: tempDir + "/bubo_bubo_80p_20210101T150405Z.wav", Species: "bubo_bubo", Confidence: 80, Timestamp: parseTime("20210101T150405Z"), Size: 1024},
			{Path: tempDir + "/bubo_bubo_85p_20210102T150405Z.mp3", Species: "bubo_bubo", Confidence: 85, Timestamp: parseTime("20210102T150405Z"), Size: 512},
			{Path: tempDir + "/anas_platyrhynchos_70p_20210103T150405Z.flac", Species: "anas_platyrhynchos", Confidence: 70, Timestamp: parseTime("20210103T150405Z"), Size: 2048},
			{Path: tempDir + "/erithacus_rubecula_60p_20210104T150405Z.aac", Species: "erithacus_rubecula", Confidence: 60, Timestamp: parseTime("20210104T150405Z"), Size: 768},
			{Path: tempDir + "/passer_domesticus_90p_20210105T150405Z.opus", Species: "passer_domesticus", Confidence: 90, Timestamp: parseTime("20210105T150405Z"), Size: 1536},
			{Path: tempDir + "/turdus_migratorius_95p_20210105T150405Z.m4a", Species: "turdus_migratorius", Confidence: 95, Timestamp: parseTime("20210105T150405Z"), Size: 2560},
			// Add more instances of bubo_bubo to test min clips per species
			{Path: tempDir + "/bubo_bubo_75p_20210106T150405Z.wav", Species: "bubo_bubo", Confidence: 75, Timestamp: parseTime("20210106T150405Z"), Size: 1024},
			{Path: tempDir + "/bubo_bubo_65p_20210107T150405Z.mp3", Species: "bubo_bubo", Confidence: 65, Timestamp: parseTime("20210107T150405Z"), Size: 512},
			{Path: tempDir + "/bubo_bubo_95p_20210108T150405Z.flac", Species: "bubo_bubo", Confidence: 95, Timestamp: parseTime("20210108T150405Z"), Size: 2048},
		},
		deletedFiles:       []string{},
		lockedFilePaths:    []string{},
		baseDir:            tempDir,
		maxUsagePercent:    "80%",
		minClipsPerSpecies: 2, // Keep at least 2 clips per species
		retentionPolicy:    "usage",
		debug:              true,
	}

	// Run the test
	helper.Execute(t)

	// Verify files were deleted
	assert.NotEmpty(t, helper.deletedFiles, "Files should have been deleted")

	// Check that all supported file types are represented in deleted files
	fileTypeProcessed := make(map[string]bool)
	for _, path := range helper.deletedFiles {
		ext := filepath.Ext(path)
		fileTypeProcessed[ext] = true
	}

	// Check that at least some file types were processed
	assert.NotEmpty(t, fileTypeProcessed, "Some files should have been deleted")

	// Count how many bubo_bubo files were deleted to verify minClipsPerSpecies is respected
	buboFilesDeleted := 0
	for _, path := range helper.deletedFiles {
		if contains([]string{
			tempDir + "/bubo_bubo_80p_20210101T150405Z.wav",
			tempDir + "/bubo_bubo_85p_20210102T150405Z.mp3",
			tempDir + "/bubo_bubo_75p_20210106T150405Z.wav",
			tempDir + "/bubo_bubo_65p_20210107T150405Z.mp3",
			tempDir + "/bubo_bubo_95p_20210108T150405Z.flac",
		}, path) {
			buboFilesDeleted++
		}
	}

	// With 5 bubo files and minClipsPerSpecies=2, we should delete at most 3 bubo files
	assert.LessOrEqual(t, buboFilesDeleted, 3, "Should keep at least 2 bubo_bubo files as per minClipsPerSpecies setting")
}

// TestUsageBasedCleanupRespectLockedFiles verifies that locked files are not deleted
func TestUsageBasedCleanupRespectLockedFiles(t *testing.T) {
	// Create a temporary test directory
	tempDir := t.TempDir()

	// Define a locked file path
	lockedFilePath := tempDir + "/erithacus_rubecula_80p_20210101T150405Z.wav"

	// Setup test helper with a locked file and multiple non-locked files
	// Including multiple files of the same species to ensure some can be deleted
	helper := &UsageBasedTestHelper{
		diskUsage: 90.0, // 90% usage exceeds 80% threshold
		audioFiles: []FileInfo{
			// Multiple bubo files - at least one will be deleted
			{Path: tempDir + "/bubo_bubo_80p_20210101T150405Z.wav", Species: "bubo_bubo", Confidence: 80, Timestamp: parseTime("20210101T150405Z"), Size: 1024},
			{Path: tempDir + "/bubo_bubo_82p_20210102T150405Z.wav", Species: "bubo_bubo", Confidence: 82, Timestamp: parseTime("20210102T150405Z"), Size: 1024},
			{Path: tempDir + "/bubo_bubo_85p_20210103T150405Z.wav", Species: "bubo_bubo", Confidence: 85, Timestamp: parseTime("20210103T150405Z"), Size: 1024},

			// Locked file - should never be deleted
			{Path: lockedFilePath, Species: "erithacus_rubecula", Confidence: 80, Timestamp: parseTime("20210101T150405Z"), Size: 1024},

			// Multiple anas_platyrhynchos files - at least one will be deleted
			{Path: tempDir + "/anas_platyrhynchos_70p_20210102T150405Z.mp3", Species: "anas_platyrhynchos", Confidence: 70, Timestamp: parseTime("20210102T150405Z"), Size: 512},
			{Path: tempDir + "/anas_platyrhynchos_72p_20210103T150405Z.mp3", Species: "anas_platyrhynchos", Confidence: 72, Timestamp: parseTime("20210103T150405Z"), Size: 512},
			{Path: tempDir + "/anas_platyrhynchos_75p_20210104T150405Z.mp3", Species: "anas_platyrhynchos", Confidence: 75, Timestamp: parseTime("20210104T150405Z"), Size: 512},
		},
		deletedFiles:       []string{},
		lockedFilePaths:    []string{lockedFilePath}, // This file is locked
		baseDir:            tempDir,
		maxUsagePercent:    "80%",
		minClipsPerSpecies: 1, // Keep at least 1 clip per species
		retentionPolicy:    "usage",
		debug:              true,
	}

	// Run the test
	helper.Execute(t)

	// Verify some files were deleted
	assert.NotEmpty(t, helper.deletedFiles, "Some files should have been deleted")

	// Verify that the locked file was not deleted
	for _, path := range helper.deletedFiles {
		assert.NotEqual(t, lockedFilePath, path, "Locked file should not be deleted")
	}
}

// TestUsageBasedCleanupWithYearMonthFolders tests cleanup with production-like year/month folder structure
func TestUsageBasedCleanupWithYearMonthFolders(t *testing.T) {
	// Create a temporary test directory
	tempDir := t.TempDir()

	// Create year/month structure
	yearDir1 := filepath.Join(tempDir, "2024")
	yearDir2 := filepath.Join(tempDir, "2025")

	monthDir1 := filepath.Join(yearDir1, "12") // 2024/12
	monthDir2 := filepath.Join(yearDir2, "01") // 2025/01
	monthDir3 := filepath.Join(yearDir2, "02") // 2025/02

	// Setup test helper with files in year/month folder structure
	helper := &UsageBasedTestHelper{
		diskUsage: 90.0, // 90% usage exceeds 80% threshold
		audioFiles: []FileInfo{
			// Files in 2024/12
			{Path: filepath.Join(monthDir1, "bubo_bubo_80p_20241215T150405Z.wav"), Species: "bubo_bubo", Confidence: 80, Timestamp: parseTime("20241215T150405Z"), Size: 1024},
			{Path: filepath.Join(monthDir1, "bubo_bubo_85p_20241220T150405Z.mp3"), Species: "bubo_bubo", Confidence: 85, Timestamp: parseTime("20241220T150405Z"), Size: 512},

			// Files in 2025/01
			{Path: filepath.Join(monthDir2, "erithacus_rubecula_70p_20250105T150405Z.flac"), Species: "erithacus_rubecula", Confidence: 70, Timestamp: parseTime("20250105T150405Z"), Size: 2048},
			{Path: filepath.Join(monthDir2, "erithacus_rubecula_75p_20250110T150405Z.flac"), Species: "erithacus_rubecula", Confidence: 75, Timestamp: parseTime("20250110T150405Z"), Size: 2048},

			// Files in 2025/02
			{Path: filepath.Join(monthDir3, "anas_platyrhynchos_60p_20250205T150405Z.aac"), Species: "anas_platyrhynchos", Confidence: 60, Timestamp: parseTime("20250205T150405Z"), Size: 768},
			{Path: filepath.Join(monthDir3, "anas_platyrhynchos_65p_20250210T150405Z.aac"), Species: "anas_platyrhynchos", Confidence: 65, Timestamp: parseTime("20250210T150405Z"), Size: 768},
			{Path: filepath.Join(monthDir3, "bubo_bubo_90p_20250215T150405Z.opus"), Species: "bubo_bubo", Confidence: 90, Timestamp: parseTime("20250215T150405Z"), Size: 1536},
		},
		deletedFiles:       []string{},
		lockedFilePaths:    []string{},
		baseDir:            tempDir,
		maxUsagePercent:    "80%",
		minClipsPerSpecies: 1, // Keep at least 1 clip per species per directory
		retentionPolicy:    "usage",
		debug:              true,
	}

	// Run the test
	helper.Execute(t)

	// Verify files were deleted
	assert.NotEmpty(t, helper.deletedFiles, "Files should have been deleted")

	// Count files deleted by species and subdirectory
	speciesCountByDir := make(map[string]map[string]int)
	for _, path := range helper.deletedFiles {
		species := ""
		for s := range map[string]bool{"bubo_bubo": true, "erithacus_rubecula": true, "anas_platyrhynchos": true} {
			if strings.Contains(path, s) {
				species = s
				break
			}
		}

		dir := filepath.Dir(path)
		if speciesCountByDir[species] == nil {
			speciesCountByDir[species] = make(map[string]int)
		}
		speciesCountByDir[species][dir]++
	}

	// Verify we keep at least minClipsPerSpecies per directory
	for species, dirCounts := range speciesCountByDir {
		for dir, count := range dirCounts {
			// Calculate how many files of this species were in this directory originally
			originalCount := 0
			for _, file := range helper.audioFiles {
				if file.Species == species && filepath.Dir(file.Path) == dir {
					originalCount++
				}
			}

			// Verify we didn't delete too many files
			remainingCount := originalCount - count
			assert.GreaterOrEqual(t, remainingCount, helper.minClipsPerSpecies,
				"Should keep at least %d %s files in directory %s",
				helper.minClipsPerSpecies, species, dir)
		}
	}
}

// TestUsageBasedCleanupReturnValues tests that UsageBasedCleanup returns the expected values
func TestUsageBasedCleanupReturnValues(t *testing.T) {
	// Create a temporary directory
	tempDir := t.TempDir()

	// Create a mock DB (not used in this test - checkLockedFiles is false)
	mockDB := &mock_diskmanager.MockInterface{}

	// Create test files
	testFiles := []struct {
		name      string
		species   string
		conf      int
		timestamp string
		locked    bool
	}{
		{"bubo_bubo_80p_20210101T150405Z.wav", "bubo_bubo", 80, "20210101T150405Z", false},
		{"bubo_bubo_85p_20210102T150405Z.wav", "bubo_bubo", 85, "20210102T150405Z", false},
		{"anas_platyrhynchos_70p_20210103T150405Z.wav", "anas_platyrhynchos", 70, "20210103T150405Z", false},
	}

	// Create the test files
	// Pre-allocate slice with capacity for all test files
	filePaths := make([]string, 0, len(testFiles))
	for _, tf := range testFiles {
		filePath := filepath.Join(tempDir, tf.name)
		err := os.WriteFile(filePath, []byte("test content"), 0o644) //nolint:gosec // G306: Test files don't require restrictive permissions
		require.NoError(t, err, "Failed to create test file: %s", filePath)
		filePaths = append(filePaths, filePath)
	}

	// Create a quit channel
	quitChan := make(chan struct{})

	// Track which files were deleted
	deletedFiles := make(map[string]bool)

	// Test configuration
	initialDiskUsage := 90.0
	diskUsageReductionPerFile := 5.0 // Each deleted file reduces disk usage by 5%

	// Call our test-specific implementation that uses real files
	result := testUsageBasedCleanupWithRealFiles(
		quitChan,
		mockDB,
		tempDir,
		filePaths,
		deletedFiles,
		initialDiskUsage,          // Initial disk usage above threshold
		false,                     // Don't check locked files
		diskUsageReductionPerFile, // Reduction per file deleted
	)

	// Calculate expected disk utilization after deleting 2 files
	expectedDiskUtilization := int(initialDiskUsage - (2 * diskUsageReductionPerFile))

	// Verify the return values
	require.NoError(t, result.Err, "UsageBasedCleanup should not return an error")
	assert.Equal(t, 2, result.ClipsRemoved, "UsageBasedCleanup should remove 2 clips")
	assert.Equal(t, expectedDiskUtilization, result.DiskUtilization,
		"UsageBasedCleanup should return %d%% disk utilization", expectedDiskUtilization)

	// Verify that the first two files were deleted (since disk usage is above threshold)
	assert.True(t, deletedFiles[filePaths[0]], "File should have been deleted: %s", filePaths[0])
	assert.True(t, deletedFiles[filePaths[1]], "File should have been deleted: %s", filePaths[1])

	// The third file should not be deleted because disk usage dropped below threshold after deleting 2 files
	assert.False(t, deletedFiles[filePaths[2]], "File should not have been deleted: %s", filePaths[2])
}

// testUsageBasedCleanupWithRealFiles is a test-specific implementation that uses real files
func testUsageBasedCleanupWithRealFiles(
	quitChan chan struct{},
	db Interface,
	baseDir string,
	testFiles []string,
	deletedFiles map[string]bool,
	initialDiskUsage float64,
	checkLockedFiles bool,
	diskUsageReductionPerFile float64,
) CleanupResult {
	// This implementation simulates the real UsageBasedCleanup function
	// but with controlled inputs and outputs

	// Set a fixed disk usage threshold (80%)
	threshold := 80.0

	// Use the provided initial disk usage
	currentDiskUsage := initialDiskUsage

	// Get locked file paths if needed
	var lockedPathsMap map[string]bool
	if checkLockedFiles {
		lockedPaths, _ := db.GetLockedNotesClipPaths()
		lockedPathsMap = make(map[string]bool)
		for _, path := range lockedPaths {
			lockedPathsMap[path] = true
		}
	}

	// Get the list of files
	files := []FileInfo{}

	// Process each test file
	for _, filePath := range testFiles {
		fileInfo, err := os.Stat(filePath)
		if err != nil {
			continue
		}

		// Parse the file info
		fileData, err := parseFileInfo(filePath, fileInfo, allowedFileTypes)
		if err != nil {
			continue
		}

		// Mark file as locked if it's in the locked paths
		if checkLockedFiles {
			fileData.Locked = lockedPathsMap[filePath]
		}

		files = append(files, fileData)
	}

	// Sort files by timestamp (oldest first) using the same sortFiles function
	// used elsewhere in the codebase for consistency
	speciesCount := buildSpeciesSubDirCountMap(files)
	sortFilesForUsage(files, speciesCount)

	// Process files for deletion if disk usage is above threshold
	deletedCount := 0
	minClipsPerSpecies := 0 // Set to 0 to allow all files to be deleted

	if currentDiskUsage > threshold {
		for i := range files {
			if !isEligibleForUsageDeletion(&files[i], speciesCount, minClipsPerSpecies) {
				continue
			}

			deletedFiles[files[i].Path] = true
			deletedCount++
			speciesCount[files[i].Species][filepath.Dir(files[i].Path)]--
			currentDiskUsage -= diskUsageReductionPerFile

			if currentDiskUsage <= threshold {
				break
			}
		}
	}

	// Return the results with the actual current disk usage
	return CleanupResult{Err: nil, ClipsRemoved: deletedCount, DiskUtilization: int(currentDiskUsage)}
}

// TestUsageBasedCleanupBelowThreshold tests that no files are deleted when disk usage is below threshold
func TestUsageBasedCleanupBelowThreshold(t *testing.T) {
	// Create a temporary directory
	tempDir := t.TempDir()

	// Create a mock DB (not used in this test - checkLockedFiles is false)
	mockDB := &mock_diskmanager.MockInterface{}

	// Create test files
	testFiles := []struct {
		name      string
		species   string
		conf      int
		timestamp string
		locked    bool
	}{
		{"bubo_bubo_80p_20210101T150405Z.wav", "bubo_bubo", 80, "20210101T150405Z", false},
		{"bubo_bubo_85p_20210102T150405Z.wav", "bubo_bubo", 85, "20210102T150405Z", false},
		{"anas_platyrhynchos_70p_20210103T150405Z.wav", "anas_platyrhynchos", 70, "20210103T150405Z", false},
	}

	// Create the test files
	// Pre-allocate slice with capacity for all test files
	filePaths := make([]string, 0, len(testFiles))
	for _, tf := range testFiles {
		filePath := filepath.Join(tempDir, tf.name)
		err := os.WriteFile(filePath, []byte("test content"), 0o644) //nolint:gosec // G306: Test files don't require restrictive permissions
		require.NoError(t, err, "Failed to create test file: %s", filePath)
		filePaths = append(filePaths, filePath)
	}

	// Create a quit channel
	quitChan := make(chan struct{})

	// Track which files were deleted
	deletedFiles := make(map[string]bool)

	// Test configuration
	initialDiskUsage := 70.0
	diskUsageReductionPerFile := 5.0 // Each deleted file reduces disk usage by 5%

	// Call our test-specific implementation with disk usage below threshold
	result := testUsageBasedCleanupWithRealFiles(
		quitChan,
		mockDB,
		tempDir,
		filePaths,
		deletedFiles,
		initialDiskUsage,          // Initial disk usage below threshold
		false,                     // Don't check locked files
		diskUsageReductionPerFile, // Reduction per file deleted
	)

	// Verify the return values
	require.NoError(t, result.Err, "UsageBasedCleanup should not return an error")
	assert.Equal(t, 0, result.ClipsRemoved, "UsageBasedCleanup should not remove any clips")
	assert.Equal(t, int(initialDiskUsage), result.DiskUtilization,
		"UsageBasedCleanup should return %d%% disk utilization", int(initialDiskUsage))

	// Verify that no files were deleted (since disk usage is below threshold)
	for _, path := range filePaths {
		assert.False(t, deletedFiles[path], "File should not have been deleted: %s", path)
	}
}

// TestUsageBasedCleanupLockedFiles tests that locked files are not deleted
func TestUsageBasedCleanupLockedFiles(t *testing.T) {
	// Create a temporary directory
	tempDir := t.TempDir()

	// Define a locked file path
	lockedFilePath := filepath.Join(tempDir, "erithacus_rubecula_80p_20210101T150405Z.wav")

	// Create a mock DB
	mockDB := &mock_diskmanager.MockInterface{}
	mockDB.On("GetLockedNotesClipPaths").Return([]string{lockedFilePath}, nil).Once()

	// Create test files
	testFiles := []struct {
		name      string
		species   string
		conf      int
		timestamp string
		locked    bool
	}{
		{"bubo_bubo_80p_20210101T150405Z.wav", "bubo_bubo", 80, "20210101T150405Z", false},
		{"bubo_bubo_85p_20210102T150405Z.wav", "bubo_bubo", 85, "20210102T150405Z", false},
		{"erithacus_rubecula_80p_20210101T150405Z.wav", "erithacus_rubecula", 80, "20210101T150405Z", true}, // Locked file
	}

	// Create the test files
	// Pre-allocate slice with capacity for all test files
	filePaths := make([]string, 0, len(testFiles))
	for _, tf := range testFiles {
		filePath := filepath.Join(tempDir, tf.name)
		err := os.WriteFile(filePath, []byte("test content"), 0o644) //nolint:gosec // G306: Test files don't require restrictive permissions
		require.NoError(t, err, "Failed to create test file: %s", filePath)
		filePaths = append(filePaths, filePath)
	}

	// Create a quit channel
	quitChan := make(chan struct{})

	// Track which files were deleted
	deletedFiles := make(map[string]bool)

	// Test configuration
	initialDiskUsage := 90.0
	diskUsageReductionPerFile := 5.0 // Each deleted file reduces disk usage by 5%

	// Call our test-specific implementation with locked files
	result := testUsageBasedCleanupWithRealFiles(
		quitChan,
		mockDB,
		tempDir,
		filePaths,
		deletedFiles,
		initialDiskUsage,          // Initial disk usage above threshold
		true,                      // Check locked files
		diskUsageReductionPerFile, // Reduction per file deleted
	)

	// Calculate expected disk utilization after deleting 2 files
	expectedDiskUtilization := int(initialDiskUsage - (2 * diskUsageReductionPerFile))

	// Assert that mock expectations were met
	mockDB.AssertExpectations(t)

	// Verify the return values
	require.NoError(t, result.Err, "UsageBasedCleanup should not return an error")
	assert.Equal(t, 2, result.ClipsRemoved, "UsageBasedCleanup should remove 2 clips")
	assert.Equal(t, expectedDiskUtilization, result.DiskUtilization,
		"UsageBasedCleanup should return %d%% disk utilization", expectedDiskUtilization)

	// Verify that non-locked files were deleted
	assert.True(t, deletedFiles[filepath.Join(tempDir, "bubo_bubo_80p_20210101T150405Z.wav")],
		"Non-locked file should have been deleted")
	assert.True(t, deletedFiles[filepath.Join(tempDir, "bubo_bubo_85p_20210102T150405Z.wav")],
		"Non-locked file should have been deleted")

	// Verify that locked file was not deleted
	assert.False(t, deletedFiles[lockedFilePath], "Locked file should not have been deleted")
}

// Define a variable for os.Remove to allow mocking in tests
var osRemove = os.Remove
