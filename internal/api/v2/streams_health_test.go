// internal/api/v2/streams_health_test.go
package api

import (
	"errors"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/tphakala/birdnet-go/internal/myaudio"
)

// TestCreateHealthSnapshot tests the createHealthSnapshot function
func TestCreateHealthSnapshot(t *testing.T) {
	t.Run("healthy stream with all fields populated", func(t *testing.T) {
		health := &myaudio.StreamHealth{
			IsHealthy:          true,
			ProcessState:       myaudio.StateRunning,
			RestartCount:       0,
			IsReceivingData:    true,
			TotalBytesReceived: 1024,
			LastErrorContext:   nil,
		}

		snapshot := createHealthSnapshot(health)

		assert.True(t, snapshot.IsHealthy)
		assert.Equal(t, "running", snapshot.ProcessState)
		assert.Equal(t, 0, snapshot.RestartCount)
		assert.True(t, snapshot.IsReceivingData)
		assert.Equal(t, int64(1024), snapshot.TotalBytesReceived)
		assert.Empty(t, snapshot.LastErrorType)
	})

	t.Run("unhealthy stream with error context", func(t *testing.T) {
		health := &myaudio.StreamHealth{
			IsHealthy:          false,
			ProcessState:       myaudio.StateCircuitOpen,
			RestartCount:       5,
			IsReceivingData:    false,
			TotalBytesReceived: 0,
			LastErrorContext: &myaudio.ErrorContext{
				ErrorType:      "rtsp_404",
				PrimaryMessage: "method DESCRIBE failed: 404 Not Found",
			},
		}

		snapshot := createHealthSnapshot(health)

		assert.False(t, snapshot.IsHealthy)
		assert.Equal(t, "circuit_open", snapshot.ProcessState)
		assert.Equal(t, 5, snapshot.RestartCount)
		assert.False(t, snapshot.IsReceivingData)
		assert.Equal(t, int64(0), snapshot.TotalBytesReceived)
		assert.Equal(t, "rtsp_404", snapshot.LastErrorType)
	})

	t.Run("stream with nil error context", func(t *testing.T) {
		health := &myaudio.StreamHealth{
			IsHealthy:          true,
			ProcessState:       myaudio.StateRunning,
			RestartCount:       0,
			IsReceivingData:    true,
			TotalBytesReceived: 2048,
			LastErrorContext:   nil,
		}

		snapshot := createHealthSnapshot(health)

		assert.Empty(t, snapshot.LastErrorType)
		assert.True(t, snapshot.IsHealthy)
	})

	t.Run("stream in backoff state", func(t *testing.T) {
		health := &myaudio.StreamHealth{
			IsHealthy:          false,
			ProcessState:       myaudio.StateBackoff,
			RestartCount:       3,
			IsReceivingData:    false,
			TotalBytesReceived: 512,
			LastErrorContext: &myaudio.ErrorContext{
				ErrorType: "connection_timeout",
			},
		}

		snapshot := createHealthSnapshot(health)

		assert.Equal(t, "backoff", snapshot.ProcessState)
		assert.Equal(t, 3, snapshot.RestartCount)
		assert.Equal(t, "connection_timeout", snapshot.LastErrorType)
	})
}

