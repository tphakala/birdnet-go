package processor

import (
	"fmt"
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
	rate := wavSampleRate(t, outputPath)
	assert.Equal(t, conf.SampleRate, rate, "bird audio should be downsampled to 48kHz")
}

// TestSaveAudioAction_BatUltrasonicStoredAsFLAC verifies that a bat detection
// above 48kHz configured with a lossy Export.Type is stored losslessly in the
// dedicated ultrasonic format (FLAC by default) at its full source rate, replacing
// the previous forced-WAV downgrade.
func TestSaveAudioAction_BatUltrasonicStoredAsFLAC(t *testing.T) {
	t.Parallel()

	tmpDir := t.TempDir()
	settings := conftest.NewTestSettings().
		WithAudioExport(tmpDir, "mp3", "192k"). // WithAudioExport defaults UltrasonicType to flac
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
		CorrelationID:    "test-bat-ultrasonic-flac",
	}

	require.NoError(t, action.Execute(t.Context(), nil))

	// The configured (lossy) MP3 file must NOT be created.
	mp3Path := filepath.Join(tmpDir, "bat-256k.mp3")
	_, err := os.Stat(mp3Path)
	assert.True(t, os.IsNotExist(err), "MP3 file must not be created for a bat capture above 48kHz")

	// A FLAC file at the full source rate SHOULD exist.
	flacPath := filepath.Join(tmpDir, "bat-256k.flac")
	rate := flacSampleRate(t, flacPath)
	assert.Equal(t, sourceRate, rate, "ultrasonic FLAC should preserve the native 256kHz rate")
}

// TestSaveAudioAction_BatUltrasonicStoredAsWAV verifies that the ultrasonic export
// format is configurable: with UltrasonicType set to WAV, a bat detection above
// 48kHz is stored as WAV at its full source rate regardless of the lossy
// Export.Type.
func TestSaveAudioAction_BatUltrasonicStoredAsWAV(t *testing.T) {
	t.Parallel()

	tmpDir := t.TempDir()
	settings := conftest.NewTestSettings().
		WithAudioExport(tmpDir, "opus", "128k").
		WithUltrasonicExportType("wav").
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
		CorrelationID:    "test-bat-ultrasonic-wav",
	}

	require.NoError(t, action.Execute(t.Context(), nil))

	// The configured (lossy) Opus file must NOT be created.
	opusPath := filepath.Join(tmpDir, "bat-256k.opus")
	_, err := os.Stat(opusPath)
	assert.True(t, os.IsNotExist(err), "Opus file must not be created for a bat capture above 48kHz")

	// A WAV file at the full source rate SHOULD exist.
	wavPath := filepath.Join(tmpDir, "bat-256k.wav")
	rate := wavSampleRate(t, wavPath)
	assert.Equal(t, sourceRate, rate, "ultrasonic WAV should preserve the native 256kHz rate")
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
	rate := wavSampleRate(t, outputPath)
	assert.Equal(t, sourceRate, rate, "bird audio at 48kHz should not be resampled")
}

// TestResolveExportFormat verifies the single source of truth shared by the
// encoder and the DB clip-name path: a bat/ultrasonic capture above the analysis
// rate resolves to Export.UltrasonicType; every other case (including a non-bat
// capture above the analysis rate, which resolveExportParams downsamples) resolves
// to Export.Type.
func TestResolveExportFormat(t *testing.T) {
	t.Parallel()

	exports := []*conf.ExportSettings{
		{Type: "mp3", UltrasonicType: "flac"},
		{Type: "wav", UltrasonicType: "wav"},
		{Type: "opus", UltrasonicType: "flac"},
		{Type: "flac", UltrasonicType: "wav"},
	}
	rates := []int{8000, 16000, 22050, 32000, 44100, 48000, 96000, 192000, 256000, 384000}

	for _, export := range exports {
		for _, rate := range rates {
			name := fmt.Sprintf("type=%s_ultra=%s_%dHz", export.Type, export.UltrasonicType, rate)

			t.Run("bat_"+name, func(t *testing.T) {
				t.Parallel()
				want := export.Type
				if rate > conf.SampleRate {
					want = export.UltrasonicType
				}
				assert.Equal(t, want, resolveExportFormat(true, rate, export),
					"a bat capture above the analysis rate uses UltrasonicType, otherwise Export.Type")
			})

			t.Run("nonbat_"+name, func(t *testing.T) {
				t.Parallel()
				assert.Equal(t, export.Type, resolveExportFormat(false, rate, export),
					"a non-bat capture always uses Export.Type regardless of source rate")
			})
		}
	}
}

// TestResolveExportFormat_Boundary pins the analysis-rate boundary with hardcoded
// expectations, independent of the re-derived table above, so an off-by-one in the
// production `sourceRate > conf.SampleRate` comparison is caught.
func TestResolveExportFormat_Boundary(t *testing.T) {
	t.Parallel()

	export := &conf.ExportSettings{Type: "mp3", UltrasonicType: "flac"}

	assert.Equal(t, "mp3", resolveExportFormat(true, conf.SampleRate, export),
		"a bat capture at exactly the analysis rate uses Export.Type (not ultrasonic)")
	assert.Equal(t, "flac", resolveExportFormat(true, conf.SampleRate+1, export),
		"a bat capture just above the analysis rate uses UltrasonicType")
	assert.Equal(t, "mp3", resolveExportFormat(false, 384000, export),
		"a non-bat capture never uses UltrasonicType, whatever the rate")
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
