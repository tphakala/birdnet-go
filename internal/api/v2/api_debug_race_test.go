package api

import (
	"fmt"
	"sync"
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

	stopWriters := make(chan struct{})
	var writers sync.WaitGroup
	for range 4 {
		writers.Go(func() {
			for {
				select {
				case <-stopWriters:
					return
				default:
					updated := newValidTestSettings()
					updated.WebServer.Debug = true
					controller.Settings = updated
				}
			}
		})
	}

	debugDone := make(chan struct{})
	go func() {
		defer close(debugDone)
		for i := range 5000 {
			controller.Debug("concurrent debug call %d", i)
		}
	}()

	select {
	case <-debugDone:
	case <-time.After(2 * time.Second):
		require.FailNow(t, "Debug did not complete", fmt.Sprintf("timed out waiting for debug loop after %s", 2*time.Second))
	}

	close(stopWriters)

	writersDone := make(chan struct{})
	go func() {
		defer close(writersDone)
		writers.Wait()
	}()

	select {
	case <-writersDone:
	case <-time.After(2 * time.Second):
		require.FailNow(t, "Writer goroutines did not stop", fmt.Sprintf("timed out waiting for writers after %s", 2*time.Second))
	}
}
