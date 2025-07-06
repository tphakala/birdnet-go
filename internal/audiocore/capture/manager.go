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

// captureBuffer holds a buffer and its configuration
type captureBuffer struct {
	buffer Buffer
	config Config
}

// ManagerImpl implements the capture Manager interface
type ManagerImpl struct {
	buffers       map[string]*captureBuffer
	bufferPool    audiocore.BufferPool
	exportManager *export.Manager
	logger        *slog.Logger
	mu            sync.RWMutex
}

// NewManager creates a new capture manager
func NewManager(bufferPool audiocore.BufferPool, exportManager *export.Manager) Manager {
	// Get logger from logging package
	logger := logging.ForService("audiocore").With("component", "capture_manager")
	if logger == nil {
		// Fallback to default slog if logging not initialized
		logger = slog.Default().With("component", "capture_manager")
	}

	return &ManagerImpl{
		buffers:       make(map[string]*captureBuffer),
		bufferPool:    bufferPool,
		exportManager: exportManager,
		logger:        logger,
	}
}

// EnableCapture enables capture for a specific source
func (m *ManagerImpl) EnableCapture(sourceID string, config Config) error {
	m.mu.Lock()
	defer m.mu.Unlock()

	// Check if already enabled
	if _, exists := m.buffers[sourceID]; exists {
		return errors.Newf("capture already enabled for source: %s", sourceID).
			Component("audiocore").
			Category(errors.CategoryState).
			Context("source_id", sourceID).
			Build()
	}

	// Validate config
	if config.Duration <= 0 {
		return errors.Newf("invalid capture duration: %v", config.Duration).
			Component("audiocore").
			Category(errors.CategoryValidation).
			Context("source_id", sourceID).
			Build()
	}

	// Create circular buffer
	buffer, err := NewCircularBuffer(config.Duration, config.Format, m.bufferPool)
	if err != nil {
		return errors.New(err).
			Component("audiocore").
			Category(errors.CategorySystem).
			Context("operation", "create_circular_buffer").
			Context("source_id", sourceID).
			Build()
	}

	// Store buffer and config
	m.buffers[sourceID] = &captureBuffer{
		buffer: buffer,
		config: config,
	}

	m.logger.Info("capture enabled",
		"source_id", sourceID,
		"duration", config.Duration,
		"pre_buffer", config.PreBuffer,
		"post_buffer", config.PostBuffer)

	return nil
}

// DisableCapture disables capture for a specific source
func (m *ManagerImpl) DisableCapture(sourceID string) error {
	m.mu.Lock()
	defer m.mu.Unlock()

	cb, exists := m.buffers[sourceID]
	if !exists {
		return errors.Newf("capture not enabled for source: %s", sourceID).
			Component("audiocore").
			Category(errors.CategoryState).
			Context("source_id", sourceID).
			Build()
	}

	// Close buffer
	if err := cb.buffer.Close(); err != nil {
		m.logger.Error("failed to close capture buffer",
			"source_id", sourceID,
			"error", err)
	}

	// Remove from map
	delete(m.buffers, sourceID)

	m.logger.Info("capture disabled", "source_id", sourceID)
	return nil
}

// IsCaptureEnabled checks if capture is enabled for a source
func (m *ManagerImpl) IsCaptureEnabled(sourceID string) bool {
	m.mu.RLock()
	defer m.mu.RUnlock()
	_, exists := m.buffers[sourceID]
	return exists
}

// Write writes audio data to the capture buffer
func (m *ManagerImpl) Write(sourceID string, data *audiocore.AudioData) error {
	m.mu.RLock()
	cb, exists := m.buffers[sourceID]
	m.mu.RUnlock()

	if !exists {
		// Not an error - capture might not be enabled
		return nil
	}

	// Write to buffer
	return cb.buffer.Write(data.Buffer)
}

// SaveClip extracts and saves a clip around a specific time
func (m *ManagerImpl) SaveClip(sourceID string, triggerTime time.Time, duration time.Duration) (*audiocore.AudioData, error) {
	m.mu.RLock()
	cb, exists := m.buffers[sourceID]
	m.mu.RUnlock()

	if !exists {
		return nil, errors.Newf("capture not enabled for source: %s", sourceID).
			Component("audiocore").
			Category(errors.CategoryState).
			Context("source_id", sourceID).
			Build()
	}

	// Calculate clip time range
	// The trigger time is when the detection occurred
	// We want pre-buffer seconds before and post-buffer seconds after
	startTime := triggerTime.Add(-cb.config.PreBuffer)
	endTime := triggerTime.Add(duration).Add(cb.config.PostBuffer)

	// Read segment from buffer
	audioData, err := cb.buffer.ReadSegment(startTime, endTime)
	if err != nil {
		return nil, errors.New(err).
			Component("audiocore").
			Category(errors.CategoryAudio).
			Context("operation", "read_clip_segment").
			Context("source_id", sourceID).
			Build()
	}

	// Create AudioData structure
	result := &audiocore.AudioData{
		Buffer:    audioData,
		Format:    cb.buffer.GetFormat(),
		Timestamp: startTime,
		Duration:  endTime.Sub(startTime),
		SourceID:  sourceID,
	}

	m.logger.Debug("clip extracted",
		"source_id", sourceID,
		"start_time", startTime.Format(time.RFC3339),
		"end_time", endTime.Format(time.RFC3339),
		"duration", result.Duration,
		"size", len(audioData))

	return result, nil
}

// ExportClip extracts and exports a clip to a file
func (m *ManagerImpl) ExportClip(ctx context.Context, sourceID string, triggerTime time.Time, duration time.Duration) (*export.ExportResult, error) {
	// First extract the clip
	audioData, err := m.SaveClip(sourceID, triggerTime, duration)
	if err != nil {
		return nil, err
	}

	// Get export config
	m.mu.RLock()
	cb, exists := m.buffers[sourceID]
	m.mu.RUnlock()

	if !exists || cb.config.ExportConfig == nil {
		return nil, errors.Newf("no export config for source: %s", sourceID).
			Component("audiocore").
			Category(errors.CategoryConfiguration).
			Context("source_id", sourceID).
			Build()
	}

	// Export using export manager
	result, err := m.exportManager.Export(ctx, audioData, cb.config.ExportConfig)
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
		"format", cb.config.ExportConfig.Format,
		"duration", audioData.Duration,
		"export_time", result.Duration)

	return result, nil
}

// GetBuffer gets the capture buffer for a source (for testing)
func (m *ManagerImpl) GetBuffer(sourceID string) (Buffer, bool) {
	m.mu.RLock()
	defer m.mu.RUnlock()

	cb, exists := m.buffers[sourceID]
	if !exists {
		return nil, false
	}
	return cb.buffer, true
}

// Close closes all capture buffers
func (m *ManagerImpl) Close() error {
	m.mu.Lock()
	defer m.mu.Unlock()

	var errs []error
	for sourceID, cb := range m.buffers {
		if err := cb.buffer.Close(); err != nil {
			errs = append(errs, errors.New(err).
				Component("audiocore").
				Category(errors.CategorySystem).
				Context("operation", "close_buffer").
				Context("source_id", sourceID).
				Build())
		}
	}

	// Clear map
	m.buffers = make(map[string]*captureBuffer)

	if len(errs) > 0 {
		return errors.Join(errs...)
	}
	return nil
}
