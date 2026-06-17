package classifier

import (
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/tphakala/birdnet-go/internal/detection"
)

func TestModelRegistry_ContainsExpectedModels(t *testing.T) {
	t.Parallel()

	expectedIDs := []string{"BirdNET_V2.4", "BirdNET_V3.0", "Perch_V2", "BSG"}
	for _, id := range expectedIDs {
		info, exists := ModelRegistry[id]
		require.True(t, exists, "ModelRegistry should contain %s", id)
		assert.Equal(t, id, info.ID)
		assert.NotEmpty(t, info.Name)
		assert.NotZero(t, info.Spec.SampleRate)
		assert.NotZero(t, info.Spec.ClipLength)
	}
}

func TestModelRegistry_BackendAndDisplayName(t *testing.T) {
	t.Parallel()

	tests := []struct {
		registryID  string
		wantName    string
		wantBackend string
		wantDisplay string
	}{
		{"BirdNET_V2.4", ModelNameBirdNETv24, BackendTFLite, "BirdNET v2.4 (TFLite)"},
		{"BirdNET_V3.0", ModelNameBirdNETv30, BackendONNX, "BirdNET v3.0 (ONNX)"},
		{"Perch_V2", ModelNamePerchV2, BackendONNX, "Google Perch v2 (ONNX)"},
	}

	for _, tt := range tests {
		t.Run(tt.registryID, func(t *testing.T) {
			t.Parallel()
			info := ModelRegistry[tt.registryID]
			assert.Equal(t, tt.wantName, info.Name)
			assert.Equal(t, tt.wantBackend, info.Backend)
			assert.Equal(t, tt.wantDisplay, info.DisplayName())
		})
	}
}

func TestDetectQuantization(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name string
		in   string
		want Quantization
	}{
		{"int8 underscore", "BirdNET_INT8_ARM.onnx", QuantizationINT8},
		{"int8 hyphen", "model-int8.onnx", QuantizationINT8},
		{"fp16", "BirdNET_GLOBAL_6K_V2.4_MData_Model_FP16.tflite", QuantizationFP16},
		{"fp32", "BirdNET_GLOBAL_6K_V2.4_Model_FP32.tflite", QuantizationFP32},
		{"no marker", "BirdNET_MData_V2.onnx", QuantizationUnknown},
		{"false positive sprint8", "sprint8_model.onnx", QuantizationUnknown},
		{"false positive point8", "perch_point8.onnx", QuantizationUnknown},
		{"int8 at start", "int8_model.onnx", QuantizationINT8},
		{"dot delimiter", "model.int8.onnx", QuantizationINT8},
		{"ambiguous multi-token", "model_int8_fp16.onnx", QuantizationUnknown},
		{"uppercase + uppercase ext", "model_FP16.TFLITE", QuantizationFP16},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			assert.Equal(t, tt.want, detectQuantization(tt.in))
		})
	}
}

func TestBirdNETV24EntryQuantization(t *testing.T) {
	t.Parallel()
	info := ModelRegistry[DefaultModelVersion]
	assert.Equal(t, BackendTFLite, info.Backend)
	assert.Equal(t, QuantizationFP32, info.Quantization)
	assert.False(t, info.IsStock, "registry templates are not marked IsStock")
}

func TestDisplayName_NoBackend(t *testing.T) {
	t.Parallel()

	info := ModelInfo{Name: "Custom Model"}
	assert.Equal(t, "Custom Model", info.DisplayName())
}

func TestModelRegistry_BirdNETSpec(t *testing.T) {
	t.Parallel()

	info := ModelRegistry["BirdNET_V2.4"]
	assert.Equal(t, 48000, info.Spec.SampleRate)
	assert.Equal(t, 3*time.Second, info.Spec.ClipLength)
	assert.Equal(t, 6523, info.NumSpecies)
	assert.Contains(t, info.ConfigAliases, "birdnet")
}

func TestModelRegistry_BirdNETv30Spec(t *testing.T) {
	t.Parallel()

	info := ModelRegistry["BirdNET_V3.0"]
	assert.Equal(t, 32000, info.Spec.SampleRate)
	assert.Equal(t, 5*time.Second, info.Spec.ClipLength)
	assert.Contains(t, info.ConfigAliases, "birdnet_v3.0")
	assert.Equal(t, "BirdNET", info.DetectionName)
	assert.Equal(t, "3.0", info.DetectionVersion)
}

