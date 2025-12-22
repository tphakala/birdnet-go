package securefs

import (
	"errors"
	"io/fs"
	"testing"
	"time"
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
	if err == nil {
		t.Fatal("expected error on first call")
	}

	// Second call - should retry (not return cached error)
	resolved, err := pc.GetSymlinkResolution("test/path", compute)
	if err != nil {
		t.Errorf("expected success on second call, got error: %v", err)
	}
	if resolved != testResolvedPath {
		t.Errorf("expected '%s', got '%s'", testResolvedPath, resolved)
	}
	if callCount != 2 {
		t.Errorf("expected compute to be called twice, was called %d times", callCount)
	}
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
	if err == nil {
		t.Fatal("expected error on first call")
	}

	// Second call - should retry (not return cached error)
	info, err := pc.GetStat("test/path", compute)
	if err != nil {
		t.Errorf("expected success on second call, got error: %v", err)
	}
	if info == nil || info.Name() != "test.txt" {
		t.Errorf("expected valid FileInfo with name 'test.txt'")
	}
	if callCount != 2 {
		t.Errorf("expected compute to be called twice, was called %d times", callCount)
	}
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
	if err == nil {
		t.Fatal("expected error on first call")
	}

	// Second call - should retry
	result, err := pc.GetAbsPath("test/path", compute)
	if err != nil {
		t.Errorf("expected success on second call, got error: %v", err)
	}
	if result != "/absolute/path" {
		t.Errorf("expected '/absolute/path', got '%s'", result)
	}
	if callCount != 2 {
		t.Errorf("expected compute to be called twice, was called %d times", callCount)
	}
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
	if err == nil {
		t.Fatal("expected error on first call")
	}

	// Second call - should retry
	result, err := pc.GetValidatePath("test/path", compute)
	if err != nil {
		t.Errorf("expected success on second call, got error: %v", err)
	}
	if result != "valid/path" {
		t.Errorf("expected 'valid/path', got '%s'", result)
	}
	if callCount != 2 {
		t.Errorf("expected compute to be called twice, was called %d times", callCount)
	}
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
	if err == nil {
		t.Fatal("expected error on first call")
	}

	// Second call - should retry
	result, err := pc.GetWithinBase("test/key", compute)
	if err != nil {
		t.Errorf("expected success on second call, got error: %v", err)
	}
	if !result {
		t.Error("expected true, got false")
	}
	if callCount != 2 {
		t.Errorf("expected compute to be called twice, was called %d times", callCount)
	}
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
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}

		// Second call - should return cached result
		resolved2, err := pc.GetSymlinkResolution("test/path", compute)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}

		if resolved1 != resolved2 {
			t.Errorf("results don't match: %s vs %s", resolved1, resolved2)
		}
		if callCount != 1 {
			t.Errorf("expected compute to be called once (cached), was called %d times", callCount)
		}
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
		if callCount != 1 {
			t.Errorf("expected 1 call, got %d", callCount)
		}

		// Second call - should be cached
		_, _ = pc.GetSymlinkResolution("test/path", compute)
		if callCount != 1 {
			t.Errorf("expected 1 call (cached), got %d", callCount)
		}

		// Wait for expiration
		time.Sleep(20 * time.Millisecond)

		// Third call - cache should be expired
		_, _ = pc.GetSymlinkResolution("test/path", compute)
		if callCount != 2 {
			t.Errorf("expected 2 calls (expired), got %d", callCount)
		}
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
