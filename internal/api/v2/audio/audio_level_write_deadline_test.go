// internal/api/v2/audio/audio_level_write_deadline_test.go
// Tests for SSE write deadline handling to prevent WriteTimeout disconnections
// This test suite verifies the fix for GitHub issue #1673
package audio

import (
	"net/http"
	"net/http/httptest"
	"testing"
	"testing/synctest"
	"time"

	"github.com/labstack/echo/v4"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/tphakala/birdnet-go/internal/api/v2/apicore"
	"github.com/tphakala/birdnet-go/internal/api/v2/apitest"
	"github.com/tphakala/birdnet-go/internal/audiocore"
)

const (
	// deadlineTestUpdates is how many audio level updates the update test sends.
	deadlineTestUpdates = 3
	// deadlineTestClockStep is how far the fake clock moves between updates:
	// non-zero and shorter than the deadline, so a stale deadline cannot match.
	deadlineTestClockStep = audioLevelWriteDeadline / 2
)

// deadlineRecordingWriter records the last write deadline set through
// http.NewResponseController, and whether anything was written before one was.
// The tests call the senders synchronously, so it needs no locking.
type deadlineRecordingWriter struct {
	*httptest.ResponseRecorder
	deadline            time.Time
	wroteBeforeDeadline bool
}

// SetWriteDeadline lets http.NewResponseController find and set a write deadline.
func (w *deadlineRecordingWriter) SetWriteDeadline(t time.Time) error {
	w.deadline = t
	return nil
}

// Write notes a write that happens before any write deadline was set.
func (w *deadlineRecordingWriter) Write(b []byte) (int, error) {
	if w.deadline.IsZero() {
		w.wroteBeforeDeadline = true
	}
	return w.ResponseRecorder.Write(b)
}

// newDeadlineTestContext returns a handler and an echo context that writes
// through a deadlineRecordingWriter.
func newDeadlineTestContext() (*Handler, echo.Context, *deadlineRecordingWriter) {
	writer := &deadlineRecordingWriter{ResponseRecorder: httptest.NewRecorder()}
	req := httptest.NewRequest(http.MethodGet, "/api/v2/streams/audio-level", http.NoBody)
	ctx := echo.New().NewContext(req, writer)

	controller := &Handler{Core: &apicore.Core{}}
	controller.Settings.Store(apitest.NewValidTestSettings())
	return controller, ctx, writer
}

// assertDeadlineSetBeforeWrite checks that nothing was written before a write
// deadline was set, and that the deadline is audioLevelWriteDeadline after the
// current time. It must run inside a synctest bubble, where the clock does not
// move on its own, so the deadline equals that sum exactly.
func assertDeadlineSetBeforeWrite(t *testing.T, writer *deadlineRecordingWriter) {
	t.Helper()
	assert.False(t, writer.wroteBeforeDeadline, "Write should not be called before SetWriteDeadline")
	assert.Equal(t, time.Now().Add(audioLevelWriteDeadline), writer.deadline,
		"Write deadline should be audioLevelWriteDeadline (%v) after the current time", audioLevelWriteDeadline)
}

// TestSendAudioLevelUpdateSetsWriteDeadline verifies that every
// sendAudioLevelUpdate sets the write deadline before writing, so the server
// WriteTimeout does not end the SSE connection.
//
// This test prevents regression of GitHub issue #1673 where SSE connections
// were disconnecting every ~30 seconds due to WriteTimeout not being reset.
func TestSendAudioLevelUpdateSetsWriteDeadline(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		controller, ctx, writer := newDeadlineTestContext()
		levels := map[string]audiocore.AudioLevelData{
			"test_source": {
				Level:  50,
				Name:   "Test Source",
				Source: "test_source",
			},
		}

		// Send repeatedly with the clock moving between updates. The recorded state
		// is cleared first so every update has to set its own deadline before it
		// writes, instead of relying on the one set for an earlier update.
		for range deadlineTestUpdates {
			writer.deadline = time.Time{}
			writer.wroteBeforeDeadline = false
			writer.Body.Reset()
			require.NoError(t, controller.sendAudioLevelUpdate(ctx, levels))
			assert.Contains(t, writer.Body.String(), "audio-level", "Each update should write its SSE message")
			assertDeadlineSetBeforeWrite(t, writer)
			time.Sleep(deadlineTestClockStep)
		}
	})
}

// TestSendAudioLevelHeartbeatSetsWriteDeadline verifies that heartbeat writes
// set the write deadline to prevent connection timeout during idle periods.
func TestSendAudioLevelHeartbeatSetsWriteDeadline(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		controller, ctx, writer := newDeadlineTestContext()

		require.NoError(t, controller.sendAudioLevelHeartbeat(ctx))

		assertDeadlineSetBeforeWrite(t, writer)
		assert.Contains(t, writer.Body.String(), "heartbeat",
			"Response should contain heartbeat message")
	})
}
