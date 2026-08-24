package vad

import (
	"encoding/binary"
	"math"
	"os"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// These tests exercise the real Silero VAD ONNX model through ONNX Runtime.
// They are skipped unless VAD_TEST_MODEL points at an installed silero .onnx
// file. VAD_TEST_ORT_LIB may optionally override the ONNX Runtime library path.
//
//	VAD_TEST_MODEL=/path/to/silero_vad.onnx go test -run TestVAD ./internal/inference/vad/
//	VAD_TEST_MODEL=/path/to/silero_vad.onnx go test -bench VAD -benchmem ./internal/inference/vad/

func modelPathFromEnv(tb testing.TB) (modelPath, libPath string) {
	tb.Helper()
	modelPath = os.Getenv("VAD_TEST_MODEL")
	if modelPath == "" {
		tb.Skip("VAD_TEST_MODEL not set; skipping real-model VAD test")
	}
	return modelPath, os.Getenv("VAD_TEST_ORT_LIB")
}

// silence16k returns durationSec seconds of 16 kHz silence as PCM16 bytes.
func silence16k(durationSec int) []byte {
	return make([]byte, durationSec*sampleRate16k*bytesPerSample)
}

// speechLikePCM16 returns a crude voiced-speech-like signal: a ~120 Hz glottal
// fundamental with formant-ish harmonics, amplitude-modulated at a syllable
// rate. It is not real speech, but it is far from silence and lets a smoke test
// assert the model responds to structured, voiced content.
func speechLikePCM16(durationSec, sampleRate int) []byte {
	n := durationSec * sampleRate
	buf := make([]byte, n*bytesPerSample)
	for i := range n {
		t := float64(i) / float64(sampleRate)
		env := 0.5 * (1 + math.Sin(2*math.Pi*4*t)) // 4 Hz syllable envelope
		sig := math.Sin(2*math.Pi*120*t) + 0.5*math.Sin(2*math.Pi*240*t) + 0.3*math.Sin(2*math.Pi*600*t)
		v := env * sig / 1.8 * 0.6 * pcm16Scale
		s := int16(v)                                                    //nolint:gosec // G115: bounded synthetic signal
		binary.LittleEndian.PutUint16(buf[i*bytesPerSample:], uint16(s)) //nolint:gosec // G115: PCM16 encode
	}
	return buf
}

// TestVAD_EmbeddedModelLoads exercises the production default path: loading the
// model embedded in the binary via the in-memory bytes API. It skips when the
// build has no embedded model (-tags noembed) or when ONNX Runtime is not
// available on the host. VAD_TEST_ORT_LIB may point at the ORT shared library.
func TestVAD_EmbeddedModelLoads(t *testing.T) {
	if !HasEmbeddedModel() {
		t.Skip("no embedded model in this build")
	}
	d, err := New(Config{ModelData: EmbeddedModelData(), LibraryPath: os.Getenv("VAD_TEST_ORT_LIB")})
	if err != nil {
		t.Skipf("ONNX Runtime unavailable; skipping embedded-model test: %v", err)
	}
	t.Cleanup(func() { assert.NoError(t, d.Close()) })

	prob, err := d.SpeechProbability(silence16k(1), sampleRate16k)
	require.NoError(t, err)
	assert.Less(t, prob, float32(0.2), "silence should score low, got %v", prob)
}

func TestVAD_SilenceIsLow(t *testing.T) {
	modelPath, libPath := modelPathFromEnv(t)

	d, err := New(Config{ModelPath: modelPath, LibraryPath: libPath})
	require.NoError(t, err)
	t.Cleanup(func() { assert.NoError(t, d.Close()) })

	prob, err := d.SpeechProbability(silence16k(3), sampleRate16k)
	require.NoError(t, err)
	assert.Less(t, prob, float32(0.2), "silence should score low, got %v", prob)
}

func TestVAD_StrategiesRunAndAgree(t *testing.T) {
	modelPath, libPath := modelPathFromEnv(t)

	pcm := speechLikePCM16(3, 48000) // native 48 kHz chunk, resampled internally

	recur, err := New(Config{ModelPath: modelPath, LibraryPath: libPath, Strategy: StrategyRecurrent})
	require.NoError(t, err)
	t.Cleanup(func() { assert.NoError(t, recur.Close()) })
	assert.Equal(t, "recurrent", recur.Strategy())

	seg, err := New(Config{ModelPath: modelPath, LibraryPath: libPath, Strategy: StrategySegmentBatched})
	require.NoError(t, err)
	t.Cleanup(func() { assert.NoError(t, seg.Close()) })
	assert.Equal(t, "segment-batched", seg.Strategy())

	pRecur, err := recur.SpeechProbability(pcm, 48000)
	require.NoError(t, err)
	pSeg, err := seg.SpeechProbability(pcm, 48000)
	require.NoError(t, err)

	t.Logf("recurrent=%.4f segment-batched=%.4f", pRecur, pSeg)
	// Both must produce valid probabilities.
	assert.GreaterOrEqual(t, pRecur, float32(0))
	assert.LessOrEqual(t, pRecur, float32(1))
	assert.GreaterOrEqual(t, pSeg, float32(0))
	assert.LessOrEqual(t, pSeg, float32(1))
	// And they must broadly agree on a signal with content spanning the whole
	// chunk (segment-batched only diverges at seams / short bursts, absent here).
	assert.InDelta(t, pRecur, pSeg, 0.2, "recurrent and segment-batched should broadly agree on a full-chunk signal")
}

// TestVAD_RealSpeechIsHigh is a positive control: it feeds a real 16 kHz mono
// speech clip (raw PCM16) and asserts both strategies score it high. Set
// VAD_TEST_SPEECH_RAW to a raw PCM16 mono 16 kHz file to enable it. This proves
// the tensor plumbing produces meaningful probabilities, not just near-zero
// output that would also pass the silence test.
func TestVAD_RealSpeechIsHigh(t *testing.T) {
	modelPath, libPath := modelPathFromEnv(t)
	rawPath := os.Getenv("VAD_TEST_SPEECH_RAW")
	if rawPath == "" {
		t.Skip("VAD_TEST_SPEECH_RAW not set; skipping real-speech positive control")
	}
	pcm, err := os.ReadFile(rawPath) //nolint:gosec // G304: test fixture path from env
	require.NoError(t, err)

	for _, kind := range []struct {
		name string
		k    StrategyKind
	}{{"recurrent", StrategyRecurrent}, {"segment-batched", StrategySegmentBatched}} {
		t.Run(kind.name, func(t *testing.T) {
			d, err := New(Config{ModelPath: modelPath, LibraryPath: libPath, Strategy: kind.k})
			require.NoError(t, err)
			t.Cleanup(func() { assert.NoError(t, d.Close()) })

			prob, err := d.SpeechProbability(pcm, sampleRate16k)
			require.NoError(t, err)
			t.Logf("%s speech probability = %.4f", kind.name, prob)
			assert.Greater(t, prob, float32(0.8), "real speech should score high")
		})
	}
}

func benchStrategy(b *testing.B, kind StrategyKind, durationSec int) {
	b.Helper()
	modelPath, libPath := modelPathFromEnv(b)
	pcm := speechLikePCM16(durationSec, 48000)

	d, err := New(Config{ModelPath: modelPath, LibraryPath: libPath, Strategy: kind})
	require.NoError(b, err)
	b.Cleanup(func() { assert.NoError(b, d.Close()) })

	// Warm up (first call also pays resampler/warmup costs).
	_, err = d.SpeechProbability(pcm, 48000)
	require.NoError(b, err)

	b.ReportAllocs()
	b.ResetTimer()
	for b.Loop() {
		if _, err := d.SpeechProbability(pcm, 48000); err != nil {
			b.Fatal(err)
		}
	}
}

func BenchmarkVADRecurrent3s(b *testing.B)      { benchStrategy(b, StrategyRecurrent, 3) }
func BenchmarkVADSegmentBatched3s(b *testing.B) { benchStrategy(b, StrategySegmentBatched, 3) }
func BenchmarkVADRecurrent5s(b *testing.B)      { benchStrategy(b, StrategyRecurrent, 5) }
func BenchmarkVADSegmentBatched5s(b *testing.B) { benchStrategy(b, StrategySegmentBatched, 5) }
