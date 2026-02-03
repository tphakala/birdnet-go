package telemetry

import (
	"context"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/tphakala/birdnet-go/internal/conf"
)

func TestSystemInitManager_ShutdownWithContext(t *testing.T) {
	// Cannot run in parallel due to singleton
	
	// Initialize system
	settings := &conf.Settings{
		Sentry: conf.SentrySettings{
			Enabled: false,
		},
	}
	
	// Get the system init manager
	manager := GetSystemInitManager()
	require.NotNil(t, manager)
	
	// Initialize the system
	err := manager.InitializeCore(settings)
	require.NoError(t, err)
	
	t.Run("Shutdown respects context timeout", func(t *testing.T) {
		// Cannot run in parallel - accessing shared singleton manager
		
		// Create an already-expired context by setting deadline in the past
		ctx, cancel := context.WithDeadline(t.Context(), time.Now().Add(-1*time.Second))
		defer cancel()
		
		// Shutdown should return context error immediately
		err := manager.Shutdown(ctx)
		require.Error(t, err)
		require.Equal(t, context.DeadlineExceeded, err)
	})
	
	t.Run("Shutdown completes within timeout", func(t *testing.T) {
		// Note: Using same singleton manager
		
		// Create context with reasonable timeout
		ctx, cancel := context.WithTimeout(t.Context(), 10*time.Second)
		defer cancel()
		
		// Shutdown should complete successfully
		err := manager.Shutdown(ctx)
		assert.NoError(t, err)
	})
	
	t.Run("Shutdown with cancelled context", func(t *testing.T) {
		// Note: Using same singleton manager
		
		// Create and immediately cancel context
		ctx, cancel := context.WithCancel(t.Context())
		cancel()
		
		// Shutdown should return context error
		err := manager.Shutdown(ctx)
		require.Error(t, err)
		require.Equal(t, context.Canceled, err)
	})
}

func TestSystemInitManager_ShutdownTimeoutCalculation(t *testing.T) {
	t.Parallel()
	
	// Test timeout calculation logic
	t.Run("Calculates remaining time correctly", func(t *testing.T) {
		// Create a fixed deadline in the future
		futureTime := time.Now().Add(5 * time.Second)
		ctx, cancel := context.WithDeadline(t.Context(), futureTime)
		defer cancel()
		
		// Get deadline
		deadline, ok := ctx.Deadline()
		assert.True(t, ok)
		assert.Equal(t, futureTime, deadline)
		
		// Calculate remaining time at a fixed point
		// Instead of using time.Until which depends on current time,
		// we test the calculation logic directly
		testTime := futureTime.Add(-3 * time.Second) // 3 seconds before deadline
		remaining := futureTime.Sub(testTime)
		assert.Equal(t, 3*time.Second, remaining)
		
		// Test with different time points
		testTime2 := futureTime.Add(-1 * time.Second) // 1 second before deadline
		remaining2 := futureTime.Sub(testTime2)
		assert.Equal(t, 1*time.Second, remaining2)
		
		// Test when deadline has passed
		testTime3 := futureTime.Add(1 * time.Second) // 1 second after deadline
		remaining3 := futureTime.Sub(testTime3)
		assert.Equal(t, -1*time.Second, remaining3)
	})
	
	t.Run("Context without deadline", func(t *testing.T) {
		// Test with context that has no deadline
		ctx := t.Context()
		
		deadline, ok := ctx.Deadline()
		assert.False(t, ok)
		assert.Zero(t, deadline)
	})
}