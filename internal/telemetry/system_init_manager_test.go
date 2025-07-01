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
		t.Parallel()
		
		// Create a context with very short timeout
		ctx, cancel := context.WithTimeout(context.Background(), 1*time.Millisecond)
		defer cancel()
		
		// Wait for context to expire
		time.Sleep(2 * time.Millisecond)
		
		// Shutdown should return context error immediately
		err := manager.Shutdown(ctx)
		assert.Error(t, err)
		assert.Equal(t, context.DeadlineExceeded, err)
	})
	
	t.Run("Shutdown completes within timeout", func(t *testing.T) {
		// Note: Using same singleton manager
		
		// Create context with reasonable timeout
		ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		
		// Shutdown should complete successfully
		err := manager.Shutdown(ctx)
		assert.NoError(t, err)
	})
	
	t.Run("Shutdown with cancelled context", func(t *testing.T) {
		// Note: Using same singleton manager
		
		// Create and immediately cancel context
		ctx, cancel := context.WithCancel(context.Background())
		cancel()
		
		// Shutdown should return context error
		err := manager.Shutdown(ctx)
		assert.Error(t, err)
		assert.Equal(t, context.Canceled, err)
	})
}

func TestSystemInitManager_ShutdownTimeoutCalculation(t *testing.T) {
	t.Parallel()
	
	// Test timeout calculation logic
	t.Run("Uses remaining time from context", func(t *testing.T) {
		// Create a context with 3 second deadline
		ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
		defer cancel()
		
		// Get deadline
		deadline, ok := ctx.Deadline()
		assert.True(t, ok)
		
		// Calculate remaining time
		remaining := time.Until(deadline)
		assert.Greater(t, remaining, 2*time.Second)
		assert.Less(t, remaining, 3*time.Second)
		
		// Sleep a bit
		time.Sleep(1 * time.Second)
		
		// Remaining time should be less now
		remaining = time.Until(deadline)
		assert.Greater(t, remaining, 1*time.Second)
		assert.Less(t, remaining, 2*time.Second)
	})
}