// TestHasHealthChanged tests the hasHealthChanged function
func TestHasHealthChanged(t *testing.T) {
	t.Run("no changes detected", func(t *testing.T) {
		prev := streamHealthSnapshot{
			IsHealthy:          true,
			ProcessState:       "running",
			LastErrorType:      "",
			RestartCount:       0,
			IsReceivingData:    true,
			TotalBytesReceived: 1024,
		}
		current := prev

		assert.False(t, hasHealthChanged(prev, current))
	})

	t.Run("health status changed", func(t *testing.T) {
		prev := streamHealthSnapshot{
			IsHealthy:          true,
			ProcessState:       "running",
			LastErrorType:      "",
			RestartCount:       0,
			IsReceivingData:    true,
			TotalBytesReceived: 1024,
		}
		current := prev
		current.IsHealthy = false

		assert.True(t, hasHealthChanged(prev, current))
	})

	t.Run("process state changed", func(t *testing.T) {
		prev := streamHealthSnapshot{
			IsHealthy:          true,
			ProcessState:       "running",
			LastErrorType:      "",
			RestartCount:       0,
			IsReceivingData:    true,
			TotalBytesReceived: 1024,
		}
		current := prev
		current.ProcessState = "circuit_open"

		assert.True(t, hasHealthChanged(prev, current))
	})

	t.Run("error type changed", func(t *testing.T) {
		prev := streamHealthSnapshot{
			IsHealthy:          true,
			ProcessState:       "running",
			LastErrorType:      "",
			RestartCount:       0,
			IsReceivingData:    true,
			TotalBytesReceived: 1024,
		}
		current := prev
		current.LastErrorType = "connection_timeout"

		assert.True(t, hasHealthChanged(prev, current))
	})

	t.Run("restart count increased", func(t *testing.T) {
		prev := streamHealthSnapshot{
			IsHealthy:          false,
			ProcessState:       "backoff",
			LastErrorType:      "connection_timeout",
			RestartCount:       3,
			IsReceivingData:    false,
			TotalBytesReceived: 1024,
		}
		current := prev
		current.RestartCount = 4

		assert.True(t, hasHealthChanged(prev, current))
	})

	t.Run("data flow status changed", func(t *testing.T) {
		prev := streamHealthSnapshot{
			IsHealthy:          true,
			ProcessState:       "running",
			LastErrorType:      "",
			RestartCount:       0,
			IsReceivingData:    true,
			TotalBytesReceived: 1024,
		}
		current := prev
		current.IsReceivingData = false

		assert.True(t, hasHealthChanged(prev, current))
	})

	t.Run("total bytes changed but not tracked as change", func(t *testing.T) {
		prev := streamHealthSnapshot{
			IsHealthy:          true,
			ProcessState:       "running",
			LastErrorType:      "",
			RestartCount:       0,
			IsReceivingData:    true,
			TotalBytesReceived: 1024,
		}
		current := prev
		current.TotalBytesReceived = 2048

		// Byte count changes should NOT trigger hasHealthChanged
		assert.False(t, hasHealthChanged(prev, current))
	})

	t.Run("multiple changes simultaneously", func(t *testing.T) {
		prev := streamHealthSnapshot{
			IsHealthy:          true,
			ProcessState:       "running",
			LastErrorType:      "",
			RestartCount:       0,
			IsReceivingData:    true,
			TotalBytesReceived: 1024,
		}
		current := streamHealthSnapshot{
			IsHealthy:          false,
			ProcessState:       "circuit_open",
			LastErrorType:      "rtsp_404",
			RestartCount:       1,
			IsReceivingData:    false,
			TotalBytesReceived: 1024,
		}

		assert.True(t, hasHealthChanged(prev, current))
	})
}

