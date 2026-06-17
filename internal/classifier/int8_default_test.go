package classifier

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/tphakala/birdnet-go/internal/conf"
)

// TestBirdNETV24EmbeddedLabelsResolve verifies that the canonical BirdNET v2.4
// registry ID (DefaultModelVersion) resolves to an embedded label filesystem and
// a valid label filename without any remap shim.
func TestBirdNETV24EmbeddedLabelsResolve(t *testing.T) {
	// Labels must resolve for the canonical ID with no remap shim.
	fs, err := getModelFileSystem(DefaultModelVersion)
	require.NoError(t, err)
	require.NotNil(t, fs)
	fn, err := conf.GetLabelFilename(DefaultModelVersion, "en-uk")
	require.NoError(t, err)
	assert.NotEmpty(t, fn, "label filename must not be empty")
}

// TestIsBirdNETV24Family verifies that isBirdNETV24Family returns true only for
// the canonical BirdNET v2.4 registry ID, and false for unrelated IDs.
func TestIsBirdNETV24Family(t *testing.T) {
	assert.True(t, isBirdNETV24Family(DefaultModelVersion))
	assert.False(t, isBirdNETV24Family("Perch_V2"))
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
		{"/models/" + DefaultBirdNETINT8ONNXModelName, DefaultModelVersion},
		{"/models/" + DefaultBirdNETModelName, DefaultModelVersion},
		// With the forked RegistryIDBirdNETV24INT8 entry removed, this filename
		// resolves to DefaultModelVersion via the "birdnet_v2.4" substring token.
		{"/models/BirdNET_V2.4_INT8.onnx", DefaultModelVersion},
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

	assert.Equal(t, DefaultModelVersion, info.ID)
	assert.Equal(t, BackendONNX, info.Backend)
	assert.True(t, info.IsStock, "auto-resolved default is stock")
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

// TestRemapV24ForONNXOnly verifies that on ONNX-only (notflite) builds a
// registry-resolved BirdNET v2.4 TFLite model (from version:"2.4" or the default)
// is transparently remapped to the INT8 ONNX entry when present, so existing
// arm64 configs keep starting instead of failing on the missing TFLite backend.
func TestRemapV24ForONNXOnly(t *testing.T) {
	t.Parallel()

	v24 := ModelRegistry[DefaultModelVersion]
	findHit := func(name string) (string, bool) {
		if name == DefaultBirdNETINT8ONNXModelName {
			return "/models/" + name, true
		}
		return "", false
	}
	findMiss := func(string) (string, bool) { return "", false }

	t.Run("tflite available: unchanged", func(t *testing.T) {
		t.Parallel()
		got := remapV24ForONNXOnly(&v24, true, findHit)
		assert.Equal(t, DefaultModelVersion, got.ID)
		assert.Equal(t, BackendTFLite, got.Backend)
	})
	t.Run("onnx-only + int8 present: remapped to unified ONNX", func(t *testing.T) {
		t.Parallel()
		got := remapV24ForONNXOnly(&v24, false, findHit)
		assert.Equal(t, DefaultModelVersion, got.ID)
		assert.Equal(t, BackendONNX, got.Backend)
		assert.Equal(t, QuantizationINT8, got.Quantization)
		assert.True(t, got.IsStock)
		assert.Equal(t, "/models/"+DefaultBirdNETINT8ONNXModelName, got.CustomPath)
	})
	t.Run("onnx-only but int8 absent: unchanged (fails clearly downstream)", func(t *testing.T) {
		t.Parallel()
		got := remapV24ForONNXOnly(&v24, false, findMiss)
		assert.Equal(t, DefaultModelVersion, got.ID)
	})
	t.Run("explicit custom .tflite path: not remapped", func(t *testing.T) {
		t.Parallel()
		custom := v24
		custom.CustomPath = "/data/model/my.tflite"
		got := remapV24ForONNXOnly(&custom, false, findHit)
		assert.Equal(t, DefaultModelVersion, got.ID)
		assert.Equal(t, "/data/model/my.tflite", got.CustomPath)
	})
	t.Run("non-v2.4 entry: unchanged", func(t *testing.T) {
		t.Parallel()
		perch := ModelRegistry[RegistryIDPerchV2]
		got := remapV24ForONNXOnly(&perch, false, findHit)
		assert.Equal(t, RegistryIDPerchV2, got.ID)
	})
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

	assert.Equal(t, DefaultModelVersion, info.ID)
	assert.Equal(t, BackendONNX, info.Backend)
	assert.Equal(t, QuantizationINT8, info.Quantization)
}

// TestDefaultClassifierResolvesUnifiedINT8 verifies that on arm64 the default
// classifier resolves to the unified BirdNET_V2.4 ID with ONNX backend and INT8
// quantization, not the deprecated forked registry ID.
func TestDefaultClassifierResolvesUnifiedINT8(t *testing.T) {
	find := func(name string) (string, bool) {
		if name == DefaultBirdNETINT8ONNXModelName {
			return "/models/" + name, true
		}
		return "", false
	}
	info := defaultClassifierModelInfo("arm64", find)
	assert.Equal(t, DefaultModelVersion, info.ID, "ID stays BirdNET_V2.4, not forked")
	assert.Equal(t, BackendONNX, info.Backend)
	assert.Equal(t, QuantizationINT8, info.Quantization)
	assert.True(t, info.IsStock, "auto-resolved default is stock")
	assert.Equal(t, "/models/"+DefaultBirdNETINT8ONNXModelName, info.CustomPath)
}

// TestDefaultClassifierFallsBackToTFLite verifies that on arm64 without the INT8
// ONNX model present, the default falls back to TFLite v2.4.
func TestDefaultClassifierFallsBackToTFLite(t *testing.T) {
	find := func(string) (string, bool) { return "", false }
	info := defaultClassifierModelInfo("arm64", find)
	assert.Equal(t, DefaultModelVersion, info.ID)
	assert.Equal(t, BackendTFLite, info.Backend)
}

// TestDetermineModelInfoINT8ONNX verifies that an explicit INT8 ONNX model path
// resolves to the unified BirdNET_V2.4 ID with ONNX backend and INT8 quantization,
// and is NOT marked as stock (it is user-supplied).
func TestDetermineModelInfoINT8ONNX(t *testing.T) {
	info, err := DetermineModelInfo("/models/BirdNET_INT8_ARM.onnx")
	require.NoError(t, err)
	assert.Equal(t, DefaultModelVersion, info.ID)
	assert.Equal(t, BackendONNX, info.Backend)
	assert.Equal(t, QuantizationINT8, info.Quantization)
	assert.False(t, info.IsStock, "explicit modelpath is not stock")
}

// TestRemapV24ForONNXOnlyUnified verifies that remapV24ForONNXOnly returns the
// unified BirdNET_V2.4 ID (not the forked INT8 ID) when remapping to ONNX.
func TestRemapV24ForONNXOnlyUnified(t *testing.T) {
	find := func(name string) (string, bool) {
		if name == DefaultBirdNETINT8ONNXModelName {
			return "/models/" + name, true
		}
		return "", false
	}
	base := ModelRegistry[DefaultModelVersion]
	got := remapV24ForONNXOnly(&base, false /*tfliteAvailable*/, find)
	assert.Equal(t, DefaultModelVersion, got.ID)
	assert.Equal(t, BackendONNX, got.Backend)
	assert.Equal(t, QuantizationINT8, got.Quantization)
	assert.True(t, got.IsStock)
}
