// internal/api/v2/streams_health.go
package api

import (
	"log/slog"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/labstack/echo/v4"
	"github.com/tphakala/birdnet-go/internal/myaudio"
	"github.com/tphakala/birdnet-go/internal/privacy"
)

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
	// All health endpoints are public (no auth) for ease of monitoring integration
	// If you need authentication, add c.getEffectiveAuthMiddleware() as the third parameter

	c.Group.GET("/streams/health", c.GetAllStreamsHealth)
	c.Group.GET("/streams/health/:url", c.GetStreamHealth)
	c.Group.GET("/streams/status", c.GetStreamsStatusSummary)
}

// GetAllStreamsHealth returns health information for all configured RTSP streams
// @Summary Get health status of all RTSP streams
// @Description Returns detailed health information for all configured RTSP streams including error diagnostics
// @Tags streams
// @Produce json
// @Success 200 {object} map[string]StreamHealthResponse "Map of URL to stream health"
// @Failure 500 {object} ErrorResponse "Internal server error"
// @Router /api/v2/streams/health [get]
func (c *Controller) GetAllStreamsHealth(ctx echo.Context) error {
	// Get health data from the FFmpeg manager
	healthData := myaudio.GetRTSPStreamHealth()

	// Convert to API response format
	response := make(map[string]StreamHealthResponse)
	for rawURL, health := range healthData {
		// Sanitize the URL for the response
		sanitizedURL := privacy.SanitizeRTSPUrl(rawURL)
		response[sanitizedURL] = convertStreamHealthToResponse(rawURL, health)
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
	var foundHealth *myaudio.StreamHealth
	var foundURL string
	for rawURL, health := range healthData {
		if rawURL == decodedURL {
			h := health // Create a copy to avoid pointer issues
			foundHealth = &h
			foundURL = rawURL
			break
		}
	}

	if foundHealth == nil {
		c.logAPIRequest(ctx, slog.LevelWarn, "Stream not found",
			"requested_url", privacy.SanitizeRTSPUrl(decodedURL),
			"active_streams", len(healthData))
		return c.HandleError(ctx, nil, "Stream not found", http.StatusNotFound)
	}

	// Convert to API response format
	response := convertStreamHealthToResponse(foundURL, *foundHealth)

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

	for rawURL, health := range healthData {
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
func convertStreamHealthToResponse(rawURL string, health myaudio.StreamHealth) StreamHealthResponse {
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
