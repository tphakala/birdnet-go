package api

import (
	"context"
	"strings"
	"testing"

	"github.com/tphakala/birdnet-go/internal/conf"
)

// TestGetSoxSpectrogramArgs_FFmpegVersionOptimization verifies the FFmpeg 7.x optimization
// that skips the expensive ffprobe call by omitting the -d (duration) parameter.
//
//nolint:gocognit // Comprehensive test with multiple validation steps per test case
func TestGetSoxSpectrogramArgs_FFmpegVersionOptimization(t *testing.T) {
	ctx := context.Background()
	widthStr := "800"
	heightStr := "400"
	absSpectrogramPath := "/tmp/test.png"
	audioPath := "/tmp/test.flac"
	raw := true

	tests := []struct {
		name               string
		ffmpegVersion      string
		ffmpegMajor        int
		ffmpegMinor        int
		expectDurationFlag bool
		description        string
	}{
		{
			name:               "FFmpeg 5.x needs duration parameter",
			ffmpegVersion:      "5.1.7-0+deb12u1+rpt1",
			ffmpegMajor:        5,
			ffmpegMinor:        1,
			expectDurationFlag: true,
			description:        "FFmpeg 5.x has sox protocol bug, requires explicit -d parameter",
		},
		{
			name:               "FFmpeg 6.x needs duration parameter (conservative)",
			ffmpegVersion:      "6.0",
			ffmpegMajor:        6,
			ffmpegMinor:        0,
			expectDurationFlag: true,
			description:        "FFmpeg 6.x treated conservatively, requires explicit -d parameter",
		},
		{
			name:               "FFmpeg 7.x skips duration parameter (optimization)",
			ffmpegVersion:      "7.1.2-0+deb13u1",
			ffmpegMajor:        7,
			ffmpegMinor:        1,
			expectDurationFlag: false,
			description:        "FFmpeg 7.x has sox protocol fix, -d parameter omitted for performance",
		},
		{
			name:               "FFmpeg 8.x skips duration parameter",
			ffmpegVersion:      "8.0-essentials_build-www.gyan.dev",
			ffmpegMajor:        8,
			ffmpegMinor:        0,
			expectDurationFlag: false,
			description:        "FFmpeg 8.x and later benefit from optimization",
		},
		{
			name:               "Unknown version uses duration parameter (safety fallback)",
			ffmpegVersion:      "",
			ffmpegMajor:        0,
			ffmpegMinor:        0,
			expectDurationFlag: true,
			description:        "Unknown FFmpeg version requires ffprobe for safety (cannot verify sox protocol fix)",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			// Create mock settings with specific FFmpeg version
			settings := &conf.Settings{
				Realtime: conf.RealtimeSettings{
					Audio: conf.AudioSettings{
						FfmpegVersion: tt.ffmpegVersion,
						FfmpegMajor:   tt.ffmpegMajor,
						FfmpegMinor:   tt.ffmpegMinor,
						Export: conf.ExportSettings{
							Length: 15, // Default capture length for fallback
						},
					},
				},
			}

			// Get the SoX arguments
			args := getSoxSpectrogramArgs(ctx, widthStr, heightStr, absSpectrogramPath, audioPath, raw, settings)

			// Convert args to string for easier inspection
			argsStr := strings.Join(args, " ")

			// Check if -d flag is present
			hasDurationFlag := false
			for i, arg := range args {
				if arg == "-d" {
					hasDurationFlag = true
					// Verify the next argument is a numeric duration
					if i+1 < len(args) {
						if args[i+1] == "" {
							t.Errorf("Duration parameter (-d) present but value is empty")
						}
					} else {
						t.Errorf("Duration parameter (-d) present but no value follows")
					}
					break
				}
			}

			if hasDurationFlag != tt.expectDurationFlag {
				t.Errorf("Unexpected -d flag presence:\n"+
					"  FFmpeg version: %s (major: %d, minor: %d)\n"+
					"  Expected -d flag: %v\n"+
					"  Got -d flag: %v\n"+
					"  Args: %s\n"+
					"  Reason: %s",
					tt.ffmpegVersion, tt.ffmpegMajor, tt.ffmpegMinor,
					tt.expectDurationFlag, hasDurationFlag,
					argsStr,
					tt.description)
			}

			// Verify essential SoX parameters are always present
			requiredParams := []string{"-n", "rate", "24k", "spectrogram", "-x", "-y", "-z", "-o"}
			for _, param := range requiredParams {
				found := false
				for _, arg := range args {
					if arg == param {
						found = true
						break
					}
				}
				if !found {
					t.Errorf("Required SoX parameter %q missing from args: %v", param, args)
				}
			}

			// Verify -r flag for raw spectrograms
			if raw {
				hasRawFlag := false
				for _, arg := range args {
					if arg == "-r" {
						hasRawFlag = true
						break
					}
				}
				if !hasRawFlag {
					t.Errorf("Raw flag (-r) should be present for raw=true, args: %v", args)
				}
			}

			t.Logf("Test passed: %s\n  Version: %s\n  Args: %s",
				tt.description, tt.ffmpegVersion, argsStr)
		})
	}
}

