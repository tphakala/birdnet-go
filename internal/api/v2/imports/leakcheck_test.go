// leakcheck_test.go: shared goroutine-leak helper for the import/migration
// domain tests. Copied from the package-api helper of the same name when the
// import domain moved out of package api (the import SSE-stream tests rely on it).

package importsapi

import (
	"testing"

	"go.uber.org/goleak"
)

// verifyNoLeaks registers a goroutine-leak check that runs when the test ends.
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
// Because the snapshot is taken when verifyNoLeaks is called, callers must call
// it directly (NOT with defer) at the START of the test, before constructing
// the component under test or doing any work that starts goroutines. Calling it
// with defer would take the snapshot at the end of the test and silently mask
// every leak it is meant to catch. The VerifyNone itself is scheduled via
// t.Cleanup, so no defer is needed at the callsite. Goroutines spawned after
// the snapshot are not in it, so a real leak (a goroutine started during the
// test and never stopped) still fails the check.
//
// extra options are appended after the snapshot, for per-test ignores such as
// goleak.IgnoreTopFunction for known process-lifetime workers.
//
// These tests run sequentially; do not add t.Parallel() to a test that calls
// verifyNoLeaks, as a process-wide leak check cannot coexist with goroutines
// from other tests running concurrently.
func verifyNoLeaks(t *testing.T, extra ...goleak.Option) {
	t.Helper()
	opts := make([]goleak.Option, 0, len(extra)+1)
	opts = append(opts, goleak.IgnoreCurrent())
	opts = append(opts, extra...)
	t.Cleanup(func() {
		goleak.VerifyNone(t, opts...)
	})
}
