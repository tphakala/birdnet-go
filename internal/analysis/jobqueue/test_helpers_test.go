// test_helpers_test.go - Shared test helpers for jobqueue package
// These helpers reduce duplication across test files and ensure consistent test setup.
package jobqueue

import (
	"testing"
	"time"

	"github.com/tphakala/birdnet-go/internal/testutil"
)

// Re-export timeout constants from testutil for convenience.
const (
	DefaultTestTimeout = testutil.DefaultTestTimeout
	ShortTestTimeout   = testutil.ShortTestTimeout
	LongTestTimeout    = testutil.LongTestTimeout
)

// waitForChannel waits for a signal on the channel or fails after timeout.
// Use this for waiting on done channels, job completion signals, etc.
func waitForChannel(t *testing.T, ch <-chan struct{}, timeout time.Duration, msg string) {
	t.Helper()
	testutil.WaitForChannel(t, ch, timeout, msg)
}

// waitForChannelWithLog waits for a signal and logs on success.
func waitForChannelWithLog(t *testing.T, ch <-chan struct{}, timeout time.Duration, failMsg, successMsg string) {
	t.Helper()
	testutil.WaitForChannelWithLog(t, ch, timeout, failMsg, successMsg)
}