// TestDetermineEventType tests the determineEventType function
func TestDetermineEventType(t *testing.T) {
	t.Run("state change has highest priority", func(t *testing.T) {
		prev := streamHealthSnapshot{
			IsHealthy:       true,
			ProcessState:    "running",
			LastErrorType:   "",
			RestartCount:    0,
			IsReceivingData: true,
		}
		current := streamHealthSnapshot{
			IsHealthy:       false,
			ProcessState:    "circuit_open",
			LastErrorType:   "rtsp_404",
			RestartCount:    1,
			IsReceivingData: false,
		}

		eventType := determineEventType(prev, current)
		assert.Equal(t, "state_change", eventType)
	})

	t.Run("health recovered", func(t *testing.T) {
		prev := streamHealthSnapshot{
			IsHealthy:       false,
			ProcessState:    "running",
			LastErrorType:   "connection_timeout",
			RestartCount:    2,
			IsReceivingData: false,
		}
		current := prev
		current.IsHealthy = true
		current.LastErrorType = ""
		current.IsReceivingData = true

		eventType := determineEventType(prev, current)
		assert.Equal(t, "health_recovered", eventType)
	})

	t.Run("health degraded", func(t *testing.T) {
		prev := streamHealthSnapshot{
			IsHealthy:       true,
			ProcessState:    "running",
			LastErrorType:   "",
			RestartCount:    0,
			IsReceivingData: true,
		}
		current := prev
		current.IsHealthy = false
		current.LastErrorType = "connection_timeout"

		eventType := determineEventType(prev, current)
		assert.Equal(t, "health_degraded", eventType)
	})

	t.Run("error detected", func(t *testing.T) {
		prev := streamHealthSnapshot{
			IsHealthy:       true,
			ProcessState:    "running",
			LastErrorType:   "",
			RestartCount:    0,
			IsReceivingData: true,
		}
		current := prev
		current.LastErrorType = "connection_timeout"

		eventType := determineEventType(prev, current)
		assert.Equal(t, "error_detected", eventType)
	})

	t.Run("error type changed to different error", func(t *testing.T) {
		prev := streamHealthSnapshot{
			IsHealthy:       false,
			ProcessState:    "running",
			LastErrorType:   "connection_timeout",
			RestartCount:    1,
			IsReceivingData: false,
		}
		current := prev
		current.LastErrorType = "rtsp_404"

		eventType := determineEventType(prev, current)
		assert.Equal(t, "error_detected", eventType)
	})

	t.Run("stream restarted", func(t *testing.T) {
		prev := streamHealthSnapshot{
			IsHealthy:       false,
			ProcessState:    "running",
			LastErrorType:   "connection_timeout",
			RestartCount:    2,
			IsReceivingData: false,
		}
		current := prev
		current.RestartCount = 3

		eventType := determineEventType(prev, current)
		assert.Equal(t, "stream_restarted", eventType)
	})

	t.Run("data flow resumed", func(t *testing.T) {
		prev := streamHealthSnapshot{
			IsHealthy:       false,
			ProcessState:    "running",
			LastErrorType:   "",
			RestartCount:    0,
			IsReceivingData: false,
		}
		current := prev
		current.IsReceivingData = true

		eventType := determineEventType(prev, current)
		assert.Equal(t, "data_flow_resumed", eventType)
	})

	t.Run("data flow stopped", func(t *testing.T) {
		prev := streamHealthSnapshot{
			IsHealthy:       true,
			ProcessState:    "running",
			LastErrorType:   "",
			RestartCount:    0,
			IsReceivingData: true,
		}
		current := prev
		current.IsReceivingData = false

		eventType := determineEventType(prev, current)
		assert.Equal(t, "data_flow_stopped", eventType)
	})

	t.Run("generic status update fallback", func(t *testing.T) {
		// When nothing significant changed (shouldn't happen in practice
		// since hasHealthChanged is checked first, but test for completeness)
		prev := streamHealthSnapshot{
			IsHealthy:       true,
			ProcessState:    "running",
			LastErrorType:   "",
			RestartCount:    0,
			IsReceivingData: true,
		}
		current := prev

		eventType := determineEventType(prev, current)
		assert.Equal(t, "status_update", eventType)
	})
}

