package support

import (
	"testing"

	"github.com/stretchr/testify/assert"
)

// TestScrubMetadataRedactsSecurityAddressListChanges covers settings_saved
// events written before the address list keys were sensitive.
func TestScrubMetadataRedactsSecurityAddressListChanges(t *testing.T) {
	t.Parallel()

	p := &DatastoreAppEventsProvider{sensitiveKeys: DefaultSensitiveKeys()}
	metadata := map[string]any{
		"source": "ui",
		"changes": map[string]any{
			"security.trustedproxies.0":          map[string]any{"old": "192.0.2.1", "new": "198.51.100.2"},
			"security.plainhttpsessionclients.0": map[string]any{"old": nil, "new": "172.18.0.5"},
			"realtime.interval":                  map[string]any{"old": 15, "new": 30},
		},
	}

	got := p.scrubMetadata(metadata)

	changes, ok := got["changes"].(map[string]any)
	assert.True(t, ok)
	assert.Equal(t, "[redacted]", changes["security.trustedproxies.0"])
	assert.Equal(t, "[redacted]", changes["security.plainhttpsessionclients.0"])
	assert.Equal(t, map[string]any{"old": 15, "new": 30}, changes["realtime.interval"])
}
