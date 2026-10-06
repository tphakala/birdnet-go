package detections

import (
	"testing"

	"go.uber.org/goleak"
)

// TestMain runs a package-wide goroutine-leak gate after all tests complete. The
// detection cache owns no goroutine, so the gate runs with no ignores.
// goleak.VerifyTestMain retries until straggling goroutines exit and already
// filters the test runner's own goroutines.
func TestMain(m *testing.M) {
	goleak.VerifyTestMain(m)
}
