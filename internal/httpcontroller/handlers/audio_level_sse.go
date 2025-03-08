package handlers

import (
	"encoding/json"
	"fmt"
	"log"
	"net/http"
	"sync"
	"time"

	"github.com/labstack/echo/v4"
	"github.com/tphakala/birdnet-go/internal/conf"
	"github.com/tphakala/birdnet-go/internal/myaudio"
)

// activeSSEConnections tracks active SSE connections per client IP
var (
	activeSSEConnections sync.Map
	connectionTimeout    = 65 * time.Second // slightly longer than client retry
)

// initializeSSEHeaders sets up the necessary headers for SSE connection
func initializeSSEHeaders(c echo.Context) {
	c.Response().Header().Set(echo.HeaderContentType, "text/event-stream")
	c.Response().Header().Set(echo.HeaderCacheControl, "no-cache")
	c.Response().Header().Set(echo.HeaderConnection, "keep-alive")
	c.Response().WriteHeader(http.StatusOK)
}

// initializeLevelsData creates and initializes the maps needed for tracking audio levels
func (h *Handlers) initializeLevelsData(isAuthenticated bool) (levels map[string]myaudio.AudioLevelData, lastUpdate, lastNonZero map[string]time.Time) {
	levels = make(map[string]myaudio.AudioLevelData)
	lastUpdate = make(map[string]time.Time)
	lastNonZero = make(map[string]time.Time)

	// Add configured audio device if set
	if h.Settings.Realtime.Audio.Source != "" {
		sourceName := h.Settings.Realtime.Audio.Source
		if !isAuthenticated {
			sourceName = "audio-source-1"
		}
		levels["malgo"] = myaudio.AudioLevelData{
			Level:  0,
			Name:   sourceName,
			Source: "malgo",
		}
		now := time.Now()
		lastUpdate["malgo"] = now
		lastNonZero["malgo"] = now
	}

	// Add all configured RTSP sources
	for i, url := range h.Settings.Realtime.RTSP.URLs {
		var displayName string
		if isAuthenticated {
			displayName = conf.SanitizeRTSPUrl(url)
		} else {
			displayName = fmt.Sprintf("camera-%d", i+1)
		}
		levels[url] = myaudio.AudioLevelData{
			Level:  0,
			Name:   displayName,
			Source: url,
		}
		now := time.Now()
		lastUpdate[url] = now
		lastNonZero[url] = now
	}

	return levels, lastUpdate, lastNonZero
}

// isSourceInactive checks if a source should be considered inactive based on its update times
func isSourceInactive(source string, now time.Time, lastUpdateTime, lastNonZeroTime map[string]time.Time, inactivityThreshold time.Duration) bool {
	lastUpdate, hasUpdate := lastUpdateTime[source]
	lastNonZero, hasNonZero := lastNonZeroTime[source]

	if !hasUpdate || !hasNonZero {
		return false // Consider new sources as active initially
	}

	noUpdateTimeout := now.Sub(lastUpdate) > inactivityThreshold
	noActivityTimeout := now.Sub(lastNonZero) > inactivityThreshold

	return noUpdateTimeout || noActivityTimeout
}

// updateAudioLevels processes new audio data and updates the levels map
func (h *Handlers) updateAudioLevels(audioData myaudio.AudioLevelData, levels map[string]myaudio.AudioLevelData,
	lastUpdateTime, lastNonZeroTime map[string]time.Time, isAuthenticated bool, inactivityThreshold time.Duration) {

	now := time.Now()

	if audioData.Source == "malgo" {
		if isAuthenticated {
			audioData.Name = h.Settings.Realtime.Audio.Source
		} else {
			audioData.Name = "audio-source-1"
		}
	} else {
		if isAuthenticated {
			audioData.Name = conf.SanitizeRTSPUrl(audioData.Source)
		} else {
			for i, url := range h.Settings.Realtime.RTSP.URLs {
				if url == audioData.Source {
					audioData.Name = fmt.Sprintf("camera-%d", i+1)
					break
				}
			}
		}
	}

	// Update activity times
	lastUpdateTime[audioData.Source] = now
	if audioData.Level > 0 {
		lastNonZeroTime[audioData.Source] = now
	}

	// Keep the current level unless the source is truly inactive
	if !isSourceInactive(audioData.Source, now, lastUpdateTime, lastNonZeroTime, inactivityThreshold) {
		levels[audioData.Source] = audioData
	} else {
		audioData.Level = 0
		levels[audioData.Source] = audioData
	}
}

