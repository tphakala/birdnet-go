//go:build onnx

package classifier

import (
	"github.com/tphakala/birdnet-go/internal/errors"
	"github.com/tphakala/birdnet-go/internal/logger"
)

// loadBat creates and registers a bat detection model instance from settings.
func (o *Orchestrator) loadBat(threads int) error {
	log := GetLogger()

	classifierModel := o.Settings.Bat.ClassifierModel
	labelPath := o.Settings.Bat.LabelPath
	embeddingModel := o.Settings.Bat.EmbeddingModel

	if classifierModel == "" || labelPath == "" || embeddingModel == "" {
		m, l, e := o.resolveInstalledPaths(RegistryIDBat)
		if classifierModel == "" {
			classifierModel = m
		}
		if labelPath == "" {
			labelPath = l
		}
		if embeddingModel == "" {
			embeddingModel = e
		}
	}

	cfg := BatModelConfig{
		EmbeddingModelPath:  embeddingModel,
		EmbeddingLabels:     o.Settings.BirdNET.Labels,
		ClassifierModelPath: classifierModel,
		ClassifierLabelPath: labelPath,
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
