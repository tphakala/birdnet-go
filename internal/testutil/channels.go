// Package testutil provides shared test utilities for the BirdNET-Go project.
// These helpers reduce duplication across test files and ensure consistent test patterns.
package testutil

import (
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

// Common test timeout constants.
const (
	// DefaultTestTimeout is the standard timeout for most async test operations.
	DefaultTestTimeout = 5 * time.Second

	// ShortTestTimeout is for operations expected to complete quickly.
	ShortTestTimeout = 1 * time.Second

	// LongTestTimeout is for operations that may take longer (CI environments).
	LongTestTimeout = 30 * time.Second
)

// WaitForChannel waits for a signal on the channel or fails after timeout.
// Use this for waiting on done channels, job completion signals, etc.
func WaitForChannel(t *testing.T, ch <-chan struct{}, timeout time.Duration, msg string) {
	t.Helper()
	select {
	case <-ch:
		// Success
	case <-time.After(timeout):
		require.Fail(t, msg)
	}
}

// WaitForChannelWithLog waits for a signal and logs on success.
func WaitForChannelWithLog(t *testing.T, ch <-chan struct{}, timeout time.Duration, failMsg, successMsg string) {
	t.Helper()
	select {
	case <-ch:
		t.Log(successMsg)
	case <-time.After(timeout):
		require.Fail(t, failMsg)
	}
}
