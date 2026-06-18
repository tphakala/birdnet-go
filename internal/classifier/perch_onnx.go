// perch_onnx.go provides Perch v2 model support using the ONNX backend.
package classifier

import (
	"context"
	"fmt"
	"math"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/tphakala/birdnet-go/internal/datastore"
	"github.com/tphakala/birdnet-go/internal/errors"
	"github.com/tphakala/birdnet-go/internal/inference"
	"github.com/tphakala/birdnet-go/internal/logger"
)

// perchLogitsOutputIndex is the output port index of the Perch v2 species logits.
// Perch v2 is a multi-output graph: embedding[1536] at index 0, then
// spatial_embedding, spectrogram, and the label logits[14795] at index 3. This
// matches the ORT path's LogitsIndex for Perch (internal/inference/onnx
// detection.go). If a future Perch graph reorders its outputs, the OpenVINO
// classifier's NumClasses-vs-label-count check (NewOpenVINOClassifier) catches the
// mismatch at load and falls back to ORT, so a stale index degrades safely rather
// than silently returning the wrong tensor.
const perchLogitsOutputIndex = 3

// Perch represents a loaded Google Perch v2 model.
// Implements ModelInstance. Goroutine-safe via internal mutex.
type Perch struct {
	classifier inference.Classifier
	labels     []string
	info       ModelInfo
	mu         sync.Mutex
}

// PerchConfig holds configuration for creating a Perch model instance.
type PerchConfig struct {
	ModelPath       string // Path to the Perch v2 ONNX model file
	LabelPath       string // Path to the Perch v2 label file
	ONNXRuntimePath string // Path to ONNX Runtime shared library
	Threads         int    // CPU threads for inference (0 = default)

	// OpenVINO opt-in, sourced from BirdNET settings. When the configured model is
	// the no_dft Perch variant and the gate allows it, Perch runs on OpenVINO
	// (ARM A76 f16 CPU or Intel iGPU); any failure falls back to ORT.
	Backend        string // BirdNET.Backend ("auto"/"onnx"/"openvino")
	OpenVINOPath   string // BirdNET.OpenVINOPath (libopenvino_c location)
	OpenVINODevice string // BirdNET.OpenVINODevice ("auto"/"cpu"/"gpu")
}

// NewPerch creates a new Perch v2 model instance.
func NewPerch(cfg *PerchConfig) (*Perch, error) {
	log := GetLogger()

	// Load and parse labels
	labelData, err := os.ReadFile(cfg.LabelPath)
	if err != nil {
		return nil, errors.New(err).
			Category(errors.CategoryFileIO).
			Context("label_path", cfg.LabelPath).
			Build()
	}

	labels, err := ParsePerchLabels(labelData)
	if err != nil {
		return nil, errors.New(err).
			Category(errors.CategoryLabelLoad).
			Context("label_path", cfg.LabelPath).
			Build()
	}

	if len(labels) == 0 {
		return nil, errors.Newf("no labels parsed from %s", cfg.LabelPath).
			Category(errors.CategoryLabelLoad).
			Build()
	}

	// Prefer the OpenVINO backend when eligible (Perch no_dft model + gate),
	// falling back to ORT on any failure. OpenVINO must never make Perch fail to
	// load, so tryPerchOpenVINO logs and swallows OV errors and returns ok=false.
	classifier, ok := tryPerchOpenVINO(cfg, labels)
	if !ok {
		// Initialize ONNX Runtime
		if err := inference.InitONNXRuntime(cfg.ONNXRuntimePath); err != nil {
			return nil, errors.New(err).
				Category(errors.CategoryModelInit).
				Context("onnx_runtime_path", cfg.ONNXRuntimePath).
				Build()
		}

		// Create ONNX classifier
		var cerr error
		classifier, cerr = inference.NewONNXClassifier(cfg.ModelPath, inference.ONNXClassifierOptions{
			Labels:  labels,
			Threads: cfg.Threads,
		})
		if cerr != nil {
			return nil, errors.New(cerr).
				Category(errors.CategoryModelInit).
				Context("model_path", cfg.ModelPath).
				Context("label_count", len(labels)).
				Build()
		}
	}

	info := ModelInfo{
		ID:          RegistryIDPerchV2,
		Name:        ModelNamePerchV2,
		Description: fmt.Sprintf("Perch v2 model with %d species", len(labels)),
		Spec:        ModelSpec{SampleRate: 32000, ClipLength: 5 * time.Second},
		NumSpecies:  len(labels),
	}

	log.Info("Perch v2 model initialized",
		logger.String("model_path", cfg.ModelPath),
		logger.Int("species", len(labels)))

	return &Perch{
		classifier: classifier,
		labels:     labels,
		info:       info,
	}, nil
}

