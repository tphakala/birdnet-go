package notification

import (
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/tphakala/birdnet-go/internal/conf"
	"go.uber.org/goleak"
)

func TestIsAlertRuleTestNotification(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name     string
		notif    *Notification
		expected bool
	}{
		{
			name:     "nil notification",
			notif:    nil,
			expected: false,
		},
		{
			name:     "notification with nil metadata",
			notif:    &Notification{},
			expected: false,
		},
		{
			name: "normal notification without test flag",
			notif: NewNotification(TypeWarning, PriorityHigh, "title", "message").
				WithMetadata("species", "Test Bird"),
			expected: false,
		},
		{
			name: "alert rule test notification",
			notif: NewNotification(TypeWarning, PriorityHigh, "title", "message").
				WithMetadata(MetadataKeyIsAlertRuleTest, true),
			expected: true,
		},
		{
			name: "notification with false test flag",
			notif: NewNotification(TypeWarning, PriorityHigh, "title", "message").
				WithMetadata(MetadataKeyIsAlertRuleTest, false),
			expected: false,
		},
		{
			name: "notification with non-bool test flag",
			notif: NewNotification(TypeWarning, PriorityHigh, "title", "message").
				WithMetadata(MetadataKeyIsAlertRuleTest, "true"),
			expected: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			result := isAlertRuleTestNotification(tt.notif)
			assert.Equal(t, tt.expected, result)
		})
	}
}

func TestPushDispatcher_SkipsAlertRuleTestNotifications(t *testing.T) {
	svc := NewService(DefaultServiceConfig())
	err := SetServiceForTesting(svc)
	if err != nil {
		svc = GetService()
		require.NotNil(t, svc, "failed to attach to notification service")
	}

	fp := newFakeProvider("fake-test", true)

	d := &pushDispatcher{
		providers:      []enhancedProvider{{prov: fp, circuitBreaker: nil, filter: conf.PushFilterConfig{}, name: fp.name}},
		log:            GetLogger(),
		enabled:        true,
		maxRetries:     0,
		retryDelay:     10 * time.Millisecond,
		defaultTimeout: 200 * time.Millisecond,
	}

	err = d.start()
	require.NoError(t, err, "failed to start dispatcher")

	// Stop and verify cleanup
	defer goleak.VerifyNone(t, goleak.IgnoreCurrent())
	defer func() {
		if d.cancel != nil {
			d.cancel()
		}
	}()

	// Send a regular notification — should be received by provider
	_, err = svc.Create(TypeWarning, PriorityHigh, "Real Alert", "real message")
	require.NoError(t, err)

	select {
	case n := <-fp.recvCh:
		assert.Equal(t, "Real Alert", n.Title)
	case <-time.After(1 * time.Second):
		require.Fail(t, "timeout waiting for regular notification")
	}

	// Send an alert rule test notification — should NOT be received by provider
	testNotif := NewNotification(TypeWarning, PriorityHigh, "Test Alert", "test message").
		WithMetadata(MetadataKeyIsAlertRuleTest, true)
	err = svc.CreateWithMetadata(testNotif)
	require.NoError(t, err)

	// Send another regular notification to verify the dispatcher is still running
	_, err = svc.Create(TypeInfo, PriorityLow, "After Test", "after test message")
	require.NoError(t, err)

	select {
	case n := <-fp.recvCh:
		// The next notification received should be "After Test", not "Test Alert"
		assert.Equal(t, "After Test", n.Title, "alert rule test notification should be skipped")
	case <-time.After(1 * time.Second):
		require.Fail(t, "timeout waiting for notification after test notification")
	}
}
