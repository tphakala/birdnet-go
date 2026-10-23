// filesystem_test.go: tests for the filesystem domain browse endpoint.

package filesystem

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"runtime"
	"testing"

	"github.com/labstack/echo/v4"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/tphakala/birdnet-go/internal/api/v2/apitest"
)

// osWindows is the runtime.GOOS value for Windows. Used to gate the symlink
// tests, where creating a symlink requires SeCreateSymbolicLinkPrivilege.
const osWindows = "windows"

// passthroughMiddleware returns a middleware that does nothing (allows all requests).
// Used for testing endpoints that require authentication middleware: the
// /filesystem group is created with c.AuthMiddleware, so a non-nil passthrough is
// needed for behavioral tests that route HTTP requests through it.
func passthroughMiddleware() echo.MiddlewareFunc {
	return func(next echo.HandlerFunc) echo.HandlerFunc {
		return func(c echo.Context) error {
			return next(c)
		}
	}
}

// setupFilesystemTestEnvironment creates a test environment specifically for filesystem tests.
// apitest.NewCore already provisions an isolated SecureFS rooted in its own
// per-test t.TempDir() (and registers cleanup that closes it before the TempDir
// is removed), so this reuses that base directory as the browse root instead of
// re-rooting SFS, which would invert the cleanup order and break Windows TempDir
// removal. A passthrough auth middleware is injected so requests route through
// the /filesystem group middleware. Many callers run in parallel, so the core
// does not publish settings (a process-global); BrowseFileSystem reads none.
func setupFilesystemTestEnvironment(t *testing.T) (*echo.Echo, *Handler, string) {
	t.Helper()

	e := echo.New()
	core := apitest.NewCore(t, apitest.WithEcho(e), apitest.WithoutSettingsPublish())

	// Set passthrough auth middleware for testing so the /filesystem group
	// middleware (c.AuthMiddleware) is non-nil and lets requests through.
	core.AuthMiddleware = passthroughMiddleware()

	// Use the SecureFS base directory apitest already rooted at a per-test
	// t.TempDir() as the browse root.
	tempDir := core.SFS.BaseDir()

	h := New(core)
	h.RegisterRoutes(core.Group)

	return e, h, tempDir
}

// symlinkOrSkip creates a symlink and skips the test if the OS denies it.
// Creating symlinks on Windows requires SeCreateSymbolicLinkPrivilege
// (admin or Developer Mode). CI runners have it, but a local developer may
// not, so skip rather than fail when creation is not permitted.
func symlinkOrSkip(t *testing.T, target, link string) string {
	t.Helper()
	if err := os.Symlink(target, link); err != nil {
		// Without SeCreateSymbolicLinkPrivilege (admin or Developer Mode)
		// os.Symlink fails on Windows with ERROR_PRIVILEGE_NOT_HELD. Gate the
		// skip on GOOS, not errors.Is(err, os.ErrPermission): syscall.Errno.Is
		// maps only ERROR_ACCESS_DENIED/EACCES/EPERM to os.ErrPermission, so
		// it would NOT match ERROR_PRIVILEGE_NOT_HELD and the skip would never
		// fire. On Unix a symlink failure is a genuine error and must fail.
		if runtime.GOOS == osWindows {
			t.Skipf("skipping: cannot create symlink (needs privilege/Developer Mode on Windows): %v", err)
		}
		require.NoError(t, err)
	}
	return link
}

