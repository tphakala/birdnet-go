package checks

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/tphakala/birdnet-go/internal/health"
)

func TestInferenceFailuresCheck(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name        string
		getInfo     func() []ModelInferenceFailureInfo
		want        health.Status
		wantMessage string
	}{
		{name: "nil provider", getInfo: nil, want: health.StatusSkipped},
		{
			name:        "no loaded model",
			getInfo:     func() []ModelInferenceFailureInfo { return nil },
			want:        health.StatusSkipped,
			wantMessage: "No acoustic model loaded",
		},
		{
			name: "all healthy, a short failure run is not failing",
			getInfo: func() []ModelInferenceFailureInfo {
				return []ModelInferenceFailureInfo{
					{ModelID: "a", ModelName: "Model A"},
					{ModelID: "b", ModelName: "Model B", ConsecutiveFailures: 3},
				}
			},
			want:        health.StatusHealthy,
			wantMessage: "2 loaded model(s) analyzing normally",
		},
		{
			name: "one failing model is critical and named",
			getInfo: func() []ModelInferenceFailureInfo {
				return []ModelInferenceFailureInfo{
					{ModelID: "a", ModelName: "Model A"},
					{ModelID: "b", ModelName: "Model B", ConsecutiveFailures: 40, Failing: true, ErrorClass: "non_finite_output"},
				}
			},
			want:        health.StatusCritical,
			wantMessage: "Every analysis fails for: Model B (see the AI Models page)",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			c := NewInferenceFailuresCheck(tt.getInfo)
			r := c.Run(t.Context())
			assert.Equal(t, "inference_failures", r.Name)
			assert.Equal(t, health.CategoryAnalysis, r.Category)
			assert.Equal(t, tt.want, r.Status)
			if tt.wantMessage != "" {
				assert.Equal(t, tt.wantMessage, r.Message)
			}
		})
	}
}

// TestInferenceFailuresCheck_Details pins the per-model details and the joined
// list of failing models.
func TestInferenceFailuresCheck_Details(t *testing.T) {
	t.Parallel()
	c := NewInferenceFailuresCheck(func() []ModelInferenceFailureInfo {
		return []ModelInferenceFailureInfo{
			{ModelID: "a", ModelName: "Model A", ConsecutiveFailures: 11, Failing: true, ErrorClass: "non_finite_output"},
			{ModelID: "b", ModelName: "Model B"},
			{ModelID: "c", ModelName: "Model C", ConsecutiveFailures: 30, Failing: true, ErrorClass: "inference_error"},
		}
	})
	r := c.Run(t.Context())
	assert.Equal(t, health.StatusCritical, r.Status)
	assert.Equal(t, "Every analysis fails for: Model A, Model C (see the AI Models page)", r.Message)
	assert.Equal(t, 2, r.Details["failing_count"])
	models, ok := r.Details["models"].([]map[string]any)
	require.True(t, ok)
	require.Len(t, models, 3)
	assert.Equal(t, map[string]any{
		"model_id": "c", "model_name": "Model C", "consecutive_failures": int64(30),
		"failing": true, "error_class": "inference_error",
	}, models[2])
}
