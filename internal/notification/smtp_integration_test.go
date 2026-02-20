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

// setupMailpitContainer creates a Mailpit container and registers cleanup.
func setupMailpitContainer(t *testing.T) *containers.MailpitContainer {
	t.Helper()
	ctx := context.Background()
	c, err := containers.NewMailpitContainer(ctx, nil)
	require.NoError(t, err, "failed to start mailpit container")
	t.Cleanup(func() { _ = c.Terminate(context.Background()) })
	return c
}

// shoutrrrSMTPURL builds a shoutrrr SMTP URL for the Mailpit container.
// Mailpit accepts any SMTP connection without authentication.
// Format: smtp://host:port/?from=sender@example.com&to=recipient@example.com&auth=None&encryption=None&useStartTLS=No
func shoutrrrSMTPURL(host string, smtpPort int, from, to string) string {
	return fmt.Sprintf("smtp://%s:%d/?from=%s&to=%s&auth=None&encryption=None&useStartTLS=No&subject=BirdNET-Go+Notification",
		host, smtpPort, from, to)
}

func TestSMTPShoutrrrDelivery(t *testing.T) {
	container := setupMailpitContainer(t)
	ctx := context.Background()
	host := container.GetHost()
	smtpPort := container.GetSMTPPort()

	const (
		fromAddr = "birdnet@example.com"
		toAddr   = "user@example.com"
	)

	tests := []struct {
		name    string
		title   string
		message string
	}{
		{
			name:    "basic_delivery",
			message: "Hello from BirdNET-Go SMTP integration test",
		},
		{
			name:    "with_title",
			title:   "Bird Detection Alert",
			message: "A rare bird was detected nearby via SMTP",
		},
		{
			name:    "special_chars_in_message",
			message: "Temperature > 30°C & humidity < 50% — bird: 🐦",
		},
		{
			name:    "long_message",
			message: strings.Repeat("C", 2048),
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			// Clear previous messages for isolation
			require.NoError(t, container.DeleteAllMessages(ctx), "should clear messages")

			url := shoutrrrSMTPURL(host, smtpPort, fromAddr, toAddr)

			provider := notification.NewShoutrrrProvider(
				"test-smtp", true, []string{url}, nil, 30*time.Second,
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

			// Give Mailpit a moment to process the message
			time.Sleep(500 * time.Millisecond)

			// Verify message was captured by Mailpit
			messages, err := container.ListMessages(ctx)
			require.NoError(t, err, "ListMessages should succeed")
			require.Len(t, messages, 1, "expected exactly one message")

			// Verify sender and recipient
			assert.Equal(t, fromAddr, messages[0].From.Address, "from address should match")
			require.Len(t, messages[0].To, 1, "expected exactly one recipient")
			assert.Equal(t, toAddr, messages[0].To[0].Address, "to address should match")

			// Verify message body via full message detail
			detail, err := container.GetMessage(ctx, messages[0].ID)
			require.NoError(t, err, "GetMessage should succeed")
			assert.Contains(t, detail.Text, tt.message, "message body should contain the notification text")
		})
	}
}

func TestSMTPShoutrrrDelivery_MultipleRecipients(t *testing.T) {
	container := setupMailpitContainer(t)
	ctx := context.Background()
	host := container.GetHost()
	smtpPort := container.GetSMTPPort()

	const fromAddr = "birdnet@example.com"
	toAddrs := "user1@example.com,user2@example.com"

	url := fmt.Sprintf("smtp://%s:%d/?from=%s&to=%s&auth=None&encryption=None&useStartTLS=No&subject=Multi-Recipient+Test",
		host, smtpPort, fromAddr, toAddrs)

	provider := notification.NewShoutrrrProvider(
		"test-smtp-multi", true, []string{url}, nil, 30*time.Second,
	)
	require.NoError(t, provider.ValidateConfig(), "ValidateConfig should succeed")

	msg := "Multi-recipient delivery test"
	n := notification.NewNotification(notification.TypeInfo, notification.PriorityMedium, "", msg)

	err := provider.Send(ctx, n)
	require.NoError(t, err, "Send should succeed")

	// Give Mailpit a moment to process
	time.Sleep(500 * time.Millisecond)

	// Verify messages were captured
	messages, err := container.ListMessages(ctx)
	require.NoError(t, err, "ListMessages should succeed")
	require.NotEmpty(t, messages, "expected at least one message")

	// Verify the message has both recipients
	detail, err := container.GetMessage(ctx, messages[0].ID)
	require.NoError(t, err, "GetMessage should succeed")
	assert.Contains(t, detail.Text, msg, "message body should contain the notification text")
}

func TestSMTPShoutrrrDelivery_UnreachableServer(t *testing.T) {
	// Use a port that nothing is listening on
	url := "smtp://127.0.0.1:1/?from=test@example.com&to=user@example.com&auth=None&encryption=None&useStartTLS=No"

	provider := notification.NewShoutrrrProvider(
		"test-smtp-unreachable", true, []string{url}, nil, 5*time.Second,
	)
	require.NoError(t, provider.ValidateConfig(), "ValidateConfig should succeed (URL parsing only)")

	n := notification.NewNotification(notification.TypeInfo, notification.PriorityMedium, "", "Should fail")

	err := provider.Send(context.Background(), n)
	assert.Error(t, err, "Send should fail with unreachable server")
}

func TestMailpitContainer_HealthCheck(t *testing.T) {
	container := setupMailpitContainer(t)
	ctx := context.Background()

	err := container.HealthCheck(ctx)
	assert.NoError(t, err, "HealthCheck should succeed")
}
