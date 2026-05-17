// api_goroutine_test.go: Tests for verifying goroutine cleanup in API v2

package api

import (
	"context"
	"testing"

	"github.com/labstack/echo/v4"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/tphakala/birdnet-go/internal/conf"
	"github.com/tphakala/birdnet-go/internal/datastore/mocks"
	"github.com/tphakala/birdnet-go/internal/logger"
	"github.com/tphakala/birdnet-go/internal/observability"
	"go.uber.org/goleak"
)

// TestControllerShutdownCleansUpGoroutines verifies that background goroutines
// are properly cleaned up when the controller is shut down
func TestControllerShutdownCleansUpGoroutines(t *testing.T) {
	// Defer goleak check to verify no goroutines leak after test
	defer goleak.VerifyNone(t,
		// Ignore goroutines from testing framework and other standard libraries
		goleak.IgnoreTopFunction("testing.(*T).Run"),
		goleak.IgnoreTopFunction("runtime.gopark"),
		goleak.IgnoreTopFunction("sync.runtime_notifyListWait"),
		// Ignore the go-cache janitor which we can't control
		goleak.IgnoreTopFunction("github.com/patrickmn/go-cache.(*janitor).Run"),
		// Ignore lumberjack logger goroutines
		goleak.IgnoreTopFunction("gopkg.in/natefinch/lumberjack%2ev2.(*Logger).millRun"),
	)

	// Create Echo instance
	e := echo.New()

	// Create mock datastore
	mockDS := mocks.NewMockInterface(t)

	// Create settings with required paths
	settings := &conf.Settings{
		WebServer: conf.WebServerSettings{
			Debug: true,
		},
		Realtime: conf.RealtimeSettings{
			Audio: conf.AudioSettings{
				Export: conf.ExportSettings{
					Path: t.TempDir(),
				},
			},
		},
	}

	// Create control channel
	controlChan := make(chan string, 10)

	// Create mock metrics
	mockMetrics, _ := observability.NewMetrics()

	// Create controller WITH route initialization to start background goroutines
	controller, err := NewWithOptions(e, mockDS, settings, nil, nil, controlChan, mockMetrics, true)
	require.NoError(t, err)

	// Wait for goroutines to start using the synchronization channel
	if controller.goroutinesStarted != nil {
		<-controller.goroutinesStarted
	}

	// Shutdown the controller
	controller.Shutdown()

	// Close control channel to prevent any lingering goroutines
	close(controlChan)
}

// TestSendReconfigActionsExitsOnShutdown verifies that sendReconfigActions
// stops sending when the controller context is cancelled during shutdown.
func TestSendReconfigActionsExitsOnShutdown(t *testing.T) {
	t.Parallel()

	ctx, cancel := context.WithCancel(t.Context())
	controlChan := make(chan string, 1)
	c := &Controller{
		controlChan: controlChan,
		ctx:         ctx,
		cancel:      cancel,
		apiLogger:   logger.Global().Module("api"),
	}

	actions := []string{"action_one", "action_two", "action_three"}

	// Read the first action, then cancel context before the goroutine can
	// finish sending all three.
	done := make(chan struct{})
	go func() {
		defer close(done)
		c.sendReconfigActions(actions, false)
	}()

	first := <-controlChan
	assert.Equal(t, "action_one", first)
	cancel()
	<-done

	// At most one more action may have been buffered before ctx check.
	remaining := len(controlChan)
	assert.LessOrEqual(t, remaining, 1, "should stop sending after context cancellation")
}

// TestSendReconfigActionsRecoverOnClosedChannel verifies that the recover
// guard catches a send-on-closed-channel panic (the shutdown TOCTOU race).
func TestSendReconfigActionsRecoverOnClosedChannel(t *testing.T) {
	t.Parallel()

	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()

	controlChan := make(chan string, 1)
	c := &Controller{
		controlChan: controlChan,
		ctx:         ctx,
		cancel:      cancel,
		apiLogger:   logger.Global().Module("api"),
	}

	close(controlChan)

	// Must not panic.
	assert.NotPanics(t, func() {
		c.sendReconfigActions([]string{"boom"}, false)
	})
}

// TestGoroutineCleanupWithoutRoutes verifies that creating a controller without
// routes doesn't start unnecessary goroutines
func TestGoroutineCleanupWithoutRoutes(t *testing.T) {
	// Register cleanup with goleak at the beginning
	t.Cleanup(func() {
		goleak.VerifyNone(t,
			// Ignore goroutines from testing framework and other standard libraries
			goleak.IgnoreTopFunction("testing.(*T).Run"),
			goleak.IgnoreTopFunction("runtime.gopark"),
			goleak.IgnoreTopFunction("sync.runtime_notifyListWait"),
			// Ignore the go-cache janitor which we can't control
			goleak.IgnoreTopFunction("github.com/patrickmn/go-cache.(*janitor).Run"),
			// Ignore lumberjack logger goroutines
			goleak.IgnoreTopFunction("gopkg.in/natefinch/lumberjack%2ev2.(*Logger).millRun"),
		)
	})

	// Setup test environment (which uses NewWithOptions with initializeRoutes=false)
	_, _, controller := setupTestEnvironment(t)

	// Since routes are not initialized, goroutinesStarted should be nil
	require.Nil(t, controller.goroutinesStarted, "goroutinesStarted channel should not be created without route initialization")

	// Controller shutdown is handled by setupTestEnvironment's t.Cleanup()
}
