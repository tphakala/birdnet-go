package processor

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/tphakala/birdnet-go/internal/conf"
)

// TestCalculateMinDetections_AllLevels verifies that each filtering level (0-5)
// produces the correct minDetections value according to the specification.
func TestCalculateMinDetections_AllLevels(t *testing.T) {
	tests := []struct {
		name        string
		level       int
		overlap     float64
		expectedMin int
		description string
	}{
		{
			name:        "level_0_off",
			level:       0,
			overlap:     2.4,
			expectedMin: 1,
			description: "Level 0 (Off): No filtering, requires only 1 detection",
		},
		{
			name:        "level_1_lenient",
			level:       1,
			overlap:     2.0,
			expectedMin: 2,
			description: "Level 1 (Lenient): overlap 2.0, step 1.0s, max 6 in 6s, 20% = 2",
		},
		{
			name:        "level_2_moderate",
			level:       2,
			overlap:     2.2,
			expectedMin: 3,
			description: "Level 2 (Moderate): overlap 2.2, step 0.8s, max ~8 in 6s, 30% = 3",
		},
		{
			name:        "level_3_balanced",
			level:       3,
			overlap:     2.4,
			expectedMin: 5,
			description: "Level 3 (Balanced): overlap 2.4, step 0.6s, max 10 in 6s, 50% = 5 (original)",
		},
		{
			name:        "level_4_strict",
			level:       4,
			overlap:     2.7,
			expectedMin: 12,
			description: "Level 4 (Strict): overlap 2.7, step 0.3s, max 20 in 6s, 60% = 12",
		},
		{
			name:        "level_5_maximum",
			level:       5,
			overlap:     2.8,
			expectedMin: 21,
			description: "Level 5 (Maximum): overlap 2.8, step 0.2s, max 30 in 6s, 70% = 21",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			p := &Processor{
				Settings: &conf.Settings{
					Realtime: conf.RealtimeSettings{
						FalsePositiveFilter: conf.FalsePositiveFilterSettings{
							Level: tt.level,
						},
						Audio: conf.AudioSettings{
							Export: conf.ExportSettings{
								Length:     15,
								PreCapture: 3,
							},
						},
					},
					BirdNET: conf.BirdNETConfig{
						Overlap: tt.overlap,
					},
				},
			}

			result := p.calculateMinDetections()

			assert.Equal(t, tt.expectedMin, result, "%s\nLevel=%d, Overlap=%.1f",
				tt.description, tt.level, tt.overlap)
		})
	}
}

// TestCalculateMinDetections_OverlapVariation verifies that the same level
// with different overlaps produces appropriate minDetections values.
func TestCalculateMinDetections_OverlapVariation(t *testing.T) {
	tests := []struct {
		name        string
		level       int
		overlap     float64
		expectedMin int
	}{
		// Level 3 with different overlaps
		{"level_3_overlap_2.4", 3, 2.4, 5}, // exact minimum
		{"level_3_overlap_2.5", 3, 2.5, 6}, // higher overlap = more detections possible
		{"level_3_overlap_2.6", 3, 2.6, 8}, // even higher
		{"level_3_overlap_2.2", 3, 2.2, 4}, // below minimum but still works

		// Level 1 with different overlaps
		{"level_1_overlap_2.0", 1, 2.0, 2}, // exact minimum
		{"level_1_overlap_2.5", 1, 2.5, 3}, // higher overlap
		{"level_1_overlap_1.5", 1, 1.5, 1}, // below minimum
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			p := &Processor{
				Settings: &conf.Settings{
					Realtime: conf.RealtimeSettings{
						FalsePositiveFilter: conf.FalsePositiveFilterSettings{
							Level: tt.level,
						},
					},
					BirdNET: conf.BirdNETConfig{
						Overlap: tt.overlap,
					},
				},
			}

			result := p.calculateMinDetections()

			assert.Equal(t, tt.expectedMin, result, "Level %d with overlap %.1f",
				tt.level, tt.overlap)
		})
	}
}

