// filesystem_test.go: Package api provides tests for filesystem browsing functionality.

package api

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"

	"github.com/labstack/echo/v4"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/tphakala/birdnet-go/internal/securefs"
)

// passthroughMiddleware returns a middleware that does nothing (allows all requests).
// Used for testing endpoints that require authentication middleware.
func passthroughMiddleware() echo.MiddlewareFunc {
	return func(next echo.HandlerFunc) echo.HandlerFunc {
		return func(c echo.Context) error {
			return next(c)
		}
	}
}

// setupFilesystemTestEnvironment creates a test environment specifically for filesystem tests.
// It sets up a controller with SecureFS rooted in a temporary directory.
func setupFilesystemTestEnvironment(t *testing.T) (*echo.Echo, *Controller, string) {
	t.Helper()

	// Get base test environment
	e, _, controller := setupTestEnvironment(t)

	// Create temp directory for filesystem tests
	tempDir := t.TempDir()

	// Close existing SFS if any
	if controller.SFS != nil {
		if err := controller.SFS.Close(); err != nil {
			t.Errorf("Failed to close existing SFS: %v", err)
		}
	}

	// Create new SecureFS rooted in temp directory
	sfs, err := securefs.New(tempDir)
	require.NoError(t, err, "Failed to create SecureFS for filesystem test")
	controller.SFS = sfs

	// Set passthrough auth middleware for testing
	WithAuthMiddleware(passthroughMiddleware())(controller)

	t.Cleanup(func() {
		if err := controller.SFS.Close(); err != nil {
			t.Errorf("Failed to close SFS: %v", err)
		}
	})

	// Initialize filesystem routes
	controller.initFileSystemRoutes()

	return e, controller, tempDir
}

// TestBrowseFileSystem_BasicDirectory tests browsing a directory with files and subdirectories.
func TestBrowseFileSystem_BasicDirectory(t *testing.T) {
	t.Parallel()
	t.Attr("component", "filesystem")
	t.Attr("type", "integration")
	t.Attr("feature", "browse")

	e, _, tempDir := setupFilesystemTestEnvironment(t)

	// Create test structure
	subDir := filepath.Join(tempDir, "subdir")
	require.NoError(t, os.Mkdir(subDir, 0o750))

	testFile := filepath.Join(tempDir, "test.txt")
	require.NoError(t, os.WriteFile(testFile, []byte("test content"), 0o600))

	// Create test server
	server := httptest.NewServer(e)
	defer server.Close()

	client := createTestHTTPClient(testResponseHeaderTimeout)

	// Make request to browse the temp directory
	resp, err := client.Get(server.URL + "/api/v2/filesystem/browse?path=" + tempDir)
	require.NoError(t, err)
	defer resp.Body.Close() //nolint:errcheck // Ignore close error in test //nolint:errcheck // Ignore close error in test

	assert.Equal(t, http.StatusOK, resp.StatusCode)

	var response BrowseResponse
	require.NoError(t, json.NewDecoder(resp.Body).Decode(&response))

	// Verify response
	assert.Equal(t, tempDir, response.CurrentPath)
	assert.Len(t, response.Items, 2) // subdir and test.txt

	// Find items by name
	var foundDir, foundFile bool
	for _, item := range response.Items {
		switch item.Name {
		case "subdir":
			assert.Equal(t, "folder", item.Type)
			foundDir = true
		case "test.txt":
			assert.Equal(t, "file", item.Type)
			assert.Equal(t, int64(12), item.Size) // "test content" = 12 bytes
			foundFile = true
		}
	}
	assert.True(t, foundDir, "Expected to find subdir")
	assert.True(t, foundFile, "Expected to find test.txt")
}

// TestBrowseFileSystem_EmptyPath tests browsing with no path specified (defaults to base directory).
func TestBrowseFileSystem_EmptyPath(t *testing.T) {
	t.Parallel()
	t.Attr("component", "filesystem")
	t.Attr("type", "unit")
	t.Attr("feature", "browse-default")

	e, _, tempDir := setupFilesystemTestEnvironment(t)

	// Create a test file in the root
	testFile := filepath.Join(tempDir, "rootfile.txt")
	require.NoError(t, os.WriteFile(testFile, []byte("root"), 0o600))

	server := httptest.NewServer(e)
	defer server.Close()

	client := createTestHTTPClient(testResponseHeaderTimeout)

	// Browse with no path parameter
	resp, err := client.Get(server.URL + "/api/v2/filesystem/browse")
	require.NoError(t, err)
	defer resp.Body.Close() //nolint:errcheck // Ignore close error in test

	assert.Equal(t, http.StatusOK, resp.StatusCode)

	var response BrowseResponse
	require.NoError(t, json.NewDecoder(resp.Body).Decode(&response))

	// Should use the SecureFS base directory
	assert.Equal(t, tempDir, response.CurrentPath)
	assert.Len(t, response.Items, 1)
	assert.Equal(t, "rootfile.txt", response.Items[0].Name)
}

