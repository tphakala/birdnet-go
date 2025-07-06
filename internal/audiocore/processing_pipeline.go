package audiocore

import (
	"context"
	"log/slog"
	"sync"
	"time"

	"github.com/tphakala/birdnet-go/internal/errors"
	"github.com/tphakala/birdnet-go/internal/logging"
)

// ProcessingPipeline manages the flow from source to analyzer
// Flow: AudioSource -> ChunkBuffer -> OverlapBuffer -> ProcessorChain -> Analyzer
// Supports: capture tee, health monitoring, metrics collection
//
// Buffer lifecycle:
//   - AudioSource allocates buffers from BufferPool
//   - ChunkBuffer copies data (can't use pool due to AudioData limitations)
//   - OverlapBuffer uses pool for temporary buffers, releases after use
//   - Analyzer receives owned data, responsible for cleanup
type ProcessingPipeline struct {
	source         AudioSource
	analyzer       Analyzer
	processorChain ProcessorChain
	overlapBuffer  *OverlapBuffer
	bufferPool     BufferPool
	config         ProcessingConfig

	// Chunk management
	chunkBuffer *ChunkBufferV2

	// Capture support
	captureManager CaptureManager

	// Metrics and monitoring
	healthMonitor *AudioHealthMonitor
	metrics       *MetricsCollector

	// Control
	ctx    context.Context
	cancel context.CancelFunc
	mu     sync.RWMutex
	wg     sync.WaitGroup
	logger *slog.Logger

	// Performance tracking
	processedChunks int64
	droppedChunks   int64
}

// ProcessingPipelineConfig holds configuration for creating a pipeline
type ProcessingPipelineConfig struct {
	Source         AudioSource
	Analyzer       Analyzer
	ProcessorChain ProcessorChain
	BufferPool     BufferPool
	Config         ProcessingConfig
	Metrics        *MetricsCollector
	HealthMonitor  *AudioHealthMonitor
	CaptureManager CaptureManager
}

// NewProcessingPipeline creates a new processing pipeline
func NewProcessingPipeline(config *ProcessingPipelineConfig) *ProcessingPipeline {
	// Validate configuration first
	if err := validatePipelineConfig(config); err != nil {
		logger := logging.ForService("audiocore")
		if logger == nil {
			logger = slog.Default()
		}
		logger.Error("invalid pipeline configuration", "error", err)
		// Return nil to fail fast on invalid config
		return nil
	}

	logger := logging.ForService("audiocore")
	if logger == nil {
		logger = slog.Default()
	}
	logger = logger.With(
		"component", "processing_pipeline",
		"source_id", config.Source.ID(),
		"analyzer_id", config.Analyzer.ID())

	// Create overlap buffer
	overlapBuffer := NewOverlapBuffer(&OverlapBufferConfig{
		SourceID:       config.Source.ID(),
		OverlapPercent: config.Config.OverlapPercent,
		BufferPool:     config.BufferPool,
		Format:         config.Source.GetFormat(),
	})

	// Create chunk buffer
	chunkBuffer := NewChunkBufferV2(ChunkBufferConfig{
		ChunkDuration: config.Config.ChunkDuration,
		Format:        config.Source.GetFormat(),
		BufferPool:    config.BufferPool,
	})

	return &ProcessingPipeline{
		source:         config.Source,
		analyzer:       config.Analyzer,
		processorChain: config.ProcessorChain,
		overlapBuffer:  overlapBuffer,
		bufferPool:     config.BufferPool,
		config:         config.Config,
		chunkBuffer:    chunkBuffer,
		captureManager: config.CaptureManager,
		healthMonitor:  config.HealthMonitor,
		metrics:        config.Metrics,
		logger:         logger,
	}
}

// Start begins processing audio data
func (p *ProcessingPipeline) Start(ctx context.Context) error {
	p.mu.Lock()
	defer p.mu.Unlock()

	if p.ctx != nil {
		return errors.New(nil).
			Component(ComponentAudioCore).
			Category(errors.CategoryState).
			Context("error", "pipeline already started").
			Build()
	}

	p.ctx, p.cancel = context.WithCancel(ctx)

	// Start processing goroutine
	p.wg.Add(1)
	go p.processLoop()

	// Start health monitoring if configured
	if p.healthMonitor != nil {
		p.wg.Add(1)
		go p.monitorHealth()
	}

	p.logger.Info("processing pipeline started")
	return nil
}

