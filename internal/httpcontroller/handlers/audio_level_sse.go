package handlers

import (
	"encoding/json"
	"fmt"
	"log"
	"net/http"
	"time"

	"github.com/labstack/echo/v4"
	"github.com/tphakala/birdnet-go/internal/myaudio"
)

// AudioLevelSSE handles Server-Sent Events for real-time audio level updates
func (h *Handlers) AudioLevelSSE(c echo.Context) error {
	if h.debug {
		log.Printf("AudioLevelSSE: New connection from %s", c.Request().RemoteAddr)
	}

	// Set headers for SSE
	c.Response().Header().Set(echo.HeaderContentType, "text/event-stream")
	c.Response().Header().Set(echo.HeaderCacheControl, "no-cache")
	c.Response().Header().Set(echo.HeaderConnection, "keep-alive")
	c.Response().WriteHeader(http.StatusOK)

	// Create a ticker for heartbeat
	heartbeat := time.NewTicker(30 * time.Second)
	defer heartbeat.Stop()

	// Initialize levels map
	levels := make(map[string]myaudio.AudioLevelData)
	lastLogTime := time.Now()

	for {
		select {
		case <-c.Request().Context().Done():
			if h.debug {
				log.Printf("AudioLevelSSE: Client disconnected: %s", c.Request().RemoteAddr)
			}
			return nil

		case audioData := <-h.AudioLevelChan:
			if h.debug {
				// print one message every 5 seconds
				if time.Since(lastLogTime) > 5*time.Second {
					log.Printf("AudioLevelSSE: Received audio data from source %s: %+v", audioData.Source, audioData)
					lastLogTime = time.Now()
				}
			}

			// Update the levels map with the new data
			levels[audioData.Source] = audioData

			// Prepare the message structure
			message := struct {
				Type   string                            `json:"type"`
				Levels map[string]myaudio.AudioLevelData `json:"levels"`
			}{
				Type:   "audio-level",
				Levels: levels,
			}

			// Marshal data to JSON
			jsonData, err := json.Marshal(message)
			if err != nil {
				log.Printf("AudioLevelSSE: Error marshaling JSON: %v", err)
				continue
			}

			if h.debug {
				// print one message every 5 seconds
				if time.Since(lastLogTime) > 5*time.Second {
					log.Printf("AudioLevelSSE: Sending data to client: %s", string(jsonData))
					lastLogTime = time.Now()
				}
			}

			// Write SSE formatted data
			if _, err := fmt.Fprintf(c.Response(), "data: %s\n\n", jsonData); err != nil {
				log.Printf("AudioLevelSSE: Error writing to client: %v", err)
				return err
			}

			// Flush the response writer buffer
			c.Response().Flush()

		case <-heartbeat.C:
			// Send heartbeat to keep connection alive
			if _, err := fmt.Fprintf(c.Response(), ":\n\n"); err != nil {
				if h.debug {
					log.Printf("AudioLevelSSE: Heartbeat error: %v", err)
				}
				return err
			}
			c.Response().Flush()
		}
	}
}
