// client_reconnect_loop_test.go: tests for StartReconnectLoop, which arms the
// reconnect machinery after a failed *initial* connection attempt.

package mqtt

import (
	"testing"
	"time"

	paho "github.com/eclipse/paho.mqtt.golang"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/tphakala/birdnet-go/internal/observability"
)

// stubPahoClient reports a fixed connection state. Only IsConnected is
// exercised; the rest of paho.Client exists to satisfy the interface.
type stubPahoClient struct {
	paho.Client
	connected bool
}

func (s *stubPahoClient) IsConnected() bool { return s.connected }

// newReconnectLoopTestClient builds a client whose reconnect delay is long
// enough that the armed timer never fires during the test, so assertions
// observe the armed state rather than racing a real connection attempt.
func newReconnectLoopTestClient(t *testing.T) *client {
	t.Helper()

	metrics, err := observability.NewMetrics()
	require.NoError(t, err)

	config := DefaultConfig()
	config.Broker = testExampleBroker
	config.ClientID = testClientID
	config.ReconnectDelay = time.Hour

	c := &client{
		config:        config,
		metrics:       metrics.MQTT,
		reconnectStop: make(chan struct{}),
	}

	t.Cleanup(func() {
		c.mu.Lock()
		defer c.mu.Unlock()
		if c.reconnectTimer != nil {
			c.reconnectTimer.Stop()
		}
	})

	return c
}

// TestStartReconnectLoopArmsRetryAfterFailedInitialConnect covers the gap that
// left MQTT dead for the whole process run: paho only invokes the
// connection-lost handler for connections that succeeded at least once, so a
// client whose first Connect failed had nothing driving reconnection.
func TestStartReconnectLoopArmsRetryAfterFailedInitialConnect(t *testing.T) {
	t.Parallel()

	c := newReconnectLoopTestClient(t)

	before := time.Now()
	c.StartReconnectLoop()

	c.mu.RLock()
	defer c.mu.RUnlock()

	assert.NotNil(t, c.reconnectTimer, "Should arm the reconnect timer")
	assert.True(t, c.disconnected, "Should mark the client disconnected so publishes are suppressed")
	assert.False(t, c.disconnectedSince.Before(before), "Should record when the outage started")
}

// TestStartReconnectLoopSuppressesPublishes verifies the graceful-degradation
// contract: a client retained after a failed initial connect drops publishes
// quietly (one warning, then silence) instead of returning an error per
// publish, matching the behaviour of a mid-session outage.
func TestStartReconnectLoopSuppressesPublishes(t *testing.T) {
	t.Parallel()

	c := newReconnectLoopTestClient(t)
	c.StartReconnectLoop()

	ctx := t.Context()
	for range 5 {
		require.NoError(t, c.publishInternal(ctx, testTopic, "payload", false),
			"Suppressed publish should return nil, not an error")
	}

	c.mu.RLock()
	defer c.mu.RUnlock()
	assert.True(t, c.publishSuppressed, "Should log the first suppressed publish, then stay silent")
	assert.Equal(t, int64(5), c.suppressedPublishCount, "Should count every suppressed publish")
}

// TestStartReconnectLoopAfterDisconnectDoesNotArm verifies that Disconnect wins
// the race: shutdown closes reconnectStop, and arming a timer afterwards would
// leak a goroutine that reconnects to a broker the caller just gave up on.
func TestStartReconnectLoopAfterDisconnectDoesNotArm(t *testing.T) {
	t.Parallel()

	c := newReconnectLoopTestClient(t)

	c.mu.Lock()
	close(c.reconnectStop)
	c.mu.Unlock()

	c.StartReconnectLoop()

	c.mu.RLock()
	defer c.mu.RUnlock()
	assert.Nil(t, c.reconnectTimer, "Should not arm a timer once the reconnect mechanism is stopped")
}

// TestStartReconnectLoopOnConnectedClientIsNoOp verifies a live connection is
// left entirely alone. Connect can report failure for a connection that came up
// late (buildNotConnectedError), and marking a healthy client disconnected would
// silently suppress its publishes until the next reconnect.
func TestStartReconnectLoopOnConnectedClientIsNoOp(t *testing.T) {
	t.Parallel()

	c := newReconnectLoopTestClient(t)

	// A real paho client backed by a live broker is unnecessary here: the guard
	// reads internalClient.IsConnected(), so a stub that reports connected is
	// enough to exercise it.
	c.mu.Lock()
	c.internalClient = &stubPahoClient{connected: true}
	c.mu.Unlock()

	c.StartReconnectLoop()

	c.mu.RLock()
	defer c.mu.RUnlock()
	assert.False(t, c.disconnected, "Should not mark a connected client disconnected")
	assert.Nil(t, c.reconnectTimer, "Should not arm a reconnect timer for a healthy connection")
	assert.True(t, c.disconnectedSince.IsZero(), "Should not record an outage that is not happening")
}

// TestStartReconnectLoopPreservesExistingOutageState verifies the call is
// idempotent with respect to outage bookkeeping. onConnectionLost may have
// already recorded the outage; overwriting disconnectedSince would understate
// the outage duration and re-logging would defeat publish suppression.
func TestStartReconnectLoopPreservesExistingOutageState(t *testing.T) {
	t.Parallel()

	c := newReconnectLoopTestClient(t)

	outageStart := time.Now().Add(-5 * time.Minute)
	c.mu.Lock()
	c.disconnected = true
	c.publishSuppressed = true
	c.suppressedPublishCount = 42
	c.disconnectedSince = outageStart
	c.mu.Unlock()

	c.StartReconnectLoop()

	c.mu.RLock()
	defer c.mu.RUnlock()
	assert.Equal(t, outageStart, c.disconnectedSince, "Should keep the original outage start time")
	assert.True(t, c.publishSuppressed, "Should not reset suppression and re-log")
	assert.Equal(t, int64(42), c.suppressedPublishCount, "Should keep the suppressed publish count")
}