// Stop halts the processing pipeline
func (p *ProcessingPipeline) Stop() error {
	p.mu.Lock()
	cancel := p.cancel
	p.mu.Unlock()

	if cancel == nil {
		return errors.New(nil).
			Component(ComponentAudioCore).
			Category(errors.CategoryState).
			Context("error", "pipeline not started").
			Build()
	}

	// Cancel context
	cancel()

	// Wait for goroutines to finish
	p.wg.Wait()

	// Clean up
	p.mu.Lock()
	p.ctx = nil
	p.cancel = nil
	p.mu.Unlock()

	p.logger.Info("processing pipeline stopped",
		"processed_chunks", p.processedChunks,
		"dropped_chunks", p.droppedChunks)

	return nil
}

// processLoop is the main processing loop
// Data flow: source -> capture tee -> chunk buffer -> overlap -> processor chain -> analyzer
func (p *ProcessingPipeline) processLoop() {
	defer p.wg.Done()
	
	// Panic recovery
	defer func() {
		if r := recover(); r != nil {
			p.logger.Error("panic in process loop",
				"panic", r,
				"source_id", p.source.ID())
			if p.metrics != nil {
				p.metrics.RecordProcessingError("pipeline", p.source.ID(), "panic")
			}
		}
	}()

	// Buffered channel for decoupling chunk processing from analysis
	analyzeChan := make(chan AudioData, p.config.BufferAhead)
	defer close(analyzeChan)

	// Start analyzer goroutine
	p.wg.Add(1)
	go p.analyzeLoop(analyzeChan)

	for {
		select {
		case data := <-p.source.AudioOutput():
			// Tee to capture buffer if enabled (for saving clips)
			if p.captureManager != nil {
				if err := p.captureManager.Write(data.SourceID, &data); err != nil {
					p.logger.Debug("failed to write to capture buffer",
						"error", err,
						"source_id", data.SourceID)
				}
			}
			
			// Accumulate data into fixed-size chunks
			p.chunkBuffer.Add(&data)

			// Process all complete chunks
			p.processChunks(analyzeChan)

		case <-p.ctx.Done():
			return
		}
	}
}

// processChunks processes all complete chunks from the buffer
func (p *ProcessingPipeline) processChunks(analyzeChan chan<- AudioData) {
	for p.chunkBuffer.HasCompleteChunk() {
		// Check context before processing each chunk
		select {
		case <-p.ctx.Done():
			return
		default:
		}

		chunk := p.chunkBuffer.GetChunk()
		if chunk == nil {
			continue
		}

		// Process the chunk through the pipeline
		processedChunk := p.processChunk(chunk)
		if processedChunk == nil {
			continue
		}

		// Send to analyzer with backpressure handling
		p.sendToAnalyzer(analyzeChan, processedChunk)
	}
}

// processChunk runs a chunk through overlap and processor chain
func (p *ProcessingPipeline) processChunk(chunk *AudioData) *AudioData {
	// Apply overlap
	processedChunk, err := p.overlapBuffer.Process(chunk)
	if err != nil {
		p.logger.Warn("overlap processing failed", "error", err)
		if p.metrics != nil {
			p.metrics.RecordProcessingError("pipeline", p.source.ID(), "overlap_error")
		}
		return nil
	}

	// Run through processor chain if configured
	if p.processorChain != nil {
		processedChunk, err = p.processorChain.Process(p.ctx, processedChunk)
		if err != nil {
			p.logger.Warn("processor chain failed", "error", err)
			if p.metrics != nil {
				p.metrics.RecordProcessingError("pipeline", p.source.ID(), "processor_error")
			}
			return nil
		}
	}

	return processedChunk
}

