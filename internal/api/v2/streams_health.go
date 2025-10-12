// internal/api/v2/streams_health.go
package api

import (
	"context"
	"log/slog"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/labstack/echo/v4"
	"github.com/labstack/echo/v4/middleware"
	"github.com/tphakala/birdnet-go/internal/myaudio"
	"github.com/tphakala/birdnet-go/internal/privacy"
	"golang.org/x/time/rate"
)

// Constants for stream health monitoring
const (
	// Stream health polling interval - how often to check for changes
	streamHealthPollInterval = 1 * time.Second

	// Rate limiting for stream health SSE endpoint
	streamHealthRateLimitRequests = 5                // Requests per window
	streamHealthRateLimitWindow   = 1 * time.Minute // Rate limit window
)

// SSEStreamHealthData represents stream health data sent via SSE
type SSEStreamHealthData struct {
	StreamHealthResponse
	EventType string `json:"event_type"` // e.g., "status_update", "state_change", "error_detected"
}

// StreamHealthResponse represents the API response for a single stream's health
type StreamHealthResponse struct {
	URL              string                       `json:"url"`               // Sanitized RTSP URL
	IsHealthy        bool                         `json:"is_healthy"`        // Overall health status
	ProcessState     string                       `json:"process_state"`     // Current process state (idle, starting, running, etc.)
	LastDataReceived *time.Time                   `json:"last_data_received"` // When data was last received (null if never)
	TimeSinceData    *float64                     `json:"time_since_data_seconds,omitempty"` // Seconds since last data (omitempty for never)
	RestartCount     int                          `json:"restart_count"`     // Number of times stream has been restarted
	Error            string                       `json:"error,omitempty"`   // Current error message if any
	// Data statistics
	TotalBytesReceived int64   `json:"total_bytes_received"` // Total bytes received
	BytesPerSecond     float64 `json:"bytes_per_second"`     // Current data rate
	IsReceivingData    bool    `json:"is_receiving_data"`    // Whether stream is actively receiving data
	// Error diagnostics (from PR #1380)
	LastErrorContext *ErrorContextResponse   `json:"last_error_context,omitempty"` // Most recent error with troubleshooting
	ErrorHistory     []*ErrorContextResponse `json:"error_history,omitempty"`      // Recent errors (last 10)
	// State history (for debugging state transitions)
	StateHistory []StateTransitionResponse `json:"state_history,omitempty"` // Recent state transitions
}

// ErrorContextResponse represents the API response for FFmpeg error context
type ErrorContextResponse struct {
	ErrorType          string    `json:"error_type"`                     // e.g., "connection_timeout", "rtsp_404"
	PrimaryMessage     string    `json:"primary_message"`                // Main error message
	UserFacingMessage  string    `json:"user_facing_msg"`                // User-friendly explanation
	TroubleshootingSteps []string `json:"troubleshooting_steps,omitempty"` // List of troubleshooting steps
	Timestamp          time.Time `json:"timestamp"`                      // When error was detected
	// Technical details (optional, for advanced users)
	TargetHost      string  `json:"target_host,omitempty"`       // Host/IP address
	TargetPort      int     `json:"target_port,omitempty"`       // Port number
	TimeoutDuration *string `json:"timeout_duration,omitempty"`  // Timeout duration as string (e.g., "10s")
	HTTPStatus      int     `json:"http_status,omitempty"`       // HTTP/RTSP status code
	RTSPMethod      string  `json:"rtsp_method,omitempty"`       // RTSP method that failed
	// Action recommendations
	ShouldOpenCircuit bool `json:"should_open_circuit"` // Whether circuit breaker should open
	ShouldRestart     bool `json:"should_restart"`      // Whether stream should restart
}

// StateTransitionResponse represents a state transition event
type StateTransitionResponse struct {
	FromState string    `json:"from_state"` // Previous state
	ToState   string    `json:"to_state"`   // New state
	Timestamp time.Time `json:"timestamp"`  // When transition occurred
	Reason    string    `json:"reason,omitempty"` // Reason for transition
}

