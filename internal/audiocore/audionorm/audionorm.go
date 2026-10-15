// Package audionorm performs two-pass loudness normalization of in-memory PCM
// audio to the EBU R 128 / ITU-R BS.1770-4 standard.
//
// Pass one measures the gated integrated loudness and the true peak; pass two
// applies a single linear gain so the signal reaches the target loudness without
// its true peak exceeding the configured ceiling. Linear gain preserves the
// clip's dynamics and cannot pump, which suits field and nature recordings.
//
// The implementation is pure Go (no cgo) so it cross-compiles cleanly, and uses
// github.com/tphakala/simd for the hot paths. It works at any standard audio
// sample rate (>= 8000 Hz) and channel count; the primary, most-tested path is
// 48 kHz, 16-bit, mono. Loudness is accurate at every supported rate; the
// true-peak estimate uses BS.1770-4 4x oversampling, which is specified for
// rates up to 48 kHz and is less precise above that.
//
//	opts := audionorm.DefaultOptions() // -23 LUFS, -1.0 dBTP, 48 kHz mono
//	res, err := audionorm.NormalizeInt16(pcm, opts)
package audionorm

import (
	"fmt"
	"math"
	"sync"

	"github.com/tphakala/birdnet-go/internal/audiocore/pcmgain"
)

// meterPool reuses Meters across the convenience functions so repeated calls at
// a fixed sample rate and channel count are allocation-free in steady state.
var meterPool sync.Pool

// acquireMeter returns a reset meter for the given config, reusing a pooled one
// when its config matches. A pooled meter with a different config is discarded
// (left for GC) and a fresh one is built.
func acquireMeter(sampleRate, channels int) *Meter {
	if v := meterPool.Get(); v != nil {
		m := v.(*Meter)
		if m.sampleRate == sampleRate && m.channels == channels {
			m.Reset()
			return m
		}
	}
	return NewMeter(sampleRate, channels)
}

func releaseMeter(m *Meter) { meterPool.Put(m) }

// Default normalization parameters. The target and ceiling are the EBU R 128
// reference values (-23 LUFS programme loudness, -1 dBTP maximum true peak).
const (
	DefaultTargetLUFS   = -23.0
	DefaultTruePeakDBTP = -1.0
	defaultSampleRate   = 48000
	defaultChannels     = 1
)

// Options configures normalization. Use DefaultOptions and override as needed.
type Options struct {
	SampleRate int // samples per second, must be >= 8000
	// Channels is the interleaved channel count, must be > 0. Layouts of 5 or 6
	// channels are assumed to be SMPTE-ordered (L, R, C, [LFE,] Ls, Rs) for the
	// BS.1770 channel weighting; other orderings will mis-weight surround/LFE.
	Channels     int
	TargetLUFS   float64 // target integrated loudness, in (-70, 0); the zero value is rejected
	TruePeakDBTP float64 // maximum allowed true peak in dBTP, must be <= 0
}

// DefaultOptions returns the EBU R 128 defaults for the BirdNET-Go signal:
// 48 kHz mono, target -23 LUFS, ceiling -1.0 dBTP.
func DefaultOptions() Options {
	return Options{
		SampleRate:   defaultSampleRate,
		Channels:     defaultChannels,
		TargetLUFS:   DefaultTargetLUFS,
		TruePeakDBTP: DefaultTruePeakDBTP,
	}
}

// Measurement holds the pass-one analysis of a buffer. Silent or sub-400 ms
// input yields IntegratedLUFS = -Inf; silent input yields TruePeakDBTP = -Inf.
type Measurement struct {
	IntegratedLUFS float64 // gated integrated loudness (LUFS)
	TruePeakDBTP   float64 // maximum true peak (dBTP)
}

// Result describes the outcome of a normalization.
type Result struct {
	Input        Measurement // pass-one measurement of the original buffer
	TargetGainDB float64     // gain needed to reach TargetLUFS, before peak limiting
	GainDB       float64     // gain actually applied
	PeakLimited  bool        // true if the true-peak ceiling reduced the gain
	OutputLUFS   float64     // estimated loudness after normalization (-Inf for silence)
}

