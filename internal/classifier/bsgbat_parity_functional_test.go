//go:build onnx

package classifier

import (
	"encoding/binary"
	"math"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/tphakala/go-audio-dsp/mel"
	dsppcm "github.com/tphakala/go-audio-dsp/pcm"
	"github.com/tphakala/go-audio-dsp/stft"

	"github.com/tphakala/birdnet-go/internal/inference"
	"github.com/tphakala/birdnet-go/internal/inference/bsgbat"
)

const (
	// envBSGBatModel, envBSGBatLabels and envBSGBatData point the test at the
	// published r1 checkpoint, its label file and the directory of the model's
	// original code and examples (data/example{1,2,3}.wav with the shipped
	// out/example{1,2,3}.wav.logits). envBSGBatORTLib optionally names the
	// libonnxruntime path.
	envBSGBatModel  = "BSGBAT_TEST_MODEL"
	envBSGBatLabels = "BSGBAT_TEST_LABELS"
	envBSGBatData   = "BSGBAT_TEST_DATA"
	envBSGBatORTLib = "ONNX_TEST_ORT_LIB"

	// shippedLogitsTolerance covers the two-decimal rounding of the shipped logits
	// (0.005) plus the float32 difference between the reference's librosa front
	// end and this one.
	shippedLogitsTolerance = 0.006
	// liveWindowTolerance bounds the difference between a live window and the same
	// frames cut from the whole recording. Both run the same mel code, so the
	// remaining difference is float32 rounding in the network.
	liveWindowTolerance = 1e-5

	// wavHeaderChunkBytes is the size of a RIFF chunk header (id and length).
	wavHeaderChunkBytes = 8
	// wavRIFFHeaderBytes is the size of the RIFF header (id, length and form).
	wavRIFFHeaderBytes = 12
	// wavFmtBitsOffset is the offset of the bits-per-sample field in a fmt chunk.
	wavFmtBitsOffset = 14
	// wavFmtRateOffset is the offset of the sample-rate field in a fmt chunk.
	wavFmtRateOffset = 4
	// wavFmtChannelsOffset is the offset of the channel-count field in a fmt chunk.
	wavFmtChannelsOffset = 2
	// pcm16Scale converts a 16-bit sample to the [-1, 1) range the reference reads.
	pcm16Scale = 32768
	// pcmScaleTolerance is far below one int16 step (1/32768), so the decoded value
	// must be the int16/32768 value itself.
	pcmScaleTolerance = 1e-9
	// pcm16Bytes is the size of a 16-bit sample.
	pcm16Bytes = 2
	// shippedLogitColumns is the number of logits per shipped row (21 species and
	// Background).
	shippedLogitColumns = 22
)

// readMono16WAV reads a 16-bit mono PCM WAV file into float32 samples in [-1, 1).
func readMono16WAV(t *testing.T, path string, wantRate int) []float32 {
	t.Helper()
	raw, err := os.ReadFile(path)
	require.NoError(t, err)
	require.GreaterOrEqual(t, len(raw), wavRIFFHeaderBytes)
	require.Equal(t, "RIFF", string(raw[:4]))

	var samples []float32
	for off := wavRIFFHeaderBytes; off+wavHeaderChunkBytes <= len(raw); {
		id := string(raw[off : off+4])
		size := int(binary.LittleEndian.Uint32(raw[off+4:]))
		body := raw[off+wavHeaderChunkBytes : min(off+wavHeaderChunkBytes+size, len(raw))]
		switch id {
		case "fmt ":
			require.Equal(t, 1, int(binary.LittleEndian.Uint16(body[wavFmtChannelsOffset:])), "mono")
			require.Equal(t, wantRate, int(binary.LittleEndian.Uint32(body[wavFmtRateOffset:])), "sample rate")
			require.Equal(t, 16, int(binary.LittleEndian.Uint16(body[wavFmtBitsOffset:])), "bit depth")
		case "data":
			// Decode with the pipeline's own converter (analysis.decodePCM16WithPool
			// calls it), then check it is the reference's int16/32768 scaling: the
			// model is level dependent through the log10(x + 1e-6) floor.
			samples = make([]float32, len(body)/pcm16Bytes)
			_, derr := dsppcm.BytesToFloat32(samples, body)
			require.NoError(t, derr)
			for i := range samples {
				want := float32(int16(binary.LittleEndian.Uint16(body[i*pcm16Bytes:]))) / pcm16Scale
				require.InDeltaf(t, want, samples[i], pcmScaleTolerance, "sample %d must be int16/32768", i)
			}
		}
		off += wavHeaderChunkBytes + size + size%2
	}
	require.NotEmpty(t, samples, "no data chunk in %s", path)
	return samples
}

