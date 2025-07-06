package audiocore

import (
	"context"
	"log/slog"
	"sync"
	"sync/atomic"
	"time"

	"github.com/tphakala/birdnet-go/internal/birdnet"
	"github.com/tphakala/birdnet-go/internal/datastore"
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
	
	// Adaptive backpressure
	backpressureDelay atomic.Int64 // Current backpressure delay in milliseconds
	lastDropRate      float64       // Last measured drop rate
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
func NewProcessingPipeline(config *ProcessingPipelineConfig) (*ProcessingPipeline, error) {
	// Validate configuration first
	if err := validatePipelineConfig(config); err != nil {
		return nil, errors.New(err).
			Component(ComponentAudioCore).
			Category(errors.CategoryValidation).
			Context("error", "invalid pipeline configuration").
			Build()
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
	// NOTE: We intentionally don't use buffer pool for chunk buffer to avoid
	// unnecessary PCM data copying (288KB) when sending to results queue.
	// The processor takes ownership of the PCM data without copying.
	chunkBuffer := NewChunkBufferV2(ChunkBufferConfig{
		ChunkDuration: config.Config.ChunkDuration,
		Format:        config.Source.GetFormat(),
		BufferPool:    nil, // Disable pooling to avoid 288KB copies
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
	}, nil
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
					// Track capture buffer write failures in metrics
					if p.metrics != nil {
						p.metrics.RecordProcessingError("capture_buffer", data.SourceID, "write_failed")
					}
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
				"drop_rate", func() float64 {
				total := p.processedChunks + p.droppedChunks
				if total == 0 {
					return 0
				}
				return float64(p.droppedChunks) / float64(total)
			}())
		}
		
		if p.metrics != nil {
			p.metrics.RecordFrameDropped(p.source.ID(), "analyzer_buffer_full")
		}
		
		// Apply adaptive backpressure based on drop rate
		// Start with current delay or default if zero
		currentDelay := p.backpressureDelay.Load()
		if currentDelay == 0 {
			currentDelay = 10 // Default 10ms
		}
		
		select {
		case <-time.After(time.Duration(currentDelay) * time.Millisecond):
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
				// Release buffer on error
				if chunk.BufferHandle != nil {
					chunk.BufferHandle.Release()
				}
				continue
			}

			// Process results with chunk data for queue conversion
			p.processAnalysisResult(&result, &chunk)
			
			// Release pooled buffer after processing
			if chunk.BufferHandle != nil {
				chunk.BufferHandle.Release()
			}

		case <-p.ctx.Done():
			return
		}
	}
}

