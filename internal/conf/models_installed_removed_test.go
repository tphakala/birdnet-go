package conf

import (
	"strings"
	"testing"

	"github.com/spf13/viper"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestLoad_ToleratesRemovedModelsInstalledKey verifies that a config written before the
// models.installed key was removed still loads: Load decodes with viper.Unmarshal
// (mapstructure without ErrorUnused), which silently ignores the now-unknown key rather than
// failing, so no migration or deprecated-field shim is needed. Uses the same unmarshal call
// and decode hook as storage.Load.
func TestLoad_ToleratesRemovedModelsInstalledKey(t *testing.T) {
	t.Parallel()

	const legacyYAML = "models:\n  enabled:\n    - birdnet\n  installed:\n    - birdnet\n    - perch_v2\n"

	v := viper.New()
	v.SetConfigType("yaml")
	require.NoError(t, v.ReadConfig(strings.NewReader(legacyYAML)))

	var s Settings
	require.NoError(t, v.Unmarshal(&s, viper.DecodeHook(DurationDecodeHook())),
		"a config carrying the removed models.installed key must still decode")
	assert.Equal(t, []string{"birdnet"}, s.Models.Enabled, "known keys still decode after the field removal")
}
