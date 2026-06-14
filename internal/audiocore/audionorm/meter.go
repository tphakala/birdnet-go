package audionorm

import (
	"math"

	simdf32 "github.com/tphakala/simd/f32"
)

// Calibration and gating constants from ITU-R BS.1770-4 / EBU R 128.
const (
	loudnessOffset   = -0.691 // LUFS calibration offset
	absoluteGateLUFS = -70.0  // absolute gate
	relativeGateLU   = -10.0  // relative gate, below the absolute-gated mean
	subBlockSeconds  = 0.1    // sub-block length (100 ms hop, 75% overlap); 4 per 400 ms block
	subBlocksPerGate = 4      // 400 ms / 100 ms

	// minSampleRate is the lowest supported rate. Below ~3364 Hz the K-weighting
	// high-shelf center (1681.97 Hz) exceeds Nyquist and the bilinear transform
	// produces unstable coefficients; 8000 Hz (the lowest standard audio rate)
	// keeps the filter well-conditioned and the 100 ms sub-block non-empty.
	minSampleRate = 8000
)

// Gating is performed in the energy domain (float32 comparisons) to avoid a
// per-block log; only the final integrated value is converted to dB.
var (
	// absGateEnergy is the absolute -70 LUFS gate as a weighted-energy threshold:
	// a block passes when z > absGateEnergy, equivalent to
	// loudnessOffset + 10*log10(z) > absoluteGateLUFS.
	absGateEnergy = float32(math.Pow(10, (absoluteGateLUFS-loudnessOffset)/10))
	// relGateEnergyFactor is the energy ratio for the -10 LU relative gate.
	relGateEnergyFactor = float32(math.Pow(10, relativeGateLU/10))
)

// biquadState is a stateful second-order section in float32. Coefficients are
// computed in float64 (see kWeightingStages) and stored here as float32; the
// per-sample recurrence runs in float32, which stays within ~0.001 LU of the
// float64 result even for low-frequency, long-duration signals.
type biquadState struct {
	b0, b1, b2, a1, a2 float32
	x1, x2, y1, y2     float32
}

func newBiquadState(c biquad) biquadState {
	return biquadState{
		b0: float32(c.b0), b1: float32(c.b1), b2: float32(c.b2),
		a1: float32(c.a1), a2: float32(c.a2),
	}
}

func (s *biquadState) process(x float32) float32 {
	y := s.b0*x + s.b1*s.x1 + s.b2*s.x2 - s.a1*s.y1 - s.a2*s.y2
	s.x2, s.x1 = s.x1, x
	s.y2, s.y1 = s.y1, y
	return y
}

func (s *biquadState) resetState() { s.x1, s.x2, s.y1, s.y2 = 0, 0, 0, 0 }

// Meter accumulates K-weighted energy from interleaved PCM and computes the
// BS.1770-4 gated integrated loudness. Feed all samples with AddFloat64/Float32/
// Int16 (one or many calls), then read IntegratedLoudness. A Meter is not safe
// for concurrent use. The numeric path is float32; the returned loudness and
// true-peak values are float64.
type Meter struct {
	sampleRate   int
	channels     int
	weights      []float32
	subBlockSize int // samples per channel in a 100 ms sub-block

	stage1 []biquadState // per channel
	stage2 []biquadState // per channel

	accum    [][]float32 // per-channel K-weighted samples for the current sub-block
	accumLen int         // frames filled in the current sub-block

	// subEnergy[c] holds the sum of squares of each completed 100 ms sub-block
	// for channel c.
	subEnergy [][]float32

	tp []truePeakChannel // per-channel true-peak estimator (raw, not K-weighted)

	zBuf  []float32 // reusable per-block weighted-energy scratch for gating
	tpSig []float32 // reusable [history | chunk] signal buffer for true-peak convolution
}

// subBlockSamples returns the number of samples per channel in a 100 ms gating
// sub-block at the given sample rate. It is < 1 only for absurdly low rates,
// which the public API rejects.
func subBlockSamples(sampleRate int) int {
	return int(math.Round(subBlockSeconds * float64(sampleRate)))
}