// readShippedLogits parses a shipped .logits file: one row per segment, 22 values.
func readShippedLogits(t *testing.T, path string) [][]float32 {
	t.Helper()
	raw, err := os.ReadFile(path)
	require.NoError(t, err)
	var rows [][]float32
	for line := range strings.SplitSeq(string(raw), "\n") {
		fields := strings.Fields(line)
		if len(fields) == 0 {
			continue
		}
		require.Len(t, fields, shippedLogitColumns)
		row := make([]float32, len(fields))
		for i, f := range fields {
			v, perr := strconv.ParseFloat(f, 32)
			require.NoError(t, perr)
			row[i] = float32(v)
		}
		rows = append(rows, row)
	}
	return rows
}

// referenceSegments frames a whole recording the way the reference does
// (data384.py wav2spectrograms): librosa-style centered framing (stft.PadZero),
// max(ceil((frames-512)/250), 1) segments with segment i starting at frame 250*i,
// and up to 250 trailing frames unscored. A recording shorter than 512 frames is
// padded in the power spectrogram, that is with log10(LogOffset) after the log.
// Each segment is normalized and returned as one model input.
func referenceSegments(t *testing.T, samples []float32) [][]float32 {
	t.Helper()
	ex, err := mel.New(bsgbat.MelConfig())
	require.NoError(t, err)
	m := ex.Compute(samples, stft.PadZero)

	frames := m.Frames
	count := max((frames-bsgbat.NumFrames+bsgbat.StepFrames-1)/bsgbat.StepFrames, 1)
	silence := float32(math.Log10(bsgbat.LogOffset))
	segments := make([][]float32, count)
	for i := range count {
		seg := make([]float32, bsgbat.InputSize)
		for f := range bsgbat.NumFrames {
			src := i*bsgbat.StepFrames + f
			col := seg[f*bsgbat.NumMels : (f+1)*bsgbat.NumMels]
			if src < frames {
				copy(col, m.Column(src))
				continue
			}
			for k := range col {
				col[k] = silence
			}
		}
		bsgbat.Normalize(seg, bsgbat.NumMels)
		segments[i] = seg
	}
	return segments
}

func sigmoid64(x float32) float64 { return 1 / (1 + math.Exp(-float64(x))) }

