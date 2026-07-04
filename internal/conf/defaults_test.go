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
