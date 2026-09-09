// mqtt_initialize_test.go - Tests for initializeMQTT()'s handling of a broker
// that is unreachable at startup.

package processor

import (
	"context"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/tphakala/birdnet-go/internal/analysis/jobqueue"
	"github.com/tphakala/birdnet-go/internal/conf"
	"github.com/tphakala/birdnet-go/internal/observability"
)

// unreachableBroker refuses connections immediately rather than timing out, so
// the connect failure under test is fast and deterministic.
const unreachableBroker = "tcp://127.0.0.1:1"

func newMQTTInitTestProcessor(t *testing.T, broker string) (*Processor, *conf.Settings) {
	t.Helper()

	metrics, err := observability.NewMetrics()
	require.NoError(t, err)

	settings := &conf.Settings{
		Realtime: conf.RealtimeSettings{
			MQTT: conf.MQTTSettings{
				Enabled: true,
				Broker:  broker,
				Topic:   "birdnet/test",
			},
		},
	}

	// JobQueue is the only field ShutdownWithContext dereferences unguarded.
	p := &Processor{Settings: settings, Metrics: metrics, JobQueue: jobqueue.NewJobQueue()}
	// Cancels the reconnect loop armed by a failed connect.
	t.Cleanup(p.DisconnectMQTTClient)

	return p, settings
}

// TestInitializeMQTTRetainsClientWhenBrokerUnreachable is the regression test
// for MQTT staying dead for the entire process run after a boot-time race with
// the network. initializeMQTT used to discard the client when the first connect
// failed, and nothing ever re-created it, so a broker that was unreachable for
// a few seconds at startup cost MQTT until the next restart.
//
// This test fails on the old code: GetMQTTClient() returned nil.
func TestInitializeMQTTRetainsClientWhenBrokerUnreachable(t *testing.T) {
	p, settings := newMQTTInitTestProcessor(t, unreachableBroker)

	p.initializeMQTT(settings)

	client := p.GetMQTTClient()
	require.NotNil(t, client, "Should retain the client so its reconnect loop can recover the connection")
	assert.False(t, client.IsConnected(), "Should not claim to be connected")

	// Publishes degrade gracefully while the client reconnects: suppressed
	// inside the mqtt package rather than surfacing ErrMQTTClientNotReady.
	err := p.PublishMQTT(t.Context(), "birdnet/test", "payload")
	assert.NoError(t, err, "Publishes should be suppressed, not error, while reconnecting")
}

// TestInitializeMQTTSkipsWhenDisabled guards the early return so a disabled
// integration never creates a client or arms a reconnect loop.
func TestInitializeMQTTSkipsWhenDisabled(t *testing.T) {
	p, settings := newMQTTInitTestProcessor(t, unreachableBroker)
	settings.Realtime.MQTT.Enabled = false

	p.initializeMQTT(settings)

	assert.Nil(t, p.GetMQTTClient(), "Should not create a client when MQTT is disabled")
}

// TestShutdownDisconnectsUnconnectedClient verifies shutdown cancels the
// reconnect loop of a client that never connected. Gating Disconnect() on
// IsConnected() leaked the retry timer for exactly the case this PR introduces.
func TestShutdownDisconnectsUnconnectedClient(t *testing.T) {
	p, _ := newMQTTInitTestProcessor(t, unreachableBroker)

	mockClient := NewMockMQTTClient()
	mockClient.Disconnect() // leaves the mock reporting not-connected
	require.False(t, mockClient.IsConnected())
	disconnectsBeforeShutdown := mockClient.DisconnectCalls()

	p.SetMQTTClient(mockClient)
	require.NoError(t, p.ShutdownWithContext(t.Context()))

	assert.Greater(t, mockClient.DisconnectCalls(), disconnectsBeforeShutdown,
		"Shutdown should disconnect the client even when it is not connected")
}

// TestShutdownDisconnectsClientWhenContextExpired verifies MQTT cleanup runs
// ahead of the expired-context bail-out. Cancelling the reconnect loop is not a
// "nice-to-have disconnect" that can be skipped when shutdown runs out of time:
// skipping it leaves the retry timer running past shutdown.
func TestShutdownDisconnectsClientWhenContextExpired(t *testing.T) {
	p, _ := newMQTTInitTestProcessor(t, unreachableBroker)

	mockClient := NewMockMQTTClient()
	mockClient.Disconnect() // never-connected client, as after a failed initial connect
	disconnectsBeforeShutdown := mockClient.DisconnectCalls()
	p.SetMQTTClient(mockClient)

	ctx, cancel := context.WithCancel(t.Context())
	cancel() // exhaust the shutdown budget before ShutdownWithContext runs
	require.NoError(t, p.ShutdownWithContext(ctx))

	assert.Greater(t, mockClient.DisconnectCalls(), disconnectsBeforeShutdown,
		"Shutdown should cancel the reconnect loop even when the context is expired")
}
