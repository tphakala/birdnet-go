package audiocore

import (
	"context"
	"time"
)

// StreamManager is the contract the engine drives network stream producers
// through. It is implemented by ffmpeg.Manager today and by stream.Manager
// later, so both producers satisfy the same surface without importing the
// engine. It is distinct from schedule.StreamManager, which is the engine-level
// stop/start surface the quiet-hours scheduler uses.
type StreamManager interface {
	// StartStream starts capture for spec.SourceID and begins dispatching
	// AudioFrames through the manager's frame callback. It returns an error when
	// the ID is already running or spec is invalid; connection failures are
	// asynchronous and surface through StreamHealth.
	StartStream(spec *StreamSpec) error
	// StopStream stops capture for sourceID and forgets it.
	StopStream(sourceID string) error
	// StreamHealth returns a point-in-time snapshot for one stream.
	StreamHealth(sourceID string) (*StreamHealth, error)
	// AllStreamHealth returns snapshots for every tracked stream, keyed by source ID.
	AllStreamHealth() map[string]*StreamHealth
	// GetActiveStreamIDs lists the currently tracked source IDs.
	GetActiveStreamIDs() []string
	// SetOnStreamReset registers the callback invoked after a stream starts or is
	// fully reset under the same sourceID.
	SetOnStreamReset(fn func(sourceID string))
	// Shutdown stops every stream with the manager's default timeout.
	Shutdown() error
	// ShutdownWithContext stops every stream honouring ctx.
	ShutdownWithContext(ctx context.Context) error
}

// StreamSpec is the protocol-neutral per-stream configuration handed to
// StreamManager.StartStream. Producer-specific options (the FFmpeg binary path,
// extra FFmpeg parameters, log level) belong to the producer's constructor
// options, not here.
type StreamSpec struct {
	// SourceID is the unique identifier for this source.
	SourceID string
	// SourceName is the human-readable display name.
	SourceName string
	// URL is the stream URL (e.g. rtsp://host/stream).
	URL string
	// Type is the source type (rtsp, http, hls, udp, ...).
	Type SourceType
	// SampleRate is the desired output rate in Hz (48000, or the bat rate).
	SampleRate int
	// SourceSampleRate is the probed source rate; 0 when unknown.
	SourceSampleRate int
	// BitDepth is the output bit depth in bits (16).
	BitDepth int
	// Channels is the target channel count (1).
	Channels int
	// SourceChannels is the probed source channel count; 0 when unknown.
	SourceChannels int
	// ChannelMode controls multi-channel handling (downmix, left, right).
	ChannelMode string
	// MediaMode controls which RTSP media is requested (auto, audio-only,
	// full-stream). Applied for RTSP sources only.
	MediaMode string
	// Transport is the RTSP transport protocol (tcp, udp).
	Transport string
	// HealthyDataThreshold is how long without data before marking unhealthy.
	// Zero uses the producer default.
	HealthyDataThreshold time.Duration
	// Debug enables verbose debug logging for this stream.
	Debug bool
}
