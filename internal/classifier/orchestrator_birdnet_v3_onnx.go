//nolint:dupl // Parallel to orchestrator_perch_onnx.go by design: each single-file secondary ONNX classifier (Perch v2, BirdNET v3.0) has its own loader file that shares this build/load/warm-up skeleton but differs in its settings fields, registry ID, config type, and constructor.
package classifier

import (
	"github.com/tphakala/birdnet-go/internal/conf"
	"github.com/tphakala/birdnet-go/internal/errors"
	"github.com/tphakala/birdnet-go/internal/logger"
)

// buildBirdNETV3 constructs a BirdNET v3.0 model instance from the given settings
// snapshot WITHOUT registering it in o.models. loadBirdNETV3 uses it for the
// initial registration; the hot-reload path (ReloadSecondaryModels) uses it
// directly so it can build the new instance on the new backend/device before
// transactionally swapping it into the existing modelEntry.
//
// The settings snapshot is passed in (rather than read inside) so the caller
// builds with the exact settings it gated the reload decision on.
//
// The path resolution is returned alongside the instance so loadBirdNETV3 can
// decide whether to repair a stale configuration without resolving a second time
// (see pathResolution). ReloadSecondaryModels discards it, which is what keeps a
// backend or device swap from rewriting the user's paths.
//
// The returned resolution is meaningful only when err == nil; every error return
// yields the zero pathResolution{}.
func (o *Orchestrator) buildBirdNETV3(settings *conf.Settings, threads int) (*BirdNETV3, pathResolution, error) {
	res := o.resolveFamilyPaths(RegistryIDBirdNETV3, modelFileSet{
		model:  settings.BirdNETV3.ModelPath,
		labels: settings.BirdNETV3.LabelPath,
	}, false)
	modelPath := res.resolved.model
	labelPath := res.resolved.labels

	if modelPath == "" || labelPath == "" {
		return nil, pathResolution{}, errors.Newf("BirdNET v3.0 model files not installed or configured").
			Component("classifier.orchestrator").
			Category(errors.CategoryModelInit).
			Context("model", RegistryIDBirdNETV3).
			Build()
	}

	if err := checkORTOrFail(settings.BirdNET.ONNXRuntimePath, "BirdNET v3.0", RegistryIDBirdNETV3, "classifier.orchestrator"); err != nil {
		return nil, pathResolution{}, err
	}

	cfg := BirdNETV3Config{
		ModelPath:       modelPath,
		LabelPath:       labelPath,
		ONNXRuntimePath: settings.BirdNET.ONNXRuntimePath,
		Threads:         threads,
		Backend:         settings.BirdNET.Backend,
		OpenVINOPath:    settings.BirdNET.OpenVINOPath,
		OpenVINODevice:  settings.BirdNET.OpenVINODevice,
	}

	model, err := NewBirdNETV3(&cfg)
	if err != nil {
		return nil, pathResolution{}, errors.New(err).
			Component("classifier.orchestrator").
			Category(errors.CategoryModelInit).
			Context("model", RegistryIDBirdNETV3).
			Build()
	}

	return model, res, nil
}

// loadBirdNETV3 creates and registers a BirdNET v3.0 model instance from settings.
// o.mu.Lock() is held by the caller.
func (o *Orchestrator) loadBirdNETV3(threads int) error {
	// Capture the settings snapshot once so the recorded backend triplet matches
	// the exact configuration the instance was built against (mirroring loadPerch).
	// This is what makes an out-of-band runtime install (LoadModel) reconcile
	// correctly: the entry records its own triplet, so a later
	// ReloadSecondaryModels rebuilds it only when the backend/device changes.
	settings := o.currentSettings()
	before := o.captureRSSBefore()
	model, res, err := o.buildBirdNETV3(settings, threads)
	if err != nil {
		return err
	}

	o.models[model.ModelID()] = &modelEntry{
		instance: model,
		backend:  secondaryTripletFor(settings),
	}
	// Queue a config repair when the model loaded from the gallery fallback
	// because the configured path was stale. Uses the resolution the build
	// already performed, so the repair can only ever persist the paths this
	// instance was actually built from. Drained after o.mu is released.
	o.queuePathCorrection(RegistryIDBirdNETV3, res)

	// Defer the warm-up + RSS measurement until the caller releases o.mu, so the
	// warm-up inference runs via the serialized inference path instead of stalling
	// live inference on o.mu. The entry is registered above first so the drainer
	// can find it by key.
	o.deferWarmup(model.ModelID(), before)

	// No separate name resolver needed. BirdNET v3.0 labels carry both the
	// scientific and common name ("Scientific name_Common name"), like BirdNET
	// v2.4, so downstream species-string parsing resolves the common name directly.

	GetLogger().Info("BirdNET v3.0 model loaded into Orchestrator",
		logger.String("model_id", model.ModelID()),
		logger.Int("species", model.NumSpecies()))

	return nil
}