func TestModelRegistry_PerchSpec(t *testing.T) {
	t.Parallel()

	info := ModelRegistry["Perch_V2"]
	assert.Equal(t, 32000, info.Spec.SampleRate)
	assert.Equal(t, 5*time.Second, info.Spec.ClipLength)
	assert.Contains(t, info.ConfigAliases, "perch_v2")
}

func TestResolveBirdNETVersion(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name    string
		version string
		wantID  string
		wantOK  bool
	}{
		{"v2.4 resolves", "2.4", "BirdNET_V2.4", true},
		{"v3.0 resolves", "3.0", "BirdNET_V3.0", true},
		{"unknown version", "9.9", "", false},
		{"empty string", "", "", false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			info, ok := ResolveBirdNETVersion(tt.version)
			assert.Equal(t, tt.wantOK, ok)
			if ok {
				assert.Equal(t, tt.wantID, info.ID)
			}
		})
	}
}

func TestDetermineModelInfo_OnnxSupport(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name   string
		input  string
		wantID string
	}{
		{"onnx with birdnet-v24 pattern", "/path/to/birdnet-v24.onnx", "BirdNET_V2.4"},
		{"onnx with birdnet-v30 pattern", "/path/to/birdnet-v30.onnx", "BirdNET_V3.0"},
		{"onnx with birdnet_v3.0 pattern", "/path/to/birdnet_v3.0.onnx", "BirdNET_V3.0"},
		{"onnx with perch pattern", "/path/to/perch_v2.onnx", "Perch_V2"},
		{"onnx unrecognized returns Custom", "/path/to/unknown-model.onnx", "Custom"},
		{"tflite with legacy name", "/path/to/BirdNET_GLOBAL_6K_V2.4_Model.tflite", "BirdNET_V2.4"},
		{"tflite unrecognized returns Custom", "/path/to/some-model.tflite", "Custom"},
		{"custom classifier build name", "/home/birdnet/BirdNET-Go_classifier_20260118.tflite", "BirdNET_V2.4"},
		{"registry ID directly", "BirdNET_V2.4", "BirdNET_V2.4"},
		{"registry ID v3.0", "BirdNET_V3.0", "BirdNET_V3.0"},
		{"registry ID Perch", "Perch_V2", "Perch_V2"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			info, err := DetermineModelInfo(tt.input)
			require.NoError(t, err)
			assert.Equal(t, tt.wantID, info.ID)
		})
	}
}

func TestDetermineModelInfo_UnrecognizedNonModelFile(t *testing.T) {
	t.Parallel()

	_, err := DetermineModelInfo("not-a-model-file.txt")
	require.Error(t, err)
	assert.Contains(t, err.Error(), "unrecognized model")
}

// TestCustomBirdNETV24ModelInfo verifies that any model configured in the
// birdnet config section keeps the canonical BirdNET_V2.4 identity regardless
// of filename. Identity divergence (e.g. a "Custom" ID for an unrecognized
// filename) breaks the per-source model-set join in resolveDesiredModelSet,
// which keys the loaded model by ID against the "birdnet" config alias -> the
// primary classifier never gets a buffer monitor and inference never starts.
// BirdNET v3.0 is selected via birdnet.version, never by a filename here.
func TestCustomBirdNETV24ModelInfo(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name      string
		path      string
		wantBack  string
		wantQuant Quantization
	}{
		{"fp16 keepact onnx", "/home/thakala/BirdNET_v24_fp16_keepact.onnx", BackendONNX, QuantizationFP16},
		{"int8 arm onnx", "/models/BirdNET_INT8_ARM.onnx", BackendONNX, QuantizationINT8},
		{"plain onnx no precision token", "/models/my-classifier.onnx", BackendONNX, QuantizationFP32},
		{"custom tflite build", "/models/BirdNET-Go_classifier.tflite", BackendTFLite, QuantizationFP32},
		{"unrecognized onnx name", "/models/totally-unknown.onnx", BackendONNX, QuantizationFP32},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			info := customBirdNETV24ModelInfo(tt.path)

			assert.Equal(t, DefaultModelVersion, info.ID, "birdnet-slot model must keep BirdNET_V2.4 identity")
			assert.Equal(t, "BirdNET", info.DetectionName)
			assert.Equal(t, "2.4", info.DetectionVersion)
			assert.Equal(t, tt.path, info.CustomPath)
			assert.False(t, info.IsStock, "user-supplied model must not be marked stock")
			assert.Equal(t, tt.wantBack, info.Backend)
			assert.Equal(t, tt.wantQuant, info.Quantization)
			assert.NotEmpty(t, info.SupportedLocales, "must inherit BirdNET v2.4 locales")
		})
	}
}

