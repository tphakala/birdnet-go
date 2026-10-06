package detections

import (
	"fmt"
	"os"
	"testing"
	"time"

	"go.uber.org/goleak"
)

// testCleanupGracePeriod gives goroutines that are still terminating after the
// last test a brief window to exit before the package-wide leak gate runs.
const testCleanupGracePeriod = 100 * time.Millisecond

// TestMain runs a package-wide goroutine-leak gate after all tests complete. The
// detection cache owns no goroutine, so the gate runs with no ignores (goleak
// already filters the test runner's own goroutines).
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
