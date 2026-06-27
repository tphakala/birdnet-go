package sse

import (
	"fmt"
	"os"
	"testing"
	"time"

	"github.com/tphakala/birdnet-go/internal/api/v2/apitest"
	"go.uber.org/goleak"
)

// testCleanupGracePeriod gives per-request SSE event-loop goroutines a brief
// window to exit after their client disconnects before the package-wide leak
// gate runs, so one test's still-terminating goroutine is not attributed to a
// leak. It mirrors the package-api harness this domain was extracted from.
const testCleanupGracePeriod = 100 * time.Millisecond

// TestMain disables HTTP keep-alives before any test runs (so the SSE
// connection-lifecycle tests get immediate client teardown) and then runs a
// package-wide goroutine-leak gate after all tests complete. The SSE stream
// endpoints spawn a long-lived event-loop goroutine per HTTP request; the gate
// verifies those loops exit when the client disconnects, guarding against the
// memory-leak regressions the connection-cleanup tests were written for. The
// ignore list mirrors package api's TestMain.
func TestMain(m *testing.M) {
	apitest.DisableHTTPKeepAlivesForTesting()

	testResult := m.Run()

	// Give a small grace period for goroutines to clean up after all tests, so
	// the gate does not observe a goroutine that is still terminating.
	time.Sleep(testCleanupGracePeriod)

	if testResult == 0 {
		opts := []goleak.Option{
			// Test-framework goroutines (not leaks).
			goleak.IgnoreTopFunction("testing.(*T).Run"),
			goleak.IgnoreTopFunction("testing.(*T).Parallel"),
			// Process-lifetime third-party workers that cannot be stopped: the
			// go-cache janitor (started by the core's DetectionCache) and the
			// lumberjack log-rotation worker.
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
