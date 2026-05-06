package api

import (
	"fmt"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
	"github.com/tphakala/birdnet-go/internal/conf"
)

// TestDebugSkipsControllerFallbackWhenGlobalUnset verifies Debug() remains a
// no-op when conf.GetSettings() is nil, even while c.Settings is being updated
// concurrently. Run with -race: if Debug() starts reading c.Settings fallback
// again, the race detector should report a data race.
func TestDebugSkipsControllerFallbackWhenGlobalUnset(t *testing.T) {
	previous := conf.GetSettings()
	conf.SetTestSettings(nil)
	t.Cleanup(func() {
		conf.SetTestSettings(previous)
	})

	controller := &Controller{
		Settings: newValidTestSettings(),
	}

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
				controller.Settings = updated
			}
		}
	}()

	for i := range 5000 {
		controller.Debug("concurrent debug call %d", i)
	}

	close(stopWriter)

	select {
	case <-writerDone:
	case <-time.After(2 * time.Second):
		require.FailNow(t, "Writer goroutine did not stop", fmt.Sprintf("timed out waiting for writer after %s", 2*time.Second))
	}
}
