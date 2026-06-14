package audionorm

import (
	"math"
	"testing"
)

// The true peak of a pure sine equals its amplitude. A -6 dBFS, 1 kHz sine must
// therefore measure about -6.0 dBTP after oversampling.
func TestTruePeakSineAmplitude(t *testing.T) {
	m := NewMeter(48000, 1)
	m.AddFloat64(sineInterleaved(-6, 1000, 1, 48000, 1))
	got := m.TruePeakDBTP()
	if math.Abs(got-(-6.0)) > 0.2 {
		t.Errorf("true peak = %.3f dBTP, want -6.0 +/-0.2", got)
	}
}

// Silence has no peak.
func TestTruePeakSilence(t *testing.T) {
	m := NewMeter(48000, 1)
	m.AddFloat64(make([]float64, 48000))
	got := m.TruePeakDBTP()
	if !math.IsInf(got, -1) {
		t.Errorf("true peak = %.3f dBTP, want -Inf for silence", got)
	}
}

// TruePeakDBTP must be non-destructive: the group-delay drain runs on a copy of
// the filter state, so repeated calls return the same value.
func TestTruePeakIdempotent(t *testing.T) {
	m := NewMeter(48000, 1)
	m.AddFloat64(sineInterleaved(-6, 4000, 0.5, 48000, 1))
	first := m.TruePeakDBTP()
	second := m.TruePeakDBTP()
	if first != second {
		t.Errorf("TruePeakDBTP not idempotent: %.6f then %.6f", first, second)
	}
}

// Oversampled true peak must never under-report the raw sample peak.
func TestTruePeakAtLeastSamplePeak(t *testing.T) {
	pcm := sineInterleaved(-3, 5000, 0.5, 48000, 1)
	var samplePeak float64
	for _, s := range pcm {
		// The meter processes float32, so compare against the float32 sample peak.
		if a := math.Abs(float64(float32(s))); a > samplePeak {
			samplePeak = a
		}
	}
	m := NewMeter(48000, 1)
	m.AddFloat64(pcm)
	tp := math.Pow(10, m.TruePeakDBTP()/20)
	if tp < samplePeak-1e-9 {
		t.Errorf("true peak %.6f below sample peak %.6f", tp, samplePeak)
	}
}
