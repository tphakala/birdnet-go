// Package pcmgain applies a decibel volume adjustment to interleaved
// little-endian int16 PCM, the capture format used throughout BirdNET-Go's
// audio pipeline.
//
// It exists so every native encoder applies gain identically. The FLAC encoder
// streams gain through a pooled scratch chunk (see ApplyInt16), while the AAC
// and Opus encoders hand a whole buffer to a one-shot library call and use
// Applied instead. Both go through the same saturating scale, so a clip
// exported at the same gain sounds the same whichever format it lands in.
package pcmgain

import (
	"encoding/binary"
	"math"
)

// int16 sample bounds for saturation.
const (
	maxInt16 = 32767
	minInt16 = -32768
)

// FactorFromDB converts a decibel gain to the linear multiplier ApplyInt16
// expects. A gain of 0 dB yields exactly 1.
func FactorFromDB(gainDB float64) float64 {
	if gainDB == 0 {
		return 1
	}
	return math.Pow(10, gainDB/20)
}

// ApplyInt16 scales interleaved int16 little-endian PCM samples in src by
// factor, writing the saturated result into dst. dst must be at least len(src)
// bytes and must not overlap src. Values that exceed the int16 range are
// clamped (saturated) rather than wrapped, which matches FFmpeg's volume filter
// perceptually (both clamp) but not bit-for-bit, since FFmpeg rounds/dithers in
// float. A trailing odd byte (not produced by real int16 PCM) is copied through
// unchanged so dst is always fully written.
//
// This is a pure-Go single pass rather than a SIMD path, but NOT because SIMD
// would be slower: routing through github.com/tphakala/simd's float32 scale
// primitives (Int16ToFloat32Scale / Float32ToInt16Scale, both with amd64 and
// arm64 assembly) measures roughly 13x faster on a 720k-sample clip. It is
// rejected on correctness and scale instead. simd exposes no int16 sample API,
// so the route needs an unsafe reinterpretation of []byte as []int16 that only
// holds on little-endian hosts; the float32 round trip is not bit-exact with
// math.Round, differing by 1 LSB on a fraction of samples; and gain runs once
// per detection, where the whole kernel is about 1% of an encode, so the
// absolute saving is ~2 ms. Revisit only if this moves to a per-frame path.
func ApplyInt16(dst, src []byte, factor float64) {
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

// Applied returns src scaled by gainDB. At 0 dB it returns src itself, so the
// common no-gain export stays zero-copy; otherwise it returns a freshly
// allocated gained copy and leaves src untouched. Callers that can stream in
// chunks should use ApplyInt16 with a pooled scratch buffer instead, since
// Applied necessarily allocates a full-size copy of the clip.
func Applied(src []byte, gainDB float64) []byte {
	if gainDB == 0 {
		return src
	}
	dst := make([]byte, len(src))
	ApplyInt16(dst, src, FactorFromDB(gainDB))
	return dst
}
