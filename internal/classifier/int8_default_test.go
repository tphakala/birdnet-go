package classifier

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/tphakala/birdnet-go/internal/conf"
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
	// Audio spec must match or the analysis framing breaks.
	assert.Equal(t, v24.Spec, info.Spec)
	// Backend, display name, and description intentionally differ.
	assert.Equal(t, BackendONNX, info.Backend)
	assert.Equal(t, "BirdNET v2.4 (ONNX)", info.DisplayName())
	assert.NotEqual(t, v24.Description, info.Description)
	// No config alias: it is selected via the arch-aware default or an explicit
	// model path, never a user-facing config ID (would collide with "birdnet").
	assert.Empty(t, info.ConfigAliases)
}

// TestLabelModelID verifies the INT8 entry resolves to BirdNET v2.4's label
// family (regression guard for the INT8 default failing to load labels).
func TestLabelModelID(t *testing.T) {
	t.Parallel()
	assert.Equal(t, DefaultModelVersion, labelModelID(RegistryIDBirdNETV24INT8))
	assert.Equal(t, DefaultModelVersion, labelModelID(DefaultModelVersion))
	assert.Equal(t, RegistryIDPerchV2, labelModelID(RegistryIDPerchV2))
}

// TestINT8EntryHasEmbeddedLabels is the regression guard for the gate-caught
// blocker: the INT8 registry ID must resolve to v2.4's embedded label filesystem
// and a valid label filename, or NewBirdNET fails to start on the arm64 default.
func TestINT8EntryHasEmbeddedLabels(t *testing.T) {
	t.Parallel()
	fsys, err := getModelFileSystem(labelModelID(RegistryIDBirdNETV24INT8))
	require.NoError(t, err)
	require.NotNil(t, fsys)

	fn, err := conf.GetLabelFilename(labelModelID(RegistryIDBirdNETV24INT8), "en-uk")
	require.NoError(t, err)
	assert.NotEmpty(t, fn)
}

// TestDetermineModelInfo_V24TFLiteStaysTFLite is the reverse guard: a v2.4 TFLite
// filename must resolve to the TFLite v2.4 entry, never the INT8 ONNX entry.
func TestDetermineModelInfo_V24TFLiteStaysTFLite(t *testing.T) {
	t.Parallel()
	info, err := DetermineModelInfo("/models/" + DefaultBirdNETModelName)
	require.NoError(t, err)
	assert.Equal(t, DefaultModelVersion, info.ID)
	assert.Equal(t, BackendTFLite, info.Backend)
}

// TestDetermineModelInfo_Deterministic guards against the map-iteration
// nondeterminism: each filename must resolve to the same entry on every call,
// and the more specific int8 token must win over its v2.4 prefix.
func TestDetermineModelInfo_Deterministic(t *testing.T) {
	t.Parallel()
	cases := []struct{ name, wantID string }{
		{"/models/" + DefaultBirdNETINT8ONNXModelName, RegistryIDBirdNETV24INT8},
		{"/models/" + DefaultBirdNETModelName, DefaultModelVersion},
		{"/models/BirdNET_V2.4_INT8.onnx", RegistryIDBirdNETV24INT8},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			t.Parallel()
			for i := range 50 {
				info, err := DetermineModelInfo(c.name)
				require.NoError(t, err)
				require.Equalf(t, c.wantID, info.ID, "iteration %d", i)
			}
		})
	}
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
