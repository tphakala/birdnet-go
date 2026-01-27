package repository

import (
	"context"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/tphakala/birdnet-go/internal/datastore"
	"github.com/tphakala/birdnet-go/internal/datastore/v2/entities"
)

// =============================================================================
// DateRangeToUnix Tests
// =============================================================================

func TestDateRangeToUnix(t *testing.T) {
	tz := time.UTC

	t.Run("nil input returns nil", func(t *testing.T) {
		start, end := DateRangeToUnix(nil, tz)
		assert.Nil(t, start)
		assert.Nil(t, end)
	})

	t.Run("valid date range", func(t *testing.T) {
		dr := &datastore.DateRange{
			Start: time.Date(2024, 6, 15, 0, 0, 0, 0, tz),
			End:   time.Date(2024, 6, 17, 0, 0, 0, 0, tz),
		}

		start, end := DateRangeToUnix(dr, tz)

		require.NotNil(t, start)
		require.NotNil(t, end)

		// Start should be midnight of first day
		expectedStart := time.Date(2024, 6, 15, 0, 0, 0, 0, tz).Unix()
		assert.Equal(t, expectedStart, *start)

		// End should be 23:59:59 of last day
		expectedEnd := time.Date(2024, 6, 17, 23, 59, 59, 0, tz).Unix()
		assert.Equal(t, expectedEnd, *end)
	})

	t.Run("nil timezone uses local", func(t *testing.T) {
		dr := &datastore.DateRange{
			Start: time.Date(2024, 6, 15, 0, 0, 0, 0, time.Local),
			End:   time.Date(2024, 6, 15, 0, 0, 0, 0, time.Local),
		}

		start, end := DateRangeToUnix(dr, nil)

		require.NotNil(t, start)
		require.NotNil(t, end)
	})
}

// =============================================================================
// TimeOfDayToHours Tests
// =============================================================================

func TestTimeOfDayToHours(t *testing.T) {
	t.Run("empty input returns nil", func(t *testing.T) {
		result := TimeOfDayToHours(nil)
		assert.Nil(t, result)

		result = TimeOfDayToHours([]string{})
		assert.Nil(t, result)
	})

	t.Run("dawn returns hours 5-6", func(t *testing.T) {
		result := TimeOfDayToHours([]string{"dawn"})
		assert.ElementsMatch(t, []int{5, 6}, result)
	})

	t.Run("day returns hours 7-17", func(t *testing.T) {
		result := TimeOfDayToHours([]string{"day"})
		expected := []int{7, 8, 9, 10, 11, 12, 13, 14, 15, 16, 17}
		assert.ElementsMatch(t, expected, result)
	})

	t.Run("dusk returns hours 18-19", func(t *testing.T) {
		result := TimeOfDayToHours([]string{"dusk"})
		assert.ElementsMatch(t, []int{18, 19}, result)
	})

	t.Run("night returns hours 20-23 and 0-4", func(t *testing.T) {
		result := TimeOfDayToHours([]string{"night"})
		expected := []int{20, 21, 22, 23, 0, 1, 2, 3, 4}
		assert.ElementsMatch(t, expected, result)
	})

	t.Run("multiple periods combine", func(t *testing.T) {
		result := TimeOfDayToHours([]string{"dawn", "dusk"})
		expected := []int{5, 6, 18, 19}
		assert.ElementsMatch(t, expected, result)
	})

	t.Run("case insensitive", func(t *testing.T) {
		result := TimeOfDayToHours([]string{"DAWN", "Day"})
		assert.Contains(t, result, 5)
		assert.Contains(t, result, 7)
	})

	t.Run("unknown period ignored", func(t *testing.T) {
		result := TimeOfDayToHours([]string{"unknown"})
		assert.Nil(t, result)
	})
}

// =============================================================================
// HourFilterToHours Tests
// =============================================================================

func TestHourFilterToHours(t *testing.T) {
	t.Run("nil input returns nil", func(t *testing.T) {
		result := HourFilterToHours(nil)
		assert.Nil(t, result)
	})

	t.Run("single hour", func(t *testing.T) {
		hf := &datastore.HourFilter{Start: 9, End: 9}
		result := HourFilterToHours(hf)
		assert.Equal(t, []int{9}, result)
	})

	t.Run("normal range", func(t *testing.T) {
		hf := &datastore.HourFilter{Start: 6, End: 10}
		result := HourFilterToHours(hf)
		assert.Equal(t, []int{6, 7, 8, 9, 10}, result)
	})

	t.Run("wrap around midnight", func(t *testing.T) {
		hf := &datastore.HourFilter{Start: 22, End: 2}
		result := HourFilterToHours(hf)
		assert.Equal(t, []int{22, 23, 0, 1, 2}, result)
	})
}

