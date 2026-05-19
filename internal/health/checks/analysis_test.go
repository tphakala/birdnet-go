package checks

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/tphakala/birdnet-go/internal/health"
)

// --- ModelsLoadedCheck tests ---

func TestModelsLoadedCheck_SingleModel(t *testing.T) {
	t.Parallel()
	check := NewModelsLoadedCheck(func() []ModelLoadInfo {
		return []ModelLoadInfo{
			{ID: "BirdNET_V2.4", Name: "BirdNET v2.4", Loaded: true, Backend: "TFLite", SpecInfo: "48kHz, 3s clips"},
		}
	})

	results := check.RunMulti(t.Context())
	require.Len(t, results, 1)
	assert.Equal(t, health.StatusHealthy, results[0].Status)
	assert.Contains(t, results[0].Name, "birdnet_v2_4")
	assert.Contains(t, results[0].Message, "BirdNET v2.4")
}

func TestModelsLoadedCheck_MultipleModels(t *testing.T) {
	t.Parallel()
	check := NewModelsLoadedCheck(func() []ModelLoadInfo {
		return []ModelLoadInfo{
			{ID: "BirdNET_V2.4", Name: "BirdNET v2.4", Loaded: true, Backend: "TFLite", SpecInfo: "48kHz, 3s clips"},
			{ID: "Perch_V2", Name: "Perch V2", Loaded: true, Backend: "ONNX", SpecInfo: "32kHz, 5s clips"},
			{ID: "Bat", Name: "Bat Classifier", Loaded: true, Backend: "ONNX", SpecInfo: "48kHz, 3s clips"},
		}
	})

	results := check.RunMulti(t.Context())
	require.Len(t, results, 3)
	for _, r := range results {
		assert.Equal(t, health.StatusHealthy, r.Status)
		assert.Equal(t, health.CategoryAnalysis, r.Category)
	}
}

func TestModelsLoadedCheck_UnloadedModel(t *testing.T) {
	t.Parallel()
	check := NewModelsLoadedCheck(func() []ModelLoadInfo {
		return []ModelLoadInfo{
			{ID: "BirdNET_V2.4", Name: "BirdNET v2.4", Loaded: true},
			{ID: "Perch_V2", Name: "Perch V2", Loaded: false},
		}
	})

	results := check.RunMulti(t.Context())
	require.Len(t, results, 2)
	assert.Equal(t, health.StatusHealthy, results[0].Status)
	assert.Equal(t, health.StatusCritical, results[1].Status)
	assert.Contains(t, results[1].Message, "not loaded")
}

func TestModelsLoadedCheck_NoModels(t *testing.T) {
	t.Parallel()
	check := NewModelsLoadedCheck(func() []ModelLoadInfo {
		return nil
	})

	results := check.RunMulti(t.Context())
	require.Len(t, results, 1)
	assert.Equal(t, health.StatusCritical, results[0].Status)
	assert.Contains(t, results[0].Message, "No analysis models loaded")
}

func TestModelsLoadedCheck_NilProvider(t *testing.T) {
	t.Parallel()
	check := NewModelsLoadedCheck(nil)
	results := check.RunMulti(t.Context())
	require.Len(t, results, 1)
	assert.Equal(t, health.StatusSkipped, results[0].Status)
}

func TestModelsLoadedCheck_Run_ReturnsWorst(t *testing.T) {
	t.Parallel()
	check := NewModelsLoadedCheck(func() []ModelLoadInfo {
		return []ModelLoadInfo{
			{ID: "A", Name: "Model A", Loaded: true},
			{ID: "B", Name: "Model B", Loaded: false},
		}
	})

	result := check.Run(t.Context())
	assert.Equal(t, health.StatusCritical, result.Status)
}

// --- PerModelInferenceLatencyCheck tests ---

func TestPerModelInferenceLatencyCheck_Healthy(t *testing.T) {
	t.Parallel()
	check := NewPerModelInferenceLatencyCheck(func() []ModelInferenceInfo {
		return []ModelInferenceInfo{
			{ModelID: "BirdNET_V2.4", ModelName: "BirdNET v2.4", AvgMS: 100, P99MS: 200, WindowMS: 1500},
			{ModelID: "Perch_V2", ModelName: "Perch V2", AvgMS: 300, P99MS: 600, WindowMS: 2500},
		}
	})

	results := check.RunMulti(t.Context())
	require.Len(t, results, 2)
	for _, r := range results {
		assert.Equal(t, health.StatusHealthy, r.Status, "model %s should be healthy", r.Name)
	}
}

func TestPerModelInferenceLatencyCheck_Warning(t *testing.T) {
	t.Parallel()
	check := NewPerModelInferenceLatencyCheck(func() []ModelInferenceInfo {
		return []ModelInferenceInfo{
			{ModelID: "BirdNET_V2.4", ModelName: "BirdNET v2.4", AvgMS: 100, P99MS: 800, WindowMS: 1500},
		}
	})

	results := check.RunMulti(t.Context())
	require.Len(t, results, 1)
	assert.Equal(t, health.StatusWarning, results[0].Status)
	assert.Contains(t, results[0].Message, "50%")
}

func TestPerModelInferenceLatencyCheck_Critical(t *testing.T) {
	t.Parallel()
	check := NewPerModelInferenceLatencyCheck(func() []ModelInferenceInfo {
		return []ModelInferenceInfo{
			{ModelID: "Perch_V2", ModelName: "Perch V2", AvgMS: 500, P99MS: 2300, WindowMS: 2500},
		}
	})

	results := check.RunMulti(t.Context())
	require.Len(t, results, 1)
	assert.Equal(t, health.StatusCritical, results[0].Status)
	assert.Contains(t, results[0].Message, "90%")
}

