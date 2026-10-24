package checks

import (
	"testing"

	"github.com/stretchr/testify/assert"

	"github.com/tphakala/birdnet-go/internal/health"
)

// TestAcousticModelsCheck covers the aggregate acoustic-model verdict: N = 0 is a
// supported state (Warning, not Critical), a failed load is Critical, and a missing
// provider is Skipped (model de-privilege epic, Phase 4).
func TestAcousticModelsCheck(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name        string
		info        AcousticModelsInfo
		wantStatus  health.Status
		wantMessage string
	}{
		{
			name:        "loaded model is healthy",
			info:        AcousticModelsInfo{State: acousticStateOK, LoadedCount: 2, EnabledCount: 2},
			wantStatus:  health.StatusHealthy,
			wantMessage: "acoustic model(s) loaded",
		},
		{
			name:        "no model enabled or loaded is a warning, not a fault",
			info:        AcousticModelsInfo{State: acousticStateNoneInstalled},
			wantStatus:  health.StatusWarning,
			wantMessage: "No acoustic model enabled or loaded",
		},
		{
			name:        "a failed load is critical",
			info:        AcousticModelsInfo{State: acousticStateLoadFailed, EnabledCount: 1, LoadFailures: 1},
			wantStatus:  health.StatusCritical,
			wantMessage: "failed to load",
		},
		{
			name:        "an unknown state is reported as unknown",
			info:        AcousticModelsInfo{State: "whoops"},
			wantStatus:  health.StatusUnknown,
			wantMessage: "Unknown acoustic model state",
		},
		{
			name:        "an empty state is unavailable",
			info:        AcousticModelsInfo{State: ""},
			wantStatus:  health.StatusUnknown,
			wantMessage: "unavailable",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			check := NewAcousticModelsCheck(func() AcousticModelsInfo { return tt.info })
			assert.Equal(t, "acoustic_models", check.Name())
			assert.Equal(t, health.CategoryAnalysis, check.Category())

			result := check.Run(t.Context())
			assert.Equal(t, tt.wantStatus, result.Status)
			assert.Contains(t, result.Message, tt.wantMessage)
			assert.Equal(t, tt.info.State, result.Details["state"])
			assert.Equal(t, tt.info.LoadedCount, result.Details["loaded"])
			assert.Equal(t, tt.info.EnabledCount, result.Details["enabled"])
			assert.Equal(t, tt.info.LoadFailures, result.Details["load_failures"])
		})
	}
}

// TestAcousticModelsCheck_NoProvider verifies the check is skipped when no provider is
// wired (before the analysis pipeline is ready), like the other analysis checks.
func TestAcousticModelsCheck_NoProvider(t *testing.T) {
	t.Parallel()
	check := NewAcousticModelsCheck(nil)
	result := check.Run(t.Context())
	assert.Equal(t, health.StatusSkipped, result.Status)
}
