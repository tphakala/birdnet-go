package system

import (
	"fmt"
	"os"
	"testing"
	"time"

	"go.uber.org/goleak"
)

// testCleanupGracePeriod gives any background goroutines started by the system
// domain (the CPU sampler, the metrics collector, terminal PTY readers) a brief
// window to exit after their owning context is cancelled before the package-wide
// leak gate runs.
const testCleanupGracePeriod = 100 * time.Millisecond

// TestMain runs a package-wide goroutine-leak gate after all tests complete so
// the system domain gets its own isolated -race test binary, matching the
// package-api harness this domain was extracted from. Like package api's
// TestMain, the gate runs with no ignores (goleak already filters the test
// runner's own goroutines).
func TestMain(m *testing.M) {
	testResult := m.Run()

	// Give a small grace period for goroutines to clean up after all tests.
	time.Sleep(testCleanupGracePeriod)

	if testResult == 0 {
		if err := goleak.Find(); err != nil {
			fmt.Fprintf(os.Stderr, "FAIL: Goroutine leak detected after all tests:\n%v\n", err)
			os.Exit(1)
		}
	}

	os.Exit(testResult)
}
