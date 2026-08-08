package api

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/tphakala/birdnet-go/internal/api/v2/apitest"
	"github.com/tphakala/birdnet-go/internal/conf"
)

// TestEbirdSettingsChanged verifies the eBird change detector. eBird hot-reloads
// (registry category `fresh`, reconfigure_ebird action), so any change to the
// section must be detected to trigger a live reconfigure, and identical settings
// must report no change so an unrelated save does not needlessly rebuild the client.
func TestEbirdSettingsChanged(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name    string
		mutate  func(s *conf.Settings)
		changed bool
	}{
		{
			name:    "enabled toggle is a change",
			mutate:  func(s *conf.Settings) { s.Realtime.EBird.Enabled = !s.Realtime.EBird.Enabled },
			changed: true,
		},
		{
			name:    "api key change is a change",
			mutate:  func(s *conf.Settings) { s.Realtime.EBird.APIKey = "ebird-key-changed-xyz" },
			changed: true,
		},
		{
			name:    "cache ttl change is a change",
			mutate:  func(s *conf.Settings) { s.Realtime.EBird.CacheTTL += 12 },
			changed: true,
		},
		{
			name:    "unrelated field is not an eBird change",
			mutate:  func(s *conf.Settings) { s.Realtime.Birdweather.ID = "somethingelse" },
			changed: false,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			base := apitest.NewValidTestSettings()
			updated := conf.CloneSettings(base)
			tt.mutate(updated)

			assert.Equal(t, tt.changed, ebirdSettingsChanged(base, updated),
				"detector result mismatch for %q", tt.name)
			assert.False(t, ebirdSettingsChanged(base, conf.CloneSettings(base)),
				"detector reported a change for identical settings in %q", tt.name)
		})
	}
}
