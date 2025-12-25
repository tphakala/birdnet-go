// test_helpers_test.go - Shared test helpers for jobqueue package
// These helpers reduce duplication across test files and ensure consistent test setup.
package jobqueue

import (
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

// --- Channel Wait Helpers ---

// waitForChannel waits for a signal on the channel or fails after timeout.
// Use this for waiting on done channels, job completion signals, etc.
func waitForChannel(t *testing.T, ch <-chan struct{}, timeout time.Duration, msg string) {
	t.Helper()
	select {
	case <-ch:
		// Success
	case <-time.After(timeout):
		require.Fail(t, msg)
	}
}

// waitForChannelWithLog waits for a signal and logs on success.
func waitForChannelWithLog(t *testing.T, ch <-chan struct{}, timeout time.Duration, failMsg, successMsg string) {
	t.Helper()
	select {
	case <-ch:
		t.Log(successMsg)
	case <-time.After(timeout):
		require.Fail(t, failMsg)
	}
}

// --- Common Test Constants ---

const (
	// DefaultTestTimeout is the standard timeout for most async test operations.
	DefaultTestTimeout = 5 * time.Second

	// ShortTestTimeout is for operations expected to complete quickly.
	ShortTestTimeout = 1 * time.Second

	// LongTestTimeout is for operations that may take longer (CI environments).
	LongTestTimeout = 30 * time.Second
)
