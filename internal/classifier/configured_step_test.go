package classifier

import (
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/tphakala/birdnet-go/internal/conf"
	"github.com/tphakala/birdnet-go/internal/inference/bsgbat"
)

// settingsWithBSGBatStep returns settings carrying the given bsgbat.stepframes.
func settingsWithBSGBatStep(frames int) *conf.Settings {
	s := &conf.Settings{}
	s.BSGBat.StepFrames = frames
	return s
}

// TestConfiguredSpec_BSGBatStep pins the frame-to-sample conversion of the
// configured BSG-BAT step: the step stays a whole number of mel hops, reaches the
// buffer geometry and the interval exactly, and never leaks to another model.
func TestConfiguredSpec_BSGBatStep(t *testing.T) {
	t.Parallel()

	const (
		customFrames = 333
		// msPerFrame is one mel hop (768 samples) at 384 kHz.
		msPerFrame = 2 * time.Millisecond
		frameBytes = conf.NumChannels * conf.BytesPerSample
	)
	registry := ModelRegistry[RegistryIDBSGBat].Spec
	def := conf.DefaultBSGBatStepFrames

	tests := []struct {
		name       string
		settings   *conf.Settings
		wantFrames int
	}{
		{"nil settings use the default", nil, def},
		{"zero uses the default", settingsWithBSGBatStep(0), def},
		{"minimum", settingsWithBSGBatStep(conf.MinBSGBatStepFrames), conf.MinBSGBatStepFrames},
		{"default", settingsWithBSGBatStep(def), def},
		{"odd value", settingsWithBSGBatStep(customFrames), customFrames},
		{"maximum", settingsWithBSGBatStep(conf.MaxBSGBatStepFrames), conf.MaxBSGBatStepFrames},
		{"below the minimum uses the default", settingsWithBSGBatStep(conf.MinBSGBatStepFrames - 1), def},
		{"above the maximum uses the default", settingsWithBSGBatStep(conf.MaxBSGBatStepFrames + 1), def},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			wantSamples := tt.wantFrames * bsgbat.HopSize
			spec := ConfiguredSpec(RegistryIDBSGBat, registry, tt.settings)
			assert.Equal(t, wantSamples, spec.StepSamples)
			assert.Equal(t, wantSamples, BSGBatStepSamples(tt.settings))

			clipBytes, overlapBytes, readSize := spec.BufferDimensions(0)
			assert.Equal(t, registry.ClipSizeBytes(), clipBytes)
			assert.Equal(t, (bsgbat.ClipSamples-wantSamples)*frameBytes, overlapBytes)
			assert.Equal(t, wantSamples*frameBytes, readSize)
			assert.Equal(t, time.Duration(tt.wantFrames)*msPerFrame, spec.BufferInterval(0),
				"the interval is exact, with no seconds rounding")
		})
	}

	t.Run("every other model is returned unchanged", func(t *testing.T) {
		t.Parallel()
		s := settingsWithBSGBatStep(conf.MaxBSGBatStepFrames)
		for id, info := range ModelRegistry {
			if id == RegistryIDBSGBat {
				continue
			}
			assert.Equal(t, info.Spec, ConfiguredSpec(id, info.Spec, s), "spec of %q", id)
		}
	})

	t.Run("registry spec holds the default and RegistrySpec applies the setting", func(t *testing.T) {
		t.Parallel()
		assert.Equal(t, def*bsgbat.HopSize, registry.StepSamples)
		spec, ok := RegistrySpec(RegistryIDBSGBat, settingsWithBSGBatStep(conf.MaxBSGBatStepFrames))
		require.True(t, ok)
		assert.Equal(t, conf.MaxBSGBatStepFrames*bsgbat.HopSize, spec.StepSamples)
		_, ok = RegistrySpec("no-such-model", nil)
		assert.False(t, ok)
	})

	t.Run("max step equals the model window in frames", func(t *testing.T) {
		t.Parallel()
		assert.Equal(t, bsgbat.NumFrames, conf.MaxBSGBatStepFrames)
	})
}

// TestOrchestrator_ModelInfosStampsBSGBatStep pins that both accessors the
// pipeline reads report the configured step, and that the shared registry entry
// keeps the default.
func TestOrchestrator_ModelInfosStampsBSGBatStep(t *testing.T) {
	t.Parallel()

	registrySpec := ModelRegistry[RegistryIDBSGBat].Spec
	inst := &mockModelInstance{id: RegistryIDBSGBat, spec: registrySpec}
	o := newTestOrchestrator(t, inst)
	o.Settings = settingsWithBSGBatStep(conf.MaxBSGBatStepFrames)
	want := conf.MaxBSGBatStepFrames * bsgbat.HopSize

	infos := o.ModelInfos()
	require.Len(t, infos, 1)
	assert.Equal(t, want, infos[0].Spec.StepSamples)

	spec, ok := o.ModelSpecFor(RegistryIDBSGBat)
	require.True(t, ok)
	assert.Equal(t, want, spec.StepSamples)

	assert.Equal(t, conf.DefaultBSGBatStepFrames*bsgbat.HopSize, ModelRegistry[RegistryIDBSGBat].Spec.StepSamples,
		"the registry entry must not be mutated")
}
