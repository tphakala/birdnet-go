package classifier

import (
	"context"
	"fmt"
	"sync"
	"time"

	"github.com/tphakala/birdnet-go/internal/conf"
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
		Labels:              cfg.EmbeddingLabels,
		Threads:             cfg.Threads,
		SkipLabelValidation: true,
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
	info := ModelRegistry[RegistryIDBat]
	info.Description = fmt.Sprintf("Bat species detection with %d species", len(batLabels))
	info.NumSpecies = len(batLabels)

	log.Info("Bat detection model initialized",
		logger.String("embedding_model", cfg.EmbeddingModelPath),
		logger.String("classifier_model", cfg.ClassifierModelPath),
		logger.Int("bat_species", len(batLabels)))

	return &Bat{
		embeddingExtractor: embExtractor,
		batClassifier:      batCC,
		info:               info,
	}, nil
}

// Predict runs the two-stage bat detection pipeline: embedding extraction then bat classification.
func (b *Bat) Predict(ctx context.Context, samples [][]float32) ([]datastore.Results, error) {
	log := GetLogger()

	span, _ := startPredictSpan(ctx, RegistryIDBat, samples)
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

	b.mu.Lock()
	defer b.mu.Unlock()

	if b.embeddingExtractor == nil || b.batClassifier == nil {
		span.markErrored(errTypeClassifierNil)
		return nil, errors.Newf("bat classifier is not initialized").
			Category(errors.CategoryModelInit).
			Build()
	}

	log.Debug("bat predict starting",
		logger.Int("sample_len", len(samples[0])),
		logger.Int("chunks", len(samples)))

	embStart := time.Now()
	_, embeddings, err := b.embeddingExtractor.PredictWithEmbeddings(samples[0])
	embDuration := time.Since(embStart)
	if err != nil {
		log.Error("bat embedding extraction failed",
			logger.Error(err),
			logger.Duration("duration", embDuration))
		err = errors.New(err).
			Category(errors.CategoryAudio).
			Context("model", RegistryIDBat).
			Context("stage", "embedding_extraction").
			Build()
		recordPredictionFailure(span, RegistryIDBat, errTypeEmbeddingExtraction, start, err)
		return nil, err
	}

	if embeddings == nil {
		log.Error("bat embedding model produced nil embeddings")
		err = errors.Newf("embedding model did not produce embeddings").
			Category(errors.CategoryModelInit).
			Context("model", RegistryIDBat).
			Build()
		recordPredictionFailure(span, RegistryIDBat, errTypeNilEmbeddings, start, err)
		return nil, err
	}

	log.Debug("bat embeddings extracted",
		logger.Int("embedding_dim", len(embeddings)),
		logger.Duration("duration", embDuration))

	classStart := time.Now()
	scores, err := b.batClassifier.PredictEmbedding(embeddings)
	classDuration := time.Since(classStart)
	if err != nil {
		log.Error("bat classification failed",
			logger.Error(err),
			logger.Duration("duration", classDuration))
		err = errors.New(err).
			Category(errors.CategoryAudio).
			Context("model", RegistryIDBat).
			Context("stage", "bat_classification").
			Build()
		recordPredictionFailure(span, RegistryIDBat, errTypeBatClassification, start, err)
		return nil, err
	}

	log.Debug("bat classification complete",
		logger.Int("score_count", len(scores)),
		logger.Duration("duration", classDuration))

	results, err := pairLabelsAndConfidence(b.batClassifier.Labels(), scores)
	if err != nil {
		recordPredictionFailure(span, RegistryIDBat, errTypeLabelMismatch, start, err)
		return nil, err
	}

	threshold := conf.Setting().Bat.Threshold
	preFilterCount := len(results)
	if threshold > 0 {
		filtered := make([]datastore.Results, 0, len(results))
		for i := range results {
			if float64(results[i].Confidence) >= threshold {
				filtered = append(filtered, results[i])
			}
		}
		results = filtered
	}

	// Sort and trim before logging so top_species reflects the highest-confidence
	// detection rather than the first label that cleared the threshold (results is
	// in label order, not confidence order).
	topResults := getTopKResults(results, defaultTopKResults)
	if len(topResults) > 0 {
		log.Debug("bat detections after threshold",
			logger.Int("pre_filter", preFilterCount),
			logger.Int("post_filter", len(results)),
			logger.Float64("threshold", threshold),
			logger.String("top_species", topResults[0].Species),
			logger.Float64("top_confidence", float64(topResults[0].Confidence)),
			logger.Duration("total_duration", embDuration+classDuration))
	} else {
		log.Debug("bat no detections above threshold",
			logger.Int("pre_filter", preFilterCount),
			logger.Float64("threshold", threshold),
			logger.Duration("total_duration", embDuration+classDuration))
	}

	// Success: Finish records the single prediction because the span is not errored.
	recordPredictionSuccess(span, len(topResults), start)

	return topResults, nil
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
