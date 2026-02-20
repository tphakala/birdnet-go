//go:build integration

package notification_test

import (
	"context"
	"fmt"
	"net/url"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/tphakala/birdnet-go/internal/notification"
	"github.com/tphakala/birdnet-go/internal/testutil/containers"
)

// setupNtfyAuthContainer creates an auth-enabled ntfy container, adds a user,
// and registers cleanup.
func setupNtfyAuthContainer(t *testing.T, username, password string) *containers.NtfyContainer {
	t.Helper()
	ctx := context.Background()
	cfg := containers.DefaultNtfyConfig()
	cfg.EnableAuth = true
	c, err := containers.NewNtfyContainer(ctx, &cfg)
	require.NoError(t, err, "failed to start auth-enabled ntfy container")
	t.Cleanup(func() { _ = c.Terminate(context.Background()) })
	require.NoError(t, c.AddUser(ctx, username, password), "failed to add user")
	return c
}

// shoutrrrNtfyAuthURL builds a shoutrrr ntfy URL with Basic Auth credentials.
// Uses url.UserPassword for correct percent-encoding of both username and password.
func shoutrrrNtfyAuthURL(username, password, host, topic string) string {
	u := &url.URL{
		Scheme:   "ntfy",
		User:     url.UserPassword(username, password),
		Host:     host,
		Path:     "/" + topic,
		RawQuery: "scheme=http",
	}
	return u.String()
}

func TestNtfyShoutrrrDelivery_BasicAuth(t *testing.T) {
	const (
		testUser = "testuser"
		testPass = "testpass"
	)

	ctx := context.Background()

	t.Run("valid_credentials", func(t *testing.T) {
		container := setupNtfyAuthContainer(t, testUser, testPass)
		host := container.GetHost(ctx)
		topic := uniqueTopic("valid-creds")

		require.NoError(t, container.GrantAccess(ctx, testUser, topic, "rw"))

		ntfyURL := shoutrrrNtfyAuthURL(testUser, testPass, host, topic)
		provider := notification.NewShoutrrrProvider(
			"test-ntfy-auth", true, []string{ntfyURL}, nil, 30*time.Second,
		)
		require.NoError(t, provider.ValidateConfig())

		msg := "Authenticated delivery test"
		n := notification.NewNotification(notification.TypeInfo, notification.PriorityMedium, "", msg)
		require.NoError(t, provider.Send(ctx, n), "Send should succeed with valid credentials")

		messages, err := container.PollMessagesWithAuth(ctx, topic, testUser, testPass)
		require.NoError(t, err)
		require.Len(t, messages, 1)
		assert.Equal(t, msg, messages[0].Message)
	})

	t.Run("wrong_password", func(t *testing.T) {
		container := setupNtfyAuthContainer(t, testUser, testPass)
		host := container.GetHost(ctx)
		topic := uniqueTopic("wrong-pass")

		// Grant access to the real user so the topic exists and is accessible
		require.NoError(t, container.GrantAccess(ctx, testUser, topic, "rw"))

		// Use wrong password in the URL
		ntfyURL := shoutrrrNtfyAuthURL(testUser, "wrong", host, topic)
		provider := notification.NewShoutrrrProvider(
			"test-ntfy-auth", true, []string{ntfyURL}, nil, 30*time.Second,
		)
		require.NoError(t, provider.ValidateConfig())

		n := notification.NewNotification(notification.TypeInfo, notification.PriorityMedium, "", "Should not be delivered")
		err := provider.Send(ctx, n)
		assert.Error(t, err, "Send should fail with wrong password")
	})

	t.Run("no_credentials_denied", func(t *testing.T) {
		container := setupNtfyAuthContainer(t, testUser, testPass)
		host := container.GetHost(ctx)
		topic := uniqueTopic("no-creds")

		// Grant topic access to the real user
		require.NoError(t, container.GrantAccess(ctx, testUser, topic, "rw"))

		// No auth in URL — deny-all default should reject
		ntfyURL := fmt.Sprintf("ntfy://%s/%s?scheme=http", host, topic)
		provider := notification.NewShoutrrrProvider(
			"test-ntfy-auth", true, []string{ntfyURL}, nil, 30*time.Second,
		)
		require.NoError(t, provider.ValidateConfig())

		n := notification.NewNotification(notification.TypeInfo, notification.PriorityMedium, "", "Should be denied")
		err := provider.Send(ctx, n)
		assert.Error(t, err, "Send should fail without credentials on auth-enabled server")
	})

	t.Run("special_chars_in_password", func(t *testing.T) {
		const specialPass = "p@ss:w#rd!"

		container := setupNtfyAuthContainer(t, testUser, testPass)
		host := container.GetHost(ctx)
		topic := uniqueTopic("special-pass")

		// Add a second user with special characters in password
		const specialUser = "specialuser"
		require.NoError(t, container.AddUser(ctx, specialUser, specialPass))
		require.NoError(t, container.GrantAccess(ctx, specialUser, topic, "rw"))

		// Build URL with URL-encoded password
		ntfyURL := fmt.Sprintf("ntfy://%s:%s@%s/%s?scheme=http",
			specialUser, url.PathEscape(specialPass), host, topic)

		provider := notification.NewShoutrrrProvider(
			"test-ntfy-auth", true, []string{ntfyURL}, nil, 30*time.Second,
		)
		require.NoError(t, provider.ValidateConfig())

		msg := "Special chars password test"
		n := notification.NewNotification(notification.TypeInfo, notification.PriorityMedium, "", msg)
		require.NoError(t, provider.Send(ctx, n), "Send should succeed with URL-encoded special chars")

		messages, err := container.PollMessagesWithAuth(ctx, topic, specialUser, specialPass)
		require.NoError(t, err)
		require.Len(t, messages, 1)
		assert.Equal(t, msg, messages[0].Message)
	})
}