// =============================================================================
// MergeHourFilters Tests
// =============================================================================

func TestMergeHourFilters(t *testing.T) {
	t.Run("both nil returns nil", func(t *testing.T) {
		result := MergeHourFilters(nil, nil)
		assert.Nil(t, result)
	})

	t.Run("only TimeOfDay provided", func(t *testing.T) {
		result := MergeHourFilters([]string{"dawn"}, nil)
		assert.ElementsMatch(t, []int{5, 6}, result)
	})

	t.Run("only Hour provided", func(t *testing.T) {
		hf := &datastore.HourFilter{Start: 8, End: 10}
		result := MergeHourFilters(nil, hf)
		assert.Equal(t, []int{8, 9, 10}, result)
	})

	t.Run("intersection when both provided", func(t *testing.T) {
		// dawn = 5, 6; hour filter = 5-7
		hf := &datastore.HourFilter{Start: 5, End: 7}
		result := MergeHourFilters([]string{"dawn"}, hf)
		// Intersection: 5, 6
		assert.ElementsMatch(t, []int{5, 6}, result)
	})

	t.Run("empty intersection returns sentinel", func(t *testing.T) {
		// dawn = 5, 6; hour filter = 10-12
		hf := &datastore.HourFilter{Start: 10, End: 12}
		result := MergeHourFilters([]string{"dawn"}, hf)
		// Should return sentinel [-1] for empty intersection
		assert.Equal(t, []int{-1}, result)
	})
}

// =============================================================================
// GetTimezoneOffset Tests
// =============================================================================

func TestGetTimezoneOffset(t *testing.T) {
	t.Run("UTC returns 0", func(t *testing.T) {
		offset := GetTimezoneOffset(time.UTC)
		assert.Equal(t, 0, offset)
	})

	t.Run("nil returns local offset", func(t *testing.T) {
		offset := GetTimezoneOffset(nil)
		_, expected := time.Now().Local().Zone()
		assert.Equal(t, expected, offset)
	})

	t.Run("fixed offset timezone", func(t *testing.T) {
		// Create a fixed +5 hours offset timezone
		loc := time.FixedZone("Test", 5*3600)
		offset := GetTimezoneOffset(loc)
		assert.Equal(t, 5*3600, offset)
	})
}

// =============================================================================
// ConfidenceFilterToMinMax Tests
// =============================================================================

func TestConfidenceFilterToMinMax(t *testing.T) {
	t.Run("nil input returns nil", func(t *testing.T) {
		minConf, maxConf := ConfidenceFilterToMinMax(nil)
		assert.Nil(t, minConf)
		assert.Nil(t, maxConf)
	})

	t.Run("greater than operator", func(t *testing.T) {
		cf := &datastore.ConfidenceFilter{Operator: ">", Value: 0.8}
		minConf, maxConf := ConfidenceFilterToMinMax(cf)
		require.NotNil(t, minConf)
		assert.Nil(t, maxConf)
		assert.InDelta(t, 0.8, *minConf, 0.0001)
	})

	t.Run("greater or equal operator", func(t *testing.T) {
		cf := &datastore.ConfidenceFilter{Operator: ">=", Value: 0.8}
		minConf, maxConf := ConfidenceFilterToMinMax(cf)
		require.NotNil(t, minConf)
		assert.Nil(t, maxConf)
		assert.InDelta(t, 0.8, *minConf, 0.0001)
	})

	t.Run("less than operator", func(t *testing.T) {
		cf := &datastore.ConfidenceFilter{Operator: "<", Value: 0.5}
		minConf, maxConf := ConfidenceFilterToMinMax(cf)
		assert.Nil(t, minConf)
		require.NotNil(t, maxConf)
		assert.InDelta(t, 0.5, *maxConf, 0.0001)
	})

	t.Run("less or equal operator", func(t *testing.T) {
		cf := &datastore.ConfidenceFilter{Operator: "<=", Value: 0.5}
		minConf, maxConf := ConfidenceFilterToMinMax(cf)
		assert.Nil(t, minConf)
		require.NotNil(t, maxConf)
		assert.InDelta(t, 0.5, *maxConf, 0.0001)
	})

	t.Run("equals operator", func(t *testing.T) {
		cf := &datastore.ConfidenceFilter{Operator: "=", Value: 0.75}
		minConf, maxConf := ConfidenceFilterToMinMax(cf)
		require.NotNil(t, minConf)
		require.NotNil(t, maxConf)
		assert.InDelta(t, 0.75, *minConf, 0.0001)
		assert.InDelta(t, 0.75, *maxConf, 0.0001)
	})

	t.Run("colon operator treated as equals", func(t *testing.T) {
		cf := &datastore.ConfidenceFilter{Operator: ":", Value: 0.75}
		minConf, maxConf := ConfidenceFilterToMinMax(cf)
		require.NotNil(t, minConf)
		require.NotNil(t, maxConf)
		assert.InDelta(t, 0.75, *minConf, 0.0001)
		assert.InDelta(t, 0.75, *maxConf, 0.0001)
	})

	t.Run("unknown operator returns nil", func(t *testing.T) {
		cf := &datastore.ConfidenceFilter{Operator: "??", Value: 0.5}
		minConf, maxConf := ConfidenceFilterToMinMax(cf)
		assert.Nil(t, minConf)
		assert.Nil(t, maxConf)
	})
}

