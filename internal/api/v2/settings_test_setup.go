// +build !maintest

package api

import (
	"os"
	"testing"
	
	"github.com/tphakala/birdnet-go/internal/conf"
)

// TestMain sets up the test environment with test settings
func TestMain(m *testing.M) {
	// Inject test settings before any test runs
	conf.SetTestSettings(getTestSettings())
	
	// Run tests
	code := m.Run()
	
	// Exit with the test result code
	os.Exit(code)
}