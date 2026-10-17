package conf

import (
	"testing"

	"github.com/spf13/viper"
	"github.com/stretchr/testify/require"
)

func TestImportElevationDefaultsOn(t *testing.T) {
	t.Cleanup(viper.Reset)
	viper.Reset()
	setDefaultConfig()
	require.True(t, viper.GetBool("import.allowInAppElevation"),
		"in-app elevation must default to enabled")
}

func TestEqualizerDefaultsShipBypassed(t *testing.T) {
	t.Cleanup(viper.Reset)
	viper.Reset()
	setDefaultConfig()
	require.False(t, viper.GetBool("realtime.audio.equalizer.enabled"))

	var filters []EqualizerFilter
	require.NoError(t, viper.UnmarshalKey("realtime.audio.equalizer.filters", &filters))
	require.Len(t, filters, 2)
	for _, f := range filters {
		require.Equal(t, 0, f.Passes, "%s ships with passes 0, which turns the filter off", f.Type)
	}
}