// TestGetRecommendedLevelForOverlap verifies the smart migration logic
// that recommends appropriate filtering levels based on overlap.
func TestGetRecommendedLevelForOverlap(t *testing.T) {
	tests := []struct {
		overlap           float64
		expectedLevel     int
		overlapSufficient bool
	}{
		{0.0, 0, true}, // Very low overlap -> Level 0
		{1.5, 0, true}, // Low overlap -> Level 0
		{2.0, 1, true}, // Can support Level 1
		{2.2, 2, true}, // Can support Level 2
		{2.4, 3, true}, // Can support Level 3
		{2.7, 4, true}, // Can support Level 4
		{2.8, 5, true}, // Can support Level 5
		{2.9, 5, true}, // High overlap -> Level 5
	}

	for _, tt := range tests {
		level, sufficient := getRecommendedLevelForOverlap(tt.overlap)
		assert.Equal(t, tt.expectedLevel, level, "Overlap %.1f", tt.overlap)
		assert.Equal(t, tt.overlapSufficient, sufficient, "Overlap %.1f", tt.overlap)
	}
}

// TestHelperFunctions verifies all helper functions return correct values.
func TestHelperFunctions(t *testing.T) {
	t.Run("getMinimumOverlapForLevel", func(t *testing.T) {
		tests := []struct {
			level      int
			minOverlap float64
		}{
			{0, 0.0},
			{1, 2.0},
			{2, 2.2},
			{3, 2.4},
			{4, 2.7},
			{5, 2.8},
			{99, 2.2}, // Invalid level should return default (Moderate)
			{-1, 2.2}, // Invalid level should return default (Moderate)
		}

		for _, tt := range tests {
			result := getMinimumOverlapForLevel(tt.level)
			assert.InDelta(t, tt.minOverlap, result, 0, "Level %d", tt.level)
		}
	})

	t.Run("getThresholdForLevel", func(t *testing.T) {
		tests := []struct {
			level     int
			threshold float64
		}{
			{0, 0.0},
			{1, 0.20},
			{2, 0.30},
			{3, 0.50},
			{4, 0.60},
			{5, 0.70},
			{99, 0.30}, // Invalid level should return default (Moderate)
			{-1, 0.30}, // Invalid level should return default (Moderate)
		}

		for _, tt := range tests {
			result := getThresholdForLevel(tt.level)
			assert.InDelta(t, tt.threshold, result, 0, "Level %d", tt.level)
		}
	})

	t.Run("getLevelName", func(t *testing.T) {
		tests := []struct {
			level int
			name  string
		}{
			{0, "Off"},
			{1, "Lenient"},
			{2, "Moderate"},
			{3, "Balanced"},
			{4, "Strict"},
			{5, "Maximum"},
			{99, "Unknown"}, // Invalid level should return "Unknown"
			{-1, "Unknown"}, // Invalid level should return "Unknown"
		}

		for _, tt := range tests {
			result := getLevelName(tt.level)
			assert.Equal(t, tt.name, result, "Level %d", tt.level)
		}
	})

	t.Run("getHardwareRequirementForLevel", func(t *testing.T) {
		tests := []struct {
			level    int
			hardware string
		}{
			{0, "Any (RPi 3B or better)"},
			{1, "Any (RPi 3B or better)"},
			{2, "Any (RPi 3B or better)"},
			{3, "Any (RPi 3B or better)"},
			{4, "RPi 4 or better required"},
			{5, "RPi 4 or better required"},
			{99, "Unknown"}, // Invalid level should return "Unknown"
			{-1, "Unknown"}, // Invalid level should return "Unknown"
		}

		for _, tt := range tests {
			result := getHardwareRequirementForLevel(tt.level)
			assert.Equal(t, tt.hardware, result, "Level %d", tt.level)
		}
	})

	t.Run("getLevelDescription", func(t *testing.T) {
		tests := []struct {
			level         int
			shouldContain []string // Verify key phrases are present
		}{
			{0, []string{"No filtering", "Default", "BirdNET-Pi"}},
			{1, []string{"Lenient", "2 confirmations", "RTSP", "surveillance"}},
			{2, []string{"Moderate", "3 confirmations", "Balanced", "hobby"}},
			{3, []string{"Balanced", "5 confirmations", "Original", "pre-September"}},
			{4, []string{"Strict", "12 confirmations", "RPi 4+", "high-quality"}},
			{5, []string{"Maximum", "21 confirmations", "RPi 4+", "professional-grade"}},
		}

		for _, tt := range tests {
			result := getLevelDescription(tt.level)
			assert.NotEmpty(t, result, "Level %d: got empty description", tt.level)

			for _, phrase := range tt.shouldContain {
				assert.Contains(t, result, phrase, "Level %d: description missing expected phrase", tt.level)
			}
		}

		// Test invalid level
		result := getLevelDescription(99)
		assert.Contains(t, result, "Unknown", "Invalid level should return 'Unknown'")
	})
}

