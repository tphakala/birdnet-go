package api

import (
	"context"
	"encoding/binary"
	"errors"
	"math"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/tphakala/birdnet-go/internal/classifier"
	"github.com/tphakala/birdnet-go/internal/datastore"
)

// TestReanalyzeSamples_TopNAggregation verifies the window-walking aggregator
// keeps the max confidence per species across windows and returns the per-model
// scores sorted descending. Uses an injected predict function so the test
// stays decoupled from the orchestrator (which would require a real model
// on disk).
func TestReanalyzeSamples_TopNAggregation(t *testing.T) {
	t.Parallel()

	spec := classifier.ModelSpec{
		SampleRate: 48000,
		ClipLength: 3 * time.Second,
	}
	clipLen := spec.SampleRate * int(spec.ClipLength.Seconds())
	stride := clipLen / 2
	// 4 full windows of input: stride positions 0, 1.5s, 3s, 4.5s; the 6s
	// position only reaches sample index 6*48000 = 288000 with clipLen=144000,
	// so the loop covers exactly 3 windows. We size samples to deliberately
	// span 3 windows so the per-window stub can return varied scores.
	samples := make([]float32, clipLen+2*stride) // 3 windows
	for i := range samples {
		// Non-zero data so an alert future stub that inspects content has signal.
		samples[i] = float32(math.Sin(float64(i) / 100))
	}

	calls := 0
	stub := func(_ context.Context, _ string, _ [][]float32) ([]datastore.Results, error) {
		calls++
		switch calls {
		case 1:
			// Window 1: robin is the winner.
			return []datastore.Results{
				{Species: "Erithacus rubecula_Robin", Confidence: 0.42},
				{Species: "Turdus merula_Blackbird", Confidence: 0.10},
			}, nil
		case 2:
			// Window 2: blackbird overtakes; robin lower.
			return []datastore.Results{
				{Species: "Turdus merula_Blackbird", Confidence: 0.85},
				{Species: "Erithacus rubecula_Robin", Confidence: 0.30},
			}, nil
		case 3:
			// Window 3: a third species appears with the highest confidence
			// overall, and robin's max stays at 0.42 from window 1.
			return []datastore.Results{
				{Species: "Parus major_Great Tit", Confidence: 0.91},
				{Species: "Erithacus rubecula_Robin", Confidence: 0.05},
			}, nil
		default:
			return nil, errors.New("unexpected call count")
		}
	}

	preds, windowCount, err := reanalyzeSamples(t.Context(), stub, "BirdNET_V2.4", spec, samples)
	require.NoError(t, err)
	assert.Equal(t, 3, windowCount, "should run exactly 3 windows for this sample length")
	require.Len(t, preds, 3)

	// Sorted descending by confidence; max-per-species retained across windows.
	// SplitSpeciesName turns "Scientific_Common" labels (BirdNET shape) into
	// separate scientific + common fields on the response.
	assert.Equal(t, "Parus major", preds[0].ScientificName)
	assert.Equal(t, "Great Tit", preds[0].CommonName)
	assert.InDelta(t, 0.91, preds[0].Confidence, 1e-6)
	assert.Equal(t, "Turdus merula", preds[1].ScientificName)
	assert.Equal(t, "Blackbird", preds[1].CommonName)
	assert.InDelta(t, 0.85, preds[1].Confidence, 1e-6)
	assert.Equal(t, "Erithacus rubecula", preds[2].ScientificName)
	assert.Equal(t, "Robin", preds[2].CommonName)
	assert.InDelta(t, 0.42, preds[2].Confidence, 1e-6,
		"robin's max confidence must come from window 1, not window 3")
}

// TestReanalyzeSamples_PerchStyleBareScientific verifies that bare binomial
// labels (Perch v2's shape: "Genus species" with no common-name suffix) are
// classified as scientific names with an empty CommonName, so the handler
// knows to fill it via the resolver chain.
func TestReanalyzeSamples_PerchStyleBareScientific(t *testing.T) {
	t.Parallel()

	spec := classifier.ModelSpec{SampleRate: 32000, ClipLength: 5 * time.Second}
	clipLen := spec.SampleRate * int(spec.ClipLength.Seconds())
	samples := make([]float32, clipLen)

	stub := func(_ context.Context, _ string, _ [][]float32) ([]datastore.Results, error) {
		return []datastore.Results{
			{Species: "Coccothraustes coccothraustes", Confidence: 0.94}, // Perch shape
		}, nil
	}

	preds, _, err := reanalyzeSamples(t.Context(), stub, "Perch_V2", spec, samples)
	require.NoError(t, err)
	require.Len(t, preds, 1)
	assert.Equal(t, "Coccothraustes coccothraustes", preds[0].ScientificName)
	assert.Empty(t, preds[0].CommonName,
		"Perch-style bare scientific must leave CommonName empty so the handler can resolve it from the locale-aware resolver chain")
}

