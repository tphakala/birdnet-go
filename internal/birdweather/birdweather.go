// birdweather.go this code implements a BirdWeather API client for uploading soundscapes and detections.
package birdweather

import (
	"bytes"
	"compress/gzip"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log"
	"math"
	"math/rand"
	"net"
	"net/http"
	"net/url"
	"time"

	"github.com/tphakala/birdnet-go/internal/conf"
	"github.com/tphakala/birdnet-go/internal/datastore"
)

// SoundscapeResponse represents the JSON structure of the response from the Birdweather API when uploading a soundscape.
type SoundscapeResponse struct {
	Success    bool `json:"success"`
	Soundscape struct {
		ID        int     `json:"id"`
		StationID int     `json:"stationId"`
		Timestamp string  `json:"timestamp"`
		URL       *string `json:"url"` // Pointer to handle null
		Filesize  int     `json:"filesize"`
		Extension string  `json:"extension"`
		Duration  *string `json:"duration"` // Pointer to handle null
	} `json:"soundscape"`
}

// BwClient holds the configuration for interacting with the Birdweather API.
type BwClient struct {
	Settings      *conf.Settings
	BirdweatherID string
	Accuracy      float64
	Latitude      float64
	Longitude     float64
	HTTPClient    *http.Client
}

// New creates and initializes a new BwClient with the given settings.
// The HTTP client is configured with a 45-second timeout to prevent hanging requests.
func New(settings *conf.Settings) (*BwClient, error) {
	// We expect that Birdweather ID is validated before this function is called
	return &BwClient{
		Settings:      settings,
		BirdweatherID: settings.Realtime.Birdweather.ID,
		Accuracy:      settings.Realtime.Birdweather.LocationAccuracy,
		Latitude:      settings.BirdNET.Latitude,
		Longitude:     settings.BirdNET.Longitude,
		HTTPClient:    &http.Client{Timeout: 45 * time.Second},
	}, nil
}

// RandomizeLocation adds a random offset to the given latitude and longitude to fuzz the location
// within a specified radius in meters for privacy, truncating the result to 4 decimal places.
// radiusMeters - the maximum radius in meters to adjust the coordinates
func (b *BwClient) RandomizeLocation(radiusMeters float64) (float64, float64) {
	// Create a new local random generator seeded with current Unix time
	src := rand.NewSource(time.Now().UnixNano())
	rnd := rand.New(src)

	// Calculate the degree offset using an approximation that 111,000 meters equals 1 degree
	degreeOffset := radiusMeters / 111000

	// Generate random offsets within +/- degreeOffset
	latOffset := (rnd.Float64() - 0.5) * 2 * degreeOffset
	lonOffset := (rnd.Float64() - 0.5) * 2 * degreeOffset

	// Apply the offsets to the original coordinates and truncate to 4 decimal places
	newLatitude := math.Floor((b.Latitude+latOffset)*10000) / 10000
	newLongitude := math.Floor((b.Longitude+lonOffset)*10000) / 10000

	return newLatitude, newLongitude
}

// handleNetworkError handles network errors and returns a more specific error message.
func handleNetworkError(err error) error {
	var netErr net.Error
	if errors.As(err, &netErr) && netErr.Timeout() {
		return fmt.Errorf("request timed out: %w", err)
	}
	var urlErr *url.Error
	if errors.As(err, &urlErr) {
		var dnsErr *net.DNSError
		if errors.As(urlErr.Err, &dnsErr) {
			return fmt.Errorf("DNS resolution failed: %w", err)
		}
	}
	return fmt.Errorf("network error: %w", err)
}

