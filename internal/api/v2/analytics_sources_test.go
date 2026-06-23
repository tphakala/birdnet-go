package api

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/labstack/echo/v4"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/require"

	"github.com/tphakala/birdnet-go/internal/datastore"
)

// analyticsSourceJSON mirrors one item of the analytics source/mic filter wire payload.
type analyticsSourceJSON struct {
	ID    string `json:"id"`
	Name  string `json:"name"`
	Count int    `json:"count"`
}

// analyticsSourcesRespJSON mirrors the analytics source list envelope.
type analyticsSourcesRespJSON struct {
	Sources []analyticsSourceJSON `json:"sources"`
}

func sampleAudioSources() []datastore.AudioSourceSummary {
	return []datastore.AudioSourceSummary{
		{ID: 7, DisplayName: "Backyard", NodeName: "node-a", SourceType: "rtsp", Count: 42},
		{ID: 3, DisplayName: "", NodeName: "node-b", SourceType: "alsa", Count: 9},
	}
}

func newAnalyticsSourcesContext(e *echo.Echo, target string) (echo.Context, *httptest.ResponseRecorder) {
	req := httptest.NewRequest(http.MethodGet, target, http.NoBody)
	rec := httptest.NewRecorder()
	c := e.NewContext(req, rec)
	c.SetPath("/api/v2/analytics/sources")
	return c, rec
}

func TestGetAnalyticsSources_ShapeAnonymizedWhenUnauthenticated(t *testing.T) {
	t.Parallel()
	e, mockDS, controller := setupAnalyticsTestEnvironment(t)

	// The test controller has no auth service, so isClientAuthenticated() is false: names must be
	// anonymized, but the opaque numeric ids and counts pass through verbatim.
	mockDS.On("GetAudioSources", mock.Anything, "2026-03-01", "2026-03-31").
		Return(sampleAudioSources(), nil)

	c, rec := newAnalyticsSourcesContext(e, "/api/v2/analytics/sources?start_date=2026-03-01&end_date=2026-03-31")
	require.NoError(t, controller.GetAnalyticsSources(c))
	require.Equal(t, http.StatusOK, rec.Code)

	var resp analyticsSourcesRespJSON
	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &resp))
	require.Len(t, resp.Sources, 2)

	// Order preserved (server already sorts by count desc).
	assert.Equal(t, "7", resp.Sources[0].ID)
	assert.Equal(t, "camera-7", resp.Sources[0].Name) // rtsp -> camera-N, never the configured "Backyard"
	assert.Equal(t, 42, resp.Sources[0].Count)

	assert.Equal(t, "3", resp.Sources[1].ID)
	assert.Equal(t, "audio-source-3", resp.Sources[1].Name) // alsa -> audio-source-N
	assert.Equal(t, 9, resp.Sources[1].Count)

	mockDS.AssertExpectations(t)
}

func TestGetAnalyticsSources_EmptyArrayNotNull(t *testing.T) {
	t.Parallel()
	e, mockDS, controller := setupAnalyticsTestEnvironment(t)

	mockDS.On("GetAudioSources", mock.Anything, "", "").
		Return([]datastore.AudioSourceSummary{}, nil)

	c, rec := newAnalyticsSourcesContext(e, "/api/v2/analytics/sources")
	require.NoError(t, controller.GetAnalyticsSources(c))
	require.Equal(t, http.StatusOK, rec.Code)

	// Empty result must serialize as {"sources": []} (not null) so the client can read .length safely.
	var resp analyticsSourcesRespJSON
	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &resp))
	require.NotNil(t, resp.Sources)
	assert.Empty(t, resp.Sources)
	mockDS.AssertExpectations(t)
}

func TestGetAnalyticsSources_AllHistoryWhenNoDates(t *testing.T) {
	t.Parallel()
	e, mockDS, controller := setupAnalyticsTestEnvironment(t)

	// Dates are optional; omitting both must pass empty strings through (all-history at the datastore).
	mockDS.On("GetAudioSources", mock.Anything, "", "").
		Return(sampleAudioSources(), nil)

	c, rec := newAnalyticsSourcesContext(e, "/api/v2/analytics/sources")
	require.NoError(t, controller.GetAnalyticsSources(c))
	require.Equal(t, http.StatusOK, rec.Code)
	mockDS.AssertExpectations(t)
}

