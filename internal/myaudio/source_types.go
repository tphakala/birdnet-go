// source_types.go - Core type definitions for audio source registry
package myaudio

import (
	"sync/atomic"
	"time"

	"github.com/tphakala/birdnet-go/internal/errors"
	"github.com/tphakala/birdnet-go/internal/myaudio/equalizer"
)

// SourceType represents the type of audio source
type SourceType string

const (
	SourceTypeRTSP      SourceType = "rtsp"       // RTSP/RTSPS streams
	SourceTypeHTTP      SourceType = "http"       // HTTP/HTTPS audio streams
	SourceTypeHLS       SourceType = "hls"        // HLS (m3u8) streams
	SourceTypeRTMP      SourceType = "rtmp"       // RTMP/RTMPS streams
	SourceTypeUDP       SourceType = "udp"        // UDP/RTP streams
	SourceTypeAudioCard SourceType = "audio_card" // Local audio devices
	SourceTypeFile      SourceType = "file"       // Audio files
	SourceTypeUnknown   SourceType = "unknown"    // Used when type needs to be detected
)

// AudioSource represents a registered audio source with its metadata
type AudioSource struct {
	// Core identification
	ID          string     `json:"id"`          // Unique identifier (e.g., "rtsp_001", "cam_backyard")
	DisplayName string     `json:"displayName"` // User-friendly name
	Type        SourceType `json:"type"`        // Source type

	// Connection information (private)
	connectionString string // NEVER exposed in logs or API

	// Safe logging
	SafeString string `json:"safeString"` // Sanitized version for logging

	// Basic tracking
	RegisteredAt time.Time `json:"registeredAt"`
	IsActive     bool      `json:"isActive"`
	LastSeen     time.Time `json:"lastSeen"`

	// Simple metrics
	TotalBytes int64 `json:"totalBytes"`
	ErrorCount int   `json:"errorCount"`

	// Per-source filter chain (atomic for lock-free access during audio processing)
	filterChain atomic.Pointer[equalizer.FilterChain] //nolint:govet // nocopy is acceptable; ListSources uses copyForAPI()
}

// GetFilterChain returns the source's filter chain atomically (lock-free).
func (s *AudioSource) GetFilterChain() *equalizer.FilterChain {
	return s.filterChain.Load()
}

// SetFilterChain atomically replaces the source's filter chain.
func (s *AudioSource) SetFilterChain(chain *equalizer.FilterChain) {
	s.filterChain.Store(chain)
}

// copyForAPI creates a copy of the source safe for API responses.
// It omits the atomic filterChain field (which cannot be safely copied)
// and the private connectionString.
func (s *AudioSource) copyForAPI() *AudioSource {
	return &AudioSource{
		ID:           s.ID,
		DisplayName:  s.DisplayName,
		Type:         s.Type,
		SafeString:   s.SafeString,
		RegisteredAt: s.RegisteredAt,
		IsActive:     s.IsActive,
		LastSeen:     s.LastSeen,
		TotalBytes:   s.TotalBytes,
		ErrorCount:   s.ErrorCount,
	}
}

// GetConnectionString returns the raw connection string for external use (e.g., FFmpeg input)
// Returns an error if the connection string is empty or invalid
func (s *AudioSource) GetConnectionString() (string, error) {
	if s.connectionString == "" {
		return "", errors.Newf("connection string is empty for source %s (ID: %s)", s.DisplayName, s.ID).
			Component("myaudio").
			Category(errors.CategoryValidation).
			Context("operation", "get_connection_string").
			Context("source_id", s.ID).
			Context("display_name", s.DisplayName).
			Context("source_type", s.Type).
			Build()
	}
	return s.connectionString, nil
}

// String implements the Stringer interface to ensure safe logging
func (s *AudioSource) String() string {
	return s.SafeString
}

// SourceConfig is used for source registration
type SourceConfig struct {
	ID          string
	DisplayName string
	Type        SourceType
}