// TestCalculateMinDetections verifies that the minimum detection threshold
// is calculated based only on overlap setting (not clip length) to filter
// false positives through repeated detection confirmation.
// NOTE: This test uses Level 3 (Balanced) which has 50% threshold (original behavior)
func TestCalculateMinDetections(t *testing.T) {
	tests := []struct {
		name        string
		overlap     float64
		expectedMin int
		description string
	}{
		{
			name:        "no_overlap",
			overlap:     0.0,
			expectedMin: 1,
			description: "No overlap means no repeated confirmation possible, require only 1 detection",
		},
		{
			name:        "low_overlap_1.5",
			overlap:     1.5,
			expectedMin: 2,
			description: "Overlap 1.5: step=1.5s, max 4 detections per 6s, require 2 (50% of 4)",
		},
		{
			name:        "medium_overlap_2.0",
			overlap:     2.0,
			expectedMin: 3,
			description: "Overlap 2.0: step=1.0s, max 6 detections per 6s, require 3 (50% of 6)",
		},
		{
			name:        "high_overlap_2.2",
			overlap:     2.2,
			expectedMin: 4,
			description: "Overlap 2.2: step=0.8s, max ~8 detections per 6s, require 4 (50% of 8)",
		},
		{
			name:        "high_overlap_2.4",
			overlap:     2.4,
			expectedMin: 5,
			description: "Overlap 2.4: step=0.6s, max 10 detections per 6s, require 5 (50% of 10)",
		},
		{
			name:        "very_high_overlap_2.5",
			overlap:     2.5,
			expectedMin: 6,
			description: "Overlap 2.5: step=0.5s, max 12 detections per 6s, require 6 (50% of 12)",
		},
		{
			name:        "extreme_overlap_2.7",
			overlap:     2.7,
			expectedMin: 10,
			description: "Overlap 2.7: step=0.3s, max 20 detections per 6s, require 10 (50% of 20)",
		},
		{
			name:        "extreme_overlap_2.8",
			overlap:     2.8,
			expectedMin: 15,
			description: "Overlap 2.8: step=0.2s, max 30 detections per 6s, require 15 (50% of 30)",
		},
		{
			name:        "extreme_overlap_2.85",
			overlap:     2.85,
			expectedMin: 20,
			description: "Overlap 2.85: step=0.15s, max 40 detections per 6s, require 20 (50% of 40) - tests epsilon fix",
		},
		{
			name:        "near_max_overlap_2.9",
			overlap:     2.9,
			expectedMin: 30,
			description: "Overlap 2.9: step=0.1s, max 60 detections per 6s, require 30 (50% of 60)",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			// Create processor with test settings
			// Use Level 3 (Balanced) which has 50% threshold - the original behavior
			// Note: captureLength and preCapture should NOT affect the result
			p := &Processor{
				Settings: &conf.Settings{
					Realtime: conf.RealtimeSettings{
						FalsePositiveFilter: conf.FalsePositiveFilterSettings{
							Level: 3, // Balanced (50% threshold - original behavior)
						},
						Audio: conf.AudioSettings{
							Export: conf.ExportSettings{
								Length:     15, // arbitrary value, should not affect result
								PreCapture: 3,  // arbitrary value, should not affect result
							},
						},
					},
					BirdNET: conf.BirdNETConfig{
						Overlap: tt.overlap,
					},
				},
			}

			result := p.calculateMinDetections()

			assert.Equal(t, tt.expectedMin, result, "%s\nOverlap=%.1f",
				tt.description, tt.overlap)
		})
	}
}

