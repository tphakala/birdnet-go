//go:build integration

package notification_test

import (
	"context"
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/tphakala/birdnet-go/internal/notification"
	"github.com/tphakala/birdnet-go/internal/testutil/containers"
)

// setupNtfyContainer creates a no-auth ntfy container and registers cleanup.
func setupNtfyContainer(t *testing.T) *containers.NtfyContainer {
	t.Helper()
	ctx := context.Background()
	c, err := containers.NewNtfyContainer(ctx, nil)
	require.NoError(t, err, "failed to start ntfy container")
	t.Cleanup(func() { _ = c.Terminate(context.Background()) })
	return c
}

// uniqueTopic returns a short unique topic name for test isolation.
func uniqueTopic(prefix string) string {
	return fmt.Sprintf("%s-%s", prefix, uuid.NewString()[:8])
}

// shoutrrrNtfyURL builds a shoutrrr ntfy URL for an HTTP-only server.
func shoutrrrNtfyURL(host, topic string) string {
	return fmt.Sprintf("ntfy://%s/%s?scheme=http", host, topic)
}

func TestNtfyShoutrrrDelivery_NoAuth(t *testing.T) {
	container := setupNtfyContainer(t)
	ctx := context.Background()
	host := container.GetHost(ctx)

	tests := []struct {
		name    string
		title   string
		message string
	}{
		{
			name:    "basic_delivery",
			message: "Hello from BirdNET-Go integration test",
		},
		{
			name:    "with_title",
			title:   "Bird Alert",
			message: "A rare bird was detected nearby",
		},
		{
			name:    "special_chars_in_message",
			message: "Temperature > 30°C & humidity < 50% — bird: 🐦",
		},
		{
			name:    "long_message",
			message: strings.Repeat("A", 2048),
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			topic := uniqueTopic(tt.name)
			url := shoutrrrNtfyURL(host, topic)

			provider := notification.NewShoutrrrProvider(
				"test-ntfy", true, []string{url}, nil, 30*time.Second,
			)
			require.NoError(t, provider.ValidateConfig(), "ValidateConfig should succeed")

			n := notification.NewNotification(
				notification.TypeInfo,
				notification.PriorityMedium,
				tt.title,
				tt.message,
			)

			err := provider.Send(ctx, n)
			require.NoError(t, err, "Send should succeed")

			// Poll messages from the topic
			messages, err := container.PollMessages(ctx, topic)
			require.NoError(t, err, "PollMessages should succeed")
			require.Len(t, messages, 1, "expected exactly one message")

			assert.Equal(t, tt.message, messages[0].Message, "message body should match")
			if tt.title != "" {
				assert.Equal(t, tt.title, messages[0].Title, "message title should match")
			}
		})
	}
}
