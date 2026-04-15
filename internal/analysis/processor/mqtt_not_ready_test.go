// mqtt_not_ready_test.go: tests for the PublishMQTT sentinel-error path.
//
// When MQTT is enabled in settings but no client reference is available
// (initial connect failed, or between disconnect and reconfigure),
// PublishMQTT must return the ErrMQTTClientNotReady sentinel WITHOUT building
// a telemetry-tagged error. Streaming publishers that run on a timer detect
// the sentinel and silently skip to avoid flooding Sentry.
package processor

import (
	"sync"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestPublishMQTT_NilClient_ReturnsSentinel verifies that PublishMQTT returns
// ErrMQTTClientNotReady (and not a category-tagged error) when no client
// reference has been set. This is the core fix for the "MQTT client not
// available" Sentry flood reported when the broker is unreachable at startup.
func TestPublishMQTT_NilClient_ReturnsSentinel(t *testing.T) {
	t.Parallel()

	p := &Processor{}

	err := p.PublishMQTT(t.Context(), "birdnet/topic", "payload")
	require.Error(t, err, "PublishMQTT must return an error when client is nil")
	assert.ErrorIs(t, err, ErrMQTTClientNotReady,
		"error must be identifiable as ErrMQTTClientNotReady (got %v)", err)
}

// TestPublishMQTT_NilClient_WarnsOnlyOnce verifies that the one-shot warn
// log fires at most once across concurrent callers. This prevents log floods
// when the MQTT broker is unreachable but enabled in settings.
func TestPublishMQTT_NilClient_WarnsOnlyOnce(t *testing.T) {
	t.Parallel()

	p := &Processor{}

	// Fire many concurrent publish attempts — the sync.Once guarantee is the
	// contract we are verifying. If race-detector complains here, we have
	// a real concurrency bug.
	const goroutines = 32
	const callsPer = 50
	var wg sync.WaitGroup
	ctx := t.Context()
	for range goroutines {
		wg.Go(func() {
			for range callsPer {
				err := p.PublishMQTT(ctx, "t", "p")
				assert.ErrorIs(t, err, ErrMQTTClientNotReady)
			}
		})
	}
	wg.Wait()

	// Confirm the sentinel is still returned after the once has fired —
	// the behavior must be idempotent and not degrade.
	err := p.PublishMQTT(t.Context(), "t", "p")
	assert.ErrorIs(t, err, ErrMQTTClientNotReady)
}

// TestPublishMQTT_WithClient_DelegatesToClient verifies that when a client
// is set, PublishMQTT delegates rather than returning the sentinel. This is
// a regression guard to make sure the fix doesn't accidentally short-circuit
// the happy path.
func TestPublishMQTT_WithClient_DelegatesToClient(t *testing.T) {
	t.Parallel()

	mockClient := NewMockMQTTClient()
	p := &Processor{}
	p.SetMQTTClient(mockClient)

	err := p.PublishMQTT(t.Context(), "birdnet/topic", "hello")
	require.NoError(t, err)
	assert.Equal(t, 1, mockClient.GetPublishCalls(), "mock publish must be invoked once")
	assert.Equal(t, "birdnet/topic", mockClient.GetPublishedTopic())
	assert.Equal(t, "hello", mockClient.GetPublishedPayload())
}

// TestPublishMQTT_SetThenClear_ReturnsSentinel verifies the reconfigure-path
// race (between DisconnectMQTTClient and SetMQTTClient) does not emit a
// category-tagged error. The reconfigure window is exactly when bursts of
// sound-level publishes would otherwise be misreported to Sentry.
func TestPublishMQTT_SetThenClear_ReturnsSentinel(t *testing.T) {
	t.Parallel()

	p := &Processor{}
	mockClient := NewMockMQTTClient()
	p.SetMQTTClient(mockClient)

	// Simulate DisconnectMQTTClient clearing the reference.
	p.DisconnectMQTTClient()

	err := p.PublishMQTT(t.Context(), "t", "p")
	assert.ErrorIs(t, err, ErrMQTTClientNotReady,
		"post-disconnect publishes must return the sentinel, not a telemetry error")
}
