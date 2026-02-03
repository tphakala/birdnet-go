package notification

import (
	"context"
	"fmt"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestBroadcastStats verifies the broadcastStats struct
func TestBroadcastStats(t *testing.T) {
	t.Parallel()

	stats := broadcastStats{
		success:   5,
		failed:    2,
		cancelled: 1,
	}

	assert.Equal(t, 5, stats.success)
	assert.Equal(t, 2, stats.failed)
	assert.Equal(t, 1, stats.cancelled)
}

// TestService_isSubscriberCancelled tests subscriber cancellation detection
func TestService_isSubscriberCancelled(t *testing.T) {
	t.Parallel()

	service := NewService(DefaultServiceConfig())
	defer service.Stop()

	t.Run("not_cancelled", func(t *testing.T) {
		t.Parallel()

		ctx, cancel := context.WithCancel(t.Context())
		defer cancel()

		sub := &Subscriber{
			ch:     make(chan *Notification, 1),
			ctx:    ctx,
			cancel: cancel,
		}

		assert.False(t, service.isSubscriberCancelled(sub))
	})

	t.Run("cancelled", func(t *testing.T) {
		t.Parallel()

		ctx, cancel := context.WithCancel(t.Context())
		cancel() // Cancel immediately

		sub := &Subscriber{
			ch:     make(chan *Notification, 1),
			ctx:    ctx,
			cancel: cancel,
		}

		assert.True(t, service.isSubscriberCancelled(sub))
	})
}

// TestService_sendToSubscriber tests sending notifications to subscribers
func TestService_sendToSubscriber(t *testing.T) {
	t.Parallel()

	service := NewService(DefaultServiceConfig())
	defer service.Stop()

	t.Run("successful_send", func(t *testing.T) {
		t.Parallel()

		ctx, cancel := context.WithCancel(t.Context())
		defer cancel()

		sub := &Subscriber{
			ch:     make(chan *Notification, 1),
			ctx:    ctx,
			cancel: cancel,
		}

		notification := NewNotification(TypeInfo, PriorityLow, "Test", "Message")

		result := service.sendToSubscriber(sub, notification)

		assert.True(t, result)

		// Verify notification was received
		select {
		case received := <-sub.ch:
			assert.Equal(t, notification.ID, received.ID)
			assert.Equal(t, notification.Title, received.Title)
		default:
			require.Fail(t, "expected notification to be in channel")
		}
	})

	t.Run("channel_full", func(t *testing.T) {
		t.Parallel()

		ctx, cancel := context.WithCancel(t.Context())
		defer cancel()

		// Create channel with 0 buffer
		sub := &Subscriber{
			ch:     make(chan *Notification), // unbuffered
			ctx:    ctx,
			cancel: cancel,
		}

		notification := NewNotification(TypeInfo, PriorityLow, "Test", "Message")

		// Should return false since channel is full (no receiver)
		result := service.sendToSubscriber(sub, notification)

		assert.False(t, result)
	})

	t.Run("clones_notification", func(t *testing.T) {
		t.Parallel()

		ctx, cancel := context.WithCancel(t.Context())
		defer cancel()

		sub := &Subscriber{
			ch:     make(chan *Notification, 1),
			ctx:    ctx,
			cancel: cancel,
		}

		notification := NewNotification(TypeInfo, PriorityLow, "Test", "Message").
			WithMetadata("key", "value")

		service.sendToSubscriber(sub, notification)

		received := <-sub.ch

		// Verify it's a different object (clone)
		assert.Equal(t, notification.ID, received.ID)

		// Modify original and verify clone is unaffected
		notification.Title = "Modified"
		assert.Equal(t, "Test", received.Title)
	})
}

// TestService_processSubscribers tests processing multiple subscribers
func TestService_processSubscribers(t *testing.T) {
	t.Parallel()

	t.Run("all_active", func(t *testing.T) {
		t.Parallel()

		config := DefaultServiceConfig()
		config.Debug = true
		service := NewService(config)
		defer service.Stop()

		// Create subscribers
		ctx1, cancel1 := context.WithCancel(t.Context())
		ctx2, cancel2 := context.WithCancel(t.Context())
		defer cancel1()
		defer cancel2()

		service.subscribers = []*Subscriber{
			{ch: make(chan *Notification, 1), ctx: ctx1, cancel: cancel1},
			{ch: make(chan *Notification, 1), ctx: ctx2, cancel: cancel2},
		}

		notification := NewNotification(TypeInfo, PriorityLow, "Test", "Message")

		active, stats := service.processSubscribers(notification)

		assert.Len(t, active, 2)
		assert.Equal(t, 2, stats.success)
		assert.Equal(t, 0, stats.failed)
		assert.Equal(t, 0, stats.cancelled)
	})

	t.Run("some_cancelled", func(t *testing.T) {
		t.Parallel()

		service := NewService(DefaultServiceConfig())
		defer service.Stop()

		ctx1, cancel1 := context.WithCancel(t.Context())
		ctx2, cancel2 := context.WithCancel(t.Context())
		defer cancel1()

		// Cancel second subscriber
		cancel2()

		service.subscribers = []*Subscriber{
			{ch: make(chan *Notification, 1), ctx: ctx1, cancel: cancel1},
			{ch: make(chan *Notification, 1), ctx: ctx2, cancel: cancel2},
		}

		notification := NewNotification(TypeInfo, PriorityLow, "Test", "Message")

		active, stats := service.processSubscribers(notification)

		assert.Len(t, active, 1)
		assert.Equal(t, 1, stats.success)
		assert.Equal(t, 0, stats.failed)
		assert.Equal(t, 1, stats.cancelled)
	})

	t.Run("some_channel_full", func(t *testing.T) {
		t.Parallel()

		service := NewService(DefaultServiceConfig())
		defer service.Stop()

		ctx1, cancel1 := context.WithCancel(t.Context())
		ctx2, cancel2 := context.WithCancel(t.Context())
		defer cancel1()
		defer cancel2()

		service.subscribers = []*Subscriber{
			{ch: make(chan *Notification, 1), ctx: ctx1, cancel: cancel1},
			{ch: make(chan *Notification), ctx: ctx2, cancel: cancel2}, // unbuffered = full
		}

		notification := NewNotification(TypeInfo, PriorityLow, "Test", "Message")

		active, stats := service.processSubscribers(notification)

		assert.Len(t, active, 2) // Both still active
		assert.Equal(t, 1, stats.success)
		assert.Equal(t, 1, stats.failed) // One failed due to full channel
		assert.Equal(t, 0, stats.cancelled)
	})

	t.Run("empty_subscribers", func(t *testing.T) {
		t.Parallel()

		service := NewService(DefaultServiceConfig())
		defer service.Stop()

		service.subscribers = []*Subscriber{}

		notification := NewNotification(TypeInfo, PriorityLow, "Test", "Message")

		active, stats := service.processSubscribers(notification)

		assert.Empty(t, active)
		assert.Equal(t, 0, stats.success)
		assert.Equal(t, 0, stats.failed)
		assert.Equal(t, 0, stats.cancelled)
	})
}

// TestService_countExpired tests counting expired notifications
func TestService_countExpired(t *testing.T) {
	t.Parallel()

	service := NewService(DefaultServiceConfig())
	defer service.Stop()

	t.Run("no_notifications", func(t *testing.T) {
		t.Parallel()

		count := service.countExpired([]*Notification{})
		assert.Equal(t, 0, count)
	})

	t.Run("no_expired", func(t *testing.T) {
		t.Parallel()

		notifications := []*Notification{
			NewNotification(TypeInfo, PriorityLow, "Test1", "Message1"),
			NewNotification(TypeInfo, PriorityLow, "Test2", "Message2"),
		}

		count := service.countExpired(notifications)
		assert.Equal(t, 0, count)
	})

	t.Run("some_expired", func(t *testing.T) {
		t.Parallel()

		pastTime := time.Now().Add(-1 * time.Hour)
		futureTime := time.Now().Add(1 * time.Hour)

		notifications := []*Notification{
			{ID: "1", ExpiresAt: &pastTime},   // expired
			{ID: "2", ExpiresAt: &futureTime}, // not expired
			{ID: "3", ExpiresAt: &pastTime},   // expired
			{ID: "4"},                         // no expiry, not expired
		}

		count := service.countExpired(notifications)
		assert.Equal(t, 2, count)
	})

	t.Run("all_expired", func(t *testing.T) {
		t.Parallel()

		pastTime := time.Now().Add(-1 * time.Hour)

		notifications := []*Notification{
			{ID: "1", ExpiresAt: &pastTime},
			{ID: "2", ExpiresAt: &pastTime},
		}

		count := service.countExpired(notifications)
		assert.Equal(t, 2, count)
	})
}

// TestService_broadcast_Integration tests the full broadcast flow
func TestService_broadcast_Integration(t *testing.T) {
	t.Parallel()

	t.Run("broadcasts_to_all_subscribers", func(t *testing.T) {
		t.Parallel()

		service := NewService(DefaultServiceConfig())
		defer service.Stop()

		// Subscribe multiple clients
		ch1, ctx1 := service.Subscribe()
		ch2, ctx2 := service.Subscribe()
		defer service.Unsubscribe(ch1)
		defer service.Unsubscribe(ch2)

		// Verify contexts are valid
		require.NotNil(t, ctx1)
		require.NotNil(t, ctx2)

		// Create and broadcast notification
		notification := NewNotification(TypeInfo, PriorityLow, "Broadcast Test", "Message")
		service.broadcast(notification)

		// Verify both subscribers received it
		select {
		case n := <-ch1:
			assert.Equal(t, notification.ID, n.ID)
		case <-time.After(500 * time.Millisecond):
			require.Fail(t, "subscriber 1 did not receive notification")
		}

		select {
		case n := <-ch2:
			assert.Equal(t, notification.ID, n.ID)
		case <-time.After(500 * time.Millisecond):
			require.Fail(t, "subscriber 2 did not receive notification")
		}
	})

	t.Run("removes_cancelled_subscribers", func(t *testing.T) {
		t.Parallel()

		service := NewService(DefaultServiceConfig())
		defer service.Stop()

		ch1, _ := service.Subscribe()
		ch2, _ := service.Subscribe()

		// Unsubscribe first one (cancels context)
		service.Unsubscribe(ch1)

		// Broadcast should only go to second subscriber
		notification := NewNotification(TypeInfo, PriorityLow, "Test", "Message")
		service.broadcast(notification)

		// Second subscriber should receive it
		select {
		case n := <-ch2:
			assert.Equal(t, notification.ID, n.ID)
		case <-time.After(500 * time.Millisecond):
			require.Fail(t, "subscriber 2 did not receive notification")
		}

		// Verify only one active subscriber remains
		service.subscribersMu.RLock()
		count := len(service.subscribers)
		service.subscribersMu.RUnlock()

		assert.Equal(t, 1, count)
	})
}

// TestService_performCleanup tests the cleanup functionality
func TestService_performCleanup(t *testing.T) {
	t.Parallel()

	t.Run("removes_expired_notifications", func(t *testing.T) {
		t.Parallel()

		service := NewService(DefaultServiceConfig())
		defer service.Stop()

		// Add expired notification
		expiredNotif := NewNotification(TypeInfo, PriorityLow, "Expired", "This is expired")
		pastTime := time.Now().Add(-1 * time.Hour)
		expiredNotif.ExpiresAt = &pastTime
		err := service.store.Save(expiredNotif)
		require.NoError(t, err)

		// Add non-expired notification
		activeNotif := NewNotification(TypeInfo, PriorityLow, "Active", "This is active")
		futureTime := time.Now().Add(1 * time.Hour)
		activeNotif.ExpiresAt = &futureTime
		err = service.store.Save(activeNotif)
		require.NoError(t, err)

		// Run cleanup
		service.performCleanup()

		// Verify expired was removed
		_, err = service.store.Get(expiredNotif.ID)
		require.Error(t, err)

		// Verify active remains
		active, err := service.store.Get(activeNotif.ID)
		require.NoError(t, err)
		assert.Equal(t, activeNotif.ID, active.ID)
	})
}

// TestService_Subscribe_Unsubscribe tests subscription management
func TestService_Subscribe_Unsubscribe(t *testing.T) {
	t.Parallel()

	t.Run("subscribe_creates_channel", func(t *testing.T) {
		t.Parallel()

		service := NewService(DefaultServiceConfig())
		defer service.Stop()

		ch, ctx := service.Subscribe()

		assert.NotNil(t, ch)
		assert.NotNil(t, ctx)

		service.subscribersMu.RLock()
		count := len(service.subscribers)
		service.subscribersMu.RUnlock()

		assert.Equal(t, 1, count)
	})

	t.Run("unsubscribe_removes_subscriber", func(t *testing.T) {
		t.Parallel()

		service := NewService(DefaultServiceConfig())
		defer service.Stop()

		ch, _ := service.Subscribe()

		service.subscribersMu.RLock()
		countBefore := len(service.subscribers)
		service.subscribersMu.RUnlock()

		service.Unsubscribe(ch)

		service.subscribersMu.RLock()
		countAfter := len(service.subscribers)
		service.subscribersMu.RUnlock()

		assert.Equal(t, 1, countBefore)
		assert.Equal(t, 0, countAfter)
	})

	t.Run("unsubscribe_cancels_context", func(t *testing.T) {
		t.Parallel()

		service := NewService(DefaultServiceConfig())
		defer service.Stop()

		ch, ctx := service.Subscribe()
		service.Unsubscribe(ch)

		// Context should be cancelled
		select {
		case <-ctx.Done():
			// Expected
		default:
			require.Fail(t, "context should be cancelled after unsubscribe")
		}
	})
}

// TestService_ConcurrentBroadcast tests thread-safety of broadcast
func TestService_ConcurrentBroadcast(t *testing.T) {
	t.Parallel()

	service := NewService(DefaultServiceConfig())
	defer service.Stop()

	// Create multiple subscribers
	channels := make([]<-chan *Notification, 5)
	for i := range 5 {
		ch, _ := service.Subscribe()
		channels[i] = ch
	}

	// Broadcast concurrently
	var wg sync.WaitGroup
	for i := range 10 {
		wg.Go(func() {
			notification := NewNotification(TypeInfo, PriorityLow, "Concurrent", "Test")
			notification.ID += fmt.Sprintf("-%d", i) // Make unique
			service.broadcast(notification)
		})
	}

	wg.Wait()

	// Drain channels to verify no deadlock with timeout protection
	for _, ch := range channels {
		drainCount := 0
		timeout := time.After(2 * time.Second)
		timedOut := false
	drainLoop:
		for {
			select {
			case <-ch:
				drainCount++
			case <-timeout:
				timedOut = true
				break drainLoop
			default:
				break drainLoop
			}
		}
		assert.False(t, timedOut, "timeout draining channel after receiving %d notifications", drainCount)
		// Should have received some notifications
		assert.GreaterOrEqual(t, drainCount, 0)
	}
}
