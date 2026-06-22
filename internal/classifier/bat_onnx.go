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
	// device is the compute device the bat pipeline bound to: the OpenVINO device
	// (CPU/GPU) when the heavy embedding extractor runs on OpenVINO, otherwise
	// deviceCPU for the ONNX Runtime CPU EP. The tiny bat classifier head always runs
	// on ORT CPU, so this reports the embedding extractor's device. Set once at
	// construction; reported via RuntimeInfo().
	device string
	// backend is the live execution backend of the embedding extractor
	// (BackendOpenVINO when it runs on OpenVINO, else BackendONNX), and precision is
	// the effective runtime precision (FP32 on the OpenVINO path, which the bat
	// embedding model is forced to; the weight precision detected from the classifier
	// model filename on the ORT path, empty when no token). Both set once at
	// construction; reported via RuntimeInfo().
	backend   string
	precision string
}

// BatModelConfig holds configuration for creating a Bat model instance.
type BatModelConfig struct {
	EmbeddingModelPath  string
	EmbeddingLabels     []string
	ClassifierModelPath string
	ClassifierLabelPath string
	ONNXRuntimePath     string
	Threads             int

	// OpenVINO opt-in, sourced from BirdNET settings: the bat pipeline reuses the
	// global BirdNET inference backend / OpenVINO device preference. When the gate
	// allows it, the heavy embedding extractor runs on OpenVINO (forced f32, since the
	// embedding head overflows at f16); the tiny bat classifier head always stays on
	// ORT. Any failure falls back to ORT.
	Backend        string // BirdNET.Backend ("auto"/"onnx"/"openvino")
	OpenVINOPath   string // BirdNET.OpenVINOPath (libopenvino_c location)
	OpenVINODevice string // BirdNET.OpenVINODevice ("auto"/"cpu"/"gpu")
}

// batEmbeddingOutputIndex is the output port of the bat embedding model
// (birdnet-v24-embeddings.onnx) that carries the embedding vector; port 0 is the
// species logits, which the bat pipeline discards. The selected port's element count
// is validated against the bat classifier's input dimension in tryBatOpenVINO, so a
// wrong index degrades to ORT rather than feeding a malformed embedding downstream.
const batEmbeddingOutputIndex = 1

// NewBat creates a new bat detection model instance.
func NewBat(cfg *BatModelConfig) (*Bat, error) {
	log := GetLogger()

	if err := inference.InitONNXRuntime(cfg.ONNXRuntimePath); err != nil {
		return nil, errors.New(err).
			Category(errors.CategoryModelInit).
			Context("onnx_runtime_path", cfg.ONNXRuntimePath).
			Build()
	}

	// Build the bat classifier head first. It always runs on ONNX Runtime (it is too
	// small to benefit from OpenVINO), and its input dimension is the embedding size
	// the upstream extractor must produce, which gates and validates the OpenVINO
	// embedding path below.
	batCC, err := inference.NewONNXCustomClassifier(cfg.ClassifierModelPath, inference.ONNXCustomClassifierOptions{
		LabelsPath: cfg.ClassifierLabelPath,
		Threads:    cfg.Threads,
	})
	if err != nil {
		return nil, errors.New(err).
			Category(errors.CategoryModelInit).
			Context("classifier_model", cfg.ClassifierModelPath).
			Build()
	}

	// Prefer the OpenVINO backend for the heavy embedding extractor when eligible
	// (same device gate as BirdNET v2.4), falling back to ORT on any failure.
	// OpenVINO must never make the bat model fail to load, so tryBatOpenVINO logs and
	// swallows OV errors and returns ok=false. device records the compute device the
	// extractor actually bound to (the OpenVINO device on the OV path).
	embExtractor, device, ok := tryBatOpenVINO(cfg, batCC.InputDim())
	// The bat embedding model is forced to f32 on every OpenVINO device, so the
	// effective OV runtime precision is FP32. The ORT path overrides all three below.
	backend := BackendOpenVINO
	precision := string(QuantizationFP32)
	if !ok {
		embClassifier, cerr := inference.NewONNXClassifier(cfg.EmbeddingModelPath, inference.ONNXClassifierOptions{
			Labels:              cfg.EmbeddingLabels,
			Threads:             cfg.Threads,
			SkipLabelValidation: true,
		})
		if cerr != nil {
			batCC.Close()
			return nil, errors.New(cerr).
				Category(errors.CategoryModelInit).
				Context("embedding_model", cfg.EmbeddingModelPath).
				Build()
		}

		ext, isExtractor := embClassifier.(inference.EmbeddingExtractor)
		if !isExtractor {
			embClassifier.Close()
			batCC.Close()
			return nil, errors.Newf("embedding model does not support embedding extraction; ensure it has 2 outputs").
				Category(errors.CategoryModelInit).
				Context("embedding_model", cfg.EmbeddingModelPath).
				Build()
		}
		embExtractor = ext
		// The embedding extractor and the classifier both run on the ONNX Runtime CPU
		// EP on this path. Surface the weight precision detected from the classifier
		// model filename (empty when no token, the common case for the bat model).
		device = deviceCPU
		backend = BackendONNX
		precision = string(detectQuantization(cfg.ClassifierModelPath))
	}

	batLabels := batCC.Labels()
	info := ModelRegistry[RegistryIDBat]
	info.Description = fmt.Sprintf("Bat species detection with %d species", len(batLabels))
	info.NumSpecies = len(batLabels)

	log.Info("Bat detection model initialized",
		logger.String("embedding_model", cfg.EmbeddingModelPath),
		logger.String("classifier_model", cfg.ClassifierModelPath),
		logger.String("backend", backend),
		logger.String("device", device),
		logger.Int("bat_species", len(batLabels)))

	return &Bat{
		embeddingExtractor: embExtractor,
		batClassifier:      batCC,
		info:               info,
		device:             device,
		backend:            backend,
		precision:          precision,
	}, nil
}

