// Package audiocore provides the core audio infrastructure for BirdNET-Go.
//
// It handles audio capture from multiple sources (RTSP streams, HTTP streams,
// local audio cards), routing audio frames to multiple consumers (model adapters,
// sound level processors), and runtime reconfiguration without application restart.
//
// AudioFrame.Data is read-only after creation. Sources must copy data out of
// upstream buffers before constructing a frame. No consumer may modify the Data
// slice after dispatch.
package audiocore

import "time"

// AudioFrame is the universal unit of audio data flowing through the system.
// Data is read-only after creation — consumers must not modify it.
//
// TODO: Consider object pooling (sync.Pool) for AudioFrame if profiling shows
// allocation pressure from high-frequency frame creation. The Data slice would
// need careful lifetime management to avoid use-after-return races — frames
// must not be returned to the pool until all consumers have finished reading.
type AudioFrame struct {
	// SourceID uniquely identifies the audio source that produced this frame.
	SourceID string

	// SourceName is the human-readable label (e.g., "Frontyard birdfeeder").
	// Stored with detections for provenance tracking.
	SourceName string

	// Data contains the raw PCM audio bytes. Read-only after frame creation.
	Data []byte

	// SampleRate in Hz (e.g., 48000, 32000).
	SampleRate int

	// BitDepth in bits (e.g., 16).
	BitDepth int

	// Channels count (e.g., 1 for mono).
	Channels int

	// Timestamp when this frame was captured.
	Timestamp time.Time
}

// AudioConsumer receives audio frames from the router.
// Implemented by model adapters, sound level processors, buffer writers, etc.
type AudioConsumer interface {
	// ID returns a unique identifier for this consumer.
	ID() string

	// SampleRate returns the expected sample rate in Hz.
	// The router creates a resampler if this differs from the source rate.
	SampleRate() int

	// BitDepth returns the expected bit depth.
	BitDepth() int

	// Channels returns the expected channel count.
	Channels() int

	// Write delivers an audio frame to this consumer.
	// Implementations must not modify frame.Data.
	Write(frame AudioFrame) error

	// Close releases resources held by this consumer.
	Close() error
}

// AudioDispatcher is the narrow interface for pushing frames into the routing layer.
// Used by DeviceManager and ffmpeg.Manager to avoid coupling to the full AudioEngine.
type AudioDispatcher interface {
	Dispatch(frame AudioFrame)
}
