//go:build onnx

package classifier

import (
	"context"
	"fmt"
	"sync"
	"time"

	"github.com/tphakala/birdnet-go/internal/datastore"
	"github.com/tphakala/birdnet-go/internal/errors"
	"github.com/tphakala/birdnet-go/internal/inference"
	"github.com/tphakala/birdnet-go/internal/logger"
)

// Bat represents a loaded bat detection model that chains BirdNET v2.4
// embedding extraction with a custom bat species classifier.
// Implements ModelInstance. Goroutine-safe via internal mutex.
type Bat struct {
	embeddingExtractor inference.EmbeddingExtractor
	batClassifier      inference.CustomClassifier
	info               ModelInfo
	threshold          float64
	mu                 sync.Mutex
}

// BatModelConfig holds configuration for creating a Bat model instance.
type BatModelConfig struct {
	EmbeddingModelPath  string
	EmbeddingLabels     []string
	ClassifierModelPath string
	ClassifierLabelPath string
	ONNXRuntimePath     string
	Threads             int
	Threshold           float64
}

// NewBat creates a new bat detection model instance.
func NewBat(cfg *BatModelConfig) (*Bat, error) {
	log := GetLogger()

	if err := inference.InitONNXRuntime(cfg.ONNXRuntimePath); err != nil {
		return nil, errors.New(err).
			Category(errors.CategoryModelInit).
			Context("onnx_runtime_path", cfg.ONNXRuntimePath).
			Build()
	}

	embClassifier, err := inference.NewONNXClassifier(cfg.EmbeddingModelPath, inference.ONNXClassifierOptions{
		Labels:  cfg.EmbeddingLabels,
		Threads: cfg.Threads,
	})
	if err != nil {
		return nil, errors.New(err).
			Category(errors.CategoryModelInit).
			Context("embedding_model", cfg.EmbeddingModelPath).
			Build()
	}

	embExtractor, ok := embClassifier.(inference.EmbeddingExtractor)
	if !ok {
		embClassifier.Close()
		return nil, errors.Newf("embedding model does not support embedding extraction; ensure it has 2 outputs").
			Category(errors.CategoryModelInit).
			Context("embedding_model", cfg.EmbeddingModelPath).
			Build()
	}

	batCC, err := inference.NewONNXCustomClassifier(cfg.ClassifierModelPath, inference.ONNXCustomClassifierOptions{
		LabelsPath: cfg.ClassifierLabelPath,
		Threads:    cfg.Threads,
	})
	if err != nil {
		embClassifier.Close()
		return nil, errors.New(err).
			Category(errors.CategoryModelInit).
			Context("classifier_model", cfg.ClassifierModelPath).
			Build()
	}

	batLabels := batCC.Labels()
	info := ModelInfo{
		ID:          "Bat",
		Name:        "Bat Classifier",
		Description: fmt.Sprintf("Bat species detection with %d species", len(batLabels)),
		Spec:        ModelSpec{SampleRate: 48000, ClipLength: 3 * time.Second},
		NumSpecies:  len(batLabels),
	}

	log.Info("Bat detection model initialized",
		logger.String("embedding_model", cfg.EmbeddingModelPath),
		logger.String("classifier_model", cfg.ClassifierModelPath),
		logger.Int("bat_species", len(batLabels)))

	return &Bat{
		embeddingExtractor: embExtractor,
		batClassifier:      batCC,
		info:               info,
		threshold:          cfg.Threshold,
	}, nil
}

// Predict runs the two-stage bat detection pipeline: embedding extraction then bat classification.
func (b *Bat) Predict(ctx context.Context, samples [][]float32) ([]datastore.Results, error) {
	if len(samples) == 0 || len(samples[0]) == 0 {
		return nil, errors.Newf("empty audio sample").
			Category(errors.CategoryValidation).
			Build()
	}

	b.mu.Lock()
	defer b.mu.Unlock()

	if b.embeddingExtractor == nil || b.batClassifier == nil {
		return nil, errors.Newf("bat classifier is not initialized").
			Category(errors.CategoryModelInit).
			Build()
	}

	_, embeddings, err := b.embeddingExtractor.PredictWithEmbeddings(samples[0])
	if err != nil {
		return nil, errors.New(err).
			Category(errors.CategoryAudio).
			Context("model", "Bat").
			Context("stage", "embedding_extraction").
			Build()
	}

	if embeddings == nil {
		return nil, errors.Newf("embedding model did not produce embeddings").
			Category(errors.CategoryModelInit).
			Context("model", "Bat").
			Build()
	}

	scores, err := b.batClassifier.PredictEmbedding(embeddings)
	if err != nil {
		return nil, errors.New(err).
			Category(errors.CategoryAudio).
			Context("model", "Bat").
			Context("stage", "bat_classification").
			Build()
	}

	results, err := pairLabelsAndConfidence(b.batClassifier.Labels(), scores)
	if err != nil {
		return nil, err
	}

	if b.threshold > 0 {
		filtered := make([]datastore.Results, 0, len(results))
		for i := range results {
			if float64(results[i].Confidence) >= b.threshold {
				filtered = append(filtered, results[i])
			}
		}
		results = filtered
	}

	return getTopKResults(results, 10), nil
}

// Spec returns the audio requirements for the bat model.
func (b *Bat) Spec() ModelSpec { return b.info.Spec }

// ModelID returns the unique model identifier.
func (b *Bat) ModelID() string { return b.info.ID }

// ModelName returns the human-readable model name.
func (b *Bat) ModelName() string { return b.info.Name }

// ModelVersion returns the model version string.
func (b *Bat) ModelVersion() string { return "1.0" }

// NumSpecies returns the number of bat species.
func (b *Bat) NumSpecies() int {
	b.mu.Lock()
	defer b.mu.Unlock()
	if b.batClassifier == nil {
		return b.info.NumSpecies
	}
	return b.batClassifier.NumClasses()
}

// Labels returns the bat species labels.
func (b *Bat) Labels() []string {
	b.mu.Lock()
	defer b.mu.Unlock()
	if b.batClassifier == nil {
		return nil
	}
	return b.batClassifier.Labels()
}

// Close releases resources held by the bat model.
func (b *Bat) Close() error {
	b.mu.Lock()
	defer b.mu.Unlock()
	if b.batClassifier != nil {
		b.batClassifier.Close()
		b.batClassifier = nil
	}
	if b.embeddingExtractor != nil {
		b.embeddingExtractor.Close()
		b.embeddingExtractor = nil
	}
	return nil
}
