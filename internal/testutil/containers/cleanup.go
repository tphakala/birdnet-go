//go:build integration

package containers

import (
	"fmt"
	"sync"
	"testing"
)

// CleanupManager manages cleanup of test resources in proper order.
// Resources are cleaned up in LIFO order (last added, first cleaned).
type CleanupManager struct {
	mu       sync.Mutex
	cleanups []cleanupFunc
}

type cleanupFunc struct {
	name string
	fn   func() error
}

// NewCleanupManager creates a new CleanupManager.
func NewCleanupManager() *CleanupManager {
	return &CleanupManager{
		cleanups: make([]cleanupFunc, 0),
	}
}

// Add adds a cleanup function to be executed later.
// Functions are executed in LIFO order (last added, first executed).
func (cm *CleanupManager) Add(name string, fn func() error) {
	cm.mu.Lock()
	defer cm.mu.Unlock()

	cm.cleanups = append(cm.cleanups, cleanupFunc{
		name: name,
		fn:   fn,
	})
}

// Cleanup executes all registered cleanup functions in LIFO order.
// It continues executing even if some cleanups fail, collecting all errors.
// The cleanup functions are executed without holding the lock to avoid deadlock
// if a cleanup function attempts to call Add().
func (cm *CleanupManager) Cleanup() []error {
	cm.mu.Lock()
	// Copy the cleanups slice and clear it while holding the lock
	cleanupsCopy := make([]cleanupFunc, len(cm.cleanups))
	copy(cleanupsCopy, cm.cleanups)
	cm.cleanups = nil
	cm.mu.Unlock()

	var errors []error

	// Execute cleanups in reverse order without holding the lock
	for i := len(cleanupsCopy) - 1; i >= 0; i-- {
		cleanup := cleanupsCopy[i]
		if err := cleanup.fn(); err != nil {
			errors = append(errors, fmt.Errorf("%s cleanup failed: %w", cleanup.name, err))
		}
	}

	return errors
}

// RegisterTestCleanup registers cleanup functions with testing.T using t.Cleanup.
// This ensures cleanup happens even if tests panic.
func (cm *CleanupManager) RegisterTestCleanup(t *testing.T) {
	t.Helper()

	t.Cleanup(func() {
		errors := cm.Cleanup()
		for _, err := range errors {
			t.Errorf("Cleanup error: %v", err)
		}
	})
}

// CleanupOnce ensures a cleanup function is only executed once.
// This is useful for cleanup functions that might be called multiple times
// (e.g., in both t.Cleanup and defer statements).
type CleanupOnce struct {
	once sync.Once
	fn   func() error
	err  error
}

// NewCleanupOnce creates a new CleanupOnce wrapper.
func NewCleanupOnce(fn func() error) *CleanupOnce {
	return &CleanupOnce{fn: fn}
}

// Do executes the cleanup function exactly once, even if called multiple times.
// Subsequent calls return the error from the first execution.
func (co *CleanupOnce) Do() error {
	co.once.Do(func() {
		co.err = co.fn()
	})
	return co.err
}
