//go:build integration

package notification_test

import (
	"context"
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/tphakala/birdnet-go/internal/notification"
	"github.com/tphakala/birdnet-go/internal/testutil/containers"
)

// setupGotifyContainer creates a Gotify container and registers cleanup.
func setupGotifyContainer(t *testing.T) *containers.GotifyContainer {
	t.Helper()
	ctx := context.Background()
	c, err := containers.NewGotifyContainer(ctx, nil)
	require.NoError(t, err, "failed to start gotify container")
	t.Cleanup(func() { _ = c.Terminate(context.Background()) })
	return c
}

// setupGotifyApp creates an application in the Gotify container and returns the token.
func setupGotifyApp(t *testing.T, ctx context.Context, container *containers.GotifyContainer, name string) *containers.GotifyApplication {
	t.Helper()
	app, err := container.CreateApplication(ctx, name, "integration test application")
	require.NoError(t, err, "failed to create gotify application")
	return app
}

// shoutrrrGotifyURL builds a shoutrrr gotify URL for an HTTP-only server.
// Format: gotify://host:port/token?disabletls=yes
func shoutrrrGotifyURL(host, token string) string {
	return fmt.Sprintf("gotify://%s/%s?disabletls=yes", host, token)
}

func TestGotifyShoutrrrDelivery(t *testing.T) {
	container := setupGotifyContainer(t)
	ctx := context.Background()
	host := container.GetHost(ctx)

	tests := []struct {
		name    string
		title   string
		message string
	}{
		{
			name:    "basic_delivery",
			message: "Hello from BirdNET-Go Gotify integration test",
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
			message: strings.Repeat("B", 2048),
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			// Create a unique application per subtest for isolation
			app := setupGotifyApp(t, ctx, container, "test-"+tt.name)
			url := shoutrrrGotifyURL(host, app.Token)

			provider := notification.NewShoutrrrProvider(
				"test-gotify", true, []string{url}, nil, 30*time.Second,
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

			// Retrieve messages from the application
			messages, err := container.GetMessages(ctx, app.ID)
			require.NoError(t, err, "GetMessages should succeed")
			require.Len(t, messages, 1, "expected exactly one message")

			assert.Equal(t, tt.message, messages[0].Message, "message body should match")
			if tt.title != "" {
				assert.Equal(t, tt.title, messages[0].Title, "message title should match")
			}
		})
	}
}

func TestGotifyShoutrrrDelivery_MultipleMessages(t *testing.T) {
	container := setupGotifyContainer(t)
	ctx := context.Background()
	host := container.GetHost(ctx)
	app := setupGotifyApp(t, ctx, container, "test-multi")
	url := shoutrrrGotifyURL(host, app.Token)

	provider := notification.NewShoutrrrProvider(
		"test-gotify-multi", true, []string{url}, nil, 30*time.Second,
	)
	require.NoError(t, provider.ValidateConfig(), "ValidateConfig should succeed")

	// Send multiple messages
	const messageCount = 5
	for i := range messageCount {
		n := notification.NewNotification(
			notification.TypeInfo,
			notification.PriorityMedium,
			fmt.Sprintf("Alert %d", i),
			fmt.Sprintf("Message number %d", i),
		)
		require.NoError(t, provider.Send(ctx, n), "Send %d should succeed", i)
	}

	// Verify all messages were received
	messages, err := container.GetMessages(ctx, app.ID)
	require.NoError(t, err, "GetMessages should succeed")
	assert.Len(t, messages, messageCount, "expected %d messages", messageCount)
}

func TestGotifyShoutrrrDelivery_InvalidToken(t *testing.T) {
	container := setupGotifyContainer(t)
	ctx := context.Background()
	host := container.GetHost(ctx)

	// Use a bogus token that will be rejected by Gotify server
	url := shoutrrrGotifyURL(host, "Ainvalid.token.")

	provider := notification.NewShoutrrrProvider(
		"test-gotify-invalid", true, []string{url}, nil, 30*time.Second,
	)
	require.NoError(t, provider.ValidateConfig(), "ValidateConfig should succeed (URL parsing only)")

	n := notification.NewNotification(
		notification.TypeInfo,
		notification.PriorityMedium,
		"",
		"This should fail",
	)

	err := provider.Send(ctx, n)
	assert.Error(t, err, "Send should fail with invalid token")
}

func TestGotifyContainer_HealthCheck(t *testing.T) {
	container := setupGotifyContainer(t)
	ctx := context.Background()

	err := container.HealthCheck(ctx)
	assert.NoError(t, err, "HealthCheck should succeed")
}
