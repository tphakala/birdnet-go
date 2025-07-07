package birdnet

import (
	"context"
	"fmt"
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

	// Note: Empty ModelPath is allowed - it means use embedded model
	
	// Validate threshold range
	if config.Threshold < 0.0 || config.Threshold > 1.0 {
		return nil, errors.New(fmt.Errorf("invalid threshold value: %f", config.Threshold)).
			Component(audiocore.ComponentAudioCore).
			Category(errors.CategoryConfiguration).
			Context("operation", "validate_threshold").
			Context("threshold", config.Threshold).
			Context("analyzer_id", id).
			Context("valid_range", "0.0-1.0").
			Build()
	}

	// Extract additional configuration
	labelsPath, _ := config.ExtraConfig["labelPath"].(string)
	threads, _ := config.ExtraConfig["threads"].(int)
	if threads == 0 {
		threads = 1
	}

	// Load model
	modelDescription := config.ModelPath
	if modelDescription == "" {
		modelDescription = "embedded"
	}
	logger.Info("loading BirdNET model",
		"model_path", modelDescription,
		"labels_path", labelsPath,
		"threads", threads)

	// Create simplified BirdNET configuration
	birdnetConfig := &birdnet.Config{
		ModelPath:  config.ModelPath,
		LabelPath:  labelsPath,
		Threads:    threads,
		Locale:     getStringFromExtraConfig(config.ExtraConfig, "locale", "en"),
		UseXNNPACK: getBoolFromExtraConfig(config.ExtraConfig, "useXNNPACK", true),
	}
	
	// Store additional configuration for later use in analysis
	// These will be accessed during prediction

	model, err := birdnet.NewBirdNETFromConfig(birdnetConfig)
	if err != nil {
		return nil, errors.New(err).
			Component(audiocore.ComponentAudioCore).
			Category(errors.CategoryConfiguration).
			Context("operation", "load_model").
			Context("model_path", config.ModelPath).
			Context("analyzer_id", id).
			Context("analyzer_type", config.Type).
			Build()
	}

	// Calculate buffer size for processing context
	// Use the same sample rate as defined in conf/consts.go
	samplesPerChunk := int(config.ChunkDuration.Seconds() * float64(conf.SampleRate))

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
		
		// Check context before sending to avoid goroutine leak
		select {
		case <-predCtx.Done():
			// Context cancelled, exit without sending
			return
		default:
			// Context still active, send result
			if err != nil {
				select {
				case errChan <- err:
				case <-predCtx.Done():
					// Context cancelled while trying to send
				}
				return
			}
			select {
			case resultChan <- results:
			case <-predCtx.Done():
				// Context cancelled while trying to send
			}
		}
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
	
	// Add processing time to metadata
	processingDuration := time.Since(startTime)
	analysisResult.Metadata["processingTime"] = processingDuration

	// Update statistics
	b.mu.Lock()
	b.totalAnalyzed++
	b.totalDetected += int64(len(analysisResult.Detections))
	b.mu.Unlock()

	// Record metrics
	if b.metrics != nil {
		b.metrics.RecordFrameProcessed("birdnet", b.id, processingDuration)
	}

	b.logger.Debug("analysis completed",
		"duration", processingDuration,
		"detections", len(analysisResult.Detections))

	return analysisResult, nil
}

// GetRequiredFormat returns the audio format this analyzer needs
func (b *BirdNETAnalyzer) GetRequiredFormat() audiocore.AudioFormat {
	return audiocore.AudioFormat{
		SampleRate: conf.SampleRate,    // Use constant from conf package
		Channels:   conf.NumChannels,   // Use constant from conf package
		BitDepth:   32,                 // BirdNET expects float32 (32-bit float)
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
	detections := make([]audiocore.Detection, 0, len(results))

	// Filter by threshold and convert to detections
	for _, result := range results {
		if result.Confidence >= b.config.Threshold {
			detection := audiocore.Detection{
				// Store the original species string from BirdNET as the label
				// This is already in the format expected by the processor
				Label:      result.Species,
				Confidence: result.Confidence,
				StartTime:  0.0, // BirdNET analyzes whole chunk
				EndTime:    data.Duration.Seconds(),
				// Attributes can be nil - we don't need the extra data anymore
				Attributes: nil,
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

// Helper functions for extracting values from ExtraConfig
func getStringFromExtraConfig(config map[string]any, key, defaultValue string) string {
	if v, ok := config[key].(string); ok {
		return v
	}
	return defaultValue
}

func getBoolFromExtraConfig(config map[string]any, key string, defaultValue bool) bool {
	if v, ok := config[key].(bool); ok {
		return v
	}
	return defaultValue
}