// sendToAnalyzer sends chunk to analyzer with backpressure handling
func (p *ProcessingPipeline) sendToAnalyzer(analyzeChan chan<- AudioData, chunk *AudioData) {
	select {
	case analyzeChan <- *chunk:
		p.processedChunks++
	default:
		// Buffer full - implement backpressure
		p.droppedChunks++
		
		// Log warning every 10 dropped chunks to avoid log spam
		if p.droppedChunks%10 == 1 {
			p.logger.Warn("analyzer buffer full, applying backpressure",
				"dropped_chunks", p.droppedChunks,
				"drop_rate", float64(p.droppedChunks)/float64(p.processedChunks+p.droppedChunks))
		}
		
		if p.metrics != nil {
			p.metrics.RecordFrameDropped(p.source.ID(), "analyzer_buffer_full")
		}
		
		// Apply backpressure: skip processing for a brief period
		// This gives the analyzer time to catch up
		select {
		case <-time.After(10 * time.Millisecond):
		case <-p.ctx.Done():
			return
		}
	}
}

// analyzeLoop processes chunks through the analyzer
func (p *ProcessingPipeline) analyzeLoop(chunks <-chan AudioData) {
	defer p.wg.Done()
	
	// Panic recovery
	defer func() {
		if r := recover(); r != nil {
			p.logger.Error("panic in analyze loop",
				"panic", r,
				"analyzer_id", p.analyzer.ID())
			if p.metrics != nil {
				p.metrics.RecordProcessingError("pipeline", p.source.ID(), "analyzer_panic")
			}
		}
	}()

	for {
		select {
		case chunk, ok := <-chunks:
			if !ok {
				return
			}

			// Run analysis with timeout
			analyzeCtx, cancel := context.WithTimeout(p.ctx, p.config.ChunkDuration)
			result, err := p.analyzer.Analyze(analyzeCtx, &chunk)
			cancel()

			if err != nil {
				p.logger.Warn("analysis failed",
					"error", err)
				if p.metrics != nil {
					p.metrics.RecordProcessingError("pipeline", p.source.ID(), "analyzer_error")
				}
				continue
			}

			// Process results
			p.processAnalysisResult(&result)

		case <-p.ctx.Done():
			return
		}
	}
}

// processAnalysisResult handles the output from the analyzer
func (p *ProcessingPipeline) processAnalysisResult(result *AnalysisResult) {
	// Log detections
	if len(result.Detections) > 0 {
		p.logger.Info("detections found",
			"count", len(result.Detections),
			"timestamp", result.Timestamp)

		for _, detection := range result.Detections {
			p.logger.Debug("detection",
				"label", detection.Label,
				"confidence", detection.Confidence,
				"start_time", detection.StartTime,
				"end_time", detection.EndTime)
		}
	}

	// TODO: Send results to detection handler/database
}

// monitorHealth monitors the health of the pipeline
func (p *ProcessingPipeline) monitorHealth() {
	defer p.wg.Done()

	ticker := time.NewTicker(5 * time.Second)
	defer ticker.Stop()

	for {
		select {
		case <-ticker.C:
			// Check pipeline health
			p.mu.RLock()
			processed := p.processedChunks
			dropped := p.droppedChunks
			p.mu.RUnlock()

			if dropped > 0 {
				dropRate := float64(dropped) / float64(processed+dropped)
				if dropRate > 0.05 { // More than 5% drop rate
					p.logger.Warn("high chunk drop rate detected",
						"drop_rate", dropRate,
						"processed", processed,
						"dropped", dropped)
				}
			}

		case <-p.ctx.Done():
			return
		}
	}
}

// GetMetrics returns current pipeline metrics
func (p *ProcessingPipeline) GetMetrics() map[string]any {
	p.mu.RLock()
	defer p.mu.RUnlock()

	return map[string]any{
		"source_id":        p.source.ID(),
		"analyzer_id":      p.analyzer.ID(),
		"processed_chunks": p.processedChunks,
		"dropped_chunks":   p.droppedChunks,
		"drop_rate":        float64(p.droppedChunks) / float64(p.processedChunks+p.droppedChunks),
	}
}


// OverlapBuffer handles chunk overlap for continuous processing
// Creates sliding window by: saving last N bytes of each chunk, prepending to next chunk
// Essential for: detecting events at chunk boundaries, smooth analysis continuity
type OverlapBuffer struct {
	sourceID      string
	overlapData   AudioBuffer  // saved data from previous chunk
	overlapSize   int         // overlap as percentage (0-100)
	overlapBytes  int         // calculated bytes to overlap
	bufferPool    BufferPool
	format        AudioFormat
	mu            sync.Mutex
}

