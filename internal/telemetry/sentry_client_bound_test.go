package telemetry

import (
	"testing"

	"github.com/getsentry/sentry-go"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/tphakala/birdnet-go/internal/conf"
)

// bindTestClient rebinds the global hub's client for one test and restores the
// original client afterwards. A nil client leaves the hub unbound, which
// sentry-go represents with a shared, disabled no-op client rather than nil.
func bindTestClient(t *testing.T, client *sentry.Client) {
	t.Helper()
	hub := sentry.CurrentHub()
	original := hub.Client()
	t.Cleanup(func() { hub.BindClient(original) })
	hub.BindClient(client)
}

// newTestClient returns an enabled client that sends nothing.
func newTestClient(t *testing.T) *sentry.Client {
	t.Helper()
	client, err := sentry.NewClient(sentry.ClientOptions{Transport: NewMockTransport()})
	require.NoError(t, err)
	return client
}

// TestSentryClientBound pins the check InitMinimalSentryForSupport and
// shouldSkipTelemetry rely on: a hub with no initialized client must not count
// as bound, even though sentry-go returns a non-nil no-op client for it.
// Not parallel: it rebinds the global hub's client.
func TestSentryClientBound(t *testing.T) {
	bindTestClient(t, nil)
	assert.NotNil(t, sentry.CurrentHub().Client(), "sentry-go binds a no-op client in place of nil")
	assert.False(t, sentryClientBound(), "an uninitialized hub must not count as bound")

	bindTestClient(t, newTestClient(t))
	assert.True(t, sentryClientBound(), "an initialized client must count as bound")
}

// TestShouldSkipTelemetryWithoutClient covers an opted-in install whose build
// has no DSN: no client is bound, so telemetry must be skipped.
// Not parallel: it changes the global hub, test mode and settings.
func TestShouldSkipTelemetryWithoutClient(t *testing.T) {
	prevMode := testMode.Load()
	t.Cleanup(func() { testMode.Store(prevMode) })
	testMode.Store(testModeDisabled)

	prevSettings := conf.GetSettings()
	t.Cleanup(func() { conf.StoreSettings(prevSettings) })
	conf.StoreSettings(&conf.Settings{Sentry: conf.SentrySettings{Enabled: true}})

	bindTestClient(t, nil)
	assert.True(t, shouldSkipTelemetry(), "opted in without a bound client must skip")

	bindTestClient(t, newTestClient(t))
	assert.False(t, shouldSkipTelemetry(), "opted in with a bound client must not skip")
}

// TestInitMinimalSentryForSupportUnboundHub checks that an unbound hub does not
// count as an initialized SDK, so a support upload with telemetry off goes on
// to initialize Sentry instead of returning early. Without a DSN that attempt
// reports the missing DSN.
// Not parallel: it changes the global hub and the build DSN.
func TestInitMinimalSentryForSupportUnboundHub(t *testing.T) {
	t.Setenv(sentryDSNEnvVar, "")
	prevDSN := sentryDSN
	t.Cleanup(func() { sentryDSN = prevDSN })
	sentryDSN = ""

	bindTestClient(t, nil)
	err := InitMinimalSentryForSupport("test-system-id", "test-version")
	require.Error(t, err, "an unbound hub must not be reused as an initialized SDK")
	assert.Contains(t, err.Error(), "DSN not configured")

	bindTestClient(t, newTestClient(t))
	assert.NoError(t, InitMinimalSentryForSupport("test-system-id", "test-version"),
		"an initialized client is reused")
}
