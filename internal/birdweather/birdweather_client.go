// birdweather.go this code implements a BirdWeather API client for uploading soundscapes and detections.
package birdweather

import (
	"bytes"
	"compress/gzip"
	"context"
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
	"os"
	"path/filepath"
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
		Duration  float64 `json:"duration"` // Duration in seconds
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

// BirdweatherClientInterface defines what methods a BirdweatherClient must have
type Interface interface {
	Publish(note *datastore.Note, pcmData []byte) error
	UploadSoundscape(timestamp string, pcmData []byte) (soundscapeID string, err error)
	PostDetection(soundscapeID, timestamp, commonName, scientificName string, confidence float64) error
	TestConnection(ctx context.Context, resultChan chan<- TestResult)
	Close()
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
func (b *BwClient) RandomizeLocation(radiusMeters float64) (latitude, longitude float64) {
	// Create a new local random generator seeded with current Unix time
	src := rand.NewSource(time.Now().UnixNano())
	rnd := rand.New(src)

	// Calculate the degree offset using an approximation that 111,000 meters equals 1 degree
	degreeOffset := radiusMeters / 111000

	// Generate random offsets within +/- degreeOffset
	latOffset := (rnd.Float64() - 0.5) * 2 * degreeOffset
	lonOffset := (rnd.Float64() - 0.5) * 2 * degreeOffset

	// Apply the offsets to the original coordinates and truncate to 4 decimal places
	latitude = math.Floor((b.Latitude+latOffset)*10000) / 10000
	longitude = math.Floor((b.Longitude+lonOffset)*10000) / 10000

	return latitude, longitude
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
		log.Printf("❌ Failed to encode PCM to WAV: %v\n", err)
		return "", fmt.Errorf("failed to encode PCM to WAV: %w", err)
	}

	// If debug is enabled, save the WAV file locally with timestamp information
	if b.Settings.Realtime.Birdweather.Debug {
		// Parse the timestamp
		parsedTime, parseErr := time.Parse("2006-01-02T15:04:05.000-0700", timestamp)
		if parseErr != nil {
			log.Printf("🔍 Attempting to save debug WAV file with timestamp: %s", timestamp)
			log.Printf("⚠️ Warning: couldn't parse timestamp for debug WAV file: %v", parseErr)
		} else {
			// Create a debug directory for WAV files
			debugDir := filepath.Join("debug", "birdweather", "wav")

			// Generate a unique filename based on the timestamp
			debugFilename := filepath.Join(debugDir, fmt.Sprintf("bw_debug_%s.wav",
				parsedTime.Format("20060102_150405")))

			// Calculate the end time (3 seconds after start)
			endTime := parsedTime.Add(3 * time.Second)

			// Save the WAV buffer with timestamp information
			wavCopy := bytes.NewBuffer(wavBuffer.Bytes())
			if saveErr := saveBufferToWAV(wavCopy, debugFilename, parsedTime, endTime); saveErr != nil {
				log.Printf("⚠️ Warning: couldn't save debug WAV file: %v", saveErr)
			} else {
				log.Printf("✅ Saved debug WAV file to %s", debugFilename)
			}
		}
	}

	// Compress the WAV data
	var gzipWavData bytes.Buffer
	gzipWriter := gzip.NewWriter(&gzipWavData)
	if _, err := io.Copy(gzipWriter, wavBuffer); err != nil {
		log.Printf("❌ Failed to compress WAV data: %v\n", err)
		return "", fmt.Errorf("failed to compress WAV data: %w", err)
	}
	if err := gzipWriter.Close(); err != nil {
		log.Printf("❌ Failed to finalize compression: %v\n", err)
		return "", fmt.Errorf("failed to finalize compression: %w", err)
	}

	// Create and execute the POST request
	soundscapeURL := fmt.Sprintf("https://app.birdweather.com/api/v1/stations/%s/soundscapes?timestamp=%s", b.BirdweatherID, timestamp)
	req, err := http.NewRequest("POST", soundscapeURL, &gzipWavData)
	if err != nil {
		log.Printf("❌ Failed to create POST request: %v\n", err)
		return "", fmt.Errorf("failed to create POST request: %w", err)
	}
	req.Header.Set("Content-Type", "application/octet-stream")
	req.Header.Set("Content-Encoding", "gzip")
	req.Header.Set("User-Agent", "BirdNET-Go")

	// Execute the request
	resp, err := b.HTTPClient.Do(req)
	if err != nil {
		log.Printf("❌ Request to upload soundscape failed: %v\n", err)
		return "", handleNetworkError(err)
	}
	if resp == nil {
		return "", fmt.Errorf("received nil response")
	}
	defer resp.Body.Close()

	// Process the response
	responseBody, err := io.ReadAll(resp.Body)
	if err != nil {
		log.Printf("❌ Failed to read response body: %v\n", err)
		return "", fmt.Errorf("failed to read response body: %w", err)
	}

	if b.Settings.Realtime.Birdweather.Debug {
		log.Println("📜 Response Body:", string(responseBody))
	}

	var sdata SoundscapeResponse
	if err := json.Unmarshal(responseBody, &sdata); err != nil {
		log.Printf("❌ Failed to decode JSON response: %v\n", err)
		return "", fmt.Errorf("failed to decode JSON response: %w", err)
	}

	if !sdata.Success {
		log.Println("❌ Upload was not successful according to the response")
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
		log.Println("📡 Posting detection to Birdweather: ", detectionURL)
	}

	// Fuzz location coordinates with user defined accuracy
	fuzzedLatitude, fuzzedLongitude := b.RandomizeLocation(b.Accuracy)

	// Convert timestamp to time.Time and calculate end time
	parsedTime, err := time.Parse("2006-01-02T15:04:05.000-0700", timestamp)
	if err != nil {
		log.Printf("❌ Failed to parse timestamp: %s, err: %v\n", timestamp, err)
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
		log.Printf("❌ Failed to marshal JSON data, err: %v\n", err)
		return fmt.Errorf("failed to marshal JSON data: %w", err)
	}

	if b.Settings.Realtime.Birdweather.Debug {
		log.Println("📜 JSON Payload:", string(postDataBytes))
	}

	// Execute POST request
	resp, err := b.HTTPClient.Post(detectionURL, "application/json", bytes.NewBuffer(postDataBytes))
	if err != nil {
		log.Printf("❌ Failed to post detection, err: %v\n", err)
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
			log.Printf("❌ Failed to read response body: %v\n", err)
			return fmt.Errorf("failed to read response body: %w", err)
		}
		log.Printf("⚠️ Failed to post detection, status code: %d, response body: %s\n", resp.StatusCode, string(responseBody))
		return fmt.Errorf("failed to post detection, status code: %d, response: %s", resp.StatusCode, string(responseBody))
	}

	return nil
}

