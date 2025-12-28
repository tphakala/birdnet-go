// internal/api/v2/audio_level_write_deadline_test.go
// Tests for SSE write deadline handling to prevent WriteTimeout disconnections
// This test suite verifies the fix for GitHub issue #1673
package api

import (
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"
	"time"

	"github.com/labstack/echo/v4"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/tphakala/birdnet-go/internal/myaudio"
)

// mockWriteDeadlineResponseWriter tracks whether SetWriteDeadline was called
// before Write operations to verify proper SSE connection keep-alive handling
type mockWriteDeadlineResponseWriter struct {
	http.ResponseWriter
	setWriteDeadlineCalled atomic.Bool
	writeCalledFirst       atomic.Bool // True if Write was called before SetWriteDeadline
	lastDeadline           time.Time
}

// SetWriteDeadline implements WriteDeadlineSetter interface
func (m *mockWriteDeadlineResponseWriter) SetWriteDeadline(t time.Time) error {
	m.setWriteDeadlineCalled.Store(true)
	m.lastDeadline = t
	return nil
}

// Write tracks if it was called before SetWriteDeadline
func (m *mockWriteDeadlineResponseWriter) Write(b []byte) (int, error) {
	if !m.setWriteDeadlineCalled.Load() {
		m.writeCalledFirst.Store(true)
	}
	return m.ResponseWriter.Write(b)
}

// Flush implements http.Flusher
func (m *mockWriteDeadlineResponseWriter) Flush() {
	if f, ok := m.ResponseWriter.(http.Flusher); ok {
		f.Flush()
	}
}

// TestSendAudioLevelUpdateSetsWriteDeadline verifies that sendAudioLevelUpdate
// sets the write deadline before writing to prevent server WriteTimeout
// from terminating the SSE connection prematurely.
//
// This test prevents regression of GitHub issue #1673 where SSE connections
// were disconnecting every ~30 seconds due to WriteTimeout not being reset.
func TestSendAudioLevelUpdateSetsWriteDeadline(t *testing.T) {
	t.Run("sets write deadline before sending data", func(t *testing.T) {
		// Create Echo context with mock response writer
		e := echo.New()
		rec := httptest.NewRecorder()
		mockWriter := &mockWriteDeadlineResponseWriter{ResponseWriter: rec}

		req := httptest.NewRequest(http.MethodGet, "/api/v2/streams/audio-level", http.NoBody)
		ctx := e.NewContext(req, rec)

		// Replace the response writer with our mock
		ctx.Response().Writer = mockWriter

		// Create minimal controller
		controller := &Controller{}

		// Create test audio level data
		levels := map[string]myaudio.AudioLevelData{
			"test_source": {
				Level:  50,
				Name:   "Test Source",
				Source: "test_source",
			},
		}

		// Call sendAudioLevelUpdate
		err := controller.sendAudioLevelUpdate(ctx, levels)
		require.NoError(t, err)

		// Verify SetWriteDeadline was called
		assert.True(t, mockWriter.setWriteDeadlineCalled.Load(),
			"SetWriteDeadline should be called before writing SSE data")

		// Verify Write was NOT called before SetWriteDeadline
		assert.False(t, mockWriter.writeCalledFirst.Load(),
			"Write should not be called before SetWriteDeadline")

		// Verify the deadline is in the future (at least a few seconds)
		assert.True(t, mockWriter.lastDeadline.After(time.Now()),
			"Write deadline should be set to a future time")
	})

	t.Run("write deadline extends connection lifetime", func(t *testing.T) {
		// Create Echo context with mock response writer
		e := echo.New()
		rec := httptest.NewRecorder()
		mockWriter := &mockWriteDeadlineResponseWriter{ResponseWriter: rec}

		req := httptest.NewRequest(http.MethodGet, "/api/v2/streams/audio-level", http.NoBody)
		ctx := e.NewContext(req, rec)
		ctx.Response().Writer = mockWriter

		controller := &Controller{}
		levels := map[string]myaudio.AudioLevelData{
			"test_source": {Level: 50, Name: "Test Source", Source: "test_source"},
		}

		// Call sendAudioLevelUpdate multiple times
		for i := range 3 {
			mockWriter.setWriteDeadlineCalled.Store(false)
			err := controller.sendAudioLevelUpdate(ctx, levels)
			require.NoError(t, err)

			// Each call should set the write deadline
			assert.True(t, mockWriter.setWriteDeadlineCalled.Load(),
				"SetWriteDeadline should be called on each update (iteration %d)", i)
		}
	})
}

// TestSendAudioLevelHeartbeatSetsWriteDeadline verifies that heartbeat writes
// set the write deadline to prevent connection timeout during idle periods.
func TestSendAudioLevelHeartbeatSetsWriteDeadline(t *testing.T) {
	t.Run("heartbeat sets write deadline before writing", func(t *testing.T) {
		// Create Echo context with mock response writer
		e := echo.New()
		rec := httptest.NewRecorder()
		mockWriter := &mockWriteDeadlineResponseWriter{ResponseWriter: rec}

		req := httptest.NewRequest(http.MethodGet, "/api/v2/streams/audio-level", http.NoBody)
		ctx := e.NewContext(req, rec)
		ctx.Response().Writer = mockWriter

		// Create minimal controller
		controller := &Controller{}

		// Call sendAudioLevelHeartbeat
		err := controller.sendAudioLevelHeartbeat(ctx)
		require.NoError(t, err)

		// Verify SetWriteDeadline was called
		assert.True(t, mockWriter.setWriteDeadlineCalled.Load(),
			"SetWriteDeadline should be called before writing heartbeat")

		// Verify Write was NOT called before SetWriteDeadline
		assert.False(t, mockWriter.writeCalledFirst.Load(),
			"Write should not be called before SetWriteDeadline")

		// Verify heartbeat was written to response
		assert.Contains(t, rec.Body.String(), "heartbeat",
			"Response should contain heartbeat message")
	})
}
