package birdnet

import (
	"context"
	"log/slog"
	"sync"
	"time"

	"github.com/tphakala/birdnet-go/internal/audiocore"
	"github.com/tphakala/birdnet-go/internal/birdnet"
	"github.com/tphakala/birdnet-go/internal/conf"
	"github.com/tphakala/birdnet-go/internal/datastore"
	"github.com/tphakala/birdnet-go/internal/errors"
	"github.com/tphakala/birdnet-go/internal/logging"
)

// BirdNETAnalyzer implements the audiocore.Analyzer interface for BirdNET
type BirdNETAnalyzer struct {
	id         string
	model      *birdnet.BirdNET
	config     audiocore.AnalyzerConfig
	bufferPool audiocore.BufferPool
	formatConv *FormatConverter

	// Performance optimization
	processingPool *sync.Pool

	// Metrics
	metrics *audiocore.MetricsCollector
	mu      sync.RWMutex
	logger  *slog.Logger

	// Statistics
	totalAnalyzed int64
	totalDetected int64
}

// ProcessingContext holds reusable buffers for processing
type ProcessingContext struct {
	floatBuffer []float32
}

// NewBirdNETAnalyzer creates a new BirdNET analyzer
func NewBirdNETAnalyzer(id string, config audiocore.AnalyzerConfig, bufferPool audiocore.BufferPool) (*BirdNETAnalyzer, error) {
	logger := logging.ForService("audiocore")
	if logger == nil {
		logger = slog.Default()
	}
	logger = logger.With(
		"component", "birdnet_analyzer",
		"analyzer_id", id)

	// Validate configuration
	if config.ModelPath == "" {
		return nil, errors.New(nil).
			Component(audiocore.ComponentAudioCore).
			Category(errors.CategoryConfiguration).
			Context("error", "model path not specified").
			Build()
	}

	// Extract additional configuration
	labelsPath, _ := config.ExtraConfig["labels_path"].(string)
	threads, _ := config.ExtraConfig["threads"].(int)
	if threads == 0 {
		threads = 1
	}

	// Load model
	logger.Info("loading BirdNET model",
		"model_path", config.ModelPath,
		"labels_path", labelsPath,
		"threads", threads)

	// Create settings for BirdNET
	// TODO: This needs to be refactored to use a simpler configuration
	// For now, create minimal settings
	settings := &conf.Settings{
		BirdNET: conf.BirdNETConfig{
			ModelPath: config.ModelPath,
			LabelPath: labelsPath,
			Threads:   threads,
		},
	}

	model, err := birdnet.NewBirdNET(settings)
	if err != nil {
		return nil, errors.New(err).
			Component(audiocore.ComponentAudioCore).
			Category(errors.CategoryConfiguration).
			Context("operation", "load_model").
			Context("model_path", config.ModelPath).
			Build()
	}

	// Calculate buffer size for processing context
	// 3 seconds at 48kHz = 144000 samples
	samplesPerChunk := int(config.ChunkDuration.Seconds() * 48000)

	return &BirdNETAnalyzer{
		id:         id,
		model:      model,
		config:     config,
		bufferPool: bufferPool,
		formatConv: NewFormatConverter(bufferPool),
		processingPool: &sync.Pool{
			New: func() any {
				return &ProcessingContext{
					floatBuffer: make([]float32, samplesPerChunk),
				}
			},
		},
		metrics: audiocore.GetMetrics(),
		logger:  logger,
	}, nil
}

// ID returns unique identifier for this analyzer
func (b *BirdNETAnalyzer) ID() string {
	return b.id
}

// Analyze processes audio data and returns results
func (b *BirdNETAnalyzer) Analyze(ctx context.Context, data *audiocore.AudioData) (audiocore.AnalysisResult, error) {
	startTime := time.Now()

	// Get processing context from pool
	procCtx := b.processingPool.Get().(*ProcessingContext)
	defer b.processingPool.Put(procCtx)

	// Convert to required format (float32 for BirdNET)
	err := b.formatConv.ConvertToFloat32(data.Buffer, procCtx.floatBuffer, data.Format)
	if err != nil {
		return audiocore.AnalysisResult{}, errors.New(err).
			Component(audiocore.ComponentAudioCore).
			Category(errors.CategoryAudio).
			Context("operation", "convert_format").
			Build()
	}

	// Run prediction with timeout
	predCtx, cancel := context.WithTimeout(ctx, b.config.ProcessingTimeout)
	defer cancel()

	// Create a channel to receive results
	resultChan := make(chan []datastore.Results, 1)
	errChan := make(chan error, 1)

	go func() {
		// BirdNET expects [][]float32, so wrap in a slice
		sample := [][]float32{procCtx.floatBuffer}
		results, err := b.model.Predict(sample)
		if err != nil {
			errChan <- err
			return
		}
		resultChan <- results
	}()

	// Wait for result or timeout
	var results []datastore.Results
	select {
	case results = <-resultChan:
		// Success
	case err := <-errChan:
		return audiocore.AnalysisResult{}, errors.New(err).
			Component(audiocore.ComponentAudioCore).
			Category(errors.CategoryProcessing).
			Context("operation", "predict").
			Build()
	case <-predCtx.Done():
		return audiocore.AnalysisResult{}, errors.New(predCtx.Err()).
			Component(audiocore.ComponentAudioCore).
			Category(errors.CategoryProcessing).
			Context("operation", "predict_timeout").
			Context("timeout", b.config.ProcessingTimeout).
			Build()
	}

	// Convert to standard format
	analysisResult := b.convertResults(results, data)

	// Update statistics
	b.mu.Lock()
	b.totalAnalyzed++
	b.totalDetected += int64(len(analysisResult.Detections))
	b.mu.Unlock()

	// Record metrics
	if b.metrics != nil {
		processingDuration := time.Since(startTime)
		b.metrics.RecordFrameProcessed("birdnet", b.id, processingDuration)
	}

	b.logger.Debug("analysis completed",
		"duration", time.Since(startTime),
		"detections", len(analysisResult.Detections))

	return analysisResult, nil
}

