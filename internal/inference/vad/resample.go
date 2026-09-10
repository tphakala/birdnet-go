package vad

import (
	"encoding/binary"

	"github.com/tphakala/birdnet-go/internal/audiocore/resample"
	"github.com/tphakala/birdnet-go/internal/errors"
)

const (
	// bytesPerSample is the size of one 16-bit PCM sample.
	bytesPerSample = 2
	// pcm16Scale normalises int16 to [-1.0, 1.0]; 32768 maps -32768 to -1.0.
	pcm16Scale = 32768.0
)

// to16k converts a chunk of 16-bit little-endian mono PCM at sampleRate into
// normalised float32 samples in [-1,1] at 16 kHz.
//
// Analysis chunks overlap (50%), so each chunk is resampled INDEPENDENTLY with
// a fresh resampler. A persistent streaming resampler fed overlapping windows
// would corrupt continuity: the second window replays audio the first already
// consumed, shifting the filter state. Independent resampling costs a small,
// infrequent allocation (once per chunk, ~ every 1.5 s per source) that is
// negligible next to the ONNX inference it feeds.
//
// The returned slice is freshly allocated and owned by the caller.
func to16k(pcm []byte, sampleRate int) ([]float32, error) {
	if len(pcm) == 0 {
		return nil, nil
	}
	if len(pcm)%bytesPerSample != 0 {
		return nil, errors.Newf("vad: pcm length %d is not a multiple of %d", len(pcm), bytesPerSample).
			Component("inference/vad").
			Category(errors.CategoryValidation).
			Build()
	}

	// Already at the target rate: convert PCM16 -> float32 directly.
	if sampleRate == sampleRate16k {
		out := make([]float32, len(pcm)/bytesPerSample)
		pcm16ToFloat32(pcm, out)
		return out, nil
	}

	r, err := resample.NewResampler(sampleRate, sampleRate16k)
	if err != nil {
		return nil, err
	}
	// r is non-nil because sampleRate != sampleRate16k.
	defer func() { _ = r.Close() }()

	aliased, err := r.ResampleFloat32Into(pcm)
	if err != nil {
		return nil, err
	}
	// ResampleFloat32Into aliases the resampler's internal scratch buffer, which
	// is released on Close; return an independent copy.
	out := make([]float32, len(aliased))
	copy(out, aliased)
	return out, nil
}

// pcm16ToFloat32 decodes 16-bit little-endian PCM bytes into normalised float32
// samples in dst. dst must have len(pcm)/bytesPerSample elements.
func pcm16ToFloat32(pcm []byte, dst []float32) {
	for i := range dst {
		sample := int16(binary.LittleEndian.Uint16(pcm[i*bytesPerSample:])) //nolint:gosec // G115: intentional uint16->int16 bit reinterpretation for PCM audio
		dst[i] = float32(sample) / pcm16Scale
	}
}