// MeasureFloat32 measures interleaved float32 PCM (nominal range [-1, 1]).
func MeasureFloat32(pcm []float32, sampleRate, channels int) (Measurement, error) {
	if err := validateDims(sampleRate, channels, len(pcm)); err != nil {
		return Measurement{}, err
	}
	m := acquireMeter(sampleRate, channels)
	defer releaseMeter(m)
	m.AddFloat32(pcm)
	return result(m), nil
}

// MeasureInt16 measures interleaved 16-bit PCM.
func MeasureInt16(pcm []int16, sampleRate, channels int) (Measurement, error) {
	if err := validateDims(sampleRate, channels, len(pcm)); err != nil {
		return Measurement{}, err
	}
	m := acquireMeter(sampleRate, channels)
	defer releaseMeter(m)
	m.AddInt16(pcm)
	return result(m), nil
}

// MeasureInt16Bytes measures interleaved little-endian 16-bit PCM supplied as raw
// bytes. It is equivalent to reinterpreting the bytes as []int16 and calling
// MeasureInt16, but decodes inline via Meter.AddInt16Bytes so no intermediate
// slice is allocated. A trailing odd byte (never produced by real int16 PCM) is
// ignored; the source bytes are not modified.
func MeasureInt16Bytes(pcm []byte, sampleRate, channels int) (Measurement, error) {
	if err := validateDims(sampleRate, channels, len(pcm)/2); err != nil {
		return Measurement{}, err
	}
	m := acquireMeter(sampleRate, channels)
	defer releaseMeter(m)
	m.AddInt16Bytes(pcm)
	return result(m), nil
}

// NormalizeFloat32 normalizes interleaved float32 PCM in place and returns the
// outcome. Silent input is left untouched (GainDB = 0).
func NormalizeFloat32(pcm []float32, opts Options) (Result, error) {
	if err := opts.validate(len(pcm)); err != nil {
		return Result{}, err
	}
	m := acquireMeter(opts.SampleRate, opts.Channels)
	defer releaseMeter(m)
	m.AddFloat32(pcm)
	res := PlanGain(result(m), opts)
	if res.GainDB != 0 {
		applyGainFloat32(pcm, math.Pow(10, res.GainDB/20))
	}
	return res, nil
}

// NormalizeInt16 normalizes interleaved 16-bit PCM in place and returns the
// outcome. Gain is applied with rounding and saturation so a boost never wraps.
// Silent input is left untouched (GainDB = 0).
func NormalizeInt16(pcm []int16, opts Options) (Result, error) {
	if err := opts.validate(len(pcm)); err != nil {
		return Result{}, err
	}
	m := acquireMeter(opts.SampleRate, opts.Channels)
	defer releaseMeter(m)
	m.AddInt16(pcm)
	res := PlanGain(result(m), opts)
	if res.GainDB != 0 {
		applyGainInt16(pcm, math.Pow(10, res.GainDB/20))
	}
	return res, nil
}

// result reads the final measurement from a fully-fed meter.
func result(m *Meter) Measurement {
	return Measurement{
		IntegratedLUFS: m.IntegratedLoudness(),
		TruePeakDBTP:   m.TruePeakDBTP(),
	}
}

