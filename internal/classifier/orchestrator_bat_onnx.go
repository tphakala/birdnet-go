//go:build onnx

package classifier

import (
	"github.com/tphakala/birdnet-go/internal/errors"
	"github.com/tphakala/birdnet-go/internal/logger"
)

// loadBat creates and registers a bat detection model instance from settings.
func (o *Orchestrator) loadBat(threads int) error {
	log := GetLogger()
	if !o.Settings.Bat.Enabled {
		log.Debug("Bat model disabled by configuration")
		return nil
	}

	cfg := BatModelConfig{
		EmbeddingModelPath:  o.Settings.Bat.EmbeddingModel,
		EmbeddingLabels:     o.Settings.BirdNET.Labels,
		ClassifierModelPath: o.Settings.Bat.ClassifierModel,
		ClassifierLabelPath: o.Settings.Bat.LabelPath,
		ONNXRuntimePath:     o.Settings.BirdNET.ONNXRuntimePath,
		Threads:             threads,
	}

	bat, err := NewBat(&cfg)
	if err != nil {
		return errors.New(err).
			Component("classifier.orchestrator").
			Category(errors.CategoryModelInit).
			Context("model", "Bat").
			Build()
	}

	o.models[bat.ModelID()] = &modelEntry{instance: bat}

	log.Info("Bat model loaded into Orchestrator",
		logger.String("model_id", bat.ModelID()),
		logger.Int("species", bat.NumSpecies()))

	return nil
}
