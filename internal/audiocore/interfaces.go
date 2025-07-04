// Package audiocore provides a modular and extensible audio processing system
// for BirdNET-Go. It supports multiple simultaneous audio sources, per-source
// configuration, and a plugin-based processing architecture.
package audiocore

import (
	"context"
	"time"
)

// AudioFormat represents the format of audio data
type AudioFormat struct {
	SampleRate int     // Sample rate in Hz (e.g., 48000)
	Channels   int     // Number of channels (1 for mono, 2 for stereo)
	BitDepth   int     // Bits per sample (e.g., 16, 24, 32)
	Encoding   string  // Encoding format (e.g., "pcm_s16le", "pcm_f32le")
}

// AudioData represents a chunk of audio with metadata
type AudioData struct {
	Buffer    []byte       // Raw audio data
	Format    AudioFormat  // Audio format information
	Timestamp time.Time    // When this audio was captured
	Duration  time.Duration // Duration of the audio chunk
	SourceID  string       // Identifier of the source that produced this audio
}

// AudioSource represents an audio input source
type AudioSource interface {
	// ID returns a unique identifier for this source
	ID() string

	// Name returns a human-readable name for this source
	Name() string

	// Start begins audio capture from this source
	Start(ctx context.Context) error

	// Stop halts audio capture
	Stop() error

	// AudioOutput returns a channel that emits audio data
	AudioOutput() <-chan AudioData

	// Errors returns a channel for error reporting
	Errors() <-chan error

	// IsActive returns true if the source is currently capturing
	IsActive() bool

	// GetFormat returns the audio format of this source
	GetFormat() AudioFormat

	// SetGain sets the audio gain level (0.0 to 1.0)
	SetGain(gain float64) error
}

// AudioProcessor processes audio data
type AudioProcessor interface {
	// ID returns a unique identifier for this processor
	ID() string

	// Process transforms audio data
	Process(ctx context.Context, input *AudioData) (*AudioData, error)

	// GetRequiredFormat returns the audio format this processor requires
	// Returns nil if the processor can handle any format
	GetRequiredFormat() *AudioFormat

	// GetOutputFormat returns the audio format this processor outputs
	// given an input format
	GetOutputFormat(inputFormat AudioFormat) AudioFormat
}

// ProcessorChain represents a sequence of audio processors
type ProcessorChain interface {
	// AddProcessor adds a processor to the chain
	AddProcessor(processor AudioProcessor) error

	// RemoveProcessor removes a processor from the chain
	RemoveProcessor(id string) error

	// Process runs audio through the entire chain
	Process(ctx context.Context, input *AudioData) (*AudioData, error)

	// GetProcessors returns all processors in order
	GetProcessors() []AudioProcessor
}

// AudioBuffer represents a reusable audio buffer
type AudioBuffer interface {
	// Data returns the underlying byte slice
	Data() []byte

	// Len returns the current length of valid data
	Len() int

	// Cap returns the capacity of the buffer
	Cap() int

	// Reset clears the buffer
	Reset()

	// Resize changes the buffer size
	Resize(newSize int) error

	// Slice returns a slice of the buffer
	Slice(start, end int) ([]byte, error)

	// Acquire increments the reference count
	Acquire()

	// Release decrements the reference count and returns to pool if zero
	Release()
}

// BufferPool manages reusable audio buffers
type BufferPool interface {
	// Get retrieves a buffer of at least the specified size
	Get(size int) AudioBuffer

	// Put returns a buffer to the pool
	Put(buffer AudioBuffer)

	// Stats returns statistics about the pool
	Stats() BufferPoolStats
}

// BufferPoolStats contains statistics about buffer pool usage
type BufferPoolStats struct {
	TotalBuffers   int
	ActiveBuffers  int
	TotalAllocated int64
	HitRate        float64
}

// AudioManager orchestrates multiple audio sources and processing
type AudioManager interface {
	// AddSource adds a new audio source
	AddSource(source AudioSource) error

	// RemoveSource removes an audio source
	RemoveSource(id string) error

	// GetSource retrieves a source by ID
	GetSource(id string) (AudioSource, bool)

	// ListSources returns all registered sources
	ListSources() []AudioSource

	// SetProcessorChain sets the processor chain for a source
	SetProcessorChain(sourceID string, chain ProcessorChain) error

	// Start begins processing audio from all sources
	Start(ctx context.Context) error

	// Stop halts all audio processing
	Stop() error

	// AudioOutput returns a channel that emits processed audio from all sources
	AudioOutput() <-chan AudioData

	// Metrics returns current metrics for the manager
	Metrics() ManagerMetrics
}

// ManagerMetrics contains runtime metrics for the audio manager
type ManagerMetrics struct {
	ActiveSources    int
	ProcessedFrames  int64
	ProcessingErrors int64
	BufferPoolStats  BufferPoolStats
	LastUpdate       time.Time
}

// SourceConfig contains configuration for an audio source
type SourceConfig struct {
	ID          string
	Name        string
	Type        string // "soundcard", "rtsp", "file", etc.
	Device      string // Device identifier or URL
	Format      AudioFormat
	BufferSize  int
	Gain        float64
	ModelID     string // BirdNET model to use for this source
	ExtraConfig map[string]interface{}
}

// ManagerConfig contains configuration for the audio manager
type ManagerConfig struct {
	MaxSources          int
	DefaultBufferSize   int
	EnableMetrics       bool
	MetricsInterval     time.Duration
	ProcessingTimeout   time.Duration
	BufferPoolConfig    BufferPoolConfig
}

// BufferPoolConfig contains configuration for buffer pools
type BufferPoolConfig struct {
	SmallBufferSize   int // Size for small buffers (e.g., 4KB)
	MediumBufferSize  int // Size for medium buffers (e.g., 64KB)
	LargeBufferSize   int // Size for large buffers (e.g., 1MB)
	MaxBuffersPerSize int // Maximum buffers to keep per size category
	EnableMetrics     bool
}