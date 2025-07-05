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
type ProcessingPipeline struct {
	source         AudioSource
	analyzer       Analyzer
	processorChain ProcessorChain
	overlapBuffer  *OverlapBuffer
	bufferPool     BufferPool
	config         ProcessingConfig

	// Chunk management
	chunkBuffer *ChunkBuffer

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
	chunkBuffer := NewChunkBuffer(ChunkBufferConfig{
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
func (p *ProcessingPipeline) processLoop() {
	defer p.wg.Done()

	analyzeChan := make(chan AudioData, p.config.BufferAhead)
	defer close(analyzeChan)

	// Start analyzer goroutine
	p.wg.Add(1)
	go p.analyzeLoop(analyzeChan)

	for {
		select {
		case data := <-p.source.AudioOutput():
			// Tee to capture buffer if enabled
			if p.captureManager != nil {
				if err := p.captureManager.Write(data.SourceID, &data); err != nil {
					p.logger.Debug("failed to write to capture buffer",
						"error", err,
						"source_id", data.SourceID)
				}
			}
			
			// Add to chunk buffer
			p.chunkBuffer.Add(&data)

			// Process complete chunks
			for p.chunkBuffer.HasCompleteChunk() {
				chunk := p.chunkBuffer.GetChunk()
				if chunk == nil {
					continue
				}

				// Apply overlap
				processedChunk, err := p.overlapBuffer.Process(chunk)
				if err != nil {
					p.logger.Warn("overlap processing failed",
						"error", err)
					if p.metrics != nil {
						p.metrics.RecordProcessingError("pipeline", p.source.ID(), "overlap_error")
					}
					continue
				}

				// Run through processor chain if configured
				if p.processorChain != nil {
					processedChunk, err = p.processorChain.Process(p.ctx, processedChunk)
					if err != nil {
						p.logger.Warn("processor chain failed",
							"error", err)
						if p.metrics != nil {
							p.metrics.RecordProcessingError("pipeline", p.source.ID(), "processor_error")
						}
						continue
					}
				}

				// Send to analyzer (non-blocking)
				select {
				case analyzeChan <- *processedChunk:
					p.processedChunks++
				default:
					// Buffer full, drop chunk
					p.droppedChunks++
					p.logger.Warn("analyzer buffer full, dropping chunk")
					if p.metrics != nil {
						p.metrics.RecordFrameDropped(p.source.ID(), "analyzer_buffer_full")
					}
				}
			}

		case <-p.ctx.Done():
			return
		}
	}
}

// analyzeLoop processes chunks through the analyzer
func (p *ProcessingPipeline) analyzeLoop(chunks <-chan AudioData) {
	defer p.wg.Done()

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

// ChunkBuffer accumulates audio data into fixed-duration chunks
type ChunkBuffer struct {
	chunkDuration  time.Duration
	format         AudioFormat
	bufferPool     BufferPool
	buffer         AudioBuffer
	currentSize    int
	targetSize     int
	overflowBuffer []byte // Buffer to hold overflow data between chunks
	mu             sync.Mutex
}

// ChunkBufferConfig holds configuration for chunk buffer
type ChunkBufferConfig struct {
	ChunkDuration time.Duration
	Format        AudioFormat
	BufferPool    BufferPool
}

// NewChunkBuffer creates a new chunk buffer
func NewChunkBuffer(config ChunkBufferConfig) *ChunkBuffer {
	// Calculate target buffer size
	bytesPerSecond := config.Format.SampleRate * config.Format.Channels * (config.Format.BitDepth / 8)
	targetSize := int(float64(bytesPerSecond) * config.ChunkDuration.Seconds())

	return &ChunkBuffer{
		chunkDuration: config.ChunkDuration,
		format:        config.Format,
		bufferPool:    config.BufferPool,
		targetSize:    targetSize,
	}
}

// Add adds audio data to the buffer
func (c *ChunkBuffer) Add(data *AudioData) {
	c.mu.Lock()
	defer c.mu.Unlock()

	// Initialize buffer if needed
	if c.buffer == nil {
		c.buffer = c.bufferPool.Get(c.targetSize)
		c.currentSize = 0
		
		// If we have overflow data from previous chunk, add it first
		if len(c.overflowBuffer) > 0 {
			copy(c.buffer.Data(), c.overflowBuffer)
			c.currentSize = len(c.overflowBuffer)
			c.overflowBuffer = nil
		}
	}

	// Copy data to buffer
	remaining := c.targetSize - c.currentSize
	toCopy := len(data.Buffer)
	if toCopy > remaining {
		toCopy = remaining
	}

	if toCopy > 0 {
		copy(c.buffer.Data()[c.currentSize:], data.Buffer[:toCopy])
		c.currentSize += toCopy
	}

	// Handle overflow data that spans chunks
	if len(data.Buffer) > toCopy {
		// Store overflow data for next chunk
		overflow := data.Buffer[toCopy:]
		c.overflowBuffer = make([]byte, len(overflow))
		copy(c.overflowBuffer, overflow)
	}
}

// HasCompleteChunk returns true if a complete chunk is ready
func (c *ChunkBuffer) HasCompleteChunk() bool {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.currentSize >= c.targetSize
}

// GetChunk returns a complete chunk and resets the buffer
func (c *ChunkBuffer) GetChunk() *AudioData {
	c.mu.Lock()
	defer c.mu.Unlock()

	if c.currentSize < c.targetSize {
		return nil
	}

	// Create chunk data
	chunk := &AudioData{
		Buffer:    c.buffer.Data()[:c.targetSize],
		Format:    c.format,
		Timestamp: time.Now().Add(-c.chunkDuration), // Approximate start time
		Duration:  c.chunkDuration,
		SourceID:  "", // Will be set by caller
	}

	// Reset buffer but preserve overflow
	c.buffer.Release()
	c.buffer = nil
	c.currentSize = 0
	
	// If we have overflow data, it will be added to the next chunk

	return chunk
}

// OverlapBuffer handles chunk overlap for continuous processing
type OverlapBuffer struct {
	sourceID      string
	overlapData   AudioBuffer
	overlapSize   int
	overlapBytes  int
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