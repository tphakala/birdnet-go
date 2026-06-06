package processor

import (
	"encoding/binary"
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/tphakala/birdnet-go/internal/conf"
	"github.com/tphakala/birdnet-go/internal/conf/conftest"
)

// makeSilentPCM16 creates a zero-filled 16-bit PCM byte slice with the given
// number of samples. Silence is valid PCM and compresses well, making it
// suitable for fast integration tests that only care about metadata.
func makeSilentPCM16(t *testing.T, sampleCount int) []byte {
	t.Helper()
	return make([]byte, sampleCount*2)
}

// readWAVSampleRate parses the sample rate from a WAV file header (bytes 24-27).
func readWAVSampleRate(t *testing.T, path string) int {
	t.Helper()
	data, err := os.ReadFile(path)
	require.NoError(t, err)
	require.GreaterOrEqual(t, len(data), 28, "WAV file too short to contain sample rate field")
	return int(binary.LittleEndian.Uint32(data[24:28]))
}

// TestSaveAudioAction_BirdDownsampledTo48kHz verifies that bird detections
// at high sample rates (e.g. 192kHz) are downsampled to 48kHz on export.
func TestSaveAudioAction_BirdDownsampledTo48kHz(t *testing.T) {
	t.Parallel()

	tmpDir := t.TempDir()
	settings := conftest.NewTestSettings().
		WithAudioExport(tmpDir, "wav", "192k").
		Build()

	const sourceRate = 192000
	const durationSamples = sourceRate * 3 // 3 seconds
	pcm := makeSilentPCM16(t, durationSamples)

	action := &SaveAudioAction{
		Settings:         settings,
		ClipName:         "bird-192k.wav",
		pcmData:          pcm,
		sourceSampleRate: sourceRate,
		modelName:        "BirdNET",
		CorrelationID:    "test-bird-downsample",
	}

	require.NoError(t, action.Execute(t.Context(), nil))

	outputPath := filepath.Join(tmpDir, "bird-192k.wav")
	rate := readWAVSampleRate(t, outputPath)
	assert.Equal(t, conf.SampleRate, rate, "bird audio should be downsampled to 48kHz")
}

// TestSaveAudioAction_BatPreservesNativeRate verifies that bat detections
// at high sample rates export at the native rate without downsampling.
func TestSaveAudioAction_BatPreservesNativeRate(t *testing.T) {
	t.Parallel()

	tmpDir := t.TempDir()
	settings := conftest.NewTestSettings().
		WithAudioExport(tmpDir, "wav", "192k").
		Build()

	const sourceRate = 256000
	const durationSamples = sourceRate * 3
	pcm := makeSilentPCM16(t, durationSamples)

	action := &SaveAudioAction{
		Settings:         settings,
		ClipName:         "bat-256k.wav",
		pcmData:          pcm,
		sourceSampleRate: sourceRate,
		modelName:        "BattyBirdNET",
		CorrelationID:    "test-bat-native",
	}

	require.NoError(t, action.Execute(t.Context(), nil))

	outputPath := filepath.Join(tmpDir, "bat-256k.wav")
	rate := readWAVSampleRate(t, outputPath)
	assert.Equal(t, sourceRate, rate, "bat audio should stay at 256kHz")
}

// TestSaveAudioAction_BatMP3FallsBackToWAV verifies that bat detections
// configured with MP3 (which caps at 48kHz) silently fall back to WAV.
func TestSaveAudioAction_BatMP3FallsBackToWAV(t *testing.T) {
	t.Parallel()

	tmpDir := t.TempDir()
	settings := conftest.NewTestSettings().
		WithAudioExport(tmpDir, "mp3", "192k").
		Build()

	const sourceRate = 256000
	const durationSamples = sourceRate * 3
	pcm := makeSilentPCM16(t, durationSamples)

	action := &SaveAudioAction{
		Settings:         settings,
		ClipName:         "bat-256k.mp3",
		pcmData:          pcm,
		sourceSampleRate: sourceRate,
		modelName:        "BattyBirdNET",
		CorrelationID:    "test-bat-mp3-fallback",
	}

	require.NoError(t, action.Execute(t.Context(), nil))

	// MP3 file should NOT exist
	mp3Path := filepath.Join(tmpDir, "bat-256k.mp3")
	_, err := os.Stat(mp3Path)
	assert.True(t, os.IsNotExist(err), "MP3 file should not be created for bat audio at high rates")

	// WAV file SHOULD exist
	wavPath := filepath.Join(tmpDir, "bat-256k.wav")
	rate := readWAVSampleRate(t, wavPath)
	assert.Equal(t, sourceRate, rate, "fallback WAV should preserve native 256kHz rate")
}