// TestBrowseFileSystem_SubDirectory tests browsing a subdirectory.
func TestBrowseFileSystem_SubDirectory(t *testing.T) {
	t.Parallel()
	t.Attr("component", "filesystem")
	t.Attr("type", "integration")
	t.Attr("feature", "browse-subdirectory")

	e, _, tempDir := setupFilesystemTestEnvironment(t)

	// Create nested structure
	subDir := filepath.Join(tempDir, "level1", "level2")
	require.NoError(t, os.MkdirAll(subDir, 0o750))

	nestedFile := filepath.Join(subDir, "nested.txt")
	require.NoError(t, os.WriteFile(nestedFile, []byte("nested"), 0o600))

	server := httptest.NewServer(e)
	defer server.Close()

	client := createTestHTTPClient(testResponseHeaderTimeout)

	// Browse the nested directory
	resp, err := client.Get(server.URL + "/api/v2/filesystem/browse?path=" + subDir)
	require.NoError(t, err)
	defer resp.Body.Close() //nolint:errcheck // Ignore close error in test

	assert.Equal(t, http.StatusOK, resp.StatusCode)

	var response BrowseResponse
	require.NoError(t, json.NewDecoder(resp.Body).Decode(&response))

	assert.Equal(t, subDir, response.CurrentPath)
	assert.NotEmpty(t, response.ParentPath)
	assert.Len(t, response.Items, 1)
	assert.Equal(t, "nested.txt", response.Items[0].Name)
}

// TestBrowseFileSystem_PathTraversal tests that path traversal attempts are blocked.
func TestBrowseFileSystem_PathTraversal(t *testing.T) {
	t.Parallel()
	t.Attr("component", "filesystem")
	t.Attr("type", "security")
	t.Attr("feature", "path-traversal-prevention")

	e, _, tempDir := setupFilesystemTestEnvironment(t)

	server := httptest.NewServer(e)
	defer server.Close()

	client := createTestHTTPClient(testResponseHeaderTimeout)

	// Test various path traversal attempts
	traversalPaths := []struct {
		name string
		path string
	}{
		{"Parent directory", tempDir + "/.."},
		{"Double parent", tempDir + "/../.."},
		{"Absolute root", "/"},
		{"Absolute etc", "/etc"},
		{"Home directory", "/home"},
	}

	for _, tc := range traversalPaths {
		t.Run(tc.name, func(t *testing.T) {
			// Note: Not parallel - shared server connection
			resp, err := client.Get(server.URL + "/api/v2/filesystem/browse?path=" + tc.path)
			require.NoError(t, err)
			defer resp.Body.Close() //nolint:errcheck // Ignore close error in test

			// Should be rejected (either BadRequest or NotFound or Forbidden)
			assert.True(t, resp.StatusCode >= 400 && resp.StatusCode < 500,
				"Path %q should be rejected, got status %d", tc.path, resp.StatusCode)
		})
	}
}

// TestBrowseFileSystem_NonExistentPath tests browsing a path that doesn't exist.
func TestBrowseFileSystem_NonExistentPath(t *testing.T) {
	t.Parallel()
	t.Attr("component", "filesystem")
	t.Attr("type", "unit")
	t.Attr("feature", "error-handling")

	e, _, tempDir := setupFilesystemTestEnvironment(t)

	server := httptest.NewServer(e)
	defer server.Close()

	client := createTestHTTPClient(testResponseHeaderTimeout)

	nonExistent := filepath.Join(tempDir, "does-not-exist")
	resp, err := client.Get(server.URL + "/api/v2/filesystem/browse?path=" + nonExistent)
	require.NoError(t, err)
	defer resp.Body.Close() //nolint:errcheck // Ignore close error in test

	assert.Equal(t, http.StatusNotFound, resp.StatusCode)
}

