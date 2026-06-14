package audionorm

import (
	"math"
	"testing"
)

// sineInt16 synthesizes mono int16 PCM of a sine at the given peak dBFS.
func sineInt16(dbfs, freq, seconds, fs float64) []int16 {
	amp := math.Pow(10, dbfs/20) * 32767
	n := int(seconds * fs)
	out := make([]int16, n)
	w := 2 * math.Pi * freq / fs
	for i := range n {
		out[i] = int16(math.Round(amp * math.Sin(w*float64(i))))
	}
	return out
}

func sineFloat32(dbfs, freq, seconds, fs float64, channels int) []float32 {
	f64 := sineInterleaved(dbfs, freq, seconds, fs, channels)
	out := make([]float32, len(f64))
	for i, v := range f64 {
		out[i] = float32(v)
	}
	return out
}

// Normalizing a float32 buffer to a target must bring the re-measured loudness
// to that target (the round-trip test).
func TestNormalizeFloat32RoundTrip(t *testing.T) {
	pcm := sineFloat32(-10, 1000, 3, 48000, 2)
	opts := DefaultOptions()
	opts.SampleRate, opts.Channels = 48000, 2
	opts.TargetLUFS = -23

	res, err := NormalizeFloat32(pcm, opts)
	if err != nil {
		t.Fatal(err)
	}
	if res.PeakLimited {
		t.Errorf("did not expect peak limiting for a quiet sine")
	}
	got, err := MeasureFloat32(pcm, 48000, 2)
	if err != nil {
		t.Fatal(err)
	}
	if math.Abs(got.IntegratedLUFS-(-23)) > 0.1 {
		t.Errorf("after normalize, loudness = %.3f LUFS, want -23 +/-0.1", got.IntegratedLUFS)
	}
}

// The BirdNET-Go path: 48 kHz, 16-bit, mono.
func TestNormalizeInt16MonoRoundTrip(t *testing.T) {
	pcm := sineInt16(-10, 1000, 3, 48000)
	opts := DefaultOptions() // 48 kHz mono by default
	opts.TargetLUFS = -23

	res, err := NormalizeInt16(pcm, opts)
	if err != nil {
		t.Fatal(err)
	}
	// -10 dBFS mono is ~-13 LUFS; reaching -23 needs ~-10 dB of attenuation.
	if res.GainDB >= 0 {
		t.Errorf("expected attenuation (negative gain), got %.3f dB", res.GainDB)
	}
	got, err := MeasureInt16(pcm, 48000, 1)
	if err != nil {
		t.Fatal(err)
	}
	if math.Abs(got.IntegratedLUFS-(-23)) > 0.3 {
		t.Errorf("after normalize, loudness = %.3f LUFS, want -23 +/-0.3", got.IntegratedLUFS)
	}
}

// When reaching the target would exceed the true-peak ceiling, the gain is
// reduced so the output peak sits at the ceiling, and PeakLimited is reported.
func TestNormalizePeakLimited(t *testing.T) {
	pcm := sineFloat32(-10, 1000, 3, 48000, 2) // loudness ~-10 LUFS, TP ~-10 dBTP
	opts := DefaultOptions()
	opts.SampleRate, opts.Channels = 48000, 2
	opts.TargetLUFS = -3     // would need +7 dB -> projected TP ~-3 dBTP
	opts.TruePeakDBTP = -6.0 // ceiling forces limiting

	res, err := NormalizeFloat32(pcm, opts)
	if err != nil {
		t.Fatal(err)
	}
	if !res.PeakLimited {
		t.Fatalf("expected PeakLimited=true (ceiling -6 dBTP)")
	}
	got, _ := MeasureFloat32(pcm, 48000, 2)
	if got.TruePeakDBTP > -6.0+0.3 {
		t.Errorf("output true peak = %.3f dBTP, must not exceed ceiling -6 (+0.3 tol)", got.TruePeakDBTP)
	}
}

func TestNormalizeSilenceIsNoOp(t *testing.T) {
	pcm := make([]float32, 48000*2)
	opts := DefaultOptions()
	opts.SampleRate, opts.Channels = 48000, 2
	res, err := NormalizeFloat32(pcm, opts)
	if err != nil {
		t.Fatal(err)
	}
	if res.GainDB != 0 {
		t.Errorf("silence gain = %.3f, want 0 (no-op)", res.GainDB)
	}
	if !math.IsInf(res.OutputLUFS, -1) {
		t.Errorf("silence output loudness = %.3f, want -Inf", res.OutputLUFS)
	}
}

// Sample rates below the supported minimum must be rejected with an error, not
// panic and not produce garbage. 4000 Hz is in the danger zone where the
// K-weighting high-shelf center (1681.97 Hz) approaches/exceeds Nyquist.
func TestMeasureRejectsTooLowSampleRate(t *testing.T) {
	pcm := []int16{1, 2, 3, 4, 5, 6, 7, 8}
	for _, fs := range []int{4, 4000, 7999} {
		if _, err := MeasureInt16(pcm, fs, 1); err == nil {
			t.Errorf("expected error for %d Hz sample rate, got nil", fs)
		}
	}
	// The 8000 Hz minimum and a normal rate must be accepted.
	for _, fs := range []int{8000, 48000} {
		if _, err := MeasureInt16(pcm, fs, 1); err != nil {
			t.Errorf("%d Hz unexpectedly rejected: %v", fs, err)
		}
	}
}

func TestNormalizeValidation(t *testing.T) {
	opts := DefaultOptions()
	opts.SampleRate, opts.Channels = 48000, 2
	cases := map[string]func() error{
		"length not multiple of channels": func() error {
			_, err := NormalizeFloat32([]float32{0, 0, 0}, opts)
			return err
		},
		"zero sample rate": func() error {
			o := opts
			o.SampleRate = 0
			_, err := NormalizeFloat32([]float32{0, 0}, o)
			return err
		},
		"positive target": func() error {
			o := opts
			o.TargetLUFS = 5
			_, err := NormalizeFloat32([]float32{0, 0}, o)
			return err
		},
		"zero target (full-scale loudness footgun)": func() error {
			o := opts
			o.TargetLUFS = 0
			_, err := NormalizeFloat32([]float32{0, 0}, o)
			return err
		},
		"target below absolute gate": func() error {
			o := opts
			o.TargetLUFS = -80
			_, err := NormalizeFloat32([]float32{0, 0}, o)
			return err
		},
		"positive true-peak ceiling": func() error {
			o := opts
			o.TruePeakDBTP = 1
			_, err := NormalizeFloat32([]float32{0, 0}, o)
			return err
		},
		"sample rate too low": func() error {
			o := opts
			o.SampleRate = 4
			_, err := NormalizeFloat32([]float32{0, 0}, o)
			return err
		},
	}
	for name, fn := range cases {
		if err := fn(); err == nil {
			t.Errorf("%s: expected error, got nil", name)
		}
	}
}
