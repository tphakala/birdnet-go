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

	expectedIDs := []string{"BirdNET_V2.4", "BirdNET_V3.0", "Perch_V2"}
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

func TestKnownConfigIDs(t *testing.T) {
	t.Parallel()

	ids := KnownConfigIDs()
	assert.True(t, ids["birdnet"])
	assert.True(t, ids["birdnet_v3.0"])
	assert.True(t, ids["perch_v2"])
	assert.False(t, ids["unknown"])
}

func TestGetModelSpec(t *testing.T) {
	t.Parallel()

	spec, ok := GetModelSpec("BirdNET_V2.4")
	require.True(t, ok)
	assert.Equal(t, 48000, spec.SampleRate)

	spec, ok = GetModelSpec("BirdNET_V3.0")
	require.True(t, ok)
	assert.Equal(t, 32000, spec.SampleRate)

	spec, ok = GetModelSpec("Perch_V2")
	require.True(t, ok)
	assert.Equal(t, 32000, spec.SampleRate)

	_, ok = GetModelSpec("nonexistent")
	assert.False(t, ok)
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

func TestResolveConfigModelID(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name       string
		configID   string
		wantID     string
		wantExists bool
	}{
		{"birdnet maps to registry ID", "birdnet", "BirdNET_V2.4", true},
		{"birdnet_v3.0 maps to registry ID", "birdnet_v3.0", "BirdNET_V3.0", true},
		{"perch_v2 maps to registry ID", "perch_v2", "Perch_V2", true},
		{"unknown returns false", "unknown_model", "", false},
		{"case insensitive", "BIRDNET", "BirdNET_V2.4", true},
		{"case insensitive birdnet v3", "BIRDNET_V3.0", "BirdNET_V3.0", true},
		{"case insensitive perch", "PERCH_V2", "Perch_V2", true},
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

func TestDetermineModelInfo_UnrecognizedNonModelFile(t *testing.T) {
	t.Parallel()

	_, err := DetermineModelInfo("not-a-model-file.txt")
	require.Error(t, err)
	assert.Contains(t, err.Error(), "unrecognized model")
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

func TestDetectionModelInfoForID_Known(t *testing.T) {
	t.Parallel()
	got := DetectionModelInfoForID("Perch_V2")
	assert.Equal(t, "Perch", got.Name)
	assert.Equal(t, "V2", got.Version)
}

func TestDetectionModelInfoForID_Unknown(t *testing.T) {
	t.Parallel()
	got := DetectionModelInfoForID("Unknown_Model")
	assert.Equal(t, detection.DefaultModelInfo(), got)
}