// =============================================================================
// ConvertAdvancedFilters Tests
// =============================================================================

func TestConvertAdvancedFilters(t *testing.T) {
	ctx := context.Background()
	tz := time.UTC

	t.Run("nil filters returns empty SearchFilters", func(t *testing.T) {
		result, err := ConvertAdvancedFilters(ctx, nil, nil, tz)
		require.NoError(t, err)
		require.NotNil(t, result)
		assert.Equal(t, &SearchFilters{}, result)
	})

	t.Run("direct field mappings", func(t *testing.T) {
		locked := true
		filters := &datastore.AdvancedSearchFilters{
			TextQuery:     "robin",
			Locked:        &locked,
			Limit:         50,
			Offset:        10,
			MinID:         100,
			SortAscending: true,
		}

		result, err := ConvertAdvancedFilters(ctx, filters, nil, tz)
		require.NoError(t, err)

		assert.Equal(t, "robin", result.Query)
		assert.Equal(t, &locked, result.IsLocked)
		assert.Equal(t, 50, result.Limit)
		assert.Equal(t, 10, result.Offset)
		assert.Equal(t, uint(100), result.MinID)
		assert.Equal(t, "detected_at", result.SortBy)
		assert.False(t, result.SortDesc) // SortAscending=true → SortDesc=false
	})

	t.Run("sort descending when SortAscending is false", func(t *testing.T) {
		filters := &datastore.AdvancedSearchFilters{
			SortAscending: false,
		}

		result, err := ConvertAdvancedFilters(ctx, filters, nil, tz)
		require.NoError(t, err)
		assert.True(t, result.SortDesc)
	})

	t.Run("date range conversion", func(t *testing.T) {
		filters := &datastore.AdvancedSearchFilters{
			DateRange: &datastore.DateRange{
				Start: time.Date(2024, 6, 1, 0, 0, 0, 0, tz),
				End:   time.Date(2024, 6, 30, 0, 0, 0, 0, tz),
			},
		}

		result, err := ConvertAdvancedFilters(ctx, filters, nil, tz)
		require.NoError(t, err)

		require.NotNil(t, result.StartTime)
		require.NotNil(t, result.EndTime)

		// Verify start is beginning of June 1st
		startTime := time.Unix(*result.StartTime, 0).In(tz)
		assert.Equal(t, 2024, startTime.Year())
		assert.Equal(t, time.June, startTime.Month())
		assert.Equal(t, 1, startTime.Day())
		assert.Equal(t, 0, startTime.Hour())

		// Verify end is end of June 30th
		endTime := time.Unix(*result.EndTime, 0).In(tz)
		assert.Equal(t, 2024, endTime.Year())
		assert.Equal(t, time.June, endTime.Month())
		assert.Equal(t, 30, endTime.Day())
		assert.Equal(t, 23, endTime.Hour())
	})

	t.Run("confidence filter conversion", func(t *testing.T) {
		filters := &datastore.AdvancedSearchFilters{
			Confidence: &datastore.ConfidenceFilter{
				Operator: ">=",
				Value:    0.8,
			},
		}

		result, err := ConvertAdvancedFilters(ctx, filters, nil, tz)
		require.NoError(t, err)

		require.NotNil(t, result.MinConfidence)
		assert.InDelta(t, 0.8, *result.MinConfidence, 0.0001)
		assert.Nil(t, result.MaxConfidence)
	})

	t.Run("time of day conversion", func(t *testing.T) {
		filters := &datastore.AdvancedSearchFilters{
			TimeOfDay: []string{"dawn", "dusk"},
		}

		result, err := ConvertAdvancedFilters(ctx, filters, nil, tz)
		require.NoError(t, err)

		// dawn=5,6 + dusk=18,19
		assert.ElementsMatch(t, []int{5, 6, 18, 19}, result.IncludedHours)
	})

	t.Run("verified filter conversion", func(t *testing.T) {
		verified := true
		filters := &datastore.AdvancedSearchFilters{
			Verified: &verified,
		}

		result, err := ConvertAdvancedFilters(ctx, filters, nil, tz)
		require.NoError(t, err)

		require.NotNil(t, result.IsReviewed)
		assert.True(t, *result.IsReviewed)
	})

	t.Run("timezone offset is set", func(t *testing.T) {
		filters := &datastore.AdvancedSearchFilters{}

		result, err := ConvertAdvancedFilters(ctx, filters, nil, tz)
		require.NoError(t, err)

		// UTC should have offset 0
		assert.Equal(t, 0, result.TimezoneOffset)
	})
}

