package api

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/tphakala/birdnet-go/internal/conf"
	"github.com/tphakala/birdnet-go/internal/securefs"
)

// TestEnsureOutputDirectory verifies that directories are created in the correct location
func TestEnsureOutputDirectory(t *testing.T) {
	// Create a temporary directory structure
	tempDir := t.TempDir()
	clipsDir := filepath.Join(tempDir, "clips")
	require.NoError(t, os.MkdirAll(clipsDir, 0o755))

	// Create SecureFS rooted at clips directory
	sfs, err := securefs.New(clipsDir)
	require.NoError(t, err)

	// Create a minimal controller with just what we need for the test
	controller := &Controller{
		Settings: &conf.Settings{
			Realtime: conf.RealtimeSettings{
				Audio: conf.AudioSettings{
					Export: conf.ExportSettings{
						Path: clipsDir,
					},
				},
			},
		},
		SFS: sfs,
	}

	tests := []struct {
		name                   string
		relSpectrogramPath     string
		expectedDirToBeCreated string
	}{
		{
			name:                   "Year/Month directory structure",
			relSpectrogramPath:     "2025/09/test_file.png",
			expectedDirToBeCreated: filepath.Join(clipsDir, "2025", "09"),
		},
		{
			name:                   "Deep nested structure",
			relSpectrogramPath:     "2025/12/31/subfolder/test.png",
			expectedDirToBeCreated: filepath.Join(clipsDir, "2025", "12", "31", "subfolder"),
		},
		{
			name:                   "Root level file",
			relSpectrogramPath:     "test.png",
			expectedDirToBeCreated: clipsDir,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			// Ensure the directory doesn't exist before the test
			if tt.expectedDirToBeCreated != clipsDir {
				_, err := os.Stat(tt.expectedDirToBeCreated)
				assert.True(t, os.IsNotExist(err), "Directory should not exist before test")
			}

			// Call ensureOutputDirectory
			err := controller.ensureOutputDirectory(tt.relSpectrogramPath)
			require.NoError(t, err)

			// Verify the directory was created
			info, err := os.Stat(tt.expectedDirToBeCreated)
			require.NoError(t, err, "Directory should exist: %s", tt.expectedDirToBeCreated)
			assert.True(t, info.IsDir(), "Should be a directory")
		})
	}
}

// TestNormalizeClipPath verifies path normalization works correctly
func TestNormalizeClipPath(t *testing.T) {
	clipsPrefix := "/home/user/clips"

	tests := []struct {
		name     string
		input    string
		prefix   string
		expected string
	}{
		{
			name:     "Already relative path",
			input:    "2025/09/bird.flac",
			prefix:   clipsPrefix,
			expected: "2025/09/bird.flac",
		},
		{
			name:     "Absolute path with prefix",
			input:    "/home/user/clips/2025/09/bird.flac",
			prefix:   clipsPrefix,
			expected: "2025/09/bird.flac",
		},
		{
			name:     "Absolute path with trailing slash prefix",
			input:    "/home/user/clips/2025/09/bird.flac",
			prefix:   clipsPrefix + "/",
			expected: "2025/09/bird.flac",
		},
		{
			name:     "Just filename",
			input:    "bird.flac",
			prefix:   clipsPrefix,
			expected: "bird.flac",
		},
		{
			name:     "Empty prefix",
			input:    "2025/09/bird.flac",
			prefix:   "",
			expected: "2025/09/bird.flac",
		},
		{
			name:     "Windows-style path",
			input:    filepath.Join("C:", "clips", "2025", "09", "bird.flac"),
			prefix:   filepath.Join("C:", "clips"),
			expected: filepath.Join("2025", "09", "bird.flac"),
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := NormalizeClipPath(tt.input, tt.prefix)
			// Use filepath.ToSlash for consistent comparison across platforms
			assert.Equal(t, filepath.ToSlash(tt.expected), filepath.ToSlash(result))
		})
	}
}

// TestSecureFSPathResolution verifies that SecureFS correctly resolves paths
func TestSecureFSPathResolution(t *testing.T) {
	tempDir := t.TempDir()
	clipsDir := filepath.Join(tempDir, "clips")

	// Create test directory structure
	testDirs := []string{
		filepath.Join(clipsDir, "2025", "09"),
		filepath.Join(clipsDir, "2025", "10"),
		filepath.Join(clipsDir, "2026", "01"),
	}

	for _, dir := range testDirs {
		require.NoError(t, os.MkdirAll(dir, 0o755))
	}

	// Create test files
	testFiles := []string{
		"2025/09/bird1.flac",
		"2025/10/bird2.flac",
		"2026/01/bird3.flac",
	}

	for _, relPath := range testFiles {
		absPath := filepath.Join(clipsDir, relPath)
		require.NoError(t, os.WriteFile(absPath, []byte("test content"), 0o644))
	}

	// Create SecureFS
	sfs, err := securefs.New(clipsDir)
	require.NoError(t, err)

	// Test StatRel finds files correctly
	for _, relPath := range testFiles {
		t.Run("StatRel_"+relPath, func(t *testing.T) {
			info, err := sfs.StatRel(relPath)
			require.NoError(t, err, "Should find file: %s", relPath)
			assert.NotNil(t, info)
			assert.Positive(t, info.Size())
		})
	}

	// Test StatRel doesn't find non-existent files
	nonExistentFiles := []string{
		"2025/09/nonexistent.flac",
		"2027/01/future.flac",
		"invalid/path/file.flac",
	}

	for _, relPath := range nonExistentFiles {
		t.Run("StatRel_NotFound_"+relPath, func(t *testing.T) {
			_, err := sfs.StatRel(relPath)
			assert.True(t, os.IsNotExist(err), "Should not find file: %s", relPath)
		})
	}
}
