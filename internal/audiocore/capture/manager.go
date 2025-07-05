// Package capture provides audio capture buffer management for saving audio clips
package capture

import (
	"sync"
	"time"

	"github.com/tphakala/birdnet-go/internal/audiocore"
	"github.com/tphakala/birdnet-go/internal/errors"
	"log/slog"
)

// CaptureConfig holds configuration for audio capture
type CaptureConfig struct {
	Duration   time.Duration
	Format     audiocore.AudioFormat
	PreBuffer  time.Duration // How much audio to keep before trigger
	PostBuffer time.Duration // How much audio to keep after trigger
}

// CaptureManager manages circular buffers for audio capture
type CaptureManager struct {
	buffers    map[string]*CircularBuffer
	configs    map[string]CaptureConfig
	bufferPool audiocore.BufferPool
	mu         sync.RWMutex
	logger     *slog.Logger
}

// NewCaptureManager creates a new capture manager
func NewCaptureManager(bufferPool audiocore.BufferPool, logger *slog.Logger) *CaptureManager {
	if logger == nil {
		logger = slog.Default()
	}
	return &CaptureManager{
		buffers:    make(map[string]*CircularBuffer),
		configs:    make(map[string]CaptureConfig),
		bufferPool: bufferPool,
		logger:     logger.With("component", "capture_manager"),
	}
}

// EnableCapture enables capture for a specific source
func (c *CaptureManager) EnableCapture(sourceID string, config CaptureConfig) error {
	c.mu.Lock()
	defer c.mu.Unlock()

	// Validate config
	if config.Duration <= 0 {
		return errors.New(nil).
			Component(audiocore.ComponentAudioCore).
			Category(errors.CategoryValidation).
			Context("source_id", sourceID).
			Context("error", "capture duration must be positive").
			Build()
	}

	// Create circular buffer
	buffer := NewCircularBuffer(CircularBufferConfig{
		Duration:   config.Duration,
		SampleRate: config.Format.SampleRate,
		Channels:   config.Format.Channels,
		BitDepth:   config.Format.BitDepth,
		BufferPool: c.bufferPool,
	})

	c.buffers[sourceID] = buffer
	c.configs[sourceID] = config
	
	c.logger.Info("capture enabled for source",
		"source_id", sourceID,
		"duration", config.Duration,
		"format", config.Format)

	return nil
}

// DisableCapture disables capture for a specific source
func (c *CaptureManager) DisableCapture(sourceID string) error {
	c.mu.Lock()
	defer c.mu.Unlock()

	buffer, exists := c.buffers[sourceID]
	if !exists {
		return errors.New(nil).
			Component(audiocore.ComponentAudioCore).
			Category(errors.CategoryNotFound).
			Context("source_id", sourceID).
			Context("error", "capture not enabled for source").
			Build()
	}

	// Clean up buffer
	buffer.Close()
	delete(c.buffers, sourceID)
	delete(c.configs, sourceID)
	
	c.logger.Info("capture disabled for source", "source_id", sourceID)

	return nil
}

// GetBuffer retrieves the capture buffer for a source
func (c *CaptureManager) GetBuffer(sourceID string) *CircularBuffer {
	c.mu.RLock()
	defer c.mu.RUnlock()
	return c.buffers[sourceID]
}

// Write writes audio data to the capture buffer for a source
func (c *CaptureManager) Write(sourceID string, data *audiocore.AudioData) error {
	c.mu.RLock()
	buffer, exists := c.buffers[sourceID]
	c.mu.RUnlock()

	if !exists {
		// Capture not enabled for this source, silently ignore
		return nil
	}

	return buffer.Write(data)
}

// SaveClip saves a clip from the capture buffer
func (c *CaptureManager) SaveClip(sourceID string, timestamp time.Time, duration time.Duration) ([]byte, error) {
	c.mu.RLock()
	buffer, exists := c.buffers[sourceID]
	config := c.configs[sourceID]
	c.mu.RUnlock()

	if !exists {
		return nil, errors.New(nil).
			Component(audiocore.ComponentAudioCore).
			Category(errors.CategoryNotFound).
			Context("source_id", sourceID).
			Context("error", "capture not enabled for source").
			Build()
	}

	// Calculate time range with pre/post buffers
	startTime := timestamp.Add(-config.PreBuffer)
	endTime := timestamp.Add(duration).Add(config.PostBuffer)
	totalDuration := endTime.Sub(startTime)

	// Extract audio from circular buffer
	audioData, err := buffer.Extract(startTime, totalDuration)
	if err != nil {
		return nil, errors.New(err).
			Component(audiocore.ComponentAudioCore).
			Category(errors.CategoryProcessing).
			Context("source_id", sourceID).
			Context("operation", "extract_audio").
			Build()
	}

	c.logger.Debug("saved audio clip",
		"source_id", sourceID,
		"timestamp", timestamp,
		"duration", totalDuration,
		"size", len(audioData))

	return audioData, nil
}

// GetCaptureStatus returns capture status for all sources
func (c *CaptureManager) GetCaptureStatus() map[string]CaptureStatus {
	c.mu.RLock()
	defer c.mu.RUnlock()

	status := make(map[string]CaptureStatus)
	for sourceID, buffer := range c.buffers {
		config := c.configs[sourceID]
		status[sourceID] = CaptureStatus{
			Enabled:        true,
			BufferDuration: config.Duration,
			CurrentSize:    buffer.Size(),
			TotalWritten:   buffer.TotalWritten(),
		}
	}

	return status
}

// CaptureStatus represents the status of capture for a source
type CaptureStatus struct {
	Enabled        bool
	BufferDuration time.Duration
	CurrentSize    int64
	TotalWritten   int64
}

// Close closes all capture buffers
func (c *CaptureManager) Close() error {
	c.mu.Lock()
	defer c.mu.Unlock()

	for sourceID, buffer := range c.buffers {
		buffer.Close()
		c.logger.Debug("closed capture buffer", "source_id", sourceID)
	}

	c.buffers = make(map[string]*CircularBuffer)
	c.configs = make(map[string]CaptureConfig)

	return nil
}