// StreamsStatusSummaryResponse provides a high-level summary of all streams
type StreamsStatusSummaryResponse struct {
	TotalStreams    int                     `json:"total_streams"`    // Total number of configured streams
	HealthyStreams  int                     `json:"healthy_streams"`  // Number of healthy streams
	UnhealthyStreams int                    `json:"unhealthy_streams"` // Number of unhealthy streams
	StreamsSummary  []StreamSummaryResponse `json:"streams_summary"`  // Brief summary of each stream
	Timestamp       time.Time               `json:"timestamp"`        // When this status was generated
}

// StreamSummaryResponse provides a brief summary of a single stream
type StreamSummaryResponse struct {
	URL           string  `json:"url"`            // Sanitized RTSP URL
	IsHealthy     bool    `json:"is_healthy"`     // Health status
	ProcessState  string  `json:"process_state"`  // Current state
	LastErrorType string  `json:"last_error_type,omitempty"` // Type of last error if any
	TimeSinceData *float64 `json:"time_since_data_seconds,omitempty"` // Seconds since last data
}

// initStreamHealthRoutes registers all stream health monitoring endpoints
func (c *Controller) initStreamHealthRoutes() {
	// All health endpoints require authentication as they may contain sensitive data
	authMiddleware := c.getEffectiveAuthMiddleware()

	// REST endpoints
	c.Group.GET("/streams/health", c.GetAllStreamsHealth, authMiddleware)
	c.Group.GET("/streams/health/:url", c.GetStreamHealth, authMiddleware)
	c.Group.GET("/streams/status", c.GetStreamsStatusSummary, authMiddleware)

	// SSE endpoint for real-time stream health updates with rate limiting
	// Configure for 5 connections per minute (5/60 = 0.0833 requests per second)
	rateLimiterConfig := middleware.RateLimiterConfig{
		Store: middleware.NewRateLimiterMemoryStoreWithConfig(
			middleware.RateLimiterMemoryStoreConfig{
				Rate:      rate.Limit(float64(streamHealthRateLimitRequests) / 60.0), // 5 per 60 seconds
				Burst:     1,                                                          // Allow 1 immediate connection
				ExpiresIn: streamHealthRateLimitWindow,
			},
		),
		IdentifierExtractor: middleware.DefaultRateLimiterConfig.IdentifierExtractor,
		ErrorHandler: func(context echo.Context, err error) error {
			return context.JSON(http.StatusTooManyRequests, map[string]string{
				"error": "Rate limit exceeded for stream health SSE connections",
			})
		},
		DenyHandler: func(context echo.Context, identifier string, err error) error {
			return context.JSON(http.StatusTooManyRequests, map[string]string{
				"error": "Too many stream health SSE connection attempts, please wait before trying again",
			})
		},
	}

	c.Group.GET("/streams/health/stream", c.StreamHealthUpdates,
		authMiddleware,
		middleware.RateLimiterWithConfig(rateLimiterConfig))
}

// GetAllStreamsHealth returns health information for all configured RTSP streams
// @Summary Get health status of all RTSP streams
// @Description Returns detailed health information for all configured RTSP streams including error diagnostics
// @Tags streams
// @Produce json
// @Success 200 {array} StreamHealthResponse "Array of stream health information"
// @Failure 500 {object} ErrorResponse "Internal server error"
// @Router /api/v2/streams/health [get]
func (c *Controller) GetAllStreamsHealth(ctx echo.Context) error {
	// Get health data from the FFmpeg manager
	healthData := myaudio.GetRTSPStreamHealth()

	// Convert to API response format
	// Use a slice instead of map to avoid collisions when multiple URLs
	// have the same sanitized form (differ only by credentials)
	response := make([]StreamHealthResponse, 0, len(healthData))
	for rawURL := range healthData {
		health := healthData[rawURL]
		response = append(response, convertStreamHealthToResponse(rawURL, &health))
	}

	return ctx.JSON(http.StatusOK, response)
}

