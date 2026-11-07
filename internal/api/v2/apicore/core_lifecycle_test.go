package apicore_test

import (
	"context"
	"sync"
	"sync/atomic"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/tphakala/birdnet-go/internal/api/v2/apicore"
)

func newLifecycleCore(t *testing.T) *apicore.Core {
	t.Helper()
	ctx, cancel := context.WithCancel(t.Context())
	c := &apicore.Core{}
	c.SetTestContext(ctx, cancel)
	return c
}

func TestGoUnlessStopped_RunsBeforeCancelOnly(t *testing.T) {
	t.Parallel()
	c := newLifecycleCore(t)

	var ran atomic.Bool
	require.True(t, c.GoUnlessStopped(func() { ran.Store(true) }))
	c.Wait()
	assert.True(t, ran.Load(), "a goroutine started before Cancel runs and Wait waits for it")

	c.Cancel()
	require.False(t, c.GoUnlessStopped(func() { t.Error("a goroutine started after Cancel") }))
	c.Wait()
}

// A Core without a context or a cancel function (SetTestContext permits both)
// still stops admitting goroutines once Cancel is called.
func TestGoUnlessStopped_WithoutContextStopsOnCancel(t *testing.T) {
	t.Parallel()
	c := &apicore.Core{}
	//nolint:staticcheck // SA1012: a nil context is the case under test
	c.SetTestContext(nil, nil)

	require.True(t, c.GoUnlessStopped(func() {}))
	c.Cancel()
	require.False(t, c.GoUnlessStopped(func() { t.Error("a goroutine started after Cancel") }))
	c.Wait()
}

// Every goroutine admitted while Shutdown (Cancel then Wait) runs concurrently
// must have finished by the time Wait returns: none is admitted after Cancel.
func TestGoUnlessStopped_NoGoroutineOutlivesShutdown(t *testing.T) {
	t.Parallel()
	const callers = 64
	for range 20 {
		c := newLifecycleCore(t)
		var admitted, finished atomic.Int64
		var callersDone sync.WaitGroup
		start := make(chan struct{})
		for range callers {
			callersDone.Go(func() {
				<-start
				if c.GoUnlessStopped(func() { finished.Add(1) }) {
					admitted.Add(1)
				}
			})
		}

		close(start)
		c.Cancel()
		c.Wait()
		finishedAtShutdown := finished.Load()
		callersDone.Wait()
		assert.Equal(t, admitted.Load(), finishedAtShutdown,
			"a goroutine admitted during Shutdown finished after Wait returned")
	}
}
