package app

import (
	"context"
	"fmt"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestLegacyService_StartStop(t *testing.T) {
	t.Parallel()

	started := make(chan struct{})
	blockingFn := func(quit <-chan struct{}) error {
		close(started)
		<-quit // block until signaled
		return nil
	}

	svc := NewLegacyService("test-legacy", blockingFn)
	assert.Equal(t, "test-legacy", svc.Name())

	require.NoError(t, svc.Start(t.Context()))

	// Wait for the function to actually start
	select {
	case <-started:
	case <-time.After(time.Second):
		t.Fatal("legacy function did not start")
	}

	ctx, cancel := context.WithTimeout(t.Context(), 2*time.Second)
	defer cancel()
	require.NoError(t, svc.Stop(ctx))
}

func TestLegacyService_PropagatesError(t *testing.T) {
	t.Parallel()

	blockingFn := func(quit <-chan struct{}) error {
		<-quit
		return assert.AnError
	}

	svc := NewLegacyService("err-legacy", blockingFn)
	require.NoError(t, svc.Start(t.Context()))

	ctx, cancel := context.WithTimeout(t.Context(), 2*time.Second)
	defer cancel()
	err := svc.Stop(ctx)
	require.ErrorIs(t, err, assert.AnError)
}

func TestLegacyService_ErrChan_ReportsEarlyExit(t *testing.T) {
	t.Parallel()

	blockingFn := func(quit <-chan struct{}) error {
		// Simulate init failure — return immediately without waiting for quit
		return fmt.Errorf("database open failed")
	}

	svc := NewLegacyService("init-fail", blockingFn)
	require.NoError(t, svc.Start(t.Context()))

	// ErrChan should receive the error without needing to call Stop
	select {
	case err := <-svc.ErrChan():
		require.ErrorContains(t, err, "database open failed")
	case <-time.After(time.Second):
		t.Fatal("ErrChan did not receive error from early exit")
	}
}

func TestLegacyService_ConcurrentStartStop(t *testing.T) {
	t.Parallel()

	// This test verifies that calling Stop() concurrently with Start()
	// does not panic due to nil channel access. Before the fix, there was
	// a race window between started.Swap(true) and channel initialization
	// where Stop() could attempt to close a nil quit channel.
	ctx := t.Context()
	for range 100 {
		svc := NewLegacyService("race-test", func(quit <-chan struct{}) error {
			<-quit
			return nil
		})

		go func() {
			_ = svc.Start(ctx)
		}()
		go func() {
			_ = svc.Stop(ctx)
		}()
	}
}

func TestLegacyService_StopTimeout(t *testing.T) {
	t.Parallel()

	blockingFn := func(quit <-chan struct{}) error {
		<-quit
		time.Sleep(5 * time.Second) // simulate slow cleanup
		return nil
	}

	svc := NewLegacyService("slow-legacy", blockingFn)
	require.NoError(t, svc.Start(t.Context()))

	ctx, cancel := context.WithTimeout(t.Context(), 100*time.Millisecond)
	defer cancel()
	err := svc.Stop(ctx)
	require.Error(t, err) // should timeout
}