func TestIsLocaleSupported_PerchHasNoLocales(t *testing.T) {
	t.Parallel()

	perchInfo := ModelRegistry["Perch_V2"]
	assert.False(t, IsLocaleSupported(&perchInfo, "en-uk"), "Perch_V2 should not support any locale")
}

func TestIsLocaleSupported_CustomModelAcceptsAll(t *testing.T) {
	t.Parallel()

	customInfo := ModelInfo{ID: modelIDCustom, SupportedLocales: []string{}}
	assert.True(t, IsLocaleSupported(&customInfo, "en-uk"), "Custom model should accept any locale")
}

func TestModelInfo_ToDetectionModelInfo_BirdNET(t *testing.T) {
	t.Parallel()
	info := ModelRegistry["BirdNET_V2.4"]
	got := info.ToDetectionModelInfo()
	assert.Equal(t, detection.ModelInfo{
		Name:    "BirdNET",
		Version: "2.4",
		Variant: "default",
	}, got)
}

func TestModelInfo_ToDetectionModelInfo_BirdNETv30(t *testing.T) {
	t.Parallel()
	info := ModelRegistry["BirdNET_V3.0"]
	got := info.ToDetectionModelInfo()
	assert.Equal(t, detection.ModelInfo{
		Name:    "BirdNET",
		Version: "3.0",
		Variant: "default",
	}, got)
}

func TestModelInfo_ToDetectionModelInfo_Perch(t *testing.T) {
	t.Parallel()
	info := ModelRegistry["Perch_V2"]
	got := info.ToDetectionModelInfo()
	assert.Equal(t, detection.ModelInfo{
		Name:    "Perch",
		Version: "V2",
		Variant: "default",
	}, got)
}

func TestDetectionModelInfoForID_Unknown(t *testing.T) {
	t.Parallel()
	got := DetectionModelInfoForID("Unknown_Model")
	assert.Equal(t, detection.DefaultModelInfo(), got)
}

func TestModelRegistry_BatEntry(t *testing.T) {
	t.Parallel()

	info, exists := ModelRegistry[RegistryIDBat]
	require.True(t, exists, "ModelRegistry should contain Bat")
	assert.Equal(t, RegistryIDBat, info.ID)
	assert.Equal(t, "Bat Classifier", info.Name)
	assert.Equal(t, BackendONNX, info.Backend)
	assert.Equal(t, 48000, info.Spec.SampleRate)
	assert.Equal(t, 3*time.Second, info.Spec.ClipLength)
	assert.Equal(t, 256000, info.Spec.RawSampleRate, "bat model expects 256kHz raw audio")
	assert.Equal(t, 256000, info.Spec.EffectiveSampleRate())
}

func TestModelRegistry_BSGEntry(t *testing.T) {
	t.Parallel()

	info, exists := ModelRegistry[RegistryIDBSG]
	require.True(t, exists, "ModelRegistry should contain BSG")
	assert.Equal(t, RegistryIDBSG, info.ID)
	assert.Equal(t, "BSG Finland", info.Name)
	assert.Equal(t, BackendONNX, info.Backend)
	assert.Equal(t, "BSG", info.DetectionName)
	assert.Equal(t, "4.4", info.DetectionVersion)
	assert.Equal(t, 48000, info.Spec.SampleRate)
	assert.Equal(t, 3*time.Second, info.Spec.ClipLength)
	assert.Contains(t, info.ConfigAliases, "bsg")
}

