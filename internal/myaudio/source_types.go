// source_types.go - Core type definitions for audio source registry
package myaudio

import "time"

// SourceType represents the type of audio source
type SourceType string

const (
	SourceTypeRTSP      SourceType = "rtsp"
	SourceTypeAudioCard SourceType = "audio_card"
	SourceTypeFile      SourceType = "file"
	SourceTypeUnknown   SourceType = "unknown" // Used when type needs to be detected
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
}

// GetConnectionString returns the original connection string (for internal use only)
func (s *AudioSource) GetConnectionString() string {
	return s.connectionString
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