package classifier

import (
	"github.com/tphakala/birdnet-go/internal/conf"
	"github.com/tphakala/birdnet-go/internal/logger"
)

// buildBirdNETV24 constructs (without registering) a BirdNET v2.4 instance from
// the given settings. NewBirdNET writes BirdNET.Labels onto that settings object,
// so the caller decides whether it is the published snapshot (startup load) or a
// private clone. The model path is resolved through o.resolvePrimaryModelPath.
//
// v2.4 derives its thread count from settings.BirdNET.Threads inside NewBirdNET, so
// there is no separate threads argument: computeThreadAllocation hands every model
// the full budget, which for v2.4 already equals settings.BirdNET.Threads.
func (o *Orchestrator) buildBirdNETV24(settings *conf.Settings) (*BirdNET, pathResolution, error) {
	bn, err := NewBirdNET(settings, nil, o.resolvePrimaryModelPath)
	if err != nil {
		return nil, pathResolution{}, err
	}
	return bn, bn.primaryPath, nil
}

// loadBirdNETV24 loads the built-in BirdNET v2.4 model into o.models under
// RegistryIDBirdNETV24, following the same build/register/defer-warm-up shape as
// the secondary loaders (loadPerch). v2.4 is the built-in model but is loaded only when
// models.enabled names it (authoritative since Phase 4); it is not implicitly enabled. Its
// load failure is non-fatal: loadEnabledModels records it and continues, so construction
// succeeds degraded at N=0 (handled by loadEnabledModels). The thread
// count is unused: v2.4 takes it from settings.BirdNET.Threads inside NewBirdNET,
// so the parameter exists only to satisfy the shared modelLoaders signature.
func (o *Orchestrator) loadBirdNETV24(_ int) error {
	settings := o.currentSettings()

	// Startup passes the published settings UNCLONED so construction-time loadLabels
	// writes BirdNET.Labels into the published snapshot, the historical behavior
	// every downstream reader of settings.BirdNET.Labels relies on. A post-publish
	// load (test-only in Phase 3) clones first so a concurrent reader never observes
	// loadLabels mutate the live object, then republishes the populated clone via
	// updateSettings (LoadModel has no updateSettings step of its own, unlike the
	// reload path).
	cloned := o.published.Load()
	if cloned {
		settings = conf.CloneSettings(settings)
	}

	before := o.captureRSSBefore()
	bn, res, err := o.buildBirdNETV24(settings)
	if err != nil {
		return err
	}

	o.models[RegistryIDBirdNETV24] = &modelEntry{instance: bn}
	o.queuePathCorrection(RegistryIDBirdNETV24, res)
	if cloned {
		o.updateSettings(settings)
	}

	// Defer the warm-up + RSS measurement until the caller releases o.mu, so the
	// warm-up inference runs via the serialized inference path (see loadPerch).
	o.deferWarmup(RegistryIDBirdNETV24, before)

	GetLogger().Info("BirdNET v2.4 model loaded into Orchestrator",
		logger.String("model_id", RegistryIDBirdNETV24),
		logger.Int("species", bn.NumSpecies()))

	return nil
}
