//go:build normcompare

package normbench

import (
	"context"
	"fmt"
	"math"
	"os"
	"path/filepath"
	"strconv"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/tphakala/birdnet-go/internal/audiocore/audionorm"
	"github.com/tphakala/birdnet-go/internal/audiocore/convert"
	"github.com/tphakala/birdnet-go/internal/audiocore/pcmgain"
)

// The BirdNET-Go export defaults (conf.AudioSettings.Export.Normalization).
const (
	targetLUFS     = -23.0
	targetTruePeak = -2.0
	targetLRA      = 7.0

	// productionMaxGainDB is the shipped clamp. preFixMaxGainDB is what it was
	// before this work (audionorm.DefaultMaxGainDB), kept so the change stays
	// measurable.
	productionMaxGainDB = audionorm.MaxNormalizeGainDB
	preFixMaxGainDB     = audionorm.DefaultMaxGainDB
)

// loudness is a parsed measurement of one audio file.
type loudness struct {
	integrated float64 // LUFS
	truePeak   float64 // dBTP
	rangeLU    float64 // LU
}

// subGateLoudness reports whether R128 gating left the clip with no integrated
// loudness at all, which audionorm signals as -Inf. The native path answers this
// case with audionorm.GateFallbackGainDB (planNative below); before that landed
// it planned no gain at all and exported the clip inaudible, which is why the
// pre-fix column still shows it.
func subGateLoudness(meas audionorm.Measurement) bool {
	return math.IsInf(meas.IntegratedLUFS, -1)
}

// bound names what stopped a planned gain from reaching the loudness target.
// Reading this per row is the whole point of the harness: a divergence caused by
// the gain ceiling is a policy choice, one caused by R128 gating is a defect.
// The ceiling differs per column (30 dB pre-fix, 60 dB as shipped), so the label
// deliberately does not name a number.
type bound string

const (
	boundNone    bound = "-"        // reached the target
	boundPeak    bound = "peak"     // true-peak ceiling reduced the gain
	boundClamp   bound = "clamp"    // the gain ceiling reduced the gain
	boundSubGate bound = "sub-gate" // integrated loudness is -Inf, so no gain was planned
	boundSilent  bound = "silent"   // no signal at all
)

// plan is one implementation's gain decision for a clip.
type plan struct {
	wantDB     float64 // gain needed to hit the target, before any limiting
	headroomDB float64 // gain the true-peak ceiling allows
	gainDB     float64 // gain actually applied
	bound      bound
}

// outcome is one case run through every implementation under comparison.
type outcome struct {
	tc    testCase
	input loudness

	preFixPlan plan
	preFix     loudness // the native path before the sub-gate fix and ceiling raise

	nativePlan plan
	native     loudness // the native path as it now ships

	ffm loudness // the FFmpeg loudnorm export path
}

// TestCompareNormalization runs every corpus case through the native Go
// normalization as it stood before the sub-gate fix and ceiling raise, through
// the native path as it now ships, and through the FFmpeg loudnorm export path.
// All three outputs are measured with the same analyser so the numbers are
// directly comparable.
//
// It is a report, not a gate: it asserts only that each path produces a
// measurable file. Read the table it prints.
func TestCompareNormalization(t *testing.T) {
	ffmpegBin := ffmpegPath(t)
	cases := loadCorpus(t, ffmpegBin)
	dir := t.TempDir()
	ctx := t.Context()

	outcomes := make([]outcome, 0, len(cases))
	for i, tc := range cases {
		stem := filepath.Join(dir, "case"+itoa(i))
		o := outcome{tc: tc}

		o.input = measure(t, ctx, ffmpegBin, writeWAV(t, stem+"-in.wav", tc.pcm))

		meas := measureNative(t, tc.pcm)
		o.preFixPlan = planPreFix(meas)
		o.nativePlan = planNative(tc.pcm)

		o.preFix = measure(t, ctx, ffmpegBin,
			writeWAV(t, stem+"-prefix.wav", pcmgain.Applied(tc.pcm, o.preFixPlan.gainDB)))
		o.native = measure(t, ctx, ffmpegBin,
			writeWAV(t, stem+"-native.wav", pcmgain.Applied(tc.pcm, o.nativePlan.gainDB)))
		o.ffm = runFFmpeg(t, ctx, ffmpegBin, stem)

		outcomes = append(outcomes, o)
	}

	report(t, outcomes)
}

// measureNative runs audionorm's pass one, the same measurement the production
// native path performs inside PlanClampedGainInt16Bytes.
func measureNative(t *testing.T, pcm []byte) audionorm.Measurement {
	t.Helper()
	meas, err := audionorm.MeasureInt16Bytes(pcm, sampleRate, channels)
	require.NoError(t, err)
	return meas
}

