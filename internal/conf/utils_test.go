package conf

import (
	"os"
	"os/exec"
	"os/user"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/tphakala/birdnet-go/internal/errors"
)

func TestParseFfmpegVersion(t *testing.T) {
	tests := []struct {
		name        string
		output      string
		wantVersion string
		wantMajor   int
		wantMinor   int
	}{
		{
			name: "FFmpeg 7.1.2 Debian",
			output: `ffmpeg version 7.1.2-0+deb13u1 Copyright (c) 2000-2025 the FFmpeg developers
built with gcc 14 (Debian 14.2.0-19)`,
			wantVersion: "7.1.2-0+deb13u1",
			wantMajor:   7,
			wantMinor:   1,
		},
		{
			name: "FFmpeg 5.1.7 Raspberry Pi",
			output: `ffmpeg version 5.1.7-0+deb12u1+rpt1 Copyright (c) 2000-2025 the FFmpeg developers
built with gcc 12 (Debian 12.2.0-14+deb12u1)`,
			wantVersion: "5.1.7-0+deb12u1+rpt1",
			wantMajor:   5,
			wantMinor:   1,
		},
		{
			name: "FFmpeg 6.0",
			output: `ffmpeg version 6.0 Copyright (c) 2000-2023 the FFmpeg developers
built with gcc 11.3.0`,
			wantVersion: "6.0",
			wantMajor:   6,
			wantMinor:   0,
		},
		{
			name: "FFmpeg 4.4.2",
			output: `ffmpeg version 4.4.2-2ubuntu1 Copyright (c) 2000-2022 the FFmpeg developers
built with gcc 11 (Ubuntu 11.2.0-19ubuntu1)`,
			wantVersion: "4.4.2-2ubuntu1",
			wantMajor:   4,
			wantMinor:   4,
		},
		{
			name:        "Empty output",
			output:      "",
			wantVersion: "",
			wantMajor:   0,
			wantMinor:   0,
		},
		{
			name:        "Invalid format",
			output:      "some random text",
			wantVersion: "",
			wantMajor:   0,
			wantMinor:   0,
		},
		{
			name: "FFmpeg git build with libavutil",
			output: `ffmpeg version N-121000-g7321e4b950 Copyright (c) 2000-2025 the FFmpeg developers
built with gcc 11.4.0 (Ubuntu 11.4.0-1ubuntu1~22.04)
configuration: --prefix=/usr/local
libavutil      59.  8.100 / 59.  8.100
libavcodec     61.  3.100 / 61.  3.100
libavformat    61.  1.100 / 61.  1.100`,
			wantVersion: "N-121000-g7321e4b950",
			wantMajor:   7,
			wantMinor:   8,
		},
		{
			name: "FFmpeg 8.0 Windows (gyan.dev build)",
			output: `ffmpeg version 8.0-essentials_build-www.gyan.dev Copyright (c) 2000-2025 the FFmpeg developers
built with gcc 15.2.0 (Rev8, Built by MSYS2 project)
configuration: --enable-gpl --enable-version3
libavutil      60.  8.100 / 60.  8.100
libavcodec     62. 11.100 / 62. 11.100
libavformat    62.  3.100 / 62.  3.100`,
			wantVersion: "8.0-essentials_build-www.gyan.dev",
			wantMajor:   8,
			wantMinor:   0,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			gotVersion, gotMajor, gotMinor := ParseFfmpegVersion(tt.output)

			assert.Equal(t, tt.wantVersion, gotVersion, "version mismatch")
			assert.Equal(t, tt.wantMajor, gotMajor, "major version mismatch")
			assert.Equal(t, tt.wantMinor, gotMinor, "minor version mismatch")
		})
	}
}