// TestBrowseFileSystem_FileNotDirectory tests browsing a file path (not a directory).
func TestBrowseFileSystem_FileNotDirectory(t *testing.T) {
	t.Parallel()
	t.Attr("component", "filesystem")
	t.Attr("type", "unit")
	t.Attr("feature", "error-handling")

	e, _, tempDir := setupFilesystemTestEnvironment(t)

	// Create a file
	testFile := filepath.Join(tempDir, "notadir.txt")
	require.NoError(t, os.WriteFile(testFile, []byte("content"), 0o600))

	server := httptest.NewServer(e)
	defer server.Close()

	client := createTestHTTPClient(testResponseHeaderTimeout)

	// Try to browse the file as if it were a directory
	resp, err := client.Get(server.URL + "/api/v2/filesystem/browse?path=" + testFile)
	require.NoError(t, err)
	defer resp.Body.Close() //nolint:errcheck // Ignore close error in test

	assert.Equal(t, http.StatusBadRequest, resp.StatusCode)
}

// TestBrowseFileSystem_EmptyDirectory tests browsing an empty directory.
func TestBrowseFileSystem_EmptyDirectory(t *testing.T) {
	t.Parallel()
	t.Attr("component", "filesystem")
	t.Attr("type", "unit")
	t.Attr("feature", "browse-empty")

	e, _, tempDir := setupFilesystemTestEnvironment(t)

	// Create an empty subdirectory
	emptyDir := filepath.Join(tempDir, "empty")
	require.NoError(t, os.Mkdir(emptyDir, 0o750))

	server := httptest.NewServer(e)
	defer server.Close()

	client := createTestHTTPClient(testResponseHeaderTimeout)

	resp, err := client.Get(server.URL + "/api/v2/filesystem/browse?path=" + emptyDir)
	require.NoError(t, err)
	defer resp.Body.Close() //nolint:errcheck // Ignore close error in test

	assert.Equal(t, http.StatusOK, resp.StatusCode)

	var response BrowseResponse
	require.NoError(t, json.NewDecoder(resp.Body).Decode(&response))

	assert.Equal(t, emptyDir, response.CurrentPath)
	assert.Empty(t, response.Items)
	// SecureFS.ParentPath returns "" for direct children of root
	assert.Empty(t, response.ParentPath)
}

// TestBrowseFileSystem_ParentPath tests that parent path is correctly computed.
func TestBrowseFileSystem_ParentPath(t *testing.T) {
	t.Parallel()
	t.Attr("component", "filesystem")
	t.Attr("type", "unit")
	t.Attr("feature", "parent-navigation")

	e, _, tempDir := setupFilesystemTestEnvironment(t)

	// Create nested structure
	level1 := filepath.Join(tempDir, "level1")
	level2 := filepath.Join(level1, "level2")
	require.NoError(t, os.MkdirAll(level2, 0o750))

	server := httptest.NewServer(e)
	defer server.Close()

	client := createTestHTTPClient(testResponseHeaderTimeout)

	tests := []struct {
		name               string
		path               string
		expectedParentPath string
	}{
		{
			name:               "Root has no parent",
			path:               tempDir,
			expectedParentPath: "", // No parent for root
		},
		{
			name:               "Level1 parent is root - returns empty for direct children",
			path:               level1,
			expectedParentPath: "", // SecureFS.ParentPath returns "" for direct children of root
		},
		{
			name:               "Level2 parent is level1",
			path:               level2,
			expectedParentPath: level1,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			// Note: Not parallel - shared server connection
			resp, err := client.Get(server.URL + "/api/v2/filesystem/browse?path=" + tc.path)
			require.NoError(t, err)
			defer resp.Body.Close() //nolint:errcheck // Ignore close error in test

			assert.Equal(t, http.StatusOK, resp.StatusCode)

			var response BrowseResponse
			require.NoError(t, json.NewDecoder(resp.Body).Decode(&response))

			assert.Equal(t, tc.expectedParentPath, response.ParentPath)
		})
	}
}

// TestConvertDirEntryToItem tests the convertDirEntryToItem helper function.
func TestConvertDirEntryToItem(t *testing.T) {
	t.Parallel()
	t.Attr("component", "filesystem")
	t.Attr("type", "unit")
	t.Attr("feature", "dir-entry-conversion")

	_, controller, tempDir := setupFilesystemTestEnvironment(t)

	// Create test files
	testFile := filepath.Join(tempDir, "testfile.txt")
	require.NoError(t, os.WriteFile(testFile, []byte("hello world"), 0o600))

	subDir := filepath.Join(tempDir, "subdir")
	require.NoError(t, os.Mkdir(subDir, 0o750))

	// Read directory entries
	entries, err := os.ReadDir(tempDir)
	require.NoError(t, err)
	require.Len(t, entries, 2)

	for _, entry := range entries {
		item, err := controller.convertDirEntryToItem(tempDir, entry)
		require.NoError(t, err)

		assert.Equal(t, entry.Name(), item.Name)
		assert.Equal(t, filepath.Join(tempDir, entry.Name()), item.ID)

		if entry.IsDir() {
			assert.Equal(t, "folder", item.Type)
		} else {
			assert.Equal(t, "file", item.Type)
			assert.Equal(t, int64(11), item.Size) // "hello world" = 11 bytes
		}
	}
}