// =============================================================================
// Mock Repositories for Entity Lookup Tests
// =============================================================================

// mockLabelRepository is a simple mock for testing ResolveSpeciesToLabelIDs
type mockLabelRepository struct {
	labels map[string]*entities.Label
}

func (m *mockLabelRepository) GetByScientificName(_ context.Context, name string) (*entities.Label, error) {
	if label, ok := m.labels[name]; ok {
		return label, nil
	}
	return nil, ErrLabelNotFound
}

// Implement other interface methods as no-ops for the mock
func (m *mockLabelRepository) GetOrCreate(_ context.Context, _ string, _ entities.LabelType) (*entities.Label, error) {
	return nil, nil //nolint:nilnil // mock implementation
}
func (m *mockLabelRepository) GetByID(_ context.Context, _ uint) (*entities.Label, error) {
	return nil, nil //nolint:nilnil // mock implementation
}
func (m *mockLabelRepository) GetAllByType(_ context.Context, _ entities.LabelType) ([]*entities.Label, error) {
	return nil, nil //nolint:nilnil // mock implementation
}
func (m *mockLabelRepository) Search(_ context.Context, _ string, _ int) ([]*entities.Label, error) {
	return nil, nil //nolint:nilnil // mock implementation
}
func (m *mockLabelRepository) Count(_ context.Context) (int64, error) { return 0, nil }
func (m *mockLabelRepository) CountByType(_ context.Context, _ entities.LabelType) (int64, error) {
	return 0, nil
}
func (m *mockLabelRepository) GetAll(_ context.Context) ([]*entities.Label, error) { return nil, nil }
func (m *mockLabelRepository) Delete(_ context.Context, _ uint) error              { return nil }
func (m *mockLabelRepository) Exists(_ context.Context, _ uint) (bool, error)      { return false, nil }
func (m *mockLabelRepository) GetRawLabelForLabel(_ context.Context, _, _ uint) (string, error) {
	return "", nil
}
func (m *mockLabelRepository) GetRawLabelsForLabels(_ context.Context, _ []ModelLabelPair) (map[string]string, error) {
	return nil, nil //nolint:nilnil // mock implementation
}

// mockAudioSourceRepository is a simple mock for testing ResolveLocationsToSourceIDs
type mockAudioSourceRepository struct {
	sources map[string][]*entities.AudioSource
}

func (m *mockAudioSourceRepository) GetByNodeName(_ context.Context, nodeName string) ([]*entities.AudioSource, error) {
	if sources, ok := m.sources[nodeName]; ok {
		return sources, nil
	}
	return []*entities.AudioSource{}, nil
}

// Implement other interface methods as no-ops for the mock
func (m *mockAudioSourceRepository) GetOrCreate(_ context.Context, _, _ string, _ *string, _ entities.SourceType) (*entities.AudioSource, error) {
	return nil, nil //nolint:nilnil // mock implementation
}
func (m *mockAudioSourceRepository) GetByID(_ context.Context, _ uint) (*entities.AudioSource, error) {
	return nil, nil //nolint:nilnil // mock implementation
}
func (m *mockAudioSourceRepository) GetBySourceURI(_ context.Context, _, _ string) (*entities.AudioSource, error) {
	return nil, nil //nolint:nilnil // mock implementation
}
func (m *mockAudioSourceRepository) GetAll(_ context.Context) ([]*entities.AudioSource, error) {
	return nil, nil //nolint:nilnil // mock implementation
}
func (m *mockAudioSourceRepository) Count(_ context.Context) (int64, error)           { return 0, nil }
func (m *mockAudioSourceRepository) Delete(_ context.Context, _ uint) error           { return nil }
func (m *mockAudioSourceRepository) Update(_ context.Context, _ uint, _ map[string]any) error {
	return nil
}
func (m *mockAudioSourceRepository) Exists(_ context.Context, _ uint) (bool, error) { return false, nil }

// =============================================================================
// ResolveSpeciesToLabelIDs Tests
// =============================================================================

