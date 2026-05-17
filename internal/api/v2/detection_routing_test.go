package api

import (
	"testing"

	"github.com/stretchr/testify/assert"
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
