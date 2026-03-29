//go:build onnx

// perch_onnx.go provides Perch v2 model support using the ONNX backend.
package classifier

import (
	"context"
	"fmt"
	"os"
	"sync"
	"time"

	"github.com/tphakala/birdnet-go/internal/datastore"
	"github.com/tphakala/birdnet-go/internal/errors"
	"github.com/tphakala/birdnet-go/internal/inference"
	"github.com/tphakala/birdnet-go/internal/logger"
)

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
}

// NewPerch creates a new Perch v2 model instance.
func NewPerch(cfg PerchConfig) (*Perch, error) {
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

	// Initialize ONNX Runtime
	if err := inference.InitONNXRuntime(cfg.ONNXRuntimePath); err != nil {
		return nil, errors.New(err).
			Category(errors.CategoryModelInit).
			Context("onnx_runtime_path", cfg.ONNXRuntimePath).
			Build()
	}

	// Create ONNX classifier
	classifier, err := inference.NewONNXClassifier(cfg.ModelPath, inference.ONNXClassifierOptions{
		Labels:  labels,
		Threads: cfg.Threads,
	})
	if err != nil {
		return nil, errors.New(err).
			Category(errors.CategoryModelInit).
			Context("model_path", cfg.ModelPath).
			Context("label_count", len(labels)).
			Build()
	}

	info := ModelInfo{
		ID:          "Perch_V2",
		Name:        "Google Perch V2",
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

// Predict runs inference on the given audio samples.
// Implements ModelInstance.
func (p *Perch) Predict(ctx context.Context, samples [][]float32) ([]datastore.Results, error) {
	if len(samples) == 0 || len(samples[0]) == 0 {
		return nil, errors.Newf("empty audio sample").
			Category(errors.CategoryValidation).
			Build()
	}

	p.mu.Lock()
	defer p.mu.Unlock()

	if p.classifier == nil {
		return nil, errors.Newf("Perch classifier is not initialized").
			Category(errors.CategoryModelInit).
			Build()
	}

	predictions, err := p.classifier.Predict(samples[0])
	if err != nil {
		return nil, errors.New(err).
			Category(errors.CategoryAudio).
			Context("model", "Perch_V2").
			Build()
	}

	// Pair labels with predictions
	results, err := pairLabelsAndConfidence(p.labels, predictions)
	if err != nil {
		return nil, err
	}

	// Return top 10 results
	return getTopKResults(results, 10), nil
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
