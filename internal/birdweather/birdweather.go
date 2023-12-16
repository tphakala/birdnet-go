package birdweather

import (
	"bytes"
	"compress/gzip"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"net/http"
	"os"
	"time"

	"github.com/tphakala/birdnet-go/internal/config"
)

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

// Ensure that BirdweatherClient implements the BirdweatherClientInterface
var _ config.BirdweatherClientInterface = (*BirdweatherClient)(nil)

// BirdweatherClient holds the configuration for the Birdweather API client.
type BirdweatherClient struct {
	BirdweatherID string
	Latitude      float64
	Longitude     float64
	HTTPClient    *http.Client
}

// NewClient creates a new BirdweatherClient with the specified ID and location.
func NewClient(birdweatherID string, latitude, longitude float64) *BirdweatherClient {
	return &BirdweatherClient{
		BirdweatherID: birdweatherID,
		Latitude:      latitude,
		Longitude:     longitude,
		// set HTTP request timeout to 3 sec
		HTTPClient: &http.Client{Timeout: 3 * time.Second},
	}
}

// UploadSoundscape uploads a soundscape file to the Birdweather API and returns the soundscape ID
func (c *BirdweatherClient) UploadSoundscape(ctx *config.Context, timestamp, filePath string) (soundscapeID string, err error) {
	soundscapeURL := fmt.Sprintf("https://app.birdweather.com/api/v1/stations/%s/soundscapes?timestamp=%s", c.BirdweatherID, timestamp)

	if ctx.Settings.Realtime.Birdweather.Debug {
		log.Println("Uploading soundscape to:", soundscapeURL)
	}

	wavData, err := os.ReadFile(filePath)
	if err != nil {
		log.Printf("Failed to read WAV file: %s, err: %v\n", filePath, err)
		return "", err
	}

	var gzipWavData bytes.Buffer
	gzipWriter := gzip.NewWriter(&gzipWavData)
	defer gzipWriter.Close() // ensure closure

	if _, err := gzipWriter.Write(wavData); err != nil {
		log.Printf("Failed to compress WAV file: %s, err: %v\n", filePath, err)
		return "", err
	}
	if err := gzipWriter.Close(); err != nil {
		log.Printf("Failed to finalize compression for WAV file: %s, err: %v\n", filePath, err)
		return "", err
	}

	req, err := http.NewRequest("POST", soundscapeURL, &gzipWavData)
	if err != nil {
		log.Printf("Failed to create POST request for soundscape: %s, err: %v\n", filePath, err)
		return "", err
	}
	req.Header.Set("Content-Type", "application/octet-stream")
	req.Header.Set("Content-Encoding", "gzip")

	resp, err := c.HTTPClient.Do(req)
	if err != nil {
		log.Printf("Request to upload soundscape %s failed: %v\n", filePath, err)
		return "", err
	}
	defer resp.Body.Close()

	responseBody, err := io.ReadAll(resp.Body)
	if err != nil {
		log.Printf("Failed to read response body: %v\n", err)
		return "", err
	}

	if ctx.Settings.Realtime.Birdweather.Debug {
		log.Println("Response Body:", string(responseBody))
	}

	var sdata SoundscapeResponse
	if err := json.Unmarshal(responseBody, &sdata); err != nil {
		log.Printf("Failed to decode JSON response: %v\n", err)
		return "", err
	}

	if !sdata.Success {
		log.Println("Upload was not successful according to the response")
		return "", fmt.Errorf("upload failed, response reported failure")
	}

	return fmt.Sprintf("%d", sdata.Soundscape.ID), nil
}

// PostDetection posts a detection to the Birdweather API matching the specified soundscape ID
func (c *BirdweatherClient) PostDetection(ctx *config.Context, soundscapeID, timestamp, commonName, scientificName string, confidence float64) error {
	detectionURL := fmt.Sprintf("https://app.birdweather.com/api/v1/stations/%s/detections", c.BirdweatherID)

	if ctx.Settings.Realtime.Birdweather.Debug {
		log.Println("Posting detection to Birdweather: ", detectionURL)
	}

	// convert timestamp string to time object and add 3 seconds to get end time
	// FIXME this is a kludge and end time should be lenght of soundscape
	timestampFormat := "2006-01-02T15:04:05.000-0700"
	parsedTime, err := time.Parse(timestampFormat, timestamp)
	if err != nil {
		log.Printf("Failed to parse timestamp: %s, err: %v\n", timestamp, err)
		return err
	}
	endTime := parsedTime.Add(3 * time.Second).Format(timestampFormat)

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
		Latitude:            c.Latitude,
		Longitude:           c.Longitude,
		SoundscapeID:        soundscapeID,
		SoundscapeStartTime: timestamp,
		SoundscapeEndTime:   endTime,
		CommonName:          commonName,
		ScientificName:      scientificName,
		Algorithm:           "2p4",
		Confidence:          fmt.Sprintf("%.2f", confidence),
	}

	postDataBytes, err := json.Marshal(postData)
	if err != nil {
		log.Printf("Failed to marshal JSON data, err: %v\n", err)
		return err
	}

	if ctx.Settings.Realtime.Birdweather.Debug {
		log.Println("JSON Payload:", string(postDataBytes))
	}

	resp, err := c.HTTPClient.Post(detectionURL, "application/json", bytes.NewBuffer(postDataBytes))
	if err != nil {
		log.Printf("Failed to post detection, err: %v\n", err)
		return err
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusCreated {
		responseBody, err := io.ReadAll(resp.Body)
		if err != nil {
			log.Printf("Failed to read response body: %v\n", err)
			return err
		}
		log.Printf("Failed to post detection, status code: %d, response body: %s\n", resp.StatusCode, string(responseBody))
		return fmt.Errorf("failed to post detection, status code: %d, response: %s", resp.StatusCode, string(responseBody))
	}

	return nil
}
