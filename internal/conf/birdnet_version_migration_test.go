package conf

import (
	"slices"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestMigrateBirdNETVersion covers the birdnet.version retirement migration
// (model de-privilege epic, Phase 3). The classifier no longer reads birdnet.version; the
// migration clears the field and, for the previously-unstartable "3.0" value,
// enables the v3.0 model so the config keeps working instead of failing at
// startup.
func TestMigrateBirdNETVersion(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name        string
		version     string
		enabled     []string
		wantChanged bool
		wantVersion string
		wantEnabled []string
	}{
		{
			name:        "empty version is a no-op",
			version:     "",
			enabled:     []string{ModelIDBirdNET},
			wantChanged: false,
			wantVersion: "",
			wantEnabled: []string{ModelIDBirdNET},
		},
		{
			name:        "2.4 clears the field without touching enabled",
			version:     "2.4",
			enabled:     []string{ModelIDBirdNET},
			wantChanged: true,
			wantVersion: "",
			wantEnabled: []string{ModelIDBirdNET},
		},
		{
			name:        "3.0 enables the v3.0 model and clears the field",
			version:     "3.0",
			enabled:     []string{ModelIDBirdNET},
			wantChanged: true,
			wantVersion: "",
			wantEnabled: []string{ModelIDBirdNET, ModelIDBirdNETV3},
		},
		{
			name:        "3.0 does not duplicate an already-enabled v3.0",
			version:     "3.0",
			enabled:     []string{ModelIDBirdNET, ModelIDBirdNETV3},
			wantChanged: true,
			wantVersion: "",
			wantEnabled: []string{ModelIDBirdNET, ModelIDBirdNETV3},
		},
		{
			name:        "3.0 with a nil enabled list still enables v3.0",
			version:     "3.0",
			enabled:     nil,
			wantChanged: true,
			wantVersion: "",
			wantEnabled: []string{ModelIDBirdNETV3},
		},
		{
			name:        "unknown version clears the field without touching enabled",
			version:     "9.9",
			enabled:     []string{ModelIDBirdNET},
			wantChanged: true,
			wantVersion: "",
			wantEnabled: []string{ModelIDBirdNET},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			s := &Settings{}
			s.BirdNET.Version = tt.version
			s.Models.Enabled = slices.Clone(tt.enabled)

			changed := s.MigrateBirdNETVersion()

			assert.Equal(t, tt.wantChanged, changed, "changed return value")
			assert.Equal(t, tt.wantVersion, s.BirdNET.Version, "birdnet.version after migration")
			assert.Equal(t, tt.wantEnabled, s.Models.Enabled, "models.enabled after migration")
		})
	}
}

// TestMigrateBirdNETVersion_Idempotent verifies a second run is a no-op once the
// field has been cleared, so repeated Load() calls do not re-persist.
func TestMigrateBirdNETVersion_Idempotent(t *testing.T) {
	t.Parallel()

	s := &Settings{}
	s.BirdNET.Version = "3.0"
	s.Models.Enabled = []string{ModelIDBirdNET}

	require.True(t, s.MigrateBirdNETVersion(), "first run should report a change")
	assert.Equal(t, []string{ModelIDBirdNET, ModelIDBirdNETV3}, s.Models.Enabled)

	// Second run: version already cleared, nothing left to do.
	assert.False(t, s.MigrateBirdNETVersion(), "second run should be a no-op")
	assert.Equal(t, []string{ModelIDBirdNET, ModelIDBirdNETV3}, s.Models.Enabled)
}
