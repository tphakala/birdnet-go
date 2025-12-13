package api

import (
	"context"
	"log/slog"
	"slices"
	"strings"
	"testing"

	"github.com/tphakala/birdnet-go/internal/conf"
	"github.com/tphakala/birdnet-go/internal/securefs"
	"github.com/tphakala/birdnet-go/internal/spectrogram"
)

// Helper function to call generator's getSoxSpectrogramArgs (via exported test method)
func getSoxSpectrogramArgsHelper(t *testing.T, ctx context.Context, audioPath, outputPath string, width int, raw bool, settings *conf.Settings) []string {
	t.Helper()
	tempDir := t.TempDir()
	sfs, err := securefs.New(tempDir)
	if err != nil {
		t.Fatalf("Failed to create SecureFS: %v", err)
	}
	gen := spectrogram.NewGenerator(settings, sfs, slog.Default())
	return gen.GetSoxSpectrogramArgsForTest(ctx, audioPath, outputPath, width, raw)
}

// Helper function for benchmarks
func getSoxSpectrogramArgsBenchHelper(b *testing.B, ctx context.Context, audioPath, outputPath string, width int, raw bool, settings *conf.Settings) []string {
	b.Helper()
	tempDir := b.TempDir()
	sfs, err := securefs.New(tempDir)
	if err != nil {
		b.Fatalf("Failed to create SecureFS: %v", err)
	}
	gen := spectrogram.NewGenerator(settings, sfs, slog.Default())
	return gen.GetSoxSpectrogramArgsForTest(ctx, audioPath, outputPath, width, raw)
}

