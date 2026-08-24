package vad

import (
	"encoding/binary"
	"math"
	"math/rand/v2"
	"os"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/tphakala/birdnet-go/internal/inference"
)

// These tests exercise the real Silero VAD sequence ONNX model through ONNX
// Runtime. They are skipped unless VAD_TEST_MODEL points at an installed
// sequence-export silero .onnx file (inputs input/h/c, outputs
// speech_probs/hn/cn; the stock upstream frame model will NOT load).
// VAD_TEST_ORT_LIB may optionally override the ONNX Runtime library path.
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

// TestVAD_SequenceSessionIO exercises the raw session contract: a stacked
// [n, 576] silence input with zeroed h/c must yield n probabilities and
// stateWidth-sized carry states, at two different sequence lengths (the
// sequence dimension is dynamic per Run).
func TestVAD_SequenceSessionIO(t *testing.T) {
	modelPath, libPath := modelPathFromEnv(t)
	require.NoError(t, inference.InitONNXRuntime(libPath))

	s, err := newSession(modelPath, nil, nil)
	require.NoError(t, err)
	t.Cleanup(func() { assert.NoError(t, s.Close()) })

	h := make([]float32, stateWidth)
	c := make([]float32, stateWidth)
	for _, n := range []int{5, 94} {
		frames := make([]float32, n*modelInputSamples)
		probs, hOut, cOut, err := s.Run(frames, h, c)
		require.NoError(t, err, "n=%d", n)
		require.Len(t, probs, n)
		require.Len(t, hOut, stateWidth)
		require.Len(t, cOut, stateWidth)
		for i, p := range probs {
			assert.GreaterOrEqual(t, p, float32(0), "prob %d in range", i)
			assert.LessOrEqual(t, p, float32(1), "prob %d in range", i)
			assert.Less(t, p, float32(0.5), "silence hop %d must not look like speech", i)
		}
	}
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

// TestVAD_SpeechLikeResponds asserts the model reacts to structured voiced
// content fed through the resampling path (48 kHz input), proving the tensor
// plumbing carries real signal, not just zeros.
func TestVAD_SpeechLikeResponds(t *testing.T) {
	modelPath, libPath := modelPathFromEnv(t)

	d, err := New(Config{ModelPath: modelPath, LibraryPath: libPath})
	require.NoError(t, err)
	t.Cleanup(func() { assert.NoError(t, d.Close()) })
	assert.Equal(t, StrategySequence, d.Strategy())

	prob, err := d.SpeechProbability(speechLikePCM16(3, 48000), 48000)
	require.NoError(t, err)
	t.Logf("speech-like probability = %.4f", prob)
	assert.GreaterOrEqual(t, prob, float32(0))
	assert.LessOrEqual(t, prob, float32(1))
}

// TestVAD_RealSpeechIsHigh is a positive control: it feeds a real 16 kHz mono
// speech clip (raw PCM16) and asserts the sequence path scores it high. Set
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

	d, err := New(Config{ModelPath: modelPath, LibraryPath: libPath})
	require.NoError(t, err)
	t.Cleanup(func() { assert.NoError(t, d.Close()) })

	prob, err := d.SpeechProbability(pcm, sampleRate16k)
	require.NoError(t, err)
	t.Logf("sequence speech probability = %.4f", prob)
	assert.Greater(t, prob, float32(0.8), "real speech should score high")
}

func BenchmarkVADSequence3s(b *testing.B) { benchSequence(b, 3) }
func BenchmarkVADSequence5s(b *testing.B) { benchSequence(b, 5) }

