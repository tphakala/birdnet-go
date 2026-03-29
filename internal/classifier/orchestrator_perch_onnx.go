//go:build onnx

package classifier

import (
	"github.com/tphakala/birdnet-go/internal/errors"
	"github.com/tphakala/birdnet-go/internal/logger"
)

// loadPerch creates and registers a Perch v2 model instance from settings.
func (o *Orchestrator) loadPerch(threads int) error {
	log := GetLogger()
	cfg := PerchConfig{
		ModelPath:       o.Settings.Perch.ModelPath,
		LabelPath:       o.Settings.Perch.LabelPath,
		ONNXRuntimePath: o.Settings.BirdNET.ONNXRuntimePath,
		Threads:         threads,
	}

	perch, err := NewPerch(cfg)
	if err != nil {
		return errors.New(err).
			Component("classifier.orchestrator").
			Category(errors.CategoryModelInit).
			Context("model", "Perch_V2").
			Build()
	}

	o.models[perch.ModelID()] = &modelEntry{instance: perch}

	// No separate Perch label resolver needed. Perch returns scientific names,
	// and the BirdNETLabelResolver (already registered) maps scientific -> common
	// for species shared between both models.

	log.Info("Perch v2 model loaded into Orchestrator",
		logger.String("model_id", perch.ModelID()),
		logger.Int("species", perch.NumSpecies()))

	return nil
}