// planPreFix reproduces the native decision as it stood before this work: plan
// target-minus-measured, reduce it to the true-peak headroom, clamp to +/-30 dB,
// and give a clip whose gated integrated loudness is -Inf no gain at all, so it
// is exported untouched. Kept as the baseline the fix is measured against.
func planPreFix(meas audionorm.Measurement) plan {
	res := audionorm.PlanGain(meas, audionorm.Options{
		SampleRate:   sampleRate,
		Channels:     channels,
		TargetLUFS:   targetLUFS,
		TruePeakDBTP: targetTruePeak,
	})
	gain, clamped := audionorm.ClampGainDB(res.GainDB, preFixMaxGainDB)

	p := plan{
		wantDB:     res.TargetGainDB,
		headroomDB: targetTruePeak - meas.TruePeakDBTP,
		gainDB:     gain,
	}
	switch {
	case subGateLoudness(meas):
		p.bound = boundSubGate
		if math.IsInf(meas.TruePeakDBTP, -1) {
			p.bound = boundSilent
		}
	case clamped:
		p.bound = boundClamp
	case res.PeakLimited:
		p.bound = boundPeak
	default:
		p.bound = boundNone
	}
	return p
}

// nativeMaxGainDB is the gain ceiling the native path applies. It defaults to
// the production value and is overridable because the ceiling turned out to be
// the dominant cause of divergence from FFmpeg, so the harness has to be able to
// price a different policy without editing production constants.
func nativeMaxGainDB() float64 {
	if v := os.Getenv("BIRDNET_NORMCOMPARE_MAXGAIN"); v != "" {
		if f, err := strconv.ParseFloat(v, 64); err == nil {
			return f
		}
	}
	return productionMaxGainDB
}

// planNative plans the gain the way the export path does, through
// audionorm.PlanGainWithGateFallbackInt16Bytes, and classifies what bounded it.
// For a clip lifted by the gate fallback the pre-clamp gain is not exposed, so
// wantDB reports the applied gain.
func planNative(pcm []byte) plan {
	gp, err := audionorm.PlanGainWithGateFallbackInt16Bytes(pcm, audionorm.Options{
		SampleRate:   sampleRate,
		Channels:     channels,
		TargetLUFS:   targetLUFS,
		TruePeakDBTP: targetTruePeak,
	}, nativeMaxGainDB())
	if err != nil {
		panic(err) // the harness's own fixtures are always measurable
	}

	p := plan{
		wantDB:     gp.Result.TargetGainDB,
		headroomDB: targetTruePeak - gp.Measurement.TruePeakDBTP,
		gainDB:     gp.GainDB,
		bound:      boundNone,
	}
	switch {
	case gp.GateLifted:
		p.wantDB = gp.GainDB
	case subGateLoudness(gp.Measurement):
		p.bound = boundSilent
	case gp.Clamped:
		p.bound = boundClamp
	case gp.Result.PeakLimited:
		p.bound = boundPeak
	}
	if gp.GateLifted && gp.Clamped {
		p.bound = boundClamp
	}
	return p
}

// runFFmpeg drives FFmpeg with normalization on, reproducing the filter
// construction (two-pass linear loudnorm, the gate fallback, and the single-pass
// fallback) that production no longer contains. The reference chain is built by
// loudnormFilter below and maintained in this package. See its comment for why,
// and TestLoudnormFilter for what pins it.
//
// The output format is FLAC rather than WAV for two reasons: it is lossless, so
// what is measured is the filter's output and not codec loss; and WAV is not
// actually reachable through this path in production (it maps to "-c:a wav",
// which is not an FFmpeg encoder) because WAV export is always native.
func runFFmpeg(t *testing.T, ctx context.Context, ffmpegBin, stem string) loudness {
	t.Helper()

	// The caller already wrote this exact PCM to stem+"-in.wav" to measure the
	// input; reuse it rather than writing a byte-identical second copy.
	out := stem + "-ffmpeg.flac"
	in := stem + "-in.wav"

	args := []string{
		"-hide_banner",
		"-loglevel", "error",
		"-i", in,
		"-af", loudnormFilter(ctx, t, ffmpegBin, in),
		// loudnorm upsamples internally to 192 kHz for true-peak detection, so
		// the output rate has to be pinned back or the FLAC inflates. Production
		// carried this same re-pin until loudnorm was removed from it.
		"-ar", strconv.Itoa(sampleRate),
		"-c:a", "flac",
		"-y", out,
	}
	runFFmpegCmd(t, ctx, ffmpegBin, args)

	return measure(t, ctx, ffmpegBin, out)
}