// TestBrowseFileSystem_BasicDirectory tests browsing a directory with files and subdirectories.
func TestBrowseFileSystem_BasicDirectory(t *testing.T) {
	t.Parallel()
	t.Attr("component", "filesystem")
	t.Attr("type", "integration")
	t.Attr("feature", "browse")

	e, _, tempDir := setupFilesystemTestEnvironment(t)

	// Create test structure. The directory name has a space and a dash, so a
	// handler that rewrites item names fails here.
	const subDirName = "sub dir-1"
	subDir := filepath.Join(tempDir, subDirName)
	require.NoError(t, os.Mkdir(subDir, 0o750))

	testFile := filepath.Join(tempDir, "test.txt")
	require.NoError(t, os.WriteFile(testFile, []byte("test content"), 0o600))

	// Create test server
	server := httptest.NewServer(e)
	defer server.Close()

	client := apitest.NewTestHTTPClient(apitest.TestResponseHeaderTimeout)

	// Make request to browse the temp directory
	resp, err := client.Get(server.URL + "/api/v2/filesystem/browse?path=" + tempDir)
	require.NoError(t, err)
	defer resp.Body.Close() //nolint:errcheck // Ignore close error in test

	assert.Equal(t, http.StatusOK, resp.StatusCode)

	var response BrowseResponse
	require.NoError(t, json.NewDecoder(resp.Body).Decode(&response))

	// Verify response
	assert.Equal(t, tempDir, response.CurrentPath)
	assert.Len(t, response.Items, 2) // the subdirectory and test.txt

	// Find items by name
	var foundDir, foundFile bool
	for _, item := range response.Items {
		switch item.Name {
		case subDirName:
			assert.Equal(t, "folder", item.Type)
			foundDir = true
		case "test.txt":
			assert.Equal(t, "file", item.Type)
			assert.Equal(t, int64(12), item.Size) // "test content" = 12 bytes
			foundFile = true
		}
	}
	assert.True(t, foundDir, "Expected to find %q", subDirName)
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

	client := apitest.NewTestHTTPClient(apitest.TestResponseHeaderTimeout)

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

	client := apitest.NewTestHTTPClient(apitest.TestResponseHeaderTimeout)

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

	client := apitest.NewTestHTTPClient(apitest.TestResponseHeaderTimeout)

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

	client := apitest.NewTestHTTPClient(apitest.TestResponseHeaderTimeout)

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

	client := apitest.NewTestHTTPClient(apitest.TestResponseHeaderTimeout)

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

	client := apitest.NewTestHTTPClient(apitest.TestResponseHeaderTimeout)

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

	client := apitest.NewTestHTTPClient(apitest.TestResponseHeaderTimeout)

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

	_, handler, tempDir := setupFilesystemTestEnvironment(t)

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
		item, err := handler.convertDirEntryToItem(tempDir, entry)
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

// TestValidateSymlinkTarget tests the validateSymlinkTarget helper function.
func TestValidateSymlinkTarget(t *testing.T) {
	t.Parallel()
	t.Attr("component", "filesystem")
	t.Attr("type", "security")
	t.Attr("feature", "symlink-validation")

	_, handler, tempDir := setupFilesystemTestEnvironment(t)

	// Create a subdirectory and file for symlink targets
	targetDir := filepath.Join(tempDir, "target")
	require.NoError(t, os.Mkdir(targetDir, 0o750))

	targetFile := filepath.Join(targetDir, "file.txt")
	require.NoError(t, os.WriteFile(targetFile, []byte("target"), 0o600))

	tests := []struct {
		name        string
		setup       func(t *testing.T) string // Returns symlink path; may t.Skip if symlinks are unavailable
		expectError bool
	}{
		{
			name: "Valid symlink within base",
			setup: func(t *testing.T) string {
				t.Helper()
				return symlinkOrSkip(t, targetDir, filepath.Join(tempDir, "valid_link"))
			},
			expectError: false,
		},
		{
			name: "Valid relative symlink within base",
			setup: func(t *testing.T) string {
				t.Helper()
				return symlinkOrSkip(t, "target", filepath.Join(tempDir, "relative_link"))
			},
			expectError: false,
		},
		{
			name: "Invalid symlink to outside base",
			setup: func(t *testing.T) string {
				t.Helper()
				// An absolute path outside the sandbox base. "/etc" is absolute
				// on Unix but drive-relative on Windows (filepath.IsAbs is false),
				// where it would resolve inside the base and not be rejected, so
				// use a Windows-absolute target there to exercise the escaping-
				// symlink rejection on every OS.
				outside := "/etc"
				if runtime.GOOS == osWindows {
					outside = `C:\Windows`
				}
				return symlinkOrSkip(t, outside, filepath.Join(tempDir, "escape_link"))
			},
			expectError: true,
		},
		{
			name: "Invalid symlink with parent traversal",
			setup: func(t *testing.T) string {
				t.Helper()
				return symlinkOrSkip(t, "../../../etc", filepath.Join(tempDir, "traversal_link"))
			},
			expectError: true,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			// Note: Not parallel because setup creates files in shared directory
			symlinkPath := tc.setup(t)

			err := handler.validateSymlinkTarget(symlinkPath)
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
	symlinkOrSkip(t, targetDir, validLink)

	server := httptest.NewServer(e)
	defer server.Close()

	client := apitest.NewTestHTTPClient(apitest.TestResponseHeaderTimeout)

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
			// convertDirEntryToItem classifies by the entry's own mode, not the
			// link target's, so a link to a directory is "symlink", not "folder".
			assert.Equal(t, "symlink", item.Type, "a symlink to a directory must not be reported as a folder")
			foundSymlink = true
		}
	}
	assert.True(t, foundSymlink, "Expected to find symlink in results")
}
