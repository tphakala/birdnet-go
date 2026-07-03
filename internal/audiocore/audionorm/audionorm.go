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

// DefaultMaxGainDB bounds the loudness gain the FLAC export paths apply: 30 dB,
// the same clamp the BirdWeather FFmpeg FLAC path uses, so a clip's loudness is
// corrected no more aggressively under one encoder than the other. It stops a
// near-silent clip from being over-amplified into loud static, and a hot clip
// from being driven toward digital silence. (Distinct from ffmpeg.MaxGainDB,
// which is a wider loudnorm-offset validation range, not this export clamp.)
const DefaultMaxGainDB = 30.0

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

func validateDims(sampleRate, channels, n int) error {
	switch {
	case sampleRate < minSampleRate:
		return fmt.Errorf("audionorm: sample rate %d Hz too low; minimum is %d Hz (K-weighting is undefined below it)", sampleRate, minSampleRate)
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
	case o.TargetLUFS >= 0 || o.TargetLUFS <= absoluteGateLUFS:
		return fmt.Errorf("audionorm: target loudness %.2f LUFS out of range (%.0f, 0)", o.TargetLUFS, absoluteGateLUFS)
	case math.IsNaN(o.TruePeakDBTP) || math.IsInf(o.TruePeakDBTP, 0):
		return fmt.Errorf("audionorm: true-peak ceiling must be finite, got %v", o.TruePeakDBTP)
	case o.TruePeakDBTP > 0:
		return fmt.Errorf("audionorm: true-peak ceiling %.2f dBTP must be <= 0", o.TruePeakDBTP)
	}
	return nil
}