// loudnormFilter builds the two-pass linear loudnorm filter this harness
// compares the native path against.
//
// It lives here because production no longer normalises with FFmpeg at all: the export path plans its gain in Go via
// audionorm. What FFmpeg's loudnorm would have done is now purely a reference
// point, and a reference point belongs in the harness that reads it. This is a
// faithful copy of what the export path built before the removal, including the
// fallbacks: single-pass when analysis yields nothing usable, and a true-peak
// anchored offset when R128 gating leaves no measurable integrated loudness.
func loudnormFilter(ctx context.Context, t *testing.T, ffmpegBin, wavPath string) string {
	t.Helper()

	stats, err := analyzeLoudness(ctx, ffmpegBin, wavPath)
	// A cancelled or timed-out analysis is not a gated clip. Folding it into the
	// single-pass fallback would silently downgrade the reference column and the
	// table would report the result as a real before/after comparison, which is
	// the one failure mode that would quietly invalidate every number this
	// harness exists to produce. The production code this copies propagated the
	// context error for the same reason.
	require.NoError(t, ctx.Err(), "loudness analysis cancelled for %s", wavPath)
	if err != nil {
		// Not cancellation, so the analysis genuinely failed (malformed ffmpeg
		// output, unreadable temp file). The single-pass fallback below is the
		// right behaviour, but say so: an unannounced fallback is indistinguishable
		// in the table from a real gated clip, which is the same class of silent
		// misclassification the guard above exists to prevent.
		t.Logf("loudness analysis failed for %s, falling back to single-pass: %v", wavPath, err)
		return loudnormFilterFromStats(nil)
	}
	return loudnormFilterFromStats(stats)
}

// loudnormSinglePass is the analysis-free filter the old export path fell back to
// whenever it had no usable measurement.
func loudnormSinglePass() string {
	return fmt.Sprintf("loudnorm=I=%.1f:TP=%.1f:LRA=%.1f", targetLUFS, targetTruePeak, targetLRA)
}

// loudnormFilterFromStats is the branch-selection half of loudnormFilter, split
// out so TestLoudnormFilter can pin all three branches without an ffmpeg binary
// or a corpus. Keeping this pure is what makes the reference testable at all:
// the two production tests that used to pin this logic
// (TestBuildTwoPassLoudnormFilter, TestLoudnormGateFallbackOffset_NilStats) were
// deleted with the production code they covered.
func loudnormFilterFromStats(stats *loudnessStats) string {
	base := loudnormSinglePass()
	if stats == nil {
		return base
	}

	if !loudnessStatsUsable(stats) {
		// Gated to nothing. Anchor the lift to the true-peak headroom instead,
		// bounded by the same ceiling the old export path used.
		inputTP := parseLoudnessField(stats.InputTP)
		if math.IsInf(inputTP, 0) || math.IsNaN(inputTP) {
			return base
		}
		offsetDB := math.Max(-loudnormMaxOffsetDB, math.Min(loudnormMaxOffsetDB, targetTruePeak-inputTP))
		if math.Abs(offsetDB) < 0.05 {
			return base
		}
		return fmt.Sprintf("%s:offset=%.1f", base, offsetDB)
	}

	return fmt.Sprintf("%s:measured_I=%s:measured_LRA=%s:measured_TP=%s:measured_thresh=%s:linear=true:offset=%s",
		base, stats.InputI, stats.InputLRA, stats.InputTP, stats.InputThresh, stats.TargetOffset)
}

// loudnessStatsUsable reports whether every measured field loudnorm's second pass
// consumes is a finite number. Checking InputI alone would not be equivalent to
// the removed export path's gate: FFmpeg reports "-inf" for input_thresh as well
// as input_i on a gated clip, and any one unparseable field sent that path to its
// fallback.
func loudnessStatsUsable(s *loudnessStats) bool {
	return s.isValid()
}

func writeWAV(t *testing.T, path string, pcm []byte) string {
	t.Helper()
	require.NoError(t, convert.SavePCMDataToWAV(path, pcm, sampleRate, bitDepth))
	return path
}

// measure reads a file's loudness through the same production analyser all
// paths are judged by. Its own targets do not affect the input_* fields it
// reports, which is all this harness reads.
func measure(t *testing.T, ctx context.Context, ffmpegBin, path string) loudness {
	t.Helper()
	stats, err := analyzeLoudness(ctx, ffmpegBin, path)
	require.NoError(t, err, "measuring %s", path)

	return loudness{
		integrated: parseLoudnessField(stats.InputI),
		truePeak:   parseLoudnessField(stats.InputTP),
		rangeLU:    parseLoudnessField(stats.InputLRA),
	}
}

