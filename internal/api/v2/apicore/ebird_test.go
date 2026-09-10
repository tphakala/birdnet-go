package apicore

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/tphakala/birdnet-go/internal/conf"
)

// ebirdTestSettings builds a minimal settings snapshot with the eBird section
// configured as requested. CacheTTL is fixed so it does not influence results.
func ebirdTestSettings(enabled bool, apiKey string) *conf.Settings {
	s := &conf.Settings{}
	s.Realtime.EBird.Enabled = enabled
	s.Realtime.EBird.APIKey = apiKey
	s.Realtime.EBird.CacheTTL = 24
	return s
}

// TestBuildEBirdClient covers the three construction outcomes: disabled and
// misconfigured both yield nil (eBird stays unavailable), while an enabled
// integration with an API key yields a client.
func TestBuildEBirdClient(t *testing.T) {
	t.Parallel()
	c := &Core{}
	tests := []struct {
		name     string
		settings *conf.Settings
		wantNil  bool
	}{
		{"disabled returns nil", ebirdTestSettings(false, "test-api-key"), true},
		{"enabled without api key returns nil", ebirdTestSettings(true, ""), true},
		{"enabled with api key returns client", ebirdTestSettings(true, "test-api-key"), false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			got := c.buildEBirdClient(tt.settings)
			if tt.wantNil {
				assert.Nil(t, got)
			} else {
				assert.NotNil(t, got)
			}
		})
	}
}

// TestCoreEBirdAccessor verifies the atomic accessor: nil on a zero-value Core,
// returns the stored client, and reflects a stored nil.
func TestCoreEBirdAccessor(t *testing.T) {
	t.Parallel()
	c := &Core{}
	assert.Nil(t, c.EBird(), "EBird() must be nil on a zero-value Core")

	client := c.buildEBirdClient(ebirdTestSettings(true, "test-api-key"))
	require.NotNil(t, client)
	c.eBirdClient.Store(client)
	assert.Same(t, client, c.EBird(), "EBird() must return the stored client")

	c.eBirdClient.Store(nil)
	assert.Nil(t, c.EBird(), "EBird() must be nil after storing nil")
}

// TestReconfigureEBird exercises the live-reconfigure path end to end against the
// global settings singleton: enabling builds a client, changing the key rebuilds
// it, and disabling clears it. This is the behaviour that makes eBird settings
// take effect without a restart (issue #4102).
func TestReconfigureEBird(t *testing.T) {
	// Not parallel: mutates the global conf singleton via conf.StoreSettings.
	// Snapshot with GetSettings (reads the published pointer) rather than Setting
	// (which can lazily load from disk and add I/O and log noise to a unit test).
	orig := conf.GetSettings()
	t.Cleanup(func() { conf.StoreSettings(orig) })

	c := &Core{}

	// Enabling eBird with a key makes the client available live. ReconfigureEBird
	// returns the newly built client, which must be the one EBird() then serves.
	conf.StoreSettings(ebirdTestSettings(true, "test-api-key"))
	first := c.ReconfigureEBird()
	require.NotNil(t, first, "eBird client must be built after enabling with a key")
	assert.Same(t, first, c.EBird(), "ReconfigureEBird must store the client it returns")

	// Changing the API key rebuilds the client as a new instance.
	conf.StoreSettings(ebirdTestSettings(true, "different-key"))
	second := c.ReconfigureEBird()
	require.NotNil(t, second)
	assert.NotSame(t, first, second, "changing eBird settings must rebuild the client")

	// Disabling clears the client and returns nil.
	conf.StoreSettings(ebirdTestSettings(false, ""))
	assert.Nil(t, c.ReconfigureEBird(), "disabling eBird must return nil")
	assert.Nil(t, c.EBird(), "disabling eBird must clear the client")
}
