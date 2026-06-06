package api

import (
	"fmt"
	"os"
	"testing"
	"time"

	"github.com/tphakala/birdnet-go/internal/notification"
	"go.uber.org/goleak"
)

// Test setup constants
const (
	testCleanupGracePeriod = 100 * time.Millisecond // Grace period for goroutines to clean up after tests
)

// TestMain sets up the package-wide test environment.
//
// It disables HTTP keep-alives so persistent connections from HTTP clients
// created during tests don't linger as background goroutines. This runs before
// any test creates a client.
//
// It deliberately does NOT inject a package-global test settings snapshot.
// Tests construct their own *conf.Settings and pass it to the controller under
// test (via getTestSettings / NewWithOptions); request-time reads fall back to
// that per-controller snapshot when the global is unset. Publishing a shared
// global baseline here would make currentSettings() (which prefers the global)
// shadow those per-test settings.
//
// It also runs a package-wide goroutine-leak gate (go.uber.org/goleak) after
// all tests complete. The gate previously lived in a non-_test.go file, so it
// never actually ran; activating it surfaced the notification.Service cleanup
// loops (now stopped per test via Stop()/cleanup) and lingering HTTPS
// connection loops from the BirdWeather connection probe (fixed at the source:
// birdweather.newSecureHTTPClient now disables keep-alives so the one-shot
// probe connection closes instead of parking read/write loops). The ignore
// list below is kept minimal so the gate catches real leaks.
func TestMain(m *testing.M) {
	// Disable HTTP keep-alives for all tests to prevent goroutine leaks from
	// persistent connections. Must run before any test creates an HTTP client.
	DisableHTTPKeepAlivesForTesting()

	// Run tests
	testResult := m.Run()

	// Stop the process-global notification service if any test brought it up.
	// Tests initialize it via notification.Initialize()/SetServiceForTesting()
	// (singleton, guarded by sync.Once), so it cannot be reset and stopped from
	// an individual test's cleanup; its cleanupLoop goroutine would otherwise
	// outlive the suite and trip the leak gate below. m.Run() has returned, so
	// no test is using the service at this point.
	if svc := notification.GetService(); svc != nil {
		svc.Stop()
	}

	// Give a small grace period for goroutines to clean up after all tests.
	// This is after ALL tests complete, so it avoids one test observing
	// another test's still-terminating goroutines.
	time.Sleep(testCleanupGracePeriod)

	// Check for goroutine leaks after ALL tests have completed.
	// This avoids the issue of one test detecting another test's goroutines.
	if testResult == 0 {
		opts := []goleak.Option{
			// Ignore the test-framework goroutines (these are not leaks).
			goleak.IgnoreTopFunction("testing.(*T).Run"),
			goleak.IgnoreTopFunction("testing.(*T).Parallel"),
			// Ignore background goroutines from third-party dependencies that run
			// for the lifetime of the process and are not leaks we can stop:
			// the go-cache janitor and the lumberjack log-rotation worker.
			goleak.IgnoreTopFunction("github.com/patrickmn/go-cache.(*janitor).Run"),
			goleak.IgnoreTopFunction("gopkg.in/natefinch/lumberjack%2ev2.(*Logger).millRun"),
		}

		if err := goleak.Find(opts...); err != nil {
			// Report the leak as a test failure
			fmt.Fprintf(os.Stderr, "FAIL: Goroutine leak detected after all tests:\n%v\n", err)
			os.Exit(1)
		}
	}

	os.Exit(testResult)
}
