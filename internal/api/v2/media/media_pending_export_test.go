// media_pending_export_test.go: coverage for the Extended Capture pending-clip
// handling. When a detection's audio clip write is deferred until the capture tail is
// recorded, the media API must return 503 + Retry-After (not 404) while the clip is
// still legitimately pending, and must fall back to 404 once the window has passed.

package media

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strconv"
	"testing"
	"time"

	"github.com/labstack/echo/v4"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/tphakala/birdnet-go/internal/datastore"
	"github.com/tphakala/birdnet-go/internal/datastore/mocks"
)

// TestServeAudioByID_PendingExport verifies that a missing clip whose Extended Capture
// export is still within its computed ready window returns 503 + Retry-After
// immediately (not a 404, and without blocking on the grace wait), while a clip whose
// window has long passed reverts to a 404.
func TestServeAudioByID_PendingExport(t *testing.T) {
	const missingClip = "2024/01/pending_95p_20240101T000000Z.wav"
	now := time.Now()

	tests := []struct {
		name      string
		begin     time.Time
		end       time.Time
		wantCode  int
		wantRetry bool
	}{
		{
			name:      "recent detection with pending export returns 503",
			begin:     now,
			end:       now.Add(30 * time.Second), // long capture still finishing
			wantCode:  http.StatusServiceUnavailable,
			wantRetry: true,
		},
		{
			name:     "old detection past the export window 404s",
			begin:    now.Add(-2 * time.Hour),
			end:      now.Add(-2 * time.Hour),
			wantCode: http.StatusNotFound,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			e, controller, _ := setupMediaTestEnvironment(t)

			mockDS := mocks.NewMockInterface(t)
			mockDS.On("GetNoteClipPath", "42").Return(missingClip, nil)
			mockDS.On("Get", "42").Return(datastore.Note{ID: 42, BeginTime: tc.begin, EndTime: tc.end}, nil).Maybe()
			controller.DS = mockDS

			req := httptest.NewRequest(http.MethodGet, "/api/v2/audio/42", http.NoBody)
			rec := httptest.NewRecorder()
			c := e.NewContext(req, rec)
			c.SetParamNames("id")
			c.SetParamValues("42")

			handlerErr := controller.ServeAudioByID(c)

			// Pending returns 503 via ctx.JSON (nil error, code on the recorder); a
			// missing/ghost clip returns an echo.HTTPError 404. Resolve either way. The
			// status code alone distinguishes the paths: a 503 can only come from the
			// immediate pending branch (the grace/ghost path yields 404), so no wall-clock
			// timing assertion is needed (and none is used, to avoid CI flakiness).
			code := rec.Code
			if httpErr, ok := errors.AsType[*echo.HTTPError](handlerErr); ok {
				code = httpErr.Code
			}
			assert.Equal(t, tc.wantCode, code)

			if tc.wantRetry {
				retryAfter := rec.Header().Get("Retry-After")
				require.NotEmpty(t, retryAfter, "503 must carry a Retry-After header")
				secs, convErr := strconv.Atoi(retryAfter)
				require.NoError(t, convErr)
				assert.GreaterOrEqual(t, secs, minRetryAfterSeconds)
			}
		})
	}
}

// TestWaitForPendingClip covers the async spectrogram worker's bounded wait for a
// not-yet-written clip. It uses an injected short poll interval and real short
// deadlines rather than synctest, because the file appearing is a real filesystem
// event decoupled from any fake clock.
func TestWaitForPendingClip(t *testing.T) {
	const rel = "pending.wav"
	const pollInterval = 5 * time.Millisecond

	t.Run("returns true immediately when the file already exists", func(t *testing.T) {
		_, controller, tempDir := setupMediaTestEnvironment(t)
		require.NoError(t, os.WriteFile(filepath.Join(tempDir, rel), []byte("x"), 0o600))

		// Deadline in the past: if the fast-path existence check did not short-circuit,
		// the wait would return false at once. A true result proves the pre-poll check
		// fired, without asserting a flaky wall-clock upper bound.
		ok := controller.waitForPendingClip(t.Context(), rel, time.Now().Add(-time.Second), pollInterval)

		assert.True(t, ok)
	})

	t.Run("returns false when the file never appears by the deadline", func(t *testing.T) {
		_, controller, _ := setupMediaTestEnvironment(t)

		const waitDeadline = 50 * time.Millisecond
		// Lower bound only (context.WithDeadline never fires early, so slow/-race CI can
		// only push elapsed higher): proves the poller waited toward the deadline rather
		// than returning false immediately. Derived from waitDeadline so the two stay in
		// sync if the deadline is retuned.
		const minWait = waitDeadline - 10*time.Millisecond

		start := time.Now()
		ok := controller.waitForPendingClip(t.Context(), rel, time.Now().Add(waitDeadline), pollInterval)

		assert.False(t, ok)
		assert.GreaterOrEqual(t, time.Since(start), minWait, "should wait until near the deadline")
	})

	t.Run("returns true when the file appears before the deadline", func(t *testing.T) {
		_, controller, tempDir := setupMediaTestEnvironment(t)
		go func() {
			time.Sleep(20 * time.Millisecond)
			_ = os.WriteFile(filepath.Join(tempDir, rel), []byte("x"), 0o600)
		}()

		ok := controller.waitForPendingClip(t.Context(), rel, time.Now().Add(time.Second), pollInterval)

		assert.True(t, ok)
	})

	t.Run("returns false promptly when the context is canceled", func(t *testing.T) {
		_, controller, _ := setupMediaTestEnvironment(t)
		ctx, cancel := context.WithCancel(t.Context())
		cancel()

		ok := controller.waitForPendingClip(ctx, rel, time.Now().Add(time.Second), pollInterval)

		assert.False(t, ok)
	})
}