// TestReanalyzeSamples_ShortClipPadded verifies that audio shorter than one
// model window gets zero-padded to one full window, so we still emit at least
// one inference call instead of silently returning no results.
func TestReanalyzeSamples_ShortClipPadded(t *testing.T) {
	t.Parallel()

	spec := classifier.ModelSpec{SampleRate: 48000, ClipLength: 3 * time.Second}
	tinySamples := make([]float32, 1000) // way under one window

	var capturedWindowLen int
	stub := func(_ context.Context, _ string, sample [][]float32) ([]datastore.Results, error) {
		require.Len(t, sample, 1, "stub should receive one channel")
		capturedWindowLen = len(sample[0])
		return []datastore.Results{{Species: "test", Confidence: 0.5}}, nil
	}

	preds, windowCount, err := reanalyzeSamples(t.Context(), stub, "BirdNET_V2.4", spec, tinySamples)
	require.NoError(t, err)
	assert.Equal(t, 1, windowCount)
	assert.Equal(t, spec.SampleRate*int(spec.ClipLength.Seconds()), capturedWindowLen,
		"window passed to predict must be exactly one clip-length after padding")
	require.Len(t, preds, 1)
}

// TestReanalyzeSamples_PredictError surfaces an inference error to the caller
// instead of silently returning partial results.
func TestReanalyzeSamples_PredictError(t *testing.T) {
	t.Parallel()

	spec := classifier.ModelSpec{SampleRate: 48000, ClipLength: 3 * time.Second}
	clipLen := spec.SampleRate * int(spec.ClipLength.Seconds())
	samples := make([]float32, clipLen) // one window

	wantErr := errors.New("model boom")
	stub := func(_ context.Context, _ string, _ [][]float32) ([]datastore.Results, error) {
		return nil, wantErr
	}

	_, _, err := reanalyzeSamples(t.Context(), stub, "BirdNET_V2.4", spec, samples)
	require.Error(t, err)
	assert.ErrorIs(t, err, wantErr)
}

// TestReanalyzeSamples_DoesNotTruncate verifies that per-model aggregation
// returns every species the model produced, sorted by confidence descending.
// Top-N truncation is a concern of the multi-model aggregator (it must be
// applied AFTER merging across all models), not this per-model layer —
// otherwise a "strong-on-one-model only" species could be silently dropped
// before merge.
func TestReanalyzeSamples_DoesNotTruncate(t *testing.T) {
	t.Parallel()

	spec := classifier.ModelSpec{SampleRate: 48000, ClipLength: 3 * time.Second}
	clipLen := spec.SampleRate * int(spec.ClipLength.Seconds())
	samples := make([]float32, clipLen)

	stub := func(_ context.Context, _ string, _ [][]float32) ([]datastore.Results, error) {
		out := make([]datastore.Results, 0, reanalyzeTopN+5)
		for i := 0; i < reanalyzeTopN+5; i++ {
			out = append(out, datastore.Results{
				Species:    "species_" + string(rune('A'+i)),
				Confidence: float32(reanalyzeTopN+5-i) / 100.0,
			})
		}
		return out, nil
	}

	preds, _, err := reanalyzeSamples(t.Context(), stub, "BirdNET_V2.4", spec, samples)
	require.NoError(t, err)
	require.Len(t, preds, reanalyzeTopN+5,
		"per-model aggregator must NOT truncate; multi-model merger does that")
	for i := 1; i < len(preds); i++ {
		assert.GreaterOrEqual(t, preds[i-1].Confidence, preds[i].Confidence,
			"results must be sorted by descending confidence")
	}
}

// TestReanalyzeSamples_EmptyInput rejects zero-length sample input rather
// than silently succeeding with no predictions.
func TestReanalyzeSamples_EmptyInput(t *testing.T) {
	t.Parallel()

	spec := classifier.ModelSpec{SampleRate: 48000, ClipLength: 3 * time.Second}
	stub := func(_ context.Context, _ string, _ [][]float32) ([]datastore.Results, error) {
		t.Fatal("predict should not be called on empty input")
		return nil, nil
	}

	_, _, err := reanalyzeSamples(t.Context(), stub, "BirdNET_V2.4", spec, nil)
	require.Error(t, err)
}