func TestParsePercentage(t *testing.T) {
	t.Parallel()

	const testConfigKey = "disk_manager.retention_policy.min_disk_free"

	tests := []struct {
		name             string
		input            string
		wantValue        float64
		wantErr          bool
		wantParseErr     bool // true = wrapped strconv.ParseFloat error
		wantRangeErr     bool // true = out-of-range error
		wantNonFiniteErr bool // true = NaN/Inf error
	}{
		// --- with % suffix ---
		{name: "whole number with suffix", input: "85%", wantValue: 85.0},
		{name: "zero percent", input: "0%", wantValue: 0.0},
		{name: "one hundred percent", input: "100%", wantValue: 100.0},
		{name: "fractional percent", input: "99.5%", wantValue: 99.5},
		// --- bare integers (no % suffix) ---
		{name: "integer without suffix", input: "85", wantValue: 85.0},
		{name: "hundred without suffix", input: "100", wantValue: 100.0},
		{name: "zero without suffix", input: "0", wantValue: 0.0},
		{name: "bare number 25", input: "25", wantValue: 25.0},
		// --- bare decimals >= 1 ---
		{name: "decimal without suffix", input: "99.5", wantValue: 99.5},
		// --- fractional values auto-scaled (0 < x < 1 -> x*100) ---
		{name: "fraction 0.8 scaled to 80", input: "0.8", wantValue: 80.0},
		{name: "fraction 0.5 scaled to 50", input: "0.5", wantValue: 50.0},
		{name: "fraction 0.01 scaled to 1", input: "0.01", wantValue: 1.0},
		{name: "fraction 0.999 scaled to 99.9", input: "0.999", wantValue: 99.9},
		// --- whitespace handling ---
		{name: "leading whitespace trimmed", input: " 85%", wantValue: 85.0},
		{name: "trailing whitespace trimmed", input: "85% ", wantValue: 85.0},
		{name: "bare number with whitespace", input: " 80 ", wantValue: 80.0},
		// --- out-of-range values ---
		{name: "negative percent", input: "-10%", wantErr: true, wantRangeErr: true},
		{name: "negative bare number", input: "-5", wantErr: true, wantRangeErr: true},
		{name: "over 100 percent", input: "150%", wantErr: true, wantRangeErr: true},
		{name: "over 100 bare number", input: "200", wantErr: true, wantRangeErr: true},
		// --- non-finite values ---
		{name: "NaN", input: "NaN", wantErr: true, wantNonFiniteErr: true},
		{name: "positive infinity", input: "Inf", wantErr: true, wantNonFiniteErr: true},
		{name: "negative infinity", input: "-Inf", wantErr: true, wantNonFiniteErr: true},
		// --- invalid inputs ---
		{name: "letters before suffix", input: "abc%", wantErr: true, wantParseErr: true},
		{name: "empty before suffix", input: "%", wantErr: true, wantParseErr: true},
		{name: "multiple dots", input: "12.34.56%", wantErr: true, wantParseErr: true},
		{name: "pure letters", input: "abc", wantErr: true, wantParseErr: true},
		{name: "empty string", input: "", wantErr: true},
		{name: "whitespace only", input: " ", wantErr: true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			got, err := ParsePercentage(tt.input, testConfigKey)

			if !tt.wantErr {
				require.NoError(t, err)
				assert.InDelta(t, tt.wantValue, got, 1e-9)
				return
			}

			require.Error(t, err)
			assert.InDelta(t, 0.0, got, 1e-9, "on error, returned value must be zero")

			// All error paths must return a structured EnhancedError
			var enhanced *errors.EnhancedError
			require.ErrorAs(t, err, &enhanced, "expected *errors.EnhancedError, got %T", err)

			assert.Equal(t, errors.CategoryValidation, enhanced.Category)
			assert.Equal(t, "conf", enhanced.GetComponent())

			ctx := enhanced.GetContext()
			assert.Equal(t, tt.input, ctx["input"], "context must carry the original input")
			assert.Equal(t, testConfigKey, ctx["config_key"], "context must carry the config key")

			switch {
			case tt.wantParseErr:
				require.ErrorContains(t, err, "invalid syntax",
					"parse errors must wrap the underlying ParseFloat error")
			case tt.wantRangeErr:
				require.ErrorContains(t, err, "outside the 0-100 range",
					"range errors must indicate the value is out of bounds")
			case tt.wantNonFiniteErr:
				require.ErrorContains(t, err, "value is not finite",
					"non-finite errors must indicate the value is NaN or Inf")
			default:
				require.ErrorContains(t, err, "invalid percentage format",
					"format errors must carry the format-validation message")
			}
		})
	}
}

