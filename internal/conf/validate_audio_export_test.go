// Package conf tests the audio export validation and normalization logic
// that prevents extension-less clip_name rows (GitHub #2810, #2814).
package conf

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// fakeFFmpegPath writes an executable stub inside t.TempDir() and returns its
// path. Tests need a resolvable, host-agnostic FfmpegPath because
// validateAudioSettings invokes ValidateToolPath, which clears FfmpegPath when
// the configured path cannot be executed. A hardcoded /usr/bin/ffmpeg breaks
// on hosts where ffmpeg lives elsewhere (e.g. /opt/homebrew/bin).
func fakeFFmpegPath(t *testing.T) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), GetFfmpegBinaryName())
	require.NoError(t, os.WriteFile(path, []byte("#!/bin/sh\nexit 0\n"), 0o755))
	return path
}

// The "missing ffmpeg forces wav" branch is not covered here because
// validateAudioSettings calls ValidateToolPath, which falls back to
// exec.LookPath("ffmpeg") when the configured FfmpegPath is empty.
// In any CI environment with ffmpeg on PATH the branch is unreachable
// without mocking exec.LookPath. Behaviour is exercised by the
// existing integration test suite in internal/analysis.
func TestValidateAudioSettings_NormalizesEmptyExportType(t *testing.T) {
	t.Parallel()
	ffmpegPath := fakeFFmpegPath(t)

	cases := []struct {
		name       string
		enabled    bool
		typeIn     string
		bitrateIn  string
		ffmpegPath string
		wantErr    bool
		wantType   string
	}{
		{
			name:       "disabled with empty type normalizes to wav",
			enabled:    false,
			typeIn:     "",
			bitrateIn:  "",
			ffmpegPath: ffmpegPath,
			wantErr:    false,
			wantType:   "wav",
		},
		{
			name:       "enabled with empty type normalizes to wav",
			enabled:    true,
			typeIn:     "",
			bitrateIn:  "",
			ffmpegPath: ffmpegPath,
			wantErr:    false,
			wantType:   "wav",
		},
		{
			name:       "disabled with garbage type now rejected",
			enabled:    false,
			typeIn:     "gibberish",
			bitrateIn:  "",
			ffmpegPath: ffmpegPath,
			wantErr:    true,
			wantType:   "gibberish",
		},
		{
			name:       "enabled wav with empty bitrate is fine",
			enabled:    true,
			typeIn:     "wav",
			bitrateIn:  "",
			ffmpegPath: ffmpegPath,
			wantErr:    false,
			wantType:   "wav",
		},
		{
			name:       "enabled mp3 with empty bitrate rejected",
			enabled:    true,
			typeIn:     "mp3",
			bitrateIn:  "",
			ffmpegPath: ffmpegPath,
			wantErr:    true,
			wantType:   "mp3",
		},
		{
			name:       "enabled mp3 with valid bitrate accepted",
			enabled:    true,
			typeIn:     "mp3",
			bitrateIn:  "128k",
			ffmpegPath: ffmpegPath,
			wantErr:    false,
			wantType:   "mp3",
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			settings := newMinimalAudioSettings()
			settings.Export.Enabled = tc.enabled
			settings.Export.Type = tc.typeIn
			settings.Export.Bitrate = tc.bitrateIn
			settings.FfmpegPath = tc.ffmpegPath

			err := validateAudioSettings(settings)

			if tc.wantErr {
				require.Error(t, err)
			} else {
				require.NoError(t, err)
			}
			assert.Equal(t, tc.wantType, settings.Export.Type,
				"Export.Type should be normalized in-memory after validateAudioSettings")
		})
	}
}

// TestValidateAudioSettings_NeverReturnsEmptyType is the class-of-bug invariant:
// regardless of input, a successful validation must leave Export.Type non-empty.
func TestValidateAudioSettings_NeverReturnsEmptyType(t *testing.T) {
	t.Parallel()
	ffmpegPath := fakeFFmpegPath(t)

	inputs := []string{"", " ", "wav", "WAV", "mp3", "aac", "opus", "flac"}
	for _, in := range inputs {
		t.Run("input="+in, func(t *testing.T) {
			t.Parallel()

			settings := newMinimalAudioSettings()
			settings.Export.Enabled = false // gates that used to skip normalization
			settings.Export.Type = in
			settings.Export.Bitrate = "128k"
			settings.FfmpegPath = ffmpegPath

			_ = validateAudioSettings(settings) // may or may not error
			require.NotEmpty(t, settings.Export.Type,
				"Export.Type is empty after validation (input=%q)", in)
			require.NotEmpty(t, strings.TrimSpace(settings.Export.Type),
				"Export.Type is whitespace after validation (input=%q)", in)
		})
	}
}

// newMinimalAudioSettings returns an AudioSettings instance populated with the
// minimum fields needed to pass validateAudioSettings apart from the fields
// the individual test cases override. Values mirror the Viper defaults from
// internal/conf/defaults.go.
func newMinimalAudioSettings() *AudioSettings {
	s := &AudioSettings{}
	s.Source = testAudioDeviceSysdefault
	s.Sources = []AudioSourceConfig{{Name: "Default", Device: testAudioDeviceSysdefault}}
	s.Export.Enabled = true
	s.Export.Length = 15
	s.Export.PreCapture = 3
	s.Export.Gain = 0
	s.Export.Type = "wav"
	s.Export.Bitrate = "96k"
	s.Export.Path = "clips/"
	s.Export.Retention.Policy = RetentionPolicyNone
	return s
}
