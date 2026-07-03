package processor

import (
	"bytes"
	"context"
	"encoding/binary"
	"io"
	"math"
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	goflac "github.com/tphakala/go-flac/pcm"

	"github.com/tphakala/birdnet-go/internal/audiocore/audionorm"
	"github.com/tphakala/birdnet-go/internal/audiocore/ffmpeg"
	"github.com/tphakala/birdnet-go/internal/audiocore/flac"
	"github.com/tphakala/birdnet-go/internal/conf"
)

// sinePCMBytes builds a mono 16-bit LE PCM sine of the given peak amplitude,
// duration, and frequency at conf.SampleRate. Duration must be >= 400 ms for the
// EBU R128 integrated-loudness gate to yield a finite measurement.
func sinePCMBytes(amp int16, seconds, freqHz float64) []byte {
	n := int(float64(conf.SampleRate) * seconds)
	buf := make([]byte, n*2)
	for i := range n {
		v := float64(amp) * math.Sin(2*math.Pi*freqHz*float64(i)/float64(conf.SampleRate))
		//nolint:gosec // G115: rounded sine within int16 range, then LE bit-write
		binary.LittleEndian.PutUint16(buf[i*2:], uint16(int16(math.Round(v))))
	}
	return buf
}

func measureLUFS(t *testing.T, pcm []byte) float64 {
	t.Helper()
	meas, err := audionorm.MeasureInt16(pcmBytesToInt16(pcm), conf.SampleRate, conf.NumChannels)
	require.NoError(t, err)
	return meas.IntegratedLUFS
}

const (
	testTargetLUFS   = -23.0
	testTruePeakDBTP = -2.0
)

// TestPlanNativeNormalizationGain_QuietClipReachesTarget verifies a quiet clip is
// boosted by exactly (target - measured) when neither the true-peak ceiling nor
// the +/-30 dB clamp binds, so applying the gain lands the clip on target.
func TestPlanNativeNormalizationGain_QuietClipReachesTarget(t *testing.T) {
	t.Parallel()
	// ~-35 LUFS: wants ~+12 dB toward -23, well under the ceiling and the clamp.
	pcm := sinePCMBytes(800, 1.0, 1000)
	measured := measureLUFS(t, pcm)
	require.Greater(t, measured, -50.0, "sanity: clip is quiet but well above the gate")
	require.Less(t, measured, testTargetLUFS-5, "sanity: clip needs a real boost")

	a := &SaveAudioAction{pcmData: pcm, CorrelationID: "test"}
	gainDB, err := a.planNativeNormalizationGain(t.Context(), conf.SampleRate, testTargetLUFS, testTruePeakDBTP)
	require.NoError(t, err)

	assert.Positive(t, gainDB, "quiet clip must be boosted")
	assert.InDelta(t, testTargetLUFS, measured+gainDB, 0.1,
		"gain must bring the measured loudness onto the target (not peak-limited, not clamped)")
	assert.Less(t, gainDB, maxNativeNormalizationGainDB, "this clip must not hit the clamp")
}

// TestPlanNativeNormalizationGain_Silence keeps silent input unchanged (gain 0)
// rather than boosting the noise floor, matching the BirdWeather native path.
func TestPlanNativeNormalizationGain_Silence(t *testing.T) {
	t.Parallel()
	pcm := make([]byte, conf.SampleRate*2) // 1 s of digital silence
	a := &SaveAudioAction{pcmData: pcm, CorrelationID: "test"}
	gainDB, err := a.planNativeNormalizationGain(t.Context(), conf.SampleRate, testTargetLUFS, testTruePeakDBTP)
	require.NoError(t, err)
	assert.Zero(t, gainDB, "silence must not be amplified")
}

// TestPlanNativeNormalizationGain_ClampsExtremeBoost bounds a near-silent (but
// above the gate) clip to +maxNativeNormalizationGainDB instead of the full
// target-driven gain.
func TestPlanNativeNormalizationGain_ClampsExtremeBoost(t *testing.T) {
	t.Parallel()
	// ~-61 LUFS with a low peak: wants ~+38 dB, above the +30 clamp and below the
	// true-peak ceiling, so the clamp (not the ceiling) binds.
	pcm := sinePCMBytes(40, 1.0, 1000)
	measured := measureLUFS(t, pcm)
	require.Less(t, measured, testTargetLUFS-maxNativeNormalizationGainDB,
		"sanity: uncapped gain would exceed the clamp")
	require.Greater(t, measured, audionormMinTargetLUFS+3, "sanity: still above the absolute gate")

	a := &SaveAudioAction{pcmData: pcm, CorrelationID: "test"}
	gainDB, err := a.planNativeNormalizationGain(t.Context(), conf.SampleRate, testTargetLUFS, testTruePeakDBTP)
	require.NoError(t, err)
	assert.InDelta(t, maxNativeNormalizationGainDB, gainDB, 1e-9, "extreme boost must clamp to +30 dB")
}

// TestPlanNativeNormalizationGain_Attenuates reduces a loud clip toward the target
// with a negative gain.
func TestPlanNativeNormalizationGain_Attenuates(t *testing.T) {
	t.Parallel()
	pcm := sinePCMBytes(20000, 1.0, 1000) // loud, ~-7 LUFS
	measured := measureLUFS(t, pcm)
	require.Greater(t, measured, testTargetLUFS, "sanity: clip is louder than target")

	a := &SaveAudioAction{pcmData: pcm, CorrelationID: "test"}
	gainDB, err := a.planNativeNormalizationGain(t.Context(), conf.SampleRate, testTargetLUFS, testTruePeakDBTP)
	require.NoError(t, err)
	assert.Negative(t, gainDB, "loud clip must be attenuated")
	assert.InDelta(t, testTargetLUFS, measured+gainDB, 0.1, "attenuation must land on target")
}