// OverlapBufferConfig holds configuration for overlap buffer
type OverlapBufferConfig struct {
	SourceID       string
	OverlapPercent float64
	BufferPool     BufferPool
	Format         AudioFormat
}

// NewOverlapBuffer creates a new overlap buffer
func NewOverlapBuffer(config *OverlapBufferConfig) *OverlapBuffer {
	return &OverlapBuffer{
		sourceID:      config.SourceID,
		overlapSize:   int(config.OverlapPercent * 100), // Convert to percentage
		bufferPool:    config.BufferPool,
		format:        config.Format,
	}
}

// Process applies overlap to a chunk
func (o *OverlapBuffer) Process(chunk *AudioData) (*AudioData, error) {
	o.mu.Lock()
	defer o.mu.Unlock()

	// Calculate overlap size in bytes
	if o.overlapBytes == 0 && o.overlapSize > 0 {
		o.overlapBytes = len(chunk.Buffer) * o.overlapSize / 100
	}

	// If no overlap configured, return chunk as-is
	if o.overlapBytes == 0 {
		return chunk, nil
	}

	// Allocate result buffer
	resultSize := len(chunk.Buffer)
	if o.overlapData != nil {
		resultSize += o.overlapData.Len()
	}
	result := o.bufferPool.Get(resultSize)

	offset := 0

	// Copy overlap data if exists
	if o.overlapData != nil {
		copy(result.Data(), o.overlapData.Data())
		offset = o.overlapData.Len()
		o.overlapData.Release()
	}

	// Copy current chunk
	copy(result.Data()[offset:], chunk.Buffer)

	// Save overlap for next chunk
	if o.overlapBytes > 0 && len(chunk.Buffer) >= o.overlapBytes {
		o.overlapData = o.bufferPool.Get(o.overlapBytes)
		copy(o.overlapData.Data(), chunk.Buffer[len(chunk.Buffer)-o.overlapBytes:])
	}

	// Create a copy of the processed data to avoid use-after-free
	processedData := make([]byte, offset+len(chunk.Buffer))
	copy(processedData, result.Data()[:offset+len(chunk.Buffer)])
	
	// Now we can safely release the result buffer
	result.Release()
	
	// Create processed chunk with the copied data
	processed := &AudioData{
		Buffer:    processedData,
		Format:    chunk.Format,
		Timestamp: chunk.Timestamp,
		Duration:  chunk.Duration,
		SourceID:  chunk.SourceID,
	}

	return processed, nil
}

// Reset clears the overlap buffer
func (o *OverlapBuffer) Reset() {
	o.mu.Lock()
	defer o.mu.Unlock()

	if o.overlapData != nil {
		o.overlapData.Release()
		o.overlapData = nil
	}
}