// PlanGain decides the linear gain for pass two from a pass-one Measurement and
// the target/ceiling in opts, without touching any audio buffer. It returns the
// full Result: the target gain (target minus measured loudness), the gain
// actually applied after true-peak limiting, and the projected output loudness.
//
// The Normalize* helpers call this internally. Callers that want to apply the
// gain themselves (for example while encoding, to avoid a second buffer pass)
// can measure with Measure* and plan here. opts is not validated against a
// buffer length, so callers that bypass Normalize* should ensure SampleRate,
// Channels, TargetLUFS and TruePeakDBTP are sane.
func PlanGain(meas Measurement, opts Options) Result {
	res := Result{Input: meas, OutputLUFS: math.Inf(-1)}
	if math.IsInf(meas.IntegratedLUFS, -1) {
		return res // silence: nothing to normalize
	}

	res.TargetGainDB = opts.TargetLUFS - meas.IntegratedLUFS
	gain := res.TargetGainDB

	// Never let the gained true peak exceed the ceiling.
	if !math.IsInf(meas.TruePeakDBTP, -1) {
		headroom := opts.TruePeakDBTP - meas.TruePeakDBTP
		if gain > headroom {
			gain = headroom
			res.PeakLimited = true
		}
	}

	res.GainDB = gain
	res.OutputLUFS = meas.IntegratedLUFS + gain
	return res
}

// DefaultMaxGainDB bounds the loudness gain the BirdWeather soundscape upload
// applies: 30 dB, the same clamp the BirdWeather FFmpeg FLAC path used, so a
// soundscape's loudness is corrected no more aggressively under one encoder
// than the other. It stops a near-silent clip from being over-amplified into
// loud static, and a hot clip from being driven toward digital silence.
//
// The detection-clip export path deliberately does NOT use this: it bounds gain
// with MaxNormalizeGainDB (shared with on-demand transcode), derived from
// the widest lift a valid loudness target can ask for. (transcode.MaxGainDB aliases
// MaxNormalizeGainDB and serves as the on-demand request validation range.)
const DefaultMaxGainDB = 30.0

// MaxNormalizeGainDB is the gain clamp for loudness normalization of detection
// clips and on-demand clips. It is the widest lift any valid configuration can
// ask for: the most aggressive permitted target (-10 LUFS, conf.MaxTargetLUFS)
// minus the EBU R128 absolute gate (AbsoluteGateLUFS), which is exactly 60 dB.
// Deliberately not DefaultMaxGainDB, the BirdWeather upload ceiling.
const MaxNormalizeGainDB = 60.0

// ClampGainDB constrains a planned gain (dB) to [-maxAbsDB, +maxAbsDB] and reports
// whether the clamp took effect. Callers pass PlanGain's GainDB and their own
// ceiling (usually DefaultMaxGainDB); the returned bool lets one caller log the
// limiting while another applies it silently. maxAbsDB is treated as a magnitude;
// callers pass a non-negative value.
func ClampGainDB(gainDB, maxAbsDB float64) (clamped float64, limited bool) {
	// Treat maxAbsDB as a magnitude so a stray negative ceiling still yields a
	// sane symmetric range rather than clamping everything to a negative bound.
	absLimit := math.Abs(maxAbsDB)
	switch {
	case gainDB > absLimit:
		return absLimit, true
	case gainDB < -absLimit:
		return -absLimit, true
	default:
		return gainDB, false
	}
}

// PlanClampedGainInt16Bytes runs the measure -> plan -> clamp sequence the
// BirdWeather native FLAC upload uses: it measures the integrated loudness and true peak of
// interleaved little-endian int16 PCM bytes (via MeasureInt16Bytes, which decodes
// inline without mutating pcm), plans the single gain that brings the clip to
// opts.TargetLUFS without its true peak exceeding opts.TruePeakDBTP (via PlanGain),
// then clamps that gain to [-maxAbsGainDB, +maxAbsGainDB] (via ClampGainDB).
//
// It returns the clamped gain to apply, the pass-one measurement, the pre-clamp
// planning Result (whose GainDB and PeakLimited callers use for logging), and
// whether the clamp took effect. Silent or sub-400 ms input yields gainDB == 0,
// leaving quiet clips unchanged rather than boosting the noise floor. On a
// measurement error every value is its zero and err is the raw MeasureInt16Bytes
// error, so callers can wrap it with their own component and context.
//
// Only the sample rate and channel count are validated (by MeasureInt16Bytes).
// Like PlanGain, opts.TargetLUFS and opts.TruePeakDBTP are NOT range-checked
// here, so callers must pass a target in (-70, 0) and a ceiling <= 0; a
// non-finite or out-of-range target otherwise flows through as a NaN or garbage
// gain. The sole current caller, the BirdWeather upload path, passes fixed
// constants. (Detection export and on-demand transcode use
// PlanGainWithGateFallbackInt16Bytes instead, which applies a gate fallback for
// clips R128 cannot measure.)
func PlanClampedGainInt16Bytes(pcm []byte, opts Options, maxAbsGainDB float64) (gainDB float64, meas Measurement, res Result, limited bool, err error) {
	meas, err = MeasureInt16Bytes(pcm, opts.SampleRate, opts.Channels)
	if err != nil {
		return 0, Measurement{}, Result{}, false, err
	}
	res = PlanGain(meas, opts)
	gainDB, limited = ClampGainDB(res.GainDB, maxAbsGainDB)
	return gainDB, meas, res, limited, nil
}