func TestResolveSpeciesToLabelIDs(t *testing.T) {
	ctx := context.Background()

	t.Run("empty input returns nil", func(t *testing.T) {
		deps := &FilterLookupDeps{
			LabelRepo: &mockLabelRepository{labels: map[string]*entities.Label{}},
		}

		result, err := ResolveSpeciesToLabelIDs(ctx, deps, nil)
		require.NoError(t, err)
		assert.Nil(t, result)

		result, err = ResolveSpeciesToLabelIDs(ctx, deps, []string{})
		require.NoError(t, err)
		assert.Nil(t, result)
	})

	t.Run("nil deps returns nil", func(t *testing.T) {
		result, err := ResolveSpeciesToLabelIDs(ctx, nil, []string{"Turdus merula"})
		require.NoError(t, err)
		assert.Nil(t, result)
	})

	t.Run("found species returns label IDs", func(t *testing.T) {
		labelRepo := &mockLabelRepository{
			labels: map[string]*entities.Label{
				"Turdus merula":    {ID: 1},
				"Parus major":      {ID: 2},
				"Erithacus rubecula": {ID: 3},
			},
		}
		deps := &FilterLookupDeps{LabelRepo: labelRepo}

		result, err := ResolveSpeciesToLabelIDs(ctx, deps, []string{"Turdus merula", "Parus major"})
		require.NoError(t, err)
		assert.ElementsMatch(t, []uint{1, 2}, result)
	})

	t.Run("unknown species returns sentinel", func(t *testing.T) {
		labelRepo := &mockLabelRepository{labels: map[string]*entities.Label{}}
		deps := &FilterLookupDeps{LabelRepo: labelRepo}

		result, err := ResolveSpeciesToLabelIDs(ctx, deps, []string{"Unknown species"})
		require.NoError(t, err)
		// Should return sentinel [0] to ensure zero results
		assert.Equal(t, []uint{0}, result)
	})

	t.Run("mixed found and not found", func(t *testing.T) {
		labelRepo := &mockLabelRepository{
			labels: map[string]*entities.Label{
				"Turdus merula": {ID: 1},
			},
		}
		deps := &FilterLookupDeps{LabelRepo: labelRepo}

		result, err := ResolveSpeciesToLabelIDs(ctx, deps, []string{"Turdus merula", "Unknown species"})
		require.NoError(t, err)
		// Should return only the found one
		assert.Equal(t, []uint{1}, result)
	})
}

// =============================================================================
// ResolveLocationsToSourceIDs Tests
// =============================================================================

func TestResolveLocationsToSourceIDs(t *testing.T) {
	ctx := context.Background()

	t.Run("empty input returns nil", func(t *testing.T) {
		deps := &FilterLookupDeps{
			SourceRepo: &mockAudioSourceRepository{sources: map[string][]*entities.AudioSource{}},
		}

		result, err := ResolveLocationsToSourceIDs(ctx, deps, nil)
		require.NoError(t, err)
		assert.Nil(t, result)

		result, err = ResolveLocationsToSourceIDs(ctx, deps, []string{})
		require.NoError(t, err)
		assert.Nil(t, result)
	})

	t.Run("nil deps returns nil", func(t *testing.T) {
		result, err := ResolveLocationsToSourceIDs(ctx, nil, []string{"node1"})
		require.NoError(t, err)
		assert.Nil(t, result)
	})

	t.Run("found locations returns source IDs", func(t *testing.T) {
		sourceRepo := &mockAudioSourceRepository{
			sources: map[string][]*entities.AudioSource{
				"node1": {{ID: 1}, {ID: 2}}, // Two sources on node1
				"node2": {{ID: 3}},
			},
		}
		deps := &FilterLookupDeps{SourceRepo: sourceRepo}

		result, err := ResolveLocationsToSourceIDs(ctx, deps, []string{"node1", "node2"})
		require.NoError(t, err)
		assert.ElementsMatch(t, []uint{1, 2, 3}, result)
	})

	t.Run("unknown location returns sentinel", func(t *testing.T) {
		sourceRepo := &mockAudioSourceRepository{sources: map[string][]*entities.AudioSource{}}
		deps := &FilterLookupDeps{SourceRepo: sourceRepo}

		result, err := ResolveLocationsToSourceIDs(ctx, deps, []string{"unknown-node"})
		require.NoError(t, err)
		// Should return sentinel [0] to ensure zero results
		assert.Equal(t, []uint{0}, result)
	})

	t.Run("mixed found and not found", func(t *testing.T) {
		sourceRepo := &mockAudioSourceRepository{
			sources: map[string][]*entities.AudioSource{
				"node1": {{ID: 5}},
			},
		}
		deps := &FilterLookupDeps{SourceRepo: sourceRepo}

		result, err := ResolveLocationsToSourceIDs(ctx, deps, []string{"node1", "unknown-node"})
		require.NoError(t, err)
		// Should return only the found one
		assert.Equal(t, []uint{5}, result)
	})
}

// =============================================================================
// singleTimeOfDayToHours Tests
// =============================================================================

