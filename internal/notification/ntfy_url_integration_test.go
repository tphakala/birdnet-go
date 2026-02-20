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

func TestNtfyURLPatterns_MatchFrontend(t *testing.T) {
	ctx := context.Background()

	// No-auth container for basic URL tests
	noAuthContainer := setupNtfyContainer(t)
	noAuthHost := noAuthContainer.GetHost(ctx)

	// Auth container for auth URL tests
	const (
		authUser    = "testuser"
		authPass    = "testpass"
		specialPass = "p@ss:w#rd"
	)

	authContainer := setupNtfyAuthContainer(t, authUser, authPass)
	authHost := authContainer.GetHost(ctx)

	// Add special-password user for the encoding test
	require.NoError(t, authContainer.AddUser(ctx, "specialpwuser", specialPass))

	tests := []struct {
		name      string
		buildURL  func(topic string) string
		host      string // which container host to use for polling
		container *containers.NtfyContainer
		authPoll  bool   // whether to poll with auth
		pollUser  string // user for auth polling
		pollPass  string // pass for auth polling
		grantUser string // user to grant access to topic
		wantErr   bool
		message   string
	}{
		{
			name: "custom_server_https_fails_http_container",
			buildURL: func(topic string) string {
				// No ?scheme=http — shoutrrr defaults to HTTPS, which will fail
				// against our HTTP-only container
				return fmt.Sprintf("ntfy://%s/%s", noAuthHost, topic)
			},
			container: noAuthContainer,
			wantErr:   true,
			message:   "This should fail because HTTPS is not available",
		},
		{
			name: "custom_server_http_works",
			buildURL: func(topic string) string {
				return fmt.Sprintf("ntfy://%s/%s?scheme=http", noAuthHost, topic)
			},
			container: noAuthContainer,
			message:   "HTTP scheme works correctly",
		},
		{
			name: "custom_server_with_basic_auth",
			buildURL: func(topic string) string {
				return fmt.Sprintf("ntfy://%s:%s@%s/%s?scheme=http",
					authUser, authPass, authHost, topic)
			},
			container: authContainer,
			authPoll:  true,
			pollUser:  authUser,
			pollPass:  authPass,
			grantUser: authUser,
			message:   "Auth delivery via frontend URL pattern",
		},
		{
			name: "url_encoded_special_chars_in_password",
			buildURL: func(topic string) string {
				return fmt.Sprintf("ntfy://specialpwuser:%s@%s/%s?scheme=http",
					url.PathEscape(specialPass), authHost, topic)
			},
			container: authContainer,
			authPoll:  true,
			pollUser:  "specialpwuser",
			pollPass:  specialPass,
			grantUser: "specialpwuser",
			message:   "Special chars password via URL encoding",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			topic := uniqueTopic(tt.name)

			// Grant topic access if needed (auth container)
			if tt.grantUser != "" {
				require.NoError(t, tt.container.GrantAccess(ctx, tt.grantUser, topic, "rw"),
					"failed to grant access for %s", tt.grantUser)
			}

			ntfyURL := tt.buildURL(topic)

			provider := notification.NewShoutrrrProvider(
				"test-url-pattern", true, []string{ntfyURL}, nil, 30*time.Second,
			)
			require.NoError(t, provider.ValidateConfig(), "ValidateConfig should succeed")

			n := notification.NewNotification(
				notification.TypeInfo,
				notification.PriorityMedium,
				"",
				tt.message,
			)

			err := provider.Send(ctx, n)

			if tt.wantErr {
				assert.Error(t, err, "Send should fail")
				return
			}

			require.NoError(t, err, "Send should succeed")

			// Poll and verify message arrived
			var messages []containers.NtfyMessage
			var pollErr error
			if tt.authPoll {
				messages, pollErr = tt.container.PollMessagesWithAuth(ctx, topic, tt.pollUser, tt.pollPass)
			} else {
				messages, pollErr = tt.container.PollMessages(ctx, topic)
			}
			require.NoError(t, pollErr, "polling messages should succeed")
			require.Len(t, messages, 1, "expected exactly one message")
			assert.Equal(t, tt.message, messages[0].Message, "message body should match")
		})
	}
}