func TestGetFfmpegVersion(t *testing.T) {
	ffmpegPath, err := exec.LookPath(GetFfmpegBinaryName())
	if err != nil {
		t.Skip("ffmpeg not available on system, skipping integration test")
	}

	version, major, minor := GetFfmpegVersionFrom(ffmpegPath)
	require.NotEmpty(t, version)

	assert.GreaterOrEqual(t, major, 3, "major version should be at least 3")
	assert.LessOrEqual(t, major, 10, "major version should be at most 10")
	assert.GreaterOrEqual(t, minor, 0, "minor version should be non-negative")
	assert.LessOrEqual(t, minor, 99, "minor version should be at most 99")
	assert.NotEqual(t, 0, major, "failed to detect major version, got: version=%s, major=%d, minor=%d", version, major, minor)

	t.Logf("Detected FFmpeg version: %s (major: %d, minor: %d)", version, major, minor)
}

func TestFindConfigFile_ExplicitConfigPath(t *testing.T) {
	// Save and restore the global ConfigPath after the test.
	origConfigPath := ConfigPath
	t.Cleanup(func() {
		ConfigPath = origConfigPath
	})

	// Create a temporary config file.
	tmpDir := t.TempDir()
	customPath := filepath.Join(tmpDir, "custom-config.yaml")
	require.NoError(t, os.WriteFile(customPath, []byte("# test config"), 0o600))

	// Set ConfigPath to the custom path.
	ConfigPath = customPath

	got, err := FindConfigFile()
	require.NoError(t, err)
	assert.Equal(t, customPath, got, "FindConfigFile should return the explicit --config path")
}

func TestFindConfigFile_ExplicitConfigPathMissing(t *testing.T) {
	// Save and restore the global ConfigPath after the test.
	origConfigPath := ConfigPath
	t.Cleanup(func() {
		ConfigPath = origConfigPath
	})

	// Point ConfigPath at a file that does not exist.
	ConfigPath = "/nonexistent/path/config.yaml"

	_, err := FindConfigFile()
	require.Error(t, err)
	require.ErrorContains(t, err, "config file not found at explicit path")
	require.ErrorContains(t, err, ConfigPath)
}

func TestFindConfigFile_EmptyConfigPathFallsThrough(t *testing.T) {
	// Save and restore the global ConfigPath after the test.
	origConfigPath := ConfigPath
	t.Cleanup(func() {
		ConfigPath = origConfigPath
	})

	// Ensure ConfigPath is empty so FindConfigFile falls through to defaults.
	ConfigPath = ""

	// We cannot predict whether a default config file exists on the test
	// machine, so just verify the function does not error with
	// "explicit path" and either succeeds or returns a generic not-found.
	result, err := FindConfigFile()
	if err != nil {
		assert.NotContains(t, err.Error(), "explicit path",
			"with empty ConfigPath, error should not mention explicit path")
	} else {
		assert.NotEmpty(t, result, "returned path should not be empty on success")
	}
}

func TestResolveConfigDir_ExplicitConfigPathHonoredWhenMissing(t *testing.T) {
	// Save and restore the global ConfigPath after the test.
	origConfigPath := ConfigPath
	t.Cleanup(func() {
		ConfigPath = origConfigPath
	})

	// An explicit --config path pointing at a file that does not exist yet
	// (fresh install before the first save) must still resolve to that file's
	// directory, so co-located artifacts (the model catalog) land next to the
	// user-specified config rather than in a default directory.
	tmpDir := t.TempDir()
	ConfigPath = filepath.Join(tmpDir, "custom-config.yaml")

	dir, err := ResolveConfigDir()
	require.NoError(t, err)
	assert.Equal(t, tmpDir, dir, "ResolveConfigDir should return the explicit --config directory even when the file is absent")
}