// TestBrowseFileSystem_MixedFileTypes tests browsing a directory with various file types.
func TestBrowseFileSystem_MixedFileTypes(t *testing.T) {
	t.Parallel()
	t.Attr("component", "filesystem")
	t.Attr("type", "integration")
	t.Attr("feature", "file-types")

	e, _, tempDir := setupFilesystemTestEnvironment(t)

	// Create various file types
	files := map[string]string{
		"audio.wav":  "RIFF",
		"image.png":  "\x89PNG",
		"text.txt":   "plain text",
		"config.yml": "key: value",
	}

	for name, content := range files {
		require.NoError(t, os.WriteFile(filepath.Join(tempDir, name), []byte(content), 0o600))
	}

	// Create a subdirectory
	require.NoError(t, os.Mkdir(filepath.Join(tempDir, "clips"), 0o750))

	server := httptest.NewServer(e)
	defer server.Close()

	client := createTestHTTPClient(testResponseHeaderTimeout)

	resp, err := client.Get(server.URL + "/api/v2/filesystem/browse?path=" + tempDir)
	require.NoError(t, err)
	defer resp.Body.Close() //nolint:errcheck // Ignore close error in test

	assert.Equal(t, http.StatusOK, resp.StatusCode)

	var response BrowseResponse
	require.NoError(t, json.NewDecoder(resp.Body).Decode(&response))

	assert.Len(t, response.Items, 5) // 4 files + 1 directory

	// Count types
	fileCount, folderCount := 0, 0
	for _, item := range response.Items {
		switch item.Type {
		case "file":
			fileCount++
		case "folder":
			folderCount++
		}
	}
	assert.Equal(t, 4, fileCount)
	assert.Equal(t, 1, folderCount)
}

// TestBrowseFileSystem_LargeDirectory tests browsing a directory with many items.
func TestBrowseFileSystem_LargeDirectory(t *testing.T) {
	t.Parallel()
	t.Attr("component", "filesystem")
	t.Attr("type", "performance")
	t.Attr("feature", "large-directory")

	e, _, tempDir := setupFilesystemTestEnvironment(t)

	// Create 100 files
	fileCount := 100
	for i := range fileCount {
		fileName := filepath.Join(tempDir, "file_"+string(rune('a'+i%26))+"_"+string(rune('0'+i/26))+".txt")
		require.NoError(t, os.WriteFile(fileName, []byte("content"), 0o600))
	}

	server := httptest.NewServer(e)
	defer server.Close()

	client := createTestHTTPClient(testResponseHeaderTimeout)

	resp, err := client.Get(server.URL + "/api/v2/filesystem/browse?path=" + tempDir)
	require.NoError(t, err)
	defer resp.Body.Close() //nolint:errcheck // Ignore close error in test

	assert.Equal(t, http.StatusOK, resp.StatusCode)

	var response BrowseResponse
	require.NoError(t, json.NewDecoder(resp.Body).Decode(&response))

	assert.Len(t, response.Items, fileCount)
}

// TestBrowseFileSystem_SpecialCharactersInPath tests paths with special characters.
func TestBrowseFileSystem_SpecialCharactersInPath(t *testing.T) {
	t.Parallel()
	t.Attr("component", "filesystem")
	t.Attr("type", "unit")
	t.Attr("feature", "special-characters")

	e, _, tempDir := setupFilesystemTestEnvironment(t)

	// Create directories with special characters (that are valid on Linux)
	specialDirs := []string{
		"dir with spaces",
		"dir-with-dashes",
		"dir_with_underscores",
		"dir.with.dots",
	}

	for _, dirName := range specialDirs {
		dirPath := filepath.Join(tempDir, dirName)
		require.NoError(t, os.Mkdir(dirPath, 0o750))
	}

	server := httptest.NewServer(e)
	defer server.Close()

	client := createTestHTTPClient(testResponseHeaderTimeout)

	resp, err := client.Get(server.URL + "/api/v2/filesystem/browse?path=" + tempDir)
	require.NoError(t, err)
	defer resp.Body.Close() //nolint:errcheck // Ignore close error in test

	assert.Equal(t, http.StatusOK, resp.StatusCode)

	var response BrowseResponse
	require.NoError(t, json.NewDecoder(resp.Body).Decode(&response))

	assert.Len(t, response.Items, len(specialDirs))

	// Verify all special directory names are present
	names := make(map[string]bool)
	for _, item := range response.Items {
		names[item.Name] = true
	}

	for _, dirName := range specialDirs {
		assert.True(t, names[dirName], "Expected to find directory %q", dirName)
	}
}

