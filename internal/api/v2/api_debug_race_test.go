package api

import (
	"testing"
	"time"

	"github.com/stretchr/testify/require"
	"github.com/tphakala/birdnet-go/internal/conf"
	"github.com/tphakala/birdnet-go/internal/conf/conftest"
)

// writerStopTimeout bounds how long the test waits for the background writer
// goroutine to stop before failing.
const writerStopTimeout = 2 * time.Second

// TestDebugSkipsControllerFallbackWhenGlobalUnset verifies Debug() remains a
// no-op when conf.GetSettings() is nil, even while c.Settings is being updated
// concurrently. Run with -race: if Debug() starts reading c.Settings fallback
// again, the race detector should report a data race.
func TestDebugSkipsControllerFallbackWhenGlobalUnset(t *testing.T) {
	previous := conf.GetSettings()
	conftest.SetTestSettings(nil)
	t.Cleanup(func() {
		conftest.SetTestSettings(previous)
	})

	controller := &Controller{}
	controller.Settings.Store(newValidTestSettings())

	stopWriter := make(chan struct{})
	writerDone := make(chan struct{})
	go func() {
		defer close(writerDone)
		for {
			select {
			case <-stopWriter:
				return
			default:
				updated := newValidTestSettings()
				updated.WebServer.Debug = true
				controller.Settings.Store(updated)
			}
		}
	}()

	for i := range 5000 {
		controller.Debug("concurrent debug call %d", i)
	}

	close(stopWriter)

	select {
	case <-writerDone:
	case <-time.After(writerStopTimeout):
		require.FailNow(t, "Writer goroutine did not stop", "timed out waiting for writer after %s", writerStopTimeout)
	}
}
