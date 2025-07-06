// Package capture provides audio capture functionality for the audiocore system.
// It implements circular buffers for continuous audio recording and clip extraction.
package capture

import (
	"context"
	"time"

	"github.com/tphakala/birdnet-go/internal/audiocore"
	"github.com/tphakala/birdnet-go/internal/audiocore/export"
)

// Config contains configuration for audio capture
type Config struct {
	// Duration is the total buffer duration
	Duration time.Duration

	// Format is the audio format for the buffer
	Format audiocore.AudioFormat

	// PreBuffer is the duration before the trigger point to include
	PreBuffer time.Duration

	// PostBuffer is the duration after the trigger point to include
	PostBuffer time.Duration

	// ExportConfig contains export settings (optional)
	ExportConfig *export.Config
}

// Manager manages capture buffers for multiple audio sources
type Manager interface {
	// EnableCapture enables capture for a specific source
	EnableCapture(sourceID string, config Config) error

	// DisableCapture disables capture for a specific source
	DisableCapture(sourceID string) error

	// IsCaptureEnabled checks if capture is enabled for a source
	IsCaptureEnabled(sourceID string) bool

	// Write writes audio data to the capture buffer
	Write(sourceID string, data *audiocore.AudioData) error

	// SaveClip extracts and saves a clip around a specific time
	SaveClip(sourceID string, triggerTime time.Time, duration time.Duration) (*audiocore.AudioData, error)

	// ExportClip extracts and exports a clip to a file
	ExportClip(ctx context.Context, sourceID string, triggerTime time.Time, duration time.Duration) (*export.ExportResult, error)

	// GetBuffer gets the capture buffer for a source (for testing)
	GetBuffer(sourceID string) (Buffer, bool)

	// Close closes all capture buffers
	Close() error
}

// Buffer defines the interface for a circular audio buffer
type Buffer interface {
	// Write adds audio data to the buffer
	Write(data []byte) error

	// ReadSegment reads audio data for a specific time range
	ReadSegment(startTime, endTime time.Time) ([]byte, error)

	// GetFormat returns the audio format of the buffer
	GetFormat() audiocore.AudioFormat

	// GetDuration returns the total buffer duration
	GetDuration() time.Duration

	// Reset clears the buffer
	Reset()

	// Close releases any resources
	Close() error
}
