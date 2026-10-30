package telemetry

import (
	"testing"
	"time"

	"github.com/getsentry/sentry-go"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/tphakala/birdnet-go/internal/conf"
)

const wiringTestDSN = "https://public@sentry.invalid/1"

// boundClientTransport returns the HTTPTransport option of the client bound to
// the global hub.
func boundClientTransport(t *testing.T) any {
	t.Helper()
	client := sentry.CurrentHub().Client()
	require.NotNil(t, client)
	t.Cleanup(func() { client.Close() })
	return client.Options().HTTPTransport
}

// TestSentryInitSitesUseDeliveryGate pins that both sentry.Init sites send
// through the shared delivery gate.
// Not parallel: it rebinds the global hub's client and changes package state.
func TestSentryInitSitesUseDeliveryGate(t *testing.T) {
	t.Setenv(sentryDSNEnvVar, wiringTestDSN)

	t.Run("main client", func(t *testing.T) {
		bindTestClient(t, nil)
		require.NoError(t, initializeSentrySDK(&conf.Settings{Version: "test"}))
		assert.Same(t, sentryDeliveryGate, boundClientTransport(t))
	})

	t.Run("minimal support client", func(t *testing.T) {
		deferredMutex.Lock()
		prevInitialized, prevUploader := sentryInitialized, attachmentUploader
		deferredMutex.Unlock()
		t.Cleanup(func() {
			deferredMutex.Lock()
			sentryInitialized, attachmentUploader = prevInitialized, prevUploader
			deferredMutex.Unlock()
		})

		bindTestClient(t, nil)
		require.NoError(t, InitMinimalSentryForSupport("test-system-id", "test-version"))
		assert.Same(t, sentryDeliveryGate, boundClientTransport(t))
	})
}

// openSharedGate suspends the shared delivery gate with a retry time far in
// the future and restores a closed gate afterwards.
func openSharedGate(t *testing.T) {
	t.Helper()
	g := sentryDeliveryGate
	t.Cleanup(func() {
		g.mu.Lock()
		defer g.mu.Unlock()
		g.state, g.consecutiveFailures, g.backoff = gateClosed, 0, deliveryInitialBackoff
	})
	g.mu.Lock()
	defer g.mu.Unlock()
	g.state = gateOpen
	g.retryAt = time.Now().Add(deliveryMaxBackoff)
}

// sharedGateWillProbe reports whether the next request through the shared
// gate is let through. The gate is open, so only a probe is.
func sharedGateWillProbe() bool {
	_, allowed := sentryDeliveryGate.admit()
	return allowed
}

// TestSupportCapturesExpediteDeliveryGate pins that a support upload probes an
// open gate at once instead of waiting out the backoff.
// Not parallel: it rebinds the global hub's client and changes the shared gate.
func TestSupportCapturesExpediteDeliveryGate(t *testing.T) {
	newClient := func(t *testing.T) {
		t.Helper()
		client, err := sentry.NewClient(sentry.ClientOptions{SampleRate: 1.0, Transport: NewMockTransport()})
		require.NoError(t, err)
		bindTestClient(t, client)
	}
	uploader := NewAttachmentUploader(true)

	t.Run("UploadSupportDump", func(t *testing.T) {
		newClient(t)
		openSharedGate(t)
		require.False(t, sharedGateWillProbe(), "setup: the gate must refuse before the upload")
		require.NoError(t, uploader.UploadSupportDump(t.Context(), []byte("dump"), "test-system-id", "", ""))
		assert.True(t, sharedGateWillProbe())
	})

	t.Run("CreateSupportEvent", func(t *testing.T) {
		newClient(t)
		openSharedGate(t)
		require.False(t, sharedGateWillProbe(), "setup: the gate must refuse before the upload")
		require.NoError(t, uploader.CreateSupportEvent(t.Context(), "test-system-id", "message", nil))
		assert.True(t, sharedGateWillProbe())
	})
}
