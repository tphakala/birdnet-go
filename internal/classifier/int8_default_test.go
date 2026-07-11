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
	t.Parallel()
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
	t.Parallel()
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

// TestRemapV24ToONNXOnARM64 verifies the stock v2.4 TFLite default is remapped to
// the INT8-ARM ONNX entry when ONNX is the right stock backend and the file is
// present: on arm64 (its reduced-memory default, TFLite still linked for custom
// models) and on a non-arm64 notflite build (no TFLite backend to run). A normal
// non-arm64 build keeps FP32 TFLite even with the ONNX file present, arm64 without
// the ONNX file stays on TFLite, and a user-supplied .tflite (CustomPath) is never
// swapped.
func TestRemapV24ToONNXOnARM64(t *testing.T) {
	t.Parallel()

	v24 := ModelRegistry[DefaultModelVersion]
	findHit := func(name string) (string, bool) {
		if name == DefaultBirdNETINT8ONNXModelName {
			return "/models/" + name, true
		}
		return "", false
	}
	findMiss := func(string) (string, bool) { return "", false }

	assertRemappedToONNX := func(t *testing.T, got ModelInfo) {
		t.Helper()
		assert.Equal(t, DefaultModelVersion, got.ID)
		assert.Equal(t, BackendONNX, got.Backend)
		assert.Equal(t, QuantizationINT8, got.Quantization)
		assert.True(t, got.IsStock)
		assert.Equal(t, "/models/"+DefaultBirdNETINT8ONNXModelName, got.CustomPath)
	}

	t.Run("arm64 (tflite linked) + int8 present: remapped to ONNX", func(t *testing.T) {
		t.Parallel()
		assertRemappedToONNX(t, remapV24ToONNXOnARM64(&v24, "arm64", true, findHit))
	})
	t.Run("non-arm64 notflite + int8 present: remapped to ONNX (no-TFLite fallback)", func(t *testing.T) {
		t.Parallel()
		assertRemappedToONNX(t, remapV24ToONNXOnARM64(&v24, "amd64", false, findHit))
	})
	t.Run("normal amd64 (tflite available) + int8 present: not remapped", func(t *testing.T) {
		t.Parallel()
		got := remapV24ToONNXOnARM64(&v24, "amd64", true, findHit)
		assert.Equal(t, DefaultModelVersion, got.ID)
		assert.Equal(t, BackendTFLite, got.Backend)
	})
	t.Run("arm64 but int8 absent: unchanged (fails clearly downstream)", func(t *testing.T) {
		t.Parallel()
		got := remapV24ToONNXOnARM64(&v24, "arm64", true, findMiss)
		assert.Equal(t, DefaultModelVersion, got.ID)
		assert.Equal(t, BackendTFLite, got.Backend)
	})
	t.Run("explicit custom .tflite path: not remapped", func(t *testing.T) {
		t.Parallel()
		custom := v24
		custom.CustomPath = "/data/model/my.tflite"
		got := remapV24ToONNXOnARM64(&custom, "arm64", true, findHit)
		assert.Equal(t, DefaultModelVersion, got.ID)
		assert.Equal(t, "/data/model/my.tflite", got.CustomPath)
		assert.Equal(t, BackendTFLite, got.Backend)
	})
	t.Run("non-v2.4 entry: unchanged", func(t *testing.T) {
		t.Parallel()
		perch := ModelRegistry[RegistryIDPerchV2]
		got := remapV24ToONNXOnARM64(&perch, "arm64", true, findHit)
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
	t.Parallel()
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
	t.Parallel()
	find := func(string) (string, bool) { return "", false }
	info := defaultClassifierModelInfo("arm64", find)
	assert.Equal(t, DefaultModelVersion, info.ID)
	assert.Equal(t, BackendTFLite, info.Backend)
}

// TestDetermineModelInfoINT8ONNX verifies that an explicit INT8 ONNX model path
// resolves to the unified BirdNET_V2.4 ID with ONNX backend and INT8 quantization,
// and is NOT marked as stock (it is user-supplied).
func TestDetermineModelInfoINT8ONNX(t *testing.T) {
	t.Parallel()
	info, err := DetermineModelInfo("/models/BirdNET_INT8_ARM.onnx")
	require.NoError(t, err)
	assert.Equal(t, DefaultModelVersion, info.ID)
	assert.Equal(t, BackendONNX, info.Backend)
	assert.Equal(t, QuantizationINT8, info.Quantization)
	assert.False(t, info.IsStock, "explicit modelpath is not stock")
}

// TestRemapV24ToONNXOnARM64Unified verifies that remapV24ToONNXOnARM64 returns the
// unified BirdNET_V2.4 ID (not the forked INT8 ID) when remapping to ONNX.
func TestRemapV24ToONNXOnARM64Unified(t *testing.T) {
	t.Parallel()
	find := func(name string) (string, bool) {
		if name == DefaultBirdNETINT8ONNXModelName {
			return "/models/" + name, true
		}
		return "", false
	}
	base := ModelRegistry[DefaultModelVersion]
	got := remapV24ToONNXOnARM64(&base, "arm64", true, find)
	assert.Equal(t, DefaultModelVersion, got.ID)
	assert.Equal(t, BackendONNX, got.Backend)
	assert.Equal(t, QuantizationINT8, got.Quantization)
	assert.True(t, got.IsStock)
}
