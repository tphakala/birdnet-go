package processor

import (
	"bytes"
	"context"
	"encoding/binary"
	"io"
	"math"
	"math/rand"
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	goflac "github.com/tphakala/go-flac/pcm"

	"github.com/tphakala/birdnet-go/internal/audiocore/audionorm"
	"github.com/tphakala/birdnet-go/internal/audiocore/ffmpeg"
	"github.com/tphakala/birdnet-go/internal/audiocore/flac"
	"github.com/tphakala/birdnet-go/internal/audiocore/pcmgain"
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

// burstPCMBytes builds a mono 16-bit LE PCM buffer of totalSeconds that is
// silent except for a burst of sine at the start. A short enough burst leaves
// every 400 ms R128 block below the -70 LUFS absolute gate while the true peak
// stays well above it, which is the sub-gate shape the gate fallback exists for
// and which a steady sine cannot produce.
func burstPCMBytes(amp int16, totalSeconds, burstSeconds, freqHz float64) []byte {
	buf := make([]byte, int(float64(conf.SampleRate)*totalSeconds)*2)
	burst := int(float64(conf.SampleRate) * burstSeconds)
	for i := range burst {
		v := float64(amp) * math.Sin(2*math.Pi*freqHz*float64(i)/float64(conf.SampleRate))
		//nolint:gosec // G115: rounded sine within int16 range, then LE bit-write
		binary.LittleEndian.PutUint16(buf[i*2:], uint16(int16(math.Round(v))))
	}
	return buf
}

// noisePCMBytes builds mono 16-bit LE uniform noise of the given peak amplitude.
// Unlike a sine or a short burst it has a LOW crest factor, which is the shape
// that exposes a true-peak-anchored lift overshooting the loudness target. The
// generator is seeded so the measurement is reproducible.
func noisePCMBytes(amp, seconds float64) []byte {
	n := int(float64(conf.SampleRate) * seconds)
	buf := make([]byte, n*2)
	r := rand.New(rand.NewSource(42)) //nolint:gosec // G404: deterministic test fixture, not security
	for i := range n {
		v := (r.Float64()*2 - 1) * amp
		//nolint:gosec // G115: bounded by amp, well inside int16 range
		binary.LittleEndian.PutUint16(buf[i*2:], uint16(int16(math.Round(v))))
	}
	return buf
}

func measureTruePeak(t *testing.T, pcm []byte) float64 {
	t.Helper()
	meas, err := audionorm.MeasureInt16Bytes(pcm, conf.SampleRate, conf.NumChannels)
	require.NoError(t, err)
	return meas.TruePeakDBTP
}

func measureLUFS(t *testing.T, pcm []byte) float64 {
	t.Helper()
	meas, err := audionorm.MeasureInt16Bytes(pcm, conf.SampleRate, conf.NumChannels)
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
	gainDB, err := a.planNativeNormalizationGain(t.Context(), conf.SampleRate, ffmpeg.FormatFLAC, testTargetLUFS, testTruePeakDBTP)
	require.NoError(t, err)

	assert.Positive(t, gainDB, "quiet clip must be boosted")
	assert.InDelta(t, testTargetLUFS, measured+gainDB, 0.1,
		"gain must bring the measured loudness onto the target (not peak-limited, not clamped)")
	assert.Less(t, gainDB, nativeExportMaxGainDB, "this clip must not hit the clamp")
}

// TestPlanNativeNormalizationGain_VeryQuietClipPassesOldClamp guards the ceiling
// raise from 30 dB to nativeExportMaxGainDB. A clip around -61 LUFS wants ~+38 dB,
// which the old 30 dB clamp truncated, leaving the export ~8 LU below target while
// the FFmpeg path (ceiling 60 dB) reached it. It must now reach target too.
func TestPlanNativeNormalizationGain_VeryQuietClipPassesOldClamp(t *testing.T) {
	t.Parallel()
	pcm := sinePCMBytes(40, 1.0, 1000) // ~-61 LUFS
	measured := measureLUFS(t, pcm)
	require.Less(t, measured, testTargetLUFS-30, "sanity: uncapped gain exceeds the old 30 dB clamp")
	require.Greater(t, measured, audionormMinTargetLUFS, "sanity: still above the absolute gate")

	a := &SaveAudioAction{pcmData: pcm, CorrelationID: "test"}
	gainDB, err := a.planNativeNormalizationGain(t.Context(), conf.SampleRate, ffmpeg.FormatFLAC, testTargetLUFS, testTruePeakDBTP)
	require.NoError(t, err)

	assert.Greater(t, gainDB, 30.0, "must no longer be truncated at the old 30 dB clamp")
	assert.InDelta(t, testTargetLUFS, measured+gainDB, 0.1, "must now land on the loudness target")
}

// TestPlanNativeNormalizationGain_SubGateClipIsLifted covers the defect this
// change exists to fix. A clip whose every R128 block sits below the -70 LUFS
// absolute gate measures -Inf, so the loudness plan asks for no gain and the clip
// used to be exported untouched and inaudible. With a finite true peak it must
// instead be lifted to the true-peak ceiling.
func TestPlanNativeNormalizationGain_SubGateClipIsLifted(t *testing.T) {
	t.Parallel()
	pcm := burstPCMBytes(45, 1.0, 0.01, 1000)
	require.True(t, math.IsInf(measureLUFS(t, pcm), -1), "sanity: clip must be under the R128 absolute gate")

	truePeak := measureTruePeak(t, pcm)
	require.False(t, math.IsInf(truePeak, -1), "sanity: clip must still have a finite true peak")

	a := &SaveAudioAction{pcmData: pcm, CorrelationID: "test"}
	gainDB, err := a.planNativeNormalizationGain(t.Context(), conf.SampleRate, ffmpeg.FormatFLAC, testTargetLUFS, testTruePeakDBTP)
	require.NoError(t, err)

	assert.Positive(t, gainDB, "a sub-gate clip with signal in it must not be exported untouched")
	assert.LessOrEqual(t, truePeak+gainDB, testTruePeakDBTP+0.1,
		"the lift must never push the true peak above the ceiling")

	// The conservative fallback alone would stop at the loudness bound, leaving
	// the clip merely audible. The refinement pass re-measures the lifted signal
	// and finishes the job, so the clip actually lands on target.
	assert.Greater(t, gainDB, testTargetLUFS-audionormMinTargetLUFS,
		"the refinement pass must carry the lift past the conservative bound")

	after, err := audionorm.MeasureInt16Bytes(pcmgain.Applied(pcm, gainDB), conf.SampleRate, conf.NumChannels)
	require.NoError(t, err)
	assert.InDelta(t, testTargetLUFS, after.IntegratedLUFS, 0.5,
		"a lifted sub-gate clip must end up on the loudness target")
}

// TestGateFallbackGainDB pins the fallback's contract directly, rather than only
// through planNativeNormalizationGain. That matters because the refinement pass
// downstream re-measures and corrects the result, so an integration-level test
// stays green even if a bound here is removed (mutation-verified). These bounds
// are the safety net for the case refinement cannot rescue: a clip still under
// the gate after the lift, or a failed re-measurement.
func TestGateFallbackGainDB(t *testing.T) {
	t.Parallel()

	const (
		target  = -23.0
		ceiling = -2.0
	)
	// The lift that brings a clip sitting exactly at the absolute gate to target.
	loudnessBound := target - audionormMinTargetLUFS

	tests := []struct {
		name     string
		meas     audionorm.Measurement
		wantOK   bool
		wantGain float64
		why      string
	}{
		{
			name:   "measurable loudness is not the fallback's business",
			meas:   audionorm.Measurement{IntegratedLUFS: -40, TruePeakDBTP: -30},
			wantOK: false,
			why:    "PlanGain already handles any clip R128 can measure",
		},
		{
			name:   "digital silence is left alone",
			meas:   audionorm.Measurement{IntegratedLUFS: math.Inf(-1), TruePeakDBTP: math.Inf(-1)},
			wantOK: false,
			why:    "there is no signal to lift, so amplifying would only invent noise",
		},
		{
			name:     "peaky sub-gate clip is bounded by the true-peak ceiling",
			meas:     audionorm.Measurement{IntegratedLUFS: math.Inf(-1), TruePeakDBTP: -10},
			wantOK:   true,
			wantGain: 8, // ceiling - truePeak; tighter than the 47 dB loudness bound
			why:      "lifting further would push the peak past the ceiling",
		},
		{
			name:     "flat sub-gate clip is bounded by the loudness target",
			meas:     audionorm.Measurement{IntegratedLUFS: math.Inf(-1), TruePeakDBTP: -80},
			wantOK:   true,
			wantGain: loudnessBound, // 78 dB of peak headroom, but only 47 is safe
			why:      "the true-peak anchor alone would drive a flat signal past the target",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			gain, ok := gateFallbackGainDB(tt.meas, target, ceiling)
			require.Equal(t, tt.wantOK, ok, tt.why)
			if tt.wantOK {
				assert.InDelta(t, tt.wantGain, gain, 1e-9, tt.why)
			} else {
				assert.Zero(t, gain, "a fallback that does not apply must plan no gain")
			}
		})
	}
}

