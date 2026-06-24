// leakcheck_test.go: shared goroutine-leak helper for api/v2 tests.

package api

import (
	"testing"

	"go.uber.org/goleak"
)

// deferNoLeaks registers a goroutine-leak check that runs when the test ends.
//
// It snapshots the goroutines alive at the moment it is called (via
// goleak.IgnoreCurrent) and ignores them, so a leftover goroutine from a
// previously-run test, most often a net/http.(*Transport).dialConn still
// connecting after another test opened an outbound HTTP connection, is not
// wrongly attributed to this test. goleak inspects every goroutine in the
// process, not just the ones this test spawned, so without the snapshot these
// checks flake under `go test -race -shuffle=on` depending on which test ran
// first.
//
// Because the snapshot is taken when deferNoLeaks is called, callers must call
// it at the START of the test, before constructing the component under test or
// doing any work that starts goroutines. Goroutines spawned afterwards are not
// in the snapshot, so a real leak (a goroutine started during the test and
// never stopped) still fails the check.
//
// extra options are appended after the snapshot, for per-test ignores such as
// goleak.IgnoreTopFunction for known process-lifetime workers.
//
// This helper is for per-test checks. The package-wide gate in TestMain
// (settings_test_setup_test.go) deliberately does NOT use IgnoreCurrent because
// it runs once after all tests, when no other test's goroutines are in flight.
//
// These tests run sequentially; do not add t.Parallel() to a test that calls
// deferNoLeaks, as a process-wide leak check cannot coexist with goroutines
// from other tests running concurrently.
func deferNoLeaks(t *testing.T, extra ...goleak.Option) {
	t.Helper()
	opts := make([]goleak.Option, 0, len(extra)+1)
	opts = append(opts, goleak.IgnoreCurrent())
	opts = append(opts, extra...)
	t.Cleanup(func() {
		goleak.VerifyNone(t, opts...)
	})
}