// TestConvertStreamHealthToResponse tests the convertStreamHealthToResponse function
func TestConvertStreamHealthToResponse(t *testing.T) {
	t.Run("complete health data conversion", func(t *testing.T) {
		now := time.Now()
		health := &myaudio.StreamHealth{
			IsHealthy:          true,
			ProcessState:       myaudio.StateRunning,
			LastDataReceived:   now,
			RestartCount:       0,
			Error:              nil,
			TotalBytesReceived: 1048576,
			BytesPerSecond:     128000.5,
			IsReceivingData:    true,
			LastErrorContext:   nil,
			ErrorHistory:       []*myaudio.ErrorContext{},
			StateHistory:       []myaudio.StateTransition{},
		}

		response := convertStreamHealthToResponse("rtsp://user:pass@camera.local:554/stream", health)

		assert.Equal(t, "rtsp://camera.local:554/stream", response.URL)
		assert.True(t, response.IsHealthy)
		assert.Equal(t, "running", response.ProcessState)
		assert.NotNil(t, response.LastDataReceived)
		assert.NotNil(t, response.TimeSinceData)
		assert.Equal(t, 0, response.RestartCount)
		assert.Empty(t, response.Error)
		assert.Equal(t, int64(1048576), response.TotalBytesReceived)
		assert.InDelta(t, 128000.5, response.BytesPerSecond, 0.1)
		assert.True(t, response.IsReceivingData)
		assert.Nil(t, response.LastErrorContext)
		assert.Empty(t, response.ErrorHistory)
		assert.Empty(t, response.StateHistory)
	})

	t.Run("handle nil LastDataReceived", func(t *testing.T) {
		health := &myaudio.StreamHealth{
			IsHealthy:          false,
			ProcessState:       myaudio.StateStarting,
			LastDataReceived:   time.Time{}, // Zero time
			RestartCount:       0,
			TotalBytesReceived: 0,
			BytesPerSecond:     0,
			IsReceivingData:    false,
		}

		response := convertStreamHealthToResponse("rtsp://camera.local:554/stream", health)

		assert.Nil(t, response.LastDataReceived)
		assert.Nil(t, response.TimeSinceData)
	})

	t.Run("handle error present", func(t *testing.T) {
		testError := errors.New("connection timeout")
		health := &myaudio.StreamHealth{
			IsHealthy:          false,
			ProcessState:       myaudio.StateBackoff,
			LastDataReceived:   time.Time{},
			RestartCount:       2,
			Error:              testError,
			TotalBytesReceived: 0,
			BytesPerSecond:     0,
			IsReceivingData:    false,
		}

		response := convertStreamHealthToResponse("rtsp://camera.local:554/stream", health)

		assert.Equal(t, "connection timeout", response.Error)
	})

	t.Run("handle error context and history", func(t *testing.T) {
		now := time.Now()
		errorCtx := &myaudio.ErrorContext{
			ErrorType:      "rtsp_404",
			PrimaryMessage: "method DESCRIBE failed: 404 Not Found",
			UserFacingMsg:  "RTSP stream not found (404)",
			TroubleShooting: []string{
				"Check if the stream name is correct",
				"Verify the stream path",
			},
			Timestamp:  now,
			TargetHost: "camera.local",
			TargetPort: 554,
			HTTPStatus: 404,
			RTSPMethod: "describe",
		}

		health := &myaudio.StreamHealth{
			IsHealthy:          false,
			ProcessState:       myaudio.StateCircuitOpen,
			LastDataReceived:   time.Time{},
			RestartCount:       3,
			TotalBytesReceived: 0,
			BytesPerSecond:     0,
			IsReceivingData:    false,
			LastErrorContext:   errorCtx,
			ErrorHistory:       []*myaudio.ErrorContext{errorCtx},
		}

		response := convertStreamHealthToResponse("rtsp://camera.local:554/stream", health)

		assert.NotNil(t, response.LastErrorContext)
		assert.Equal(t, "rtsp_404", response.LastErrorContext.ErrorType)
		assert.Equal(t, "method DESCRIBE failed: 404 Not Found", response.LastErrorContext.PrimaryMessage)
		assert.Len(t, response.ErrorHistory, 1)
	})

	t.Run("handle state history", func(t *testing.T) {
		now := time.Now()
		stateHistory := []myaudio.StateTransition{
			{
				From:      myaudio.StateStarting,
				To:        myaudio.StateCircuitOpen,
				Timestamp: now,
				Reason:    "permanent failure detected",
			},
		}

		health := &myaudio.StreamHealth{
			IsHealthy:          false,
			ProcessState:       myaudio.StateCircuitOpen,
			LastDataReceived:   time.Time{},
			RestartCount:       1,
			TotalBytesReceived: 0,
			BytesPerSecond:     0,
			IsReceivingData:    false,
			StateHistory:       stateHistory,
		}

		response := convertStreamHealthToResponse("rtsp://camera.local:554/stream", health)

		assert.Len(t, response.StateHistory, 1)
		assert.Equal(t, "starting", response.StateHistory[0].FromState)
		assert.Equal(t, "circuit_open", response.StateHistory[0].ToState)
		assert.Equal(t, "permanent failure detected", response.StateHistory[0].Reason)
	})

	t.Run("URL sanitization", func(t *testing.T) {
		health := &myaudio.StreamHealth{
			IsHealthy:          true,
			ProcessState:       myaudio.StateRunning,
			RestartCount:       0,
			TotalBytesReceived: 1024,
			BytesPerSecond:     128,
			IsReceivingData:    true,
		}

		response := convertStreamHealthToResponse("rtsp://admin:secret123@192.168.1.100:554/live", health)

		// Verify credentials are removed
		assert.Equal(t, "rtsp://192.168.1.100:554/live", response.URL)
		assert.NotContains(t, response.URL, "admin")
		assert.NotContains(t, response.URL, "secret123")
	})
}

