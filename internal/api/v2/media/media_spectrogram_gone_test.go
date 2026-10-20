// media_spectrogram_gone_test.go: coverage for answering a spectrogram request for a
// clip whose audio is gone for good without the server-side wait, while keeping the
// wait for a clip that may still be written, and for the machine readable reason the
// status and 404 answers carry (Forgejo #1949).

package media

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"testing"
	"time"

	"github.com/labstack/echo/v4"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/tphakala/birdnet-go/internal/conf"
	"github.com/tphakala/birdnet-go/internal/datastore"
	"github.com/tphakala/birdnet-go/internal/datastore/mocks"
	"github.com/tphakala/birdnet-go/internal/spectrogram"
)

const (
	// goneTestWaitOverride is the shortened server-side wait. A fast answer must come
	// back well inside it, a waiting answer must take at least this long.
	goneTestWaitOverride = 400 * time.Millisecond
	// goneTestQuery selects the 514 px raw render of the clip.
	goneTestQuery = "?size=md&raw=true"
	// goneTestOldAge is far past diskmanager.ClipRecencyWindow and the pending window.
	goneTestOldAge = time.Hour
	// goneTestRecentAge is past ReadyAt plus pendingExportGraceMargin but inside the
	// recency window, so the clip may still be written but no export is pending.
	goneTestRecentAge = 5 * time.Minute
	// goneTestCaptureSpan is the detection's duration (BeginTime to EndTime).
	goneTestCaptureSpan = 15 * time.Second
)

// goneNote returns a detection row for the test clip that completed age ago.
func goneNote(age time.Duration) datastore.Note {
	end := time.Now().Add(-age)
	return datastore.Note{
		ClipName:  retainedClipName,
		BeginTime: end.Add(-goneTestCaptureSpan),
		EndTime:   end,
	}
}

// goneDS returns a mock for a detection with the test clip name and the given row.
func goneDS(t *testing.T, note *datastore.Note) *mocks.MockInterface {
	t.Helper()
	ds := mocks.NewMockInterface(t)
	ds.EXPECT().GetNoteClipPath(retainedNoteID).Return(retainedClipName, nil).Maybe()
	ds.EXPECT().Get(retainedNoteID).Return(*note, nil)
	ds.EXPECT().GetNoteModelType(retainedNoteID).Return("bird", nil).Maybe()
	return ds
}

// serveSpectrogramTimed issues the by-ID image request and returns the response with
// the time it took.
func serveSpectrogramTimed(t *testing.T, h *Handler, e *echo.Echo, query string) (*httptest.ResponseRecorder, time.Duration) {
	t.Helper()
	start := time.Now()
	rec := serveSpectrogram(t, h, e, query)
	return rec, time.Since(start)
}

// getSpectrogramStatusData issues GET /spectrogram/:id/status and returns the data
// object of the answer.
func getSpectrogramStatusData(t *testing.T, h *Handler, e *echo.Echo, query string) map[string]any {
	t.Helper()
	req := httptest.NewRequest(http.MethodGet, "/api/v2/spectrogram/"+retainedNoteID+"/status"+query, http.NoBody)
	rec := httptest.NewRecorder()
	c := e.NewContext(req, rec)
	c.SetParamNames("id")
	c.SetParamValues(retainedNoteID)
	require.NoError(t, h.GetSpectrogramStatus(c))
	require.Equal(t, http.StatusOK, rec.Code, rec.Body.String())
	var body struct {
		Data map[string]any `json:"data"`
	}
	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &body))
	return body.Data
}

// responseReason returns the data.reason of a JSON error response.
func responseReason(t *testing.T, rec *httptest.ResponseRecorder) string {
	t.Helper()
	var body struct {
		Message string `json:"message"`
		Data    struct {
			Reason string `json:"reason"`
		} `json:"data"`
	}
	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &body), rec.Body.String())
	return body.Data.Reason
}