func TestRegistryIDConstants_MatchRegistryKeys(t *testing.T) {
	t.Parallel()

	constants := map[string]string{
		"RegistryIDBirdNETV3": RegistryIDBirdNETV3,
		"RegistryIDBSG":       RegistryIDBSG,
		"RegistryIDBat":       RegistryIDBat,
		"RegistryIDPerchV2":   RegistryIDPerchV2,
	}
	for name, id := range constants {
		_, exists := ModelRegistry[id]
		assert.True(t, exists, "constant %s=%q must have a matching ModelRegistry entry", name, id)
	}
}

func TestKnownConfigIDs_AllModels(t *testing.T) {
	t.Parallel()

	ids := KnownConfigIDs()
	assert.True(t, ids["birdnet"], "birdnet config ID should be known")
	assert.True(t, ids["birdnet_v3.0"], "birdnet_v3.0 config ID should be known")
	assert.True(t, ids["perch_v2"], "perch_v2 config ID should be known")
	assert.True(t, ids["bat"], "bat config ID should be known")
	assert.True(t, ids["bsg"], "bsg config ID should be known")
	assert.False(t, ids["unknown"], "unknown config ID should not be known")
}

func TestResolveConfigModelID_AllModels(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name       string
		configID   string
		wantID     string
		wantExists bool
	}{
		{"birdnet resolves", "birdnet", "BirdNET_V2.4", true},
		{"birdnet_v3.0 resolves", "birdnet_v3.0", RegistryIDBirdNETV3, true},
		{"perch_v2 resolves", "perch_v2", RegistryIDPerchV2, true},
		{"bat resolves", "bat", RegistryIDBat, true},
		{"bsg resolves", "bsg", RegistryIDBSG, true},
		{"case insensitive bat", "BAT", RegistryIDBat, true},
		{"case insensitive bsg", "BSG", RegistryIDBSG, true},
		{"unknown returns false", "nonexistent", "", false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			gotID, gotExists := ResolveConfigModelID(tt.configID)
			assert.Equal(t, tt.wantExists, gotExists)
			if gotExists {
				assert.Equal(t, tt.wantID, gotID)
			}
		})
	}
}

func TestGetModelSpec_AllModels(t *testing.T) {
	t.Parallel()

	tests := []struct {
		registryID     string
		wantSampleRate int
		wantClipLen    time.Duration
	}{
		{"BirdNET_V2.4", 48000, 3 * time.Second},
		{RegistryIDBirdNETV3, 32000, 5 * time.Second},
		{RegistryIDPerchV2, 32000, 5 * time.Second},
		{RegistryIDBat, 48000, 3 * time.Second},
		{RegistryIDBSG, 48000, 3 * time.Second},
	}

	for _, tt := range tests {
		t.Run(tt.registryID, func(t *testing.T) {
			t.Parallel()
			spec, ok := GetModelSpec(tt.registryID)
			require.True(t, ok, "GetModelSpec should find %s", tt.registryID)
			assert.Equal(t, tt.wantSampleRate, spec.SampleRate)
			assert.Equal(t, tt.wantClipLen, spec.ClipLength)
		})
	}
}

func TestDetermineModelInfo_BSGFilenamePatterns(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name   string
		input  string
		wantID string
	}{
		{"bsg_finland pattern", "/path/to/bsg_finland_v4.4.onnx", RegistryIDBSG},
		{"bsg-finland pattern", "/path/to/bsg-finland.onnx", RegistryIDBSG},
		{"registry ID directly", RegistryIDBSG, RegistryIDBSG},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			info, err := DetermineModelInfo(tt.input)
			require.NoError(t, err)
			assert.Equal(t, tt.wantID, info.ID)
		})
	}
}

func TestModelInfo_ToDetectionModelInfo_BSG(t *testing.T) {
	t.Parallel()

	info := ModelRegistry[RegistryIDBSG]
	got := info.ToDetectionModelInfo()
	assert.Equal(t, detection.ModelInfo{
		Name:    "BSG",
		Version: "4.4",
		Variant: "default",
	}, got)
}

