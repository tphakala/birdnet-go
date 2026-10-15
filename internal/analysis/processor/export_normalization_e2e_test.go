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

	"github.com/tphakala/birdnet-go/internal/audiocore/audioformat"
	"github.com/tphakala/birdnet-go/internal/audiocore/clipenc"
	"github.com/tphakala/birdnet-go/internal/conf"
)

// requireFFmpegTools resolves the ffmpeg binary these tests encode and decode
// with, skipping when it is absent. Both are needed: encoding proves the export
// path runs, decoding is how the loudness is read back.
func requireFFmpegTools(t *testing.T) string {
	t.Helper()
	bin, err := exec.LookPath(conf.GetFfmpegBinaryName())
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

// TestEncodeClip_NativeLossyNormalizesToTarget is the end-to-end proof that a
// clip encoded by a native lossy encoder lands on the loudness target.
//
// Every other test around this stops at the resolved gain, which pins the
// plumbing but not the result. Nothing else in CI actually encoded a clip and
// measured it, so a wrong gain or a dropped gain would pass. FFmpeg is used only
// to decode the clip back for measurement.
func TestEncodeClip_NativeLossyNormalizesToTarget(t *testing.T) {
	t.Parallel()
	ffmpegBin := requireFFmpegTools(t)

	for _, tc := range []struct {
		format  string
		ext     string
		encoder string
	}{
		{audioformat.AAC, "m4a", clipenc.NativeAAC},
		{audioformat.MP3, "mp3", clipenc.NativeMP3},
		{audioformat.Opus, "opus", clipenc.NativeOpus},
	} {
		t.Run(tc.format, func(t *testing.T) {
			t.Parallel()

			// A quiet 1 kHz sine, well clear of the R128 gate and far enough below
			// the target that the gain has to be substantial and neither the
			// true-peak ceiling nor the clamp binds.
			pcm := sinePCMBytes(800, 2.0, 1000)
			measured := measureLUFS(t, pcm)
			require.Less(t, measured, testTargetLUFS-5, "sanity: the clip needs a real boost")

			s := &conf.Settings{}
			s.Realtime.Audio.Export.Normalization.Enabled = true
			s.Realtime.Audio.Export.Normalization.TargetLUFS = testTargetLUFS
			s.Realtime.Audio.Export.Normalization.TruePeak = testTruePeakDBTP
			s.Realtime.Audio.Export.Bitrate = "192k"
			// A static gain that must NOT be applied, because normalization supersedes it.
			s.Realtime.Audio.Export.Gain = -12

			a := &SaveAudioAction{Settings: s, pcmData: pcm, CorrelationID: "e2e"}
			out := filepath.Join(t.TempDir(), "clip."+tc.ext)

			enc, err := a.encodeClip(t.Context(), conf.SampleRate, tc.format, out)
			require.NoError(t, err)
			require.Equal(t, tc.encoder, enc.Encoder)

			got := measureLUFS(t, decodeToPCM16(t, ffmpegBin, out))

			// 1.5 LU covers codec loss and the resampling in the decode round trip.
			assert.InDelta(t, testTargetLUFS, got, 1.5,
				"a natively encoded clip must reach the loudness target")
			// The static gain would have landed it ~12 dB below the input instead.
			assert.Greater(t, got, measured, "normalization must supersede the negative static gain")
		})
	}
}

// TestEncodeClip_WAVAppliesResolvedGain covers the WAV branch, which until this
// change wrote captured samples verbatim and so silently ignored both
// normalization and Export.Gain. That was easy to miss because an
// ultrasonic clip can be stored as WAV without the operator choosing WAV.
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

	enc, err := a.encodeClip(t.Context(), conf.SampleRate, audioformat.WAV, out)
	require.NoError(t, err)
	require.Equal(t, clipenc.NativeWAV, enc.Encoder)

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

	_, err := a.encodeClip(t.Context(), conf.SampleRate, audioformat.WAV, out)
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
