package conf

import (
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestBSGBatStepValidationAndNormalize pins the bsgbat.stepframes range: the API
// path accepts 0 (unset, resolves to the default) and the inclusive
// [MinBSGBatStepFrames, MaxBSGBatStepFrames] range and rejects anything else,
// while the load path repairs a bad value to the default so boot never fails.
func TestBSGBatStepValidationAndNormalize(t *testing.T) {
	t.Run("validation range edges", func(t *testing.T) {
		tests := []struct {
			name    string
			frames  int
			wantErr bool
		}{
			{"zero means the default", 0, false},
			{"below the minimum", MinBSGBatStepFrames - 1, true},
			{"minimum", MinBSGBatStepFrames, false},
			{"default", DefaultBSGBatStepFrames, false},
			{"maximum", MaxBSGBatStepFrames, false},
			{"above the maximum", MaxBSGBatStepFrames + 1, true},
			{"negative", -1, true},
		}
		for _, tt := range tests {
			t.Run(tt.name, func(t *testing.T) {
				s := createMinimalValidSettings()
				s.BSGBat.StepFrames = tt.frames
				err := ValidateSettings(s)
				if tt.wantErr {
					require.Error(t, err)
					assert.Contains(t, err.Error(), "bsgbat.stepframes")
					return
				}
				require.NoError(t, err)
			})
		}
	})

	t.Run("normalize repairs zero silently", func(t *testing.T) {
		s := &Settings{}
		s.normalizeBSGBatStep()
		assert.Equal(t, DefaultBSGBatStepFrames, s.BSGBat.StepFrames)
		assert.Empty(t, s.ValidationWarnings)
	})

	t.Run("normalize repairs an out of range value with a warning and then validates", func(t *testing.T) {
		s := createMinimalValidSettings()
		s.BSGBat.StepFrames = MaxBSGBatStepFrames + 1
		s.normalizeBSGBatStep()
		assert.Equal(t, DefaultBSGBatStepFrames, s.BSGBat.StepFrames)
		assert.Len(t, s.ValidationWarnings, 1)
		require.NoError(t, ValidateSettings(s))
	})

	t.Run("the load pass repairs the step before validation", func(t *testing.T) {
		stepWarnings := func(s *Settings) []string {
			var out []string
			for _, w := range s.ValidationWarnings {
				if strings.Contains(w, "bsgbat stepframes") {
					out = append(out, w)
				}
			}
			return out
		}

		bad := createMinimalValidSettings()
		bad.BSGBat.StepFrames = MaxBSGBatStepFrames + 1
		normalizeIncompleteFeatures(bad)
		assert.Equal(t, DefaultBSGBatStepFrames, bad.BSGBat.StepFrames)
		assert.Len(t, stepWarnings(bad), 1)
		require.NoError(t, ValidateSettings(bad))

		unset := createMinimalValidSettings()
		unset.BSGBat.StepFrames = 0
		normalizeIncompleteFeatures(unset)
		assert.Equal(t, DefaultBSGBatStepFrames, unset.BSGBat.StepFrames)
		assert.Empty(t, stepWarnings(unset))
		require.NoError(t, ValidateSettings(unset))
	})

	t.Run("normalize keeps an in-range value", func(t *testing.T) {
		s := &Settings{}
		s.BSGBat.StepFrames = MinBSGBatStepFrames
		s.normalizeBSGBatStep()
		assert.Equal(t, MinBSGBatStepFrames, s.BSGBat.StepFrames)
		assert.Empty(t, s.ValidationWarnings)
	})
}