// processAnalysisResult converts audiocore results to birdnet.Results format and sends to queue
//
// OWNERSHIP MODEL:
// This function implements a critical ownership transfer of PCM audio data:
// 1. The input chunk contains PCM data that may be backed by a pooled buffer
// 2. We create a copy of the PCM data to transfer ownership to the results queue
// 3. The queue consumer (processor) takes full ownership and is responsible for the data
// 4. After this function returns, the original chunk buffer can be safely released/reused
//
// This design ensures:
// - No use-after-free bugs from pooled buffers being reused while still in the queue
// - Clear ownership boundaries between audiocore and the processor
// - Compatibility with the existing processor's expectations
func (p *ProcessingPipeline) processAnalysisResult(result *AnalysisResult, chunk *AudioData) {
	// Skip if no detections
	if len(result.Detections) == 0 {
		return
	}

	// Convert detections to datastore.Results format
	datastoreResults := make([]datastore.Results, 0, len(result.Detections))
	
	for _, detection := range result.Detections {
		// Detection.Label now contains the species string in the correct format
		datastoreResults = append(datastoreResults, datastore.Results{
			Species:    detection.Label,
			Confidence: detection.Confidence,
		})
	}
	
	// Calculate elapsed time from metadata if available
	var elapsedTime time.Duration
	if procTime, ok := result.Metadata["processingTime"].(time.Duration); ok {
		elapsedTime = procTime
	} else {
		// Estimate based on chunk duration if not available
		elapsedTime = chunk.Duration
	}
	
	// OWNERSHIP TRANSFER: Avoid copying 288KB of PCM data
	// The existing myaudio implementation transfers ownership without copying,
	// and the processor expects to own the data. However, we must handle the
	// case where chunk.Buffer is backed by a pooled buffer that will be reused.
	//
	// Strategy: If the chunk has a buffer handle (pooled), we must copy.
	// Otherwise, we can transfer ownership directly.
	var pcmData []byte
	if chunk.BufferHandle != nil {
		// This buffer is pooled and will be released after this function.
		// We must copy it to transfer ownership to the queue.
		pcmData = make([]byte, len(chunk.Buffer))
		copy(pcmData, chunk.Buffer)
	} else {
		// This buffer is not pooled - we can transfer ownership directly
		// without copying the 288KB of data.
		pcmData = chunk.Buffer
	}
	
	// Create birdnet.Results struct matching the format from myaudio
	queueResult := birdnet.Results{
		StartTime:   result.Timestamp,
		ElapsedTime: elapsedTime,
		PCMdata:     pcmData,          // PCM data (ownership transfers to queue)
		Results:     datastoreResults,
		Source:      result.SourceID,
		// ClipName is generated by the processor, not here
	}
	
	// Check queue depth for metrics (non-blocking)
	queueDepth := len(birdnet.ResultsQueue)
	queueCapacity := cap(birdnet.ResultsQueue)
	if p.metrics != nil {
		p.metrics.UpdateQueueDepth(queueDepth, queueCapacity)
	}
	
	// Send to the existing results queue
	select {
	case birdnet.ResultsQueue <- queueResult:
		p.logger.Debug("sent results to queue",
			"detections", len(datastoreResults),
			"source", result.SourceID,
			"timestamp", result.Timestamp)
		
		// Record successful queue submission
		if p.metrics != nil {
			p.metrics.RecordQueueSubmission(result.SourceID)
		}
	default:
		// Queue is full, drop the results
		p.logger.Warn("results queue full, dropping results",
			"source", result.SourceID,
			"detections", len(datastoreResults))
		if p.metrics != nil {
			p.metrics.RecordFrameDropped(result.SourceID, "results_queue_full")
		}
	}
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
				// Calculate drop rate with zero check
				total := processed + dropped
				var dropRate float64
				if total > 0 {
					dropRate = float64(dropped) / float64(total)
				} else {
					dropRate = 0
				}
				
				// Adaptive backpressure adjustment
				currentDelay := p.backpressureDelay.Load()
				if currentDelay == 0 {
					currentDelay = 10 // Default starting delay
				}
				
				if dropRate > 0.05 { // More than 5% drop rate
					// Increase backpressure if drop rate is increasing
					if dropRate > p.lastDropRate {
						newDelay := currentDelay * 2
						if newDelay > 100 { // Cap at 100ms
							newDelay = 100
						}
						p.backpressureDelay.Store(newDelay)
						p.logger.Warn("increasing backpressure due to high drop rate",
							"drop_rate", dropRate,
							"new_delay_ms", newDelay,
							"processed", processed,
							"dropped", dropped)
					}
				} else if dropRate < 0.01 && currentDelay > 10 { // Less than 1% drop rate
					// Decrease backpressure if drop rate is low
					newDelay := currentDelay / 2
					if newDelay < 10 { // Minimum 10ms
						newDelay = 10
					}
					p.backpressureDelay.Store(newDelay)
					p.logger.Info("reducing backpressure due to low drop rate",
						"drop_rate", dropRate,
						"new_delay_ms", newDelay)
				}
				
				p.lastDropRate = dropRate
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
		"drop_rate":        func() float64 {
			total := p.processedChunks + p.droppedChunks
			if total == 0 {
				return 0
			}
			return float64(p.droppedChunks) / float64(total)
		}(),
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

	// Get a buffer from pool for the processed data
	processedSize := offset + len(chunk.Buffer)
	processedBuffer := o.bufferPool.Get(processedSize)
	copy(processedBuffer.Data(), result.Data()[:processedSize])
	
	// Now we can safely release the temporary result buffer
	result.Release()
	
	// Create processed chunk with pooled buffer
	processed := &AudioData{
		Buffer:       processedBuffer.Data()[:processedSize],
		Format:       chunk.Format,
		Timestamp:    chunk.Timestamp,
		Duration:     chunk.Duration,
		SourceID:     chunk.SourceID,
		BufferHandle: processedBuffer, // Track the pooled buffer for proper cleanup
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
		return errors.New(nil).
			Component(ComponentAudioCore).
			Category(errors.CategoryValidation).
			Context("error", "sample rate mismatch - source must match analyzer requirements").
			Context("source_rate", format.SampleRate).
			Context("analyzer_rate", requiredFormat.SampleRate).
			Build()
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