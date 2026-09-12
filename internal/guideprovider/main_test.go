package guideprovider

import (
	"os"
	"testing"
	"time"

	"go.uber.org/goleak"
)

// testCleanupGracePeriod gives background goroutines a brief window to finish
// unwinding after the last test's cache is closed. Close cancels the cache context
// and waits on its wait group, but a goroutine that has just returned from Wait can
// still be a few instructions from exiting when the gate runs.
const testCleanupGracePeriod = 100 * time.Millisecond

// TestMain runs a package-wide goroutine-leak gate after all tests complete.
//
// This package is exactly the kind internal/CLAUDE.md asks for one: a GuideCache
// owns a refresh ticker, a detached DB pre-load, wait-group-tracked pre-fetch and
// warm goroutines, and singleflight executions that outlive their caller — and the
// suite deliberately exercises spawn-vs-Close races. Without the gate a cache that
// stopped honoring Close would pass CI silently.
//
// The ignore list matches the analogous gates in internal/imageprovider and the
// api/v2 domains.
func TestMain(m *testing.M) {
	testResult := m.Run()

	time.Sleep(testCleanupGracePeriod)

	if err := goleak.Find(
		goleak.IgnoreTopFunction("testing.(*T).Run"),
		goleak.IgnoreTopFunction("runtime.gopark"),
		goleak.IgnoreTopFunction("gopkg.in/natefinch/lumberjack%2ev2.(*Logger).millRun"),
	); err != nil {
		//nolint:forbidigo // a leak report must reach the test output directly
		println("goroutine leak detected after guideprovider tests:", err.Error())
		os.Exit(1)
	}

	os.Exit(testResult)
}