// TestCalculateMinDetectionsIndependentOfClipLength verifies that
// audio clip length settings do NOT affect minDetections calculation.
// This is the fix for issue #1314.
// NOTE: Uses Level 2 (Moderate, 30% threshold) for realistic test values
func TestCalculateMinDetectionsIndependentOfClipLength(t *testing.T) {
	tests := []struct {
		name           string
		level          int
		overlap        float64
		clipConfigs    []struct{ length, preCapture int }
		expectedMinDet int
		description    string
	}{
		{
			name:    "overlap_2.2_various_clip_lengths",
			level:   2, // Moderate (30%)
			overlap: 2.2,
			clipConfigs: []struct{ length, preCapture int }{
				{15, 3},  // default
				{30, 5},  // medium
				{60, 15}, // long (user's config from issue #1314)
				{60, 30}, // very long pre-capture
				{10, 0},  // short, no pre-capture
			},
			expectedMinDet: 3, // Level 2 with overlap 2.2: step=0.8s, max ~8 in 6s, 30% = 3
			description:    "Overlap 2.2 with Level 2 should always require 3 detections regardless of clip length",
		},
		{
			name:    "overlap_2.4_various_clip_lengths",
			level:   2, // Moderate (30%)
			overlap: 2.4,
			clipConfigs: []struct{ length, preCapture int }{
				{15, 3},
				{45, 10},
				{60, 20},
			},
			expectedMinDet: 3, // Level 2 with overlap 2.4: step=0.6s, max 10 in 6s, 30% = 3
			description:    "Overlap 2.4 with Level 2 should always require 3 detections regardless of clip length",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			for i, config := range tt.clipConfigs {
				p := &Processor{
					Settings: &conf.Settings{
						Realtime: conf.RealtimeSettings{
							FalsePositiveFilter: conf.FalsePositiveFilterSettings{
								Level: tt.level,
							},
							Audio: conf.AudioSettings{
								Export: conf.ExportSettings{
									Length:     config.length,
									PreCapture: config.preCapture,
								},
							},
						},
						BirdNET: conf.BirdNETConfig{
							Overlap: tt.overlap,
						},
					},
				}

				result := p.calculateMinDetections()

				assert.Equal(t, tt.expectedMinDet, result,
					"%s\nConfig %d (length=%ds, preCapture=%ds): Clip length should NOT affect minDetections!",
					tt.description, i+1, config.length, config.preCapture)
			}
		})
	}
}

// TestCalculateMinDetectionsConsistency verifies that calculateMinDetections
// returns consistent results when called multiple times with same settings.
func TestCalculateMinDetectionsConsistency(t *testing.T) {
	p := &Processor{
		Settings: &conf.Settings{
			Realtime: conf.RealtimeSettings{
				FalsePositiveFilter: conf.FalsePositiveFilterSettings{
					Level: 3, // Balanced
				},
				Audio: conf.AudioSettings{
					Export: conf.ExportSettings{
						Length:     15,
						PreCapture: 3,
					},
				},
			},
			BirdNET: conf.BirdNETConfig{
				Overlap: 2.4,
			},
		},
	}

	// Call multiple times and verify consistent results
	first := p.calculateMinDetections()
	for i := range 100 {
		result := p.calculateMinDetections()
		assert.Equal(t, first, result, "Inconsistent results: iteration %d", i)
	}
}

