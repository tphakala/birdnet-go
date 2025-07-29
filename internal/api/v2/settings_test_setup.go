// +build !maintest

package api

import (
	"fmt"
	"os"
	"testing"
	
	"github.com/tphakala/birdnet-go/internal/conf"
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
		return m.Run()
	}()
	
	// Exit with the test result code
	os.Exit(code)
}