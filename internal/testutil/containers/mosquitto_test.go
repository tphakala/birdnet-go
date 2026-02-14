//go:build integration

//nolint:misspell // Mosquitto is the official Eclipse project name
package containers

import (
	"context"
	"sync"
	"testing"
	"time"

	mqtt "github.com/eclipse/paho.mqtt.golang"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestMosquittoContainer_ClearRetainedMessages tests the ClearRetainedMessages function
// to ensure it properly clears retained messages without flakiness.
func TestMosquittoContainer_ClearRetainedMessages(t *testing.T) {
	ctx := context.Background()

	// Create Mosquitto container
	container, err := NewMosquittoContainer(ctx, nil)
	require.NoError(t, err, "failed to create Mosquitto container")
	defer func() {
		assert.NoError(t, container.Terminate(ctx), "failed to terminate container")
	}()

	t.Run("clear retained messages", func(t *testing.T) {
		// Publish some retained messages
		publisher, err := container.CreateClient("publisher")
		require.NoError(t, err, "failed to create publisher client")
		defer publisher.Disconnect(250)

		topics := []string{"test/topic1", "test/topic2", "test/topic3"}
		for _, topic := range topics {
			token := publisher.Publish(topic, 0, true, []byte("retained message"))
			require.True(t, token.WaitTimeout(5*time.Second), "publish timeout")
			require.NoError(t, token.Error(), "failed to publish")
		}

		// Verify retained messages exist
		subscriber, err := container.CreateClient("subscriber")
		require.NoError(t, err, "failed to create subscriber client")
		defer subscriber.Disconnect(250)

		var mu sync.Mutex
		receivedTopics := make([]string, 0)
		messagesReceived := make(chan struct{})
		var receiveOnce sync.Once

		token := subscriber.Subscribe("#", 0, func(client mqtt.Client, msg mqtt.Message) {
			if msg.Retained() {
				mu.Lock()
				receivedTopics = append(receivedTopics, msg.Topic())
				mu.Unlock()
				receiveOnce.Do(func() {
					close(messagesReceived)
				})
			}
		})
		require.True(t, token.WaitTimeout(5*time.Second), "subscribe timeout")
		require.NoError(t, token.Error(), "failed to subscribe")

		// Wait for all retained messages to be received
		require.Eventually(t, func() bool {
			mu.Lock()
			defer mu.Unlock()
			return len(receivedTopics) == len(topics)
		}, 2*time.Second, 50*time.Millisecond, "timed out waiting for all retained messages")

		subscriber.Disconnect(250)

		// Clear retained messages
		err = container.ClearRetainedMessages(ctx)
		require.NoError(t, err, "failed to clear retained messages")

		// Verify no retained messages remain
		verifier, err := container.CreateClient("verifier")
		require.NoError(t, err, "failed to create verifier client")
		defer verifier.Disconnect(250)

		mu.Lock()
		receivedTopics = make([]string, 0)
		mu.Unlock()
		messagesReceived = make(chan struct{})
		receiveOnce = sync.Once{}

		token = verifier.Subscribe("#", 0, func(client mqtt.Client, msg mqtt.Message) {
			if msg.Retained() {
				mu.Lock()
				receivedTopics = append(receivedTopics, msg.Topic())
				mu.Unlock()
				receiveOnce.Do(func() {
					close(messagesReceived)
				})
			}
		})
		require.True(t, token.WaitTimeout(5*time.Second), "subscribe timeout")
		require.NoError(t, token.Error(), "failed to subscribe")

		// Wait a bit to ensure no retained messages arrive
		select {
		case <-messagesReceived:
			t.Fatal("should not receive any retained messages after clearing")
		case <-time.After(600 * time.Millisecond):
			// Expected - no retained messages
		}

		mu.Lock()
		finalCount := len(receivedTopics)
		mu.Unlock()

		assert.Equal(t, 0, finalCount, "should have no retained messages after clearing")
	})

	t.Run("clear when no retained messages", func(t *testing.T) {
		// Clear when there are no retained messages should not error
		err := container.ClearRetainedMessages(ctx)
		assert.NoError(t, err, "clearing when no retained messages should not error")
	})
}

// TestMosquittoContainer_ClearRetainedMessages_ContextCancellation tests that
// ClearRetainedMessages respects context cancellation.
func TestMosquittoContainer_ClearRetainedMessages_ContextCancellation(t *testing.T) {
	ctx := context.Background()

	// Create Mosquitto container
	container, err := NewMosquittoContainer(ctx, nil)
	require.NoError(t, err, "failed to create Mosquitto container")
	defer func() {
		assert.NoError(t, container.Terminate(ctx), "failed to terminate container")
	}()

	// Create a context that's already cancelled
	cancelledCtx, cancel := context.WithCancel(ctx)
	cancel()

	// Try to clear with cancelled context
	err = container.ClearRetainedMessages(cancelledCtx)
	require.Error(t, err, "should error with cancelled context")
	assert.Contains(t, err.Error(), "context cancelled", "error should mention context cancellation")
}
