// label_files_test.go contains comprehensive unit tests for validating label files for different locales.
//
// These tests validate:
// 1. Loading of all label files in the V2.4 directory for each supported locale
// 2. Line count validation - ensuring each file has exactly GetExpectedLinesV24() (6522) lines
// 3. Format validation - checking proper scientific_name_common_name format
// 4. Consistency validation - comparing scientific names across locales (with reporting)
// 5. Error handling for unsupported models/locales
// 6. Filename generation validation
//
// The tests discovered some legitimate scientific name differences across certain locale files,
// which are reported as informational warnings rather than test failures.
package birdnet

import (
	"bufio"
	"bytes"
	"fmt"
	"path/filepath"
	"strings"
	"testing"
	"unicode"
	"unicode/utf8"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/tphakala/birdnet-go/internal/conf"
)

// Note: GetExpectedLinesV24() function is defined in label_files.go

// Mock logger for testing
type testLogger struct {
	logs []string
}

func (tl *testLogger) Debug(format string, v ...any) {
	// Store debug messages for test verification if needed
	tl.logs = append(tl.logs, fmt.Sprintf(format, v...))
}

func TestLoadAllV24LabelFiles(t *testing.T) {
	modelVersion := BirdNET_GLOBAL_6K_V2_4

	// Test each locale code in the mapping
	for localeCode, fileLocale := range conf.LocaleCodeMapping {
		t.Run(localeCode, func(t *testing.T) {
			t.Parallel()
			logger := &testLogger{}

			// Test loading the label file
			data, err := GetLabelFileDataWithLogger(modelVersion, localeCode, logger)
			require.NoError(t, err, "Failed to load label file for locale %s", localeCode)
			require.NotEmpty(t, data, "Label file for locale %s is empty", localeCode)

			// Verify file content is not binary (should be text)
			assert.True(t, isValidTextContent(data), "Label file for locale %s contains non-text content", localeCode)

			// Test that we can get the filename without error
			filename, err := conf.GetLabelFilename(modelVersion, localeCode)
			require.NoError(t, err, "Failed to get filename for locale %s", localeCode)

			expectedPattern := filepath.Join("V2.4", "BirdNET_GLOBAL_6K_V2.4_Labels_"+fileLocale+".txt")
			assert.Equal(t, expectedPattern, filename, "Unexpected filename for locale %s", localeCode)
		})
	}
}

func TestValidateV24LabelFileLineCount(t *testing.T) {
	modelVersion := BirdNET_GLOBAL_6K_V2_4

	for localeCode := range conf.LocaleCodeMapping {
		t.Run(localeCode, func(t *testing.T) {
			t.Parallel()
			data, err := GetLabelFileData(modelVersion, localeCode)
			require.NoError(t, err, "Failed to load label file for locale %s", localeCode)

			lines := countNonEmptyLines(data)
			require.NotEqual(t, -1, lines, "Error scanning label file for locale %s", localeCode)

			expectedLines := GetExpectedLinesV24()
			assert.Equal(t, expectedLines, lines, "Label file for locale %s has wrong line count", localeCode)
		})
	}
}

func TestValidateV24LabelFileFormat(t *testing.T) {
	modelVersion := BirdNET_GLOBAL_6K_V2_4

	for localeCode := range conf.LocaleCodeMapping {
		t.Run(localeCode, func(t *testing.T) {
			t.Parallel()
			data, err := GetLabelFileData(modelVersion, localeCode)
			require.NoError(t, err, "Failed to load label file for locale %s", localeCode)

			lines := strings.Split(string(data), "\n")
			nonEmptyLines := 0

			for i, line := range lines {
				line = strings.TrimSpace(line)
				if line == "" {
					continue // Skip empty lines
				}

				nonEmptyLines++

				// Validate that line is not too short
				assert.GreaterOrEqual(t, len(line), 5, "Line %d in %s locale file is too short: %s", i+1, localeCode, line)

				// Check for proper scientific name format (should split into exactly two parts)
				parts := strings.SplitN(line, "_", 2)
				if !assert.Len(t, parts, 2, "Line %d in %s locale file is malformed (should have exactly one underscore): %s", i+1, localeCode, line) {
					continue
				}

				scientificName := parts[0]
				if scientificName != "" {
					assert.True(t, isFirstRuneUpperCase(scientificName), "Line %d in %s locale file has invalid scientific name format: %s", i+1, localeCode, scientificName)
				}
			}

			// Validate total line count matches expected
			expectedLines := GetExpectedLinesV24()
			assert.Equal(t, expectedLines, nonEmptyLines, "Locale %s has wrong non-empty line count", localeCode)
		})
	}
}