// TestDecodeClipMonoPCM16_Roundtrip writes a tiny WAV file with a known tone,
// asks the decoder to read it back at a target sample rate, and verifies the
// sample count matches what ffmpeg's resampler would produce. This test
// requires ffmpeg in PATH and is skipped otherwise (same convention as other
// audio integration tests in this repo).
func TestDecodeClipMonoPCM16_Roundtrip(t *testing.T) {
	t.Parallel()

	ffmpeg, err := exec.LookPath("ffmpeg")
	if err != nil {
		t.Skip("ffmpeg not in PATH; skipping decode roundtrip")
	}

	dir := t.TempDir()
	srcPath := filepath.Join(dir, "tone.wav")

	// 1 second of 440 Hz at 48 kHz mono 16-bit.
	const (
		srcRate    = 48000
		srcSeconds = 1
		freq       = 440.0
	)
	writeMonoWAV16(t, srcPath, srcRate, srcSeconds, freq)

	// Decode at the same rate so we know exactly how many samples to expect.
	samples, err := decodeClipMonoPCM16(t.Context(), ffmpeg, srcPath, srcRate, 10)
	require.NoError(t, err)
	assert.Equal(t, srcRate*srcSeconds, len(samples),
		"decoder should return exactly one second of samples at the source rate")

	// Non-silent: a 440 Hz sine should produce non-zero values somewhere in
	// the first 100 samples.
	var nonZero bool
	for _, s := range samples[:100] {
		if s != 0 {
			nonZero = true
			break
		}
	}
	assert.True(t, nonZero, "decoded sine should be non-silent")

	// Now decode at half the source rate. ffmpeg's resampler should produce
	// ~half as many samples; allow a small tolerance because resampler
	// implementations differ slightly at filter edges.
	half, err := decodeClipMonoPCM16(t.Context(), ffmpeg, srcPath, srcRate/2, 10)
	require.NoError(t, err)
	want := srcRate / 2 * srcSeconds
	assert.InDelta(t, want, len(half), float64(want)/100,
		"resampled length should match target rate within ~1%")
}

// TestDecodeClipMonoPCM16_RejectsBadInput verifies the decoder surfaces an
// error (instead of returning empty samples) when ffmpeg can't decode the
// input. Uses a deliberately corrupt fixture rather than a missing file so
// we exercise the stderr-capture path.
func TestDecodeClipMonoPCM16_RejectsBadInput(t *testing.T) {
	t.Parallel()

	ffmpeg, err := exec.LookPath("ffmpeg")
	if err != nil {
		t.Skip("ffmpeg not in PATH; skipping decode error path")
	}

	dir := t.TempDir()
	badPath := filepath.Join(dir, "garbage.wav")
	require.NoError(t, os.WriteFile(badPath, []byte("this is not a wav file"), 0o600))

	_, err = decodeClipMonoPCM16(t.Context(), ffmpeg, badPath, 48000, 10)
	require.Error(t, err, "decoder must report ffmpeg failure on malformed input")
}

// TestDecodeClipMonoPCM16_RejectsEmptyFfmpegPath catches misconfigured
// installations early (and rules out accidental "ffmpeg" PATH lookups from
// the handler).
func TestDecodeClipMonoPCM16_RejectsEmptyFfmpegPath(t *testing.T) {
	t.Parallel()

	_, err := decodeClipMonoPCM16(t.Context(), "", "/does/not/matter.wav", 48000, 10)
	require.Error(t, err)
}

// writeMonoWAV16 emits a minimal 16-bit PCM mono WAV file containing a sine
// wave of the given frequency. Hand-rolled rather than pulled from a 3rd-party
// dep because the format is small and the test exercise is specifically that
// ffmpeg can decode what we wrote.
func writeMonoWAV16(t *testing.T, path string, sampleRate, seconds int, freq float64) {
	t.Helper()

	numSamples := sampleRate * seconds
	dataBytes := numSamples * 2 // 16-bit mono

	f, err := os.Create(path)
	require.NoError(t, err)
	defer func() { _ = f.Close() }()

	// RIFF header
	_, _ = f.Write([]byte("RIFF"))
	_ = binary.Write(f, binary.LittleEndian, uint32(36+dataBytes))
	_, _ = f.Write([]byte("WAVE"))

	// fmt chunk (PCM)
	_, _ = f.Write([]byte("fmt "))
	_ = binary.Write(f, binary.LittleEndian, uint32(16)) // subchunk size
	_ = binary.Write(f, binary.LittleEndian, uint16(1))  // PCM format
	_ = binary.Write(f, binary.LittleEndian, uint16(1))  // channels = mono
	_ = binary.Write(f, binary.LittleEndian, uint32(sampleRate))
	_ = binary.Write(f, binary.LittleEndian, uint32(sampleRate*2)) // byte rate
	_ = binary.Write(f, binary.LittleEndian, uint16(2))            // block align
	_ = binary.Write(f, binary.LittleEndian, uint16(16))           // bits per sample

	// data chunk
	_, _ = f.Write([]byte("data"))
	_ = binary.Write(f, binary.LittleEndian, uint32(dataBytes))
	for i := 0; i < numSamples; i++ {
		v := int16(math.Sin(2*math.Pi*freq*float64(i)/float64(sampleRate)) * 32767)
		_ = binary.Write(f, binary.LittleEndian, v)
	}
}
