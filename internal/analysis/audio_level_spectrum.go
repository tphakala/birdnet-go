// audio_level_spectrum.go - Server-side magnitude spectrum for the live spectrogram.
//
// Safari/WebKit cannot feed an HLS-backed <audio> element into the Web Audio
// graph (WebKit bug 180696, open since 2017), so AnalyserNode.getByteFrequencyData()
// returns all zeros there and the live spectrogram waterfall stays blank. This
// computes the same magnitude data server-side from the PCM the audio-level
// consumer already receives, so browsers whose AnalyserNode is dead can render
// the waterfall from the audio-level SSE stream instead.
package analysis

import (
	"encoding/binary"
	"math"
	"math/cmplx"
	"time"
)

const (
	// spectrumFFTSize is the analysis window in samples. It matches the
	// browser-side AnalyserNode fftSize so the fallback bins line up with the
	// bins SpectrogramCanvas already expects.
	spectrumFFTSize = 1024

	// spectrumBinCount is the number of magnitude bins published per column,
	// i.e. the usable half of the FFT.
	spectrumBinCount = spectrumFFTSize / 2

	// spectrumInterval caps how often a column is produced per source. It
	// matches the audio-level SSE rate limit, so no column is computed that
	// the stream would drop anyway.
	spectrumInterval = 50 * time.Millisecond

	// spectrumDBFloor and spectrumDBCeiling mirror the AnalyserNode defaults
	// (minDecibels/maxDecibels), so a column lands in roughly the same part of
	// the colour map as an analyser-driven one. The match is close, not exact:
	// Web Audio uses a Blackman window and 0.8 temporal smoothing, this uses a
	// Hann window and none.
	spectrumDBFloor   = -100.0
	spectrumDBCeiling = -30.0

	// spectrumMagnitudeEpsilon avoids log10(0) for digital silence.
	spectrumMagnitudeEpsilon = 1e-12
)

// spectrumAnalyzer turns a stream of 16-bit PCM frames into fixed-size
// magnitude columns. It keeps a rolling window so columns have a constant
// resolution regardless of how many samples an individual frame carries.
//
// Columns are produced unconditionally rather than on subscriber demand:
// BenchmarkSpectrumAnalyzer measures ~42us per column on an Intel N150, so at
// 20 columns/s a source costs well under 0.1% of a core there, and still a
// fraction of a percent on a Raspberry Pi. Demand tracking would cost more
// plumbing than it saves.
//
// Not safe for concurrent use: one analyzer belongs to one AudioLevelConsumer,
// whose Write is called by that route's single drain goroutine.
type spectrumAnalyzer struct {
	ring     []float64    // newest spectrumFFTSize samples, oldest first
	filled   int          // samples written into ring so far, capped at len(ring)
	window   []float64    // Hann window
	scale    float64      // window-coherent-gain correction for magnitudes
	scratch  []complex128 // reused FFT working buffer
	lastEmit time.Time
}

func newSpectrumAnalyzer() *spectrumAnalyzer {
	window := make([]float64, spectrumFFTSize)
	var sum float64
	for i := range window {
		window[i] = 0.5 * (1 - math.Cos(2*math.Pi*float64(i)/float64(spectrumFFTSize-1)))
		sum += window[i]
	}
	return &spectrumAnalyzer{
		ring:    make([]float64, spectrumFFTSize),
		window:  window,
		scale:   2.0 / sum,
		scratch: make([]complex128, spectrumFFTSize),
	}
}

// process appends a PCM frame to the rolling window and returns a new
// magnitude column, or nil when the window is not full yet or the previous
// column is younger than spectrumInterval.
func (s *spectrumAnalyzer) process(pcm []byte, now time.Time) []byte {
	s.push(pcm)
	if s.filled < spectrumFFTSize {
		return nil
	}
	if !s.lastEmit.IsZero() && now.Sub(s.lastEmit) < spectrumInterval {
		return nil
	}
	s.lastEmit = now
	return s.column()
}

// push copies the newest samples of pcm into the rolling window, discarding
// the same number of oldest samples.
func (s *spectrumAnalyzer) push(pcm []byte) {
	// Drop a trailing odd byte: it cannot form a 16-bit sample.
	if len(pcm)%2 != 0 {
		pcm = pcm[:len(pcm)-1]
	}
	count := len(pcm) / 2
	if count == 0 {
		return
	}

	// A frame longer than the window replaces it outright; only its newest
	// spectrumFFTSize samples matter.
	if count >= spectrumFFTSize {
		pcm = pcm[(count-spectrumFFTSize)*2:]
		count = spectrumFFTSize
	} else {
		copy(s.ring, s.ring[count:])
	}

	base := spectrumFFTSize - count
	for i := range count {
		sample := int16(binary.LittleEndian.Uint16(pcm[i*2 : i*2+2])) //nolint:gosec // G115: intentional uint16→int16 bit reinterpretation for PCM audio
		s.ring[base+i] = float64(sample) / pcm16Max
	}

	if s.filled += count; s.filled > spectrumFFTSize {
		s.filled = spectrumFFTSize
	}
}

// column runs the FFT over the current window and scales each magnitude to a
// 0-255 byte over [spectrumDBFloor, spectrumDBCeiling].
func (s *spectrumAnalyzer) column() []byte {
	for i := range spectrumFFTSize {
		s.scratch[i] = complex(s.ring[i]*s.window[i], 0)
	}
	fftInPlace(s.scratch)

	const dbRange = spectrumDBCeiling - spectrumDBFloor
	// A fresh slice per column: it is published on a channel and read by SSE
	// goroutines, so it cannot be reused.
	out := make([]byte, spectrumBinCount)
	for i := range spectrumBinCount {
		db := 20 * math.Log10(cmplx.Abs(s.scratch[i])*s.scale+spectrumMagnitudeEpsilon)
		scaled := (db - spectrumDBFloor) * (255.0 / dbRange)
		switch {
		case scaled <= 0:
			out[i] = 0
		case scaled >= 255:
			out[i] = 255
		default:
			out[i] = byte(scaled)
		}
	}
	return out
}

// fftInPlace performs an in-place iterative Cooley-Tukey FFT on data, whose
// length must be a power of two.
//
// This is deliberately a small package-local copy rather than a shared helper:
// the only other FFT in the tree is private to internal/audiocore/ultrasonic,
// a bat-detection package the live audio-level path should not depend on.
// Extracting a common DSP package is a separate cleanup.
func fftInPlace(data []complex128) {
	n := len(data)
	if n <= 1 {
		return
	}

	// Bit-reversal permutation.
	j := 0
	for i := 1; i < n; i++ {
		bit := n >> 1
		for j&bit != 0 {
			j ^= bit
			bit >>= 1
		}
		j ^= bit
		if i < j {
			data[i], data[j] = data[j], data[i]
		}
	}

	// Butterfly stages.
	for size := 2; size <= n; size <<= 1 {
		half := size >> 1
		wn := cmplx.Rect(1, -2.0*math.Pi/float64(size))
		for start := 0; start < n; start += size {
			w := complex(1, 0)
			for k := range half {
				u := data[start+k]
				v := w * data[start+k+half]
				data[start+k] = u + v
				data[start+k+half] = u - v
				w *= wn
			}
		}
	}
}
