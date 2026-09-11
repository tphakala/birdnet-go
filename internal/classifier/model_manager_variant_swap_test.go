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
// embedded baseline (the DFT->baseline path in replacePrimaryVariant).
func TestApplyConfigForVariantSwap_PrimaryWritesModelPathOnly(t *testing.T) {
	// Not parallel: mutates global settings via conf.StoreSettings.
	origSettings := conf.GetSettings()
	t.Cleanup(func() { conf.StoreSettings(origSettings) })
	isolateTestConfig(t)

	settings := conftest.GetTestSettings()
	settings.BirdNET.LabelPath = "/srv/custom.txt"
	conf.StoreSettings(settings)
	mm := NewModelManager(t.TempDir(), nil, settings)

	mm.applyConfigForVariantSwap(permanentRegistryID, "/models/birdnet-v2.4/x.onnx")
	current := conf.GetSettings()
	assert.Equal(t, "/models/birdnet-v2.4/x.onnx", current.BirdNET.ModelPath, "the model path must be persisted")
	assert.Equal(t, "/srv/custom.txt", current.BirdNET.LabelPath, "a variant swap must never touch LabelPath")

	// Revert to the baseline with an empty model path.
	mm.applyConfigForVariantSwap(permanentRegistryID, "")
	current = conf.GetSettings()
	assert.Empty(t, current.BirdNET.ModelPath, "an empty model path reverts to the embedded baseline")
	assert.Equal(t, "/srv/custom.txt", current.BirdNET.LabelPath, "revert must still not touch LabelPath")
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

	mm.applyConfigForVariantSwap(permanentRegistryID, "/models/should-not-write.onnx")
	current := conf.GetSettings()
	assert.Equal(t, "/keep/me.onnx", current.BirdNET.ModelPath, "a nil settings receiver must be a no-op")
}
