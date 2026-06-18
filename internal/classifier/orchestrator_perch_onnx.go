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
func (o *Orchestrator) buildPerch(settings *conf.Settings, threads int) (*Perch, error) {
	modelPath := settings.Perch.ModelPath
	labelPath := settings.Perch.LabelPath

	if modelPath == "" || labelPath == "" {
		m, l, _ := o.resolveInstalledPaths(RegistryIDPerchV2)
		if modelPath == "" {
			modelPath = m
		}
		if labelPath == "" {
			labelPath = l
		}
	}

	if modelPath == "" || labelPath == "" {
		return nil, errors.Newf("Perch v2 model files not installed or configured").
			Component("classifier.orchestrator").
			Category(errors.CategoryModelInit).
			Context("model", RegistryIDPerchV2).
			Build()
	}

	if err := checkORTOrFail(settings.BirdNET.ONNXRuntimePath, "Perch v2", RegistryIDPerchV2, "classifier.orchestrator"); err != nil {
		return nil, err
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
		return nil, errors.New(err).
			Component("classifier.orchestrator").
			Category(errors.CategoryModelInit).
			Context("model", RegistryIDPerchV2).
			Build()
	}

	return perch, nil
}

// loadPerch creates and registers a Perch v2 model instance from settings.
func (o *Orchestrator) loadPerch(threads int) error {
	// Capture the settings snapshot once so the recorded backend triplet matches
	// the exact configuration the instance was built against. This is what makes
	// an out-of-band runtime install (LoadModel) reconcile correctly: the entry
	// records its own triplet, so a later ReloadSecondaryModels rebuilds it only
	// when the backend/device actually changes (Forgejo #1119).
	settings := o.currentSettings()
	perch, err := o.buildPerch(settings, threads)
	if err != nil {
		return err
	}

	o.models[perch.ModelID()] = &modelEntry{
		instance: perch,
		backend:  secondaryTripletFor(settings),
	}

	// No separate Perch label resolver needed. Perch returns scientific names,
	// and the BirdNETLabelResolver (already registered) maps scientific -> common
	// for species shared between both models.

	GetLogger().Info("Perch v2 model loaded into Orchestrator",
		logger.String("model_id", perch.ModelID()),
		logger.Int("species", perch.NumSpecies()))

	return nil
}