func TestGetSoxFormats_WithExplicitPath(t *testing.T) {
	soxPath, err := exec.LookPath(GetSoxBinaryName())
	if err != nil {
		t.Skip("SoX not available in PATH, skipping")
	}

	formats := GetSoxFormats(soxPath)
	require.NotEmpty(t, formats, "SoX should report supported formats")
	assert.Contains(t, formats, "wav")
}

func TestGetSoxFormats_WithInvalidPath(t *testing.T) {
	formats := GetSoxFormats("/nonexistent/sox")
	assert.Empty(t, formats)
}

func TestGetFfmpegVersionFrom_WithExplicitPath(t *testing.T) {
	ffmpegPath, err := exec.LookPath(GetFfmpegBinaryName())
	if err != nil {
		t.Skip("FFmpeg not available in PATH, skipping")
	}

	version, major, _ := GetFfmpegVersionFrom(ffmpegPath)
	assert.NotEmpty(t, version)
	assert.Positive(t, major)
}

func TestGetFfmpegVersionFrom_WithInvalidPath(t *testing.T) {
	version, major, minor := GetFfmpegVersionFrom("/nonexistent/ffmpeg")
	assert.Empty(t, version)
	assert.Zero(t, major)
	assert.Zero(t, minor)
}

func TestGetUserHomeDir(t *testing.T) {
	t.Run("returns a non-empty directory", func(t *testing.T) {
		homeDir, err := GetUserHomeDir()
		require.NoError(t, err)
		assert.NotEmpty(t, homeDir)
		assert.DirExists(t, homeDir)
	})

	t.Run("result is cached", func(t *testing.T) {
		first, err1 := GetUserHomeDir()
		require.NoError(t, err1)
		second, err2 := GetUserHomeDir()
		require.NoError(t, err2)
		assert.Equal(t, first, second)
	})
}

func TestResolveHomeDir(t *testing.T) {
	t.Run("resolves without HOME set", func(t *testing.T) {
		if _, err := user.Current(); err != nil {
			t.Skip("skipping: os/user.Current() unavailable (minimal container without /etc/passwd)")
		}

		origHome := os.Getenv("HOME")
		t.Cleanup(func() { _ = os.Setenv("HOME", origHome) })
		require.NoError(t, os.Unsetenv("HOME"))

		homeDir, err := resolveHomeDir()
		require.NoError(t, err, "resolveHomeDir must succeed without $HOME via os/user.Current()")
		assert.NotEmpty(t, homeDir)
		assert.DirExists(t, homeDir)
	})
}

func TestExpandTildePath(t *testing.T) {
	t.Run("expands tilde prefix", func(t *testing.T) {
		result, err := ExpandTildePath("~/models/bird.tflite")
		require.NoError(t, err)
		assert.NotContains(t, result, "~")
		assert.True(t, filepath.IsAbs(result))
		assert.True(t, strings.HasSuffix(result, filepath.Join("models", "bird.tflite")))
	})

	t.Run("returns non-tilde path unchanged", func(t *testing.T) {
		result, err := ExpandTildePath("/usr/share/models/bird.tflite")
		require.NoError(t, err)
		assert.Equal(t, "/usr/share/models/bird.tflite", result)
	})

	t.Run("returns relative path unchanged", func(t *testing.T) {
		result, err := ExpandTildePath("models/bird.tflite")
		require.NoError(t, err)
		assert.Equal(t, "models/bird.tflite", result)
	})

	t.Run("handles bare tilde", func(t *testing.T) {
		result, err := ExpandTildePath("~")
		require.NoError(t, err)
		assert.NotEqual(t, "~", result)
		assert.DirExists(t, result)
	})

	t.Run("returns empty path unchanged", func(t *testing.T) {
		result, err := ExpandTildePath("")
		require.NoError(t, err)
		assert.Empty(t, result)
	})
}
