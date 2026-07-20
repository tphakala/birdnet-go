package processor

import (
	"encoding/binary"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/tphakala/birdnet-go/internal/audiocore/ffmpeg"
	"github.com/tphakala/birdnet-go/internal/conf"
)

// requireFFmpegTools resolves the ffmpeg binary these tests encode and decode
// with, skipping when it is absent. Both are needed: encoding proves the export
// path runs, decoding is how the loudness is read back.
func requireFFmpegTools(t *testing.T) string {
	t.Helper()
	bin, err := exec.LookPath("ffmpeg")
	if err != nil {
		t.Skip("ffmpeg not on PATH:", err)
	}
	return bin
}

// decodeToPCM16 decodes an encoded clip back to the mono 16-bit PCM shape
// measureLUFS expects, at the rate it was written.
func decodeToPCM16(t *testing.T, ffmpegBin, path string) []byte {
	t.Helper()
	out, err := exec.CommandContext(t.Context(), ffmpegBin, //nolint:gosec // G204: test-controlled paths
		"-hide_banner", "-loglevel", "error",
		"-i", path,
		"-ac", "1",
		"-ar", strconv.Itoa(conf.SampleRate),
		"-f", "s16le",
		"pipe:1",
	).Output()
	require.NoError(t, err)
	require.NotEmpty(t, out)
	return out
}

// TestEncodeClip_FFmpegPathNormalizesToTarget is the end-to-end proof of this
// change's central claim: a clip encoded by FFmpeg now lands on the same loudness
// target as one encoded natively, with no loudnorm filter anywhere.
//
// Every other test around this stops at the ExportOptions struct, which pins the
// plumbing but not the result. Nothing in CI actually encoded an MP3 and measured
// it, so a wrong gain, a dropped -af, or a filter FFmpeg silently rejected would
// all have passed. MP3 is the case that matters most: it has no native encoder,
// so it is the FFmpeg path on every default install.
func TestEncodeClip_FFmpegPathNormalizesToTarget(t *testing.T) {
	t.Parallel()
	ffmpegBin := requireFFmpegTools(t)

	// A quiet 1 kHz sine, well clear of the R128 gate and far enough below the
	// target that the gain has to be substantial and neither the true-peak
	// ceiling nor the clamp binds.
	pcm := sinePCMBytes(800, 2.0, 1000)
	measured := measureLUFS(t, pcm)
	require.Less(t, measured, testTargetLUFS-5, "sanity: the clip needs a real boost")

	s := &conf.Settings{}
	s.Realtime.Audio.Export.Normalization.Enabled = true
	s.Realtime.Audio.Export.Normalization.TargetLUFS = testTargetLUFS
	s.Realtime.Audio.Export.Normalization.TruePeak = testTruePeakDBTP
	s.Realtime.Audio.Export.Bitrate = "192k"
	s.Realtime.Audio.FfmpegPath = ffmpegBin
	// A static gain that must NOT be applied, because normalization supersedes it.
	s.Realtime.Audio.Export.Gain = -12

	a := &SaveAudioAction{Settings: s, pcmData: pcm, CorrelationID: "e2e"}
	out := filepath.Join(t.TempDir(), "clip.mp3")

	enc, err := a.encodeClip(t.Context(), conf.SampleRate, ffmpeg.FormatMP3, out)
	require.NoError(t, err)
	require.Equal(t, encoderFFmpeg, enc, "MP3 has no native encoder; it must go through FFmpeg")

	got := measureLUFS(t, decodeToPCM16(t, ffmpegBin, out))

	// 1.5 LU covers MP3 codec loss and the resampling in the decode round trip.
	assert.InDelta(t, testTargetLUFS, got, 1.5,
		"an FFmpeg-encoded clip must reach the loudness target, same as a native one")
	// The static gain would have landed it ~12 dB below the input instead.
	assert.Greater(t, got, measured, "normalization must supersede the negative static gain")
}

// TestEncodeClip_WAVAppliesResolvedGain covers the WAV branch, which until this
// change wrote captured samples verbatim and so silently ignored both
// normalization and Export.Gain. That was easy to miss because resolveExportParams
// downgrades to WAV on its own for ultrasonic clips and encoder-less installs, so
// an operator could land here without choosing WAV.
func TestEncodeClip_WAVAppliesResolvedGain(t *testing.T) {
	t.Parallel()

	pcm := sinePCMBytes(800, 2.0, 1000)
	measured := measureLUFS(t, pcm)

	s := &conf.Settings{}
	s.Realtime.Audio.Export.Normalization.Enabled = true
	s.Realtime.Audio.Export.Normalization.TargetLUFS = testTargetLUFS
	s.Realtime.Audio.Export.Normalization.TruePeak = testTruePeakDBTP

	a := &SaveAudioAction{Settings: s, pcmData: pcm, CorrelationID: "wav"}
	out := filepath.Join(t.TempDir(), "clip.wav")

	enc, err := a.encodeClip(t.Context(), conf.SampleRate, ffmpeg.FormatWAV, out)
	require.NoError(t, err)
	require.Equal(t, encoderNativeWAV, enc)

	got := measureLUFS(t, readWAVSamples(t, out))
	assert.InDelta(t, testTargetLUFS, got, 0.5, "WAV must honour the resolved loudness gain")
	assert.Greater(t, got, measured, "the quiet clip must have been boosted")
}

// TestEncodeClip_WAVLeavesPCMUnmodified guards the aliasing hazard the WAV gain
// introduces: the gained samples must be a copy, because the spectrogram
// pre-render job reads a.pcmData after the export returns and would otherwise
// render a clip that had been amplified underneath it.
func TestEncodeClip_WAVLeavesPCMUnmodified(t *testing.T) {
	t.Parallel()

	pcm := sinePCMBytes(800, 1.0, 1000)
	before := make([]byte, len(pcm))
	copy(before, pcm)

	s := &conf.Settings{}
	s.Realtime.Audio.Export.Gain = 12

	a := &SaveAudioAction{Settings: s, pcmData: pcm, CorrelationID: "wav-alias"}
	out := filepath.Join(t.TempDir(), "clip.wav")

	_, err := a.encodeClip(t.Context(), conf.SampleRate, ffmpeg.FormatWAV, out)
	require.NoError(t, err)

	assert.Equal(t, before, a.pcmData, "the export must not amplify the shared capture buffer in place")
}

// readWAVSamples returns the PCM payload of a 16-bit mono WAV, skipping the
// header by walking the RIFF chunks rather than assuming a fixed 44-byte offset.
func readWAVSamples(t *testing.T, path string) []byte {
	t.Helper()
	data, err := os.ReadFile(path) //nolint:gosec // test-controlled temp path
	require.NoError(t, err)
	require.Greater(t, len(data), 12, "file is too small to be a WAV")
	require.Equal(t, "RIFF", string(data[0:4]))
	require.Equal(t, "WAVE", string(data[8:12]))

	for off := 12; off+8 <= len(data); {
		id := string(data[off : off+4])
		size := int(binary.LittleEndian.Uint32(data[off+4 : off+8]))
		body := off + 8
		if id == "data" {
			return data[body:min(body+size, len(data))]
		}
		off = body + size
		if size%2 == 1 {
			off++ // RIFF chunks are word-aligned
		}
	}
	t.Fatal("no data chunk found in WAV")
	return nil
}