// NewMeter creates a meter for the given sample rate (Hz) and interleaved
// channel count. It panics if channels is not positive or sampleRate is below
// minSampleRate (8000 Hz); callers using the validated top-level API never hit
// that.
func NewMeter(sampleRate, channels int) *Meter {
	if channels <= 0 {
		panic("audionorm: NewMeter requires positive channels")
	}
	if sampleRate < minSampleRate {
		panic("audionorm: NewMeter requires sample rate >= 8000 Hz")
	}
	s1, s2 := kWeightingStages(float64(sampleRate))
	m := &Meter{
		sampleRate:   sampleRate,
		channels:     channels,
		weights:      channelWeights(channels),
		subBlockSize: subBlockSamples(sampleRate),
		stage1:       make([]biquadState, channels),
		stage2:       make([]biquadState, channels),
		accum:        make([][]float32, channels),
		subEnergy:    make([][]float32, channels),
		tp:           make([]truePeakChannel, channels),
		tpSig:        make([]float32, tapsPerPhase-1+tpChunk),
	}
	for c := range channels {
		m.stage1[c] = newBiquadState(s1)
		m.stage2[c] = newBiquadState(s2)
		m.accum[c] = make([]float32, m.subBlockSize)
	}
	return m
}

// Reset returns the meter to its just-constructed state so it can be reused for
// another clip without reallocating. Filter state, sub-block accumulation, true
// peak, and gating buffers are cleared while their backing arrays are retained,
// so a reused meter is allocation-free in steady state. Sample rate and channel
// count are unchanged.
func (m *Meter) Reset() {
	for c := range m.channels {
		m.stage1[c].resetState()
		m.stage2[c].resetState()
		m.subEnergy[c] = m.subEnergy[c][:0]
		m.tp[c] = truePeakChannel{}
	}
	m.accumLen = 0
}

// AddFloat64 feeds interleaved float64 samples (range nominally [-1, 1]). The
// length must be a multiple of the channel count; any trailing partial frame is
// ignored. Samples are narrowed to float32 internally (lossless for audio in
// [-1, 1]).
func (m *Meter) AddFloat64(interleaved []float64) {
	frames := len(interleaved) / m.channels
	if frames == 0 {
		return
	}
	for i := range frames {
		base := i * m.channels
		for c := range m.channels {
			m.kweight(c, float32(interleaved[base+c]))
		}
		m.advanceFrame()
	}
	for c := range m.channels {
		for pos := 0; pos < frames; pos += tpChunk {
			n := min(tpChunk, frames-pos)
			dst := m.tpChunkStart(c, n)
			for i := range n {
				dst[i] = float32(interleaved[(pos+i)*m.channels+c])
			}
			m.tpChunkEnd(c, n)
		}
	}
}

// AddFloat32 feeds interleaved float32 samples, the native BirdNET-Go format.
func (m *Meter) AddFloat32(interleaved []float32) {
	frames := len(interleaved) / m.channels
	if frames == 0 {
		return
	}
	for i := range frames {
		base := i * m.channels
		for c := range m.channels {
			m.kweight(c, interleaved[base+c])
		}
		m.advanceFrame()
	}
	for c := range m.channels {
		for pos := 0; pos < frames; pos += tpChunk {
			n := min(tpChunk, frames-pos)
			dst := m.tpChunkStart(c, n)
			for i := range n {
				dst[i] = interleaved[(pos+i)*m.channels+c]
			}
			m.tpChunkEnd(c, n)
		}
	}
}

// AddInt16 feeds interleaved 16-bit PCM, converting inline with the 1/32768
// convention (exact in float32, a power of two).
func (m *Meter) AddInt16(interleaved []int16) {
	const scale = float32(1.0 / 32768.0)
	frames := len(interleaved) / m.channels
	if frames == 0 {
		return
	}
	for i := range frames {
		base := i * m.channels
		for c := range m.channels {
			m.kweight(c, float32(interleaved[base+c])*scale)
		}
		m.advanceFrame()
	}
	for c := range m.channels {
		for pos := 0; pos < frames; pos += tpChunk {
			n := min(tpChunk, frames-pos)
			dst := m.tpChunkStart(c, n)
			for i := range n {
				dst[i] = float32(interleaved[(pos+i)*m.channels+c]) * scale
			}
			m.tpChunkEnd(c, n)
		}
	}
}

// kweight runs one sample of channel c through the K-weighting cascade, storing
// the weighted result in the current sub-block accumulator.
func (m *Meter) kweight(c int, x float32) {
	m.accum[c][m.accumLen] = m.stage2[c].process(m.stage1[c].process(x))
}