// TestGetSoxSpectrogramArgs_DurationParameterRequired verifies that the -d (duration)
// parameter is always included in Sox spectrogram arguments regardless of FFmpeg version.
// This ensures correct spectrogram generation where Sox shows the full audio duration
// rather than misinterpreting -x (width in pixels) as seconds (fixes issue #1484).
func TestGetSoxSpectrogramArgs_DurationParameterRequired(t *testing.T) {
	ctx := context.Background()
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
			name:               "FFmpeg 5.x includes duration parameter",
			ffmpegVersion:      "5.1.7-0+deb12u1+rpt1",
			ffmpegMajor:        5,
			ffmpegMinor:        1,
			expectDurationFlag: true,
			description:        "FFmpeg 5.x requires explicit -d parameter for correct spectrogram",
		},
		{
			name:               "FFmpeg 6.x includes duration parameter",
			ffmpegVersion:      "6.0",
			ffmpegMajor:        6,
			ffmpegMinor:        0,
			expectDurationFlag: true,
			description:        "FFmpeg 6.x requires explicit -d parameter for correct spectrogram",
		},
		{
			name:               "FFmpeg 7.x includes duration parameter",
			ffmpegVersion:      "7.1.2-0+deb13u1",
			ffmpegMajor:        7,
			ffmpegMinor:        1,
			expectDurationFlag: true,
			description:        "FFmpeg 7.x requires explicit -d parameter for correct spectrogram (fixes #1484)",
		},
		{
			name:               "FFmpeg 8.x includes duration parameter",
			ffmpegVersion:      "8.0-essentials_build-www.gyan.dev",
			ffmpegMajor:        8,
			ffmpegMinor:        0,
			expectDurationFlag: true,
			description:        "FFmpeg 8.x requires explicit -d parameter for correct spectrogram",
		},
		{
			name:               "Unknown version includes duration parameter",
			ffmpegVersion:      "",
			ffmpegMajor:        0,
			ffmpegMinor:        0,
			expectDurationFlag: true,
			description:        "Unknown FFmpeg version uses -d parameter as safety fallback",
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

			// Get the SoX arguments using helper
			width := 800
			args := getSoxSpectrogramArgsHelper(t, ctx, audioPath, absSpectrogramPath, width, raw, settings)

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
				found := slices.Contains(args, param)
				if !found {
					t.Errorf("Required SoX parameter %q missing from args: %v", param, args)
				}
			}

			// Verify -r flag for raw spectrograms
			if raw {
				hasRawFlag := slices.Contains(args, "-r")
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

	args := getSoxSpectrogramArgsHelper(t, ctx, "/tmp/test.flac", "/tmp/test.png", 800, true, settings)

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

	for b.Loop() {
		_ = getSoxSpectrogramArgsBenchHelper(b, ctx, "/tmp/test.flac", "/tmp/test.png", 800, true, settings)
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

	for b.Loop() {
		_ = getSoxSpectrogramArgsBenchHelper(b, ctx, "/tmp/test.flac", "/tmp/test.png", 800, true, settings)
	}
}

// TestGetSoxSpectrogramArgs_NilSettings verifies safe fallback behavior with nil settings
func TestGetSoxSpectrogramArgs_NilSettings(t *testing.T) {
	ctx := context.Background()

	// This should not panic and should use safety fallback (with duration parameter)
	// The function signature requires non-nil settings, but if nil is passed,
	// it should handle gracefully or panic is acceptable since it violates contract
	defer func() {
		if r := recover(); r != nil {
			// Panic is acceptable for nil settings as it's a programming error
			t.Logf("Function correctly panicked with nil settings: %v", r)
		}
	}()

	// Attempt to call with nil settings - this should either:
	// 1. Panic (acceptable - violates function contract)
	// 2. Use fallback behavior (defensive programming)
	args := getSoxSpectrogramArgsHelper(t, ctx, "/tmp/test.flac", "/tmp/test.png", 800, true, nil)

	// If we reach here without panic, verify duration parameter is present (safety fallback)
	hasDurationFlag := slices.Contains(args, "-d")

	if !hasDurationFlag {
		t.Errorf("With nil settings, expected safety fallback with -d flag, but it was missing")
	}

	t.Logf("Function handled nil settings without panic (defensive programming)")
}

// TestGetSoxSpectrogramArgs_PartialSettings verifies behavior with partially initialized settings
func TestGetSoxSpectrogramArgs_PartialSettings(t *testing.T) {
	ctx := context.Background()

	tests := []struct {
		name               string
		settings           *conf.Settings
		expectDurationFlag bool
		description        string
	}{
		{
			name: "Settings with Export but no FFmpeg version",
			settings: &conf.Settings{
				Realtime: conf.RealtimeSettings{
					Audio: conf.AudioSettings{
						Export: conf.ExportSettings{
							Length: 15,
						},
					},
				},
			},
			expectDurationFlag: true,
			description:        "No FFmpeg version info should use safety fallback with -d flag",
		},
		{
			name: "Settings with version string but major=0",
			settings: &conf.Settings{
				Realtime: conf.RealtimeSettings{
					Audio: conf.AudioSettings{
						FfmpegVersion: "N-121000-g7321e4b950", // Git build version
						FfmpegMajor:   0,                      // Not parsed correctly
						FfmpegMinor:   0,
						Export: conf.ExportSettings{
							Length: 15,
						},
					},
				},
			},
			expectDurationFlag: true,
			description:        "Version string present but major=0 should use safety fallback",
		},
		{
			name: "Settings with major=7 but empty version string",
			settings: &conf.Settings{
				Realtime: conf.RealtimeSettings{
					Audio: conf.AudioSettings{
						FfmpegVersion: "", // Empty version string
						FfmpegMajor:   7,
						FfmpegMinor:   1,
						Export: conf.ExportSettings{
							Length: 15,
						},
					},
				},
			},
			expectDurationFlag: true,
			description:        "Empty version string should use safety fallback (HasFfmpegVersion returns false)",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			args := getSoxSpectrogramArgsHelper(t, ctx, "/tmp/test.flac", "/tmp/test.png", 800, true, tt.settings)

			hasDurationFlag := slices.Contains(args, "-d")

			if hasDurationFlag != tt.expectDurationFlag {
				t.Errorf("%s:\n  Expected -d flag: %v\n  Got -d flag: %v\n  Args: %v",
					tt.description, tt.expectDurationFlag, hasDurationFlag, args)
			}

			t.Logf("Test passed: %s", tt.description)
		})
	}
}