// GetRequiredFormat returns the audio format this analyzer needs
func (b *BirdNETAnalyzer) GetRequiredFormat() audiocore.AudioFormat {
	return audiocore.AudioFormat{
		SampleRate: 48000,
		Channels:   1,
		BitDepth:   32,
		Encoding:   "pcm_f32le",
	}
}

// GetConfiguration returns analyzer-specific configuration
func (b *BirdNETAnalyzer) GetConfiguration() audiocore.AnalyzerConfig {
	b.mu.RLock()
	defer b.mu.RUnlock()
	return b.config
}

// Close releases any resources
func (b *BirdNETAnalyzer) Close() error {
	b.logger.Info("closing analyzer",
		"total_analyzed", b.totalAnalyzed,
		"total_detected", b.totalDetected)

	// Close the BirdNET model to release TFLite interpreters
	if b.model != nil {
		if err := b.model.Close(); err != nil {
			return errors.New(err).
				Component(audiocore.ComponentAudioCore).
				Category(errors.CategoryResource).
				Context("operation", "close_model").
				Build()
		}
	}

	return nil
}

// convertResults converts BirdNET results to standard format
func (b *BirdNETAnalyzer) convertResults(results []datastore.Results, data *audiocore.AudioData) audiocore.AnalysisResult {
	detections := make([]audiocore.Detection, 0)

	// Filter by threshold
	for _, result := range results {
		if result.Confidence >= b.config.Threshold {
			detection := audiocore.Detection{
				Label:      result.Species,
				Confidence: result.Confidence,
				StartTime:  0.0, // BirdNET analyzes whole chunk
				EndTime:    data.Duration.Seconds(),
				Attributes: map[string]any{
					// Add any additional attributes if available
				},
			}
			detections = append(detections, detection)
		}
	}

	return audiocore.AnalysisResult{
		Timestamp:  data.Timestamp,
		Duration:   data.Duration,
		Detections: detections,
		Metadata: map[string]any{
			"model_version": b.config.ExtraConfig["model_version"],
			"chunk_size":    len(data.Buffer),
		},
		AnalyzerID: b.id,
		SourceID:   data.SourceID,
	}
}

// GetStatistics returns analyzer statistics
func (b *BirdNETAnalyzer) GetStatistics() map[string]int64 {
	b.mu.RLock()
	defer b.mu.RUnlock()

	return map[string]int64{
		"total_analyzed": b.totalAnalyzed,
		"total_detected": b.totalDetected,
		"average_detections_per_analysis": func() int64 {
			if b.totalAnalyzed == 0 {
				return 0
			}
			return b.totalDetected / b.totalAnalyzed
		}(),
	}
}

// UpdateThreshold updates the detection threshold
func (b *BirdNETAnalyzer) UpdateThreshold(threshold float32) {
	b.mu.Lock()
	defer b.mu.Unlock()

	b.config.Threshold = threshold
	b.logger.Info("detection threshold updated",
		"new_threshold", threshold)
}

// BirdNETAnalyzerFactory creates BirdNET analyzers
type BirdNETAnalyzerFactory struct {
	bufferPool audiocore.BufferPool
}

// NewBirdNETAnalyzerFactory creates a new BirdNET analyzer factory
func NewBirdNETAnalyzerFactory(bufferPool audiocore.BufferPool) *BirdNETAnalyzerFactory {
	return &BirdNETAnalyzerFactory{
		bufferPool: bufferPool,
	}
}

// CreateAnalyzer creates an analyzer from configuration
func (f *BirdNETAnalyzerFactory) CreateAnalyzer(id string, config audiocore.AnalyzerConfig) (audiocore.Analyzer, error) {
	return NewBirdNETAnalyzer(id, config, f.bufferPool)
}

// SupportedTypes returns list of supported analyzer types
func (f *BirdNETAnalyzerFactory) SupportedTypes() []string {
	return []string{"birdnet"}
}

// Ensure interfaces are implemented
var (
	_ audiocore.Analyzer        = (*BirdNETAnalyzer)(nil)
	_ audiocore.AnalyzerFactory = (*BirdNETAnalyzerFactory)(nil)
)