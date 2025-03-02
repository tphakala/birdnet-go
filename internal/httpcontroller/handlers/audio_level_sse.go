package handlers

import (
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"sync"
	"time"

	"github.com/google/uuid"
	"github.com/labstack/echo/v4"
	"github.com/tphakala/birdnet-go/internal/logger"
	"github.com/tphakala/birdnet-go/internal/myaudio"
)

// activeSSEConnections tracks active SSE connections per client IP
var (
	activeSSEConnections sync.Map
	connectionTimeout    = 65 * time.Second // slightly longer than client retry
)

// getAudioConnectionLogger creates a connection-specific logger with the connection ID and client IP as fields
func (h *Handlers) getAudioConnectionLogger(connectionID, clientIP string) *logger.Logger {
	if h.Logger == nil {
		return nil
	}

	// Create a component-specific logger for audio levels with connection ID and client IP as permanent fields
	return h.Logger.Named("sse.audio.level").With(
		"connection_id", connectionID,
		"client_ip", clientIP,
	)
}

// getAudioSourceLogger creates a source-specific logger
func (h *Handlers) getAudioSourceLogger(connectionLogger *logger.Logger, source string) *logger.Logger {
	if connectionLogger == nil {
		return nil
	}

	// Create a source-specific logger as a child of the connection logger
	// This creates a full hierarchy like: sse.audio.source.malgo or sse.audio.source.rtsp
	var sourcePath string
	switch {
	case source == "malgo":
		sourcePath = "source.malgo"
	case strings.HasPrefix(source, "rtsp://"):
		sourcePath = "source.rtsp"
	default:
		sourcePath = "source.unknown"
	}

	return connectionLogger.Named(sourcePath)
}

// abs returns the absolute value of a float64
func abs(x float64) float64 {
	if x < 0 {
		return -x
	}
	return x
}

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
			displayName = cleanRTSPUrl(url)
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
	lastUpdateTime, lastNonZeroTime map[string]time.Time, isAuthenticated bool, inactivityThreshold time.Duration,
	connLogger *logger.Logger) {

	now := time.Now()

	// Get a source-specific logger if we have a connection logger
	var sourceLogger *logger.Logger
	if connLogger != nil {
		sourceLogger = h.getAudioSourceLogger(connLogger, audioData.Source)
	}

	// Update activity times
	lastUpdateTime[audioData.Source] = now

	// Check for new audio activity
	previousLevel := 0
	if prevData, exists := levels[audioData.Source]; exists {
		previousLevel = prevData.Level
	}

	significantChange := abs(float64(previousLevel-audioData.Level)) > 20

	// Set the name based on source and authentication status
	switch {
	case audioData.Source == "malgo" && isAuthenticated:
		audioData.Name = h.Settings.Realtime.Audio.Source
	case audioData.Source == "malgo" && !isAuthenticated:
		audioData.Name = "audio-source-1"
	case isAuthenticated:
		audioData.Name = cleanRTSPUrl(audioData.Source)
	default:
		// For RTSP sources when not authenticated
		for i, url := range h.Settings.Realtime.RTSP.URLs {
			if url == audioData.Source {
				audioData.Name = fmt.Sprintf("camera-%d", i+1)
				break
			}
		}
	}

	if audioData.Level > 0 {
		lastNonZeroTime[audioData.Source] = now

		// Log audio level activity for non-zero levels when source logger is available
		if sourceLogger != nil && audioData.Level > 30 && significantChange {
			sourceLogger.Debug("Audio activity detected",
				"level", audioData.Level,
				"previous_level", previousLevel,
				"name", audioData.Name)
		}
	}

	// Check if the source is inactive
	sourceInactive := isSourceInactive(audioData.Source, now, lastUpdateTime, lastNonZeroTime, inactivityThreshold)

	// Keep the current level unless the source is truly inactive
	if !sourceInactive {
		// Log significant changes
		if sourceLogger != nil && significantChange {
			direction := "decreased"
			if audioData.Level > previousLevel {
				direction = "increased"
			}

			sourceLogger.Debug("Significant level change",
				"previous", previousLevel,
				"current", audioData.Level,
				"direction", direction)
		}

		levels[audioData.Source] = audioData
	} else {
		if sourceLogger != nil && levels[audioData.Source].Level > 0 {
			sourceLogger.Debug("Source became inactive",
				"previous_level", levels[audioData.Source].Level,
				"inactive_time_s", int(now.Sub(lastNonZeroTime[audioData.Source]).Seconds()))
		}

		audioData.Level = 0
		levels[audioData.Source] = audioData
	}
}

// checkSourceActivity checks all sources for inactivity and updates their levels if needed
func checkSourceActivity(levels map[string]myaudio.AudioLevelData, lastUpdateTime, lastNonZeroTime map[string]time.Time,
	inactivityThreshold time.Duration, connLogger *logger.Logger) bool {

	now := time.Now()
	updated := false
	inactiveSources := 0

	for source, data := range levels {
		if !isSourceInactive(source, now, lastUpdateTime, lastNonZeroTime, inactivityThreshold) || data.Level == 0 {
			continue
		}

		data.Level = 0
		levels[source] = data
		updated = true
		inactiveSources++

		if connLogger != nil {
			sourceLogger := connLogger.Named("source." + source)
			sourceLogger.Debug("Auto-marked as inactive",
				"reason", "activity_timeout",
				"last_activity_s", int(now.Sub(lastNonZeroTime[source]).Seconds()))
		}
	}

	if updated && connLogger != nil {
		connLogger.Debug("Inactive sources detected",
			"inactive_count", inactiveSources,
			"total_sources", len(levels))
	}

	return updated
}

