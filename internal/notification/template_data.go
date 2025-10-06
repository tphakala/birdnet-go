package notification

import (
	"fmt"
	"net/url"
	"time"

	"github.com/tphakala/birdnet-go/internal/events"
)

type TemplateData struct {
	CommonName         string
	ScientificName     string
	Confidence         float64
	ConfidencePercent  string
	DetectionTime      string
	DetectionDate      string
	Latitude           float64
	Longitude          float64
	Location           string
	DetectionURL       string
	ImageURL           string
	DaysSinceFirstSeen int
}

func NewTemplateData(event events.DetectionEvent, baseURL string, timeAs24h bool) *TemplateData {
	metadata := event.GetMetadata()

	// Get begin_time from metadata
	var beginTime time.Time
	if bt, ok := metadata["begin_time"].(time.Time); ok {
		beginTime = bt
	} else {
		// Fallback to event timestamp if begin_time not in metadata
		beginTime = event.GetTimestamp()
	}

	detectionTime := beginTime.Format("15:04:05")
	detectionDate := beginTime.Format("2006-01-02")
	if !timeAs24h {
		detectionTime = beginTime.Format("3:04:05 PM")
	}

	confidence := event.GetConfidence()
	confidencePercent := fmt.Sprintf("%.0f", confidence*100)

	// Get lat/lon from metadata
	var latitude, longitude float64
	if lat, ok := metadata["latitude"].(float64); ok {
		latitude = lat
	}
	if lon, ok := metadata["longitude"].(float64); ok {
		longitude = lon
	}
	location := fmt.Sprintf("%.6f, %.6f", latitude, longitude)

	// Get note ID from metadata for detection URL
	var noteID string
	if id, ok := metadata["note_id"].(uint); ok {
		noteID = fmt.Sprintf("%d", id)
	}

	var detectionURL string
	if noteID != "" {
		detectionURL = fmt.Sprintf("%s/ui/detections/%s", baseURL, noteID)
	} else {
		detectionURL = fmt.Sprintf("%s/ui/detections", baseURL)
	}

	scientificName := event.GetScientificName()

	// Get image URL from metadata if available (direct URL from image provider)
	// Fall back to proxy URL if not available
	var imageURL string
	if imgURL, ok := metadata["image_url"].(string); ok && imgURL != "" {
		imageURL = imgURL
	} else {
		// Fallback to proxy URL if direct URL not available
		encodedScientificName := url.QueryEscape(scientificName)
		imageURL = fmt.Sprintf("%s/api/v2/media/species-image?scientific_name=%s", baseURL, encodedScientificName)
	}

	return &TemplateData{
		CommonName:         event.GetSpeciesName(),
		ScientificName:     scientificName,
		Confidence:         confidence,
		ConfidencePercent:  confidencePercent,
		DetectionTime:      detectionTime,
		DetectionDate:      detectionDate,
		Latitude:           latitude,
		Longitude:          longitude,
		Location:           location,
		DetectionURL:       detectionURL,
		ImageURL:           imageURL,
		DaysSinceFirstSeen: event.GetDaysSinceFirstSeen(),
	}
}

func BuildBaseURL(host string, port string, autoTLS bool) string {
	scheme := "http"
	if autoTLS {
		scheme = "https"
	}

	if host == "" {
		host = "localhost"
	}

	if (scheme == "https" && port == "443") || (scheme == "http" && port == "80") {
		return fmt.Sprintf("%s://%s", scheme, host)
	}

	return fmt.Sprintf("%s://%s:%s", scheme, host, port)
}
