package classifier

import (
	"sync/atomic"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/tphakala/birdnet-go/internal/conf"
	"github.com/tphakala/birdnet-go/internal/conf/conftest"
)

// TestLoadInstalledModels_HonorsEnabledSet pins the core Phase 4 guarantee that a
// downloaded-but-disabled gallery model is NOT hot-loaded during the startup scan, while an
// enabled installed model is. It drives loadInstalledModels directly against a real
// orchestrator with an overridden Perch loader (so no real model file is needed): the gate is
// mm.orchestrator.modelIDEnabled(entry.RegistryID). Not parallel: overrides the global Perch
// loader and publishes the global settings snapshot.
func TestLoadInstalledModels_HonorsEnabledSet(t *testing.T) {
	isolateTestConfig(t)

	perchEntry, ok := GetCatalogEntry("perch-v2")
	require.True(t, ok)
	require.Equal(t, RegistryIDPerchV2, perchEntry.RegistryID)

	settings := conftest.GetTestSettings()
	settings.Models.Enabled = []string{conf.ModelIDBirdNET} // Perch NOT enabled
	conftest.SetTestSettings(settings)
	t.Cleanup(func() { conftest.SetTestSettings(nil) })

	o, err := NewOrchestrator(settings)
	require.NoError(t, err, "construction must succeed even if the embedded v2.4 is unavailable (N=0)")
	t.Cleanup(func() { o.Delete() })

	// Override the Perch loader so LoadModel registers a mock and records invocation, with no
	// real model file. Loaders write directly to o.models under the load lock.
	var perchLoaded atomic.Bool
	origLoader, hadLoader := modelLoaders[RegistryIDPerchV2]
	modelLoaders[RegistryIDPerchV2] = func(orc *Orchestrator, _ int) error {
		perchLoaded.Store(true)
		orc.models[RegistryIDPerchV2] = &modelEntry{instance: &mockModelInstance{id: RegistryIDPerchV2}}
		return nil
	}
	t.Cleanup(func() {
		if hadLoader {
			modelLoaders[RegistryIDPerchV2] = origLoader
		} else {
			delete(modelLoaders, RegistryIDPerchV2)
		}
	})

	mm := NewModelManager(t.TempDir(), o, settings)

	// Perch is installed (in installedIDs) but not enabled: the gate must skip it.
	mm.loadInstalledModels(GetLogger(), []string{"perch-v2"})
	assert.False(t, perchLoaded.Load(), "a disabled installed model must not be hot-loaded")
	assert.False(t, o.IsModelLoaded(RegistryIDPerchV2))

	// Enable Perch: the same scan now loads it.
	enabled := conftest.GetTestSettings()
	enabled.Models.Enabled = []string{conf.ModelIDBirdNET, conf.ModelIDPerchV2}
	conftest.SetTestSettings(enabled)
	mm.loadInstalledModels(GetLogger(), []string{"perch-v2"})
	assert.True(t, perchLoaded.Load(), "an enabled installed model is hot-loaded")
	assert.True(t, o.IsModelLoaded(RegistryIDPerchV2))
}

// TestScanInstalled_CapturesLegacyAutoEnableOnce pins the one-shot legacy auto-enable
// capture (model de-privilege epic, Phase 4): on the first scan of a config whose marker is
// unset, ScanInstalled reproduces the pre-Phase-4 behavior (v2.4 prepended to models.enabled)
// and records that it has run by setting the AutoEnableMigrated marker, so it never re-runs on
// a writable config. Not parallel: mutates the global settings snapshot.
func TestScanInstalled_CapturesLegacyAutoEnableOnce(t *testing.T) {
	origSettings := conf.GetSettings()
	t.Cleanup(func() { conf.StoreSettings(origSettings) })
	isolateTestConfig(t)

	settings := conftest.GetTestSettings()
	settings.Models.Enabled = []string{}       // no v2.4 named yet
	settings.Models.AutoEnableMigrated = false // capture has not run
	conf.StoreSettings(settings)

	mm := NewModelManager(t.TempDir(), nil, settings)
	mm.ScanInstalled()

	after := conf.GetSettings()
	assert.True(t, after.Models.AutoEnableMigrated, "the one-shot capture records that it has run")
	assert.Contains(t, after.Models.Enabled, conf.ModelIDBirdNET,
		"the legacy capture prepends v2.4 exactly as before Phase 4")
}

// TestScanInstalled_DoesNotReenableDisabledModel pins that once the capture marker is set, a
// user-curated models.enabled is authoritative: ScanInstalled does not re-add v2.4 (or any
// model) on a later scan, so a deliberately disabled model stays disabled. Not parallel:
// mutates the global settings snapshot.
func TestScanInstalled_DoesNotReenableDisabledModel(t *testing.T) {
	origSettings := conf.GetSettings()
	t.Cleanup(func() { conf.StoreSettings(origSettings) })
	isolateTestConfig(t)

	settings := conftest.GetTestSettings()
	settings.Models.Enabled = []string{conf.ModelIDPerchV2} // v2.4 deliberately not enabled
	settings.Models.AutoEnableMigrated = true               // capture already ran once
	conf.StoreSettings(settings)

	mm := NewModelManager(t.TempDir(), nil, settings)
	mm.ScanInstalled()

	after := conf.GetSettings()
	assert.Equal(t, []string{conf.ModelIDPerchV2}, after.Models.Enabled,
		"with the capture marker set, ScanInstalled leaves models.enabled untouched")
	assert.True(t, after.Models.AutoEnableMigrated, "the marker stays set")
}

// TestScanInstalled_ReRunsCaptureInMemoryWhenMarkerNotPersisted pins the read-only-config
// behavior: when the marker cannot be persisted (a read-only mount), the capture re-runs in
// memory on the next scan, reproducing the pre-Phase-4 behavior of enabling v2.4 every start.
// Simulated here by leaving the marker unset on the second scan. Not parallel.
func TestScanInstalled_ReRunsCaptureInMemoryWhenMarkerNotPersisted(t *testing.T) {
	origSettings := conf.GetSettings()
	t.Cleanup(func() { conf.StoreSettings(origSettings) })
	isolateTestConfig(t)

	settings := conftest.GetTestSettings()
	settings.Models.Enabled = []string{}
	settings.Models.AutoEnableMigrated = false
	conf.StoreSettings(settings)

	mm := NewModelManager(t.TempDir(), nil, settings)
	mm.ScanInstalled()
	require.Contains(t, conf.GetSettings().Models.Enabled, conf.ModelIDBirdNET)

	// Simulate a read-only start where the marker never landed: reset it and re-scan.
	reset := conf.CloneSettings(conf.GetSettings())
	reset.Models.Enabled = []string{}
	reset.Models.AutoEnableMigrated = false
	conf.StoreSettings(reset)

	mm.ScanInstalled()
	assert.Contains(t, conf.GetSettings().Models.Enabled, conf.ModelIDBirdNET,
		"the capture re-runs in memory when the marker was not persisted")
}