func TestGetAnalyticsSources_InvalidDateFormat(t *testing.T) {
	t.Parallel()
	e, _, controller := setupAnalyticsTestEnvironment(t)

	c, rec := newAnalyticsSourcesContext(e, "/api/v2/analytics/sources?start_date=not-a-date")
	err := controller.GetAnalyticsSources(c)
	require.Error(t, err)
	assert.Equal(t, http.StatusBadRequest, rec.Code)
}

func TestGetAnalyticsSources_ReversedRange(t *testing.T) {
	t.Parallel()
	e, _, controller := setupAnalyticsTestEnvironment(t)

	c, rec := newAnalyticsSourcesContext(e,
		"/api/v2/analytics/sources?start_date=2026-03-05&end_date=2026-03-01")
	err := controller.GetAnalyticsSources(c)
	require.Error(t, err)
	assert.Equal(t, http.StatusBadRequest, rec.Code)
}

func TestGetAnalyticsSources_QueryTimeout(t *testing.T) {
	t.Parallel()
	e, mockDS, controller := setupAnalyticsTestEnvironment(t)

	mockDS.On("GetAudioSources", mock.Anything, "", "").
		Return([]datastore.AudioSourceSummary(nil), context.DeadlineExceeded)

	c, rec := newAnalyticsSourcesContext(e, "/api/v2/analytics/sources")
	// handleAnalyticsQueryError writes the 408 response and returns nil.
	require.NoError(t, controller.GetAnalyticsSources(c))
	assert.Equal(t, http.StatusRequestTimeout, rec.Code)
	mockDS.AssertExpectations(t)
}

func TestAnonymizeHistoricalSourceName(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name       string
		sourceType string
		id         uint
		want       string
	}{
		{name: "alsa sound card", sourceType: "alsa", id: 1, want: "audio-source-1"},
		{name: "pulseaudio sound card", sourceType: "pulseaudio", id: 2, want: "audio-source-2"},
		{name: "rtsp stream", sourceType: "rtsp", id: 5, want: "camera-5"},
		{name: "file input", sourceType: "file", id: 8, want: "file-source-8"},
		{name: "unknown type", sourceType: "unknown", id: 4, want: "source-4"},
		{name: "unrecognized future type", sourceType: "webrtc", id: 6, want: "source-6"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			assert.Equal(t, tt.want, anonymizeHistoricalSourceName(tt.sourceType, tt.id))
		})
	}
}

func TestAnalyticsSourceLabel(t *testing.T) {
	t.Parallel()

	t.Run("authenticated prefers display name", func(t *testing.T) {
		t.Parallel()
		src := &datastore.AudioSourceSummary{ID: 7, DisplayName: "Backyard", NodeName: "node-a", SourceType: "rtsp"}
		assert.Equal(t, "Backyard", analyticsSourceLabel(src, true))
	})

	t.Run("authenticated falls back to node name", func(t *testing.T) {
		t.Parallel()
		src := &datastore.AudioSourceSummary{ID: 7, DisplayName: "", NodeName: "node-a", SourceType: "rtsp"}
		assert.Equal(t, "node-a", analyticsSourceLabel(src, true))
	})

	t.Run("authenticated falls back to id label", func(t *testing.T) {
		t.Parallel()
		src := &datastore.AudioSourceSummary{ID: 7, DisplayName: "", NodeName: "", SourceType: "rtsp"}
		assert.Equal(t, "source-7", analyticsSourceLabel(src, true))
	})

	t.Run("unauthenticated anonymizes regardless of display name", func(t *testing.T) {
		t.Parallel()
		src := &datastore.AudioSourceSummary{ID: 7, DisplayName: "Backyard", NodeName: "node-a", SourceType: "rtsp"}
		assert.Equal(t, "camera-7", analyticsSourceLabel(src, false))
	})
}