// GetStreamHealth returns health information for a specific RTSP stream
// @Summary Get health status of a specific RTSP stream
// @Description Returns detailed health information for a specific RTSP stream by URL
// @Tags streams
// @Produce json
// @Param url path string true "URL-encoded RTSP stream URL"
// @Success 200 {object} StreamHealthResponse "Stream health information"
// @Failure 400 {object} ErrorResponse "Invalid or missing URL parameter"
// @Failure 404 {object} ErrorResponse "Stream not found"
// @Failure 500 {object} ErrorResponse "Internal server error"
// @Router /api/v2/streams/health/{url} [get]
func (c *Controller) GetStreamHealth(ctx echo.Context) error {
	// Get URL parameter (URL-encoded)
	encodedURL := ctx.Param("url")
	if encodedURL == "" {
		return c.HandleError(ctx, nil, "URL parameter is required", http.StatusBadRequest)
	}

	// Decode the URL
	decodedURL, err := url.QueryUnescape(encodedURL)
	if err != nil {
		return c.HandleError(ctx, err, "Invalid URL encoding", http.StatusBadRequest)
	}

	// Get health data from the FFmpeg manager
	healthData := myaudio.GetRTSPStreamHealth()

	// Find the matching stream (case-sensitive exact match)
	health, exists := healthData[decodedURL]
	if !exists {
		c.logAPIRequest(ctx, slog.LevelWarn, "Stream not found",
			"requested_url", privacy.SanitizeRTSPUrl(decodedURL),
			"active_streams", len(healthData))
		return c.HandleError(ctx, nil, "Stream not found", http.StatusNotFound)
	}

	// Convert to API response format
	response := convertStreamHealthToResponse(decodedURL, &health)

	return ctx.JSON(http.StatusOK, response)
}

// GetStreamsStatusSummary returns a high-level summary of all stream statuses
// @Summary Get summary of all stream statuses
// @Description Returns a high-level summary including counts of healthy/unhealthy streams
// @Tags streams
// @Produce json
// @Success 200 {object} StreamsStatusSummaryResponse "Streams status summary"
// @Failure 500 {object} ErrorResponse "Internal server error"
// @Router /api/v2/streams/status [get]
func (c *Controller) GetStreamsStatusSummary(ctx echo.Context) error {
	// Get health data from the FFmpeg manager
	healthData := myaudio.GetRTSPStreamHealth()

	// Build summary
	summary := StreamsStatusSummaryResponse{
		TotalStreams:    len(healthData),
		HealthyStreams:  0,
		UnhealthyStreams: 0,
		StreamsSummary:  make([]StreamSummaryResponse, 0, len(healthData)),
		Timestamp:       time.Now(),
	}

	for rawURL := range healthData {
		health := healthData[rawURL]
		// Count healthy/unhealthy
		if health.IsHealthy {
			summary.HealthyStreams++
		} else {
			summary.UnhealthyStreams++
		}

		// Build brief summary for this stream
		streamSummary := StreamSummaryResponse{
			URL:          privacy.SanitizeRTSPUrl(rawURL),
			IsHealthy:    health.IsHealthy,
			ProcessState: health.ProcessState.String(),
		}

		// Add time since data if available
		if !health.LastDataReceived.IsZero() {
			timeSince := time.Since(health.LastDataReceived).Seconds()
			streamSummary.TimeSinceData = &timeSince
		}

		// Add last error type if available
		if health.LastErrorContext != nil {
			streamSummary.LastErrorType = health.LastErrorContext.ErrorType
		}

		summary.StreamsSummary = append(summary.StreamsSummary, streamSummary)
	}

	return ctx.JSON(http.StatusOK, summary)
}

