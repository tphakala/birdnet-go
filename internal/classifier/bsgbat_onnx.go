// bsgbat_onnx.go provides BSG-BAT bat classifier support on ONNX Runtime.
package classifier

import (
	"context"
	"fmt"
	"os"
	"slices"
	"strings"
	"sync"
	"time"

	"github.com/tphakala/birdnet-go/internal/datastore"
	"github.com/tphakala/birdnet-go/internal/errors"
	"github.com/tphakala/birdnet-go/internal/inference"
	"github.com/tphakala/birdnet-go/internal/inference/bsgbat"
	"github.com/tphakala/birdnet-go/internal/logger"
)

// bsgBatSigmoidSensitivity is the slope of the sigmoid that turns a BSG-BAT logit
// into a confidence. The model is multi-label (trained with a per-class sigmoid), so
// the plain logistic function applies.
const bsgBatSigmoidSensitivity = 1.0

// BSGBat is a loaded BSG-BAT bat classifier: the go-audio-dsp mel front end and
// per-window normalization (internal/inference/bsgbat) feeding a spectrogram CNN.
// It implements ModelInstance and is safe for concurrent use through its mutex.
//
// Every window is scored on its own, with no state carried between calls, so a
// restart, a gap or a probe on silence reproduces the same scores.
type BSGBat struct {
	model    inference.TensorModel
	frontEnd *bsgbat.FrontEnd
	// labels are the species labels only. The network's Background class is not a
	// species, so it is dropped at load and its position kept in backgroundIndex to
	// skip its logit.
	labels          []string
	backgroundIndex int
	info            ModelInfo
	mu              sync.Mutex
	// modelPath is the model file this instance loaded from. Set once at
	// construction; reported via ResolvedModelPath().
	modelPath string
}

// BSGBatModelConfig holds configuration for creating a BSG-BAT model instance.
type BSGBatModelConfig struct {
	ModelPath       string // Path to the BSG-BAT ONNX model file
	LabelPath       string // Path to the BSG-BAT label file (species, then Background)
	ONNXRuntimePath string // Path to the ONNX Runtime shared library
	Threads         int    // CPU threads for inference (0 = default)
}

// parseBSGBatLabels reads the BSG-BAT label file: one name per line, the species
// followed by the Background class. It returns the species labels and the position
// of Background in the model output. A file without exactly one Background line, or
// without any species, is rejected.
func parseBSGBatLabels(data []byte) (species []string, backgroundIndex int, err error) {
	var all []string
	for line := range strings.SplitSeq(string(data), "\n") {
		if name := strings.TrimSpace(line); name != "" {
			all = append(all, name)
		}
	}
	backgroundIndex = -1
	for i, name := range all {
		if name == bsgbat.BackgroundLabel {
			if backgroundIndex >= 0 {
				return nil, 0, fmt.Errorf("label file has more than one %q class", bsgbat.BackgroundLabel)
			}
			backgroundIndex = i
			continue
		}
		species = append(species, name)
	}
	if backgroundIndex < 0 {
		return nil, 0, fmt.Errorf("label file has no %q class", bsgbat.BackgroundLabel)
	}
	if len(species) == 0 {
		return nil, 0, fmt.Errorf("label file has no species")
	}
	return species, backgroundIndex, nil
}

// NewBSGBat creates a new BSG-BAT model instance.
func NewBSGBat(cfg *BSGBatModelConfig) (*BSGBat, error) {
	labelData, err := os.ReadFile(cfg.LabelPath)
	if err != nil {
		return nil, errors.New(err).
			Category(errors.CategoryFileIO).
			Context("label_path", cfg.LabelPath).
			Build()
	}
	labels, backgroundIndex, err := parseBSGBatLabels(labelData)
	if err != nil {
		return nil, errors.New(err).
			Category(errors.CategoryLabelLoad).
			Context("label_path", cfg.LabelPath).
			Build()
	}

	if err := inference.InitONNXRuntime(cfg.ONNXRuntimePath); err != nil {
		return nil, errors.New(err).
			Category(errors.CategoryModelInit).
			Context("onnx_runtime_path", cfg.ONNXRuntimePath).
			Build()
	}

	model, err := inference.NewONNXTensorModel(cfg.ModelPath, inference.ONNXTensorModelOptions{Threads: cfg.Threads})
	if err != nil {
		return nil, errors.New(err).
			Category(errors.CategoryModelInit).
			Context("model_path", cfg.ModelPath).
			Build()
	}
	if err := checkBSGBatShapes(model, len(labels)+1); err != nil {
		model.Close()
		return nil, errors.New(err).
			Category(errors.CategoryModelInit).
			Context("model_path", cfg.ModelPath).
			Build()
	}

	frontEnd, err := bsgbat.NewFrontEnd()
	if err != nil {
		model.Close()
		return nil, errors.New(err).
			Category(errors.CategoryModelInit).
			Build()
	}

	info := ModelRegistry[RegistryIDBSGBat]
	info.Description = fmt.Sprintf("BSG-BAT bat classifier with %d species", len(labels))
	info.NumSpecies = len(labels)

	GetLogger().Info("BSG-BAT model initialized",
		logger.String("model_path", cfg.ModelPath),
		logger.Int("species", len(labels)))

	return &BSGBat{
		model:           model,
		frontEnd:        frontEnd,
		labels:          labels,
		backgroundIndex: backgroundIndex,
		info:            info,
		modelPath:       cfg.ModelPath,
	}, nil
}

