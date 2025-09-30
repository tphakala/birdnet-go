package processor

import (
	"testing"

	"github.com/tphakala/birdnet-go/internal/conf"
)

// TestCalculateMinDetections verifies that the minimum detection threshold
// scales correctly based on detection window duration and overlap settings.
func TestCalculateMinDetections(t *testing.T) {
	tests := []struct {
		name             string
		captureLength    int // seconds
		preCaptureLength int // seconds
		overlap          float64
		expectedMin      int
		description      string
	}{
		{
			name:             "default_12s_window_high_overlap",
			captureLength:    15,
			preCaptureLength: 3,
			overlap:          2.4,
			expectedMin:      4,
			description:      "Default settings: 15s clip - 3s pre-capture = 12s window, overlap 2.4 should require 4 detections",
		},
		{
			name:             "default_15s_window_high_overlap",
			captureLength:    15,
			preCaptureLength: 0,
			overlap:          2.4,
			expectedMin:      5,
			description:      "15s window with no pre-capture, overlap 2.4 should require 5 detections (baseline)",
		},
		{
			name:             "no_overlap_default_window",
			captureLength:    15,
			preCaptureLength: 3,
			overlap:          0.0,
			expectedMin:      1,
			description:      "No overlap should accept first detection regardless of window size",
		},
		{
			name:             "no_overlap_15s_window",
			captureLength:    15,
			preCaptureLength: 0,
			overlap:          0.0,
			expectedMin:      1,
			description:      "No overlap with 15s baseline window should require exactly 1 detection",
		},
		{
			name:             "long_clip_30s_window",
			captureLength:    60,
			preCaptureLength: 30,
			overlap:          2.4,
			expectedMin:      10,
			description:      "Long clips: 60s - 30s = 30s window (2x baseline) should require 10 detections",
		},
		{
			name:             "short_clip_3s_window",
			captureLength:    6,
			preCaptureLength: 3,
			overlap:          2.4,
			expectedMin:      1,
			description:      "Short clips: 6s - 3s = 3s window should require at least 1 detection",
		},
		{
			name:             "very_short_clip_near_zero_window",
			captureLength:    4,
			preCaptureLength: 3,
			overlap:          2.4,
			expectedMin:      1,
			description:      "Very short window (1s) should still require minimum 1 detection",
		},
		{
			name:             "edge_case_zero_window",
			captureLength:    3,
			preCaptureLength: 3,
			overlap:          2.4,
			expectedMin:      1,
			description:      "Edge case: captureLength == preCaptureLength results in 0s window, should require minimum 1",
		},
		{
			name:             "edge_case_negative_window_clamped",
			captureLength:    2,
			preCaptureLength: 3,
			overlap:          2.4,
			expectedMin:      1,
			description:      "Edge case: captureLength < preCaptureLength gets clamped to 0, should require minimum 1",
		},
		{
			name:             "high_overlap_2.7",
			captureLength:    15,
			preCaptureLength: 3,
			overlap:          2.7,
			expectedMin:      8,
			description:      "Very high overlap (2.7) with 12s window should require 8 detections",
		},
		{
			name:             "high_overlap_2.9",
			captureLength:    15,
			preCaptureLength: 3,
			overlap:          2.9,
			expectedMin:      24,
			description:      "Extreme overlap (2.9) with 12s window should require 24 detections",
		},
		{
			name:             "medium_overlap_1.5",
			captureLength:    15,
			preCaptureLength: 3,
			overlap:          1.5,
			expectedMin:      2,
			description:      "Medium overlap (1.5) with 12s window should require 2 detections",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			// Create processor with test settings
			p := &Processor{
				Settings: &conf.Settings{
					Realtime: conf.RealtimeSettings{
						Audio: conf.AudioSettings{
							Export: conf.ExportSettings{
								Length:     tt.captureLength,
								PreCapture: tt.preCaptureLength,
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
				t.Errorf("%s\nGot minDetections = %d, want %d\nSettings: captureLength=%ds, preCaptureLength=%ds, overlap=%.1f",
					tt.description, result, tt.expectedMin, tt.captureLength, tt.preCaptureLength, tt.overlap)
			}
		})
	}
}

// TestCalculateMinDetectionsScaling verifies that minDetections scales
// proportionally with detection window duration.
func TestCalculateMinDetectionsScaling(t *testing.T) {
	tests := []struct {
		name             string
		baseCapture      int
		basePreCapture   int
		scaleCapture     int
		scalePreCapture  int
		overlap          float64
		scaleFactor      float64 // Expected scale factor
		description      string
	}{
		{
			name:            "double_window_doubles_detections",
			baseCapture:     15,
			basePreCapture:  0,
			scaleCapture:    30,
			scalePreCapture: 0,
			overlap:         2.4,
			scaleFactor:     2.0,
			description:     "30s window should require 2x the detections of 15s window",
		},
		{
			name:            "half_window_halves_detections",
			baseCapture:     15,
			basePreCapture:  0,
			scaleCapture:    8,
			scalePreCapture: 0,
			overlap:         2.4,
			scaleFactor:     8.0 / 15.0,
			description:     "8s window should require proportionally fewer detections",
		},
		{
			name:            "triple_window_triples_detections",
			baseCapture:     15,
			basePreCapture:  0,
			scaleCapture:    45,
			scalePreCapture: 0,
			overlap:         2.7,
			scaleFactor:     3.0,
			description:     "45s window should require 3x the detections of 15s window",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			// Calculate base minDetections
			baseProcessor := &Processor{
				Settings: &conf.Settings{
					Realtime: conf.RealtimeSettings{
						Audio: conf.AudioSettings{
							Export: conf.ExportSettings{
								Length:     tt.baseCapture,
								PreCapture: tt.basePreCapture,
							},
						},
					},
					BirdNET: conf.BirdNETConfig{
						Overlap: tt.overlap,
					},
				},
			}
			baseMin := baseProcessor.calculateMinDetections()

			// Calculate scaled minDetections
			scaledProcessor := &Processor{
				Settings: &conf.Settings{
					Realtime: conf.RealtimeSettings{
						Audio: conf.AudioSettings{
							Export: conf.ExportSettings{
								Length:     tt.scaleCapture,
								PreCapture: tt.scalePreCapture,
							},
						},
					},
					BirdNET: conf.BirdNETConfig{
						Overlap: tt.overlap,
					},
				},
			}
			scaledMin := scaledProcessor.calculateMinDetections()

			// Verify scaling is proportional
			expectedScaled := int(float64(baseMin) * tt.scaleFactor)
			// Allow for rounding differences (±1)
			if scaledMin < expectedScaled-1 || scaledMin > expectedScaled+1 {
				t.Errorf("%s\nBase: %d detections, Scaled: %d detections (expected ~%d)\nScale factor: %.2f",
					tt.description, baseMin, scaledMin, expectedScaled, tt.scaleFactor)
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