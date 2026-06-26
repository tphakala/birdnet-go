// Package api provides fuzzing tests for API v2 security-related functions.
// These tests validate input handling against malformed and malicious inputs
// to ensure the API is hardened against security vulnerabilities.
package api

import (
	"fmt"
	"math"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"unicode/utf8"

	authapi "github.com/tphakala/birdnet-go/internal/api/v2/auth"

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
		result, valid := NormalizeClipPathStrict(path, prefix)

		if valid {
			// Valid results should not contain path traversal
			assert.NotContains(t, result, "..", "Valid result must not contain ..")

			// Valid results should not be absolute paths
			if result != "" {
				assert.False(t, strings.HasPrefix(result, "/"), "Valid result should not be absolute")
			}
		}

		// Consistency check
		result2, valid2 := NormalizeClipPathStrict(path, prefix)
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
		result := NormalizeClipPath(path, "clips/")

		// Result should match the strict version (just without the bool)
		strictResult, _ := NormalizeClipPathStrict(path, "clips/")
		assert.Equal(t, strictResult, result, "NormalizeClipPath should match NormalizeClipPathStrict result")

		// Consistency check
		result2 := NormalizeClipPath(path, "clips/")
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
		result := parseConfidenceFilter(input)

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
		result2 := parseConfidenceFilter(input)
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
		result := parseHourFilter(input)

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
		result2 := parseHourFilter(input)
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
		result := parseDateRangeFilter(single, start, end)

		if result != nil {
			// Start must not be after End
			assert.False(t, result.Start.After(result.End), "Start must not be after End")
		}

		// Consistency check
		result2 := parseDateRangeFilter(single, start, end)
		if result == nil {
			assert.Nil(t, result2, "Inconsistent nil result")
		} else {
			assert.NotNil(t, result2, "Inconsistent non-nil result")
			assert.True(t, result.Start.Equal(result2.Start), "Inconsistent start")
			assert.True(t, result.End.Equal(result2.End), "Inconsistent end")
		}
	})
}

// =============================================================================
// Security Invariant Tests with Malformed Input
// =============================================================================

// TestSecurityInvariantsWithMalformedInput tests that API v2 functions
// handle malformed inputs without panicking or exposing vulnerabilities.
func TestSecurityInvariantsWithMalformedInput(t *testing.T) {
	t.Parallel()

	// Collection of potentially problematic inputs
	malformedInputs := []string{
		// Null bytes
		"\x00",
		"test\x00test",
		string([]byte{0x00, 0x01, 0x02}),
		// Unicode edge cases
		"\uFEFF",                   // BOM
		"\u202E",                   // RTL override
		"\u0000",                   // Null
		"\uFFFF",                   // Invalid
		string([]byte{0xC0, 0xAF}), // Overlong encoding
		// Control characters
		"\x01\x02\x03\x04\x05",
		"\x1B[31m", // ANSI escape
		// Very long inputs
		strings.Repeat("a", 1<<16),
		strings.Repeat("/", 1<<16),
		// Repeated special patterns
		strings.Repeat("..", 1000),
		strings.Repeat("%00", 1000),
		strings.Repeat("\r\n", 1000),
	}

	for i, input := range malformedInputs {
		idx := i // capture index for unique test names

		// Test isValidBasePath
		t.Run(fmt.Sprintf("isValidBasePath/%d", idx), func(t *testing.T) {
			t.Parallel()
			// Should not panic
			result := authapi.IsValidBasePath(input)
			// Most malformed inputs should be rejected
			// Use filepath.IsLocal for comprehensive path validation
			if strings.Contains(input, "\x00") || !filepath.IsLocal(strings.TrimPrefix(input, "/")) {
				assert.False(t, result, "Should reject input with null/traversal")
			}
		})

		// Test validateAndSanitizeRedirect
		t.Run(fmt.Sprintf("validateAndSanitizeRedirect/%d", idx), func(t *testing.T) {
			t.Parallel()
			// Should not panic and should return safe default
			result := authapi.ValidateAndSanitizeRedirect(input)
			assert.True(t, strings.HasPrefix(result, "/"), "Should return path starting with /")
		})

		// Test containsCRLFCharacters
		t.Run(fmt.Sprintf("containsCRLFCharacters/%d", idx), func(t *testing.T) {
			t.Parallel()
			// Should not panic
			_ = authapi.ContainsCRLFCharacters(input)
		})

		// Test parseConfidenceFilter
		t.Run(fmt.Sprintf("parseConfidenceFilter/%d", idx), func(t *testing.T) {
			t.Parallel()
			// Should not panic
			_ = parseConfidenceFilter(input)
		})

		// Test parseHourFilter
		t.Run(fmt.Sprintf("parseHourFilter/%d", idx), func(t *testing.T) {
			t.Parallel()
			// Should not panic
			_ = parseHourFilter(input)
		})

		// Test NormalizeClipPathStrict
		t.Run(fmt.Sprintf("NormalizeClipPathStrict/%d", idx), func(t *testing.T) {
			t.Parallel()
			// Should not panic
			_, _ = NormalizeClipPathStrict(input, "clips/")
		})
	}
}

// TestInvalidUTF8Handling tests that invalid UTF-8 sequences don't cause issues.
func TestInvalidUTF8Handling(t *testing.T) {
	t.Parallel()

	invalidUTF8 := [][]byte{
		{0xC0, 0xAF},             // Overlong encoding of '/'
		{0xC0, 0xAE},             // Overlong encoding of '.'
		{0xE0, 0x80, 0xAF},       // Overlong encoding
		{0xF0, 0x80, 0x80, 0xAF}, // Overlong encoding
		{0xFE, 0xFE, 0xFF, 0xFF}, // Invalid sequence
		{0x80, 0x81, 0x82},       // Continuation bytes without start
	}

	for i, invalid := range invalidUTF8 {
		input := "/" + string(invalid) + "/"
		t.Run(string(rune('A'+i)), func(t *testing.T) {
			// Verify it's actually invalid UTF-8
			assert.False(t, utf8.ValidString(input), "Test input should be invalid UTF-8")

			// Functions should handle without panic
			_ = authapi.IsValidBasePath(input)
			_ = authapi.ValidateAndSanitizeRedirect(input)
			_ = authapi.ContainsCRLFCharacters(input)
			_, _ = NormalizeClipPathStrict(input, "clips/")
		})
	}
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
			_, valid := NormalizeClipPathStrict(tc.path, "clips/")
			assert.Equal(t, tc.expected, valid, "Path: %q", tc.path)
		})
	}
}
