package export

import (
	"context"
	"os"
	"sync"
	"time"

	"github.com/tphakala/birdnet-go/internal/audiocore"
	"github.com/tphakala/birdnet-go/internal/errors"
)

// Manager manages audio export operations
type Manager struct {
	exporters map[Format]Exporter
	mu        sync.RWMutex
}

// NewManager creates a new export manager
func NewManager() *Manager {
	return &Manager{
		exporters: make(map[Format]Exporter),
	}
}

// RegisterExporter registers an exporter for a specific format
func (m *Manager) RegisterExporter(format Format, exporter Exporter) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.exporters[format] = exporter
}

// Export exports audio data using the appropriate exporter
func (m *Manager) Export(ctx context.Context, audioData *audiocore.AudioData, config *Config) (*ExportResult, error) {
	start := time.Now()
	result := &ExportResult{
		Metadata: &Metadata{
			SourceID:  audioData.SourceID,
			Timestamp: audioData.Timestamp,
			Duration:  audioData.Duration,
			Format:    config.Format,
			Extra:     make(map[string]interface{}),
		},
	}

	// Validate config
	if err := ValidateConfig(config); err != nil {
		result.Error = err
		return result, err
	}

	// Get exporter for format
	m.mu.RLock()
	exporter, exists := m.exporters[config.Format]
	m.mu.RUnlock()

	if !exists {
		err := errors.Newf("no exporter registered for format: %s", config.Format).
			Component("audiocore").
			Category(errors.CategoryConfiguration).
			Context("format", string(config.Format)).
			Build()
		result.Error = err
		return result, err
	}

	// Perform export
	filePath, err := exporter.ExportToFile(ctx, audioData, config)
	if err != nil {
		result.Error = err
		return result, err
	}

	// Get file info
	if info, err := getFileInfo(filePath); err == nil {
		result.Metadata.FileSize = info.Size()
	}

	result.Success = true
	result.FilePath = filePath
	result.Metadata.FilePath = filePath
	result.Duration = time.Since(start)

	return result, nil
}

// ExportClip exports a specific audio clip
func (m *Manager) ExportClip(ctx context.Context, audioData *audiocore.AudioData, startTime, endTime time.Time, config *Config) (*ExportResult, error) {
	start := time.Now()
	result := &ExportResult{
		Metadata: &Metadata{
			SourceID:  audioData.SourceID,
			Timestamp: startTime,
			Duration:  endTime.Sub(startTime),
			Format:    config.Format,
			Extra:     make(map[string]interface{}),
		},
	}

	// Get exporter
	m.mu.RLock()
	exporter, exists := m.exporters[config.Format]
	m.mu.RUnlock()

	if !exists {
		err := errors.Newf("no exporter registered for format: %s", config.Format).
			Component("audiocore").
			Category(errors.CategoryConfiguration).
			Context("format", string(config.Format)).
			Build()
		result.Error = err
		return result, err
	}

	// Export clip
	filePath, err := exporter.ExportClip(ctx, audioData, startTime, endTime, config)
	if err != nil {
		result.Error = err
		return result, err
	}

	// Get file info
	if info, err := getFileInfo(filePath); err == nil {
		result.Metadata.FileSize = info.Size()
	}

	result.Success = true
	result.FilePath = filePath
	result.Metadata.FilePath = filePath
	result.Duration = time.Since(start)

	return result, nil
}

// IsFormatSupported checks if a format is supported
func (m *Manager) IsFormatSupported(format Format) bool {
	m.mu.RLock()
	defer m.mu.RUnlock()
	_, exists := m.exporters[format]
	return exists
}

// SupportedFormats returns all supported formats
func (m *Manager) SupportedFormats() []Format {
	m.mu.RLock()
	defer m.mu.RUnlock()

	formats := make([]Format, 0, len(m.exporters))
	for format := range m.exporters {
		formats = append(formats, format)
	}
	return formats
}

// DefaultManager creates a manager with default exporters
func DefaultManager(ffmpegPath string) *Manager {
	manager := NewManager()

	// Register WAV exporter (always available)
	manager.RegisterExporter(FormatWAV, NewWAVExporter())

	// Register FFmpeg-based exporters if FFmpeg is available
	if ffmpegPath != "" {
		manager.RegisterExporter(FormatMP3, NewFFmpegExporter(FormatMP3))
		manager.RegisterExporter(FormatFLAC, NewFFmpegExporter(FormatFLAC))
		manager.RegisterExporter(FormatAAC, NewFFmpegExporter(FormatAAC))
		manager.RegisterExporter(FormatOpus, NewFFmpegExporter(FormatOpus))
	}

	return manager
}

// getFileInfo is a helper to get file information
func getFileInfo(path string) (interface{ Size() int64 }, error) {
	return os.Stat(path)
}
