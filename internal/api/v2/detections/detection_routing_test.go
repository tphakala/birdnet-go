package detections

import (
	"reflect"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/tphakala/birdnet-go/internal/api/v2/apicore"
)

func TestNeedsAdvancedRouting(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name     string
		params   detectionQueryParams
		expected bool
	}{
		// --- Basic cases: no filters should not trigger advanced routing ---
		{
			name:     "empty params",
			params:   detectionQueryParams{},
			expected: false,
		},
		{
			name:     "hourly with just date and hour",
			params:   detectionQueryParams{QueryType: queryTypeHourly, Date: "2025-06-01", Hour: "10"},
			expected: false,
		},
		{
			name:     "species with just species and date",
			params:   detectionQueryParams{QueryType: queryTypeSpecies, Species: "Turdus merula", Date: "2025-06-01"},
			expected: false,
		},
		{
			name:     "search with just search term",
			params:   detectionQueryParams{QueryType: queryTypeSearch, Search: "robin"},
			expected: false,
		},

		// --- Advanced filters always trigger routing ---
		{
			name:     "confidence triggers advanced",
			params:   detectionQueryParams{Confidence: "0.8"},
			expected: true,
		},
		{
			name:     "timeOfDay triggers advanced",
			params:   detectionQueryParams{TimeOfDay: "morning"},
			expected: true,
		},
		{
			name:     "verified triggers advanced",
			params:   detectionQueryParams{Verified: "correct"},
			expected: true,
		},
		{
			name:     "location triggers advanced",
			params:   detectionQueryParams{Location: "backyard"},
			expected: true,
		},
		{
			name:     "source triggers advanced",
			params:   detectionQueryParams{Source: "north"},
			expected: true,
		},
		{
			name:     "locked triggers advanced",
			params:   detectionQueryParams{Locked: "true"},
			expected: true,
		},
		{
			name:     "hourRange triggers advanced",
			params:   detectionQueryParams{HourRange: "6-18"},
			expected: true,
		},
		{
			name:     "startDate triggers advanced",
			params:   detectionQueryParams{StartDate: "2025-01-01"},
			expected: true,
		},
		{
			name:     "endDate triggers advanced",
			params:   detectionQueryParams{EndDate: "2025-12-31"},
			expected: true,
		},

		// --- Sort behavior ---
		{
			name:     "non-default sort triggers advanced for default queryType",
			params:   detectionQueryParams{SortBy: "confidence_desc"},
			expected: true,
		},
		{
			name:     "date_desc sort does not trigger advanced for default queryType",
			params:   detectionQueryParams{SortBy: sortByDateDesc},
			expected: false,
		},
		{
			name:     "any sort triggers advanced for hourly",
			params:   detectionQueryParams{QueryType: queryTypeHourly, SortBy: sortByDateDesc},
			expected: true,
		},

		// --- Cross-type filter checks (date/hour/species/search on non-native query types) ---

		// Species on non-species query types
		{
			name:     "species on search queryType triggers advanced",
			params:   detectionQueryParams{QueryType: queryTypeSearch, Search: "robin", Species: "Turdus merula"},
			expected: true,
		},
		{
			name:     "species on default queryType triggers advanced",
			params:   detectionQueryParams{Species: "Turdus merula"},
			expected: true,
		},
		{
			name:     "species on hourly queryType triggers advanced",
			params:   detectionQueryParams{QueryType: queryTypeHourly, Hour: "10", Species: "Turdus merula"},
			expected: true,
		},
		{
			name:     "species on species queryType does NOT trigger (handled natively)",
			params:   detectionQueryParams{QueryType: queryTypeSpecies, Species: "Turdus merula"},
			expected: false,
		},

		// Search on non-search query types
		{
			name:     "search on default queryType triggers advanced",
			params:   detectionQueryParams{Search: "robin"},
			expected: true,
		},
		{
			name:     "search on hourly queryType triggers advanced",
			params:   detectionQueryParams{QueryType: queryTypeHourly, Hour: "10", Search: "robin"},
			expected: true,
		},
		{
			name:     "search on species queryType triggers advanced",
			params:   detectionQueryParams{QueryType: queryTypeSpecies, Species: "Turdus merula", Search: "robin"},
			expected: true,
		},
		{
			name:     "search on search queryType does NOT trigger (handled natively)",
			params:   detectionQueryParams{QueryType: queryTypeSearch, Search: "robin"},
			expected: false,
		},

		// Date on query types that don't handle it natively
		{
			name:     "date on search queryType triggers advanced",
			params:   detectionQueryParams{QueryType: queryTypeSearch, Search: "robin", Date: "2025-06-01"},
			expected: true,
		},
		{
			name:     "date on default queryType triggers advanced",
			params:   detectionQueryParams{Date: "2025-06-01"},
			expected: true,
		},
		{
			name:     "date on hourly queryType does NOT trigger (handled natively)",
			params:   detectionQueryParams{QueryType: queryTypeHourly, Hour: "10", Date: "2025-06-01"},
			expected: false,
		},
		{
			name:     "date on species queryType does NOT trigger (handled natively)",
			params:   detectionQueryParams{QueryType: queryTypeSpecies, Species: "Turdus merula", Date: "2025-06-01"},
			expected: false,
		},

		// Hour on query types that don't handle it natively
		{
			name:     "hour on search queryType triggers advanced",
			params:   detectionQueryParams{QueryType: queryTypeSearch, Search: "robin", Hour: "14"},
			expected: true,
		},
		{
			name:     "hour on default queryType triggers advanced",
			params:   detectionQueryParams{Hour: "14"},
			expected: true,
		},
		{
			name:     "hour on hourly queryType does NOT trigger (handled natively)",
			params:   detectionQueryParams{QueryType: queryTypeHourly, Hour: "14"},
			expected: false,
		},
		{
			name:     "hour on species queryType does NOT trigger (handled natively)",
			params:   detectionQueryParams{QueryType: queryTypeSpecies, Species: "Turdus merula", Hour: "14"},
			expected: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			result := tt.params.needsAdvancedRouting()
			assert.Equal(t, tt.expected, result, "needsAdvancedRouting() mismatch")
		})
	}
}

