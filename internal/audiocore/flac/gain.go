package flac

import (
	"encoding/binary"
	"math"
)

// int16 sample bounds for saturation.
const (
	maxInt16 = 32767
	minInt16 = -32768
)

// applyGainInt16 scales interleaved int16 little-endian PCM samples in src by
// factor, writing the saturated result into dst. dst must be at least len(src)
// bytes and must not overlap src. Values that exceed the int16 range are
// clamped (saturated) rather than wrapped, which matches FFmpeg's volume filter
// perceptually (both clamp) but not bit-for-bit, since FFmpeg rounds/dithers in
// float. A trailing odd byte (not produced by real int16 PCM) is copied through
// unchanged so dst is always fully written.
//
// This is a pure-Go single pass rather than a SIMD path: github.com/tphakala/simd
// exposes no int16 scale primitive, and routing through its f64 scale would need
// two full-width buffers and two passes, which is slower for this
// memory-bandwidth-bound 16-bit operation.
func applyGainInt16(dst, src []byte, factor float64) {
	n := len(src) &^ 1 // largest even length <= len(src)
	for i := 0; i < n; i += 2 {
		s := int16(binary.LittleEndian.Uint16(src[i:]))
		scaled := math.Round(float64(s) * factor)
		switch {
		case scaled > maxInt16:
			scaled = maxInt16
		case scaled < minInt16:
			scaled = minInt16
		}
		binary.LittleEndian.PutUint16(dst[i:], uint16(int16(scaled)))
	}
	if n < len(src) {
		copy(dst[n:], src[n:])
	}
}
