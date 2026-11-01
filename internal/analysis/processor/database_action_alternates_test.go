package processor

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/tphakala/birdnet-go/internal/conf"
	"github.com/tphakala/birdnet-go/internal/detection"
)

// alt builds an AdditionalResult for the storedAlternates tests.
func alt(sci string, confidence float64, outOfRange bool) detection.AdditionalResult {
	return detection.AdditionalResult{
		Species:    detection.Species{ScientificName: sci},
		Confidence: confidence,
		OutOfRange: outOfRange,
	}
}

func altSpecies(rows []detection.AdditionalResult) []string {
	names := make([]string, len(rows))
	for i, r := range rows {
		names[i] = r.Species.ScientificName
	}
	return names
}

func TestStoredAlternates(t *testing.T) {
	t.Parallel()

	const (
		weak   = 0.05
		strong = 0.5
	)
	tests := []struct {
		name          string
		input         []detection.AdditionalResult
		minConfidence float64
		want          []string
	}{
		{
			name:          "drops below the cutoff and keeps above it",
			input:         []detection.AdditionalResult{alt("A", weak, false), alt("B", strong, false)},
			minConfidence: 0.1,
			want:          []string{"B"},
		},
		{
			name:          "keeps a row exactly at a non-dyadic cutoff",
			input:         []detection.AdditionalResult{alt("A", float64(float32(0.35)), false)},
			minConfidence: 0.35,
			want:          []string{"A"},
		},
		{
			name:          "zero disables the cutoff",
			input:         []detection.AdditionalResult{alt("A", 0.001, false)},
			minConfidence: 0,
			want:          []string{"A"},
		},
		{
			name:          "negative disables the cutoff",
			input:         []detection.AdditionalResult{alt("A", 0.001, false)},
			minConfidence: -0.5,
			want:          []string{"A"},
		},
		{
			name:          "drops out-of-range rows regardless of confidence",
			input:         []detection.AdditionalResult{alt("A", strong, true), alt("B", strong, false)},
			minConfidence: 0,
			want:          []string{"B"},
		},
		{
			name:          "range drop and cutoff both apply",
			input:         []detection.AdditionalResult{alt("A", strong, true), alt("B", weak, false), alt("C", strong, false)},
			minConfidence: 0.1,
			want:          []string{"C"},
		},
		{
			name:          "no input yields no rows",
			input:         nil,
			minConfidence: 0.1,
			want:          []string{},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			assert.Equal(t, tt.want, altSpecies(storedAlternates(tt.input, tt.minConfidence)))
		})
	}

	t.Run("leaves the input untouched", func(t *testing.T) {
		t.Parallel()
		input := []detection.AdditionalResult{alt("A", weak, false), alt("B", strong, true), alt("C", strong, false)}
		_ = storedAlternates(input, 0.1)
		assert.Equal(t, []string{"A", "B", "C"}, altSpecies(input))
	})
}

// newAlternatesAction builds a DatabaseAction whose settings snapshot carries
// the given cutoff.
func newAlternatesAction(cutoff float64, rows []detection.AdditionalResult) *DatabaseAction {
	settings := &conf.Settings{}
	settings.BirdNET.AlternateMinConfidence = cutoff
	det := testDetection()
	return &DatabaseAction{
		Settings: settings,
		Result:   det.Result,
		Results:  rows,
	}
}

func TestDatabaseAction_SaveDetection_StoresTrimmedAlternates(t *testing.T) {
	t.Parallel()

	const (
		weak   = 0.05
		strong = 0.5
		cutoff = 0.1
	)
	rows := []detection.AdditionalResult{alt("Weak", weak, false), alt("OutOfRange", strong, true), alt("Kept", strong, false)}

	t.Run("repository path receives only the stored rows and the action keeps the full list", func(t *testing.T) {
		t.Parallel()
		repo := NewMockDetectionRepository()
		action := newAlternatesAction(cutoff, rows)
		action.Repo = repo

		require.NoError(t, action.saveDetection(t.Context()))
		assert.Equal(t, []string{"Kept"}, altSpecies(repo.GetLastSavedAdditionalResults()))
		assert.Equal(t, []string{"Weak", "OutOfRange", "Kept"}, altSpecies(action.Results), "a retry must see the same input")

		// A second attempt stores the same rows.
		require.NoError(t, action.saveDetection(t.Context()))
		assert.Equal(t, []string{"Kept"}, altSpecies(repo.GetLastSavedAdditionalResults()))
	})

	t.Run("an action without a settings snapshot stores every in-range row", func(t *testing.T) {
		t.Parallel()
		repo := NewMockDetectionRepository()
		action := newAlternatesAction(cutoff, rows)
		action.Settings = nil
		action.Repo = repo

		require.NoError(t, action.saveDetection(t.Context()))
		assert.Equal(t, []string{"Weak", "Kept"}, altSpecies(repo.GetLastSavedAdditionalResults()))
	})

	t.Run("legacy datastore fallback receives the same rows", func(t *testing.T) {
		t.Parallel()
		mockDs := NewActionMockDatastore()
		action := newAlternatesAction(cutoff, rows)
		action.Ds = mockDs

		require.NoError(t, action.saveDetection(t.Context()))
		saved := mockDs.GetLastSavedResults()
		require.Len(t, saved, 1)
		assert.Contains(t, saved[0].Species, "Kept")
	})
}