// TestConvertErrorContextToResponse tests the convertErrorContextToResponse function
func TestConvertErrorContextToResponse(t *testing.T) {
	t.Run("nil context returns nil", func(t *testing.T) {
		response := convertErrorContextToResponse(nil)
		assert.Nil(t, response)
	})

	t.Run("complete error context conversion", func(t *testing.T) {
		now := time.Now()
		timeout := 10 * time.Second
		errCtx := &myaudio.ErrorContext{
			ErrorType:       "connection_timeout",
			PrimaryMessage:  "Connection timed out after 10s",
			UserFacingMsg:   "Connection timeout",
			TroubleShooting: []string{"Check network", "Verify URL"},
			Timestamp:       now,
			TargetHost:      "camera.local",
			TargetPort:      554,
			TimeoutDuration: timeout,
			HTTPStatus:      0,
			RTSPMethod:      "",
		}

		response := convertErrorContextToResponse(errCtx)

		assert.NotNil(t, response)
		assert.Equal(t, "connection_timeout", response.ErrorType)
		assert.Equal(t, "Connection timed out after 10s", response.PrimaryMessage)
		assert.Equal(t, "Connection timeout", response.UserFacingMessage)
		assert.Len(t, response.TroubleshootingSteps, 2)
		assert.Equal(t, now, response.Timestamp)
		assert.Equal(t, "camera.local", response.TargetHost)
		assert.Equal(t, 554, response.TargetPort)
		assert.NotNil(t, response.TimeoutDuration)
		assert.Equal(t, "10s", *response.TimeoutDuration)
		assert.Equal(t, 0, response.HTTPStatus)
		assert.Empty(t, response.RTSPMethod)
		assert.False(t, response.ShouldOpenCircuit)
		assert.True(t, response.ShouldRestart)
	})

	t.Run("permanent failure context", func(t *testing.T) {
		now := time.Now()
		errCtx := &myaudio.ErrorContext{
			ErrorType:       "rtsp_404",
			PrimaryMessage:  "method DESCRIBE failed: 404 Not Found",
			UserFacingMsg:   "RTSP stream not found (404)",
			TroubleShooting: []string{"Check stream path"},
			Timestamp:       now,
			TargetHost:      "camera.local",
			TargetPort:      554,
			HTTPStatus:      404,
			RTSPMethod:      "describe",
		}

		response := convertErrorContextToResponse(errCtx)

		assert.Equal(t, 404, response.HTTPStatus)
		assert.Equal(t, "DESCRIBE", response.RTSPMethod) // Should be uppercase
		assert.True(t, response.ShouldOpenCircuit)
		assert.False(t, response.ShouldRestart)
	})

	t.Run("RTSP method uppercase conversion", func(t *testing.T) {
		now := time.Now()
		errCtx := &myaudio.ErrorContext{
			ErrorType:      "rtsp_404",
			PrimaryMessage: "method SETUP failed",
			Timestamp:      now,
			RTSPMethod:     "setup",
		}

		response := convertErrorContextToResponse(errCtx)

		assert.Equal(t, "SETUP", response.RTSPMethod)
	})

	t.Run("optional fields omitted when not set", func(t *testing.T) {
		now := time.Now()
		errCtx := &myaudio.ErrorContext{
			ErrorType:      "generic_error",
			PrimaryMessage: "Something went wrong",
			Timestamp:      now,
			// All optional fields left empty
		}

		response := convertErrorContextToResponse(errCtx)

		assert.Empty(t, response.TargetHost)
		assert.Equal(t, 0, response.TargetPort)
		assert.Nil(t, response.TimeoutDuration)
		assert.Equal(t, 0, response.HTTPStatus)
		assert.Empty(t, response.RTSPMethod)
	})
}
