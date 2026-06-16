package classifier

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestINT8RegistryEntry verifies the dedicated INT8-ARM ONNX entry mirrors the
// BirdNET v2.4 identity but runs on the ONNX backend.
func TestINT8RegistryEntry(t *testing.T) {
	t.Parallel()

	info, ok := ModelRegistry[RegistryIDBirdNETV24INT8]
	require.True(t, ok, "INT8 registry entry must exist")

	assert.Equal(t, BackendONNX, info.Backend)
	// Detections must be attributed to the same model/version as TFLite v2.4 so
	// detection history stays continuous across the backend switch.
	v24 := ModelRegistry[DefaultModelVersion]
	assert.Equal(t, v24.DetectionName, info.DetectionName)
	assert.Equal(t, v24.DetectionVersion, info.DetectionVersion)
	assert.Equal(t, v24.NumSpecies, info.NumSpecies)
	assert.Equal(t, v24.SupportedLocales, info.SupportedLocales)
	assert.Equal(t, v24.DefaultLocale, info.DefaultLocale)
	// Distinct backend suffix in the display name.
	assert.Equal(t, "BirdNET v2.4 (ONNX)", info.DisplayName())
	// No config alias: it is selected via the arch-aware default or an explicit
	// model path, never a user-facing config ID (would collide with "birdnet").
	assert.Empty(t, info.ConfigAliases)
}

// TestDefaultClassifierModelInfo_ARM64PrefersINT8WhenPresent verifies that on
// arm64 the INT8 ONNX model is chosen when present in the model search path.
func TestDefaultClassifierModelInfo_ARM64PrefersINT8WhenPresent(t *testing.T) {
	t.Parallel()

	find := func(name string) (string, bool) {
		if name == DefaultBirdNETINT8ONNXModelName {
			return "/models/" + name, true
		}
		return "", false
	}

	info := defaultClassifierModelInfo("arm64", find)

	assert.Equal(t, RegistryIDBirdNETV24INT8, info.ID)
	assert.Equal(t, BackendONNX, info.Backend)
	assert.Equal(t, "/models/"+DefaultBirdNETINT8ONNXModelName, info.CustomPath)
}

// TestDefaultClassifierModelInfo_ARM64FallsBackToTFLite verifies that arm64
// hosts without the INT8 model shipped (e.g. native installs) keep TFLite v2.4.
func TestDefaultClassifierModelInfo_ARM64FallsBackToTFLite(t *testing.T) {
	t.Parallel()

	find := func(string) (string, bool) { return "", false }

	info := defaultClassifierModelInfo("arm64", find)

	assert.Equal(t, DefaultModelVersion, info.ID)
	assert.Equal(t, BackendTFLite, info.Backend)
	assert.Empty(t, info.CustomPath)
}

// TestDefaultClassifierModelInfo_AMD64AlwaysTFLite verifies the INT8 default is
// arm64-only: amd64 keeps TFLite even when an INT8 model is present on disk.
func TestDefaultClassifierModelInfo_AMD64AlwaysTFLite(t *testing.T) {
	t.Parallel()

	find := func(name string) (string, bool) { return "/models/" + name, true }

	info := defaultClassifierModelInfo("amd64", find)

	assert.Equal(t, DefaultModelVersion, info.ID)
	assert.Equal(t, BackendTFLite, info.Backend)
}

// TestDefaultRangeFilterONNXPath verifies the arm64-only ONNX range filter
// default: the ONNX MData model is chosen on arm64 when present, never on amd64.
func TestDefaultRangeFilterONNXPath(t *testing.T) {
	t.Parallel()

	find := func(name string) (string, bool) {
		if name == DefaultRangeFilterV2ONNXModelName {
			return "/models/" + name, true
		}
		return "", false
	}

	t.Run("arm64 with file present", func(t *testing.T) {
		t.Parallel()
		path, ok := defaultRangeFilterONNXPath("arm64", find)
		assert.True(t, ok)
		assert.Equal(t, "/models/"+DefaultRangeFilterV2ONNXModelName, path)
	})

	t.Run("arm64 without file falls back", func(t *testing.T) {
		t.Parallel()
		_, ok := defaultRangeFilterONNXPath("arm64", func(string) (string, bool) { return "", false })
		assert.False(t, ok)
	})

	t.Run("amd64 ignores file", func(t *testing.T) {
		t.Parallel()
		_, ok := defaultRangeFilterONNXPath("amd64", find)
		assert.False(t, ok)
	})
}

// TestDetermineModelInfo_INT8ArmFilename verifies an explicit int8-arm ONNX
// model path resolves to the INT8 entry, not the TFLite v2.4 entry.
func TestDetermineModelInfo_INT8ArmFilename(t *testing.T) {
	t.Parallel()

	info, err := DetermineModelInfo("/models/" + DefaultBirdNETINT8ONNXModelName)
	require.NoError(t, err)

	assert.Equal(t, RegistryIDBirdNETV24INT8, info.ID)
	assert.Equal(t, BackendONNX, info.Backend)
}