func validateDims(sampleRate, channels, n int) error {
	switch {
	case sampleRate < MinSampleRate:
		return fmt.Errorf("audionorm: sample rate %d Hz too low; minimum is %d Hz (K-weighting is undefined below it)", sampleRate, MinSampleRate)
	case channels <= 0:
		return fmt.Errorf("audionorm: channels must be positive, got %d", channels)
	case n%channels != 0:
		return fmt.Errorf("audionorm: sample count %d is not a multiple of channels %d", n, channels)
	}
	return nil
}

func (o Options) validate(n int) error {
	if err := validateDims(o.SampleRate, o.Channels, n); err != nil {
		return err
	}
	switch {
	case math.IsNaN(o.TargetLUFS) || math.IsInf(o.TargetLUFS, 0):
		return fmt.Errorf("audionorm: target loudness must be finite, got %v", o.TargetLUFS)
	case o.TargetLUFS >= 0 || o.TargetLUFS <= AbsoluteGateLUFS:
		return fmt.Errorf("audionorm: target loudness %.2f LUFS out of range (%.0f, 0)", o.TargetLUFS, AbsoluteGateLUFS)
	case math.IsNaN(o.TruePeakDBTP) || math.IsInf(o.TruePeakDBTP, 0):
		return fmt.Errorf("audionorm: true-peak ceiling must be finite, got %v", o.TruePeakDBTP)
	case o.TruePeakDBTP > 0:
		return fmt.Errorf("audionorm: true-peak ceiling %.2f dBTP must be <= 0", o.TruePeakDBTP)
	}
	return nil
}

// GateFallbackGainDB handles the clip EBU R128 gating cannot measure. A clip
// whose gated integrated loudness falls below the -70 LUFS absolute gate yields
// -Inf, and PlanGain plans no gain at all for it, so the clip would be exported
// untouched and inaudible. When such a clip still has a finite true peak there
// is enough signal to work with, so the gain is anchored to the true-peak
// ceiling, which is what the removed FFmpeg loudnorm path did for the same case.
//
// That anchor alone is not enough. Lifting purely by true-peak headroom leaves
// the resulting loudness at (ceiling - crest factor), so a FLAT signal (steady
// hiss from a dead or muted microphone, whose crest factor is only a few dB)
// lands far LOUDER than the loudness target: measured, a sub-gate noise floor
// came out at -13.8 LUFS against a -23 target. The second bound closes that.
// Gating failing is itself information: the clip's true loudness must be below
// AbsoluteGateLUFS, so lifting by no more than (target - gate) cannot
// overshoot the target however peaky or flat the clip turns out to be.
//
// That premise is worth stating precisely, since the bound rests on it.
// The meter reports -Inf on exactly three paths: every 400 ms block sat
// under the absolute gate (the premise holds by construction); the relative gate
// eliminated every block (unreachable, because the loudest gated block always
// exceeds a gate set 10 LU below their mean); or the clip is shorter than one
// 400 ms block, where loudness is undefined at ANY level. Only that third path
// breaks the premise. Detection export never reaches it, since its clips are
// whole segments, but on-demand clip windows can be shorter than 400 ms. Such a
// window is lifted to its true-peak ceiling, by at most (target - gate) dB, and
// is attenuated instead when its peak is already above the ceiling.
//
// Genuine silence (a true peak of -Inf too) reports false and is left alone;
// there is nothing to lift.
func GateFallbackGainDB(meas Measurement, targetLUFS, truePeakDBTP float64) (gainDB float64, ok bool) {
	if !math.IsInf(meas.IntegratedLUFS, -1) || math.IsInf(meas.TruePeakDBTP, -1) {
		return 0, false
	}
	peakBound := truePeakDBTP - meas.TruePeakDBTP
	loudnessBound := targetLUFS - AbsoluteGateLUFS
	return math.Min(peakBound, loudnessBound), true
}