// TestPlanNativeNormalizationGain_SubGateFlatSignalDoesNotOvershoot is the
// regression guard for a defect found by measurement, not by reading: anchoring
// a sub-gate lift purely to true-peak headroom leaves the output loudness at
// (ceiling - crest factor), so a FLAT signal is driven far past the target. A
// steady low-level noise floor (a dead or muted microphone) has a crest factor
// of only a few dB and came out at -13.8 LUFS against a -23 target, i.e. a user
// upgrading would get loud hiss where they previously got a silent clip.
//
// The loudness bound in gateFallbackGainDB exists solely to prevent this, so
// this test asserts the property that bound guarantees.
func TestPlanNativeNormalizationGain_SubGateFlatSignalDoesNotOvershoot(t *testing.T) {
	t.Parallel()

	// Steady uniform noise: sub-gate integrated loudness, low crest factor.
	pcm := noisePCMBytes(8, 1.0)
	meas, err := audionorm.MeasureInt16Bytes(pcm, conf.SampleRate, conf.NumChannels)
	require.NoError(t, err)
	require.True(t, math.IsInf(meas.IntegratedLUFS, -1), "sanity: clip must be under the R128 absolute gate")
	require.False(t, math.IsInf(meas.TruePeakDBTP, -1), "sanity: clip must still have a finite true peak")

	a := &SaveAudioAction{pcmData: pcm, CorrelationID: "test"}
	gainDB, err := a.planNativeNormalizationGain(t.Context(), conf.SampleRate, ffmpeg.FormatFLAC, testTargetLUFS, testTruePeakDBTP)
	require.NoError(t, err)
	assert.Positive(t, gainDB, "the clip must still be lifted out of inaudibility")

	// Measure what the encoder would actually write, rather than trusting the
	// planned gain: the whole defect was a mismatch between the two.
	after, err := audionorm.MeasureInt16Bytes(pcmgain.Applied(pcm, gainDB), conf.SampleRate, conf.NumChannels)
	require.NoError(t, err)

	// The property that matters: never LOUDER than the target. The small
	// tolerance absorbs the measurement round trip, and is far tighter than the
	// ~10 LU overshoot this test exists to catch.
	assert.LessOrEqual(t, after.IntegratedLUFS, testTargetLUFS+0.5,
		"a flat sub-gate clip must never be lifted past the loudness target")
	assert.InDelta(t, testTargetLUFS, after.IntegratedLUFS, 0.5,
		"and it should still reach the target, not stop short of it")
}

