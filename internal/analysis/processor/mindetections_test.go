package processor

import (
	"testing"

	"github.com/tphakala/birdnet-go/internal/conf"
)

// TestCalculateMinDetections verifies that the minimum detection threshold
// is calculated based only on overlap setting (not clip length) to filter
// false positives through repeated detection confirmation.
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
			expectedMin: 1,
			description: "Overlap 1.5: step=1.5s, max ~2 detections per 3s, require 1 (50% of 2)",
		},
		{
			name:        "medium_overlap_2.0",
			overlap:     2.0,
			expectedMin: 2,
			description: "Overlap 2.0: step=1.0s, max 3 detections per 3s, require 2 (50% of 3)",
		},
		{
			name:        "high_overlap_2.2",
			overlap:     2.2,
			expectedMin: 2,
			description: "Overlap 2.2: step=0.8s, max ~4 detections per 3s, require 2 (50% of 4)",
		},
		{
			name:        "high_overlap_2.4",
			overlap:     2.4,
			expectedMin: 3,
			description: "Overlap 2.4: step=0.6s, max 5 detections per 3s, require 3 (50% of 5)",
		},
		{
			name:        "very_high_overlap_2.5",
			overlap:     2.5,
			expectedMin: 3,
			description: "Overlap 2.5: step=0.5s, max 6 detections per 3s, require 3 (50% of 6)",
		},
		{
			name:        "extreme_overlap_2.7",
			overlap:     2.7,
			expectedMin: 5,
			description: "Overlap 2.7: step=0.3s, max 10 detections per 3s, require 5 (50% of 10)",
		},
		{
			name:        "extreme_overlap_2.8",
			overlap:     2.8,
			expectedMin: 8,
			description: "Overlap 2.8: step=0.2s, max 15 detections per 3s, require 8 (ceil(50% of 15))",
		},
		{
			name:        "extreme_overlap_2.85",
			overlap:     2.85,
			expectedMin: 10,
			description: "Overlap 2.85: step=0.15s, max 20 detections per 3s, require 10 (50% of 20) - tests epsilon fix",
		},
		{
			name:        "near_max_overlap_2.9",
			overlap:     2.9,
			expectedMin: 15,
			description: "Overlap 2.9: step=0.1s, max 30 detections per 3s, require 15 (50% of 30)",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			// Create processor with test settings
			// Note: captureLength and preCapture should NOT affect the result
			p := &Processor{
				Settings: &conf.Settings{
					Realtime: conf.RealtimeSettings{
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
func TestCalculateMinDetectionsIndependentOfClipLength(t *testing.T) {
	tests := []struct {
		name            string
		overlap         float64
		clipConfigs     []struct{ length, preCapture int }
		expectedMinDet  int
		description     string
	}{
		{
			name:    "overlap_2.2_various_clip_lengths",
			overlap: 2.2,
			clipConfigs: []struct{ length, preCapture int }{
				{15, 3},   // default
				{30, 5},   // medium
				{60, 15},  // long (user's config from issue #1314)
				{60, 30},  // very long pre-capture
				{10, 0},   // short, no pre-capture
			},
			expectedMinDet: 2,
			description:    "Overlap 2.2 should always require 2 detections regardless of clip length",
		},
		{
			name:    "overlap_2.4_various_clip_lengths",
			overlap: 2.4,
			clipConfigs: []struct{ length, preCapture int }{
				{15, 3},
				{45, 10},
				{60, 20},
			},
			expectedMinDet: 3,
			description:    "Overlap 2.4 should always require 3 detections regardless of clip length",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			for i, config := range tt.clipConfigs {
				p := &Processor{
					Settings: &conf.Settings{
						Realtime: conf.RealtimeSettings{
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
func TestCalculateMinDetectionsIssue1314(t *testing.T) {
	// User's actual configuration from issue #1314
	p := &Processor{
		Settings: &conf.Settings{
			Realtime: conf.RealtimeSettings{
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
	// With the fix, this should be 2 (reasonable)
	expectedMin := 2
	if result != expectedMin {
		t.Errorf("Issue #1314 regression: Got minDetections = %d, want %d\n"+
			"Long clip lengths should NOT increase detection requirements!\n"+
			"Settings: overlap=%.1f, captureLength=%ds, preCapture=%ds",
			result, expectedMin, p.Settings.BirdNET.Overlap,
			p.Settings.Realtime.Audio.Export.Length,
			p.Settings.Realtime.Audio.Export.PreCapture)
	}

	// Verify it's reasonable by checking logs showed "matched 1/14 times"
	// With our fix, this should now be "matched 1/2 times" (rejected) or "matched 2/2 times" (accepted)
	if result >= 14 {
		t.Errorf("minDetections = %d is too high! This was the bug in issue #1314", result)
	}
}
