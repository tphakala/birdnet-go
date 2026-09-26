package classifier

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/tphakala/birdnet-go/internal/conf"
	"github.com/tphakala/birdnet-go/internal/conf/conftest"
)

// TestApplyConfigForVariantSwap_PrimaryWritesModelPathOnly pins the "model only"
// rule that used to live in familyFields and now lives in the swap helper: the
// family's model field is persisted and nothing else, so a user's custom
// BirdNET.LabelPath survives a swap and a revert. An empty model path reverts to the
// embedded baseline (the DFT->baseline path in replaceVariant).
func TestApplyConfigForVariantSwap_PrimaryWritesModelPathOnly(t *testing.T) {
	// Not parallel: mutates global settings via conf.StoreSettings.
	origSettings := conf.GetSettings()
	t.Cleanup(func() { conf.StoreSettings(origSettings) })
	isolateTestConfig(t)

	settings := conftest.GetTestSettings()
	settings.BirdNET.LabelPath = "/srv/custom.txt"
	conf.StoreSettings(settings)
	mm := NewModelManager(t.TempDir(), nil, settings)

	mm.applyConfigForVariantSwap(RegistryIDBirdNETV24, "/models/birdnet-v2.4/x.onnx")
	current := conf.GetSettings()
	assert.Equal(t, "/models/birdnet-v2.4/x.onnx", current.BirdNET.ModelPath, "the model path must be persisted")
	assert.Equal(t, "/srv/custom.txt", current.BirdNET.LabelPath, "a variant swap must never touch LabelPath")

	// Revert to the baseline with an empty model path.
	mm.applyConfigForVariantSwap(RegistryIDBirdNETV24, "")
	current = conf.GetSettings()
	assert.Empty(t, current.BirdNET.ModelPath, "an empty model path reverts to the embedded baseline")
	assert.Equal(t, "/srv/custom.txt", current.BirdNET.LabelPath, "revert must still not touch LabelPath")
}

// TestApplyConfigForVariantSwap_EnablesModel verifies a variant swap persists the model's
// enable so an install on an instance where the model is not enabled (reachable at N=0) makes
// the swapped variant load, without duplicating an already-enabled model or reordering the
// list. This is the fix for the gallery-install-at-N=0 gap.
func TestApplyConfigForVariantSwap_EnablesModel(t *testing.T) {
	// Not parallel: mutates global settings via conf.StoreSettings.
	origSettings := conf.GetSettings()
	t.Cleanup(func() { conf.StoreSettings(origSettings) })
	isolateTestConfig(t)

	alias := ConfigAliasForRegistry(RegistryIDBirdNETV24)

	// v2.4 not enabled (reachable at N=0): the swap must add it.
	settings := conftest.GetTestSettings()
	settings.Models.Enabled = []string{}
	conf.StoreSettings(settings)
	mm := NewModelManager(t.TempDir(), nil, settings)

	mm.applyConfigForVariantSwap(RegistryIDBirdNETV24, "/models/birdnet-v2.4/x.onnx")
	assert.Contains(t, conf.GetSettings().Models.Enabled, alias,
		"a variant swap must enable the model so it loads at N=0")

	// An already-enabled model (with other entries present) must not be duplicated and the
	// existing order must be preserved.
	settings2 := conftest.GetTestSettings()
	settings2.Models.Enabled = []string{"perch_v2", alias}
	conf.StoreSettings(settings2)
	mm.applyConfigForVariantSwap(RegistryIDBirdNETV24, "/models/birdnet-v2.4/y.onnx")
	assert.Equal(t, []string{"perch_v2", alias}, conf.GetSettings().Models.Enabled,
		"an already-enabled model must not be duplicated and order must be preserved")
}

// TestApplyConfigForVariantSwap_UnknownRegistryIsNoop verifies an unknown registry ID
// writes nothing (familyFields returns ok=false, so the helper returns before storing).
func TestApplyConfigForVariantSwap_UnknownRegistryIsNoop(t *testing.T) {
	// Not parallel: mutates global settings via conf.StoreSettings.
	origSettings := conf.GetSettings()
	t.Cleanup(func() { conf.StoreSettings(origSettings) })
	isolateTestConfig(t)

	settings := conftest.GetTestSettings()
	settings.BirdNET.ModelPath = "/keep/me.onnx"
	conf.StoreSettings(settings)
	mm := NewModelManager(t.TempDir(), nil, settings)

	mm.applyConfigForVariantSwap("NoSuchModel", "/models/should-not-write.onnx")
	current := conf.GetSettings()
	assert.Equal(t, "/keep/me.onnx", current.BirdNET.ModelPath, "an unknown registry ID must not write any model field")
}

// TestApplyConfigForVariantSwap_NilSettingsIsNoop pins the mm.settings == nil guard.
func TestApplyConfigForVariantSwap_NilSettingsIsNoop(t *testing.T) {
	// Not parallel: mutates global settings via conf.StoreSettings.
	origSettings := conf.GetSettings()
	t.Cleanup(func() { conf.StoreSettings(origSettings) })

	settings := conftest.GetTestSettings()
	settings.BirdNET.ModelPath = "/keep/me.onnx"
	conf.StoreSettings(settings)
	mm := NewModelManager(t.TempDir(), nil, nil) // nil settings receiver

	mm.applyConfigForVariantSwap(RegistryIDBirdNETV24, "/models/should-not-write.onnx")
	current := conf.GetSettings()
	assert.Equal(t, "/keep/me.onnx", current.BirdNET.ModelPath, "a nil settings receiver must be a no-op")
}
