// query_filters_fuzz_test.go: Fuzz tests for query filter parsing.

package apicore

import (
	"math"
	"slices"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// FuzzParseConfidenceFilter tests confidence filter parsing.
func FuzzParseConfidenceFilter(f *testing.F) {
	seedInputs := []string{
		// Valid values
		"50",
		"0",
		"100",
		">=50",
		"<=75",
		">50",
		"<75",
		"=50",
		// Invalid values
		"",
		"-1",
		"101",
		"abc",
		"NaN",
		"Inf",
		"-Inf",
		// Edge cases
		"0.5",
		"99.99",
		">=0",
		"<=100",
		">=-1",
		"<=101",
		// Injection attempts
		"50; DROP TABLE",
		"50<script>",
		">=50\n>=100",
		// Extremely large/small
		"9999999999999999999",
		"-9999999999999999999",
		"0.0000000001",
	}

	for _, input := range seedInputs {
		f.Add(input)
	}

	f.Fuzz(func(t *testing.T, input string) {
		// Should never panic
		result := ParseConfidenceFilter(input)

		if result != nil {
			// Valid operator
			validOps := []string{">=", "<=", ">", "<", "="}
			assert.True(t, slices.Contains(validOps, result.Operator), "Operator must be valid: %s", result.Operator)

			// Value must be in valid range (0.0 to 1.0 after division)
			assert.GreaterOrEqual(t, result.Value, 0.0, "Value must be >= 0")
			assert.LessOrEqual(t, result.Value, 1.0, "Value must be <= 1.0")

			// Value must not be NaN
			assert.False(t, math.IsNaN(result.Value), "Value must not be NaN")
		}
	})
}

// FuzzParseHourFilter tests hour filter parsing.
func FuzzParseHourFilter(f *testing.F) {
	seedInputs := []string{
		// Valid single hours
		"0",
		"12",
		"23",
		// Valid ranges
		"0-23",
		"6-18",
		"0-0",
		"23-23",
		// Invalid values
		"",
		"-1",
		"24",
		"25",
		// Invalid ranges
		"-1-10",
		"10-24",
		"10-5", // inverted
		"abc",
		"10-abc",
		"abc-10",
		// Injection attempts
		"10; DROP TABLE",
		"10<script>",
		// Edge cases
		"0-",
		"-0",
		"10--20",
		"10-20-30",
	}

	for _, input := range seedInputs {
		f.Add(input)
	}

	f.Fuzz(func(t *testing.T, input string) {
		// Should never panic
		result := ParseHourFilter(input)

		if result != nil {
			// Hours must be in valid range
			assert.GreaterOrEqual(t, result.Start, 0, "Start hour must be >= 0")
			assert.LessOrEqual(t, result.Start, 23, "Start hour must be <= 23")
			assert.GreaterOrEqual(t, result.End, 0, "End hour must be >= 0")
			assert.LessOrEqual(t, result.End, 23, "End hour must be <= 23")

			// Start must not be greater than End
			assert.LessOrEqual(t, result.Start, result.End, "Start must be <= End")
		}

		// ParseHourFilter has no table test, so this is the only check that
		// it returns the same result for the same input.
		result2 := ParseHourFilter(input)
		if result == nil {
			assert.Nil(t, result2, "Inconsistent nil result")
		} else {
			require.NotNil(t, result2, "Inconsistent non-nil result")
			assert.Equal(t, *result, *result2, "Inconsistent result")
		}
	})
}

// FuzzParseDateRangeFilter tests date range filter parsing.
func FuzzParseDateRangeFilter(f *testing.F) {
	type testCase struct {
		single string
		start  string
		end    string
	}

	seeds := []testCase{
		// Valid single dates
		{"2024-01-15", "", ""},
		{"today", "", ""},
		{"yesterday", "", ""},
		// Valid ranges
		{"", "2024-01-01", "2024-01-31"},
		{"", "2024-01-01", "2024-12-31"},
		// Invalid dates
		{"invalid", "", ""},
		{"2024-13-01", "", ""},
		{"2024-01-32", "", ""},
		{"", "2024-01-01", "invalid"},
		{"", "invalid", "2024-01-01"},
		// Empty
		{"", "", ""},
		// Injection attempts
		{"2024-01-01; DROP TABLE", "", ""},
		{"", "2024-01-01<script>", "2024-01-31"},
	}

	for _, s := range seeds {
		f.Add(s.single, s.start, s.end)
	}

	f.Fuzz(func(t *testing.T, single, start, end string) {
		// Should never panic
		result := ParseDateRangeFilter(single, start, end)

		if result != nil {
			// Start must not be after End
			assert.False(t, result.Start.After(result.End), "Start must not be after End")
		}
	})
}
