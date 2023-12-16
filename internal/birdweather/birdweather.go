package birdweather

import (
	"bytes"
	"compress/gzip"
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"time"
)

// BirdweatherClient holds the configuration for the Birdweather API client.
type BirdweatherClient struct {
	BirdweatherID string
	Latitude      float64
	Longitude     float64
}

// NewClient creates a new BirdweatherClient with the specified ID and location.
func NewClient(birdweatherID string, latitude, longitude float64) *BirdweatherClient {
	return &BirdweatherClient{
		BirdweatherID: birdweatherID,
		Latitude:      latitude,
		Longitude:     longitude,
	}
}

// UploadSoundscape uploads a soundscape file to the Birdweather server and returns the soundscape ID.
func (c *BirdweatherClient) UploadSoundscape(timestamp, filePath string) (soundscapeID string, err error) {
	// Create the URL for the POST request
	soundscapeURL := fmt.Sprintf("https://app.birdweather.com/api/v1/stations/%s/soundscapes?timestamp=%s", c.BirdweatherID, timestamp)

	// Read and compress the WAV file
	wavData, err := os.ReadFile(filePath)
	if err != nil {
		return "", err
	}

	var gzipWavData bytes.Buffer
	gzipWriter := gzip.NewWriter(&gzipWavData)
	if _, err := gzipWriter.Write(wavData); err != nil {
		return "", err
	}
	if err := gzipWriter.Close(); err != nil {
		return "", err
	}

	// Create and send the POST request
	req, err := http.NewRequest("POST", soundscapeURL, &gzipWavData)
	if err != nil {
		return "", err
	}
	req.Header.Set("Content-Type", "application/octet-stream")
	req.Header.Set("Content-Encoding", "gzip")

	client := &http.Client{}
	resp, err := client.Do(req)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()

	// Decode the JSON response
	if resp.StatusCode == http.StatusOK {
		var sdata map[string]map[string]string
		if err := json.NewDecoder(resp.Body).Decode(&sdata); err != nil {
			return "", err
		}
		fmt.Println("Successfully uploaded soundscape.")
		return sdata["soundscape"]["id"], nil
	}

	return "", fmt.Errorf("failed to upload soundscape, status code: %d", resp.StatusCode)
}

// PostDetection sends detection data to the Birdweather server.
func (c *BirdweatherClient) PostDetection(timestamp, soundscapeID string) error {
	detectionURL := fmt.Sprintf("https://app.birdweather.com/api/v1/stations/%s/detections", c.BirdweatherID)

	// Add 3 seconds to timestamp
	parsedTime, _ := time.Parse(time.RFC3339, timestamp)
	endTime := parsedTime.Add(3 * time.Second)

	// Prepare the data for the POST request.
	postData := map[string]interface{}{
		"timestamp":           timestamp,
		"lat":                 c.Latitude,
		"lon":                 c.Longitude,
		"soundscapeId":        soundscapeID,
		"soundscapeStartTime": timestamp,
		"soundscapeEndTime":   endTime,
	}

	// Set model type
	postData["algorithm"] = "2p4"

	postDataBytes, err := json.Marshal(postData)
	if err != nil {
		return err
	}

	// Send the POST request.
	resp, err := http.Post(detectionURL, "application/json", bytes.NewBuffer(postDataBytes))
	if err != nil {
		return err
	}
	defer resp.Body.Close()

	// Check for a successful response.
	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("failed to post detection, status code: %d", resp.StatusCode)
	} else {
		fmt.Println("Successfully posted detection.")
	}

	return nil
}