// convertStreamHealthToResponse converts internal StreamHealth to API response format
func convertStreamHealthToResponse(rawURL string, health *myaudio.StreamHealth) StreamHealthResponse {
	response := StreamHealthResponse{
		URL:                privacy.SanitizeRTSPUrl(rawURL),
		IsHealthy:          health.IsHealthy,
		ProcessState:       health.ProcessState.String(),
		RestartCount:       health.RestartCount,
		TotalBytesReceived: health.TotalBytesReceived,
		BytesPerSecond:     health.BytesPerSecond,
		IsReceivingData:    health.IsReceivingData,
	}

	// Handle LastDataReceived (may be zero time if never received data)
	if !health.LastDataReceived.IsZero() {
		response.LastDataReceived = &health.LastDataReceived
		timeSince := time.Since(health.LastDataReceived).Seconds()
		response.TimeSinceData = &timeSince
	}

	// Handle error message
	if health.Error != nil {
		response.Error = health.Error.Error()
	}

	// Convert last error context
	if health.LastErrorContext != nil {
		response.LastErrorContext = convertErrorContextToResponse(health.LastErrorContext)
	}

	// Convert error history
	if len(health.ErrorHistory) > 0 {
		response.ErrorHistory = make([]*ErrorContextResponse, 0, len(health.ErrorHistory))
		for _, errCtx := range health.ErrorHistory {
			if errCtx != nil {
				response.ErrorHistory = append(response.ErrorHistory, convertErrorContextToResponse(errCtx))
			}
		}
	}

	// Convert state history
	if len(health.StateHistory) > 0 {
		response.StateHistory = make([]StateTransitionResponse, 0, len(health.StateHistory))
		for _, st := range health.StateHistory {
			response.StateHistory = append(response.StateHistory, StateTransitionResponse{
				FromState: st.From.String(),
				ToState:   st.To.String(),
				Timestamp: st.Timestamp,
				Reason:    st.Reason,
			})
		}
	}

	return response
}

// convertErrorContextToResponse converts internal ErrorContext to API response format
func convertErrorContextToResponse(errCtx *myaudio.ErrorContext) *ErrorContextResponse {
	if errCtx == nil {
		return nil
	}

	response := &ErrorContextResponse{
		ErrorType:            errCtx.ErrorType,
		PrimaryMessage:       errCtx.PrimaryMessage,
		UserFacingMessage:    errCtx.UserFacingMsg,
		TroubleshootingSteps: errCtx.TroubleShooting,
		Timestamp:            errCtx.Timestamp,
		ShouldOpenCircuit:    errCtx.ShouldOpenCircuit(),
		ShouldRestart:        errCtx.ShouldRestart(),
	}

	// Add technical details if available
	if errCtx.TargetHost != "" {
		response.TargetHost = errCtx.TargetHost
	}
	if errCtx.TargetPort > 0 {
		response.TargetPort = errCtx.TargetPort
	}
	if errCtx.TimeoutDuration > 0 {
		timeout := errCtx.TimeoutDuration.String()
		response.TimeoutDuration = &timeout
	}
	if errCtx.HTTPStatus > 0 {
		response.HTTPStatus = errCtx.HTTPStatus
	}
	if errCtx.RTSPMethod != "" {
		response.RTSPMethod = strings.ToUpper(errCtx.RTSPMethod)
	}

	return response
}