// checkBSGBatShapes verifies the loaded graph takes exactly one [1, 1, 512, 128]
// spectrogram (the layout, not only the element count) and returns one logit per
// label, Background included.
func checkBSGBatShapes(model inference.TensorModel, logitCount int) error {
	shape := model.InputShape()
	want := []int64{1, 1, bsgbat.NumFrames, bsgbat.NumMels}
	if !slices.Equal(shape, want) {
		return fmt.Errorf("BSG-BAT model input shape %v, expected %v", shape, want)
	}
	sizes := model.OutputSizes()
	if len(sizes) == 0 || sizes[0] != logitCount {
		return fmt.Errorf("BSG-BAT model output sizes %v do not match %d labels", sizes, logitCount)
	}
	return nil
}

// Predict scores one window of bsgbat.ClipSamples samples at the model's sample rate
// and returns the top-K species. The confidence threshold is not applied here: the
// processor applies it per detection after the per-species thresholds.
// Implements ModelInstance.
func (b *BSGBat) Predict(ctx context.Context, samples [][]float32) ([]datastore.Results, error) {
	span, _ := startPredictSpan(ctx, RegistryIDBSGBat, samples)
	defer span.Finish()

	start := time.Now()

	if len(samples) == 0 || len(samples[0]) == 0 {
		span.markErrored(errTypeEmptySample)
		return nil, errors.Newf("empty audio sample").
			Category(errors.CategoryValidation).
			Build()
	}

	b.mu.Lock()
	defer b.mu.Unlock()

	if b.model == nil {
		span.markErrored(errTypeClassifierNil)
		return nil, errors.Newf("BSG-BAT classifier is not initialized").
			Category(errors.CategoryModelInit).
			Build()
	}

	logits, err := b.run(samples[0])
	if err != nil {
		err = errors.New(err).
			Category(errors.CategoryAudio).
			Context("model", RegistryIDBSGBat).
			Build()
		recordPredictionFailure(span, RegistryIDBSGBat, errTypeInvokeFailed, start, err)
		return nil, err
	}

	if idx := firstNonFinite(logits); idx != noNonFiniteScore {
		err = newNonFiniteScoreError(nonFiniteScore{modelID: RegistryIDBSGBat, index: idx, count: len(logits)}, b.RuntimeInfo)
		recordPredictionFailure(span, RegistryIDBSGBat, errTypeNonFiniteLogits, start, err)
		return nil, err
	}

	// Multi-label sigmoid over the species logits; the Background logit is skipped.
	scores := make([]float32, 0, len(b.labels))
	for i, logit := range logits {
		if i == b.backgroundIndex {
			continue
		}
		scores = append(scores, float32(customSigmoid(float64(logit), bsgBatSigmoidSensitivity)))
	}

	results, err := pairLabelsAndConfidence(b.labels, scores)
	if err != nil {
		recordPredictionFailure(span, RegistryIDBSGBat, errTypeLabelMismatch, start, err)
		return nil, err
	}

	topResults := getTopKResults(results, defaultTopKResults)
	recordPredictionSuccess(span, len(topResults), start)
	return topResults, nil
}

// run computes the model input for one window and returns the raw logits. The
// caller holds b.mu.
func (b *BSGBat) run(window []float32) ([]float32, error) {
	input, err := b.frontEnd.Window(window)
	if err != nil {
		return nil, err
	}
	outputs, err := b.model.Run(input)
	if err != nil {
		return nil, err
	}
	if len(outputs) == 0 || len(outputs[0]) != len(b.labels)+1 {
		return nil, fmt.Errorf("BSG-BAT model returned an unexpected output shape")
	}
	return outputs[0], nil
}

// Spec returns the audio requirements for BSG-BAT.
func (b *BSGBat) Spec() ModelSpec { return b.info.Spec }

// ModelID returns the unique model identifier.
func (b *BSGBat) ModelID() string { return b.info.ID }

// ModelName returns the human-readable model name.
func (b *BSGBat) ModelName() string { return b.info.Name }

// ModelVersion returns the model version string.
func (b *BSGBat) ModelVersion() string { return b.info.DetectionVersion }

// NumSpecies returns the number of species, without the Background class.
func (b *BSGBat) NumSpecies() int { return len(b.labels) }

// Labels returns a copy of the species labels, without the Background class, so
// the species index and the consensus filter never see it.
func (b *BSGBat) Labels() []string {
	out := make([]string, len(b.labels))
	copy(out, b.labels)
	return out
}

// RuntimeInfo returns the device, backend and precision: BSG-BAT always runs
// FP32 on the ONNX Runtime CPU execution provider. Implements ModelInstance.
func (b *BSGBat) RuntimeInfo() (device, backend, precision string) {
	return deviceCPU, BackendONNX, string(QuantizationFP32)
}

// ResolvedModelPath returns the model file this instance loaded from. Fixed at
// construction, so the read needs no lock. Implements ModelInstance.
func (b *BSGBat) ResolvedModelPath() string { return b.modelPath }

// Close releases resources held by the BSG-BAT model.
func (b *BSGBat) Close() error {
	b.mu.Lock()
	defer b.mu.Unlock()
	if b.model != nil {
		b.model.Close()
		b.model = nil
	}
	return nil
}