// AudioLevelSSE handles Server-Sent Events for audio level monitoring
// API: GET /api/v1/audio-level
func (h *Handlers) AudioLevelSSE(c echo.Context) error {
	clientIP := c.RealIP()
	connectionID := uuid.New().String()[:8] // Generate a unique ID for this connection

	// Get a connection-specific logger
	connLogger := h.getAudioConnectionLogger(connectionID, clientIP)

	// Check for existing connection
	if _, exists := activeSSEConnections.LoadOrStore(clientIP, time.Now()); exists {
		if connLogger != nil {
			connLogger.Warn("Rejected duplicate connection")
		} else {
			h.LogWarn("AudioLevelSSE: Rejected duplicate connection", "client_ip", clientIP)
		}
		return c.NoContent(http.StatusTooManyRequests)
	}

	// Log connection established
	if connLogger != nil {
		connLogger.Info("New connection established")
	} else {
		h.LogInfo("AudioLevelSSE: New connection", "client_ip", clientIP)
	}

	// Cleanup connection on exit
	defer func() {
		activeSSEConnections.Delete(clientIP)
		if connLogger != nil {
			connLogger.Info("Connection closed")
		} else {
			h.LogDebug("AudioLevelSSE: Cleaned up connection", "client_ip", clientIP)
		}
	}()

	// Start connection timeout timer
	timeout := time.NewTimer(connectionTimeout)
	defer timeout.Stop()

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
	startTime := time.Now()

	// Send initial empty update to establish connection
	if err := sendLevelsUpdate(c, levels); err != nil {
		if connLogger != nil {
			connLogger.Error("Error sending initial update", "error", err)
		} else {
			h.LogError("AudioLevelSSE: Error sending initial update", err)
		}
		return err
	}

	// Connection metrics
	messageCount := 0
	totalSources := len(levels)

	for {
		select {
		case <-timeout.C:
			if connLogger != nil {
				connLogger.Info("Connection timeout",
					"duration_ms", time.Since(startTime).Milliseconds(),
					"messages_sent", messageCount)
			} else {
				h.LogDebug("AudioLevelSSE: Connection timeout", "client_ip", clientIP)
			}
			return nil

		case <-c.Request().Context().Done():
			if connLogger != nil {
				connLogger.Info("Client disconnected",
					"duration_ms", time.Since(startTime).Milliseconds(),
					"messages_sent", messageCount)
			} else {
				h.LogDebug("AudioLevelSSE: Client disconnected", "client_ip", clientIP)
			}
			return nil

		case audioData := <-h.AudioLevelChan:
			// Only log details occasionally to avoid flooding logs
			if time.Since(lastLogTime) > 5*time.Second {
				if connLogger != nil {
					connLogger.Debug("Processing audio data",
						"source", audioData.Source,
						"level", audioData.Level,
						"name", audioData.Name)
				} else {
					h.LogDebug("AudioLevelSSE: Received audio data",
						"client_ip", clientIP,
						"source", audioData.Source,
						"level", audioData.Level,
						"name", audioData.Name)
				}
				lastLogTime = time.Now()
			}

			h.updateAudioLevels(audioData, levels, lastUpdateTime, lastNonZeroTime,
				h.Server.IsAccessAllowed(c), inactivityThreshold, connLogger)

			// Only send updates if enough time has passed (rate limiting)
			if time.Since(lastSentTime) >= 50*time.Millisecond {
				if err := sendLevelsUpdate(c, levels); err != nil {
					if connLogger != nil {
						connLogger.Error("Error sending update", "error", err)
					} else {
						h.LogError("AudioLevelSSE: Error sending update", err)
					}
					return err
				}
				messageCount++
				lastSentTime = time.Now()
			}

		case <-activityCheck.C:
			if updated := checkSourceActivity(levels, lastUpdateTime, lastNonZeroTime, inactivityThreshold, connLogger); updated {
				if err := sendLevelsUpdate(c, levels); err != nil {
					if connLogger != nil {
						connLogger.Error("Error sending update", "error", err)
					} else {
						h.LogError("AudioLevelSSE: Error sending update", err)
					}
					return err
				}
				messageCount++
			}

		case <-heartbeat.C:
			// Send a comment as heartbeat
			if _, err := fmt.Fprintf(c.Response(), ": heartbeat %d\n\n", time.Now().Unix()); err != nil {
				if connLogger != nil {
					connLogger.Error("Heartbeat error", "error", err)
				} else {
					h.LogError("AudioLevelSSE: Heartbeat error", err)
				}
				return err
			}

			// Log connection stats on heartbeat occasionally
			if connLogger != nil && time.Since(startTime) > 30*time.Second {
				connLogger.Debug("Connection stats",
					"duration_s", int(time.Since(startTime).Seconds()),
					"messages_sent", messageCount,
					"sources", totalSources,
					"msg_per_min", float64(messageCount)/(time.Since(startTime).Minutes()))
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

// cleanRTSPUrl removes sensitive information from RTSP URL and returns a display-friendly version
func cleanRTSPUrl(url string) string {
	// Find the @ symbol that separates credentials from host
	atIndex := -1
	for i := len("rtsp://"); i < len(url); i++ {
		if url[i] == '@' {
			atIndex = i
			break
		}
	}

	if atIndex > -1 {
		// Keep only rtsp:// and everything after @
		url = "rtsp://" + url[atIndex+1:]
	}

	// Find the first slash after the host:port
	slashIndex := -1
	for i := len("rtsp://"); i < len(url); i++ {
		if url[i] == '/' {
			slashIndex = i
			break
		}
	}

	if slashIndex > -1 {
		// Keep only up to the first slash
		url = url[:slashIndex]
	}

	return url
}
