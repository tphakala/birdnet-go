package classifier

import (
	"github.com/tphakala/birdnet-go/internal/errors"
	"github.com/tphakala/birdnet-go/internal/logger"
)

// loadBat creates and registers a bat detection model instance from settings.
// o.mu.Lock() is held by the caller.
func (o *Orchestrator) loadBat(threads int) error {
	log := GetLogger()
	before := o.captureRSSBefore()

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

	if classifierModel == "" || labelPath == "" || embeddingModel == "" {
		return errors.Newf("bat model files not installed or configured").
			Component("classifier.orchestrator").
			Category(errors.CategoryModelInit).
			Context("model", RegistryIDBat).
			Build()
	}

	if err := checkORTOrFail(o.Settings.BirdNET.ONNXRuntimePath, "Bat model", RegistryIDBat, "classifier.orchestrator"); err != nil {
		return err
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
			Context("model", RegistryIDBat).
			Build()
	}

	o.warmupAndRecordRSS(bat.ModelID(), before, bat)
	o.models[bat.ModelID()] = &modelEntry{instance: bat}

	log.Info("Bat model loaded into Orchestrator",
		logger.String("model_id", bat.ModelID()),
		logger.Int("species", bat.NumSpecies()))

	return nil
}
