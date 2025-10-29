package processor

import (
	"strings"
	"testing"

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

			if result != tt.expectedMin {
				t.Errorf("%s\nGot minDetections = %d, want %d\nLevel=%d, Overlap=%.1f",
					tt.description, result, tt.expectedMin, tt.level, tt.overlap)
			}
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
		{"level_3_overlap_2.4", 3, 2.4, 5},  // exact minimum
		{"level_3_overlap_2.5", 3, 2.5, 6},  // higher overlap = more detections possible
		{"level_3_overlap_2.6", 3, 2.6, 8},  // even higher
		{"level_3_overlap_2.2", 3, 2.2, 4},  // below minimum but still works

		// Level 1 with different overlaps
		{"level_1_overlap_2.0", 1, 2.0, 2},  // exact minimum
		{"level_1_overlap_2.5", 1, 2.5, 3},  // higher overlap
		{"level_1_overlap_1.5", 1, 1.5, 1},  // below minimum
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

			if result != tt.expectedMin {
				t.Errorf("Level %d with overlap %.1f: got %d, want %d",
					tt.level, tt.overlap, result, tt.expectedMin)
			}
		})
	}
}

// TestGetRecommendedLevelForOverlap verifies the smart migration logic
// that recommends appropriate filtering levels based on overlap.
func TestGetRecommendedLevelForOverlap(t *testing.T) {
	tests := []struct {
		overlap          float64
		expectedLevel    int
		overlapSufficient bool
	}{
		{0.0, 0, true},   // Very low overlap -> Level 0
		{1.5, 0, true},   // Low overlap -> Level 0
		{2.0, 1, true},   // Can support Level 1
		{2.2, 2, true},   // Can support Level 2
		{2.4, 3, true},   // Can support Level 3
		{2.7, 4, true},   // Can support Level 4
		{2.8, 5, true},   // Can support Level 5
		{2.9, 5, true},   // High overlap -> Level 5
	}

	for _, tt := range tests {
		level, sufficient := getRecommendedLevelForOverlap(tt.overlap)
		if level != tt.expectedLevel {
			t.Errorf("Overlap %.1f: got level %d, want %d",
				tt.overlap, level, tt.expectedLevel)
		}
		if sufficient != tt.overlapSufficient {
			t.Errorf("Overlap %.1f: got sufficient=%v, want %v",
				tt.overlap, sufficient, tt.overlapSufficient)
		}
	}
}

// TestHelperFunctions verifies all helper functions return correct values.
func TestHelperFunctions(t *testing.T) {
	t.Run("getMinimumOverlapForLevel", func(t *testing.T) {
		tests := []struct {
			level       int
			minOverlap  float64
		}{
			{0, 0.0},
			{1, 2.0},
			{2, 2.2},
			{3, 2.4},
			{4, 2.7},
			{5, 2.8},
			{99, 2.2},  // Invalid level should return default (Moderate)
			{-1, 2.2},  // Invalid level should return default (Moderate)
		}

		for _, tt := range tests {
			result := getMinimumOverlapForLevel(tt.level)
			if result != tt.minOverlap {
				t.Errorf("Level %d: got min overlap %.1f, want %.1f",
					tt.level, result, tt.minOverlap)
			}
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
			{99, 0.30},  // Invalid level should return default (Moderate)
			{-1, 0.30},  // Invalid level should return default (Moderate)
		}

		for _, tt := range tests {
			result := getThresholdForLevel(tt.level)
			if result != tt.threshold {
				t.Errorf("Level %d: got threshold %.2f, want %.2f",
					tt.level, result, tt.threshold)
			}
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
			{99, "Unknown"},  // Invalid level should return "Unknown"
			{-1, "Unknown"},  // Invalid level should return "Unknown"
		}

		for _, tt := range tests {
			result := getLevelName(tt.level)
			if result != tt.name {
				t.Errorf("Level %d: got name %q, want %q",
					tt.level, result, tt.name)
			}
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
			{99, "Unknown"},  // Invalid level should return "Unknown"
			{-1, "Unknown"},  // Invalid level should return "Unknown"
		}

		for _, tt := range tests {
			result := getHardwareRequirementForLevel(tt.level)
			if result != tt.hardware {
				t.Errorf("Level %d: got hardware %q, want %q",
					tt.level, result, tt.hardware)
			}
		}
	})

	t.Run("getLevelDescription", func(t *testing.T) {
		tests := []struct {
			level       int
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
			if result == "" {
				t.Errorf("Level %d: got empty description", tt.level)
				continue
			}

			for _, phrase := range tt.shouldContain {
				if !strings.Contains(result, phrase) {
					t.Errorf("Level %d: description missing expected phrase %q\nGot: %s",
						tt.level, phrase, result)
				}
			}
		}

		// Test invalid level
		result := getLevelDescription(99)
		if !strings.Contains(result, "Unknown") {
			t.Errorf("Invalid level should return 'Unknown', got: %s", result)
		}
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

			if result != tt.expectedMin {
				t.Errorf("%s\nGot minDetections = %d, want %d\nOverlap=%.1f",
					tt.description, result, tt.expectedMin, tt.overlap)
			}
		})
	}
}

// TestCalculateMinDetectionsIndependentOfClipLength verifies that
// audio clip length settings do NOT affect minDetections calculation.
// This is the fix for issue #1314.
// NOTE: Uses Level 2 (Moderate, 30% threshold) for realistic test values
func TestCalculateMinDetectionsIndependentOfClipLength(t *testing.T) {
	tests := []struct {
		name            string
		level           int
		overlap         float64
		clipConfigs     []struct{ length, preCapture int }
		expectedMinDet  int
		description     string
	}{
		{
			name:    "overlap_2.2_various_clip_lengths",
			level:   2, // Moderate (30%)
			overlap: 2.2,
			clipConfigs: []struct{ length, preCapture int }{
				{15, 3},   // default
				{30, 5},   // medium
				{60, 15},  // long (user's config from issue #1314)
				{60, 30},  // very long pre-capture
				{10, 0},   // short, no pre-capture
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

				if result != tt.expectedMinDet {
					t.Errorf("%s\nConfig %d (length=%ds, preCapture=%ds): Got minDetections = %d, want %d\nClip length should NOT affect minDetections!",
						tt.description, i+1, config.length, config.preCapture, result, tt.expectedMinDet)
				}
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
	for i := 0; i < 100; i++ {
		result := p.calculateMinDetections()
		if result != first {
			t.Errorf("Inconsistent results: iteration %d returned %d, expected %d", i, result, first)
		}
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
	if result != expectedMin {
		t.Errorf("Issue #1314 regression: Got minDetections = %d, want %d\n"+
			"Long clip lengths should NOT increase detection requirements!\n"+
			"Settings: level=%d, overlap=%.1f, captureLength=%ds, preCapture=%ds",
			result, expectedMin, p.Settings.Realtime.FalsePositiveFilter.Level,
			p.Settings.BirdNET.Overlap,
			p.Settings.Realtime.Audio.Export.Length,
			p.Settings.Realtime.Audio.Export.PreCapture)
	}

	// Verify it's reasonable by checking logs showed "matched 1/14 times"
	// With our fix, this should now be "matched 1/3 times" (rejected) or "matched 3/3 times" (accepted)
	if result >= 14 {
		t.Errorf("minDetections = %d is too high! This was the bug in issue #1314", result)
	}
}
