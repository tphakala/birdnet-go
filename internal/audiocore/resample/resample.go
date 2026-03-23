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
// It keeps a pre-allocated output buffer that grows as needed but is
// never shrunk, so subsequent calls with the same input size incur no
// heap allocations.
//
// Resampler is not safe for concurrent use; callers must serialise calls.
type Resampler struct {
	fromRate int
	toRate   int
	inner    *audioresampler.SimpleResampler
	inFloats []float64 // scratch buffer for PCM→float64 conversion
	outBuf   []byte    // pre-allocated output buffer, reused across calls
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

// ResampleInto resamples the raw 16-bit PCM bytes in input and returns a slice
// of the internal output buffer containing the resampled PCM bytes.
//
// The returned slice is only valid until the next call to ResampleInto or Close.
// input must contain an even number of bytes (each sample is two bytes).
func (r *Resampler) ResampleInto(input []byte) ([]byte, error) {
	if len(input) == 0 {
		return []byte{}, nil
	}

	if len(input)%bytesPerSample != 0 {
		return nil, errors.Newf("input length %d is not a multiple of %d (16-bit PCM requires even byte count)", len(input), bytesPerSample).
			Component("audiocore/resample").
			Category(errors.CategoryValidation).
			Context("input_len", len(input)).
			Build()
	}

	sampleCount := len(input) / bytesPerSample

	// Grow scratch buffer if needed.
	if cap(r.inFloats) < sampleCount {
		r.inFloats = make([]float64, sampleCount)
	}
	r.inFloats = r.inFloats[:sampleCount]

	// Convert 16-bit PCM bytes → normalised float64.
	for i := range sampleCount {
		sample := int16(binary.LittleEndian.Uint16(input[i*bytesPerSample:])) //nolint:gosec // G115: intentional uint16→int16 bit reinterpretation for PCM audio
		r.inFloats[i] = float64(sample) / pcm16Scale
	}

	// Resample.
	outFloats, err := r.inner.Process(r.inFloats)
	if err != nil {
		return nil, errors.Newf("resampler process failed: %w", err).
			Component("audiocore/resample").
			Category(errors.CategoryAudio).
			Context("from_rate", r.fromRate).
			Context("to_rate", r.toRate).
			Context("input_samples", sampleCount).
			Build()
	}

	// Ensure output buffer is large enough.
	requiredBytes := len(outFloats) * bytesPerSample
	if cap(r.outBuf) < requiredBytes {
		r.outBuf = make([]byte, requiredBytes)
	}
	r.outBuf = r.outBuf[:requiredBytes]

	// Convert normalised float64 → 16-bit PCM bytes.
	for i, f := range outFloats {
		// Clamp to [-1.0, 1.0].
		if f > 1.0 {
			f = 1.0
		} else if f < -1.0 {
			f = -1.0
		}
		s := int16(f * pcm16MaxPositive)
		binary.LittleEndian.PutUint16(r.outBuf[i*bytesPerSample:], uint16(s)) //nolint:gosec // G115: intentional int16→uint16 bit reinterpretation for PCM audio
	}

	return r.outBuf, nil
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
	r.outBuf = nil
	return nil
}

// String returns a human-readable description of the resampler for logging.
func (r *Resampler) String() string {
	return fmt.Sprintf("Resampler(%d→%d Hz)", r.fromRate, r.toRate)
}