// TestPlanNativeNormalizationGain_ContextCancelled returns the context error
// before doing any measurement work.
func TestPlanNativeNormalizationGain_ContextCancelled(t *testing.T) {
	t.Parallel()
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	a := &SaveAudioAction{pcmData: sinePCMBytes(800, 1.0, 1000), CorrelationID: "test"}
	_, err := a.planNativeNormalizationGain(ctx, conf.SampleRate, testTargetLUFS, testTruePeakDBTP)
	require.ErrorIs(t, err, context.Canceled)
}

// TestNativeNormalizationEndToEnd encodes a quiet clip through the real
// plan-gain + flac.EncodePCM path, decodes the FLAC, and confirms the decoded
// audio sits on the loudness target. This exercises the gain application done by
// the encoder, not just the planning.
func TestNativeNormalizationEndToEnd(t *testing.T) {
	t.Parallel()
	pcm := sinePCMBytes(800, 1.0, 1000)
	a := &SaveAudioAction{pcmData: pcm, CorrelationID: "test"}

	gainDB, err := a.planNativeNormalizationGain(t.Context(), conf.SampleRate, testTargetLUFS, testTruePeakDBTP)
	require.NoError(t, err)

	out := filepath.Join(t.TempDir(), "clip.flac")
	require.NoError(t, flac.EncodePCM(t.Context(), &flac.Options{
		PCMData:    pcm,
		OutputPath: out,
		SampleRate: conf.SampleRate,
		Channels:   conf.NumChannels,
		BitDepth:   conf.BitDepth,
		GainDB:     gainDB,
	}))

	data, err := os.ReadFile(out) //nolint:gosec // test-controlled temp path
	require.NoError(t, err)
	dec, err := goflac.NewDecoder(bytes.NewReader(data))
	require.NoError(t, err)
	decoded, err := io.ReadAll(dec)
	require.NoError(t, err)

	got := measureLUFS(t, decoded)
	assert.InDelta(t, testTargetLUFS, got, 1.0, "decoded clip loudness must sit near the target")
}

func TestAudionormSupportsTargets(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name     string
		target   float64
		truePeak float64
		want     bool
	}{
		{"typical -23/-2", -23, -2, true},
		{"boundary near 0", -0.1, -1, true},
		{"target zero rejected", 0, -1, false},
		{"target positive rejected", 5, -1, false},
		{"target at absolute gate rejected", -70, -1, false},
		{"target below gate rejected", -75, -1, false},
		{"positive true peak rejected", -23, 1, false},
		{"zero true peak allowed", -23, 0, true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			assert.Equal(t, tt.want, audionormSupportsTargets(tt.target, tt.truePeak))
		})
	}
}

func TestPcmBytesToInt16(t *testing.T) {
	t.Parallel()
	// Round-trips signed values including negatives via LE bit layout.
	in := []int16{0, 1, -1, 32767, -32768, 1234, -1234}
	raw := make([]byte, len(in)*2)
	for i, s := range in {
		//nolint:gosec // G115: signed->LE bit-write for test fixture
		binary.LittleEndian.PutUint16(raw[i*2:], uint16(s))
	}
	assert.Equal(t, in, pcmBytesToInt16(raw))

	// A trailing odd byte is ignored (not produced by real int16 PCM).
	assert.Len(t, pcmBytesToInt16([]byte{1, 2, 3}), 1)
	assert.Empty(t, pcmBytesToInt16(nil))
}

// TestEncodeClipSelectsNativeNormalization drives the real encodeClip switch with
// the native gate on and normalization enabled, proving it routes to the native
// audionorm path (returns encoderNativeFLAC, not FFmpeg) AND that the static
// Export.Gain is NOT applied on top of the loudness gain (FFmpeg gives
// normalization precedence over gain, and the native path must match). Not
// parallel: it toggles the process-global native-encoder gate override.
func TestEncodeClipSelectsNativeNormalization(t *testing.T) {
	t.Cleanup(flac.SetNativeEncoderEnabledForTest(true))

	s := &conf.Settings{}
	s.Realtime.Audio.Export.Normalization.Enabled = true
	s.Realtime.Audio.Export.Normalization.TargetLUFS = testTargetLUFS
	s.Realtime.Audio.Export.Normalization.TruePeak = testTruePeakDBTP
	s.Realtime.Audio.Export.Gain = 12.0 // must be ignored while normalizing

	pcm := sinePCMBytes(800, 1.0, 1000) // ~-35 LUFS, wants ~+12 dB toward target
	a := &SaveAudioAction{Settings: s, pcmData: pcm, CorrelationID: "test"}

	out := filepath.Join(t.TempDir(), "clip.flac")
	enc, err := a.encodeClip(t.Context(), conf.SampleRate, ffmpeg.FormatFLAC, out)
	require.NoError(t, err)
	assert.Equal(t, encoderNativeFLAC, enc, "must select the native FLAC encoder, not FFmpeg")

	data, err := os.ReadFile(out) //nolint:gosec // test-controlled temp path
	require.NoError(t, err)
	dec, err := goflac.NewDecoder(bytes.NewReader(data))
	require.NoError(t, err)
	decoded, err := io.ReadAll(dec)
	require.NoError(t, err)

	got := measureLUFS(t, decoded)
	assert.InDelta(t, testTargetLUFS, got, 1.0,
		"decoded clip must sit on the target; the +12 dB Export.Gain must NOT be added")
}
