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
// Ignores are kept narrowly scoped to known benign background goroutines: the
// SQLite-backed datastore opens a database/sql connection opener, and the logger
// uses lumberjack for rotation. Both run for the lifetime of the process.
func TestMain(m *testing.M) {
	goleak.VerifyTestMain(m,
		goleak.IgnoreTopFunction("testing.(*T).Run"),
		goleak.IgnoreTopFunction("testing.(*T).Parallel"),
		goleak.IgnoreTopFunction("runtime.gopark"),
		goleak.IgnoreTopFunction("gopkg.in/natefinch/lumberjack%2ev2.(*Logger).millRun"),
		goleak.IgnoreTopFunction("database/sql.(*DB).connectionOpener"),
	)
}
