// Package resample provides a resampler wrapper with buffer reuse for audio routing.
//
// The Resampler wraps github.com/tphakala/go-audio-resampler and exposes a
// byte-oriented API that accepts and returns 16-bit signed PCM (little-endian).
// It maintains a pre-allocated output buffer that is reused across calls to
// minimise allocations in the hot path.
//
// The returned slice from ResampleInto is only valid until the next call.
// Per-route goroutines ensure sequential access so this is safe by design.
package resample

import (
	"encoding/binary"
	"fmt"

	audioresampler "github.com/tphakala/go-audio-resampler"

	"github.com/tphakala/birdnet-go/internal/errors"
)

// Byte-level constants for 16-bit PCM encoding.
const (
	// bytesPerSample is the number of bytes in a single 16-bit PCM sample.
	bytesPerSample = 2

	// pcm16Scale is used to normalise int16 to [-1.0, 1.0].
	// Using 32768.0 ensures -32768 maps exactly to -1.0.
	pcm16Scale = 32768.0

	// pcm16MaxPositive is the scale factor for float64 → int16 conversion.
	// Using 32767.0 avoids overflow when the input is exactly +1.0.
	pcm16MaxPositive = 32767.0
)

// Resampler converts mono 16-bit PCM audio between two sample rates.
// It keeps pre-allocated buffers that grow as needed but are never shrunk,
// so subsequent calls with the same input size incur no heap allocations.
//
// Resampler is not safe for concurrent use; callers must serialise calls.
type Resampler struct {
	fromRate  int
	toRate    int
	inner     *audioresampler.SimpleResampler
	inFloats  []float64 // scratch buffer for PCM→float64 conversion
	outFloats []float64 // scratch buffer for resampled float64 output
	outBuf    []byte    // pre-allocated output buffer for ResampleInto
}

// NewResampler creates a Resampler that converts audio from fromRate to toRate.
// Returns nil, nil when fromRate == toRate — no resampling is required.
// Returns an error if the underlying resampler cannot be initialised.
func NewResampler(fromRate, toRate int) (*Resampler, error) {
	if fromRate == toRate {
		return nil, nil //nolint:nilnil // intentional: nil means "no resampling needed"
	}

	inner, err := audioresampler.NewEngine(
		float64(fromRate),
		float64(toRate),
		audioresampler.QualityMedium,
	)
	if err != nil {
		return nil, errors.Newf("failed to create resampler from %d Hz to %d Hz: %w", fromRate, toRate, err).
			Component("audiocore/resample").
			Category(errors.CategoryAudio).
			Context("from_rate", fromRate).
			Context("to_rate", toRate).
			Build()
	}

	return &Resampler{
		fromRate: fromRate,
		toRate:   toRate,
		inner:    inner,
	}, nil
}

// EstimateOutputBytes returns the maximum number of bytes that ResampleTo
// may write for the given input byte length.
func (r *Resampler) EstimateOutputBytes(inputBytes int) int {
	if inputBytes <= 0 {
		return 0
	}
	return r.inner.EstimateOutput(inputBytes/bytesPerSample) * bytesPerSample
}