func TestSingleTimeOfDayToHours(t *testing.T) {
	t.Run("empty or any returns nil", func(t *testing.T) {
		assert.Nil(t, singleTimeOfDayToHours(""))
		assert.Nil(t, singleTimeOfDayToHours("any"))
		assert.Nil(t, singleTimeOfDayToHours("ANY"))
	})

	t.Run("day returns hours 6-18", func(t *testing.T) {
		result := singleTimeOfDayToHours("day")
		expected := []int{6, 7, 8, 9, 10, 11, 12, 13, 14, 15, 16, 17, 18}
		assert.Equal(t, expected, result)
	})

	t.Run("night returns hours 18-23 and 0-6", func(t *testing.T) {
		result := singleTimeOfDayToHours("night")
		expected := []int{18, 19, 20, 21, 22, 23, 0, 1, 2, 3, 4, 5, 6}
		assert.Equal(t, expected, result)
	})

	t.Run("sunrise returns hours 5-7", func(t *testing.T) {
		result := singleTimeOfDayToHours("sunrise")
		assert.Equal(t, []int{5, 6, 7}, result)
	})

	t.Run("sunset returns hours 17-19", func(t *testing.T) {
		result := singleTimeOfDayToHours("sunset")
		assert.Equal(t, []int{17, 18, 19}, result)
	})

	t.Run("case insensitive", func(t *testing.T) {
		assert.NotNil(t, singleTimeOfDayToHours("DAY"))
		assert.NotNil(t, singleTimeOfDayToHours("Night"))
		assert.NotNil(t, singleTimeOfDayToHours("SUNRISE"))
	})

	t.Run("unknown returns nil", func(t *testing.T) {
		assert.Nil(t, singleTimeOfDayToHours("unknown"))
		assert.Nil(t, singleTimeOfDayToHours("evening"))
	})
}

// =============================================================================
// parseDateString Tests
// =============================================================================

func TestParseDateString(t *testing.T) {
	tz := time.UTC

	t.Run("empty string returns nil", func(t *testing.T) {
		result := parseDateString("", tz, false)
		assert.Nil(t, result)
	})

	t.Run("valid date start of day", func(t *testing.T) {
		result := parseDateString("2024-06-15", tz, false)
		require.NotNil(t, result)

		ts := time.Unix(*result, 0).In(tz)
		assert.Equal(t, 2024, ts.Year())
		assert.Equal(t, time.June, ts.Month())
		assert.Equal(t, 15, ts.Day())
		assert.Equal(t, 0, ts.Hour())
		assert.Equal(t, 0, ts.Minute())
		assert.Equal(t, 0, ts.Second())
	})

	t.Run("valid date end of day", func(t *testing.T) {
		result := parseDateString("2024-06-15", tz, true)
		require.NotNil(t, result)

		ts := time.Unix(*result, 0).In(tz)
		assert.Equal(t, 2024, ts.Year())
		assert.Equal(t, time.June, ts.Month())
		assert.Equal(t, 15, ts.Day())
		assert.Equal(t, 23, ts.Hour())
		assert.Equal(t, 59, ts.Minute())
		assert.Equal(t, 59, ts.Second())
	})

	t.Run("invalid date returns nil", func(t *testing.T) {
		result := parseDateString("invalid-date", tz, false)
		assert.Nil(t, result)

		result = parseDateString("2024/06/15", tz, false) // Wrong format
		assert.Nil(t, result)
	})

	t.Run("nil timezone uses local", func(t *testing.T) {
		result := parseDateString("2024-06-15", nil, false)
		require.NotNil(t, result)
	})
}

// =============================================================================
// ConvertSearchFilters Tests
// =============================================================================

