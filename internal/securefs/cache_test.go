package securefs

import (
	"errors"
	"io/fs"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// Test constants for path resolution testing.
const testResolvedPath = "/resolved/path"

// TestGetSymlinkResolutionDoesNotCacheErrors verifies symlink resolution errors are not cached
func TestGetSymlinkResolutionDoesNotCacheErrors(t *testing.T) {
	pc := NewPathCache()
	callCount := 0
	transientErr := errors.New("transient error")

	compute := func(path string) (string, error) {
		callCount++
		if callCount == 1 {
			return "", transientErr
		}
		return testResolvedPath, nil
	}

	// First call - returns error
	_, err := pc.GetSymlinkResolution("test/path", compute)
	require.Error(t, err, "expected error on first call")

	// Second call - should retry (not return cached error)
	resolved, err := pc.GetSymlinkResolution("test/path", compute)
	require.NoError(t, err, "expected success on second call")
	assert.Equal(t, testResolvedPath, resolved)
	assert.Equal(t, 2, callCount, "expected compute to be called twice")
}

// TestGetStatDoesNotCacheErrors verifies stat errors are not cached
func TestGetStatDoesNotCacheErrors(t *testing.T) {
	pc := NewPathCache()
	callCount := 0
	transientErr := errors.New("file temporarily unavailable")

	compute := func(path string) (fs.FileInfo, error) {
		callCount++
		if callCount == 1 {
			return nil, transientErr
		}
		return &mockFileInfo{name: "test.txt"}, nil
	}

	// First call - returns error
	_, err := pc.GetStat("test/path", compute)
	require.Error(t, err, "expected error on first call")

	// Second call - should retry (not return cached error)
	info, err := pc.GetStat("test/path", compute)
	require.NoError(t, err, "expected success on second call")
	require.NotNil(t, info, "expected valid FileInfo")
	assert.Equal(t, "test.txt", info.Name())
	assert.Equal(t, 2, callCount, "expected compute to be called twice")
}

// TestGetAbsPathDoesNotCacheErrors verifies absolute path errors are not cached
func TestGetAbsPathDoesNotCacheErrors(t *testing.T) {
	pc := NewPathCache()
	callCount := 0
	transientErr := errors.New("transient error")

	compute := func(path string) (string, error) {
		callCount++
		if callCount == 1 {
			return "", transientErr
		}
		return "/absolute/path", nil
	}

	// First call - returns error
	_, err := pc.GetAbsPath("test/path", compute)
	require.Error(t, err, "expected error on first call")

	// Second call - should retry
	result, err := pc.GetAbsPath("test/path", compute)
	require.NoError(t, err, "expected success on second call")
	assert.Equal(t, "/absolute/path", result)
	assert.Equal(t, 2, callCount, "expected compute to be called twice")
}

// TestGetValidatePathDoesNotCacheErrors verifies path validation errors are not cached
func TestGetValidatePathDoesNotCacheErrors(t *testing.T) {
	pc := NewPathCache()
	callCount := 0
	transientErr := errors.New("transient error")

	compute := func(path string) (string, error) {
		callCount++
		if callCount == 1 {
			return "", transientErr
		}
		return "valid/path", nil
	}

	// First call - returns error
	_, err := pc.GetValidatePath("test/path", compute)
	require.Error(t, err, "expected error on first call")

	// Second call - should retry
	result, err := pc.GetValidatePath("test/path", compute)
	require.NoError(t, err, "expected success on second call")
	assert.Equal(t, "valid/path", result)
	assert.Equal(t, 2, callCount, "expected compute to be called twice")
}

// TestGetWithinBaseDoesNotCacheErrors verifies within-base check errors are not cached
func TestGetWithinBaseDoesNotCacheErrors(t *testing.T) {
	pc := NewPathCache()
	callCount := 0
	transientErr := errors.New("transient error")

	compute := func() (bool, error) {
		callCount++
		if callCount == 1 {
			return false, transientErr
		}
		return true, nil
	}

	// First call - returns error
	_, err := pc.GetWithinBase("test/key", compute)
	require.Error(t, err, "expected error on first call")

	// Second call - should retry
	result, err := pc.GetWithinBase("test/key", compute)
	require.NoError(t, err, "expected success on second call")
	assert.True(t, result)
	assert.Equal(t, 2, callCount, "expected compute to be called twice")
}

// TestSuccessResultsCached verifies that successful results ARE cached
func TestSuccessResultsCached(t *testing.T) {
	t.Run("GetSymlinkResolution_caches_success", func(t *testing.T) {
		pc := NewPathCache()
		callCount := 0

		compute := func(path string) (string, error) {
			callCount++
			return testResolvedPath, nil
		}

		// First call
		resolved1, err := pc.GetSymlinkResolution("test/path", compute)
		require.NoError(t, err)

		// Second call - should return cached result
		resolved2, err := pc.GetSymlinkResolution("test/path", compute)
		require.NoError(t, err)

		assert.Equal(t, resolved1, resolved2, "results should match")
		assert.Equal(t, 1, callCount, "expected compute to be called once (cached)")
	})
}

// TestCacheEntryExpiration verifies that cached entries expire
func TestCacheEntryExpiration(t *testing.T) {
	t.Run("symlink_cache_expires", func(t *testing.T) {
		pc := NewPathCache()
		// Set very short TTL for testing
		pc.symlinkTTL = 10 * time.Millisecond

		callCount := 0
		compute := func(path string) (string, error) {
			callCount++
			return testResolvedPath, nil
		}

		// First call
		_, _ = pc.GetSymlinkResolution("test/path", compute)
		assert.Equal(t, 1, callCount, "expected 1 call")

		// Second call - should be cached
		_, _ = pc.GetSymlinkResolution("test/path", compute)
		assert.Equal(t, 1, callCount, "expected 1 call (cached)")

		// Wait for expiration
		time.Sleep(20 * time.Millisecond)

		// Third call - cache should be expired
		_, _ = pc.GetSymlinkResolution("test/path", compute)
		assert.Equal(t, 2, callCount, "expected 2 calls (expired)")
	})
}

// mockFileInfo implements fs.FileInfo for testing
type mockFileInfo struct {
	name    string
	size    int64
	mode    fs.FileMode
	modTime time.Time
	isDir   bool
}

func (m *mockFileInfo) Name() string       { return m.name }
func (m *mockFileInfo) Size() int64        { return m.size }
func (m *mockFileInfo) Mode() fs.FileMode  { return m.mode }
func (m *mockFileInfo) ModTime() time.Time { return m.modTime }
func (m *mockFileInfo) IsDir() bool        { return m.isDir }
func (m *mockFileInfo) Sys() any           { return nil }