// StreamHealthUpdates streams real-time RTSP stream health updates via SSE
// @Summary Stream real-time RTSP stream health updates
// @Description Establishes an SSE connection to receive real-time updates when stream health changes
// @Tags streams
// @Produce text/event-stream
// @Success 200 {object} SSEStreamHealthData "Stream health update events"
// @Failure 401 {object} ErrorResponse "Unauthorized"
// @Failure 429 {object} ErrorResponse "Too many requests"
// @Router /api/v2/streams/health/stream [get]
func (c *Controller) StreamHealthUpdates(ctx echo.Context) error {
	// Create a context with timeout for maximum connection duration
	timeoutCtx, cancel := context.WithTimeout(ctx.Request().Context(), maxSSEStreamDuration)
	defer cancel()

	// Override the request context with timeout context
	originalReq := ctx.Request()
	ctx.SetRequest(originalReq.WithContext(timeoutCtx))

	// Set SSE headers
	setSSEHeaders(ctx)

	// Generate client ID for logging
	clientID := generateCorrelationID()

	// Log the connection
	c.logSSEConnection(clientID, ctx.RealIP(), ctx.Request().UserAgent(), "stream-health", true)
	defer c.logSSEConnection(clientID, ctx.RealIP(), "", "stream-health", false)

	// Send initial connection message
	if err := c.sendConnectionMessage(ctx, clientID, "Connected to stream health updates", "stream_health"); err != nil {
		return err
	}

	// Keep track of previous state to detect changes
	previousState := make(map[string]streamHealthSnapshot)

	// Setup ticker for polling health data
	ticker := time.NewTicker(streamHealthPollInterval)
	defer ticker.Stop()

	// Setup heartbeat ticker
	heartbeatTicker := time.NewTicker(sseHeartbeatInterval)
	defer heartbeatTicker.Stop()

	for {
		select {
		case <-heartbeatTicker.C:
			// Send heartbeat to keep connection alive
			if err := c.sendSSEHeartbeat(ctx, clientID, "stream_health"); err != nil {
				if c.apiLogger != nil {
					c.apiLogger.Debug("Stream health SSE heartbeat failed, client likely disconnected",
						"client_id", clientID,
						"error", err.Error())
				}
				return err
			}

		case <-ticker.C:
			// Poll for stream health changes
			healthData := myaudio.GetRTSPStreamHealth()

			// Process stream updates
			if err := c.processStreamHealthUpdates(ctx, clientID, healthData, previousState); err != nil {
				return err
			}

			// Check for removed streams
			if err := c.processRemovedStreams(ctx, clientID, healthData, previousState); err != nil {
				return err
			}

		case <-ctx.Request().Context().Done():
			// Client disconnected or timeout reached
			return nil
		}
	}
}

// streamHealthSnapshot captures key health metrics for change detection
type streamHealthSnapshot struct {
	IsHealthy        bool
	ProcessState     string
	LastErrorType    string
	RestartCount     int
	IsReceivingData  bool
	TotalBytesReceived int64
}

// createHealthSnapshot creates a snapshot of stream health for comparison
func createHealthSnapshot(health *myaudio.StreamHealth) streamHealthSnapshot {
	snapshot := streamHealthSnapshot{
		IsHealthy:        health.IsHealthy,
		ProcessState:     health.ProcessState.String(),
		RestartCount:     health.RestartCount,
		IsReceivingData:  health.IsReceivingData,
		TotalBytesReceived: health.TotalBytesReceived,
	}

	if health.LastErrorContext != nil {
		snapshot.LastErrorType = health.LastErrorContext.ErrorType
	}

	return snapshot
}

// hasHealthChanged checks if stream health has changed significantly
func hasHealthChanged(prev, current streamHealthSnapshot) bool {
	return prev.IsHealthy != current.IsHealthy ||
		prev.ProcessState != current.ProcessState ||
		prev.LastErrorType != current.LastErrorType ||
		prev.RestartCount != current.RestartCount ||
		prev.IsReceivingData != current.IsReceivingData
}