// UploadSoundscape uploads a soundscape file to the Birdweather API and returns the soundscape ID if successful.
// It handles the PCM to WAV conversion, compresses the data, and manages HTTP request creation and response handling safely.
func (b *BwClient) UploadSoundscape(timestamp string, pcmData []byte) (soundscapeID string, err error) {
	// Add check for empty pcmData
	if len(pcmData) == 0 {
		return "", fmt.Errorf("pcmData is empty")
	}

	// Encode PCM data to WAV format
	wavBuffer, err := encodePCMtoWAV(pcmData)
	if err != nil {
		log.Printf("Failed to encode PCM to WAV: %v\n", err)
		return "", fmt.Errorf("failed to encode PCM to WAV: %w", err)
	}

	// Compress the WAV data
	var gzipWavData bytes.Buffer
	gzipWriter := gzip.NewWriter(&gzipWavData)
	if _, err := io.Copy(gzipWriter, wavBuffer); err != nil {
		log.Printf("Failed to compress WAV data: %v\n", err)
		return "", fmt.Errorf("failed to compress WAV data: %w", err)
	}
	if err := gzipWriter.Close(); err != nil {
		log.Printf("Failed to finalize compression: %v\n", err)
		return "", fmt.Errorf("failed to finalize compression: %w", err)
	}

	// Create and execute the POST request
	soundscapeURL := fmt.Sprintf("https://app.birdweather.com/api/v1/stations/%s/soundscapes?timestamp=%s", b.BirdweatherID, timestamp)
	req, err := http.NewRequest("POST", soundscapeURL, &gzipWavData)
	if err != nil {
		log.Printf("Failed to create POST request: %v\n", err)
		return "", fmt.Errorf("failed to create POST request: %w", err)
	}
	req.Header.Set("Content-Type", "application/octet-stream")
	req.Header.Set("Content-Encoding", "gzip")
	req.Header.Set("User-Agent", "BirdNET-Go")

	// Execute the request
	resp, err := b.HTTPClient.Do(req)
	if err != nil {
		log.Printf("Request to upload soundscape failed: %v\n", err)
		return "", handleNetworkError(err)
	}
	if resp == nil {
		return "", fmt.Errorf("received nil response")
	}
	defer resp.Body.Close()

	// Process the response
	responseBody, err := io.ReadAll(resp.Body)
	if err != nil {
		log.Printf("Failed to read response body: %v\n", err)
		return "", fmt.Errorf("failed to read response body: %w", err)
	}

	if b.Settings.Realtime.Birdweather.Debug {
		log.Println("Response Body:", string(responseBody))
	}

	var sdata SoundscapeResponse
	if err := json.Unmarshal(responseBody, &sdata); err != nil {
		log.Printf("Failed to decode JSON response: %v\n", err)
		return "", fmt.Errorf("failed to decode JSON response: %w", err)
	}

	if !sdata.Success {
		log.Println("Upload was not successful according to the response")
		return "", fmt.Errorf("upload failed, response reported failure")
	}

	return fmt.Sprintf("%d", sdata.Soundscape.ID), nil
}

