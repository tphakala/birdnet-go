package classifier

import (
	"github.com/tphakala/birdnet-go/internal/conf"
	"github.com/tphakala/birdnet-go/internal/errors"
	"github.com/tphakala/birdnet-go/internal/logger"
)

// bsgBatDisplayName is the model name used in load errors and logs.
const bsgBatDisplayName = "BSG-BAT"

// buildBSGBat constructs a BSG-BAT model instance from the given settings snapshot
// WITHOUT registering it in o.models. loadBSGBat uses it for the initial
// registration; the hot-reload path (ReloadSecondaryModels) uses it directly so it
// can build the new instance before transactionally swapping it into the existing
// modelEntry. BSG-BAT runs on ONNX Runtime only: the OpenVINO preference in the
// settings is ignored, and only the thread count reaches the model.
//
// The settings snapshot is passed in (rather than read inside) so the caller builds
// with the exact settings it gated the reload decision on.
//
// The path resolution is returned alongside the instance so loadBSGBat can decide
// whether to repair a stale configuration without resolving a second time (see
// pathResolution). ReloadSecondaryModels discards it, which keeps a thread-count
// change from rewriting the user's paths.
//
// The returned resolution is meaningful only when err == nil; every error return
// yields the zero pathResolution{}.
func (o *Orchestrator) buildBSGBat(settings *conf.Settings, threads int) (*BSGBat, pathResolution, error) {
	res := o.resolveFamilyPaths(RegistryIDBSGBat, modelFileSet{
		model:  settings.BSGBat.ModelPath,
		labels: settings.BSGBat.LabelPath,
	}, false)
	modelPath := res.resolved.model
	labelPath := res.resolved.labels

	if modelPath == "" || labelPath == "" {
		return nil, pathResolution{}, errors.Newf("%s model files not installed or configured", bsgBatDisplayName).
			Component("classifier.orchestrator").
			Category(errors.CategoryModelInit).
			Context("model", RegistryIDBSGBat).
			Build()
	}

	if err := checkORTOrFail(settings.BirdNET.ONNXRuntimePath, bsgBatDisplayName, RegistryIDBSGBat, "classifier.orchestrator"); err != nil {
		return nil, pathResolution{}, err
	}

	cfg := BSGBatModelConfig{
		ModelPath:       modelPath,
		LabelPath:       labelPath,
		ONNXRuntimePath: settings.BirdNET.ONNXRuntimePath,
		Threads:         threads,
	}

	bsgBat, err := NewBSGBat(&cfg)
	if err != nil {
		return nil, pathResolution{}, errors.New(err).
			Component("classifier.orchestrator").
			Category(errors.CategoryModelInit).
			Context("model", RegistryIDBSGBat).
			Build()
	}

	return bsgBat, res, nil
}

// loadBSGBat creates and registers a BSG-BAT model instance from settings.
// o.mu.Lock() is held by the caller.
func (o *Orchestrator) loadBSGBat(threads int) error {
	// Capture the settings snapshot once so the recorded backend triplet matches the
	// exact configuration the instance was built against (mirroring loadPerch).
	settings := o.currentSettings()
	before := o.captureRSSBefore()

	bsgBat, res, err := o.buildBSGBat(settings, threads)
	if err != nil {
		return err
	}

	o.models[bsgBat.ModelID()] = &modelEntry{
		instance: bsgBat,
		backend:  secondaryTripletFor(settings),
	}
	// Queue a config repair when the model loaded from the gallery fallback because
	// the configured path was stale. Drained after o.mu is released.
	o.queuePathCorrection(RegistryIDBSGBat, res)

	// Defer the warm-up and RSS measurement until the caller releases o.mu, so the
	// warm-up inference runs through the serialized inference path.
	o.deferWarmup(bsgBat.ModelID(), before)

	GetLogger().Info("BSG-BAT model loaded into Orchestrator",
		logger.String("model_id", bsgBat.ModelID()),
		logger.Int("species", bsgBat.NumSpecies()))

	return nil
}
