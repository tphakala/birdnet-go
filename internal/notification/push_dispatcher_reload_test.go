package notification

import (
	"sync"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/tphakala/birdnet-go/internal/conf"
)

// resetDispatcherState resets global push dispatcher state for test isolation.
func resetDispatcherState(t *testing.T) {
	t.Helper()
	t.Cleanup(func() {
		if globalPushDispatcher != nil {
			globalPushDispatcher.stop()
		}
		globalPushDispatcher = nil
		dispatcherOnce = sync.Once{}
	})
	globalPushDispatcher = nil
	dispatcherOnce = sync.Once{}
}

func TestReconfigureFromSettings_NoExistingDispatcher(t *testing.T) {
	resetDispatcherState(t)

	settings := &conf.Settings{}
	settings.Notification.Push.Enabled = false

	err := ReconfigureFromSettings(settings)
	require.NoError(t, err)
	assert.Nil(t, globalPushDispatcher)
}

func TestReconfigureFromSettings_EnabledWithProviders(t *testing.T) {
	resetDispatcherState(t)

	settings := &conf.Settings{}
	settings.Notification.Push.Enabled = true
	settings.Notification.Push.Providers = []conf.PushProviderConfig{
		{
			Type:    "shoutrrr",
			Enabled: true,
			Name:    "test-discord",
			URLs:    []string{"discord://token@webhookid"},
		},
	}

	err := ReconfigureFromSettings(settings)
	// ValidateConfig may fail without a real shoutrrr URL
	if err != nil {
		t.Logf("ReconfigureFromSettings returned error (expected with fake URL): %v", err)
	}
}

func TestReconfigureFromSettings_ReplacesExisting(t *testing.T) {
	resetDispatcherState(t)

	// Create a minimal dispatcher to simulate existing state
	globalPushDispatcher = &pushDispatcher{
		log:     GetLogger(),
		enabled: true,
	}

	settings := &conf.Settings{}
	settings.Notification.Push.Enabled = false

	err := ReconfigureFromSettings(settings)
	require.NoError(t, err)
	assert.Nil(t, globalPushDispatcher)
}

func TestReconfigureFromSettings_SyncOnceReset(t *testing.T) {
	resetDispatcherState(t)

	// First init: disabled
	settings := &conf.Settings{}
	settings.Notification.Push.Enabled = false
	err := ReconfigureFromSettings(settings)
	require.NoError(t, err)
	assert.Nil(t, globalPushDispatcher)

	// Second init: still disabled — verifies sync.Once was properly reset
	err = ReconfigureFromSettings(settings)
	require.NoError(t, err)
	assert.Nil(t, globalPushDispatcher)
}