func TestServeSpectrogramByID_MissingSourceWaitsOnlyWhileClipCanAppear(t *testing.T) {
	tests := []struct {
		name       string
		note       datastore.Note
		files      map[string]string
		wantFast   bool
		wantCode   int
		wantReason string
		wantBody   string
	}{
		{
			name:       "old detection with no render answers at once",
			note:       goneNote(goneTestOldAge),
			wantFast:   true,
			wantCode:   http.StatusNotFound,
			wantReason: spectrogramReasonSourceMissing,
		},
		{
			name:     "old detection still serves a kept render at once",
			note:     goneNote(goneTestOldAge),
			files:    map[string]string{"2026/01/a_1026px-legend.png": "legend"},
			wantFast: true,
			wantCode: http.StatusOK,
			wantBody: "legend",
		},
		{
			name:       "recent detection keeps the wait and is then pending",
			note:       goneNote(goneTestRecentAge),
			wantCode:   http.StatusNotFound,
			wantReason: spectrogramReasonSourcePending,
		},
		{
			name:       "unknown capture times keep the wait",
			note:       datastore.Note{ClipName: retainedClipName},
			wantCode:   http.StatusNotFound,
			wantReason: spectrogramReasonSourcePending,
		},
		{
			name:       "fresh export temp file keeps the wait",
			note:       goneNote(goneTestOldAge),
			files:      map[string]string{"2026/01/a.wav.1234.1.temp": "encoding"},
			wantCode:   http.StatusNotFound,
			wantReason: spectrogramReasonSourcePending,
		},
		{
			name:       "clip under another extension keeps the wait",
			note:       goneNote(goneTestOldAge),
			files:      map[string]string{"2026/01/a.flac": "audio"},
			wantCode:   http.StatusNotFound,
			wantReason: spectrogramReasonSourcePending,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			h, e, root := newRetainedTestHandler(t, conf.SpectrogramModeAuto)
			h.audioWaitTimeoutOverride = goneTestWaitOverride
			for rel, content := range tt.files {
				writeRetainedFile(t, root, rel, content)
			}
			h.DS = goneDS(t, &tt.note)

			rec, elapsed := serveSpectrogramTimed(t, h, e, goneTestQuery)

			require.Equal(t, tt.wantCode, rec.Code, rec.Body.String())
			if tt.wantFast {
				assert.Less(t, elapsed, goneTestWaitOverride, "a clip that cannot appear must not be waited for")
			} else {
				assert.GreaterOrEqual(t, elapsed, goneTestWaitOverride, "a clip that may still be written keeps the wait")
			}
			if tt.wantCode == http.StatusOK {
				assert.Equal(t, tt.wantBody, rec.Body.String())
				return
			}
			assert.Equal(t, tt.wantReason, responseReason(t, rec))
			assert.Contains(t, rec.Body.String(), msgSourceAudioNotFound)
		})
	}
}

func TestGetSpectrogramStatus_NotStartedReportsSourceReason(t *testing.T) {
	tests := []struct {
		name       string
		note       datastore.Note
		files      map[string]string
		wantReason string
	}{
		{
			name:       "source on disk is ready to generate",
			note:       goneNote(goneTestOldAge),
			files:      map[string]string{retainedClipName: "audio"},
			wantReason: spectrogramReasonNotGenerated,
		},
		{
			name:       "old and absent source is missing",
			note:       goneNote(goneTestOldAge),
			wantReason: spectrogramReasonSourceMissing,
		},
		{
			name:       "recent and absent source is pending",
			note:       goneNote(goneTestRecentAge),
			wantReason: spectrogramReasonSourcePending,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			h, e, root := newRetainedTestHandler(t, conf.SpectrogramModeAuto)
			for rel, content := range tt.files {
				writeRetainedFile(t, root, rel, content)
			}
			h.DS = goneDS(t, &tt.note)

			data := getSpectrogramStatusData(t, h, e, goneTestQuery)

			assert.Equal(t, spectrogramStatusNotStarted, data["status"])
			assert.Equal(t, tt.wantReason, data["reason"])
		})
	}
}

