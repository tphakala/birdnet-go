package classifier

import (
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestModelRegistry_ContainsExpectedModels(t *testing.T) {
	t.Parallel()

	expectedIDs := []string{"BirdNET_V2.4", "Perch_V2"}
	for _, id := range expectedIDs {
		info, exists := ModelRegistry[id]
		require.True(t, exists, "ModelRegistry should contain %s", id)
		assert.Equal(t, id, info.ID)
		assert.NotEmpty(t, info.Name)
		assert.NotZero(t, info.Spec.SampleRate)
		assert.NotZero(t, info.Spec.ClipLength)
	}
}

func TestModelRegistry_BirdNETSpec(t *testing.T) {
	t.Parallel()

	info := ModelRegistry["BirdNET_V2.4"]
	assert.Equal(t, 48000, info.Spec.SampleRate)
	assert.Equal(t, 3*time.Second, info.Spec.ClipLength)
	assert.Equal(t, 6523, info.NumSpecies)
	assert.Contains(t, info.ConfigAliases, "birdnet")
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
	assert.True(t, ids["perch_v2"])
	assert.False(t, ids["unknown"])
}

func TestGetModelSpec(t *testing.T) {
	t.Parallel()

	spec, ok := GetModelSpec("BirdNET_V2.4")
	require.True(t, ok)
	assert.Equal(t, 48000, spec.SampleRate)

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
		{"onnx with perch pattern", "/path/to/perch_v2.onnx", "Perch_V2"},
		{"onnx unrecognized returns Custom", "/path/to/unknown-model.onnx", "Custom"},
		{"tflite with legacy name", "/path/to/BirdNET_GLOBAL_6K_V2.4_Model.tflite", "BirdNET_V2.4"},
		{"tflite unrecognized returns Custom", "/path/to/some-model.tflite", "Custom"},
		{"registry ID directly", "BirdNET_V2.4", "BirdNET_V2.4"},
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
		{"perch_v2 maps to registry ID", "perch_v2", "Perch_V2", true},
		{"unknown returns false", "unknown_model", "", false},
		{"case insensitive", "BIRDNET", "BirdNET_V2.4", true},
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
