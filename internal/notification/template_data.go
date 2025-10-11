package notification

import (
	"fmt"
	"log/slog"
	"net/url"
	"os"
	"strings"
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

	// Use the event's location string (e.g., "backyard-camera", "RTSP URL", etc.)
	location := event.GetLocation()

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

// BuildBaseURL constructs the base URL for notification links based on host, port, and TLS settings.
// It returns a fully qualified URL (e.g., "https://example.com:8080" or "http://localhost").
// Default ports (80 for HTTP, 443 for HTTPS) are omitted from the URL for cleaner links.
//
// Host resolution priority (highest to lowest):
//  1. host parameter (from security.host config)
//  2. BIRDNET_HOST environment variable
//  3. "localhost" fallback (with warning log)
//
// The BIRDNET_HOST environment variable should be set to just the hostname/IP without
// scheme or port (e.g., "birdnet.home.arpa" or "192.168.1.100"). The scheme and port
// are determined by the autoTLS and port parameters.
func BuildBaseURL(host, port string, autoTLS bool) string {
	scheme := "http"
	if autoTLS {
		scheme = "https"
	}

	// Priority 1: Use provided host from config (security.host)
	if host == "" {
		// Priority 2: Try BIRDNET_HOST environment variable
		if envHost := os.Getenv("BIRDNET_HOST"); envHost != "" {
			host = strings.TrimSpace(envHost)
			slog.Debug("Using BIRDNET_HOST environment variable for notification URLs", "host", host)
		}
	}

	// Priority 3: Fallback to localhost with warning
	if host == "" {
		host = "localhost"
		slog.Warn("Using localhost for notification URLs. Set security.host in config or BIRDNET_HOST environment variable for proper URL generation when using reverse proxy or remote access")
	}

	// Omit default ports for cleaner URLs
	if (scheme == "https" && port == "443") || (scheme == "http" && port == "80") {
		return fmt.Sprintf("%s://%s", scheme, host)
	}

	return fmt.Sprintf("%s://%s:%s", scheme, host, port)
}
