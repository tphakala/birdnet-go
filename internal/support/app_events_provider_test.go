package support

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
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

// TestScrubMetadataMasksUSBSerialInDeviceChanges covers settings_saved events
// whose device change carries a usb-id token with the USB serial.
func TestScrubMetadataMasksUSBSerialInDeviceChanges(t *testing.T) {
	t.Parallel()

	p := &DatastoreAppEventsProvider{sensitiveKeys: DefaultSensitiveKeys()}
	metadata := map[string]any{
		"changes": map[string]any{
			"realtime.audio.sources.0.device": map[string]any{
				"old": "usb-id:0d8c:0014:OLD123",
				"new": "usb-id:046d:0825:NEW456",
			},
			"realtime.audio.sources.1.device": map[string]any{"old": nil, "new": "hw:1,0"},
		},
		"devices": []any{"usb-id:0d8c:0014:LIST789", "hw:2,0"},
	}

	got := p.scrubMetadata(metadata)

	changes, ok := got["changes"].(map[string]any)
	require.True(t, ok)
	assert.Equal(t, map[string]any{
		"old": "usb-id:0d8c:0014:<redacted>",
		"new": "usb-id:046d:0825:<redacted>",
	}, changes["realtime.audio.sources.0.device"])
	assert.Equal(t, map[string]any{"old": nil, "new": "hw:1,0"}, changes["realtime.audio.sources.1.device"])
	assert.Equal(t, []any{"usb-id:0d8c:0014:<redacted>", "hw:2,0"}, got["devices"])
}
