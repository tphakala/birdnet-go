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
// package-api harness this domain was extracted from. The ignore list mirrors
// package api's TestMain: test-framework goroutines plus the process-lifetime
// third-party workers (the go-cache janitor started by the core's DetectionCache
// and the lumberjack log-rotation worker) that cannot be stopped.
func TestMain(m *testing.M) {
	testResult := m.Run()

	// Give a small grace period for goroutines to clean up after all tests.
	time.Sleep(testCleanupGracePeriod)

	if testResult == 0 {
		opts := []goleak.Option{
			goleak.IgnoreTopFunction("testing.(*T).Run"),
			goleak.IgnoreTopFunction("testing.(*T).Parallel"),
			goleak.IgnoreTopFunction("github.com/patrickmn/go-cache.(*janitor).Run"),
			goleak.IgnoreTopFunction("gopkg.in/natefinch/lumberjack%2ev2.(*Logger).millRun"),
		}

		if err := goleak.Find(opts...); err != nil {
			fmt.Fprintf(os.Stderr, "FAIL: Goroutine leak detected after all tests:\n%v\n", err)
			os.Exit(1)
		}
	}

	os.Exit(testResult)
}