// TestBSGBatParity_Functional is the correctness proof for the BSG-BAT front end
// and instance. It needs the published r1 model and the model's example recordings,
// so it is skipped unless the BSGBAT_TEST_* variables are set:
//
//   - BSGBAT_TEST_MODEL: path to bsgbat_v0.21_r1.onnx
//   - BSGBAT_TEST_LABELS: path to bsgbat_labels.txt
//   - BSGBAT_TEST_DATA: directory holding data/example{1,2,3}.wav and
//     out/example{1,2,3}.wav.logits
//   - ONNX_TEST_ORT_LIB: optional libonnxruntime path
//
// For each example it checks that (a) the whole-recording reference path matches
// the shipped logits, and (b) a live window cut at the segment's offset gives the
// same logits as that segment, which is what the analysis buffer delivers in a
// continuous stream. CI never runs it.
func TestBSGBatParity_Functional(t *testing.T) {
	modelPath := os.Getenv(envBSGBatModel)
	labelPath := os.Getenv(envBSGBatLabels)
	dataDir := os.Getenv(envBSGBatData)
	if modelPath == "" || labelPath == "" || dataDir == "" {
		t.Skipf("set %s, %s and %s to run", envBSGBatModel, envBSGBatLabels, envBSGBatData)
	}

	inst, err := NewBSGBat(&BSGBatModelConfig{
		ModelPath:       modelPath,
		LabelPath:       labelPath,
		ONNXRuntimePath: os.Getenv(envBSGBatORTLib),
		Threads:         1,
	})
	require.NoError(t, err)
	t.Cleanup(func() { _ = inst.Close() })

	require.Equal(t, 21, inst.NumSpecies())
	for _, label := range inst.Labels() {
		require.NotEqual(t, bsgbat.BackgroundLabel, label, "Labels must hold species only")
	}

	// The reference path runs the same graph through its own runner so the live
	// path is compared with an independent route to the logits.
	ref, err := inference.NewONNXTensorModel(modelPath, inference.ONNXTensorModelOptions{Threads: 1})
	require.NoError(t, err)
	t.Cleanup(ref.Close)

	var maxShipped, maxLive float64
	for _, name := range []string{"example1", "example2", "example3"} {
		t.Run(name, func(t *testing.T) {
			samples := readMono16WAV(t, filepath.Join(dataDir, "data", name+".wav"), bsgbat.SampleRate)
			shipped := readShippedLogits(t, filepath.Join(dataDir, "out", name+".wav.logits"))
			segments := referenceSegments(t, samples)
			require.Len(t, segments, len(shipped), "segment count must match the shipped rows")

			for i, seg := range segments {
				out, rerr := ref.Run(seg)
				require.NoError(t, rerr)
				refLogits := out[0]
				require.Len(t, refLogits, shippedLogitColumns)

				// (a) the whole-recording path against the shipped logits.
				for c := range refLogits {
					d := math.Abs(float64(refLogits[c] - shipped[i][c]))
					maxShipped = max(maxShipped, d)
					assert.LessOrEqualf(t, d, shippedLogitsTolerance, "segment %d column %d: %v vs shipped %v", i, c, refLogits[c], shipped[i][c])
				}

				// (b) the live window of an interior segment against the same
				// segment. Segment 0 starts in the left zero padding and has no live
				// equivalent.
				if i == 0 {
					continue
				}
				start := bsgbat.HopSize*bsgbat.StepFrames*i - bsgbat.FrameSize/2
				require.LessOrEqual(t, start+bsgbat.ClipSamples, len(samples), "segment %d window must lie inside the recording", i)
				window := samples[start : start+bsgbat.ClipSamples]

				live, lerr := inst.run(window)
				require.NoError(t, lerr)
				require.Len(t, live, len(refLogits))
				for c := range live {
					d := math.Abs(float64(live[c] - refLogits[c]))
					maxLive = max(maxLive, d)
					assert.LessOrEqualf(t, d, liveWindowTolerance, "live segment %d column %d: %v vs reference %v", i, c, live[c], refLogits[c])
				}

				results, perr := inst.Predict(t.Context(), [][]float32{window})
				require.NoError(t, perr)
				require.NotEmpty(t, results)
				for _, r := range results {
					assert.NotEqual(t, bsgbat.BackgroundLabel, r.Species, "Background must never be reported")
				}
				// The top result is the species with the largest logit.
				best := 0
				for c := range refLogits[:shippedLogitColumns-1] {
					if refLogits[c] > refLogits[best] {
						best = c
					}
				}
				assert.Equalf(t, inst.Labels()[best], results[0].Species, "segment %d top species", i)
				assert.InDelta(t, sigmoid64(refLogits[best]), float64(results[0].Confidence), liveWindowTolerance)
			}
		})
	}
	t.Logf("max |logit - shipped| = %.5f (tolerance %.3f), max |live - whole recording| = %.2e (tolerance %.0e)",
		maxShipped, shippedLogitsTolerance, maxLive, liveWindowTolerance)
}

// TestBSGBat_PredictSilence_Functional checks that digital silence, which is what
// the cadence probe and the warm-up feed the model, scores without a failure and
// within the probability range. Skipped like the parity test.
func TestBSGBat_PredictSilence_Functional(t *testing.T) {
	modelPath := os.Getenv(envBSGBatModel)
	labelPath := os.Getenv(envBSGBatLabels)
	if modelPath == "" || labelPath == "" {
		t.Skipf("set %s and %s to run", envBSGBatModel, envBSGBatLabels)
	}

	inst, err := NewBSGBat(&BSGBatModelConfig{
		ModelPath:       modelPath,
		LabelPath:       labelPath,
		ONNXRuntimePath: os.Getenv(envBSGBatORTLib),
		Threads:         1,
	})
	require.NoError(t, err)
	t.Cleanup(func() { _ = inst.Close() })

	silence := make([]float32, inst.Spec().ClipSampleCount())
	results, err := inst.Predict(t.Context(), [][]float32{silence})
	require.NoError(t, err)
	require.NotEmpty(t, results)
	for _, r := range results {
		assert.GreaterOrEqual(t, r.Confidence, float32(0))
		assert.LessOrEqual(t, r.Confidence, float32(1))
	}
}
