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
// Data is read-only after creation; consumers must not modify it.
//
// Data may be backed by a pooled slice. When Ref is non-nil, the slice is
// managed by a FrameRef and must not be retained beyond the consumer's Write
// return. AudioRouter.Dispatch retains the ref once per successful enqueue and
// the drainer releases after Write; producers that do not pool leave Ref nil,
// in which case Data's lifetime is governed by GC as before.
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

	// Ref, when non-nil, tracks pooled ownership of Data. Producers that
	// allocate Data from a buffer.Manager pool attach a FrameRef whose
	// release closure returns the slice. Ref is nil when Data is a plain
	// make() allocation (tests, legacy construction, streams without a
	// wired bufMgr).
	Ref *FrameRef
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
	//
	// Buffer ownership contract:
	// Implementations MUST NOT retain frame.Data after Write returns. The
	// caller may recycle the underlying slice into a buffer pool immediately
	// after Write completes, so any asynchronous use (channel send, goroutine
	// hand-off, queue enqueue) MUST copy the data first. See hlsConsumer.Write
	// in internal/api/v2/audio_hls.go for an example of correct
	// copy-before-enqueue behaviour.
	Write(frame AudioFrame) error

	// Close releases resources held by this consumer.
	Close() error
}

// AudioDispatcher is the narrow interface for pushing frames into the routing layer.
// Used by DeviceManager and ffmpeg.Manager to avoid coupling to the full AudioEngine.
type AudioDispatcher interface {
	Dispatch(frame AudioFrame)
}
