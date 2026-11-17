package conf

import (
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestIsValidDenoisePresetName(t *testing.T) {
	t.Parallel()

	for _, name := range []string{DenoisePresetLight, DenoisePresetLightMedium, DenoisePresetMedium, DenoisePresetHeavy, DefaultDenoisePreset} {
		assert.Truef(t, IsValidDenoisePresetName(name), "%q", name)
	}
	for _, name := range []string{"", "Light", "light_medium", "extreme"} {
		assert.Falsef(t, IsValidDenoisePresetName(name), "%q", name)
	}
}