// RefineLiftedGainDB returns the extra gain to add on top of a gate-fallback
// lift. Loudness gating is not linear in gain, so the loudness of the lifted
// signal cannot be derived arithmetically: blocks that sat under the absolute
// gate before the lift pass it afterwards. The only way to know is to measure
// the lifted signal, which is what this does, on a copy so pcm is untouched.
//
// Once the clip is measurable a normal PlanGain finishes the job, landing it on
// the target and respecting the true-peak ceiling. A clip still under the gate
// after the lift (or a measurement error) yields 0, leaving the conservative
// fallback as the final answer rather than guessing.
//
// This runs only for sub-gate clips, so the extra measurement pass is off the
// common path entirely.
func RefineLiftedGainDB(pcm []byte, liftDB float64, opts Options) float64 {
	lifted, err := MeasureInt16Bytes(pcmgain.Applied(pcm, liftDB), opts.SampleRate, opts.Channels)
	if err != nil || math.IsInf(lifted.IntegratedLUFS, -1) {
		return 0
	}
	return PlanGain(lifted, opts).GainDB
}

// GatedPlan is the outcome of PlanGainWithGateFallbackInt16Bytes.
type GatedPlan struct {
	// GainDB is the clamped gain to apply.
	GainDB float64
	// Measurement is the pass-one measurement of the unmodified PCM.
	Measurement Measurement
	// Result is the planning Result before the gate fallback and the clamp.
	Result Result
	// GateLifted reports whether the clip sat under the absolute gate and the gate
	// fallback (and its refinement) decided the gain.
	GateLifted bool
	// Clamped reports whether the clamp took effect.
	Clamped bool
}

// PlanGainWithGateFallbackInt16Bytes runs the full detection-export gain plan on
// interleaved little-endian int16 PCM bytes: measure, PlanGain, the gate
// fallback and its refinement for clips under the absolute gate (see
// GateFallbackGainDB), then ClampGainDB to [-maxAbsGainDB, +maxAbsGainDB].
//
// On a measurement error the plan is the zero value and err is the raw
// MeasureInt16Bytes error. pcm is not modified.
func PlanGainWithGateFallbackInt16Bytes(pcm []byte, opts Options, maxAbsGainDB float64) (GatedPlan, error) {
	meas, err := MeasureInt16Bytes(pcm, opts.SampleRate, opts.Channels)
	if err != nil {
		return GatedPlan{}, err
	}
	plan := GatedPlan{Measurement: meas, Result: PlanGain(meas, opts)}
	planned := plan.Result.GainDB
	if fallback, ok := GateFallbackGainDB(meas, opts.TargetLUFS, opts.TruePeakDBTP); ok {
		// The fallback is deliberately conservative, because a sub-gate clip's
		// real loudness is unknown and the bound has to assume the worst case.
		// Lifting it, however, usually raises it above the gate, at which point
		// it CAN be measured: refine from that real measurement so a very quiet
		// clip still lands on target instead of merely somewhere audible.
		planned = fallback + RefineLiftedGainDB(pcm, fallback, opts)
		plan.GateLifted = true
	}
	plan.GainDB, plan.Clamped = ClampGainDB(planned, maxAbsGainDB)
	return plan, nil
}