func TestConvertSearchFilters(t *testing.T) {
	ctx := context.Background()
	tz := time.UTC

	t.Run("nil filters returns empty SearchFilters", func(t *testing.T) {
		result, err := ConvertSearchFilters(ctx, nil, nil, tz)
		require.NoError(t, err)
		require.NotNil(t, result)
		assert.Equal(t, &SearchFilters{}, result)
	})

	t.Run("date range conversion", func(t *testing.T) {
		filters := &datastore.SearchFilters{
			DateStart: "2024-06-01",
			DateEnd:   "2024-06-30",
		}

		result, err := ConvertSearchFilters(ctx, filters, nil, tz)
		require.NoError(t, err)

		require.NotNil(t, result.StartTime)
		require.NotNil(t, result.EndTime)

		startTime := time.Unix(*result.StartTime, 0).In(tz)
		assert.Equal(t, 1, startTime.Day())
		assert.Equal(t, 0, startTime.Hour())

		endTime := time.Unix(*result.EndTime, 0).In(tz)
		assert.Equal(t, 30, endTime.Day())
		assert.Equal(t, 23, endTime.Hour())
	})

	t.Run("confidence range conversion", func(t *testing.T) {
		filters := &datastore.SearchFilters{
			ConfidenceMin: 0.7,
			ConfidenceMax: 0.95,
		}

		result, err := ConvertSearchFilters(ctx, filters, nil, tz)
		require.NoError(t, err)

		require.NotNil(t, result.MinConfidence)
		require.NotNil(t, result.MaxConfidence)
		assert.InDelta(t, 0.7, *result.MinConfidence, 0.0001)
		assert.InDelta(t, 0.95, *result.MaxConfidence, 0.0001)
	})

	t.Run("verified only sets correct verification filter", func(t *testing.T) {
		filters := &datastore.SearchFilters{
			VerifiedOnly: true,
		}

		result, err := ConvertSearchFilters(ctx, filters, nil, tz)
		require.NoError(t, err)

		require.NotNil(t, result.Verified)
		assert.Equal(t, VerificationFilter(entities.VerificationCorrect), *result.Verified)
		assert.Nil(t, result.IsReviewed)
	})

	t.Run("unverified only sets IsReviewed false", func(t *testing.T) {
		filters := &datastore.SearchFilters{
			UnverifiedOnly: true,
		}

		result, err := ConvertSearchFilters(ctx, filters, nil, tz)
		require.NoError(t, err)

		assert.Nil(t, result.Verified)
		require.NotNil(t, result.IsReviewed)
		assert.False(t, *result.IsReviewed)
	})

	t.Run("locked only sets IsLocked true", func(t *testing.T) {
		filters := &datastore.SearchFilters{
			LockedOnly: true,
		}

		result, err := ConvertSearchFilters(ctx, filters, nil, tz)
		require.NoError(t, err)

		require.NotNil(t, result.IsLocked)
		assert.True(t, *result.IsLocked)
	})

	t.Run("unlocked only sets IsLocked false", func(t *testing.T) {
		filters := &datastore.SearchFilters{
			UnlockedOnly: true,
		}

		result, err := ConvertSearchFilters(ctx, filters, nil, tz)
		require.NoError(t, err)

		require.NotNil(t, result.IsLocked)
		assert.False(t, *result.IsLocked)
	})

	t.Run("time of day conversion", func(t *testing.T) {
		filters := &datastore.SearchFilters{
			TimeOfDay: "sunrise",
		}

		result, err := ConvertSearchFilters(ctx, filters, nil, tz)
		require.NoError(t, err)

		assert.Equal(t, []int{5, 6, 7}, result.IncludedHours)
	})

	t.Run("sort options", func(t *testing.T) {
		tests := []struct {
			sortBy   string
			wantBy   string
			wantDesc bool
		}{
			{"date_desc", SortFieldDetectedAt, true},
			{"date_asc", SortFieldDetectedAt, false},
			{"confidence_desc", SortFieldConfidence, true},
			{"species_asc", "label_id", false},
			{"", SortFieldDetectedAt, true}, // Default
		}

		for _, tt := range tests {
			t.Run("sort_"+tt.sortBy, func(t *testing.T) {
				filters := &datastore.SearchFilters{SortBy: tt.sortBy}
				result, err := ConvertSearchFilters(ctx, filters, nil, tz)
				require.NoError(t, err)
				assert.Equal(t, tt.wantBy, result.SortBy)
				assert.Equal(t, tt.wantDesc, result.SortDesc)
			})
		}
	})

	t.Run("pagination conversion", func(t *testing.T) {
		filters := &datastore.SearchFilters{
			Page:    3,
			PerPage: 25,
		}

		result, err := ConvertSearchFilters(ctx, filters, nil, tz)
		require.NoError(t, err)

		assert.Equal(t, 25, result.Limit)
		assert.Equal(t, 50, result.Offset) // (3-1) * 25 = 50
	})

	t.Run("pagination defaults", func(t *testing.T) {
		filters := &datastore.SearchFilters{
			Page:    0, // Invalid, should default to 1
			PerPage: 0, // Invalid, should default to 20
		}

		result, err := ConvertSearchFilters(ctx, filters, nil, tz)
		require.NoError(t, err)

		assert.Equal(t, 20, result.Limit)
		assert.Equal(t, 0, result.Offset) // (1-1) * 20 = 0
	})

	t.Run("pagination max per page", func(t *testing.T) {
		filters := &datastore.SearchFilters{
			Page:    1,
			PerPage: 500, // Over max, should be capped to 20
		}

		result, err := ConvertSearchFilters(ctx, filters, nil, tz)
		require.NoError(t, err)

		assert.Equal(t, 20, result.Limit) // Capped to default
	})

	t.Run("timezone offset is set", func(t *testing.T) {
		filters := &datastore.SearchFilters{}

		result, err := ConvertSearchFilters(ctx, filters, nil, tz)
		require.NoError(t, err)

		assert.Equal(t, 0, result.TimezoneOffset) // UTC = 0
	})
}