// TestValidateSymlinkTarget tests the validateSymlinkTarget helper function.
func TestValidateSymlinkTarget(t *testing.T) {
	t.Parallel()
	t.Attr("component", "filesystem")
	t.Attr("type", "security")
	t.Attr("feature", "symlink-validation")

	_, controller, tempDir := setupFilesystemTestEnvironment(t)

	// Create a subdirectory and file for symlink targets
	targetDir := filepath.Join(tempDir, "target")
	require.NoError(t, os.Mkdir(targetDir, 0o750))

	targetFile := filepath.Join(targetDir, "file.txt")
	require.NoError(t, os.WriteFile(targetFile, []byte("target"), 0o600))

	tests := []struct {
		name        string
		setup       func() string // Returns symlink path
		expectError bool
	}{
		{
			name: "Valid symlink within base",
			setup: func() string {
				symlinkPath := filepath.Join(tempDir, "valid_link")
				err := os.Symlink(targetDir, symlinkPath)
				require.NoError(t, err)
				return symlinkPath
			},
			expectError: false,
		},
		{
			name: "Valid relative symlink within base",
			setup: func() string {
				symlinkPath := filepath.Join(tempDir, "relative_link")
				err := os.Symlink("target", symlinkPath)
				require.NoError(t, err)
				return symlinkPath
			},
			expectError: false,
		},
		{
			name: "Invalid symlink to outside base",
			setup: func() string {
				symlinkPath := filepath.Join(tempDir, "escape_link")
				err := os.Symlink("/etc", symlinkPath)
				require.NoError(t, err)
				return symlinkPath
			},
			expectError: true,
		},
		{
			name: "Invalid symlink with parent traversal",
			setup: func() string {
				symlinkPath := filepath.Join(tempDir, "traversal_link")
				err := os.Symlink("../../../etc", symlinkPath)
				require.NoError(t, err)
				return symlinkPath
			},
			expectError: true,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			// Note: Not parallel because setup creates files in shared directory
			symlinkPath := tc.setup()

			err := controller.validateSymlinkTarget(symlinkPath)
			if tc.expectError {
				assert.Error(t, err, "Expected error for symlink %q", symlinkPath)
			} else {
				assert.NoError(t, err, "Expected no error for symlink %q", symlinkPath)
			}
		})
	}
}

// TestBrowseFileSystem_SymlinkHandling tests how symlinks are displayed in browse results.
func TestBrowseFileSystem_SymlinkHandling(t *testing.T) {
	t.Parallel()
	t.Attr("component", "filesystem")
	t.Attr("type", "integration")
	t.Attr("feature", "symlink-display")

	e, _, tempDir := setupFilesystemTestEnvironment(t)

	// Create a target directory with a file
	targetDir := filepath.Join(tempDir, "target")
	require.NoError(t, os.Mkdir(targetDir, 0o750))
	require.NoError(t, os.WriteFile(filepath.Join(targetDir, "file.txt"), []byte("content"), 0o600))

	// Create a valid symlink to the target directory
	validLink := filepath.Join(tempDir, "valid_link")
	require.NoError(t, os.Symlink(targetDir, validLink))

	server := httptest.NewServer(e)
	defer server.Close()

	client := createTestHTTPClient(testResponseHeaderTimeout)

	resp, err := client.Get(server.URL + "/api/v2/filesystem/browse?path=" + tempDir)
	require.NoError(t, err)
	defer resp.Body.Close() //nolint:errcheck // Ignore close error in test

	assert.Equal(t, http.StatusOK, resp.StatusCode)

	var response BrowseResponse
	require.NoError(t, json.NewDecoder(resp.Body).Decode(&response))

	// Should have target directory and the symlink
	assert.Len(t, response.Items, 2)

	// Find the symlink item
	var foundSymlink bool
	for _, item := range response.Items {
		if item.Name == "valid_link" {
			// Symlink to directory is shown as folder (follows symlink for type)
			assert.Contains(t, []string{"folder", "symlink"}, item.Type,
				"Symlink should be shown as folder or symlink")
			foundSymlink = true
		}
	}
	assert.True(t, foundSymlink, "Expected to find symlink in results")
}
