package audionorm

import (
	"encoding/binary"
	"math"
	"testing"
)

// int16sToLEBytes serializes interleaved int16 samples to little-endian bytes,
// the on-the-wire layout MeasureInt16Bytes / Meter.AddInt16Bytes decode.
func int16sToLEBytes(samples []int16) []byte {
	b := make([]byte, len(samples)*2)
	for i, s := range samples {
		binary.LittleEndian.PutUint16(b[i*2:], uint16(s)) //nolint:gosec // G115: signed->LE bit-write for test fixture
	}
	return b
}

// boundaryInt16 fills n samples by cycling through int16 edge values so the
// full-scale minimum (-32768) and maximum (32767) both appear. -32768 is the
// two's-complement boundary a signed-vs-unsigned decode bug corrupts most, and
// it drives the true-peak path (which keys on the maximum magnitude sample).
func boundaryInt16(n int) []int16 {
	edges := []int16{0, -1, 1, 32767, -32768, 1234, -1234}
	out := make([]int16, n)
	for i := range out {
		out[i] = edges[i%len(edges)]
	}
	return out
}

// MeasureInt16Bytes must produce exactly the same Measurement as MeasureInt16 on
// the same samples: it decodes the identical int16 values inline, so there is no
// float rounding difference to tolerate. BirdNET-Go audio is always mono; the
// cases cover a mono sine and the full-scale +/-boundary samples.
func TestMeasureInt16BytesMatchesInt16(t *testing.T) {
	t.Parallel()
	cases := []struct {
		name string
		pcm  []int16
	}{
		{"mono", sineInt16(-12, 1000, 1, 48000)},
		// 1 s (> the 400 ms gate) of cycling edge values, guaranteeing -32768 and
		// 32767 flow through both the k-weighting and true-peak passes.
		{"full-scale boundary", boundaryInt16(48000)},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			want, err := MeasureInt16(tc.pcm, 48000, 1)
			if err != nil {
				t.Fatal(err)
			}
			got, err := MeasureInt16Bytes(int16sToLEBytes(tc.pcm), 48000, 1)
			if err != nil {
				t.Fatal(err)
			}
			if got != want {
				t.Errorf("MeasureInt16Bytes = %+v, want %+v (must equal MeasureInt16)", got, want)
			}
		})
	}
}

// Regression guard for the signed cast: a Uint16-only decode would turn negative
// samples into large positives and inflate the loudness. A finite (>= 400 ms)
// sine has half its samples negative, so an unsigned misread would diverge from
// the []int16 reference path.
func TestMeasureInt16BytesSignedNegatives(t *testing.T) {
	t.Parallel()
	pcm := sineInt16(-6, 500, 1, 48000)
	want, err := MeasureInt16(pcm, 48000, 1)
	if err != nil {
		t.Fatal(err)
	}
	got, err := MeasureInt16Bytes(int16sToLEBytes(pcm), 48000, 1)
	if err != nil {
		t.Fatal(err)
	}
	if got != want {
		t.Errorf("signed decode mismatch: MeasureInt16Bytes = %+v, want %+v", got, want)
	}
}

// A trailing odd byte (never present in real int16 PCM) is dropped rather than
// panicking, so the result equals that of the even-truncated buffer.
func TestMeasureInt16BytesOddTrailingByte(t *testing.T) {
	t.Parallel()
	pcm := sineInt16(-12, 1000, 1, 48000)
	even := int16sToLEBytes(pcm)
	want, err := MeasureInt16Bytes(even, 48000, 1)
	if err != nil {
		t.Fatal(err)
	}
	got, err := MeasureInt16Bytes(append(even, 0x7f), 48000, 1) //nolint:gocritic // appendAssign: intentional odd-length copy for the test
	if err != nil {
		t.Fatal(err)
	}
	if got != want {
		t.Errorf("odd trailing byte changed result: got %+v, want %+v", got, want)
	}
}

// Empty/sub-frame input measures as silence (IntegratedLUFS == -Inf) without
// erroring or panicking: validateDims accepts n == 0, and AddInt16Bytes
// early-returns on zero frames.
func TestMeasureInt16BytesEmpty(t *testing.T) {
	t.Parallel()
	cases := []struct {
		name string
		b    []byte
	}{
		{"nil", nil},
		{"empty", []byte{}},
		{"single odd byte", []byte{0x7f}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			got, err := MeasureInt16Bytes(tc.b, 48000, 1)
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if !math.IsInf(got.IntegratedLUFS, -1) {
				t.Errorf("empty input: IntegratedLUFS = %v, want -Inf (silence)", got.IntegratedLUFS)
			}
		})
	}
}

func TestClampGainDB(t *testing.T) {
	t.Parallel()
	const maxAbs = DefaultMaxGainDB
	cases := []struct {
		name        string
		in          float64
		maxAbs      float64
		wantVal     float64
		wantLimited bool
	}{
		{"within range", 12.5, maxAbs, 12.5, false},
		{"at positive ceiling", maxAbs, maxAbs, maxAbs, false},
		{"above positive ceiling", maxAbs + 5, maxAbs, maxAbs, true},
		{"at negative ceiling", -maxAbs, maxAbs, -maxAbs, false},
		{"below negative ceiling", -maxAbs - 5, maxAbs, -maxAbs, true},
		{"zero", 0, maxAbs, 0, false},
		// A negative ceiling is treated as its magnitude, so the range stays
		// symmetric rather than clamping every value to a negative bound.
		{"negative ceiling, over", 50, -maxAbs, maxAbs, true},
		{"negative ceiling, under", -50, -maxAbs, -maxAbs, true},
		{"negative ceiling, within", 10, -maxAbs, 10, false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			got, limited := ClampGainDB(tc.in, tc.maxAbs)
			if got != tc.wantVal || limited != tc.wantLimited {
				t.Errorf("ClampGainDB(%v, %v) = (%v, %v), want (%v, %v)",
					tc.in, tc.maxAbs, got, limited, tc.wantVal, tc.wantLimited)
			}
		})
	}
}
