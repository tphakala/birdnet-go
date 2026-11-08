package sse

import (
	"testing"

	"github.com/tphakala/birdnet-go/internal/api/v2/apitest"
	"go.uber.org/goleak"
)

// TestMain disables HTTP keep-alives before any test runs (so the SSE
// connection-lifecycle tests get immediate client teardown) and then runs a
// package-wide goroutine-leak gate after all tests complete. The SSE stream
// endpoints spawn a long-lived event-loop goroutine per HTTP request; the gate
// verifies those loops exit when the client disconnects, guarding against the
// memory-leak regressions the connection-cleanup tests were written for. The
// gate runs with no ignores. goleak.VerifyTestMain retries until straggling
// goroutines exit and already filters the test runner's own goroutines.
func TestMain(m *testing.M) {
	apitest.DisableHTTPKeepAlivesForTesting()
	goleak.VerifyTestMain(m)
}
