package vad

import (
	"encoding/binary"
	"fmt"
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
		t.Run(fmt.Sprintf("seqlen=%d", n), func(t *testing.T) {
			frames := make([]float32, n*modelInputSamples)
			probs, hOut, cOut, err := s.Run(frames, h, c)
			require.NoError(t, err)
			require.Len(t, probs, n)
			require.Len(t, hOut, stateWidth)
			require.Len(t, cOut, stateWidth)
			for i, p := range probs {
				assert.GreaterOrEqual(t, p, float32(0), "prob %d in range", i)
				assert.LessOrEqual(t, p, float32(1), "prob %d in range", i)
				assert.Less(t, p, float32(0.5), "silence hop %d must not look like speech", i)
			}
		})
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
	assert.LessOrEqual(t, prob, float32(1))
	assert.GreaterOrEqual(t, prob, float32(parityThreshold),
		"speech-like input fed through the 48 kHz resampling path must reach the gate threshold")
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

// TestVAD_StreamingCoverageParity is the privacy gate for the streaming change.
// On a real speech clip it verifies two properties against the old behaviour of
// independently scoring full 3 s chunks: (1) peak parity, the streaming path
// reaches at least the same peak confidence and scores sustained real speech
// high; and (2) coverage parity, the streaming path spends at least as large a
// fraction of the clip above the gate threshold as the full-chunk path, so it
// does not drop speech the old path caught. A recall loss on either MUST NOT
// ship. (Per-window time-aligned correspondence over a labelled corpus is the
// dedicated birdsong/speech FP harness, tracked separately.)
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

	cfg := Config{ModelPath: modelPath, LibraryPath: libPath}
	baseProbs := fullChunkProbs(t, cfg, pcm)
	streamProbs := streamingProbs(t, cfg, pcm)
	baseMax := maxOf(baseProbs)
	streamMax := maxOf(streamProbs)
	baseCover := fractionAtOrAbove(baseProbs, parityThreshold)
	streamCover := fractionAtOrAbove(streamProbs, parityThreshold)
	t.Logf("full-chunk max=%.4f cover=%.3f | streaming max=%.4f cover=%.3f",
		baseMax, baseCover, streamMax, streamCover)

	require.GreaterOrEqual(t, baseMax, float32(parityThreshold),
		"sanity: the full-chunk path must detect speech in the speech fixture")
	require.Positive(t, baseCover, "sanity: the full-chunk path must flag some of the clip")
	// Peak parity.
	assert.GreaterOrEqual(t, streamMax, float32(parityThreshold),
		"PRIVACY REGRESSION: streaming must reach the gate threshold wherever the full-chunk path does")
	assert.Greater(t, streamMax, float32(0.8),
		"streaming should score sustained real speech high, not just above threshold")
	// Coverage parity: streaming must flag at least as much of the clip as the
	// full-chunk path (no net recall loss).
	assert.GreaterOrEqual(t, streamCover, baseCover,
		"PRIVACY REGRESSION: streaming flags less of the clip than the full-chunk path")
}

// TestVAD_StreamingNoiseFPParity: speech-free noise must not trip the gate on
// either path (the streaming rework must not add false positives either).
func TestVAD_StreamingNoiseFPParity(t *testing.T) {
	modelPath, libPath := modelPathFromEnv(t)
	pcm := noisePCM16(10, sampleRate16k, 42)

	cfg := Config{ModelPath: modelPath, LibraryPath: libPath}
	baseMax := maxOf(fullChunkProbs(t, cfg, pcm))
	streamMax := maxOf(streamingProbs(t, cfg, pcm))
	t.Logf("noise: full-chunk max=%.4f streaming max=%.4f", baseMax, streamMax)

	assert.Less(t, baseMax, float32(parityThreshold), "noise must not trip the full-chunk path")
	assert.Less(t, streamMax, float32(parityThreshold), "noise must not trip the streaming path")
}