// TestGetSoxSpectrogramArgs_ArgumentOrder verifies that SoX arguments are in correct order
func TestGetSoxSpectrogramArgs_ArgumentOrder(t *testing.T) {
	ctx := context.Background()
	settings := &conf.Settings{
		Realtime: conf.RealtimeSettings{
			Audio: conf.AudioSettings{
				FfmpegVersion: "7.1.2",
				FfmpegMajor:   7,
				FfmpegMinor:   1,
				Export: conf.ExportSettings{
					Length: 15,
				},
			},
		},
	}

	args := getSoxSpectrogramArgs(ctx, "800", "400", "/tmp/test.png", "/tmp/test.flac", true, settings)

	// Verify the base arguments are in correct order
	expectedStart := []string{"-n", "rate", "24k", "spectrogram", "-x", "800", "-y", "400"}

	if len(args) < len(expectedStart) {
		t.Fatalf("Not enough arguments returned, got %d, expected at least %d", len(args), len(expectedStart))
	}

	for i, expected := range expectedStart {
		if args[i] != expected {
			t.Errorf("Argument mismatch at position %d: expected %q, got %q", i, expected, args[i])
		}
	}
}

// BenchmarkGetSoxSpectrogramArgs_WithFFmpeg7 benchmarks the optimized path (no ffprobe call)
func BenchmarkGetSoxSpectrogramArgs_WithFFmpeg7(b *testing.B) {
	ctx := context.Background()
	settings := &conf.Settings{
		Realtime: conf.RealtimeSettings{
			Audio: conf.AudioSettings{
				FfmpegVersion: "7.1.2",
				FfmpegMajor:   7,
				FfmpegMinor:   1,
				Export: conf.ExportSettings{
					Length: 15,
				},
			},
		},
	}

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		_ = getSoxSpectrogramArgs(ctx, "800", "400", "/tmp/test.png", "/tmp/test.flac", true, settings)
	}
}

// BenchmarkGetSoxSpectrogramArgs_WithFFmpeg5 benchmarks the non-optimized path (with ffprobe)
// Note: This will be slower due to the ffprobe call, but it's necessary for FFmpeg 5.x
func BenchmarkGetSoxSpectrogramArgs_WithFFmpeg5(b *testing.B) {
	ctx := context.Background()
	settings := &conf.Settings{
		Realtime: conf.RealtimeSettings{
			Audio: conf.AudioSettings{
				FfmpegVersion: "5.1.7",
				FfmpegMajor:   5,
				FfmpegMinor:   1,
				Export: conf.ExportSettings{
					Length: 15,
				},
			},
		},
	}

	// Note: This benchmark will show the overhead of the duration lookup path
	// In production, the cache would help reduce this overhead for repeated calls
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		_ = getSoxSpectrogramArgs(ctx, "800", "400", "/tmp/test.png", "/tmp/test.flac", true, settings)
	}
}
