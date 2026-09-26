package testutil

import (
	"testing"

	"go.uber.org/goleak"
)

// VerifyNoLeaks registers a per-test goroutine-leak check that runs when the
// test ends.
//
// It snapshots the goroutines alive at the moment it is called (via
// goleak.IgnoreCurrent) and ignores them, so a leftover goroutine from a
// previously-run test (for example a net/http dialConn goroutine left by an
// earlier test) is not wrongly attributed to this test. goleak inspects every
// goroutine in the process, not just the ones this test spawned, so without the
// snapshot these checks flake under `go test -race -shuffle=on` depending on
// which test ran first.
//
// Because the snapshot is taken when VerifyNoLeaks is called, callers must
// call it directly (NOT with defer) as the first statement of the test, before
// constructing the component under test or doing any work that starts
// goroutines. The VerifyNone itself is scheduled via t.Cleanup, and because it
// is registered first it runs last, after every cleanup the test registers
// later (such as stopping the service under test). Goroutines spawned after
// the snapshot are not in it, so a real leak (a goroutine started during the
// test and never stopped) still fails the check.
//
// extra options are appended after the snapshot, for per-test ignores of
// named goroutines that cannot be stopped, such as the go-cache janitor.
//
// This helper is for per-test checks. Do not pass IgnoreCurrent to a
// package-wide goleak.VerifyTestMain gate: the option is evaluated before
// m.Run, so it would only hide goroutines started by TestMain's own setup
// (masking leaks there) and does nothing for the tests themselves.
//
// Do not combine VerifyNoLeaks with t.Parallel(): a process-wide leak check
// cannot coexist with goroutines from other tests running concurrently.
func VerifyNoLeaks(t *testing.T, extra ...goleak.Option) {
	t.Helper()
	opts := make([]goleak.Option, 0, len(extra)+1)
	opts = append(opts, goleak.IgnoreCurrent())
	opts = append(opts, extra...)
	t.Cleanup(func() {
		goleak.VerifyNone(t, opts...)
	})
}
