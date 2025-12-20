//go:build !maintest

package api

import (
	"fmt"
	"os"
	"testing"
	"time"

	"github.com/tphakala/birdnet-go/internal/conf"
	"go.uber.org/goleak"
)

// Test setup constants
const (
	testCleanupGracePeriod = 100 * time.Millisecond // Grace period for goroutines to clean up after tests
)

// TestMain sets up the test environment with test settings
func TestMain(m *testing.M) {
	// Initialize test settings and run tests
	code := func() int {
		// Handle any panic during test setup
		defer func() {
			if r := recover(); r != nil {
				// Log the panic and exit with failure
				fmt.Fprintf(os.Stderr, "Failed to initialize test settings: %v\n", r)
				os.Exit(1)
			}
		}()
		
		// Disable HTTP keep-alives for all tests to prevent goroutine leaks
		// This must be done before any test creates an HTTP client
		DisableHTTPKeepAlivesForTesting()
		
		// Inject test settings before any test runs
		// Create a dummy *testing.T for initialization purposes
		// This is safe since we only use t.Helper() which doesn't require active test
		testT := &testing.T{}
		testSettings := getTestSettings(testT)
		if testSettings == nil {
			panic("getTestSettings() returned nil")
		}
		conf.SetTestSettings(testSettings)
		
		// Run tests
		testResult := m.Run()
		
		// Give a small grace period for goroutines to clean up after all tests
		// This is the ONLY place we use time.Sleep, and it's after ALL tests complete
		time.Sleep(testCleanupGracePeriod)
		
		// Check for goroutine leaks after ALL tests have completed
		// This avoids the issue of one test detecting another test's goroutines
		if testResult == 0 {
			opts := []goleak.Option{
				// Ignore standard library and testing goroutines
				goleak.IgnoreTopFunction("testing.(*T).Run"),
				goleak.IgnoreTopFunction("testing.(*T).Parallel"),
				goleak.IgnoreTopFunction("runtime.gopark"),
				goleak.IgnoreTopFunction("sync.runtime_notifyListWait"),
				// Ignore go-cache janitor
				goleak.IgnoreTopFunction("github.com/patrickmn/go-cache.(*janitor).Run"),
				// Ignore lumberjack logger
				goleak.IgnoreTopFunction("gopkg.in/natefinch/lumberjack%2ev2.(*Logger).millRun"),
				// Ignore audio streaming HLS initialization
				goleak.IgnoreTopFunction("github.com/tphakala/birdnet-go/internal/httpcontroller/handlers.init.0.func1"),
			}
			
			if err := goleak.Find(opts...); err != nil {
				// Report the leak as a test failure
				fmt.Fprintf(os.Stderr, "FAIL: Goroutine leak detected after all tests:\n%v\n", err)
				return 1 // Fail the test suite
			}
		}
		
		return testResult
	}()
	
	// Exit with the test result code
	os.Exit(code)
}