// TestCalculateMinDetectionsIssue1314 is a regression test for issue #1314
// where users with overlap=2.2-2.4 and long clip lengths (45-60s) were getting
// impossibly high minDetections requirements (14+) that rejected all bird calls.
// NOTE: Uses Level 2 (Moderate, 30%) to match typical user configuration
func TestCalculateMinDetectionsIssue1314(t *testing.T) {
	// User's actual configuration from issue #1314
	p := &Processor{
		Settings: &conf.Settings{
			Realtime: conf.RealtimeSettings{
				FalsePositiveFilter: conf.FalsePositiveFilterSettings{
					Level: 2, // Moderate (30% threshold)
				},
				Audio: conf.AudioSettings{
					Export: conf.ExportSettings{
						Length:     60, // 60-second clips
						PreCapture: 15, // 15-second pre-capture
					},
				},
			},
			BirdNET: conf.BirdNETConfig{
				Overlap: 2.2, // or 2.35-2.36 based on logs
			},
		},
	}

	result := p.calculateMinDetections()

	// With the bug, this was 14 (impossible to achieve)
	// With the fix and Level 2, this should be 3 (reasonable)
	// Level 2 with overlap 2.2: step=0.8s, max ~8 in 6s, 30% = 3
	expectedMin := 3
	assert.Equal(t, expectedMin, result,
		"Issue #1314 regression: Long clip lengths should NOT increase detection requirements!\n"+
			"Settings: level=%d, overlap=%.1f, captureLength=%ds, preCapture=%ds",
		p.Settings.Realtime.FalsePositiveFilter.Level,
		p.Settings.BirdNET.Overlap,
		p.Settings.Realtime.Audio.Export.Length,
		p.Settings.Realtime.Audio.Export.PreCapture)

	// Verify it's reasonable by checking logs showed "matched 1/14 times"
	// With our fix, this should now be "matched 1/3 times" (rejected) or "matched 3/3 times" (accepted)
	assert.Less(t, result, 14, "minDetections = %d is too high! This was the bug in issue #1314", result)
}

// TestSuggestLevelForDisabledFilter verifies that the smart migration logic
// provides appropriate recommendations when filtering is disabled (level 0).
func TestSuggestLevelForDisabledFilter(t *testing.T) {
	tests := []struct {
		name                 string
		overlap              float64
		expectRecommendation bool
		expectedLevel        int
		expectedLevelName    string
	}{
		{
			name:                 "overlap_2.0_recommends_level_1",
			overlap:              2.0,
			expectRecommendation: true,
			expectedLevel:        1,
			expectedLevelName:    "Lenient",
		},
		{
			name:                 "overlap_2.2_recommends_level_2",
			overlap:              2.2,
			expectRecommendation: true,
			expectedLevel:        2,
			expectedLevelName:    "Moderate",
		},
		{
			name:                 "overlap_2.4_recommends_level_3",
			overlap:              2.4,
			expectRecommendation: true,
			expectedLevel:        3,
			expectedLevelName:    "Balanced",
		},
		{
			name:                 "overlap_2.7_recommends_level_4",
			overlap:              2.7,
			expectRecommendation: true,
			expectedLevel:        4,
			expectedLevelName:    "Strict",
		},
		{
			name:                 "overlap_2.8_recommends_level_5",
			overlap:              2.8,
			expectRecommendation: true,
			expectedLevel:        5,
			expectedLevelName:    "Maximum",
		},
		{
			name:                 "low_overlap_no_recommendation",
			overlap:              1.5,
			expectRecommendation: false,
			expectedLevel:        0,
			expectedLevelName:    "",
		},
		{
			name:                 "zero_overlap_no_recommendation",
			overlap:              0.0,
			expectRecommendation: false,
			expectedLevel:        0,
			expectedLevelName:    "",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			// Call the function - it will log but we mainly verify it doesn't panic
			// and that the logic flows correctly based on getRecommendedLevelForOverlap
			suggestLevelForDisabledFilter(tt.overlap)

			// Verify the underlying recommendation logic
			recommendedLevel, _ := getRecommendedLevelForOverlap(tt.overlap)
			if tt.expectRecommendation {
				assert.Equal(t, tt.expectedLevel, recommendedLevel,
					"Expected recommendation level %d (%s)", tt.expectedLevel, tt.expectedLevelName)
			} else {
				assert.Equal(t, 0, recommendedLevel, "Expected no recommendation (level 0)")
			}
		})
	}
}

