package audionorm

import simdf32 "github.com/tphakala/simd/f32"

// gainChunk bounds the scratch buffer used to apply int16 gain so memory stays
// O(1) regardless of clip length.
const gainChunk = 8192

// applyGainFloat32 multiplies every sample by a linear gain in place. Float
// samples are not clamped: the caller's true-peak ceiling is what bounds them.
func applyGainFloat32(pcm []float32, gain float64) {
	if gain == 1.0 || len(pcm) == 0 {
		return
	}
	simdf32.Scale(pcm, pcm, float32(gain))
}

// applyGainInt16 multiplies every sample by a linear gain in place, rounding to
// the nearest integer (ties to even) and saturating to the int16 range so a
// boost can never wrap around. Both the multiply and the saturating round are
// SIMD-accelerated. Work proceeds in fixed-size chunks to avoid allocating a
// full-length scratch buffer for large clips.
func applyGainInt16(pcm []int16, gain float64) {
	if gain == 1.0 || len(pcm) == 0 {
		return
	}
	g := float32(gain)
	var scratch [gainChunk]float32
	for start := 0; start < len(pcm); start += gainChunk {
		end := min(start+gainChunk, len(pcm))
		seg := pcm[start:end]
		buf := scratch[:len(seg)]
		// buf = seg * gain in int16 magnitude units, then round-to-even and
		// saturate back into seg. Float32ToInt16Scale clamps to [-32768, 32767].
		simdf32.Int16ToFloat32Scale(buf, seg, g)
		simdf32.Float32ToInt16Scale(seg, buf, 1.0)
	}
}
