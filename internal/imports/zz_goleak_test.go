package imports_test

import (
	"testing"

	"go.uber.org/goleak"
)

// TestMain runs the whole imports_test package under goleak so the engine audio
// tests, which drive a bounded clip-copy worker pool (copyCandidateClips), are
// verified to leave no goroutines behind. A leaked copy goroutine or an
// un-released semaphore would surface here.
//
// No ignores are listed. A test that opens a database must close it (in
// t.Cleanup), which stops its database/sql connectionOpener goroutine before
// this gate runs. goleak
// v1.3.0 already filters the test-runner stacks (testing.(*T).Run /
// testing.(*T).Parallel) via its built-in isTestStack check, and with the
// default GOTRACEBACK setting the runtime hides its own frames, so
// runtime.gopark never appears as the top function goleak matches against (a
// parked goroutine shows its first non-runtime frame, such as
// sync.runtime_notifyListWait) and an ignore for it would be a no-op.
func TestMain(m *testing.M) {
	goleak.VerifyTestMain(m)
}