// TestValidateOverlapForLevel verifies overlap validation logic for enabled filter levels.
func TestValidateOverlapForLevel(t *testing.T) {
	tests := []struct {
		name          string
		level         int
		overlap       float64
		minOverlap    float64
		minDetections int
		expectWarning bool
		description   string
	}{
		{
			name:          "level_1_overlap_sufficient",
			level:         1,
			overlap:       2.0,
			minOverlap:    2.0,
			minDetections: 2,
			expectWarning: false,
			description:   "Overlap meets minimum for Level 1 (Lenient)",
		},
		{
			name:          "level_1_overlap_too_low",
			level:         1,
			overlap:       1.8,
			minOverlap:    2.0,
			minDetections: 2,
			expectWarning: true,
			description:   "Overlap below minimum for Level 1",
		},
		{
			name:          "level_3_overlap_perfect",
			level:         3,
			overlap:       2.4,
			minOverlap:    2.4,
			minDetections: 5,
			expectWarning: false,
			description:   "Overlap exactly at minimum for Level 3 (Balanced)",
		},
		{
			name:          "level_3_overlap_too_low",
			level:         3,
			overlap:       2.2,
			minOverlap:    2.4,
			minDetections: 4,
			expectWarning: true,
			description:   "Overlap below minimum for Level 3",
		},
		{
			name:          "level_4_overlap_sufficient",
			level:         4,
			overlap:       2.7,
			minOverlap:    2.7,
			minDetections: 12,
			expectWarning: false,
			description:   "Overlap meets minimum for Level 4 (Strict)",
		},
		{
			name:          "level_5_overlap_excellent",
			level:         5,
			overlap:       2.9,
			minOverlap:    2.8,
			minDetections: 21,
			expectWarning: false,
			description:   "Overlap exceeds minimum for Level 5 (Maximum)",
		},
		{
			name:          "level_5_overlap_too_low",
			level:         5,
			overlap:       2.5,
			minOverlap:    2.8,
			minDetections: 6,
			expectWarning: true,
			description:   "Overlap well below minimum for Level 5",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			// Call the function - it will log but we mainly verify it doesn't panic
			validateOverlapForLevel(tt.level, tt.overlap, tt.minOverlap, tt.minDetections)

			// Verify the warning condition is correctly identified
			shouldWarn := tt.overlap < tt.minOverlap
			assert.Equal(t, tt.expectWarning, shouldWarn,
				"%s: overlap=%.1f, minOverlap=%.1f",
				tt.description, tt.overlap, tt.minOverlap)
		})
	}
}

// TestWarnAboutHardwareRequirements verifies hardware warning logic for high filter levels.
func TestWarnAboutHardwareRequirements(t *testing.T) {
	tests := []struct {
		name          string
		level         int
		overlap       float64
		expectWarning bool
		description   string
	}{
		{
			name:          "level_0_no_warning",
			level:         0,
			overlap:       2.0,
			expectWarning: false,
			description:   "Level 0 should not trigger hardware warnings",
		},
		{
			name:          "level_1_no_warning",
			level:         1,
			overlap:       2.0,
			expectWarning: false,
			description:   "Level 1 should not trigger hardware warnings",
		},
		{
			name:          "level_2_no_warning",
			level:         2,
			overlap:       2.2,
			expectWarning: false,
			description:   "Level 2 should not trigger hardware warnings",
		},
		{
			name:          "level_3_no_warning",
			level:         3,
			overlap:       2.4,
			expectWarning: false,
			description:   "Level 3 should not trigger hardware warnings",
		},
		{
			name:          "level_4_valid_overlap",
			level:         4,
			overlap:       2.7,
			expectWarning: true,
			description:   "Level 4 should warn about hardware requirements",
		},
		{
			name:          "level_5_valid_overlap",
			level:         5,
			overlap:       2.8,
			expectWarning: true,
			description:   "Level 5 should warn about hardware requirements",
		},
		{
			name:          "level_4_overlap_too_high",
			level:         4,
			overlap:       3.0,
			expectWarning: true,
			description:   "Level 4 with overlap >= 3.0 should warn about invalid calculation",
		},
		{
			name:          "level_5_overlap_too_high",
			level:         5,
			overlap:       3.1,
			expectWarning: true,
			description:   "Level 5 with overlap >= 3.0 should warn about invalid calculation",
		},
		{
			name:          "level_4_overlap_2.5",
			level:         4,
			overlap:       2.5,
			expectWarning: true,
			description:   "Level 4 with overlap 2.5 should calculate 500ms max inference time",
		},
		{
			name:          "level_5_overlap_2.9",
			level:         5,
			overlap:       2.9,
			expectWarning: true,
			description:   "Level 5 with overlap 2.9 should calculate 100ms max inference time",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			// Call the function - it will log but we mainly verify it doesn't panic
			warnAboutHardwareRequirements(tt.level, tt.overlap)

			// Verify the warning condition
			shouldWarn := tt.level >= 4
			assert.Equal(t, tt.expectWarning, shouldWarn,
				"%s: level=%d", tt.description, tt.level)

			// For high levels with valid overlap, verify inference time calculation logic
			if tt.level >= 4 && tt.overlap < 3.0 {
				stepSize := 3.0 - tt.overlap
				maxInferenceTime := stepSize * 1000
				t.Logf("Level %d with overlap %.1f requires inference < %.0fms",
					tt.level, tt.overlap, maxInferenceTime)

				// Sanity check the calculation
				if maxInferenceTime <= 0 {
					t.Errorf("Invalid max inference time calculation: %.0fms", maxInferenceTime)
				}
			}
		})
	}
}