// TestPlanNativeNormalizationGain_Silence keeps silent input unchanged (gain 0)
// rather than boosting the noise floor, matching the BirdWeather native path.
func TestPlanNativeNormalizationGain_Silence(t *testing.T) {
	t.Parallel()
	pcm := make([]byte, conf.SampleRate*2) // 1 s of digital silence
	a := &SaveAudioAction{pcmData: pcm, CorrelationID: "test"}
	gainDB, err := a.planNativeNormalizationGain(t.Context(), conf.SampleRate, ffmpeg.FormatFLAC, testTargetLUFS, testTruePeakDBTP)
	require.NoError(t, err)
	assert.Zero(t, gainDB, "silence must not be amplified")
}

// TestPlanNativeNormalizationGain_ClampsExtremeBoost bounds the gain at
// nativeExportMaxGainDB.
//
// Reaching that backstop takes the most aggressive target the config allows.
// nativeExportMaxGainDB is conf.MaxTargetLUFS (-10) minus the R128 absolute gate
// (-70), so at any lower target both the loudness bound and the measurable-clip
// path stay strictly under it and the ceiling never binds. This uses -10 so the
// backstop is genuinely exercised rather than asserted against dead code.
func TestPlanNativeNormalizationGain_ClampsExtremeBoost(t *testing.T) {
	t.Parallel()
	// A very short, very low burst in silence: sub-gate loudness and a true peak
	// around -72 dBFS, so both fallback bounds exceed the ceiling.
	const aggressiveTargetLUFS = -10.0 // conf.MaxTargetLUFS
	pcm := burstPCMBytes(8, 1.0, 0.01, 1000)
	require.True(t, math.IsInf(measureLUFS(t, pcm), -1), "sanity: clip must be under the R128 absolute gate")
	truePeak := measureTruePeak(t, pcm)
	require.False(t, math.IsInf(truePeak, -1), "sanity: true peak is finite")
	require.Greater(t, testTruePeakDBTP-truePeak, nativeExportMaxGainDB,
		"sanity: the true-peak-anchored gain must exceed the ceiling")
	require.Greater(t, aggressiveTargetLUFS-audionormMinTargetLUFS, nativeExportMaxGainDB-1e-9,
		"sanity: the loudness bound must also reach the ceiling at this target")

	a := &SaveAudioAction{pcmData: pcm, CorrelationID: "test"}
	gainDB, err := a.planNativeNormalizationGain(t.Context(), conf.SampleRate, ffmpeg.FormatFLAC, aggressiveTargetLUFS, testTruePeakDBTP)
	require.NoError(t, err)
	assert.InDelta(t, nativeExportMaxGainDB, gainDB, 1e-9, "extreme boost must clamp to +nativeExportMaxGainDB")
}

