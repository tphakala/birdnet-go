package inference_test

import (
	"encoding/json"
	"fmt"
	"math"
	"math/rand/v2"
	"os"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/tphakala/birdnet-go/internal/inference"
	"github.com/tphakala/birdnet-go/internal/inference/onnx"
	"github.com/tphakala/birdnet-go/internal/inference/vad"
)

// These tests compare the outputs of the ONNX Runtime code paths they list between two
// builds of the repository, so a change of Go binding can be checked against the
// same native library, models and inputs. They use only APIs that exist on both
// sides of such a change, so this file compiles unchanged on either build.
//
// Run it on the reference build with ORT_PARITY_MODE=write, which stores every
// output in ORT_PARITY_FILE, then on the build under test with
// ORT_PARITY_MODE=compare:
//
//	ORT_PARITY_MODE=write ORT_PARITY_FILE=parity.json \
//	  ORT_PARITY_LIB=/usr/lib/libonnxruntime.so.1.31.0 \
//	  ORT_PARITY_MODELS='v24=/m/v24.onnx:/m/v24_labels.txt,emb=/m/emb.onnx' \
//	  ORT_PARITY_GEOMODEL=/m/geomodel.onnx:/m/geomodel_labels.txt \
//	  go test -run TestORTBindingParity ./internal/inference
//
// Environment:
//   - ORT_PARITY_MODE: "write" or "compare". Unset skips the test.
//   - ORT_PARITY_FILE: JSON file written or read.
//   - ORT_PARITY_LIB: optional libonnxruntime path.
//   - ORT_PARITY_MODELS: comma separated id=model[:labels] entries for
//     classifiers. Without labels the model is loaded for logits and embeddings
//     only (label validation skipped, no batch run).
//   - ORT_PARITY_GEOMODEL: model:labels of the geographic model, used for the
//     range filter and, because it maps three inputs to one score per species,
//     for the custom classifier.
//
// The Silero VAD model embedded in the binary is always included.
const (
	envParityMode     = "ORT_PARITY_MODE"
	envParityFile     = "ORT_PARITY_FILE"
	envParityLib      = "ORT_PARITY_LIB"
	envParityModels   = "ORT_PARITY_MODELS"
	envParityGeomodel = "ORT_PARITY_GEOMODEL"

	parityModeWrite   = "write"
	parityModeCompare = "compare"

	// Keys carry a thread marker. Single-thread runs are compared bit for bit;
	// runs at the default thread count may differ in reduction order, so they
	// are compared within parityMultiThreadTolerance.
	parityKeySingleThread = "/t1/"
	parityKeyMultiThread  = "/tN/"

	// parityMultiThreadTolerance bounds the difference of a multi-thread output,
	// absolute for values below 1 and relative above, since a float32 logit near
	// 20 has a spacing of about 2e-6.
	parityMultiThreadTolerance = 1e-6

	paritySeed       = 20261023
	parityBatchSize  = 2
	parityGridCells  = 6
	parityVADHopRuns = 7
)

// parityVADHops are the hop counts of the VAD runs, with repeats and a drop to
// one hop so the speech_probs buffer changes size.
var parityVADHops = [parityVADHopRuns]int{31, 20, 31, 32, 32, 1, 31}

// paritySpec is one classifier entry of ORT_PARITY_MODELS.
type paritySpec struct {
	id, model, labels string
}

// parityOutputs maps an output name to its values.
type parityOutputs map[string][]float32

func parityMode(tb testing.TB) string {
	tb.Helper()
	mode := os.Getenv(envParityMode)
	if mode == "" {
		tb.Skipf("set %s to %s or %s to run", envParityMode, parityModeWrite, parityModeCompare)
	}
	require.Contains(tb, []string{parityModeWrite, parityModeCompare}, mode, envParityMode)
	return mode
}

// parseParitySpecs reads ORT_PARITY_MODELS.
func parseParitySpecs(tb testing.TB) []paritySpec {
	tb.Helper()
	var specs []paritySpec
	for entry := range strings.SplitSeq(os.Getenv(envParityModels), ",") {
		if entry == "" {
			continue
		}
		id, rest, ok := strings.Cut(entry, "=")
		require.True(tb, ok, "%s entry %q: want id=model[:labels]", envParityModels, entry)
		model, labels, _ := strings.Cut(rest, ":")
		specs = append(specs, paritySpec{id: id, model: model, labels: labels})
	}
	return specs
}

// parityGeomodel reads ORT_PARITY_GEOMODEL; ok is false when it is unset.
func parityGeomodel(tb testing.TB) (model, labels string, ok bool) {
	tb.Helper()
	v := os.Getenv(envParityGeomodel)
	if v == "" {
		return "", "", false
	}
	model, labels, found := strings.Cut(v, ":")
	require.True(tb, found, "%s: want model:labels", envParityGeomodel)
	return model, labels, true
}

