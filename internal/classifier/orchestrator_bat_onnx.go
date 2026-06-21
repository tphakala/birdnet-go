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

	// Read the live published settings snapshot once (mirroring loadPerch) rather
	// than the deprecated o.Settings pointer, so an out-of-band LoadModel after a
	// hot-reload builds against the same configuration the rest of the orchestrator
	// sees instead of a possibly-staler pointer.
	settings := o.currentSettings()

	classifierModel := settings.Bat.ClassifierModel
	labelPath := settings.Bat.LabelPath
	embeddingModel := settings.Bat.EmbeddingModel

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

	if err := checkORTOrFail(settings.BirdNET.ONNXRuntimePath, "Bat model", RegistryIDBat, "classifier.orchestrator"); err != nil {
		return err
	}

	cfg := BatModelConfig{
		EmbeddingModelPath:  embeddingModel,
		EmbeddingLabels:     settings.BirdNET.Labels,
		ClassifierModelPath: classifierModel,
		ClassifierLabelPath: labelPath,
		ONNXRuntimePath:     settings.BirdNET.ONNXRuntimePath,
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

	o.models[bat.ModelID()] = &modelEntry{instance: bat}
	o.setEmbeddingDimGauge(bat.ModelID(), bat)

	// Defer the warm-up + RSS measurement until the caller releases o.mu, so the
	// warm-up inference runs via the serialized inference path instead of stalling
	// live inference on o.mu. The entry is registered above first
	// so the drainer can find it by key.
	o.deferWarmup(bat.ModelID(), before)

	log.Info("Bat model loaded into Orchestrator",
		logger.String("model_id", bat.ModelID()),
		logger.Int("species", bat.NumSpecies()))

	return nil
}
