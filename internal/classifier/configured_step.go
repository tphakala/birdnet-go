package classifier

import (
	"github.com/tphakala/birdnet-go/internal/conf"
	"github.com/tphakala/birdnet-go/internal/inference/bsgbat"
)

// BSGBatStepSamples returns the configured BSG-BAT analysis step in samples at
// 384 kHz: bsgbat.stepframes whole mel hops of bsgbat.HopSize samples. An unset
// (zero) or out-of-range value, or nil settings, resolves to
// conf.DefaultBSGBatStepFrames. The step is never routed through seconds, so it
// stays on the model's hop grid.
func BSGBatStepSamples(s *conf.Settings) int {
	frames := conf.DefaultBSGBatStepFrames
	if s != nil && conf.ValidBSGBatStepFrames(s.BSGBat.StepFrames) {
		frames = s.BSGBat.StepFrames
	}
	return frames * bsgbat.HopSize
}

// ConfiguredSpec applies the settings that override a registry spec. For BSG-BAT
// it replaces StepSamples with the configured step (BSGBatStepSamples); every
// other model is returned unchanged. It is keyed by registry ID because the
// setting lives in the model's own section, like its confidence threshold.
func ConfiguredSpec(modelID string, spec ModelSpec, s *conf.Settings) ModelSpec {
	if modelID == RegistryIDBSGBat {
		spec.StepSamples = BSGBatStepSamples(s)
	}
	return spec
}

// RegistrySpec returns the registry spec of a model with the settings applied
// (ConfiguredSpec). Readers of the analysis step outside the orchestrator use it
// instead of reading ModelRegistry directly, whose StepSamples is the default.
func RegistrySpec(modelID string, s *conf.Settings) (ModelSpec, bool) {
	spec, ok := GetModelSpec(modelID)
	if !ok {
		return ModelSpec{}, false
	}
	return ConfiguredSpec(modelID, spec, s), true
}