// PostDetection posts a detection to the Birdweather API matching the specified soundscape ID.
func (b *BwClient) PostDetection(soundscapeID, timestamp, commonName, scientificName string, confidence float64) error {
	// Simple input validation
	if soundscapeID == "" || timestamp == "" || commonName == "" || scientificName == "" {
		return fmt.Errorf("invalid input: all string parameters must be non-empty")
	}

	detectionURL := fmt.Sprintf("https://app.birdweather.com/api/v1/stations/%s/detections", b.BirdweatherID)

	if b.Settings.Realtime.Birdweather.Debug {
		log.Println("Posting detection to Birdweather: ", detectionURL)
	}

	// Fuzz location coordinates with user defined accuracy
	fuzzedLatitude, fuzzedLongitude := b.RandomizeLocation(b.Accuracy)

	// Convert timestamp to time.Time and calculate end time
	parsedTime, err := time.Parse("2006-01-02T15:04:05.000-0700", timestamp)
	if err != nil {
		log.Printf("Failed to parse timestamp: %s, err: %v\n", timestamp, err)
		return fmt.Errorf("failed to parse timestamp: %w", err)
	}
	endTime := parsedTime.Add(3 * time.Second).Format("2006-01-02T15:04:05.000-0700") // Add 3 seconds to timestamp for endTime

	// Prepare JSON payload for POST request
	postData := struct {
		Timestamp           string  `json:"timestamp"`
		Latitude            float64 `json:"lat"`
		Longitude           float64 `json:"lon"`
		SoundscapeID        string  `json:"soundscapeId"`
		SoundscapeStartTime string  `json:"soundscapeStartTime"`
		SoundscapeEndTime   string  `json:"soundscapeEndTime"`
		CommonName          string  `json:"commonName"`
		ScientificName      string  `json:"scientificName"`
		Algorithm           string  `json:"algorithm"`
		Confidence          string  `json:"confidence"`
	}{
		Timestamp:           timestamp,
		Latitude:            fuzzedLatitude,
		Longitude:           fuzzedLongitude,
		SoundscapeID:        soundscapeID,
		SoundscapeStartTime: timestamp,
		SoundscapeEndTime:   endTime,
		CommonName:          commonName,
		ScientificName:      scientificName,
		Algorithm:           "2p4",
		Confidence:          fmt.Sprintf("%.2f", confidence),
	}

	// Marshal JSON data
	postDataBytes, err := json.Marshal(postData)
	if err != nil {
		log.Printf("Failed to marshal JSON data, err: %v\n", err)
		return fmt.Errorf("failed to marshal JSON data: %w", err)
	}

	if b.Settings.Realtime.Birdweather.Debug {
		log.Println("JSON Payload:", string(postDataBytes))
	}

	// Execute POST request
	resp, err := b.HTTPClient.Post(detectionURL, "application/json", bytes.NewBuffer(postDataBytes))
	if err != nil {
		log.Printf("Failed to post detection, err: %v\n", err)
		return handleNetworkError(err)
	}
	if resp == nil {
		return fmt.Errorf("received nil response")
	}
	defer resp.Body.Close()

	// Handle response
	if resp.StatusCode != http.StatusCreated {
		responseBody, err := io.ReadAll(resp.Body)
		if err != nil {
			log.Printf("Failed to read response body: %v\n", err)
			return fmt.Errorf("failed to read response body: %w", err)
		}
		log.Printf("Failed to post detection, status code: %d, response body: %s\n", resp.StatusCode, string(responseBody))
		return fmt.Errorf("failed to post detection, status code: %d, response: %s", resp.StatusCode, string(responseBody))
	}

	return nil
}

// Upload function handles the uploading of detected clips and their details to Birdweather.
// It first parses the timestamp from the note, then uploads the soundscape, and finally posts the detection.
func (b *BwClient) Publish(note datastore.Note, pcmData []byte) error {
	// Add check for empty pcmData
	if len(pcmData) == 0 {
		return fmt.Errorf("pcmData is empty")
	}

	// Use system's local timezone for timestamp parsing
	loc := time.Local

	// Combine date and time from note to form a full timestamp string
	dateTimeString := fmt.Sprintf("%sT%s", note.Date, note.Time)

	// Parse the timestamp using the given format and the system's local timezone
	parsedTime, err := time.ParseInLocation("2006-01-02T15:04:05", dateTimeString, loc)
	if err != nil {
		log.Printf("Error parsing date: %v\n", err)
		return fmt.Errorf("error parsing date: %w", err)
	}

	// Format the parsed time to the required timestamp format with timezone information
	timestamp := parsedTime.Format("2006-01-02T15:04:05.000-0700")

	// Upload the soundscape to Birdweather and retrieve the soundscape ID
	soundscapeID, err := b.UploadSoundscape(timestamp, pcmData)
	if err != nil {
		log.Printf("Failed to upload soundscape to Birdweather: %v\n", err)
		return fmt.Errorf("failed to upload soundscape to Birdweather: %w", err)
	}

	// Log the successful posting of the soundscape, if debugging is enabled
	if b.Settings.Realtime.Birdweather.Debug {
		log.Println("Soundscape successfully posted to Birdweather")
	}

	// Post the detection details to Birdweather using the retrieved soundscape ID
	err = b.PostDetection(soundscapeID, timestamp, note.CommonName, note.ScientificName, note.Confidence)
	if err != nil {
		log.Printf("Failed to post detection to Birdweather: %v\n", err)
		return fmt.Errorf("failed to post detection to Birdweather: %w", err)
	}

	return nil
}
