// Package dto contains data transfer objects for API v2 responses.
package dto

import (
	"time"

	"github.com/tphakala/birdnet-go/internal/detection"
)

// DetectionResponse is the API response for detection endpoints.
// Uses camelCase JSON tags per REST API conventions.
type DetectionResponse struct {
	ID             uint        `json:"id"`
	Date           string      `json:"date"`           // "2024-01-15"
	Time           string      `json:"time"`           // "14:30:00"
	Timestamp      string      `json:"timestamp"`      // ISO8601 with TZ
	ScientificName string      `json:"scientificName"`
	CommonName     string      `json:"commonName"`
	SpeciesCode    string      `json:"speciesCode,omitempty"`
	Confidence     float64     `json:"confidence"`
	Latitude       float64     `json:"latitude,omitempty"`
	Longitude      float64     `json:"longitude,omitempty"`
	ClipName       string      `json:"clipName,omitempty"`
	Verified       string      `json:"verified,omitempty"`
	Locked         bool        `json:"locked"`
	Source         *SourceInfo `json:"source,omitempty"`
	Model          *ModelInfo  `json:"model,omitempty"`
	Comments       []Comment   `json:"comments,omitempty"`

	// Time context
	BeginTime string `json:"beginTime,omitempty"`
	EndTime   string `json:"endTime,omitempty"`
	TimeOfDay string `json:"timeOfDay,omitempty"`

	// Species tracking metadata
	IsNewSpecies       bool   `json:"isNewSpecies,omitempty"`
	DaysSinceFirstSeen int    `json:"daysSinceFirstSeen,omitempty"`
	IsNewThisYear      bool   `json:"isNewThisYear,omitempty"`
	IsNewThisSeason    bool   `json:"isNewThisSeason,omitempty"`
	DaysThisYear       int    `json:"daysThisYear,omitempty"`
	DaysThisSeason     int    `json:"daysThisSeason,omitempty"`
	CurrentSeason      string `json:"currentSeason,omitempty"`

	// Weather data (populated on demand)
	Weather *WeatherInfo `json:"weather,omitempty"`
}

// SourceInfo describes the audio source in API responses.
type SourceInfo struct {
	ID          string `json:"id"`
	Type        string `json:"type,omitempty"`
	DisplayName string `json:"displayName,omitempty"`
}

// ModelInfo describes the AI model in API responses.
type ModelInfo struct {
	Name    string `json:"name"`
	Version string `json:"version"`
	Custom  bool   `json:"custom"`
}

// Comment represents a detection comment in API responses.
type Comment struct {
	ID        uint   `json:"id"`
	Entry     string `json:"entry"`
	CreatedAt string `json:"createdAt"`
	UpdatedAt string `json:"updatedAt"`
}

// WeatherInfo represents weather data in API responses.
type WeatherInfo struct {
	WeatherIcon string  `json:"weatherIcon"`
	WeatherMain string  `json:"weatherMain,omitempty"`
	Description string  `json:"description,omitempty"`
	Temperature float64 `json:"temperature,omitempty"`
	WindSpeed   float64 `json:"windSpeed,omitempty"`
	WindGust    float64 `json:"windGust,omitempty"`
	Humidity    int     `json:"humidity,omitempty"`
	Units       string  `json:"units,omitempty"`
}

// NewDetectionResponse creates a DetectionResponse from a detection.Result.
func NewDetectionResponse(r *detection.Result) *DetectionResponse {
	if r == nil {
		return nil
	}

	dto := &DetectionResponse{
		ID:             r.ID,
		Date:           r.Date(),
		Time:           r.Time(),
		Timestamp:      r.Timestamp.Format(time.RFC3339),
		ScientificName: r.Species.ScientificName,
		CommonName:     r.Species.CommonName,
		SpeciesCode:    r.Species.Code,
		Confidence:     r.Confidence,
		Latitude:       r.Latitude,
		Longitude:      r.Longitude,
		ClipName:       r.ClipName,
		Verified:       r.Verified,
		Locked:         r.Locked,
	}

	// Only set time fields if non-zero to avoid "0001-01-01T00:00:00Z" in API
	if !r.BeginTime.IsZero() {
		dto.BeginTime = r.BeginTime.Format(time.RFC3339)
	}
	if !r.EndTime.IsZero() {
		dto.EndTime = r.EndTime.Format(time.RFC3339)
	}

	// Add source info if available
	if r.AudioSource.ID != "" {
		dto.Source = &SourceInfo{
			ID:          r.AudioSource.ID,
			Type:        r.AudioSource.Type,
			DisplayName: r.AudioSource.DisplayName,
		}
	}

	// Add model info
	if r.Model.Name != "" {
		dto.Model = &ModelInfo{
			Name:    r.Model.Name,
			Version: r.Model.Version,
			Custom:  r.Model.Custom,
		}
	}

	// Convert comments
	if len(r.Comments) > 0 {
		dto.Comments = make([]Comment, len(r.Comments))
		for i, c := range r.Comments {
			dto.Comments[i] = Comment{
				ID:        c.ID,
				Entry:     c.Entry,
				CreatedAt: c.CreatedAt.Format(time.RFC3339),
				UpdatedAt: c.UpdatedAt.Format(time.RFC3339),
			}
		}
	}

	return dto
}

// PaginatedResponse represents a paginated API response.
type PaginatedResponse struct {
	Data        any   `json:"data"`
	Total       int64 `json:"total"`
	Limit       int   `json:"limit"`
	Offset      int   `json:"offset"`
	CurrentPage int   `json:"current_page"`
	TotalPages  int   `json:"total_pages"`
}

// NewPaginatedResponse creates a paginated response from detection results.
func NewPaginatedResponse(results []*detection.Result, total int64, limit, offset int) *PaginatedResponse {
	// Guard against division by zero
	if limit <= 0 {
		limit = 1
	}

	detections := make([]*DetectionResponse, len(results))
	for i, r := range results {
		detections[i] = NewDetectionResponse(r)
	}

	currentPage := (offset / limit) + 1
	totalPages := int((total + int64(limit) - 1) / int64(limit))

	return &PaginatedResponse{
		Data:        detections,
		Total:       total,
		Limit:       limit,
		Offset:      offset,
		CurrentPage: currentPage,
		TotalPages:  totalPages,
	}
}
