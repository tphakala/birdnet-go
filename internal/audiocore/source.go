// source.go - Audio source type definitions for the audiocore package.
package audiocore

import (
	"strings"
	"time"

	"github.com/tphakala/birdnet-go/internal/errors"
)

// SourceType represents the transport protocol or device class of an audio source.
type SourceType string

const (
	// SourceTypeRTSP identifies RTSP/RTSPS network streams.
	SourceTypeRTSP SourceType = "rtsp"

	// SourceTypeHTTP identifies HTTP/HTTPS audio streams.
	SourceTypeHTTP SourceType = "http"

	// SourceTypeHLS identifies HLS (m3u8) streams.
	SourceTypeHLS SourceType = "hls"

	// SourceTypeRTMP identifies RTMP/RTMPS streams.
	SourceTypeRTMP SourceType = "rtmp"

	// SourceTypeUDP identifies UDP/RTP streams.
	SourceTypeUDP SourceType = "udp"

	// SourceTypeAudioCard identifies local audio capture devices.
	SourceTypeAudioCard SourceType = "audio_card"

	// SourceTypeFile identifies audio files read from disk.
	SourceTypeFile SourceType = "file"

	// SourceTypeUnknown is used when the source type has not yet been determined.
	SourceTypeUnknown SourceType = "unknown"
)

// String returns the string representation of the source type.
func (st SourceType) String() string {
	return string(st)
}

// SourceState represents the operational state of an audio source.
type SourceState int

const (
	// SourceInactive means the source exists but is not currently capturing.
	SourceInactive SourceState = iota

	// SourceStarting means the source is initialising its connection or device.
	SourceStarting

	// SourceRunning means the source is actively capturing and dispatching frames.
	SourceRunning

	// SourceError means the source has encountered an unrecoverable error.
	SourceError

	// SourceStopped means the source was deliberately stopped.
	SourceStopped
)

// String returns a human-readable label for the source state.
func (ss SourceState) String() string {
	switch ss {
	case SourceInactive:
		return "inactive"
	case SourceStarting:
		return "starting"
	case SourceRunning:
		return "running"
	case SourceError:
		return "error"
	case SourceStopped:
		return "stopped"
	default:
		return "unknown"
	}
}

// AudioSource represents a registered audio source with its metadata and runtime state.
// The connectionString field is intentionally private to prevent accidental logging or
// serialisation of sensitive credentials.
type AudioSource struct {
	// ID is the unique identifier for this source (e.g., "rtsp_001", "cam_backyard").
	ID string `json:"id"`

	// DisplayName is a user-friendly label shown in the UI and stored with detections.
	DisplayName string `json:"displayName"`

	// Type identifies the transport protocol or device class.
	Type SourceType `json:"type"`

	// connectionString holds the raw URL or device path — never logged or serialised.
	connectionString string

	// SafeString is a sanitised version of the connection string safe for logging
	// (e.g., credentials replaced with "***").
	SafeString string `json:"safeString"`

	// SampleRate is the capture sample rate in Hz (e.g., 48000).
	SampleRate int `json:"sampleRate"`

	// BitDepth is the capture bit depth (e.g., 16).
	BitDepth int `json:"bitDepth"`

	// Channels is the number of capture channels (e.g., 1 for mono).
	Channels int `json:"channels"`

	// Gain is the configured input gain in dB. 0 means no adjustment.
	Gain float64 `json:"gain"`

	// State is the current operational state of the source.
	State SourceState `json:"state"`

	// RegisteredAt is the time this source was first registered.
	RegisteredAt time.Time `json:"registeredAt"`

	// LastSeen is the time the most recent audio frame was received from this source.
	LastSeen time.Time `json:"lastSeen"`

	// IsActive reports whether the source is currently producing frames.
	IsActive bool `json:"isActive"`

	// TotalBytes is the cumulative count of PCM bytes received from this source.
	TotalBytes int64 `json:"totalBytes"`

	// ErrorCount is the cumulative number of errors encountered by this source.
	ErrorCount int `json:"errorCount"`
}

// GetConnectionString returns the raw connection string (e.g., for passing to FFmpeg).
// Returns an error if the connection string has not been set.
func (s *AudioSource) GetConnectionString() (string, error) {
	if s.connectionString == "" {
		return "", errors.Newf("connection string is empty for source %s (ID: %s)", s.DisplayName, s.ID).
			Component("audiocore").
			Category(errors.CategoryValidation).
			Context("operation", "get_connection_string").
			Context("source_id", s.ID).
			Context("display_name", s.DisplayName).
			Context("source_type", s.Type).
			Build()
	}
	return s.connectionString, nil
}

// SetConnectionString stores the raw connection string for this source.
func (s *AudioSource) SetConnectionString(cs string) {
	s.connectionString = cs
}

// String implements fmt.Stringer and returns the sanitised connection string,
// ensuring that credentials are never inadvertently written to logs.
func (s *AudioSource) String() string {
	return s.SafeString
}

// StreamTypeToSourceType converts a stream type string (e.g., "rtsp", "http")
// to the corresponding SourceType constant.
func StreamTypeToSourceType(streamType string) SourceType {
	switch strings.ToLower(streamType) {
	case "rtsp":
		return SourceTypeRTSP
	case "http":
		return SourceTypeHTTP
	case "hls":
		return SourceTypeHLS
	case "rtmp":
		return SourceTypeRTMP
	case "udp":
		return SourceTypeUDP
	default:
		return SourceTypeUnknown
	}
}

// SourceConfig carries the parameters required to register a new audio source.
type SourceConfig struct {
	// ID is the unique identifier for the source.
	ID string

	// DisplayName is a user-friendly label.
	DisplayName string

	// Type identifies the transport protocol or device class.
	Type SourceType

	// ConnectionString is the raw URL or device path (treated as sensitive).
	ConnectionString string

	// SampleRate is the desired capture sample rate in Hz.
	SampleRate int

	// BitDepth is the desired capture bit depth (e.g., 16).
	BitDepth int

	// Channels is the desired number of capture channels.
	Channels int

	// Gain is the input gain adjustment in dB. 0 means no adjustment.
	// Positive values amplify, negative values attenuate.
	Gain float64
}