// TestSaveAudioAction_BirdAt48kHzNoResample verifies that bird audio already
// at 48kHz is not resampled (no unnecessary work).
func TestSaveAudioAction_BirdAt48kHzNoResample(t *testing.T) {
	t.Parallel()

	tmpDir := t.TempDir()
	settings := conftest.NewTestSettings().
		WithAudioExport(tmpDir, "wav", "192k").
		Build()

	const sourceRate = 48000
	const durationSamples = sourceRate * 3
	pcm := makeSilentPCM16(t, durationSamples)

	action := &SaveAudioAction{
		Settings:         settings,
		ClipName:         "bird-48k.wav",
		pcmData:          pcm,
		sourceSampleRate: sourceRate,
		modelName:        "BirdNET",
		CorrelationID:    "test-bird-48k",
	}

	require.NoError(t, action.Execute(t.Context(), nil))

	outputPath := filepath.Join(tmpDir, "bird-48k.wav")
	rate := readWAVSampleRate(t, outputPath)
	assert.Equal(t, sourceRate, rate, "bird audio at 48kHz should not be resampled")
}

// TestSaveAudioAction_BatOpusFallsBackToWAV verifies that Opus format also
// falls back to WAV for bat audio at high sample rates.
func TestSaveAudioAction_BatOpusFallsBackToWAV(t *testing.T) {
	t.Parallel()

	tmpDir := t.TempDir()
	settings := conftest.NewTestSettings().
		WithAudioExport(tmpDir, "opus", "128k").
		Build()

	const sourceRate = 256000
	const durationSamples = sourceRate * 3
	pcm := makeSilentPCM16(t, durationSamples)

	action := &SaveAudioAction{
		Settings:         settings,
		ClipName:         "bat-256k.opus",
		pcmData:          pcm,
		sourceSampleRate: sourceRate,
		modelName:        "BattyBirdNET",
		CorrelationID:    "test-bat-opus-fallback",
	}

	require.NoError(t, action.Execute(t.Context(), nil))

	// Opus file should NOT exist
	opusPath := filepath.Join(tmpDir, "bat-256k.opus")
	_, err := os.Stat(opusPath)
	assert.True(t, os.IsNotExist(err), "Opus file should not be created for bat audio at high rates")

	// WAV file SHOULD exist
	wavPath := filepath.Join(tmpDir, "bat-256k.wav")
	rate := readWAVSampleRate(t, wavPath)
	assert.Equal(t, sourceRate, rate, "fallback WAV should preserve native 256kHz rate")
}

// TestNeedsBatFormatFallback verifies the bat format fallback logic using
// model name, source rate, and export format.
func TestNeedsBatFormatFallback(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name     string
		model    string
		rate     int
		format   string
		expected bool
	}{
		{"bat_high_rate_mp3", "BattyBirdNET", 256000, "mp3", true},
		{"bat_high_rate_opus", "BattyBirdNET", 256000, "opus", true},
		{"bat_high_rate_aac", "BattyBirdNET", 256000, "aac", true},
		{"bat_high_rate_wav", "BattyBirdNET", 256000, "wav", false},
		{"bat_high_rate_flac", "BattyBirdNET", 256000, "flac", false},
		{"bat_low_rate_mp3", "BattyBirdNET", 48000, "mp3", false},
		{"bird_high_rate_mp3", "BirdNET", 192000, "mp3", false},
		{"bird_low_rate_wav", "BirdNET", 48000, "wav", false},
		{"unknown_model", "Unknown", 256000, "mp3", false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			assert.Equal(t, tt.expected, needsBatFormatFallback(tt.model, "", tt.rate, tt.format))
		})
	}
}

// TestReplaceExtension verifies the file extension replacement helper.
func TestReplaceExtension(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name     string
		path     string
		newExt   string
		expected string
	}{
		{"mp3_to_wav", "/audio/bird.mp3", ".wav", "/audio/bird.wav"},
		{"opus_to_wav", "/audio/bat.opus", ".wav", "/audio/bat.wav"},
		{"no_extension", "/audio/clip", ".wav", "/audio/clip.wav"},
		{"nested_path", "/data/2026/05/bat-256k.mp3", ".wav", "/data/2026/05/bat-256k.wav"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			assert.Equal(t, tt.expected, replaceExtension(tt.path, tt.newExt))
		})
	}
}
