// media_pending_export_test.go: coverage for the Extended Capture pending-clip
// handling. When a detection's audio clip write is deferred until the capture tail is
// recorded, the media API must return 503 + Retry-After (not 404) while the clip is
// still legitimately pending, and must fall back to 404 once the window has passed.

package media

import (
	"context"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strconv"
	"testing"
	"testing/synctest"
	"time"

	"github.com/labstack/echo/v4"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/tphakala/birdnet-go/internal/datastore"
	"github.com/tphakala/birdnet-go/internal/datastore/mocks"
	"github.com/tphakala/birdnet-go/internal/errors"
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
			// Retry-After tells the pending paths apart: the immediate pending branch sizes
			// it to the time left until ReadyAt (above the floor), while the post-grace 503
			// of a recent clip uses the floor, so no wall-clock timing assertion is needed
			// (and none is used, to avoid CI flakiness).
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
				assert.Greater(t, secs, minRetryAfterSeconds,
					"pending export must size Retry-After to the time left, above the floor")
			}
		})
	}
}

// TestWaitForPendingClip covers the async spectrogram worker's bounded wait for a
// not-yet-written clip. Each subtest runs under synctest with an injected poll
// interval, so elapsed times are fake-clock durations, asserted exactly except
// when the file appears, where the poll that sees it may land one interval after
// the write. The controller is built outside the bubble; the writer goroutine that makes the file appear runs
// inside it, so the file appears at a known fake-clock instant while the file
// system itself stays real.
func TestWaitForPendingClip(t *testing.T) {
	const (
		rel          = "pending.wav"
		pollInterval = 5 * time.Millisecond
		waitDeadline = time.Second
		appearAfter  = 20 * time.Millisecond
	)

	t.Run("returns true without polling when the file already exists", func(t *testing.T) {
		_, controller, tempDir := setupMediaTestEnvironment(t)
		require.NoError(t, os.WriteFile(filepath.Join(tempDir, rel), []byte("x"), 0o600))

		synctest.Test(t, func(t *testing.T) {
			start := time.Now()
			ok := controller.waitForPendingClip(t.Context(), rel, start.Add(waitDeadline), pollInterval)

			assert.True(t, ok)
			assert.Zero(t, time.Since(start), "an existing file must not wait a poll interval")
		})
	})

	t.Run("returns false at the deadline when the file never appears", func(t *testing.T) {
		_, controller, _ := setupMediaTestEnvironment(t)

		synctest.Test(t, func(t *testing.T) {
			start := time.Now()
			ok := controller.waitForPendingClip(t.Context(), rel, start.Add(waitDeadline), pollInterval)

			assert.False(t, ok)
			assert.Equal(t, waitDeadline, time.Since(start), "must wait exactly until the deadline")
		})
	})

	t.Run("returns true within one poll of the file appearing", func(t *testing.T) {
		_, controller, tempDir := setupMediaTestEnvironment(t)

		synctest.Test(t, func(t *testing.T) {
			go func() {
				time.Sleep(appearAfter)
				_ = os.WriteFile(filepath.Join(tempDir, rel), []byte("x"), 0o600)
			}()

			start := time.Now()
			ok := controller.waitForPendingClip(t.Context(), rel, start.Add(waitDeadline), pollInterval)

			assert.True(t, ok)
			elapsed := time.Since(start)
			assert.GreaterOrEqual(t, elapsed, appearAfter, "cannot return before the file exists")
			assert.LessOrEqual(t, elapsed, appearAfter+pollInterval, "must notice the file within one poll")
		})
	})

	t.Run("returns false without waiting when the context is canceled", func(t *testing.T) {
		_, controller, _ := setupMediaTestEnvironment(t)

		synctest.Test(t, func(t *testing.T) {
			ctx, cancel := context.WithCancel(t.Context())
			cancel()

			start := time.Now()
			ok := controller.waitForPendingClip(ctx, rel, start.Add(waitDeadline), pollInterval)

			assert.False(t, ok)
			assert.Zero(t, time.Since(start), "a canceled context must return without polling")
		})
	})
}