// checkSourceActivity checks all sources for inactivity and updates their levels if needed
func checkSourceActivity(levels map[string]myaudio.AudioLevelData, lastUpdateTime, lastNonZeroTime map[string]time.Time,
	inactivityThreshold time.Duration) bool {

	now := time.Now()
	updated := false

	for source, data := range levels {
		if isSourceInactive(source, now, lastUpdateTime, lastNonZeroTime, inactivityThreshold) && data.Level != 0 {
			data.Level = 0
			levels[source] = data
			updated = true
		}
	}

	return updated
}

// AudioLevelSSE handles Server-Sent Events for audio level monitoring
// API: GET /api/v1/audio-level
func (h *Handlers) AudioLevelSSE(c echo.Context) error {
	clientIP := c.RealIP()

	// Check for existing connection
	if _, exists := activeSSEConnections.LoadOrStore(clientIP, time.Now()); exists {
		if h.debug {
			log.Printf("AudioLevelSSE: Rejected duplicate connection from %s", clientIP)
		}
		return c.NoContent(http.StatusTooManyRequests)
	}

	// Cleanup connection on exit
	defer func() {
		activeSSEConnections.Delete(clientIP)
		if h.debug {
			log.Printf("AudioLevelSSE: Cleaned up connection for %s", clientIP)
		}
	}()

	// Start connection timeout timer
	timeout := time.NewTimer(connectionTimeout)
	defer timeout.Stop()

	if h.debug {
		log.Printf("AudioLevelSSE: New connection from %s", clientIP)
	}

	// Set up SSE headers
	initializeSSEHeaders(c)

	// Create tickers for heartbeat and activity check
	heartbeat := time.NewTicker(10 * time.Second)
	defer heartbeat.Stop()
	activityCheck := time.NewTicker(1 * time.Second)
	defer activityCheck.Stop()

	// Initialize data structures
	const inactivityThreshold = 15 * time.Second
	levels, lastUpdateTime, lastNonZeroTime := h.initializeLevelsData(h.Server.IsAccessAllowed(c))
	lastLogTime := time.Now()
	lastSentTime := time.Now()

	// Send initial empty update to establish connection
	if err := sendLevelsUpdate(c, levels); err != nil {
		log.Printf("AudioLevelSSE: Error sending initial update: %v", err)
		return err
	}

	for {
		select {
		case <-timeout.C:
			if h.debug {
				log.Printf("AudioLevelSSE: Connection timeout for %s", clientIP)
			}
			return nil

		case <-c.Request().Context().Done():
			if h.debug {
				log.Printf("AudioLevelSSE: Client disconnected: %s", clientIP)
			}
			return nil

		case audioData := <-h.AudioLevelChan:
			if h.debug {
				if time.Since(lastLogTime) > 5*time.Second {
					log.Printf("AudioLevelSSE: Received audio data from source %s: %+v", audioData.Source, audioData)
					lastLogTime = time.Now()
				}
			}

			h.updateAudioLevels(audioData, levels, lastUpdateTime, lastNonZeroTime, h.Server.IsAccessAllowed(c), inactivityThreshold)

			// Only send updates if enough time has passed (rate limiting)
			if time.Since(lastSentTime) >= 50*time.Millisecond {
				if err := sendLevelsUpdate(c, levels); err != nil {
					log.Printf("AudioLevelSSE: Error sending update: %v", err)
					return err
				}
				lastSentTime = time.Now()
			}

		case <-activityCheck.C:
			if updated := checkSourceActivity(levels, lastUpdateTime, lastNonZeroTime, inactivityThreshold); updated {
				if err := sendLevelsUpdate(c, levels); err != nil {
					log.Printf("AudioLevelSSE: Error sending update: %v", err)
					return err
				}
			}

		case <-heartbeat.C:
			// Send a comment as heartbeat
			if _, err := fmt.Fprintf(c.Response(), ": heartbeat %d\n\n", time.Now().Unix()); err != nil {
				if h.debug {
					log.Printf("AudioLevelSSE: Heartbeat error: %v", err)
				}
				return err
			}
			c.Response().Flush()
		}
	}
}

// sendLevelsUpdate sends the current levels data to the client
func sendLevelsUpdate(c echo.Context, levels map[string]myaudio.AudioLevelData) error {
	message := struct {
		Type   string                            `json:"type"`
		Levels map[string]myaudio.AudioLevelData `json:"levels"`
	}{
		Type:   "audio-level",
		Levels: levels,
	}

	jsonData, err := json.Marshal(message)
	if err != nil {
		return fmt.Errorf("error marshaling JSON: %w", err)
	}

	if _, err := fmt.Fprintf(c.Response(), "data: %s\n\n", jsonData); err != nil {
		return fmt.Errorf("error writing to client: %w", err)
	}

	c.Response().Flush()
	return nil
}