func benchSequence(b *testing.B, durationSec int) {
	b.Helper()
	modelPath, libPath := modelPathFromEnv(b)
	pcm := speechLikePCM16(durationSec, 48000)

	d, err := New(Config{ModelPath: modelPath, LibraryPath: libPath})
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

// Parity-test constants: the production default gate threshold and the gate's
// chunk/step geometry (3 s analysis chunks arriving every 0.5 s, flushed on a
// 1 s cadence).
const (
	parityThreshold  = 0.35
	parityChunkBytes = 3 * sampleRate16k * bytesPerSample
	parityStepBytes  = sampleRate16k * bytesPerSample / 2 // 0.5 s
	parityFlushEvery = 2                                  // flush every 2nd step = 1 s cadence
)

// noisePCM16 returns durationSec seconds of deterministic white noise at
// sampleRate as PCM16 bytes (seeded PRNG, modest amplitude), a stand-in for
// speech-free environmental audio.
func noisePCM16(durationSec, sampleRate int, seed uint64) []byte {
	rng := rand.New(rand.NewPCG(seed, seed)) //nolint:gosec // G404: deterministic test signal, not crypto
	n := durationSec * sampleRate
	buf := make([]byte, n*bytesPerSample)
	for i := range n {
		v := (rng.Float64()*2 - 1) * 0.2 * pcm16Scale
		s := int16(v)                                                    //nolint:gosec // G115: bounded synthetic signal
		binary.LittleEndian.PutUint16(buf[i*bytesPerSample:], uint16(s)) //nolint:gosec // G115: PCM16 encode
	}
	return buf
}

// TestVAD_StreamingCoverageParity is the privacy gate for the streaming change:
// on a real speech clip, the streaming path (fresh 0.5 s slices, 1 s flush
// cadence, state carried) must detect speech at least as strongly as the old
// behaviour of independently scoring full 3 s chunks. A recall loss here means
// the optimisation weakened the privacy filter and MUST NOT ship.
func TestVAD_StreamingCoverageParity(t *testing.T) {
	modelPath, libPath := modelPathFromEnv(t)
	rawPath := os.Getenv("VAD_TEST_SPEECH_RAW")
	if rawPath == "" {
		t.Skip("VAD_TEST_SPEECH_RAW not set; skipping streaming coverage parity")
	}
	pcm, err := os.ReadFile(rawPath) //nolint:gosec // G304: test fixture path from env
	require.NoError(t, err)
	if len(pcm) < parityChunkBytes {
		t.Skipf("speech fixture too short: %d bytes, need %d", len(pcm), parityChunkBytes)
	}

	baseMax := maxFullChunkProb(t, modelPath, libPath, pcm)
	streamMax := maxStreamingProb(t, modelPath, libPath, pcm)
	t.Logf("full-chunk max=%.4f streaming max=%.4f", baseMax, streamMax)

	require.GreaterOrEqual(t, baseMax, float32(parityThreshold),
		"sanity: the full-chunk path must detect speech in the speech fixture")
	assert.GreaterOrEqual(t, streamMax, float32(parityThreshold),
		"PRIVACY REGRESSION: streaming must detect speech wherever the full-chunk path does")
	assert.Greater(t, streamMax, float32(0.8),
		"streaming should score sustained real speech high, not just above threshold")
}

// TestVAD_StreamingNoiseFPParity: speech-free noise must not trip the gate on
// either path (the streaming rework must not add false positives either).
func TestVAD_StreamingNoiseFPParity(t *testing.T) {
	modelPath, libPath := modelPathFromEnv(t)
	pcm := noisePCM16(10, sampleRate16k, 42)

	baseMax := maxFullChunkProb(t, modelPath, libPath, pcm)
	streamMax := maxStreamingProb(t, modelPath, libPath, pcm)
	t.Logf("noise: full-chunk max=%.4f streaming max=%.4f", baseMax, streamMax)

	assert.Less(t, baseMax, float32(parityThreshold), "noise must not trip the full-chunk path")
	assert.Less(t, streamMax, float32(parityThreshold), "noise must not trip the streaming path")
}

// maxFullChunkProb scores pcm as independent full 3 s chunks every 0.5 s (the
// old gate behaviour) and returns the maximum aggregate probability.
func maxFullChunkProb(t *testing.T, modelPath, libPath string, pcm []byte) float32 {
	t.Helper()
	d, err := New(Config{ModelPath: modelPath, LibraryPath: libPath})
	require.NoError(t, err)
	t.Cleanup(func() { assert.NoError(t, d.Close()) })

	var maxProb float32
	for off := 0; off+parityChunkBytes <= len(pcm); off += parityStepBytes {
		p, err := d.SpeechProbability(pcm[off:off+parityChunkBytes], sampleRate16k)
		require.NoError(t, err)
		maxProb = max(maxProb, p)
	}
	return maxProb
}

// maxStreamingProb feeds pcm through a Streamer exactly as the gate does: 0.5 s
// of fresh audio per step, a Flush every parityFlushEvery steps against one
// shared session, and returns the maximum aggregate probability seen.
func maxStreamingProb(t *testing.T, modelPath, libPath string, pcm []byte) float32 {
	t.Helper()
	sess, err := NewSession(Config{ModelPath: modelPath, LibraryPath: libPath})
	require.NoError(t, err)
	t.Cleanup(func() { assert.NoError(t, sess.Close()) })
	st := NewStreamer(0)

	var maxProb float32
	steps := 0
	for off := 0; off+parityStepBytes <= len(pcm); off += parityStepBytes {
		require.NoError(t, st.Append(pcm[off:off+parityStepBytes], sampleRate16k))
		steps++
		if steps%parityFlushEvery != 0 {
			continue
		}
		p, ok, _, err := st.Flush(sess)
		require.NoError(t, err)
		if ok {
			maxProb = max(maxProb, p)
		}
	}
	return maxProb
}

// BenchmarkVADStreamer1s measures the steady-state streaming cost of one second
// of fresh audio per Flush (the production cadence), for comparison against
// BenchmarkVADSequence3s (the full-chunk cost it replaces).
func BenchmarkVADStreamer1s(b *testing.B) {
	modelPath, libPath := modelPathFromEnv(b)
	pcm := speechLikePCM16(1, 48000)

	sess, err := NewSession(Config{ModelPath: modelPath, LibraryPath: libPath})
	require.NoError(b, err)
	b.Cleanup(func() { assert.NoError(b, sess.Close()) })
	st := NewStreamer(0)

	// Warm up.
	require.NoError(b, st.Append(pcm, 48000))
	_, _, _, err = st.Flush(sess)
	require.NoError(b, err)

	b.ReportAllocs()
	b.ResetTimer()
	for b.Loop() {
		if err := st.Append(pcm, 48000); err != nil {
			b.Fatal(err)
		}
		if _, _, _, err := st.Flush(sess); err != nil {
			b.Fatal(err)
		}
	}
}