// TestGetSpectrogramStatus_ServeFlowFailureStaysUnderPathKey pins the queue-key split
// as deliberate: the image GET records its failure under the path key, so the by-ID
// status poll keeps answering not_started (with the source reason) instead of failed.
// A not-ready 503 of the serve flow would otherwise read as a terminal failure.
func TestGetSpectrogramStatus_ServeFlowFailureStaysUnderPathKey(t *testing.T) {
	h, e, _ := newRetainedTestHandler(t, conf.SpectrogramModeAuto)
	h.audioWaitTimeoutOverride = 50 * time.Millisecond
	h.DS = goneDS(t, new(goneNote(goneTestRecentAge)))

	req := httptest.NewRequest(http.MethodGet, "/?size=md&raw=true", http.NoBody)
	params, err := h.parseSpectrogramParameters(e.NewContext(req, httptest.NewRecorder()))
	require.NoError(t, err)
	freqSuffix := spectrogram.ProfileSuffix(spectrogram.ProfileForModelType("bird"))
	_, _, _, relSpectrogramPath := buildSpectrogramPaths(retainedClipName, params.width, params.raw, params.style, params.dynamicRange, freqSuffix)
	pathKey := buildSpectrogramKey(relSpectrogramPath, params.width, params.raw)
	queueKey := buildSpectrogramQueueKey(retainedNoteID, params.width, params.raw, params.style, params.dynamicRange)
	t.Cleanup(func() {
		spectrogramQueue.Delete(pathKey)
		spectrogramQueue.Delete(queueKey)
	})

	rec := serveSpectrogram(t, h, e, goneTestQuery)
	require.Equal(t, http.StatusNotFound, rec.Code, rec.Body.String())

	pathEntry, ok := spectrogramQueue.Load(pathKey)
	require.True(t, ok, "the serve flow retains its failure under the path key")
	pathStatus, ok := pathEntry.(*SpectrogramQueueStatus)
	require.True(t, ok)
	assert.Equal(t, spectrogramStatusFailed, pathStatus.GetStatus())
	_, byID := spectrogramQueue.Load(queueKey)
	assert.False(t, byID, "the serve flow never writes the by-ID key")

	data := getSpectrogramStatusData(t, h, e, goneTestQuery)
	assert.Equal(t, spectrogramStatusNotStarted, data["status"])
	assert.Equal(t, spectrogramReasonSourcePending, data["reason"])
}

func TestRunAsyncSpectrogramGeneration_GoneSourceFailsWithoutWaiting(t *testing.T) {
	h, e, _ := newRetainedTestHandler(t, conf.SpectrogramModeAuto)
	h.DS = goneDS(t, new(goneNote(goneTestOldAge)))

	req := httptest.NewRequest(http.MethodGet, "/?size=md&raw=true", http.NoBody)
	params, err := h.parseSpectrogramParameters(e.NewContext(req, httptest.NewRecorder()))
	require.NoError(t, err)
	profile := spectrogram.ProfileForModelType("bird")
	queueKey := buildSpectrogramQueueKey(retainedNoteID, params.width, params.raw, params.style, params.dynamicRange)
	t.Cleanup(func() { spectrogramQueue.Delete(queueKey) })
	h.initializeQueueStatus(queueKey)

	start := time.Now()
	h.runAsyncSpectrogramGeneration(retainedNoteID, retainedClipName, filepath.ToSlash(retainedClipName), queueKey, params, spectrogram.ProfileSuffix(profile), profile)
	elapsed := time.Since(start)

	// The wait is not shortened here (no override), so the old code needed the full
	// audioWaitTimeout before it failed.
	assert.Less(t, elapsed, audioWaitTimeout/2, "a gone clip must not cost the audio wait")
	data := getSpectrogramStatusData(t, h, e, goneTestQuery)
	assert.Equal(t, spectrogramStatusFailed, data["status"])
	assert.Equal(t, spectrogramReasonSourceMissing, data["reason"])
}