// parityNoise returns n deterministic samples in [-0.5, 0.5).
func parityNoise(rng *rand.Rand, n int) []float32 {
	out := make([]float32, n)
	for i := range out {
		out[i] = rng.Float32() - 0.5
	}
	return out
}

func parityLabels(tb testing.TB, path string) []string {
	tb.Helper()
	labels, err := onnx.LoadLabels(path)
	require.NoError(tb, err)
	return labels
}

// collectClassifier records logits, embeddings and a two-segment batch for one
// model. The single-thread pass goes through onnx.NewClassifier with default
// options; the multi-thread pass goes through the production wrapper at the
// default thread count.
func collectClassifier(tb testing.TB, spec paritySpec, out parityOutputs) {
	tb.Helper()
	opts := []onnx.ClassifierOption{onnx.WithTopK(0), onnx.WithMinConfidence(0)}
	var labels []string
	if spec.labels != "" {
		labels = parityLabels(tb, spec.labels)
		opts = append(opts, onnx.WithLabels(labels))
	} else {
		labels = []string{"placeholder"}
		opts = append(opts, onnx.WithLabels(labels), onnx.WithSkipLabelValidation())
	}

	c, err := onnx.NewClassifier(spec.model, opts...)
	require.NoError(tb, err, spec.id)
	defer func() { assert.NoError(tb, c.Close()) }()

	rng := rand.New(rand.NewPCG(paritySeed, 1))
	segments := make([][]float32, parityBatchSize)
	for i := range segments {
		segments[i] = parityNoise(rng, c.Config().SampleCount)
	}

	logits, emb, err := c.PredictRawWithEmbeddings(segments[0])
	require.NoError(tb, err, spec.id)
	out[spec.id+parityKeySingleThread+"logits"] = logits
	out[spec.id+parityKeySingleThread+"embeddings"] = emb

	if spec.labels != "" {
		results, err := c.PredictBatch(segments)
		require.NoError(tb, err, spec.id)
		for i, r := range results {
			out[fmt.Sprintf("%s%sbatch%d/scores", spec.id, parityKeySingleThread, i)] = r.RawScores
			out[fmt.Sprintf("%s%sbatch%d/embeddings", spec.id, parityKeySingleThread, i)] = r.Embeddings
		}
	}

	multi, err := inference.NewONNXClassifier(spec.model, inference.ONNXClassifierOptions{
		Labels:              labels,
		SkipLabelValidation: spec.labels == "",
	})
	require.NoError(tb, err, spec.id)
	defer multi.Close()
	extractor, ok := multi.(inference.EmbeddingExtractor)
	require.True(tb, ok, spec.id)
	mLogits, mEmb, err := extractor.PredictWithEmbeddings(segments[0])
	require.NoError(tb, err, spec.id)
	out[spec.id+parityKeyMultiThread+"logits"] = mLogits
	out[spec.id+parityKeyMultiThread+"embeddings"] = mEmb
}

// collectGeomodel records the range filter over a small grid and the custom
// classifier over the same location and week inputs.
func collectGeomodel(tb testing.TB, model, labelsPath string, out parityOutputs) {
	tb.Helper()
	labels := parityLabels(tb, labelsPath)
	rng := rand.New(rand.NewPCG(paritySeed, 2))

	grid := make([]float32, 0, parityGridCells*3)
	for range parityGridCells {
		lat := 180*rng.Float32() - 90
		lon := 360*rng.Float32() - 180
		week := float32(1 + rng.IntN(48))
		grid = append(grid, lat, lon, week)
	}

	rf, err := inference.NewONNXRangeFilter(model, inference.ONNXRangeFilterOptions{Labels: labels})
	require.NoError(tb, err)
	defer rf.Close()
	scores, err := rf.PredictBatch(grid, parityGridCells)
	require.NoError(tb, err)
	out["geo"+parityKeySingleThread+"rangefilter"] = scores

	for _, threads := range []struct {
		marker  string
		threads int
	}{{parityKeySingleThread, 1}, {parityKeyMultiThread, 0}} {
		cc, err := inference.NewONNXCustomClassifier(model, inference.ONNXCustomClassifierOptions{
			Labels: labels, Threads: threads.threads,
		})
		require.NoError(tb, err)
		for i := range parityGridCells {
			got, err := cc.PredictEmbedding(grid[i*3 : i*3+3])
			require.NoError(tb, err)
			out[fmt.Sprintf("geo%scustom%d", threads.marker, i)] = got
		}
		cc.Close()
	}
}

