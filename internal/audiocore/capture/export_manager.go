package capture

import (
	"context"
	"log/slog"
	"sync"
	"time"

	"github.com/tphakala/birdnet-go/internal/audiocore"
	"github.com/tphakala/birdnet-go/internal/audiocore/export"
	"github.com/tphakala/birdnet-go/internal/errors"
	"github.com/tphakala/birdnet-go/internal/logging"
)

// extendedConfig holds both capture and export configuration
type extendedConfig struct {
	CaptureConfig
	exportConfig *export.Config
}

// ExportManager wraps the basic CaptureManager and adds export functionality
type ExportManager struct {
	*CaptureManager
	exportManager *export.Manager
	configs       map[string]extendedConfig
	mu            sync.RWMutex
}

// NewManager creates a new capture manager with export capabilities
func NewManager(bufferPool audiocore.BufferPool, exportManager *export.Manager) Manager {
	// Get logger from logging package
	var logger *slog.Logger
	if l := logging.ForService("audiocore"); l != nil {
		logger = l.With("component", "capture_manager")
	} else {
		// Fallback to default slog if logging not initialized
		logger = slog.Default().With("component", "capture_manager")
	}

	// Create base capture manager
	baseMgr := NewCaptureManager(bufferPool, logger)

	return &ExportManager{
		CaptureManager: baseMgr,
		exportManager:  exportManager,
		configs:        make(map[string]extendedConfig),
	}
}

// EnableCapture enables capture for a specific source with export config
func (m *ExportManager) EnableCapture(sourceID string, config Config) error {
	// Create CaptureConfig from Config
	captureConfig := CaptureConfig{
		Duration:   config.Duration,
		Format:     config.Format,
		PreBuffer:  config.PreBuffer,
		PostBuffer: config.PostBuffer,
	}

	// Enable capture in base manager
	if err := m.CaptureManager.EnableCapture(sourceID, captureConfig); err != nil {
		return err
	}

	// Store export config
	m.mu.Lock()
	m.configs[sourceID] = extendedConfig{
		CaptureConfig: captureConfig,
		exportConfig:  config.ExportConfig,
	}
	m.mu.Unlock()

	return nil
}

// DisableCapture disables capture for a specific source
func (m *ExportManager) DisableCapture(sourceID string) error {
	m.mu.Lock()
	delete(m.configs, sourceID)
	m.mu.Unlock()

	return m.CaptureManager.DisableCapture(sourceID)
}

// IsCaptureEnabled checks if capture is enabled for a source
func (m *ExportManager) IsCaptureEnabled(sourceID string) bool {
	m.mu.RLock()
	defer m.mu.RUnlock()
	_, exists := m.configs[sourceID]
	return exists
}

// SaveClip extracts and saves a clip around a specific time
func (m *ExportManager) SaveClip(sourceID string, triggerTime time.Time, duration time.Duration) (*audiocore.AudioData, error) {
	// Get raw audio data from base manager
	audioData, err := m.CaptureManager.SaveClip(sourceID, triggerTime, duration)
	if err != nil {
		return nil, err
	}

	// Get config
	m.mu.RLock()
	config := m.configs[sourceID]
	m.mu.RUnlock()

	// Create AudioData structure
	result := &audiocore.AudioData{
		Buffer:    audioData,
		Format:    config.Format,
		Timestamp: triggerTime.Add(-config.PreBuffer),
		Duration:  duration + config.PreBuffer + config.PostBuffer,
		SourceID:  sourceID,
	}

	return result, nil
}

// ExportClip extracts and exports a clip to a file
func (m *ExportManager) ExportClip(ctx context.Context, sourceID string, triggerTime time.Time, duration time.Duration) (*export.ExportResult, error) {
	// First extract the clip
	audioData, err := m.SaveClip(sourceID, triggerTime, duration)
	if err != nil {
		return nil, err
	}

	// Get export config
	m.mu.RLock()
	config, exists := m.configs[sourceID]
	m.mu.RUnlock()

	if !exists || config.exportConfig == nil {
		return nil, errors.Newf("no export config for source: %s", sourceID).
			Component("audiocore").
			Category(errors.CategoryConfiguration).
			Context("source_id", sourceID).
			Build()
	}

	// Export using export manager
	result, err := m.exportManager.Export(ctx, audioData, config.exportConfig)
	if err != nil {
		return nil, errors.New(err).
			Component("audiocore").
			Category(errors.CategorySystem).
			Context("operation", "export_clip").
			Context("source_id", sourceID).
			Build()
	}

	m.logger.Info("clip exported",
		"source_id", sourceID,
		"file_path", result.FilePath,
		"format", config.exportConfig.Format,
		"duration", audioData.Duration,
		"export_time", result.Duration)

	return result, nil
}

// GetBuffer gets the capture buffer for a source (for testing)
func (m *ExportManager) GetBuffer(sourceID string) (Buffer, bool) {
	buffer := m.CaptureManager.GetBuffer(sourceID)
	if buffer == nil {
		return nil, false
	}
	// Wrap the circular buffer to implement our Buffer interface
	return &bufferAdapter{buffer: buffer}, true
}

// bufferAdapter adapts CircularBuffer to Buffer interface
type bufferAdapter struct {
	buffer *CircularBuffer
}

func (b *bufferAdapter) Write(data []byte) error {
	// Create minimal AudioData for compatibility
	audioData := &audiocore.AudioData{
		Buffer:    data,
		Format:    b.buffer.config.ToAudioFormat(),
		Timestamp: time.Now(),
	}
	return b.buffer.Write(audioData)
}

func (b *bufferAdapter) ReadSegment(startTime, endTime time.Time) ([]byte, error) {
	duration := endTime.Sub(startTime)
	return b.buffer.Extract(startTime, duration)
}

func (b *bufferAdapter) GetFormat() audiocore.AudioFormat {
	return b.buffer.config.ToAudioFormat()
}

func (b *bufferAdapter) GetDuration() time.Duration {
	return b.buffer.config.Duration
}

func (b *bufferAdapter) Reset() {
	b.buffer.Clear()
}

func (b *bufferAdapter) Close() error {
	b.buffer.Close()
	return nil
}

// ToAudioFormat converts CircularBufferConfig to AudioFormat
func (c CircularBufferConfig) ToAudioFormat() audiocore.AudioFormat {
	return audiocore.AudioFormat{
		SampleRate: c.SampleRate,
		Channels:   c.Channels,
		BitDepth:   c.BitDepth,
		Encoding:   "pcm_s16le", // Default encoding
	}
}