func TestPerModelInferenceLatencyCheck_MixedStatus(t *testing.T) {
	t.Parallel()
	check := NewPerModelInferenceLatencyCheck(func() []ModelInferenceInfo {
		return []ModelInferenceInfo{
			{ModelID: "BirdNET_V2.4", ModelName: "BirdNET v2.4", AvgMS: 50, P99MS: 100, WindowMS: 1500},
			{ModelID: "Perch_V2", ModelName: "Perch V2", AvgMS: 500, P99MS: 2300, WindowMS: 2500},
		}
	})

	results := check.RunMulti(t.Context())
	require.Len(t, results, 2)
	assert.Equal(t, health.StatusHealthy, results[0].Status)
	assert.Equal(t, health.StatusCritical, results[1].Status)

	aggregate := check.Run(t.Context())
	assert.Equal(t, health.StatusCritical, aggregate.Status)
}

func TestPerModelInferenceLatencyCheck_NoStats(t *testing.T) {
	t.Parallel()
	check := NewPerModelInferenceLatencyCheck(func() []ModelInferenceInfo {
		return nil
	})

	results := check.RunMulti(t.Context())
	require.Len(t, results, 1)
	assert.Equal(t, health.StatusUnknown, results[0].Status)
}

func TestPerModelInferenceLatencyCheck_NilProvider(t *testing.T) {
	t.Parallel()
	check := NewPerModelInferenceLatencyCheck(nil)
	results := check.RunMulti(t.Context())
	require.Len(t, results, 1)
	assert.Equal(t, health.StatusSkipped, results[0].Status)
}

func TestPerModelInferenceLatencyCheck_ZeroWindow(t *testing.T) {
	t.Parallel()
	check := NewPerModelInferenceLatencyCheck(func() []ModelInferenceInfo {
		return []ModelInferenceInfo{
			{ModelID: "Test", ModelName: "Test Model", AvgMS: 100, P99MS: 200, WindowMS: 0},
		}
	})

	results := check.RunMulti(t.Context())
	require.Len(t, results, 1)
	assert.Equal(t, health.StatusUnknown, results[0].Status)
}

func TestPerModelInferenceLatencyCheck_CorrectWindowPerModel(t *testing.T) {
	t.Parallel()
	check := NewPerModelInferenceLatencyCheck(func() []ModelInferenceInfo {
		return []ModelInferenceInfo{
			// BirdNET v2.4: 3s clips, 50% overlap -> 1500ms window
			// 800ms p99 / 1500ms = 53% -> Warning
			{ModelID: "BirdNET_V2.4", ModelName: "BirdNET v2.4", AvgMS: 400, P99MS: 800, WindowMS: 1500},
			// Perch V2: 5s clips, 50% overlap -> 2500ms window
			// 800ms p99 / 2500ms = 32% -> Healthy
			{ModelID: "Perch_V2", ModelName: "Perch V2", AvgMS: 400, P99MS: 800, WindowMS: 2500},
		}
	})

	results := check.RunMulti(t.Context())
	require.Len(t, results, 2)
	assert.Equal(t, health.StatusWarning, results[0].Status, "BirdNET should be warning at 53% of window")
	assert.Equal(t, health.StatusHealthy, results[1].Status, "Perch should be healthy at 32% of window")
}

// --- ORTAvailabilityCheck tests ---

func TestORTAvailabilityCheck_Available(t *testing.T) {
	t.Parallel()
	check := NewORTAvailabilityCheck(func() (bool, bool, string, string, string) {
		return true, true, "1.25.1", "/usr/lib/libonnxruntime.so", ""
	})

	result := check.Run(t.Context())
	assert.Equal(t, health.StatusHealthy, result.Status)
	assert.Contains(t, result.Message, "1.25.1")
}

func TestORTAvailabilityCheck_Unavailable(t *testing.T) {
	t.Parallel()
	check := NewORTAvailabilityCheck(func() (bool, bool, string, string, string) {
		return false, false, "", "", "ONNX Runtime library not found"
	})

	result := check.Run(t.Context())
	assert.Equal(t, health.StatusWarning, result.Status)
	assert.Contains(t, result.Message, "not found")
}

func TestORTAvailabilityCheck_NilProvider(t *testing.T) {
	t.Parallel()
	check := NewORTAvailabilityCheck(nil)
	result := check.Run(t.Context())
	assert.Equal(t, health.StatusSkipped, result.Status)
}

func TestORTAvailabilityCheck_FoundNotInitialized(t *testing.T) {
	t.Parallel()
	check := NewORTAvailabilityCheck(func() (bool, bool, string, string, string) {
		return true, false, "1.25.1", "/usr/lib/libonnxruntime.so", ""
	})

	result := check.Run(t.Context())
	assert.Equal(t, health.StatusHealthy, result.Status)
	assert.Contains(t, result.Message, "not yet initialized")
}

// --- Helper tests ---

func TestSanitizeID(t *testing.T) {
	t.Parallel()
	tests := []struct {
		input    string
		expected string
	}{
		{"BirdNET_V2.4", "birdnet_v2_4"},
		{"Perch_V2", "perch_v2"},
		{"Bat", "bat"},
		{"My-Model.v3.0", "my_model_v3_0"},
	}
	for _, tt := range tests {
		assert.Equal(t, tt.expected, sanitizeID(tt.input), "sanitizeID(%q)", tt.input)
	}
}