// TestPlanNativeNormalizationGain_Attenuates reduces a loud clip toward the target
// with a negative gain.
func TestPlanNativeNormalizationGain_Attenuates(t *testing.T) {
	t.Parallel()
	pcm := sinePCMBytes(20000, 1.0, 1000) // loud, ~-7 LUFS
	measured := measureLUFS(t, pcm)
	require.Greater(t, measured, testTargetLUFS, "sanity: clip is louder than target")

	a := &SaveAudioAction{pcmData: pcm, CorrelationID: "test"}
	gainDB, err := a.planNativeNormalizationGain(t.Context(), conf.SampleRate, ffmpeg.FormatFLAC, testTargetLUFS, testTruePeakDBTP)
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
	_, err := a.planNativeNormalizationGain(ctx, conf.SampleRate, ffmpeg.FormatFLAC, testTargetLUFS, testTruePeakDBTP)
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

	gainDB, err := a.planNativeNormalizationGain(t.Context(), conf.SampleRate, ffmpeg.FormatFLAC, testTargetLUFS, testTruePeakDBTP)
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

// The []byte->[]int16 signed-cast and odd-trailing-byte regression guards now
// live with the shared decoder in internal/audiocore/audionorm (MeasureInt16Bytes
// / Meter.AddInt16Bytes), which this path calls instead of a local helper.

// TestEncodeClipSelectsNativeNormalization drives the real encodeClip switch with
// normalization enabled, proving FLAC routes to the native audionorm path (returns
// encoderNativeFLAC, not FFmpeg) AND that the static Export.Gain is NOT applied on
// top of the loudness gain (FFmpeg gives normalization precedence over gain, and
// the native path must match).
func TestEncodeClipSelectsNativeNormalization(t *testing.T) {
	t.Parallel()

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
	assert.Equal(t, encoderNativeFLAC, enc.Encoder, "must select the native FLAC encoder, not FFmpeg")

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

// TestEncodeClipFLACWithoutNormalization proves the default FLAC path: with
// normalization DISABLED, encodeClip still routes FLAC to the native go-flac
// encoder (no env gate, no FFmpeg) and applies the static Export.Gain verbatim.
// This is the branch the native-encoder promotion newly routes through go-flac for
// every user who has not enabled normalization, so it is the highest-value guard.
func TestEncodeClipFLACWithoutNormalization(t *testing.T) {
	t.Parallel()

	const staticGainDB = 6.0
	s := &conf.Settings{}
	s.Realtime.Audio.Export.Normalization.Enabled = false
	s.Realtime.Audio.Export.Gain = staticGainDB

	pcm := sinePCMBytes(800, 1.0, 1000)
	inputLUFS := measureLUFS(t, pcm)

	a := &SaveAudioAction{Settings: s, pcmData: pcm, CorrelationID: "test"}
	out := filepath.Join(t.TempDir(), "clip.flac")
	enc, err := a.encodeClip(t.Context(), conf.SampleRate, ffmpeg.FormatFLAC, out)
	require.NoError(t, err)
	assert.Equal(t, encoderNativeFLAC, enc.Encoder, "FLAC must encode natively with no env gate and no FFmpeg")

	data, err := os.ReadFile(out) //nolint:gosec // test-controlled temp path
	require.NoError(t, err)
	dec, err := goflac.NewDecoder(bytes.NewReader(data))
	require.NoError(t, err)
	decoded, err := io.ReadAll(dec)
	require.NoError(t, err)

	// Normalization is off, so the static +6 dB Export.Gain is applied verbatim with
	// no loudness targeting: the decoded clip sits ~6 dB above the input loudness.
	got := measureLUFS(t, decoded)
	assert.InDelta(t, inputLUFS+staticGainDB, got, 1.0,
		"static Export.Gain must be applied on the no-normalization FLAC path")
}

// TestEncodeClipFLACNormalizationSkipped drives the defensive branch inside the
// FLAC case: normalization is enabled but the targets are outside audionorm's
// range (unreachable for a validated config, which clamps them into range). With
// FFmpeg FLAC removed there is no loudnorm fallback, so encodeClip must still
// encode natively, skip normalization with a WARN, and apply the static Export.Gain.
func TestEncodeClipFLACNormalizationSkipped(t *testing.T) {
	t.Parallel()

	const staticGainDB = 6.0
	s := &conf.Settings{}
	s.Realtime.Audio.Export.Normalization.Enabled = true
	s.Realtime.Audio.Export.Normalization.TargetLUFS = testTargetLUFS
	// TruePeak > 0 is outside audionorm's supported range, so audionormSupportsTargets
	// returns false and encodeClip takes the skip-normalization branch.
	s.Realtime.Audio.Export.Normalization.TruePeak = 5.0
	s.Realtime.Audio.Export.Gain = staticGainDB

	pcm := sinePCMBytes(800, 1.0, 1000)
	inputLUFS := measureLUFS(t, pcm)

	a := &SaveAudioAction{Settings: s, pcmData: pcm, CorrelationID: "test"}
	out := filepath.Join(t.TempDir(), "clip.flac")
	enc, err := a.encodeClip(t.Context(), conf.SampleRate, ffmpeg.FormatFLAC, out)
	require.NoError(t, err)
	assert.Equal(t, encoderNativeFLAC, enc.Encoder, "out-of-range normalization must still encode natively, never FFmpeg")

	data, err := os.ReadFile(out) //nolint:gosec // test-controlled temp path
	require.NoError(t, err)
	dec, err := goflac.NewDecoder(bytes.NewReader(data))
	require.NoError(t, err)
	decoded, err := io.ReadAll(dec)
	require.NoError(t, err)

	// Normalization was skipped, so only the static +6 dB Export.Gain is applied:
	// the decoded clip sits ~6 dB above the input, NOT on the loudness target.
	got := measureLUFS(t, decoded)
	assert.InDelta(t, inputLUFS+staticGainDB, got, 1.0,
		"the skip-normalization branch must apply the static Export.Gain, not loudness targeting")
}