// tryBatOpenVINO attempts to build an OpenVINO embedding extractor for the bat
// pipeline's heavy embedding model. It returns (extractor, device, true) on success
// or (nil, "", false) to fall back to ORT, where device is the concrete OpenVINO
// device the extractor bound to (inference.OVDeviceCPU/OVDeviceGPU). Any failure
// (gate denied, init/compile/validation error) is logged and swallowed: OpenVINO
// must never make the bat model fail to load. The device gate matches BirdNET v2.4;
// the bat embedding model is forced to f32 on every device by
// openVINOPrecisionFor(RegistryIDBat, ...) because its embedding head overflows at
// f16. expectedDim is the bat classifier's input dimension, used to reject a wrong
// output port before any inference.
func tryBatOpenVINO(cfg *BatModelConfig, expectedDim int) (inference.EmbeddingExtractor, string, bool) {
	plan, ok, reason := openVINOPlanFor(cfg.Backend, cfg.OpenVINODevice, RegistryIDBat, cfg.OpenVINOPath, batEmbeddingOutputIndex)
	if !ok {
		logOpenVINODeclined(RegistryIDBat, cfg.Backend, reason)
		return nil, "", false
	}

	log := GetLogger()
	// InitOpenVINO is idempotent: the auto/GPU plan above may already have loaded the
	// core to enumerate devices, but the explicit-CPU plan path does not, so init here
	// to cover it. A load failure means no usable OpenVINO; fall back to ORT.
	if err := inference.InitOpenVINO(cfg.OpenVINOPath); err != nil {
		log.Warn("Bat OpenVINO init failed; using ONNX Runtime", logger.Error(err))
		return nil, "", false
	}

	start := time.Now()
	extractor, err := inference.NewOpenVINOEmbeddingExtractor(cfg.EmbeddingModelPath, inference.OpenVINOEmbeddingExtractorOptions{
		Threads:       cfg.Threads,
		Device:        plan.device,
		OutputIndex:   plan.outputIndex,
		PrecisionHint: plan.precision, // f32 for the bat embedding model on every device
		ExpectedDim:   expectedDim,
	})
	if err != nil {
		log.Warn("Bat OpenVINO embedding extractor init failed; using ONNX Runtime",
			logger.String("device", plan.device),
			logger.Error(err))
		return nil, "", false
	}

	log.Info("Bat embedding extractor using OpenVINO backend",
		logger.String("device", plan.device),
		logger.String("precision", openVINOPrecisionLabel(plan.precision)),
		logger.Int("embedding_dim", extractor.NumSpecies()),
		logger.String("init_time", time.Since(start).String()))
	return extractor, plan.device, true
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

// RuntimeInfo returns the device, backend, and effective precision the bat
// pipeline bound to at construction: the OpenVINO device (CPU/GPU) when the
// embedding extractor runs on OpenVINO, else "CPU" for the ONNX Runtime CPU EP;
// BackendOpenVINO on the OV path (else BackendONNX); FP32 on the OV path (which
// the bat embedding model is forced to), or the weight precision detected from
// the bat classifier model filename on the ORT path (empty when no token). All
// three are set once at construction and never mutated, so no lock is needed.
// Implements ModelInstance.
func (b *Bat) RuntimeInfo() (device, backend, precision string) {
	return b.device, b.backend, b.precision
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