// Upload function handles the uploading of detected clips and their details to Birdweather.
// It first parses the timestamp from the note, then uploads the soundscape, and finally posts the detection.
func (b *BwClient) Publish(note *datastore.Note, pcmData []byte) error {
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
		log.Printf("❌ Error parsing date: %v\n", err)
		return fmt.Errorf("error parsing date: %w", err)
	}

	// Format the parsed time to the required timestamp format with timezone information
	timestamp := parsedTime.Format("2006-01-02T15:04:05.000-0700")

	// If debug is enabled, save the raw PCM data to help diagnose issues
	if b.Settings.Realtime.Birdweather.Debug {
		debugDir := filepath.Join("debug", "birdweather", "pcm")
		debugFilename := filepath.Join(debugDir, fmt.Sprintf("bw_pcm_debug_%s.raw",
			parsedTime.Format("20060102_150405")))

		// Create directory if it doesn't exist
		if err := createDebugDirectory(debugDir); err != nil {
			log.Printf("⚠️ Warning: %v", err)
		} else {
			// Save raw PCM data
			if err := os.WriteFile(debugFilename, pcmData, 0o644); err != nil {
				log.Printf("⚠️ Warning: couldn't save debug PCM file: %v", err)
			} else {
				log.Printf("✅ Saved debug PCM file to %s", debugFilename)

				// Write metadata
				metaFilename := debugFilename + ".txt"
				metaInfo := "PCM Raw Data\n"
				metaInfo += fmt.Sprintf("File: %s\n", filepath.Base(debugFilename))
				metaInfo += fmt.Sprintf("Timestamp: %s\n", timestamp)
				metaInfo += fmt.Sprintf("Bird: %s (%s)\n", note.CommonName, note.ScientificName)
				metaInfo += fmt.Sprintf("Confidence: %.2f\n", note.Confidence)

				// Calculate proper audio duration - for 16-bit mono PCM at 48kHz:
				// Duration in seconds = bytes / (sample rate * bytes per sample * channels)
				pcmSize := len(pcmData)
				bytesPerSample := 2 // 16-bit = 2 bytes
				channels := 1       // Mono
				sampleRate := 48000 // 48kHz
				durationSec := float64(pcmSize) / float64(sampleRate*bytesPerSample*channels)

				metaInfo += fmt.Sprintf("Size: %d bytes\n", pcmSize)
				metaInfo += fmt.Sprintf("Expected Duration: %.3f seconds\n", durationSec)
				metaInfo += fmt.Sprintf("Sample Rate: %d Hz\n", sampleRate)
				metaInfo += fmt.Sprintf("Bits Per Sample: %d\n", bytesPerSample*8)
				metaInfo += fmt.Sprintf("Channels: %d\n", channels)

				// Save metadata
				if err := os.WriteFile(metaFilename, []byte(metaInfo), 0o644); err != nil {
					log.Printf("⚠️ Warning: couldn't save PCM metadata file: %v", err)
				}
			}
		}
	}

	// Upload the soundscape to Birdweather and retrieve the soundscape ID
	soundscapeID, err := b.UploadSoundscape(timestamp, pcmData)
	if err != nil {
		log.Printf("❌ Failed to upload soundscape to Birdweather: %v\n", err)
		return fmt.Errorf("failed to upload soundscape to Birdweather: %w", err)
	}

	// Log the successful posting of the soundscape, if debugging is enabled
	if b.Settings.Realtime.Birdweather.Debug {
		log.Println("✅ Soundscape successfully posted to Birdweather")
	}

	// Post the detection details to Birdweather using the retrieved soundscape ID
	err = b.PostDetection(soundscapeID, timestamp, note.CommonName, note.ScientificName, note.Confidence)
	if err != nil {
		log.Printf("❌ Failed to post detection to Birdweather: %v\n", err)
		return fmt.Errorf("failed to post detection to Birdweather: %w", err)
	}

	return nil
}

// Close properly cleans up the BwClient resources
// Currently this just cancels any pending HTTP requests
func (b *BwClient) Close() {
	if b.HTTPClient != nil && b.HTTPClient.Transport != nil {
		// If the transport implements the CloseIdleConnections method, call it
		type transporter interface {
			CloseIdleConnections()
		}
		if transport, ok := b.HTTPClient.Transport.(transporter); ok {
			transport.CloseIdleConnections()
		}

		// Cancel any in-flight requests by using a new client
		b.HTTPClient = nil
	}

	if b.Settings.Realtime.Birdweather.Debug {
		log.Println("✅ BirdWeather client closed")
	}
}

// createDebugDirectory creates a directory for debug files and returns any error encountered
func createDebugDirectory(path string) error {
	if err := os.MkdirAll(path, 0o755); err != nil {
		return fmt.Errorf("couldn't create debug directory: %w", err)
	}
	return nil
}
