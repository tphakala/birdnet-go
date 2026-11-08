package backup

import (
	"path/filepath"
	"testing"

	"github.com/tphakala/birdnet-go/internal/testutil"
)

// stateFileName is the state file name used by the StateManager tests.
const stateFileName = "backup-state.json"

// newTestStateManager returns a manager backed by a file in a temp directory
// together with that file's path.
func newTestStateManager(t *testing.T) (sm *StateManager, path string) {
	t.Helper()
	path = filepath.Join(t.TempDir(), stateFileName)
	return newStateManagerAt(path), path
}

// requireReturns runs fn and fails the test if it has not returned by the
// long test timeout, so a deadlock fails the test instead of hanging the run.
// fn must not make assertions: it runs on another goroutine.
func requireReturns(t *testing.T, fn func(), msg string) {
	t.Helper()
	done := make(chan struct{})
	go func() {
		defer close(done)
		fn()
	}()
	testutil.WaitForChannel(t, done, testutil.LongTestTimeout, msg)
}