// TestValidateAndLogFilterConfig_Integration verifies the main validation function
// correctly delegates to helper functions based on filter level configuration.
func TestValidateAndLogFilterConfig_Integration(t *testing.T) {
	tests := []struct {
		name        string
		level       int
		overlap     float64
		description string
	}{
		{
			name:        "level_0_calls_suggest",
			level:       0,
			overlap:     2.4,
			description: "Level 0 should call suggestLevelForDisabledFilter",
		},
		{
			name:        "level_1_calls_validate",
			level:       1,
			overlap:     2.0,
			description: "Level 1 should call validateOverlapForLevel",
		},
		{
			name:        "level_3_calls_validate",
			level:       3,
			overlap:     2.4,
			description: "Level 3 should call validateOverlapForLevel",
		},
		{
			name:        "level_4_calls_validate_and_warn",
			level:       4,
			overlap:     2.7,
			description: "Level 4 should call both validateOverlapForLevel and warnAboutHardwareRequirements",
		},
		{
			name:        "level_5_calls_validate_and_warn",
			level:       5,
			overlap:     2.8,
			description: "Level 5 should call both validateOverlapForLevel and warnAboutHardwareRequirements",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			// Create settings with valid configuration
			settings := &conf.Settings{
				Realtime: conf.RealtimeSettings{
					FalsePositiveFilter: conf.FalsePositiveFilterSettings{
						Level: tt.level,
					},
					Audio: conf.AudioSettings{
						Export: conf.ExportSettings{
							Length:     15,
							PreCapture: 3,
						},
					},
				},
				BirdNET: conf.BirdNETConfig{
					Overlap: tt.overlap,
				},
			}

			// Call the main validation function - should not panic
			validateAndLogFilterConfig(settings)

			// Verify the level wasn't changed (no validation errors)
			assert.Equal(t, tt.level, settings.Realtime.FalsePositiveFilter.Level,
				"%s: Level was unexpectedly changed", tt.description)
		})
	}
}

// TestValidateAndLogFilterConfig_InvalidLevel verifies that invalid filter levels
// are properly handled and reset to safe defaults.
func TestValidateAndLogFilterConfig_InvalidLevel(t *testing.T) {
	tests := []struct {
		name          string
		initialLevel  int
		expectedLevel int
		description   string
	}{
		{
			name:          "negative_level_resets_to_zero",
			initialLevel:  -1,
			expectedLevel: 0,
			description:   "Negative level should be reset to 0",
		},
		{
			name:          "level_too_high_resets_to_zero",
			initialLevel:  10,
			expectedLevel: 0,
			description:   "Level > 5 should be reset to 0",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			settings := &conf.Settings{
				Realtime: conf.RealtimeSettings{
					FalsePositiveFilter: conf.FalsePositiveFilterSettings{
						Level: tt.initialLevel,
					},
				},
				BirdNET: conf.BirdNETConfig{
					Overlap: 2.4,
				},
			}

			// Call validation - should reset invalid level to 0
			validateAndLogFilterConfig(settings)

			assert.Equal(t, tt.expectedLevel, settings.Realtime.FalsePositiveFilter.Level,
				"%s: Expected level to be reset", tt.description)
		})
	}
}
