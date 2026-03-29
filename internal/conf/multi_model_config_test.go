package conf

import (
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestPerchConfig_Defaults(t *testing.T) {
	t.Parallel()
	settings := &Settings{}
	assert.False(t, settings.Perch.Enabled)
	assert.Empty(t, settings.Perch.ModelPath)
	assert.Empty(t, settings.Perch.LabelPath)
	assert.InDelta(t, 0.0, settings.Perch.Threshold, 0.001)
}

func TestModelsConfig_Defaults(t *testing.T) {
	t.Parallel()
	settings := &Settings{}
	assert.Empty(t, settings.Models.Enabled)
}
