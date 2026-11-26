package classifier

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/tphakala/birdnet-go/internal/conf"
	"github.com/tphakala/birdnet-go/internal/conf/conftest"
)

// TestV24LateLoad_PredictAfterEnableAtRuntime covers enabling BirdNET v2.4 on an
// install that started without it: the instance is built after the settings
// snapshot is published, so its labels are not in the snapshot, and Predict must
// still pair its scores with the labels the instance loaded.
// Skips on a noembed build, where the embedded model cannot load.
func TestV24LateLoad_PredictAfterEnableAtRuntime(t *testing.T) {
	isolateTestConfig(t)
	settings := conftest.GetTestSettings()
	settings.Models.Enabled = nil
	conftest.SetTestSettings(settings)
	t.Cleanup(func() { conftest.SetTestSettings(nil) })

	o, err := NewOrchestrator(settings)
	require.NoError(t, err)
	// Registered before any assertion that can end the test; Delete is safe to
	// repeat when requireV24Loaded has already deleted o.
	t.Cleanup(func() { o.Delete() })
	require.False(t, o.IsModelLoaded(RegistryIDBirdNETV24), "v2.4 is not enabled at startup")

	// Enable v2.4 the way a UI save does: publish a new snapshot.
	enabled := conf.CloneSettings(conf.GetSettings())
	enableBirdNETV24(enabled)
	conftest.SetTestSettings(enabled)

	loadErr := o.LoadModel(RegistryIDBirdNETV24)
	if hasEmbeddedModels {
		require.NoError(t, loadErr)
	}
	requireV24Loaded(t, o) // deletes o and skips under noembed

	// Without a published snapshot currentSettings() falls back to the instance
	// and the label source cannot matter, so the check would pass vacuously.
	require.NotNil(t, conf.GetSettings(), "a settings snapshot must be published")

	spec, ok := o.ModelSpecFor(RegistryIDBirdNETV24)
	require.True(t, ok)
	results, err := o.PredictModel(t.Context(), RegistryIDBirdNETV24, silentInput(spec))
	require.NoError(t, err, "v2.4 loaded after publish must predict without a label pairing error")
	assert.NotEmpty(t, results)
}
