package entities

import "time"

// SourceType represents the type of audio input.
type SourceType string

const (
	SourceTypeRTSP       SourceType = "rtsp"
	SourceTypeALSA       SourceType = "alsa"
	SourceTypePulseAudio SourceType = "pulseaudio"
	SourceTypeFile       SourceType = "file"
	SourceTypeUnknown    SourceType = "unknown"
)

// AudioSource represents a normalized audio input source.
// This normalizes the source_node string from the legacy schema.
type AudioSource struct {
	ID uint `gorm:"primaryKey"`
	// SourceURI is the source identifier (e.g., "rtsp://...", "hw:0,0", "/path/to/file").
	// Named SourceURI (not SourceID) to avoid GORM field name collision with Detection.SourceID
	// which caused GORM to create spurious foreign keys in the wrong direction.
	SourceURI   string     `gorm:"column:source_uri;type:varchar(500);not null;uniqueIndex:idx_audio_source_unique,priority:1"`
	NodeName    string     `gorm:"type:varchar(100);not null;uniqueIndex:idx_audio_source_unique,priority:2;index"`
	SourceType  SourceType `gorm:"type:varchar(20);not null"`
	DisplayName *string    `gorm:"type:varchar(200)"`
	ConfigJSON  *string    `gorm:"type:text"`
	CreatedAt   time.Time  `gorm:"autoCreateTime"`
}

// TableName returns the table name for GORM.
func (AudioSource) TableName() string {
	return "audio_sources"
}