// ResampleTo resamples the raw 16-bit PCM bytes in input and writes the
// result into dst, returning the number of bytes written. Callers should
// use dst[:n] for the audio data. dst must be at least
// EstimateOutputBytes(len(input)) bytes long; if it is too small,
// an error is returned without advancing resampler state.
//
// input must contain an even number of bytes (each sample is two bytes).
func (r *Resampler) ResampleTo(input, dst []byte) (int, error) {
	if len(input) == 0 {
		return 0, nil
	}

	if len(input)%bytesPerSample != 0 {
		return 0, errors.Newf("input length %d is not a multiple of %d (16-bit PCM requires even byte count)", len(input), bytesPerSample).
			Component("audiocore/resample").
			Category(errors.CategoryValidation).
			Context("input_len", len(input)).
			Build()
	}

	sampleCount := len(input) / bytesPerSample

	// Grow input scratch buffer if needed.
	if cap(r.inFloats) < sampleCount {
		r.inFloats = make([]float64, sampleCount)
	}
	r.inFloats = r.inFloats[:sampleCount]

	// Convert 16-bit PCM bytes → normalised float64.
	for i := range sampleCount {
		sample := int16(binary.LittleEndian.Uint16(input[i*bytesPerSample:])) //nolint:gosec // G115: intentional uint16→int16 bit reinterpretation for PCM audio
		r.inFloats[i] = float64(sample) / pcm16Scale
	}

	// Grow output float scratch buffer if needed.
	needed := r.inner.EstimateOutput(sampleCount)
	if cap(r.outFloats) < needed {
		r.outFloats = make([]float64, needed)
	}
	r.outFloats = r.outFloats[:needed]

	// Resample using the zero-allocation path.
	n, err := r.inner.ProcessInto(r.inFloats, r.outFloats)
	if err != nil {
		return 0, errors.Newf("resampler process failed: %w", err).
			Component("audiocore/resample").
			Category(errors.CategoryAudio).
			Context("from_rate", r.fromRate).
			Context("to_rate", r.toRate).
			Context("input_samples", sampleCount).
			Build()
	}

	// Verify dst is large enough for the actual output.
	requiredBytes := n * bytesPerSample
	if len(dst) < requiredBytes {
		return 0, errors.Newf("destination buffer too small: need %d bytes, have %d", requiredBytes, len(dst)).
			Component("audiocore/resample").
			Category(errors.CategoryValidation).
			Context("required_bytes", requiredBytes).
			Context("dst_len", len(dst)).
			Build()
	}

	// Convert normalised float64 → 16-bit PCM bytes directly into dst.
	for i, f := range r.outFloats[:n] {
		if f > 1.0 {
			f = 1.0
		} else if f < -1.0 {
			f = -1.0
		}
		s := int16(f * pcm16MaxPositive)
		binary.LittleEndian.PutUint16(dst[i*bytesPerSample:], uint16(s)) //nolint:gosec // G115: intentional int16→uint16 bit reinterpretation for PCM audio
	}

	return requiredBytes, nil
}

// ResampleInto resamples the raw 16-bit PCM bytes in input and returns a slice
// of the internal output buffer containing the resampled PCM bytes.
//
// The returned slice is only valid until the next call to ResampleInto or Close.
// input must contain an even number of bytes (each sample is two bytes).
func (r *Resampler) ResampleInto(input []byte) ([]byte, error) {
	if len(input) == 0 {
		return []byte{}, nil
	}

	// Ensure internal output buffer is large enough.
	needed := r.EstimateOutputBytes(len(input))
	if cap(r.outBuf) < needed {
		r.outBuf = make([]byte, needed)
	}
	r.outBuf = r.outBuf[:needed]

	n, err := r.ResampleTo(input, r.outBuf)
	if err != nil {
		return nil, err
	}
	return r.outBuf[:n], nil
}

// FromRate returns the input sample rate in Hz.
func (r *Resampler) FromRate() int { return r.fromRate }

// ToRate returns the output sample rate in Hz.
func (r *Resampler) ToRate() int { return r.toRate }

// Close releases resources held by the resampler.
// After Close, the Resampler must not be used.
func (r *Resampler) Close() error {
	if r.inner == nil {
		return nil
	}

	// SimpleResampler has no Close method; release references so GC can reclaim them.
	r.inner = nil
	r.inFloats = nil
	r.outFloats = nil
	r.outBuf = nil
	return nil
}

// String returns a human-readable description of the resampler for logging.
func (r *Resampler) String() string {
	return fmt.Sprintf("Resampler(%d->%d Hz)", r.fromRate, r.toRate)
}

// ResampleBytes is a one-shot convenience function that resamples raw 16-bit
// PCM bytes from one sample rate to another. It creates a temporary Resampler,
// processes the input, and returns an independent copy of the output.
// When fromRate == toRate, returns the input slice directly (no copy, no allocation).
func ResampleBytes(pcm []byte, fromRate, toRate int) ([]byte, error) {
	if fromRate == toRate {
		return pcm, nil
	}

	r, err := NewResampler(fromRate, toRate)
	if err != nil {
		return nil, err
	}
	defer func() { _ = r.Close() }()

	out, err := r.ResampleInto(pcm)
	if err != nil {
		return nil, err
	}

	result := make([]byte, len(out))
	copy(result, out)
	return result, nil
}
