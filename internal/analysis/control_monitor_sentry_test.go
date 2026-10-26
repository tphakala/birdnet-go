package analysis

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/getsentry/sentry-go"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/tphakala/birdnet-go/internal/conf"
	"github.com/tphakala/birdnet-go/internal/telemetry"
)

// sentryDSNEnv is the environment variable telemetry reads the build DSN from
// (the telemetry package keeps its own constant unexported).
const sentryDSNEnv = "BIRDNET_GO_SENTRY_DSN"

// TestHandleControlSignal_ReconfigureSentry pins that the reconfigure_sentry
// control signal binds a Sentry client when Error Tracking is enabled and
// unbinds it when disabled, without a restart.
func TestHandleControlSignal_ReconfigureSentry(t *testing.T) {
	// Not parallel: it changes the global settings, hub and environment.
	sink := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))
	t.Cleanup(sink.Close)
	t.Setenv(sentryDSNEnv, "http://public@"+strings.TrimPrefix(sink.URL, "http://")+"/1")

	hub := sentry.CurrentHub()
	originalClient := hub.Client()
	originalSettings := conf.GetSettings()
	t.Cleanup(func() {
		conf.StoreSettings(&conf.Settings{Version: "test", Sentry: conf.SentrySettings{Enabled: false}})
		assert.NoError(t, telemetry.ReconfigureSentry())
		conf.StoreSettings(originalSettings)
		hub.BindClient(originalClient)
		telemetry.UpdateTelemetryEnabled()
	})

	cm := &ControlMonitor{}

	conf.StoreSettings(&conf.Settings{Version: "test", Sentry: conf.SentrySettings{Enabled: true}})
	cm.handleControlSignal(telemetry.SignalReconfigureSentry)
	require.NotNil(t, hub.Client())
	assert.True(t, hub.Client().IsEnabled(), "enabling must bind a Sentry client")

	conf.StoreSettings(&conf.Settings{Version: "test", Sentry: conf.SentrySettings{Enabled: false}})
	cm.handleControlSignal(telemetry.SignalReconfigureSentry)
	assert.False(t, hub.Client().IsEnabled(), "disabling must unbind the Sentry client")
}