// =============================================================================
// ResolveSpeciesToLabelIDsWithCommonName Tests
// =============================================================================

// Extended mock that also implements Search
type mockLabelRepositoryWithSearch struct {
	mockLabelRepository
	searchResults []*entities.Label
}

func (m *mockLabelRepositoryWithSearch) Search(_ context.Context, _ string, _ int) ([]*entities.Label, error) {
	return m.searchResults, nil
}

func TestResolveSpeciesToLabelIDsWithCommonName(t *testing.T) {
	ctx := context.Background()

	t.Run("empty species returns nil", func(t *testing.T) {
		deps := &FilterLookupDeps{
			LabelRepo: &mockLabelRepositoryWithSearch{},
		}

		result, err := ResolveSpeciesToLabelIDsWithCommonName(ctx, deps, "")
		require.NoError(t, err)
		assert.Nil(t, result)
	})

	t.Run("nil deps returns nil", func(t *testing.T) {
		result, err := ResolveSpeciesToLabelIDsWithCommonName(ctx, nil, "robin")
		require.NoError(t, err)
		assert.Nil(t, result)
	})

	t.Run("found species returns label IDs", func(t *testing.T) {
		deps := &FilterLookupDeps{
			LabelRepo: &mockLabelRepositoryWithSearch{
				searchResults: []*entities.Label{
					{ID: 1},
					{ID: 2},
				},
			},
		}

		result, err := ResolveSpeciesToLabelIDsWithCommonName(ctx, deps, "robin")
		require.NoError(t, err)
		assert.ElementsMatch(t, []uint{1, 2}, result)
	})

	t.Run("no results returns sentinel", func(t *testing.T) {
		deps := &FilterLookupDeps{
			LabelRepo: &mockLabelRepositoryWithSearch{
				searchResults: []*entities.Label{},
			},
		}

		result, err := ResolveSpeciesToLabelIDsWithCommonName(ctx, deps, "unknown")
		require.NoError(t, err)
		assert.Equal(t, []uint{0}, result)
	})
}

// =============================================================================
// ResolveDeviceToSourceIDs Tests
// =============================================================================

// Extended mock that also implements GetAll
type mockAudioSourceRepositoryWithGetAll struct {
	mockAudioSourceRepository
	allSources []*entities.AudioSource
}

func (m *mockAudioSourceRepositoryWithGetAll) GetAll(_ context.Context) ([]*entities.AudioSource, error) {
	return m.allSources, nil
}

func TestResolveDeviceToSourceIDs(t *testing.T) {
	ctx := context.Background()

	t.Run("empty device returns nil", func(t *testing.T) {
		deps := &FilterLookupDeps{
			SourceRepo: &mockAudioSourceRepositoryWithGetAll{},
		}

		result, err := ResolveDeviceToSourceIDs(ctx, deps, "")
		require.NoError(t, err)
		assert.Nil(t, result)
	})

	t.Run("nil deps returns nil", func(t *testing.T) {
		result, err := ResolveDeviceToSourceIDs(ctx, nil, "node1")
		require.NoError(t, err)
		assert.Nil(t, result)
	})

	t.Run("found device returns source IDs", func(t *testing.T) {
		deps := &FilterLookupDeps{
			SourceRepo: &mockAudioSourceRepositoryWithGetAll{
				allSources: []*entities.AudioSource{
					{ID: 1, NodeName: "node1"},
					{ID: 2, NodeName: "node2"},
					{ID: 3, NodeName: "mynode1"},
				},
			},
		}

		result, err := ResolveDeviceToSourceIDs(ctx, deps, "node1")
		require.NoError(t, err)
		// Should match "node1" and "mynode1" (contains "node1")
		assert.ElementsMatch(t, []uint{1, 3}, result)
	})

	t.Run("case insensitive matching", func(t *testing.T) {
		deps := &FilterLookupDeps{
			SourceRepo: &mockAudioSourceRepositoryWithGetAll{
				allSources: []*entities.AudioSource{
					{ID: 1, NodeName: "MyDevice"},
					{ID: 2, NodeName: "OTHER"},
				},
			},
		}

		result, err := ResolveDeviceToSourceIDs(ctx, deps, "mydevice")
		require.NoError(t, err)
		assert.Equal(t, []uint{1}, result)
	})

	t.Run("no matches returns sentinel", func(t *testing.T) {
		deps := &FilterLookupDeps{
			SourceRepo: &mockAudioSourceRepositoryWithGetAll{
				allSources: []*entities.AudioSource{
					{ID: 1, NodeName: "node1"},
				},
			},
		}

		result, err := ResolveDeviceToSourceIDs(ctx, deps, "unknown")
		require.NoError(t, err)
		assert.Equal(t, []uint{0}, result)
	})
}
