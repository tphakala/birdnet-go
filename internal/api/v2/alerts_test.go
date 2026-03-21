package api

import (
	"math"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestValidateEscalationSteps(t *testing.T) {
	tests := []struct {
		name       string
		steps      []float64
		wantErr    string
		wantSorted []float64
	}{
		{
			name:  "nil is valid (no escalation)",
			steps: nil,
		},
		{
			name:    "empty slice is rejected",
			steps:   []float64{},
			wantErr: "must be nil (no escalation) or a non-empty array",
		},
		{
			name:    "negative value rejected",
			steps:   []float64{85, -1, 95},
			wantErr: "must not contain negative values, got -1",
		},
		{
			name:    "duplicate values rejected",
			steps:   []float64{85, 90, 85},
			wantErr: "must not contain duplicates, got 85 twice",
		},
		{
			name:    "NaN is rejected",
			steps:   []float64{85, math.NaN(), 95},
			wantErr: "must contain finite numbers",
		},
		{
			name:    "positive Inf is rejected",
			steps:   []float64{85, math.Inf(1)},
			wantErr: "must contain finite numbers",
		},
		{
			name:    "negative Inf is rejected",
			steps:   []float64{math.Inf(-1), 90},
			wantErr: "must contain finite numbers",
		},
		{
			name:       "single step is valid",
			steps:      []float64{90},
			wantSorted: []float64{90},
		},
		{
			name:       "already sorted stays sorted",
			steps:      []float64{85, 90, 95},
			wantSorted: []float64{85, 90, 95},
		},
		{
			name:       "unsorted gets sorted ascending",
			steps:      []float64{95, 85, 90},
			wantSorted: []float64{85, 90, 95},
		},
		{
			name:       "zero is valid",
			steps:      []float64{0, 50, 100},
			wantSorted: []float64{0, 50, 100},
		},
		{
			name:       "fractional values are valid",
			steps:      []float64{99.5, 85.5, 90.1},
			wantSorted: []float64{85.5, 90.1, 99.5},
		},
		{
			name:    "duplicate zeros rejected",
			steps:   []float64{0, 85, 0},
			wantErr: "must not contain duplicates, got 0 twice",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			// Copy input so we can verify sorting without affecting other tests
			var input []float64
			if tt.steps != nil {
				input = make([]float64, len(tt.steps))
				copy(input, tt.steps)
			}

			err := validateEscalationSteps(input)

			if tt.wantErr != "" {
				require.Error(t, err)
				assert.Contains(t, err.Error(), tt.wantErr)
				return
			}

			require.NoError(t, err)
			if tt.wantSorted != nil {
				assert.Equal(t, tt.wantSorted, input, "steps should be sorted ascending after validation")
			}
		})
	}
}