func TestModelInfo_ToDetectionModelInfo_Bat(t *testing.T) {
	t.Parallel()

	info := ModelRegistry[RegistryIDBat]
	got := info.ToDetectionModelInfo()
	assert.Equal(t, detection.ModelInfo{
		Name:    "BattyBirdNET",
		Version: "1.0",
		Variant: "default",
	}, got)
}

func TestDetectionModelInfoForID_AllModels(t *testing.T) {
	t.Parallel()

	tests := []struct {
		registryID string
		wantName   string
	}{
		{RegistryIDBirdNETV3, "BirdNET"},
		{RegistryIDPerchV2, "Perch"},
		{RegistryIDBSG, "BSG"},
		{RegistryIDBat, "BattyBirdNET"},
	}

	for _, tt := range tests {
		t.Run(tt.registryID, func(t *testing.T) {
			t.Parallel()
			got := DetectionModelInfoForID(tt.registryID)
			assert.Equal(t, tt.wantName, got.Name)
		})
	}
}

func TestModelInfo_ToDetectionModelInfo_CustomPath(t *testing.T) {
	t.Parallel()

	info := ModelRegistry[RegistryIDBSG]
	info.CustomPath = "/custom/path/model.onnx"
	got := info.ToDetectionModelInfo()
	assert.Equal(t, "custom", got.Variant)
	require.NotNil(t, got.ClassifierPath)
	assert.Equal(t, "/custom/path/model.onnx", *got.ClassifierPath)
}

func TestModelInfo_ToDetectionModelInfo_EmptyDetectionName(t *testing.T) {
	t.Parallel()

	info := ModelInfo{ID: modelIDCustom, Name: "Custom"}
	got := info.ToDetectionModelInfo()
	assert.Equal(t, detection.DefaultModelInfo(), got)
}

// FuzzDetectQuantization seeds a few model names and asserts the result is
// always one of the four defined Quantization constants and that the call
// never panics.
func FuzzDetectQuantization(f *testing.F) {
	seeds := []string{
		"BirdNET_INT8_ARM.onnx",
		"model-fp16.tflite",
		"BirdNET_GLOBAL_6K_V2.4_Model_FP32.tflite",
		"sprint8_model.onnx",
		"perch_point8.onnx",
		"model_int8_fp16.onnx",
		"model_FP16.TFLITE",
		"int8_model.onnx",
		"",
		"/path/to/BirdNET_GLOBAL_6K_V2.4_Model_FP32.tflite",
	}
	for _, s := range seeds {
		f.Add(s)
	}
	valid := map[Quantization]bool{
		QuantizationUnknown: true,
		QuantizationFP32:    true,
		QuantizationFP16:    true,
		QuantizationINT8:    true,
	}
	f.Fuzz(func(t *testing.T, name string) {
		q := detectQuantization(name)
		if !valid[q] {
			t.Errorf("detectQuantization(%q) returned unexpected value %q", name, q)
		}
	})
}

func TestToDetectionModelInfoVariant(t *testing.T) {
	t.Parallel()
	t.Run("stock INT8 default attributes as default", func(t *testing.T) {
		t.Parallel()
		m := stockBirdNETV24ONNXVariant("/models/BirdNET_INT8_ARM.onnx", QuantizationINT8)
		det := m.ToDetectionModelInfo()
		assert.Equal(t, "default", det.Variant)
		require.NotNil(t, det.ClassifierPath)
		assert.Equal(t, "/models/BirdNET_INT8_ARM.onnx", *det.ClassifierPath)
	})
	t.Run("embedded FP32 default attributes as default", func(t *testing.T) {
		t.Parallel()
		m := ModelRegistry[DefaultModelVersion]
		require.Empty(t, m.CustomPath, "precondition: registry entry must not have a custom path")
		assert.Equal(t, "default", m.ToDetectionModelInfo().Variant)
	})
	t.Run("user/gallery model with path attributes as custom", func(t *testing.T) {
		t.Parallel()
		m := ModelRegistry[DefaultModelVersion]
		m.CustomPath = "/home/user/my_birdnet.tflite"
		m.IsStock = false
		assert.Equal(t, "custom", m.ToDetectionModelInfo().Variant)
	})
}