// collectVAD records probabilities and final states over parityVADHops with the
// state threaded through, on the embedded Silero model.
func collectVAD(tb testing.TB, out parityOutputs) {
	tb.Helper()
	require.True(tb, vad.HasEmbeddedModel(), "no embedded VAD model in this build")
	sess, err := vad.NewSession(vad.Config{ModelData: vad.EmbeddedModelData(), LibraryPath: os.Getenv(envParityLib)})
	require.NoError(tb, err)
	defer func() { assert.NoError(tb, sess.Close()) }()

	const stateWidth, rowWidth = 128, 576 // Silero state width and context+window row width
	rng := rand.New(rand.NewPCG(paritySeed, 3))
	h := make([]float32, stateWidth)
	c := make([]float32, stateWidth)
	var probs []float32
	for _, n := range parityVADHops {
		p, hOut, cOut, err := sess.Run(parityNoise(rng, n*rowWidth), h, c)
		require.NoError(tb, err)
		probs = append(probs, p...)
		h = append(h[:0], hOut...)
		c = append(c[:0], cOut...)
	}
	out["vad"+parityKeySingleThread+"probs"] = probs
	out["vad"+parityKeySingleThread+"h"] = h
	out["vad"+parityKeySingleThread+"c"] = c
}

// TestORTBindingParity writes or compares the outputs of every ONNX Runtime
// code path. See the file comment for the environment.
func TestORTBindingParity(t *testing.T) {
	mode := parityMode(t)
	file := os.Getenv(envParityFile)
	require.NotEmpty(t, file, envParityFile)
	require.NoError(t, inference.InitONNXRuntime(os.Getenv(envParityLib)))

	got := parityOutputs{}
	for _, spec := range parseParitySpecs(t) {
		collectClassifier(t, spec, got)
	}
	if model, labels, ok := parityGeomodel(t); ok {
		collectGeomodel(t, model, labels, got)
	}
	collectVAD(t, got)

	if mode == parityModeWrite {
		data, err := json.Marshal(got)
		require.NoError(t, err)
		require.NoError(t, os.WriteFile(file, data, 0o600))
		t.Logf("wrote %d outputs to %s", len(got), file)
		return
	}

	data, err := os.ReadFile(file) //nolint:gosec // G304: test input path from the environment
	require.NoError(t, err)
	var want parityOutputs
	require.NoError(t, json.Unmarshal(data, &want))
	require.NotEmpty(t, want)

	for name, w := range want {
		g, ok := got[name]
		require.True(t, ok, "output %s missing from this build", name)
		compareParity(t, name, w, g)
	}
	for name := range got {
		_, ok := want[name]
		assert.True(t, ok, "output %s is not in the reference file", name)
	}
	t.Logf("compared %d outputs", len(want))
}

// compareParity compares one output: bit for bit for single-thread runs, within
// parityMultiThreadTolerance for multi-thread runs.
func compareParity(t *testing.T, name string, want, got []float32) {
	t.Helper()
	require.Len(t, got, len(want), name)
	exact := !strings.Contains(name, parityKeyMultiThread)
	for i := range want {
		if exact {
			if math.Float32bits(want[i]) != math.Float32bits(got[i]) {
				t.Errorf("%s[%d]: reference %v, got %v (not bit-identical)", name, i, want[i], got[i])
				return
			}
			continue
		}
		bound := parityMultiThreadTolerance * math.Max(1, math.Abs(float64(want[i])))
		if math.Abs(float64(want[i])-float64(got[i])) > bound {
			t.Errorf("%s[%d]: reference %v, got %v (beyond %g)", name, i, want[i], got[i], bound)
			return
		}
	}
}

// BenchmarkONNXClassifierPredict measures a single-segment classifier run per
// model in ORT_PARITY_MODELS, at one thread and at the default thread count,
// with allocations reported.
func BenchmarkONNXClassifierPredict(b *testing.B) {
	specs := parseParitySpecs(b)
	if len(specs) == 0 {
		b.Skipf("set %s to run", envParityModels)
	}
	require.NoError(b, inference.InitONNXRuntime(os.Getenv(envParityLib)))

	for _, spec := range specs {
		labels := []string{"placeholder"}
		if spec.labels != "" {
			labels = parityLabels(b, spec.labels)
		}
		for _, threads := range []int{1, 0} {
			name := fmt.Sprintf("%s/threads=%d", spec.id, threads)
			b.Run(name, func(b *testing.B) {
				c, err := inference.NewONNXClassifier(spec.model, inference.ONNXClassifierOptions{
					Labels: labels, Threads: threads, SkipLabelValidation: spec.labels == "",
				})
				require.NoError(b, err)
				defer c.Close()
				extractor, ok := c.(inference.EmbeddingExtractor)
				require.True(b, ok)

				cfgOpts := []onnx.ClassifierOption{onnx.WithLabels(labels), onnx.WithSkipLabelValidation()}
				sizer, err := onnx.NewClassifier(spec.model, cfgOpts...)
				require.NoError(b, err)
				samples := parityNoise(rand.New(rand.NewPCG(paritySeed, 4)), sizer.Config().SampleCount)
				require.NoError(b, sizer.Close())

				b.ReportAllocs()
				for b.Loop() {
					if _, _, err := extractor.PredictWithEmbeddings(samples); err != nil {
						b.Fatal(err)
					}
				}
			})
		}
	}
}