// TestAdvancedPageKey_NoDelimiterCollision pins that free-text filter values that
// concatenate to the same text cannot make two different requests share a cache entry.
func TestAdvancedPageKey_NoDelimiterCollision(t *testing.T) {
	t.Parallel()
	a := detectionQueryParams{QueryType: queryTypeSearch, Location: "node", Source: "rtsp://camera", Locked: "true"}
	b := detectionQueryParams{QueryType: queryTypeSearch, Location: "node:rtsp", Source: "//camera", Locked: "true"}
	assert.NotEqual(t, a.advancedPageKey(), b.advancedPageKey())

	same := a
	assert.Equal(t, a.advancedPageKey(), same.advancedPageKey())
	same.Source = "rtsp://camera2"
	assert.NotEqual(t, a.advancedPageKey(), same.advancedPageKey(), "source must be part of the key")
}

// TestAdvancedPageKey_EveryFilterFieldChangesKey fails when a field is added to
// detectionQueryParams without being keyed. QueryType and IncludeWeather are the
// only fields buildAdvancedSearchFilters never reads, so they must not change
// the key; every other field must.
func TestAdvancedPageKey_EveryFilterFieldChangesKey(t *testing.T) {
	t.Parallel()
	base := detectionQueryParams{}
	baseKey := base.advancedPageKey()

	for field := range reflect.TypeFor[detectionQueryParams]().Fields() {
		p := base
		v := reflect.ValueOf(&p).Elem().FieldByName(field.Name)
		switch v.Kind() {
		case reflect.String:
			v.SetString("x")
		case reflect.Int:
			v.SetInt(7)
		case reflect.Bool:
			v.SetBool(true)
		case reflect.Slice:
			v.Set(reflect.ValueOf([]string{"Parus major"}))
		default:
			require.Failf(t, "unsupported field kind", "%s has kind %s: extend this test", field.Name, v.Kind())
		}

		if field.Name == "QueryType" || field.Name == "IncludeWeather" {
			assert.Equal(t, baseKey, p.advancedPageKey(), "%s must not change the key", field.Name)
			continue
		}
		assert.NotEqual(t, baseKey, p.advancedPageKey(), "%s must be part of the key", field.Name)
	}
}

// TestPageKeys_MatchDatastoreArguments pins that each simple page key carries
// exactly the arguments its datastore call uses and nothing else, so two keys
// are equal only when the loaders would query identically.
func TestPageKeys_MatchDatastoreArguments(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name string
		got  *apicore.DetectionPageKey
		want *apicore.DetectionPageKey
	}{
		{
			name: "hourly",
			got:  hourlyPageKey("2025-03-07", "08", 2, 10, 20),
			want: &apicore.DetectionPageKey{Kind: apicore.DetectionPageHourly, Date: "2025-03-07", Hour: "08", Duration: 2, Limit: 10, Offset: 20},
		},
		{
			name: "species",
			got:  speciesPageKey("Parus major", "2025-03-07", "08", 2, 10, 20),
			want: &apicore.DetectionPageKey{Kind: apicore.DetectionPageSpecies, Species: "Parus major", Date: "2025-03-07", Hour: "08", Duration: 2, Limit: 10, Offset: 20},
		},
		{
			name: "search without alternatives",
			got:  searchPageKey("crow", nil, 10, 20),
			want: &apicore.DetectionPageKey{Kind: apicore.DetectionPageSearch, Search: "crow", Limit: 10, Offset: 20},
		},
		{
			name: "search with alternatives",
			got:  searchPageKey("owl", []string{"Tyto alba", "Strix aluco"}, 10, 20),
			want: &apicore.DetectionPageKey{Kind: apicore.DetectionPageSearch, Search: "owl", SearchScientific: "Tyto alba\x00Strix aluco", Limit: 10, Offset: 20},
		},
		{
			name: "empty search is the all query",
			got:  searchPageKey("", nil, 10, 20),
			want: &apicore.DetectionPageKey{Kind: apicore.DetectionPageSearch, Limit: 10, Offset: 20},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			assert.Equal(t, tt.want, tt.got)
		})
	}
	assert.NotEqual(t, hourlyPageKey("d", "h", 1, 1, 0), speciesPageKey("", "d", "h", 1, 1, 0), "kinds never share a key")
}