// parseLoudnessField accepts the "-inf" FFmpeg emits for silence and for clips
// that fall entirely under the R128 gate, which is a real outcome here rather
// than a parse failure.
func parseLoudnessField(v string) float64 {
	f, err := strconv.ParseFloat(v, 64)
	if err != nil {
		return math.Inf(-1)
	}
	return f
}

func report(t *testing.T, outcomes []outcome) {
	t.Helper()

	t.Logf("\nTargets: I=%.1f LUFS  TP=%.1f dBTP  LRA=%.1f LU   pre-fix clamp %.0f dB, native clamp %.0f dB, ffmpeg clamp %.0f dB\n",
		targetLUFS, targetTruePeak, targetLRA, preFixMaxGainDB, nativeMaxGainDB(), loudnormMaxOffsetDB)

	header := fmt.Sprintf("%-38s | %14s | %6s | %6s | %15s | %7s | %7s | %7s | %14s",
		"case", "input I/TP", "want", "hdroom", "applied (bound)",
		"pre-fix I", "native I", "ffmpeg I", "LRA p/n/f")
	t.Log(header)
	t.Log(dashes(len(header)))

	var worstPreFix, worstNative float64
	var worstPreFixName, worstNativeName string
	subGate, clampBound := 0, 0

	for i := range outcomes {
		o := &outcomes[i]

		dPreFix := delta(o.ffm.integrated, o.preFix.integrated)
		dNative := delta(o.ffm.integrated, o.native.integrated)
		if math.Abs(dPreFix) > math.Abs(worstPreFix) && !math.IsInf(dPreFix, 0) {
			worstPreFix, worstPreFixName = dPreFix, o.tc.name
		}
		if math.Abs(dNative) > math.Abs(worstNative) && !math.IsInf(dNative, 0) {
			worstNative, worstNativeName = dNative, o.tc.name
		}
		switch o.preFixPlan.bound {
		case boundSubGate:
			subGate++
		case boundClamp:
			clampBound++
		case boundNone, boundPeak, boundSilent:
			// Not a divergence worth counting: the gain either reached target or
			// was stopped by the true-peak ceiling, which both paths respect.
		}

		t.Logf("%-38s | %14s | %6s | %6s | %15s | %7s | %7s | %7s | %14s",
			o.tc.name,
			fmt.Sprintf("%6.2f/%6.2f", o.input.integrated, o.input.truePeak),
			db(o.preFixPlan.wantDB),
			db(o.preFixPlan.headroomDB),
			fmt.Sprintf("%6s (%s)", db(o.preFixPlan.gainDB), o.preFixPlan.bound),
			lufs(o.preFix.integrated),
			lufs(o.native.integrated),
			lufs(o.ffm.integrated),
			fmt.Sprintf("%4.1f/%4.1f/%4.1f", o.preFix.rangeLU, o.native.rangeLU, o.ffm.rangeLU),
		)
	}

	t.Log("")
	t.Log("want    = gain needed to reach the loudness target, before any limiting")
	t.Log("hdroom  = gain the true-peak ceiling allows")
	t.Log("bound   = what actually stopped the gain: clamp, peak, sub-gate, or - for none")
	t.Log("LRA p/n/f = loudness range of the pre-fix / native / ffmpeg output")
	t.Log("")
	t.Logf("cases where the pre-fix 30 dB clamp was the binding constraint: %d of %d", clampBound, len(outcomes))
	t.Logf("cases where R128 gating left the native path with no gain at all: %d of %d", subGate, len(outcomes))
	t.Logf("largest finite loudness gap, ffmpeg vs pre-fix native: %+.2f LU on %s", worstPreFix, worstPreFixName)
	t.Logf("largest finite loudness gap, ffmpeg vs native now:      %+.2f LU on %s", worstNative, worstNativeName)
}

func delta(a, b float64) float64 {
	if math.IsInf(a, 0) || math.IsInf(b, 0) {
		return math.Inf(1)
	}
	return a - b
}

func db(v float64) string {
	if math.IsInf(v, 0) {
		return "  -Inf"
	}
	return fmt.Sprintf("%+6.1f", v)
}

func lufs(v float64) string {
	if math.IsInf(v, 0) {
		return "   -Inf"
	}
	return fmt.Sprintf("%7.2f", v)
}

func dashes(n int) string {
	b := make([]byte, n)
	for i := range b {
		b[i] = '-'
	}
	return string(b)
}