// tryPerchOpenVINO attempts to build an OpenVINO classifier for Perch v2. It
// returns (classifier, true) on success or (nil, false) to fall back to ORT.
// Any failure (ineligible model, gate denied, init/compile/validation error) is
// logged and swallowed: OpenVINO must never make Perch fail to load. OV is only
// attempted for the no_dft model variant, since the stock perch_v2.onnx cannot
// compile on OpenVINO (a dynamic-rank DFT op).
func tryPerchOpenVINO(cfg *PerchConfig, labels []string) (inference.Classifier, bool) {
	if !isPerchNoDFT(cfg.ModelPath) {
		return nil, false
	}
	plan, ok := openVINOPlanFor(cfg.Backend, cfg.OpenVINODevice, RegistryIDPerchV2, cfg.OpenVINOPath, perchLogitsOutputIndex)
	if !ok {
		return nil, false
	}

	log := GetLogger()
	// InitOpenVINO is idempotent: the auto/GPU plan above may already have loaded the
	// core to enumerate devices, but the explicit-CPU plan path does not, so init
	// here to cover it. A load failure means no usable OpenVINO; fall back to ORT.
	if err := inference.InitOpenVINO(cfg.OpenVINOPath); err != nil {
		log.Warn("Perch OpenVINO init failed; using ONNX Runtime", logger.Error(err))
		return nil, false
	}

	start := time.Now()
	classifier, err := inference.NewOpenVINOClassifier(cfg.ModelPath, inference.OpenVINOClassifierOptions{
		Labels:        labels,
		Threads:       cfg.Threads,
		Device:        plan.device,
		OutputIndex:   plan.outputIndex,
		PrecisionHint: plan.precision, // "" => f16 default; Perch f16-GPU validated OK
	})
	if err != nil {
		log.Warn("Perch OpenVINO classifier init failed; using ONNX Runtime",
			logger.String("device", plan.device),
			logger.Error(err))
		return nil, false
	}

	log.Info("Perch v2 model using OpenVINO backend",
		logger.String("device", plan.device),
		logger.Int("species", classifier.NumSpecies()),
		logger.String("init_time", time.Since(start).String()))
	return classifier, true
}

// isPerchNoDFT reports whether the model file is the OpenVINO-compatible Perch
// no_dft variant. The model gallery does not yet ship a distinct identity for it,
// so it is detected by filename (contains "no_dft" or "no-dft").
func isPerchNoDFT(modelPath string) bool {
	base := strings.ToLower(filepath.Base(modelPath))
	return strings.Contains(base, "no_dft") || strings.Contains(base, "no-dft")
}

// Predict runs inference on the given audio samples.
// Implements ModelInstance.
func (p *Perch) Predict(ctx context.Context, samples [][]float32) ([]datastore.Results, error) {
	span, _ := startPredictSpan(ctx, RegistryIDPerchV2, samples)
	defer span.Finish()

	start := time.Now()

	// Guard against empty sample slice. Pre-inference rejections are tagged but
	// not counted as predictions, mirroring BirdNET.Predict.
	if len(samples) == 0 || len(samples[0]) == 0 {
		span.markErrored(errTypeEmptySample)
		return nil, errors.Newf("empty audio sample").
			Category(errors.CategoryValidation).
			Build()
	}

	p.mu.Lock()
	defer p.mu.Unlock()

	// Guard against nil classifier (e.g., after Close() is called concurrently)
	if p.classifier == nil {
		span.markErrored(errTypeClassifierNil)
		return nil, errors.Newf("Perch classifier is not initialized").
			Category(errors.CategoryModelInit).
			Build()
	}

	rawLogits, err := p.classifier.Predict(samples[0])
	if err != nil {
		err = errors.New(err).
			Category(errors.CategoryAudio).
			Context("model", RegistryIDPerchV2).
			Build()
		recordPredictionFailure(span, RegistryIDPerchV2, errTypeInvokeFailed, start, err)
		return nil, err
	}

	// Apply softmax to normalize raw logits into probabilities (0.0-1.0).
	// The inference.Classifier interface returns pre-activation logits;
	// BirdNET applies sigmoid in its own Predict path, Perch needs softmax.
	predictions := perchSoftmax(rawLogits)

	// Pair labels with predictions
	results, err := pairLabelsAndConfidence(p.labels, predictions)
	if err != nil {
		recordPredictionFailure(span, RegistryIDPerchV2, errTypeLabelMismatch, start, err)
		return nil, err
	}

	// Success: Finish records the single prediction because the span is not errored.
	topResults := getTopKResults(results, defaultTopKResults)
	recordPredictionSuccess(span, len(topResults), start)

	return topResults, nil
}

// Spec returns the audio requirements for Perch v2.
func (p *Perch) Spec() ModelSpec { return p.info.Spec }

// ModelID returns the unique model identifier.
func (p *Perch) ModelID() string { return p.info.ID }

// ModelName returns the human-readable model name.
func (p *Perch) ModelName() string { return p.info.Name }

// ModelVersion returns the model version string.
func (p *Perch) ModelVersion() string { return "Perch V2" }

// NumSpecies returns the number of species.
func (p *Perch) NumSpecies() int { return len(p.labels) }

// Labels returns a copy of the species labels to prevent mutation of internal state.
func (p *Perch) Labels() []string {
	out := make([]string, len(p.labels))
	copy(out, p.labels)
	return out
}

// Close releases resources held by the Perch model.
func (p *Perch) Close() error {
	p.mu.Lock()
	defer p.mu.Unlock()
	if p.classifier != nil {
		p.classifier.Close()
		p.classifier = nil
	}
	return nil
}

// perchSoftmax normalizes raw logits into probabilities via the softmax function.
func perchSoftmax(logits []float32) []float32 {
	if len(logits) == 0 {
		return logits
	}
	result := make([]float32, len(logits))
	maxVal := logits[0]
	for _, v := range logits[1:] {
		if v > maxVal {
			maxVal = v
		}
	}
	var sum float32
	for i, v := range logits {
		result[i] = float32(math.Exp(float64(v - maxVal)))
		sum += result[i]
	}
	for i := range result {
		result[i] /= sum
	}
	return result
}
