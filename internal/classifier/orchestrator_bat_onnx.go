package classifier

import (
	"github.com/tphakala/birdnet-go/internal/conf"
	"github.com/tphakala/birdnet-go/internal/errors"
	"github.com/tphakala/birdnet-go/internal/logger"
)

// buildBat constructs a Bat model instance from the given settings snapshot WITHOUT
// registering it in o.models. loadBat uses it for the initial registration; the
// hot-reload path (ReloadSecondaryModels) uses it directly so it can build the new
// instance on the new backend/device before transactionally swapping it into the
// existing modelEntry.
//
// The settings snapshot is passed in (rather than read inside) so the caller builds
// with the exact settings it gated the reload decision on.
func (o *Orchestrator) buildBat(settings *conf.Settings, threads int) (*Bat, error) {
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
		return nil, errors.Newf("bat model files not installed or configured").
			Component("classifier.orchestrator").
			Category(errors.CategoryModelInit).
			Context("model", RegistryIDBat).
			Build()
	}

	if err := checkORTOrFail(settings.BirdNET.ONNXRuntimePath, "Bat model", RegistryIDBat, "classifier.orchestrator"); err != nil {
		return nil, err
	}

	cfg := BatModelConfig{
		EmbeddingModelPath:  embeddingModel,
		EmbeddingLabels:     settings.BirdNET.Labels,
		ClassifierModelPath: classifierModel,
		ClassifierLabelPath: labelPath,
		ONNXRuntimePath:     settings.BirdNET.ONNXRuntimePath,
		Threads:             threads,
		Backend:             settings.BirdNET.Backend,
		OpenVINOPath:        settings.BirdNET.OpenVINOPath,
		OpenVINODevice:      settings.BirdNET.OpenVINODevice,
	}

	bat, err := NewBat(&cfg)
	if err != nil {
		return nil, errors.New(err).
			Component("classifier.orchestrator").
			Category(errors.CategoryModelInit).
			Context("model", RegistryIDBat).
			Build()
	}

	return bat, nil
}

// loadBat creates and registers a bat detection model instance from settings.
// o.mu.Lock() is held by the caller.
func (o *Orchestrator) loadBat(threads int) error {
	// Capture the settings snapshot once so the recorded backend triplet matches the
	// exact configuration the instance was built against (mirroring loadPerch). This
	// is what makes an out-of-band runtime install (LoadModel) reconcile correctly:
	// the entry records its own triplet, so a later ReloadSecondaryModels rebuilds it
	// only when the backend/device actually changes.
	settings := o.currentSettings()
	before := o.captureRSSBefore()

	bat, err := o.buildBat(settings, threads)
	if err != nil {
		return err
	}

	o.models[bat.ModelID()] = &modelEntry{
		instance: bat,
		backend:  secondaryTripletFor(settings),
	}
	// Defer the warm-up + RSS measurement until the caller releases o.mu, so the
	// warm-up inference runs via the serialized inference path instead of stalling
	// live inference on o.mu. The entry is registered above first
	// so the drainer can find it by key.
	o.deferWarmup(bat.ModelID(), before)

	GetLogger().Info("Bat model loaded into Orchestrator",
		logger.String("model_id", bat.ModelID()),
		logger.Int("species", bat.NumSpecies()))

	return nil
}
