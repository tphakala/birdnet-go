//nolint:dupl // Parallel to orchestrator_birdnet_v3_onnx.go by design: each single-file secondary ONNX classifier (Perch v2, BirdNET v3.0) has its own loader file that shares this build/load/warm-up skeleton but differs in its settings fields, registry ID, config type, and constructor.
package classifier

import (
	"github.com/tphakala/birdnet-go/internal/conf"
	"github.com/tphakala/birdnet-go/internal/errors"
	"github.com/tphakala/birdnet-go/internal/logger"
)

// buildPerch constructs a Perch v2 model instance from the given settings
// snapshot WITHOUT registering it in o.models. loadPerch uses it for the
// initial registration; the hot-reload path (ReloadSecondaryModels) uses it
// directly so it can build the new instance on the new backend/device before
// transactionally swapping it into the existing modelEntry.
//
// The settings snapshot is passed in (rather than read inside) so the caller
// builds with the exact settings it gated the reload decision on.
//
// The path resolution is returned alongside the instance so loadPerch can decide
// whether to repair a stale configuration without resolving a second time (see
// pathResolution). ReloadSecondaryModels discards it, which is what keeps a
// backend or device swap from rewriting the user's paths.
//
// The returned resolution is meaningful only when err == nil; every error return
// yields the zero pathResolution{}.
func (o *Orchestrator) buildPerch(settings *conf.Settings, threads int) (*Perch, pathResolution, error) {
	res := o.resolveFamilyPaths(RegistryIDPerchV2, modelFileSet{
		model:  settings.Perch.ModelPath,
		labels: settings.Perch.LabelPath,
	}, false)
	modelPath := res.resolved.model
	labelPath := res.resolved.labels

	if modelPath == "" || labelPath == "" {
		return nil, pathResolution{}, errors.Newf("Perch v2 model files not installed or configured").
			Component("classifier.orchestrator").
			Category(errors.CategoryModelInit).
			Context("model", RegistryIDPerchV2).
			Build()
	}

	if err := checkORTOrFail(settings.BirdNET.ONNXRuntimePath, "Perch v2", RegistryIDPerchV2, "classifier.orchestrator"); err != nil {
		return nil, pathResolution{}, err
	}

	cfg := PerchConfig{
		ModelPath:       modelPath,
		LabelPath:       labelPath,
		ONNXRuntimePath: settings.BirdNET.ONNXRuntimePath,
		Threads:         threads,
		Backend:         settings.BirdNET.Backend,
		OpenVINOPath:    settings.BirdNET.OpenVINOPath,
		OpenVINODevice:  settings.BirdNET.OpenVINODevice,
	}

	perch, err := NewPerch(&cfg)
	if err != nil {
		return nil, pathResolution{}, errors.New(err).
			Component("classifier.orchestrator").
			Category(errors.CategoryModelInit).
			Context("model", RegistryIDPerchV2).
			Build()
	}

	return perch, res, nil
}

// loadPerch creates and registers a Perch v2 model instance from settings.
// o.mu.Lock() is held by the caller.
func (o *Orchestrator) loadPerch(threads int) error {
	// Capture the settings snapshot once so the recorded backend triplet matches
	// the exact configuration the instance was built against. This is what makes
	// an out-of-band runtime install (LoadModel) reconcile correctly: the entry
	// records its own triplet, so a later ReloadSecondaryModels rebuilds it only
	// when the backend/device actually changes (Forgejo #1119).
	settings := o.currentSettings()
	before := o.captureRSSBefore()
	perch, res, err := o.buildPerch(settings, threads)
	if err != nil {
		return err
	}

	o.models[perch.ModelID()] = &modelEntry{
		instance: perch,
		backend:  secondaryTripletFor(settings),
	}
	// Queue a config repair when the model loaded from the gallery fallback
	// because the configured path was stale. Uses the resolution the build
	// already performed, so the repair can only ever persist the paths this
	// instance was actually built from. Drained after o.mu is released.
	o.queuePathCorrection(RegistryIDPerchV2, res)

	// Defer the warm-up + RSS measurement until the caller releases o.mu, so the
	// warm-up inference runs via the serialized inference path instead of stalling
	// live inference on o.mu. The entry is registered above first
	// so the drainer can find it by key.
	o.deferWarmup(perch.ModelID(), before)

	// No separate Perch label resolver needed. Perch returns scientific names,
	// and the BirdNETLabelResolver (already registered) maps scientific -> common
	// for species shared between both models.

	GetLogger().Info("Perch v2 model loaded into Orchestrator",
		logger.String("model_id", perch.ModelID()),
		logger.Int("species", perch.NumSpecies()))

	return nil
}
