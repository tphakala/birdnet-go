package audionorm

import (
	"math"
	"slices"
	"testing"
)

// sineInterleaved synthesizes `seconds` of a `freq` Hz sine at `dbfs` peak
// level, replicated in-phase across `channels` interleaved channels.
func sineInterleaved(dbfs, freq, seconds, fs float64, channels int) []float64 {
	amp := math.Pow(10, dbfs/20)
	n := int(seconds * fs)
	out := make([]float64, n*channels)
	w := 2 * math.Pi * freq / fs
	for i := range n {
		s := amp * math.Sin(w*float64(i))
		for c := range channels {
			out[i*channels+c] = s
		}
	}
	return out
}

func measure(t *testing.T, pcm []float64, fs, channels int) float64 {
	t.Helper()
	m := NewMeter(fs, channels)
	m.AddFloat64(pcm)
	return m.IntegratedLoudness()
}

// EBU Tech 3341 Test Case 1: stereo 1 kHz sine at -23 dBFS reads -23.0 LUFS.
// This is the calibration anchor of the whole standard.
func TestMeterStereo1kHzMinus23(t *testing.T) {
	got := measure(t, sineInterleaved(-23, 1000, 4, 48000, 2), 48000, 2)
	if math.Abs(got-(-23.0)) > 0.1 {
		t.Errorf("integrated loudness = %.3f LUFS, want -23.0 +/-0.1", got)
	}
}

// EBU Tech 3341 Test Case 2: stereo 1 kHz sine at -33 dBFS reads -33.0 LUFS.
func TestMeterStereo1kHzMinus33(t *testing.T) {
	got := measure(t, sineInterleaved(-33, 1000, 4, 48000, 2), 48000, 2)
	if math.Abs(got-(-33.0)) > 0.1 {
		t.Errorf("integrated loudness = %.3f LUFS, want -33.0 +/-0.1", got)
	}
}

// EBU Tech 3341 Test Case 3: -23 dBFS tone for 10 s then silence for 10 s still
// reads -23.0 LUFS, because the absolute -70 LUFS gate discards the silence.
func TestMeterAbsoluteGateDiscardsSilence(t *testing.T) {
	fs := 48000
	tone := sineInterleaved(-23, 1000, 10, float64(fs), 2)
	silence := make([]float64, 10*fs*2)
	pcm := slices.Concat(tone, silence)
	got := measure(t, pcm, fs, 2)
	if math.Abs(got-(-23.0)) > 0.1 {
		t.Errorf("integrated loudness = %.3f LUFS, want -23.0 +/-0.1 (silence must be gated)", got)
	}
}

// Mono is the BirdNET-Go signal. A mono 1 kHz sine at -23 dBFS reads
// 10*log10(2) ~= 3.01 dB lower than the stereo case, i.e. -26.01 LUFS.
func TestMeterMono1kHzMinus23(t *testing.T) {
	got := measure(t, sineInterleaved(-23, 1000, 4, 48000, 1), 48000, 1)
	want := -23.0 - 10*math.Log10(2)
	if math.Abs(got-want) > 0.1 {
		t.Errorf("mono integrated loudness = %.3f LUFS, want %.3f +/-0.1", got, want)
	}
}

// AddInt16 must produce the same loudness as feeding the equivalent float64
// samples, since it just converts inline.
func TestMeterAddInt16MatchesFloat64(t *testing.T) {
	i16 := sineInt16(-14, 1000, 2, 48000)
	f64 := make([]float64, len(i16))
	for i, s := range i16 {
		f64[i] = float64(s) / 32768
	}

	mi := NewMeter(48000, 1)
	mi.AddInt16(i16)
	mf := NewMeter(48000, 1)
	mf.AddFloat64(f64)

	if mi.IntegratedLoudness() != mf.IntegratedLoudness() {
		t.Errorf("AddInt16 loudness %.6f != AddFloat64 %.6f", mi.IntegratedLoudness(), mf.IntegratedLoudness())
	}
}

// AddFloat32 must match AddFloat64 of the widened samples exactly.
func TestMeterAddFloat32MatchesFloat64(t *testing.T) {
	f32 := sineFloat32(-14, 1000, 2, 48000, 1)
	f64 := make([]float64, len(f32))
	for i, s := range f32 {
		f64[i] = float64(s)
	}

	ma := NewMeter(48000, 1)
	ma.AddFloat32(f32)
	mb := NewMeter(48000, 1)
	mb.AddFloat64(f64)

	if ma.IntegratedLoudness() != mb.IntegratedLoudness() {
		t.Errorf("AddFloat32 loudness %.6f != AddFloat64 %.6f", ma.IntegratedLoudness(), mb.IntegratedLoudness())
	}
}

// The LFE channel (index 3 in a 5.1 layout) has weight 0, so energy there must
// not contribute to loudness. A 5.1 signal with a loud tone only in LFE reads as
// silence.
func TestMeterLFEExcluded(t *testing.T) {
	const fs, channels = 48000, 6
	n := fs * 2
	pcm := make([]float64, n*channels)
	amp := 0.5 // a loud tone, but only in LFE
	w := 2 * math.Pi * 1000 / float64(fs)
	for i := range n {
		pcm[i*channels+3] = amp * math.Sin(w*float64(i)) // LFE only
	}
	got := measure(t, pcm, fs, channels)
	if !math.IsInf(got, -1) {
		t.Errorf("LFE-only 5.1 signal measured %.3f LUFS, want -Inf (LFE excluded)", got)
	}
}

// Pure-Go relative-gate check (independent of ffmpeg): a loud segment followed by
// a much quieter one. The quiet segment passes the absolute gate but falls below
// the relative gate (-10 LU under the mean), so it is excluded and the result
// equals the loud segment's loudness.
func TestMeterRelativeGateExcludesQuietSegment(t *testing.T) {
	fs := 48000
	loud := sineInterleaved(-20, 1000, 5, float64(fs), 2)  // -20 LUFS
	quiet := sineInterleaved(-50, 1000, 5, float64(fs), 2) // -50 LUFS, gated out
	got := measure(t, append(loud, quiet...), fs, 2)
	if math.Abs(got-(-20.0)) > 0.2 {
		t.Errorf("integrated loudness = %.3f LUFS, want -20.0 +/-0.2 (quiet part relative-gated)", got)
	}
}

// A signal shorter than one 400 ms gating block has no measurable loudness.
func TestMeterTooShortIsSilent(t *testing.T) {
	got := measure(t, sineInterleaved(-23, 1000, 0.2, 48000, 1), 48000, 1)
	if !math.IsInf(got, -1) {
		t.Errorf("integrated loudness = %.3f, want -Inf for sub-400ms input", got)
	}
}