// maxOf returns the maximum of probs (0 for empty).
func maxOf(probs []float32) float32 {
	var m float32
	for _, p := range probs {
		m = max(m, p)
	}
	return m
}

// fractionAtOrAbove returns the fraction of probs at or above threshold (0 for empty).
func fractionAtOrAbove(probs []float32, threshold float64) float64 {
	if len(probs) == 0 {
		return 0
	}
	n := 0
	for _, p := range probs {
		if float64(p) >= threshold {
			n++
		}
	}
	return float64(n) / float64(len(probs))
}

// fullChunkProbs scores pcm as independent full 3 s chunks every 0.5 s (the old
// gate behaviour) and returns every per-chunk aggregate probability.
func fullChunkProbs(t *testing.T, cfg Config, pcm []byte) []float32 {
	t.Helper()
	d, err := New(cfg)
	require.NoError(t, err)
	t.Cleanup(func() { assert.NoError(t, d.Close()) })

	probs := make([]float32, 0, len(pcm)/parityStepBytes+1)
	for off := 0; off+parityChunkBytes <= len(pcm); off += parityStepBytes {
		p, err := d.SpeechProbability(pcm[off:off+parityChunkBytes], sampleRate16k)
		require.NoError(t, err)
		probs = append(probs, p)
	}
	return probs
}

// streamingProbs feeds pcm through a Streamer exactly as the gate does: 0.5 s of
// fresh audio per step, a Flush every parityFlushEvery steps against one shared
// session, and returns every per-flush aggregate probability.
func streamingProbs(t *testing.T, cfg Config, pcm []byte) []float32 {
	t.Helper()
	sess, err := NewSession(cfg)
	require.NoError(t, err)
	t.Cleanup(func() { assert.NoError(t, sess.Close()) })
	st := NewStreamer(0)

	probs := make([]float32, 0, len(pcm)/parityStepBytes/parityFlushEvery+1)
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
			probs = append(probs, p)
		}
	}
	return probs
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

// TestVAD_EmbeddedStreamingParity is a CI-runnable check that needs no external
// fixture: it drives the embedded sequence model with a synthetic voiced signal
// (whose syllable modulation the model scores as speech) and asserts that the
// streaming path detects the same speech peak as the full-chunk path on real
// ONNX Runtime. It skips on a noembed build or when ONNX Runtime is unavailable,
// so it exercises the real streamer+model tensor plumbing wherever CI has ORT
// linked while the stub-session unit tests pin the bookkeeping everywhere else.
// The stronger time-resolved coverage parity lives in TestVAD_StreamingCoverageParity
// (real speech clip, run locally), since a synthetic signal's amplitude envelope
// makes a per-decision coverage fraction cadence-sensitive rather than meaningful.
func TestVAD_EmbeddedStreamingParity(t *testing.T) {
	if !HasEmbeddedModel() {
		t.Skip("no embedded model in this build")
	}
	cfg := Config{ModelData: EmbeddedModelData(), LibraryPath: os.Getenv("VAD_TEST_ORT_LIB")}
	probe, err := New(cfg)
	if err != nil {
		t.Skipf("ONNX Runtime unavailable; skipping embedded streaming parity: %v", err)
	}
	require.NoError(t, probe.Close())

	pcm := speechLikePCM16(6, sampleRate16k)
	baseMax := maxOf(fullChunkProbs(t, cfg, pcm))
	streamMax := maxOf(streamingProbs(t, cfg, pcm))
	t.Logf("embedded: full-chunk max=%.4f streaming max=%.4f", baseMax, streamMax)

	require.GreaterOrEqual(t, baseMax, float32(parityThreshold),
		"the synthetic voiced signal must register as speech on the embedded model")
	assert.GreaterOrEqual(t, streamMax, float32(parityThreshold),
		"PRIVACY REGRESSION: streaming must detect the speech the full-chunk path detects")
	assert.InDelta(t, baseMax, streamMax, 0.15,
		"streaming must reach a comparable peak confidence to the full-chunk path")
}