// tpChunkStart copies channel c's carried-over history into the shared true-peak
// signal buffer and returns the slice to fill with the next n raw samples.
func (m *Meter) tpChunkStart(c, n int) []float32 {
	copy(m.tpSig[:tapsPerPhase-1], m.tp[c].hist[:])
	return m.tpSig[tapsPerPhase-1 : tapsPerPhase-1+n]
}

// tpChunkEnd runs the fused oversampling peak detection over the filled buffer.
func (m *Meter) tpChunkEnd(c, n int) {
	m.tp[c].batch(m.tpSig[:tapsPerPhase-1+n])
}

// advanceFrame closes the current sub-block when it fills, recording its energy.
func (m *Meter) advanceFrame() {
	m.accumLen++
	if m.accumLen == m.subBlockSize {
		for c := range m.channels {
			m.subEnergy[c] = append(m.subEnergy[c], simdf32.SumOfSquares(m.accum[c]))
		}
		m.accumLen = 0
	}
}

// blockEnergies returns the per-gating-block weighted mean-square energy z_j
// (sum_i G_i * meanSquare_i) over all overlapping 400 ms blocks.
func (m *Meter) blockEnergies() []float32 {
	numSub := len(m.subEnergy[0])
	numBlocks := numSub - (subBlocksPerGate - 1)
	if numBlocks <= 0 {
		return nil
	}
	blockSamples := float32(subBlocksPerGate * m.subBlockSize)
	if cap(m.zBuf) < numBlocks {
		m.zBuf = make([]float32, numBlocks)
	}
	z := m.zBuf[:numBlocks]
	for j := range numBlocks {
		var sum float32
		for c := range m.channels {
			e := m.subEnergy[c][j] + m.subEnergy[c][j+1] + m.subEnergy[c][j+2] + m.subEnergy[c][j+3]
			sum += m.weights[c] * (e / blockSamples)
		}
		z[j] = sum
	}
	return z
}

// IntegratedLoudness returns the BS.1770-4 gated integrated loudness in LUFS.
// It returns negative infinity when the input is shorter than one 400 ms block
// or entirely below the absolute gate (silence). Gating runs in the energy
// domain (float32); only the final dB conversion uses float64.
func (m *Meter) IntegratedLoudness() float64 {
	z := m.blockEnergies()
	if len(z) == 0 {
		return math.Inf(-1)
	}

	// Absolute gate at -70 LUFS (z > absGateEnergy).
	var sumAbs float32
	var cntAbs int
	for _, zj := range z {
		if zj > absGateEnergy {
			sumAbs += zj
			cntAbs++
		}
	}
	if cntAbs == 0 {
		return math.Inf(-1)
	}

	// Relative gate: 10 LU below the absolute-gated mean (z > 0.1 * meanAbs).
	relGate := (sumAbs / float32(cntAbs)) * relGateEnergyFactor
	var sumRel float32
	var cntRel int
	for _, zj := range z {
		if zj > absGateEnergy && zj > relGate {
			sumRel += zj
			cntRel++
		}
	}
	if cntRel == 0 {
		return math.Inf(-1)
	}
	return loudnessOffset + 10*math.Log10(float64(sumRel)/float64(cntRel))
}

// TruePeakDBTP returns the maximum true peak across all channels in dBTP (full
// scale = 0 dBTP). It returns negative infinity for digital silence.
func (m *Meter) TruePeakDBTP() float64 {
	maxDB := math.Inf(-1)
	for c := range m.tp {
		if d := m.tp[c].dbtp(); d > maxDB {
			maxDB = d
		}
	}
	return maxDB
}

// channelWeights returns the BS.1770-4 channel weighting coefficients G_i for a
// standard interleaved layout. Mono, stereo, 3.0 and 4.0 use unity weights; 5.0
// and 5.1 apply the +1.5 dB surround weighting and exclude LFE.
func channelWeights(channels int) []float32 {
	w := make([]float32, channels)
	for i := range w {
		w[i] = 1.0
	}
	switch channels {
	case 5: // L R C Ls Rs
		w[3], w[4] = 1.41, 1.41
	case 6: // 5.1: L R C LFE Ls Rs
		w[3] = 0.0
		w[4], w[5] = 1.41, 1.41
	}
	return w
}
