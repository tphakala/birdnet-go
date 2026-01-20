// Package mqtt provides MQTT client functionality and data transfer objects.
package mqtt

import (
	"time"

	"github.com/tphakala/birdnet-go/internal/detection"
	"github.com/tphakala/birdnet-go/internal/imageprovider"
)

// MQTTEventDTO is the data transfer object for MQTT publishing.
// Structure is flat and uses PascalCase to maintain backward
// compatibility with existing Home Assistant automations.
//
// IMPORTANT: Field names are part of the MQTT API contract.
// DO NOT CHANGE existing field names without explicit maintainer approval.
// See mqtt_api_contract_test.go for the complete contract specification.
type MQTTEventDTO struct {
	// ===========================================================================
	// Legacy fields - DO NOT CHANGE (breaks existing HA automations)
	// These use PascalCase via Go's default JSON marshaling
	// ===========================================================================
	Date           string  `json:"Date"`           // "2024-01-15"
	Time           string  `json:"Time"`           // "14:30:00"
	CommonName     string  `json:"CommonName"`
	ScientificName string  `json:"ScientificName"`
	Confidence     float64 `json:"Confidence"`
	Latitude       float64 `json:"Latitude"`
	Longitude      float64 `json:"Longitude"`
	ClipName       string  `json:"ClipName"`
	ProcessingTime float64 `json:"ProcessingTime"` // Nanoseconds as float (matching existing behavior)

	// ===========================================================================
	// Existing fields with specific casing (part of API contract)
	// ===========================================================================
	DetectionID uint    `json:"detectionId"`            // camelCase - database ID for URL construction
	SourceID    string  `json:"sourceId"`               // camelCase - audio source ID for HA filtering
	Occurrence  float64 `json:"occurrence,omitempty"`   // lowercase with omitempty

	// ===========================================================================
	// BirdImage (PascalCase for backward compatibility - DO NOT CHANGE)
	// ===========================================================================
	BirdImage *BirdImageDTO `json:"BirdImage,omitempty"`

	// ===========================================================================
	// New fields - can be added without breaking existing automations
	// These use camelCase per modern JSON conventions
	// ===========================================================================
	SourceType    string `json:"sourceType,omitempty"`    // "rtsp", "alsa", "pulseaudio"
	SourceName    string `json:"sourceName,omitempty"`    // Display name
	ModelName     string `json:"modelName,omitempty"`     // "BirdNET-Analyzer"
	ModelVersion  string `json:"modelVersion,omitempty"`  // "2.4"
	IsCustomModel bool   `json:"isCustomModel,omitempty"` // Custom model flag
	Timezone      string `json:"timezone,omitempty"`      // e.g., "Europe/Helsinki"
}

// BirdImageDTO represents the species thumbnail in MQTT payloads.
// CRITICAL: Must use PascalCase to match existing MQTT contract.
type BirdImageDTO struct {
	URL            string `json:"URL"`
	ScientificName string `json:"ScientificName"`
	LicenseName    string `json:"LicenseName"`
	LicenseURL     string `json:"LicenseURL"`
	AuthorName     string `json:"AuthorName"`
	AuthorURL      string `json:"AuthorURL"`
	CachedAt       string `json:"CachedAt,omitempty"`       // ISO8601 timestamp
	SourceProvider string `json:"SourceProvider,omitempty"` // "wikimedia", "flickr", etc.
}

// NewMQTTEventDTO creates an MQTTEventDTO from a detection.Result.
func NewMQTTEventDTO(r *detection.Result) *MQTTEventDTO {
	dto := &MQTTEventDTO{
		Date:           r.Date(),
		Time:           r.Time(),
		CommonName:     r.Species.CommonName,
		ScientificName: r.Species.ScientificName,
		Confidence:     r.Confidence,
		Latitude:       r.Latitude,
		Longitude:      r.Longitude,
		ClipName:       r.ClipName,
		ProcessingTime: float64(r.ProcessingTime), // Nanoseconds as float
		DetectionID:    r.ID,
		SourceID:       r.AudioSource.ID,
		Occurrence:     r.Occurrence,
		SourceType:     r.AudioSource.Type,
		SourceName:     r.AudioSource.DisplayName,
		ModelName:      r.Model.Name,
		ModelVersion:   r.Model.Version,
		IsCustomModel:  r.Model.Custom,
	}

	// Add timezone if timestamp has location info
	if r.Timestamp.Location() != nil {
		dto.Timezone = r.Timestamp.Location().String()
	}

	return dto
}

// SetBirdImage adds bird image data to the DTO.
func (dto *MQTTEventDTO) SetBirdImage(img *imageprovider.BirdImage) {
	if img == nil || img.URL == "" {
		return
	}

	dto.BirdImage = &BirdImageDTO{
		URL:            img.URL,
		ScientificName: img.ScientificName,
		LicenseName:    img.LicenseName,
		LicenseURL:     img.LicenseURL,
		AuthorName:     img.AuthorName,
		AuthorURL:      img.AuthorURL,
		SourceProvider: img.SourceProvider,
	}

	if !img.CachedAt.IsZero() {
		dto.BirdImage.CachedAt = img.CachedAt.Format(time.RFC3339)
	}
}
