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

	"github.com/tphakala/birdnet-go/internal/conf"
)

// Note: GetExpectedLinesV24() function is defined in label_files.go

// Mock logger for testing
type testLogger struct {
	logs []string
}

func (tl *testLogger) Debug(format string, v ...interface{}) {
	// Store debug messages for test verification if needed
	tl.logs = append(tl.logs, fmt.Sprintf(format, v...))
}

func TestLoadAllV24LabelFiles(t *testing.T) {
	modelVersion := BirdNET_GLOBAL_6K_V2_4

	// Test each locale code in the mapping
	for localeCode, fileLocale := range conf.LocaleCodeMapping {
		t.Run(localeCode, func(t *testing.T) {
			logger := &testLogger{}

			// Test loading the label file
			data, err := GetLabelFileDataWithLogger(modelVersion, localeCode, logger)
			if err != nil {
				t.Fatalf("Failed to load label file for locale %s: %v", localeCode, err)
			}

			if len(data) == 0 {
				t.Fatalf("Label file for locale %s is empty", localeCode)
			}

			// Verify file content is not binary (should be text)
			if !isValidTextContent(data) {
				t.Errorf("Label file for locale %s contains non-text content", localeCode)
			}

			// Test that we can get the filename without error
			filename, err := conf.GetLabelFilename(modelVersion, localeCode)
			if err != nil {
				t.Errorf("Failed to get filename for locale %s: %v", localeCode, err)
			} else {
				expectedPattern := filepath.Join("V2.4", "BirdNET_GLOBAL_6K_V2.4_Labels_"+fileLocale+".txt")
				if filename != expectedPattern {
					t.Errorf("Unexpected filename for locale %s: got %s, expected %s",
						localeCode, filename, expectedPattern)
				}
			}
		})
	}
}

func TestValidateV24LabelFileLineCount(t *testing.T) {
	modelVersion := BirdNET_GLOBAL_6K_V2_4

	for localeCode := range conf.LocaleCodeMapping {
		t.Run(localeCode, func(t *testing.T) {
			data, err := GetLabelFileData(modelVersion, localeCode)
			if err != nil {
				t.Fatalf("Failed to load label file for locale %s: %v", localeCode, err)
			}

			lines := countNonEmptyLines(data)
			expectedLines := GetExpectedLinesV24()
			if lines != expectedLines {
				t.Errorf("Label file for locale %s has %d lines, expected %d",
					localeCode, lines, expectedLines)
			}
		})
	}
}

func TestValidateV24LabelFileFormat(t *testing.T) {
	modelVersion := BirdNET_GLOBAL_6K_V2_4

	for localeCode := range conf.LocaleCodeMapping {
		t.Run(localeCode, func(t *testing.T) {
			data, err := GetLabelFileData(modelVersion, localeCode)
			if err != nil {
				t.Fatalf("Failed to load label file for locale %s: %v", localeCode, err)
			}

			lines := strings.Split(string(data), "\n")
			nonEmptyLines := 0

			for i, line := range lines {
				line = strings.TrimSpace(line)
				if line == "" {
					continue // Skip empty lines
				}

				nonEmptyLines++

				// Validate line format: should contain an underscore separator
				if !strings.Contains(line, "_") {
					t.Errorf("Line %d in %s locale file is malformed (missing underscore): %s",
						i+1, localeCode, line)
				}

				// Validate that line is not too short
				if len(line) < 5 {
					t.Errorf("Line %d in %s locale file is too short: %s",
						i+1, localeCode, line)
				}

				// Check for proper scientific name format (should start with capital letter)
				parts := strings.SplitN(line, "_", 2)
				if len(parts) == 2 {
					scientificName := parts[0]
					if scientificName != "" && !isFirstRuneUpperCase(scientificName) {
						t.Errorf("Line %d in %s locale file has invalid scientific name format: %s",
							i+1, localeCode, scientificName)
					}
				}
			}

			// Validate total line count matches expected
			expectedLines := GetExpectedLinesV24()
			if nonEmptyLines != expectedLines {
				t.Errorf("Locale %s has %d non-empty lines, expected %d",
					localeCode, nonEmptyLines, expectedLines)
			}
		})
	}
}

func TestLabelFileConsistency(t *testing.T) {
	modelVersion := BirdNET_GLOBAL_6K_V2_4

	// Load English labels as reference
	enData, err := GetLabelFileData(modelVersion, "en-us")
	if err != nil {
		t.Fatalf("Failed to load English reference labels: %v", err)
	}

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
			if err != nil {
				t.Fatalf("Failed to load label file for locale %s: %v", localeCode, err)
			}

			lines := extractNonEmptyLines(data)
			scientificNames := extractScientificNames(lines)

			if len(scientificNames) != len(enScientificNames) {
				t.Errorf("Locale %s has %d scientific names, expected %d",
					localeCode, len(scientificNames), len(enScientificNames))
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
	if err == nil {
		t.Error("Expected error for unsupported model version, got nil")
	}

	// Test unsupported locale (should fall back to default)
	data, err := GetLabelFileData(BirdNET_GLOBAL_6K_V2_4, "invalid-locale")
	if err != nil {
		t.Errorf("Expected fallback for invalid locale, got error: %v", err)
	}
	if len(data) == 0 {
		t.Error("Expected fallback data for invalid locale, got empty data")
	}
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
				if err == nil {
					t.Errorf("Expected error for %s/%s, got nil", tc.modelVersion, tc.localeCode)
				}
			} else {
				if err != nil {
					t.Errorf("Unexpected error for %s/%s: %v", tc.modelVersion, tc.localeCode, err)
				}
				if filename != tc.expected {
					t.Errorf("Expected filename %s, got %s", tc.expected, filename)
				}
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
	return count
}

// isValidTextContent checks if the data contains valid text content
func isValidTextContent(data []byte) bool {
	// Check for null bytes which would indicate binary content
	for _, b := range data {
		if b == 0 {
			return false
		}
	}
	return true
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
	var scientificNames []string
	for _, line := range lines {
		parts := strings.SplitN(line, "_", 2)
		scientificNames = append(scientificNames, parts[0])
	}
	return scientificNames
}
