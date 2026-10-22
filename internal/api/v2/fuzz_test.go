// Package api provides fuzzing tests for API v2 security-related functions.
// These tests validate input handling against malformed and malicious inputs
// to ensure the API is hardened against security vulnerabilities.
package api

import (
	"math"
	"slices"
	"strings"
	"testing"

	"github.com/tphakala/birdnet-go/internal/api/v2/apicore"

	"github.com/stretchr/testify/assert"
)

// =============================================================================
// Fuzz Tests for Path Normalization
// =============================================================================

// FuzzNormalizeClipPathStrict tests clip path normalization with various inputs.
func FuzzNormalizeClipPathStrict(f *testing.F) {
	seedInputs := []struct {
		path   string
		prefix string
	}{
		// Valid paths
		{"clips/2024/01/bird.wav", "clips/"},
		{"2024/01/bird.wav", "clips/"},
		{"clips/", "clips/"},
		// Path traversal
		{"../etc/passwd", "clips/"},
		{"clips/../../../etc/passwd", "clips/"},
		{"..%2f..%2f..%2fetc%2fpasswd", "clips/"},
		// Absolute paths
		{"/etc/passwd", "clips/"},
		{"/absolute/path", "clips/"},
		// Backslashes
		{"clips\\2024\\01\\bird.wav", "clips/"},
		{"..\\..\\etc\\passwd", "clips/"},
		// Empty and special
		{"", "clips/"},
		{".", "clips/"},
		{"..", "clips/"},
		// Case variations
		{"CLIPS/2024/file.wav", "clips/"},
		{"Clips/2024/file.wav", "clips/"},
		// Different prefixes
		{"audio/2024/file.wav", "audio/"},
		{"data/clips/file.wav", "data/clips/"},
		// Null bytes
		{"clips/file\x00.wav", "clips/"},
		// Unicode
		{"clips/файл.wav", "clips/"},
		{"clips/鳥.wav", "clips/"},
	}

	for _, s := range seedInputs {
		f.Add(s.path, s.prefix)
	}

	f.Fuzz(func(t *testing.T, path, prefix string) {
		// Should never panic
		result, valid := apicore.NormalizeClipPathStrict(path, prefix)

		if valid {
			// Valid results should not contain path traversal
			assert.NotContains(t, result, "..", "Valid result must not contain ..")

			// Valid results should not be absolute paths
			if result != "" {
				assert.False(t, strings.HasPrefix(result, "/"), "Valid result should not be absolute")
			}
		}

		// Consistency check
		result2, valid2 := apicore.NormalizeClipPathStrict(path, prefix)
		assert.Equal(t, result, result2, "Inconsistent result")
		assert.Equal(t, valid, valid2, "Inconsistent validity")
	})
}

// FuzzNormalizeClipPath tests the backward-compatible clip path normalization.
func FuzzNormalizeClipPath(f *testing.F) {
	seedInputs := []string{
		"clips/2024/01/bird.wav",
		"2024/01/bird.wav",
		"../etc/passwd",
		"/etc/passwd",
		"",
		".",
		"..",
		"clips/../../../etc/passwd",
	}

	for _, input := range seedInputs {
		f.Add(input)
	}

	f.Fuzz(func(t *testing.T, path string) {
		// Should never panic
		result := apicore.NormalizeClipPath(path, "clips/")

		// Result should match the strict version (just without the bool)
		strictResult, _ := apicore.NormalizeClipPathStrict(path, "clips/")
		assert.Equal(t, strictResult, result, "NormalizeClipPath should match NormalizeClipPathStrict result")

		// Consistency check
		result2 := apicore.NormalizeClipPath(path, "clips/")
		assert.Equal(t, result, result2, "Inconsistent results")
	})
}

// =============================================================================
// Fuzz Tests for Parameter Parsing
// =============================================================================

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
		result := apicore.ParseConfidenceFilter(input)

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

		// Consistency check
		result2 := apicore.ParseConfidenceFilter(input)
		if result == nil {
			assert.Nil(t, result2, "Inconsistent nil result")
		} else {
			assert.NotNil(t, result2, "Inconsistent non-nil result")
			assert.Equal(t, result.Operator, result2.Operator, "Inconsistent operator")
			assert.InDelta(t, result.Value, result2.Value, 0.0001, "Inconsistent value")
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
		result := apicore.ParseHourFilter(input)

		if result != nil {
			// Hours must be in valid range
			assert.GreaterOrEqual(t, result.Start, 0, "Start hour must be >= 0")
			assert.LessOrEqual(t, result.Start, 23, "Start hour must be <= 23")
			assert.GreaterOrEqual(t, result.End, 0, "End hour must be >= 0")
			assert.LessOrEqual(t, result.End, 23, "End hour must be <= 23")

			// Start must not be greater than End
			assert.LessOrEqual(t, result.Start, result.End, "Start must be <= End")
		}

		// Consistency check
		result2 := apicore.ParseHourFilter(input)
		if result == nil {
			assert.Nil(t, result2, "Inconsistent nil result")
		} else {
			assert.NotNil(t, result2, "Inconsistent non-nil result")
			assert.Equal(t, result.Start, result2.Start, "Inconsistent start")
			assert.Equal(t, result.End, result2.End, "Inconsistent end")
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
		result := apicore.ParseDateRangeFilter(single, start, end)

		if result != nil {
			// Start must not be after End
			assert.False(t, result.Start.After(result.End), "Start must not be after End")
		}

		// Consistency check
		result2 := apicore.ParseDateRangeFilter(single, start, end)
		if result == nil {
			assert.Nil(t, result2, "Inconsistent nil result")
		} else {
			assert.NotNil(t, result2, "Inconsistent non-nil result")
			assert.True(t, result.Start.Equal(result2.Start), "Inconsistent start")
			assert.True(t, result.End.Equal(result2.End), "Inconsistent end")
		}
	})
}

// TestAdvancedPathTraversalAttacksAPIv2 tests path traversal attempts specific to API v2.
func TestAdvancedPathTraversalAttacksAPIv2(t *testing.T) {
	t.Parallel()

	attacks := []struct {
		name     string
		path     string
		expected bool // should be valid?
	}{
		// Standard traversal
		{"Simple traversal", "../etc/passwd", false},
		{"Nested traversal", "clips/../../../etc/passwd", false},
		{"Encoded traversal", "..%2f..%2fetc/passwd", false},

		// Windows-style paths
		{"Windows absolute", "C:\\Windows\\System32", false},
		{"Windows UNC", "\\\\server\\share", false},
		{"Mixed backslash", "clips\\..\\..\\etc", false},

		// Null byte injection
		{"Null in path", "clips/file\x00.wav", false},

		// Valid paths
		{"Simple file", "2024/01/bird.wav", true},
		{"Nested valid", "audio/clips/bird.wav", true},
	}

	for _, tc := range attacks {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			_, valid := apicore.NormalizeClipPathStrict(tc.path, "clips/")
			assert.Equal(t, tc.expected, valid, "Path: %q", tc.path)
		})
	}
}