// determineEventType determines the appropriate event type based on what changed
func determineEventType(prev, current streamHealthSnapshot) string {
	// Prioritize event types by importance
	if prev.ProcessState != current.ProcessState {
		return "state_change"
	}
	if prev.IsHealthy != current.IsHealthy {
		if current.IsHealthy {
			return "health_recovered"
		}
		return "health_degraded"
	}
	if prev.LastErrorType != current.LastErrorType && current.LastErrorType != "" {
		return "error_detected"
	}
	if prev.RestartCount != current.RestartCount {
		return "stream_restarted"
	}
	if prev.IsReceivingData != current.IsReceivingData {
		if current.IsReceivingData {
			return "data_flow_resumed"
		}
		return "data_flow_stopped"
	}
	return "status_update"
}

// processStreamHealthUpdates processes health updates for all active streams
func (c *Controller) processStreamHealthUpdates(ctx echo.Context, clientID string, healthData map[string]myaudio.StreamHealth, previousState map[string]streamHealthSnapshot) error {
	for rawURL := range healthData {
		health := healthData[rawURL]
		currentSnapshot := createHealthSnapshot(&health)

		// Check if this is a new stream or if something changed
		previousSnapshot, exists := previousState[rawURL]
		if !exists {
			// New stream detected
			if err := c.sendStreamHealthUpdate(ctx, rawURL, &health, "stream_added"); err != nil {
				if c.apiLogger != nil {
					c.apiLogger.Debug("Failed to send stream_added event, client disconnected",
						"url", privacy.SanitizeRTSPUrl(rawURL),
						"client_id", clientID,
						"error", err.Error())
				}
				return err
			}
		} else if hasHealthChanged(previousSnapshot, currentSnapshot) {
			// Stream health changed
			eventType := determineEventType(previousSnapshot, currentSnapshot)
			if err := c.sendStreamHealthUpdate(ctx, rawURL, &health, eventType); err != nil {
				if c.apiLogger != nil {
					c.apiLogger.Debug("Failed to send health update, client disconnected",
						"url", privacy.SanitizeRTSPUrl(rawURL),
						"event_type", eventType,
						"client_id", clientID,
						"previous_state", previousSnapshot.ProcessState,
						"current_state", currentSnapshot.ProcessState,
						"error", err.Error())
				}
				return err
			}
		}

		// Update previous state
		previousState[rawURL] = currentSnapshot
	}

	return nil
}

// processRemovedStreams checks for and processes streams that have been removed
func (c *Controller) processRemovedStreams(ctx echo.Context, clientID string, healthData map[string]myaudio.StreamHealth, previousState map[string]streamHealthSnapshot) error {
	for prevURL := range previousState {
		if _, exists := healthData[prevURL]; exists {
			continue
		}

		// Stream was removed
		sanitizedURL := privacy.SanitizeRTSPUrl(prevURL)
		emptyHealth := myaudio.StreamHealth{}
		response := convertStreamHealthToResponse(prevURL, &emptyHealth)
		event := SSEStreamHealthData{
			StreamHealthResponse: response,
			EventType:            "stream_removed",
		}

		if err := c.sendSSEMessage(ctx, "stream_health", event); err != nil {
			return err
		}

		delete(previousState, prevURL)

		if c.apiLogger != nil {
			c.apiLogger.Info("Stream removed",
				"url", sanitizedURL,
				"client_id", clientID)
		}
	}

	return nil
}

// sendStreamHealthUpdate sends a stream health update via SSE
func (c *Controller) sendStreamHealthUpdate(ctx echo.Context, rawURL string, health *myaudio.StreamHealth, eventType string) error {
	response := convertStreamHealthToResponse(rawURL, health)
	event := SSEStreamHealthData{
		StreamHealthResponse: response,
		EventType:            eventType,
	}

	if err := c.sendSSEMessage(ctx, "stream_health", event); err != nil {
		return err
	}

	if c.apiLogger != nil {
		c.apiLogger.Debug("Stream health update sent",
			"url", privacy.SanitizeRTSPUrl(rawURL),
			"event_type", eventType,
			"is_healthy", health.IsHealthy,
			"state", health.ProcessState.String())
	}

	return nil
}
