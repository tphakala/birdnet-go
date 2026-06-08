package onnx

import (
	"os"
	"testing"
)

// envBenchModel gates BenchmarkPredictRaw_AllocationGate. It must point at a
// real ONNX model that exposes an embedding output; without it the benchmark
// skips. CI ships no model file, so the default path is always a clean skip.
const envBenchModel = "BIRDNET_EMBEDDING_TEST_MODEL"

// BenchmarkPredictRaw_AllocationGate pins, empirically, the allocation gate that
// the predict path enforces structurally: PredictRaw (extract off) materializes
// no embedding slice for a capable model, while PredictRawWithEmbeddings (extract
// on) materializes exactly one. The gate lives in Classifier.predict, where the
// embedding make is guarded by
//
//	extractEmbeddings && c.config.EmbeddingSize > 0 && c.config.EmbeddingIndex >= 0
//
// so the off path can never allocate the embedding regardless of model
// capability. M1 guarantees this by construction; this benchmark confirms it on
// a live forward pass.
//
// It is gated behind a real model path because proving zero embedding allocation
// on the off path requires a real ONNX session: the tensors only exist once the
// session runs. There is no in-package fixture model, so the default path skips.
//
// To fill this in locally once a capable model is available:
//
//  1. Initialize the ONNX Runtime once for the process. Either call
//     MustInitORT(libPath) with the path to the onnxruntime shared library, or
//     call ort.SetSharedLibraryPath + ort.InitializeEnvironment directly, and
//     defer DestroyORT().
//  2. Build the classifier from the model path:
//     c, err := NewClassifier(modelPath, WithSkipLabelValidation())
//     (or WithLabelsPath(labelPath) to supply real labels). Defer c.Close().
//     Confirm c.EmbeddingDim() > 0 so the model is actually embedding-capable;
//     otherwise the on path has nothing to allocate and the assertion is moot.
//  3. Build a zero input sized to the model's configured sample count:
//     audio := make([]float32, c.config.SampleCount)
//     (config is the ModelConfig set by NewClassifier; SampleCount is the
//     per-window sample length the predict path validates against.)
//  4. Measure each path with testing.AllocsPerRun and assert the delta is one
//     slice, e.g.:
//     off := testing.AllocsPerRun(100, func() { _, _ = c.PredictRaw(audio) })
//     on  := testing.AllocsPerRun(100, func() { _, _, _ = c.PredictRawWithEmbeddings(audio) })
//     and assert on == off + 1 (the single embedding slice). Optionally split
//     into b.Run("off", ...) and b.Run("on", ...) sub-benchmarks, each calling
//     b.ReportAllocs(), to report the per-op allocation counts side by side.
func BenchmarkPredictRaw_AllocationGate(b *testing.B) {
	modelPath := os.Getenv(envBenchModel)
	if modelPath == "" {
		b.Skipf("set %s to a capable ONNX model to run", envBenchModel)
	}
	b.Skip("fill in once a capable test model is available; see comment for structure")
}
