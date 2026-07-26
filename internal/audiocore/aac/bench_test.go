package aac

import (
	"path/filepath"
	"testing"
)

// BenchmarkEncodePCM measures a realistic detection clip: 15 seconds of mono
// 48 kHz 16-bit PCM at the default 96 kbps. The result matters for the rollout
// decision, because clip export latency competes with the audio-serving retry
// window on slow hardware. Compare it against wall-clock 15 s to read the
// realtime factor.
func BenchmarkEncodePCM(b *testing.B) {
	pcm := benchTonePCM(15.0)
	dir := b.TempDir()
	out := filepath.Join(dir, "bench.m4a")
	ctx := b.Context()

	b.SetBytes(int64(len(pcm)))
	b.ReportAllocs()

	for b.Loop() {
		if err := EncodePCM(ctx, &Options{
			PCMData:     pcm,
			OutputPath:  out,
			SampleRate:  testSampleRate,
			Channels:    1,
			BitDepth:    16,
			BitrateKbps: testBitrate,
		}); err != nil {
			b.Fatal(err)
		}
	}
}

// BenchmarkEncodePCMWithGain adds the gain pass, which copies the clip once
// before encoding. The delta against BenchmarkEncodePCM is the cost of that
// copy.
func BenchmarkEncodePCMWithGain(b *testing.B) {
	pcm := benchTonePCM(15.0)
	out := filepath.Join(b.TempDir(), "bench.m4a")
	ctx := b.Context()

	b.SetBytes(int64(len(pcm)))
	b.ReportAllocs()

	for b.Loop() {
		if err := EncodePCM(ctx, &Options{
			PCMData:     pcm,
			OutputPath:  out,
			SampleRate:  testSampleRate,
			Channels:    1,
			BitDepth:    16,
			BitrateKbps: testBitrate,
			GainDB:      -6,
		}); err != nil {
			b.Fatal(err)
		}
	}
}

// benchTonePCM mirrors tonePCM without the *testing.T dependency.
func benchTonePCM(seconds float64) []byte {
	n := int(float64(testSampleRate) * seconds)
	b := make([]byte, n*2)
	for i := range n {
		// A simple ramp is enough to keep the coder busy; exact content does not
		// change the cost profile meaningfully.
		v := int16((i % 8000) - 4000)
		b[i*2] = byte(v)
		b[i*2+1] = byte(v >> 8)
	}
	return b
}