// validatePipelineConfig validates pipeline configuration
// Ensures: overlap < chunk duration, reasonable buffer sizes, valid formats
func validatePipelineConfig(config *ProcessingPipelineConfig) error {
	if config == nil {
		return errors.New(nil).
			Component(ComponentAudioCore).
			Category(errors.CategoryValidation).
			Context("error", "nil configuration").
			Build()
	}

	// Validate required components
	if config.Source == nil {
		return errors.New(nil).
			Component(ComponentAudioCore).
			Category(errors.CategoryValidation).
			Context("error", "missing audio source").
			Build()
	}
	if config.Analyzer == nil {
		return errors.New(nil).
			Component(ComponentAudioCore).
			Category(errors.CategoryValidation).
			Context("error", "missing analyzer").
			Build()
	}
	if config.BufferPool == nil {
		return errors.New(nil).
			Component(ComponentAudioCore).
			Category(errors.CategoryValidation).
			Context("error", "missing buffer pool").
			Build()
	}

	// Validate chunk duration
	if config.Config.ChunkDuration <= 0 {
		return errors.New(nil).
			Component(ComponentAudioCore).
			Category(errors.CategoryValidation).
			Context("error", "chunk duration must be positive").
			Context("duration", config.Config.ChunkDuration).
			Build()
	}
	if config.Config.ChunkDuration < 100*time.Millisecond {
		return errors.New(nil).
			Component(ComponentAudioCore).
			Category(errors.CategoryValidation).
			Context("error", "chunk duration too small").
			Context("duration", config.Config.ChunkDuration).
			Context("minimum", "100ms").
			Build()
	}
	if config.Config.ChunkDuration > 30*time.Second {
		return errors.New(nil).
			Component(ComponentAudioCore).
			Category(errors.CategoryValidation).
			Context("error", "chunk duration too large").
			Context("duration", config.Config.ChunkDuration).
			Context("maximum", "30s").
			Build()
	}

	// Validate overlap vs chunk duration
	overlapDuration := time.Duration(config.Config.OverlapPercent * float64(config.Config.ChunkDuration))
	if overlapDuration >= config.Config.ChunkDuration {
		return errors.New(nil).
			Component(ComponentAudioCore).
			Category(errors.CategoryValidation).
			Context("error", "overlap exceeds chunk duration").
			Context("overlap_percent", config.Config.OverlapPercent).
			Context("overlap_duration", overlapDuration).
			Context("chunk_duration", config.Config.ChunkDuration).
			Build()
	}
	if config.Config.OverlapPercent < 0 {
		return errors.New(nil).
			Component(ComponentAudioCore).
			Category(errors.CategoryValidation).
			Context("error", "negative overlap percent").
			Context("overlap_percent", config.Config.OverlapPercent).
			Build()
	}

	// Validate audio format
	format := config.Source.GetFormat()
	if format.SampleRate < 8000 || format.SampleRate > 192000 {
		return errors.New(nil).
			Component(ComponentAudioCore).
			Category(errors.CategoryValidation).
			Context("error", "invalid sample rate").
			Context("sample_rate", format.SampleRate).
			Context("valid_range", "8000-192000").
			Build()
	}
	if format.Channels < 1 || format.Channels > 8 {
		return errors.New(nil).
			Component(ComponentAudioCore).
			Category(errors.CategoryValidation).
			Context("error", "invalid channel count").
			Context("channels", format.Channels).
			Context("valid_range", "1-8").
			Build()
	}
	if format.BitDepth != 8 && format.BitDepth != 16 && format.BitDepth != 24 && format.BitDepth != 32 {
		return errors.New(nil).
			Component(ComponentAudioCore).
			Category(errors.CategoryValidation).
			Context("error", "unsupported bit depth").
			Context("bit_depth", format.BitDepth).
			Context("supported", "8,16,24,32").
			Build()
	}

	// Validate buffer size calculations
	bytesPerSecond := format.SampleRate * format.Channels * (format.BitDepth / 8)
	chunkSize := int(float64(bytesPerSecond) * config.Config.ChunkDuration.Seconds())
	if chunkSize > 100*1024*1024 { // 100MB limit
		return errors.New(nil).
			Component(ComponentAudioCore).
			Category(errors.CategoryValidation).
			Context("error", "chunk size too large").
			Context("chunk_size_bytes", chunkSize).
			Context("maximum_bytes", 100*1024*1024).
			Build()
	}

	// Validate analyzer configuration
	analyzerConfig := config.Analyzer.GetConfiguration()
	requiredFormat := config.Analyzer.GetRequiredFormat()
	
	// Check if source format is compatible with analyzer requirements
	if requiredFormat.SampleRate != 0 && format.SampleRate != requiredFormat.SampleRate {
		// This is OK if we have a processor chain that can resample
		if config.ProcessorChain == nil {
			return errors.New(nil).
				Component(ComponentAudioCore).
				Category(errors.CategoryValidation).
				Context("error", "sample rate mismatch without processor chain").
				Context("source_rate", format.SampleRate).
				Context("analyzer_rate", requiredFormat.SampleRate).
				Build()
		}
	}

	// Validate analyzer-specific settings
	if analyzerConfig.Threshold < 0 || analyzerConfig.Threshold > 1 {
		return errors.New(nil).
			Component(ComponentAudioCore).
			Category(errors.CategoryValidation).
			Context("error", "invalid detection threshold").
			Context("threshold", analyzerConfig.Threshold).
			Context("valid_range", "0.0-1.0").
			Build()
	}

	return nil
}