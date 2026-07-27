//go:build !race

// The race detector adds allocations to instrumented code, which makes
// testing.AllocsPerRun report non-zero counts. These zero-allocation assertions
// are therefore only meaningful without -race.

package audionorm

import "testing"

// A reused meter (Reset between clips) must not allocate in steady state.
func TestMeterReuseZeroAlloc(t *testing.T) {
	m := NewMeter(48000, 1)
	pcm := sineInterleaved(-12, 1000, 1, 48000, 1)
	allocs := testing.AllocsPerRun(20, func() {
		m.Reset()
		m.AddFloat64(pcm)
		_ = m.IntegratedLoudness()
		_ = m.TruePeakDBTP()
	})
	if allocs != 0 {
		t.Errorf("reused meter: %.1f allocs/op, want 0", allocs)
	}
}

// The int16 reuse path (Reset + AddInt16) must also be zero-alloc.
func TestMeterReuseInt16ZeroAlloc(t *testing.T) {
	m := NewMeter(48000, 1)
	pcm := sineInt16(-12, 1000, 1, 48000)
	allocs := testing.AllocsPerRun(20, func() {
		m.Reset()
		m.AddInt16(pcm)
		_ = m.IntegratedLoudness()
		_ = m.TruePeakDBTP()
	})
	if allocs != 0 {
		t.Errorf("reused meter (int16): %.1f allocs/op, want 0", allocs)
	}
}

// The convenience MeasureInt16 must reach zero allocations in steady state via
// the internal meter pool (same config reused).
func TestMeasureInt16SteadyStateZeroAlloc(t *testing.T) {
	pcm := sineInt16(-12, 1000, 1, 48000)
	allocs := testing.AllocsPerRun(20, func() {
		_, _ = MeasureInt16(pcm, 48000, 1)
	})
	if allocs != 0 {
		t.Errorf("pooled MeasureInt16: %.1f allocs/op, want 0", allocs)
	}
}

// MeasureInt16Bytes must also reach zero allocations in steady state: it decodes
// bytes inline via the pooled meter, allocating no intermediate []int16.
func TestMeasureInt16BytesSteadyStateZeroAlloc(t *testing.T) {
	pcm := int16sToLEBytes(sineInt16(-12, 1000, 1, 48000))
	allocs := testing.AllocsPerRun(20, func() {
		_, _ = MeasureInt16Bytes(pcm, 48000, 1)
	})
	if allocs != 0 {
		t.Errorf("pooled MeasureInt16Bytes: %.1f allocs/op, want 0", allocs)
	}
}

// NormalizeInt16 in steady state must also be zero-alloc (gain applied in place
// with a stack scratch buffer; meter pooled).
func TestNormalizeInt16SteadyStateZeroAlloc(t *testing.T) {
	src := sineInt16(-12, 1000, 1, 48000)
	pcm := make([]int16, len(src))
	opts := DefaultOptions()
	allocs := testing.AllocsPerRun(20, func() {
		copy(pcm, src)
		_, _ = NormalizeInt16(pcm, opts)
	})
	if allocs != 0 {
		t.Errorf("pooled NormalizeInt16: %.1f allocs/op, want 0", allocs)
	}
}