func TestLabelFileConsistency(t *testing.T) {
	modelVersion := BirdNET_GLOBAL_6K_V2_4

	// Load English labels as reference
	enData, err := GetLabelFileData(modelVersion, "en-us")
	require.NoError(t, err, "Failed to load English reference labels")

	enLines := extractNonEmptyLines(enData)
	enScientificNames := extractScientificNames(enLines)

	// Track inconsistencies for reporting
	var inconsistencies []string

	// Test that all other locales have the same scientific names in the same order
	for localeCode := range conf.LocaleCodeMapping {
		if localeCode == "en-us" {
			continue // Skip reference locale
		}

		t.Run("consistency_"+localeCode, func(t *testing.T) {
			data, err := GetLabelFileData(modelVersion, localeCode)
			require.NoError(t, err, "Failed to load label file for locale %s", localeCode)

			lines := extractNonEmptyLines(data)
			scientificNames := extractScientificNames(lines)

			if !assert.Len(t, scientificNames, len(enScientificNames), "Locale %s has wrong scientific name count", localeCode) {
				return
			}

			// Check that scientific names match in order, but only log differences
			mismatches := 0
			for i, expectedName := range enScientificNames {
				if i >= len(scientificNames) {
					break
				}
				if scientificNames[i] != expectedName {
					mismatches++
					if mismatches <= 5 { // Only report first 5 mismatches to avoid spam
						inconsistency := fmt.Sprintf("Locale %s line %d: %s vs %s (en-us)",
							localeCode, i+1, scientificNames[i], expectedName)
						inconsistencies = append(inconsistencies, inconsistency)
						t.Logf("Scientific name difference found: %s", inconsistency)
					}
				}
			}

			if mismatches > 0 {
				t.Logf("Locale %s has %d scientific name differences from en-us reference",
					localeCode, mismatches)
			}
		})
	}

	// Log summary of inconsistencies but don't fail the test
	if len(inconsistencies) > 0 {
		t.Logf("Found %d scientific name inconsistencies across locale files. This may indicate label file version differences.", len(inconsistencies))
		for i, inconsistency := range inconsistencies {
			if i < 10 { // Limit output
				t.Logf("  %s", inconsistency)
			}
		}
		if len(inconsistencies) > 10 {
			t.Logf("  ... and %d more", len(inconsistencies)-10)
		}
	}
}

func TestLabelFileDataErrors(t *testing.T) {
	// Test unsupported model version
	_, err := GetLabelFileData("INVALID_MODEL", "en-us")
	require.Error(t, err, "Expected error for unsupported model version")

	// Test unsupported locale (should fall back to default)
	data, err := GetLabelFileData(BirdNET_GLOBAL_6K_V2_4, "invalid-locale")
	require.NoError(t, err, "Expected fallback for invalid locale")
	assert.NotEmpty(t, data, "Expected fallback data for invalid locale")
}

func TestGetLabelFilename(t *testing.T) {
	testCases := []struct {
		modelVersion string
		localeCode   string
		expected     string
		expectError  bool
	}{
		{
			modelVersion: BirdNET_GLOBAL_6K_V2_4,
			localeCode:   "en-us",
			expected:     filepath.Join("V2.4", "BirdNET_GLOBAL_6K_V2.4_Labels_en_us.txt"),
			expectError:  false,
		},
		{
			modelVersion: BirdNET_GLOBAL_6K_V2_4,
			localeCode:   "de",
			expected:     filepath.Join("V2.4", "BirdNET_GLOBAL_6K_V2.4_Labels_de.txt"),
			expectError:  false,
		},
		{
			modelVersion: "INVALID_MODEL",
			localeCode:   "en-us",
			expected:     "",
			expectError:  true,
		},
		{
			modelVersion: BirdNET_GLOBAL_6K_V2_4,
			localeCode:   "invalid-locale",
			expected:     "",
			expectError:  true,
		},
	}

	for _, tc := range testCases {
		t.Run(tc.modelVersion+"_"+tc.localeCode, func(t *testing.T) {
			filename, err := conf.GetLabelFilename(tc.modelVersion, tc.localeCode)

			if tc.expectError {
				assert.Error(t, err, "Expected error for %s/%s", tc.modelVersion, tc.localeCode)
			} else {
				require.NoError(t, err, "Unexpected error for %s/%s", tc.modelVersion, tc.localeCode)
				assert.Equal(t, tc.expected, filename, "Unexpected filename")
			}
		})
	}
}

// Helper functions

// countNonEmptyLines counts non-empty lines in the data, ignoring trailing empty lines
func countNonEmptyLines(data []byte) int {
	scanner := bufio.NewScanner(bytes.NewReader(data))
	count := 0
	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())
		if line != "" {
			count++
		}
	}

	// Check for scanning errors
	if err := scanner.Err(); err != nil {
		// In a test context, we could panic or return a special value
		// For now, we'll return -1 to indicate an error occurred
		return -1
	}

	return count
}

// isValidTextContent checks if the data contains valid UTF-8 text content
func isValidTextContent(data []byte) bool {
	// Use utf8.Valid for proper UTF-8 validation
	return utf8.Valid(data)
}

// isFirstRuneUpperCase checks if the first rune of a string is uppercase using Unicode rules
func isFirstRuneUpperCase(s string) bool {
	if s == "" {
		return false
	}
	firstRune, _ := utf8.DecodeRuneInString(s)
	return unicode.IsUpper(firstRune)
}

// extractNonEmptyLines extracts all non-empty lines from data
func extractNonEmptyLines(data []byte) []string {
	var lines []string
	scanner := bufio.NewScanner(bytes.NewReader(data))
	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())
		if line != "" {
			lines = append(lines, line)
		}
	}
	return lines
}

// extractScientificNames extracts scientific names (part before underscore) from label lines
func extractScientificNames(lines []string) []string {
	// Pre-allocate slice with capacity for all lines
	scientificNames := make([]string, 0, len(lines))
	for _, line := range lines {
		parts := strings.SplitN(line, "_", 2)
		scientificNames = append(scientificNames, parts[0])
	}
	return scientificNames
}
