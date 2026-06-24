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
// Ignores are kept narrowly scoped to the two known benign background goroutines that run
// for the lifetime of the process: the SQLite-backed datastore's database/sql connection
// opener and the logger's lumberjack rotation worker. goleak v1.3.0 already filters the
// test-runner stacks (testing.(*T).Run / testing.(*T).Parallel) via its built-in
// isTestStack check, and a blanket runtime.gopark ignore would suppress real leaks of any
// parked goroutine, so neither is listed here.
func TestMain(m *testing.M) {
	goleak.VerifyTestMain(m,
		goleak.IgnoreTopFunction("gopkg.in/natefinch/lumberjack%2ev2.(*Logger).millRun"),
		goleak.IgnoreTopFunction("database/sql.(*DB).connectionOpener"),
	)
}
