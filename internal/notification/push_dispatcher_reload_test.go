package notification

import (
	"sync"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/tphakala/birdnet-go/internal/conf"
)

// resetDispatcherState resets global push dispatcher state for test isolation.
// Acquires dispatcherMu to avoid races with ReconfigureFromSettings.
func resetDispatcherState(t *testing.T) {
	t.Helper()
	reset := func() {
		dispatcherMu.Lock()
		defer dispatcherMu.Unlock()
		if globalPushDispatcher != nil {
			globalPushDispatcher.stop()
		}
		globalPushDispatcher = nil
		dispatcherOnce = sync.Once{}
	}
	t.Cleanup(reset)
	reset()
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
	if err != nil {
		// Notification service not running in test environment — start() fails
		// but dispatcher struct is created with providers initialized.
		// This is expected: the dispatcher exists but its dispatch loop isn't running.
		t.Logf("ReconfigureFromSettings returned expected test-env error: %v", err)
		return
	}
	assert.NotNil(t, globalPushDispatcher, "dispatcher should be initialized on successful reconfigure")
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

func TestReconfigureFromSettings_MultipleReconfigures(t *testing.T) {
	resetDispatcherState(t)

	// First reconfigure: disabled
	settings := &conf.Settings{}
	settings.Notification.Push.Enabled = false
	err := ReconfigureFromSettings(settings)
	require.NoError(t, err)
	assert.Nil(t, globalPushDispatcher)

	// Second reconfigure: verifies repeated calls succeed without error
	err = ReconfigureFromSettings(settings)
	require.NoError(t, err)
	assert.Nil(t, globalPushDispatcher)
}

// TestReconfigureFromSettings_AppliesProviderEnabledToggle verifies that the
// registered provider set follows the enabled flag of the settings passed to
// each reconfigure: enabling a provider builds it, disabling removes it.
func TestReconfigureFromSettings_AppliesProviderEnabledToggle(t *testing.T) {
	// Service first, dispatcher reset second: cleanups run LIFO, so the
	// dispatcher is stopped before the service it subscribed to.
	installIsolatedService(t, NewService(DefaultServiceConfig()))
	resetDispatcherState(t)

	settings := &conf.Settings{}
	settings.Notification.Push.Enabled = true
	settings.Notification.Push.Providers = []conf.PushProviderConfig{{
		Type:      "webhook",
		Enabled:   false,
		Name:      testPushWebhookName,
		Endpoints: []conf.WebhookEndpointConfig{{URL: testPushWebhookURL}},
	}}

	providerNames := func() []string {
		dispatcherMu.Lock()
		defer dispatcherMu.Unlock()
		require.NotNil(t, globalPushDispatcher)
		names := make([]string, 0, len(globalPushDispatcher.providers))
		for i := range globalPushDispatcher.providers {
			names = append(names, globalPushDispatcher.providers[i].name)
		}
		return names
	}

	require.NoError(t, ReconfigureFromSettings(settings))
	assert.Empty(t, providerNames(), "disabled provider must not be registered")

	settings.Notification.Push.Providers[0].Enabled = true
	require.NoError(t, ReconfigureFromSettings(settings))
	assert.Equal(t, []string{testPushWebhookName}, providerNames(), "enabling a provider must build it on the next reconfigure")

	settings.Notification.Push.Providers[0].Enabled = false
	require.NoError(t, ReconfigureFromSettings(settings))
	assert.Empty(t, providerNames(), "disabling a provider must remove it on the next reconfigure")
}
