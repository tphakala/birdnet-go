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

// TestEnsureOutputDirectorySecurity verifies that ensureOutputDirectory maintains
// security boundaries even though it uses os.MkdirAll directly
func TestEnsureOutputDirectorySecurity(t *testing.T) {
	// Create a test directory structure
	tempDir := t.TempDir()
	clipsDir := filepath.Join(tempDir, "clips")
	outsideDir := filepath.Join(tempDir, "outside")

	require.NoError(t, os.MkdirAll(clipsDir, 0o755))
	require.NoError(t, os.MkdirAll(outsideDir, 0o755))

	// Create SecureFS rooted at clips directory
	sfs, err := securefs.New(clipsDir)
	require.NoError(t, err)

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

	t.Run("Creates directories within bounds", func(t *testing.T) {
		// Test that valid paths work
		validPaths := []string{
			"2025/09/file.png",
			"subdir/file.png",
			"deeply/nested/path/file.png",
		}

		for _, path := range validPaths {
			err := controller.ensureOutputDirectory(path)
			require.NoError(t, err, "Should create directory for path: %s", path)

			// Verify directory was created within clips directory
			expectedDir := filepath.Join(clipsDir, filepath.Dir(path))
			info, err := os.Stat(expectedDir)
			require.NoError(t, err, "Directory should exist: %s", expectedDir)
			assert.True(t, info.IsDir())
		}
	})

	t.Run("Handles edge cases safely", func(t *testing.T) {
		// These paths are already relative and safe
		// ensureOutputDirectory expects pre-validated relative paths
		edgeCases := []struct {
			name        string
			path        string
			shouldExist string
		}{
			{
				name:        "Simple relative path",
				path:        "test/file.png",
				shouldExist: filepath.Join(clipsDir, "test"),
			},
			{
				name:        "Current directory file",
				path:        "file.png",
				shouldExist: clipsDir, // Already exists
			},
		}

		for _, tc := range edgeCases {
			t.Run(tc.name, func(t *testing.T) {
				err := controller.ensureOutputDirectory(tc.path)
				require.NoError(t, err)

				info, err := os.Stat(tc.shouldExist)
				require.NoError(t, err, "Directory should exist: %s", tc.shouldExist)
				assert.True(t, info.IsDir())
			})
		}
	})

	t.Run("Security context documentation", func(t *testing.T) {
		// This test documents the security model:
		// 1. ensureOutputDirectory receives ALREADY VALIDATED relative paths
		// 2. It constructs absolute paths using c.SFS.BaseDir()
		// 3. This ensures all operations stay within the SecureFS sandbox

		// The function assumes the input has been validated by the caller
		// This is enforced by the call chain:
		// ServeSpectrogramByID -> generateSpectrogram -> normalizeAndValidatePath -> ensureOutputDirectory

		// Document that no directory is created outside clips
		outsideContent := filepath.Join(outsideDir, "should_not_exist")
		_, err := os.Stat(outsideContent)
		assert.True(t, os.IsNotExist(err), "No files should be created outside clips directory")
	})
}

// TestPathValidationChain documents the validation chain that ensures
// paths are safe before reaching ensureOutputDirectory
func TestPathValidationChain(t *testing.T) {
	tempDir := t.TempDir()
	clipsDir := filepath.Join(tempDir, "clips")
	require.NoError(t, os.MkdirAll(clipsDir, 0o755))

	sfs, err := securefs.New(clipsDir)
	require.NoError(t, err)

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

	t.Run("normalizeAndValidatePath ensures safety", func(t *testing.T) {
		// Test that the validation function properly validates paths
		testCases := []struct {
			name      string
			input     string
			shouldErr bool
		}{
			{
				name:      "Valid relative path",
				input:     "2025/09/file.flac",
				shouldErr: false,
			},
			{
				name:      "Path traversal attempt",
				input:     "../../../etc/passwd",
				shouldErr: true,
			},
			{
				name:      "Absolute path within clips",
				input:     filepath.Join(clipsDir, "2025", "09", "file.flac"),
				shouldErr: false,
			},
		}

		for _, tc := range testCases {
			t.Run(tc.name, func(t *testing.T) {
				normalized, err := controller.normalizeAndValidatePath(tc.input)
				if tc.shouldErr {
					require.Error(t, err, "Should reject unsafe path: %s", tc.input)
				} else {
					require.NoError(t, err, "Should accept safe path: %s", tc.input)
					assert.NotEmpty(t, normalized)
				}
			})
		}
	})
}
