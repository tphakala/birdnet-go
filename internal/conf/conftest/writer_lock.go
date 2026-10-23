package conftest

import (
	"runtime"
	"sync"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/tphakala/birdnet-go/internal/conf"
)

// blockedProbeYields is how many times AssertStillBlocked yields the processor
// while watching a goroutine that should be waiting on the settings writer
// lock. A writer that is not waiting finishes within the bound (a clone and a
// store), so this bound sets how long the passing case takes; a lock-skipping
// writer is caught only when it finishes within the bound, and a correct writer
// never fails it. No wall-clock sleep is involved.
const blockedProbeYields = 100_000

// HoldSettingsWriter takes the process-wide settings writer lock through
// conf.UpdateSettings and keeps holding it until the returned release function
// is called, so a test can start another writer while the lock is held and
// check that it waits. It returns once the lock is held.
//
// publish, when not nil, runs at release time inside the held lock with the
// clone that UpdateSettings will publish. That models a gallery install or a
// range filter rebuild whose publish lands in the middle of another writer's
// cycle. release is idempotent and is also registered with t.Cleanup.
func HoldSettingsWriter(tb testing.TB, publish func(updated *conf.Settings)) (release func()) {
	tb.Helper()

	entered := make(chan struct{})
	gate := make(chan struct{})
	finished := make(chan error, 1)

	go func() {
		_, _, err := conf.UpdateSettings(func(_, updated *conf.Settings) error {
			close(entered)
			<-gate
			if publish != nil {
				publish(updated)
			}
			return nil
		})
		finished <- err
	}()
	<-entered

	var once sync.Once
	release = func() {
		once.Do(func() {
			close(gate)
			require.NoError(tb, <-finished, "the writer that held the settings lock must publish")
		})
	}
	tb.Cleanup(release)
	return release
}

// AssertStillBlocked fails the test when done is closed, that is, when the
// operation it belongs to completed although the settings writer lock is held
// by HoldSettingsWriter. what names the operation in the failure message.
func AssertStillBlocked(tb testing.TB, done <-chan struct{}, what string) {
	tb.Helper()
	for range blockedProbeYields {
		select {
		case <-done:
			tb.Fatalf("%s completed while another settings writer held the writer lock; it must wait for it", what)
		default:
			runtime.Gosched()
		}
	}
}
