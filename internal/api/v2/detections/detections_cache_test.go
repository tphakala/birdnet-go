package detections

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"net/url"
	"sync"
	"testing"

	"github.com/labstack/echo/v4"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/require"

	"github.com/tphakala/birdnet-go/internal/api/v2/apicore"
	"github.com/tphakala/birdnet-go/internal/datastore"
	"github.com/tphakala/birdnet-go/internal/datastore/mocks"
)

// cacheTestNotes is the page every cache test serves from the mock datastore.
func cacheTestNotes() []datastore.Note {
	return []datastore.Note{{
		ID: 1, Date: "2025-03-07", Time: "08:15:00", Source: testRealtimeSource(),
		SpeciesCode: "AMCRO", ScientificName: "Corvus brachyrhynchos", CommonName: "American Crow",
		Confidence: 0.95,
	}}
}

// getDetectionsBody runs GET /detections with the given query through the
// handler and returns the status and raw body.
func getDetectionsBody(t *testing.T, e *echo.Echo, h *Handler, query url.Values) (status int, body string) {
	t.Helper()
	req := httptest.NewRequest(http.MethodGet, "/api/v2/detections?"+query.Encode(), http.NoBody)
	rec := httptest.NewRecorder()
	c := e.NewContext(req, rec)
	c.SetPath("/api/v2/detections")
	require.NoError(t, h.GetDetections(c))
	return rec.Code, rec.Body.String()
}

// TestGetDetections_CacheHitPerQueryType pins that every query type is served
// from the cache on the second identical request: one datastore round trip, a
// byte-identical body, and exactly one miss and one hit.
func TestGetDetections_CacheHitPerQueryType(t *testing.T) {
	notes := cacheTestNotes()
	tests := []struct {
		name  string
		query url.Values
		setup func(m *mocks.MockInterface)
	}{
		{
			name:  "all",
			query: url.Values{"queryType": {"all"}, "numResults": {"10"}},
			setup: func(m *mocks.MockInterface) {
				m.EXPECT().SearchNotes("", false, 10, 0).Return(notes, int64(1), nil).Once()
			},
		},
		{
			name:  "hourly",
			query: url.Values{"queryType": {"hourly"}, "date": {"2025-03-07"}, "hour": {"08"}, "duration": {"1"}, "numResults": {"10"}},
			setup: func(m *mocks.MockInterface) {
				m.EXPECT().GetHourlyDetections("2025-03-07", "08", 1, 10, 0).Return(notes, nil).Once()
				m.EXPECT().CountHourlyDetections("2025-03-07", "08", 1).Return(int64(1), nil).Once()
			},
		},
		{
			name:  "species",
			query: url.Values{"queryType": {"species"}, "species": {"American Crow"}, "date": {"2025-03-07"}, "numResults": {"10"}},
			setup: func(m *mocks.MockInterface) {
				m.EXPECT().SpeciesDetections("American Crow", "2025-03-07", "", 1, false, 10, 0).Return(notes, nil).Once()
				m.EXPECT().CountSpeciesDetections("American Crow", "2025-03-07", "", 1).Return(int64(1), nil).Once()
			},
		},
		{
			name:  "search",
			query: url.Values{"queryType": {"search"}, "search": {"Crow"}, "numResults": {"10"}},
			setup: func(m *mocks.MockInterface) {
				m.EXPECT().SearchNotes("Crow", false, 10, 0).Return(notes, int64(1), nil).Once()
			},
		},
		{
			name:  "advanced",
			query: url.Values{"queryType": {"search"}, "search": {"Crow"}, "confidence": {">0.5"}, "numResults": {"10"}},
			setup: func(m *mocks.MockInterface) {
				m.EXPECT().SearchNotesAdvanced(mock.Anything).Return(notes, int64(1), nil).Once()
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			e, mockDS, h := setupTestEnvironment(t)
			tt.setup(mockDS)

			status1, body1 := getDetectionsBody(t, e, h, tt.query)
			status2, body2 := getDetectionsBody(t, e, h, tt.query)

			require.Equal(t, http.StatusOK, status1)
			assert.Equal(t, status1, status2)
			assert.Equal(t, body1, body2, "hit and miss responses are byte-identical")
			stats := h.DetectionCache.Stats()
			assert.Equal(t, uint64(1), stats.Misses)
			assert.Equal(t, uint64(1), stats.Hits)
			assert.Equal(t, 1, h.DetectionCache.Len())
		})
	}
}

// TestGetDetections_AllAndEmptySearchShareOnePage pins the folded getAllDetections:
// the default (all) type and an empty search run the identical datastore call, so
// they share one cache entry.
func TestGetDetections_AllAndEmptySearchShareOnePage(t *testing.T) {
	e, mockDS, h := setupTestEnvironment(t)
	mockDS.EXPECT().SearchNotes("", false, 10, 0).Return(cacheTestNotes(), int64(1), nil).Once()

	_, allBody := getDetectionsBody(t, e, h, url.Values{"queryType": {"all"}, "numResults": {"10"}})
	_, searchBody := getDetectionsBody(t, e, h, url.Values{"queryType": {"search"}, "numResults": {"10"}})

	assert.Equal(t, allBody, searchBody)
	assert.Equal(t, uint64(1), h.DetectionCache.Stats().Hits)
}

// TestGetDetections_AdvancedKeyIgnoresQueryType pins that requests differing only in
// queryType, which the advanced filters never read, share one datastore call.
func TestGetDetections_AdvancedKeyIgnoresQueryType(t *testing.T) {
	e, mockDS, h := setupTestEnvironment(t)
	mockDS.EXPECT().SearchNotesAdvanced(mock.Anything).Return(cacheTestNotes(), int64(1), nil).Once()

	for _, queryType := range []string{"search", "all"} {
		status, _ := getDetectionsBody(t, e, h, url.Values{
			"queryType": {queryType}, "confidence": {">0.5"}, "numResults": {"10"},
		})
		require.Equal(t, http.StatusOK, status, queryType)
	}
	assert.Equal(t, uint64(1), h.DetectionCache.Stats().Hits)
}

// TestGetDetections_ConcurrentIdenticalRequestsQueryOnce pins that simultaneous
// identical requests share one datastore load.
func TestGetDetections_ConcurrentIdenticalRequestsQueryOnce(t *testing.T) {
	const callers = 20
	e, mockDS, h := setupTestEnvironment(t)
	mockDS.EXPECT().SearchNotes("Crow", false, 10, 0).Return(cacheTestNotes(), int64(1), nil).Once()

	var wg sync.WaitGroup
	statuses := make([]int, callers)
	for i := range callers {
		wg.Go(func() {
			req := httptest.NewRequest(http.MethodGet, "/api/v2/detections?queryType=search&search=Crow&numResults=10", http.NoBody)
			rec := httptest.NewRecorder()
			c := e.NewContext(req, rec)
			c.SetPath("/api/v2/detections")
			assert.NoError(t, h.GetDetections(c))
			statuses[i] = rec.Code
		})
	}
	wg.Wait()

	for i, status := range statuses {
		assert.Equal(t, http.StatusOK, status, "caller %d", i)
	}
}

// TestBatchResolveDetections_ServedFromCache pins that batch resolve uses the same
// cached pages as the list endpoint.
func TestBatchResolveDetections_ServedFromCache(t *testing.T) {
	e, mockDS, h := setupTestEnvironment(t)
	mockDS.EXPECT().SearchNotes("Crow", false, maxBatchSize+1, 0).Return(cacheTestNotes(), int64(1), nil).Once()

	resolve := func() string {
		body, err := json.Marshal(BatchResolveRequest{QueryType: "search", Search: "Crow"})
		require.NoError(t, err)
		req := httptest.NewRequest(http.MethodPost, "/api/v2/detections/batch/resolve", bytes.NewReader(body))
		req.Header.Set(echo.HeaderContentType, echo.MIMEApplicationJSON)
		rec := httptest.NewRecorder()
		require.NoError(t, h.BatchResolveDetections(e.NewContext(req, rec)))
		require.Equal(t, http.StatusOK, rec.Code)
		return rec.Body.String()
	}

	first := resolve()
	second := resolve()
	assert.Equal(t, first, second)
	stats := h.DetectionCache.Stats()
	assert.Equal(t, uint64(1), stats.Misses)
	assert.Equal(t, uint64(1), stats.Hits)
}

// TestDetectionCache_InvalidatedByWriteHandlers pins that every write handler
// that calls invalidateDetectionCache empties the cache, so the next list
// request queries the datastore again.
func TestDetectionCache_InvalidatedByWriteHandlers(t *testing.T) {
	tests := []struct {
		name  string
		setup func(m *mocks.MockInterface)
		call  func(t *testing.T, e *echo.Echo, h *Handler)
	}{
		{
			name: "DeleteDetection",
			setup: func(m *mocks.MockInterface) {
				m.EXPECT().Get("1").Return(datastore.Note{ID: 1}, nil).Once()
				m.EXPECT().Delete("1").Return(nil).Once()
			},
			call: func(t *testing.T, e *echo.Echo, h *Handler) {
				t.Helper()
				rec := httptest.NewRecorder()
				c := e.NewContext(httptest.NewRequest(http.MethodDelete, "/api/v2/detections/1", http.NoBody), rec)
				c.SetParamNames("id")
				c.SetParamValues("1")
				require.NoError(t, h.DeleteDetection(c))
				require.Equal(t, http.StatusNoContent, rec.Code)
			},
		},
		{
			name: "ReviewDetection",
			setup: func(m *mocks.MockInterface) {
				m.EXPECT().Get("1").Return(datastore.Note{ID: 1}, nil).Once()
				m.EXPECT().IsNoteLocked("1").Return(false, nil).Once()
				m.EXPECT().SaveNoteReview(mock.Anything).Return(nil).Once()
			},
			call: func(t *testing.T, e *echo.Echo, h *Handler) {
				t.Helper()
				req := httptest.NewRequest(http.MethodPost, "/api/v2/detections/1/review", bytes.NewReader([]byte(`{"verified":"correct"}`)))
				req.Header.Set(echo.HeaderContentType, echo.MIMEApplicationJSON)
				rec := httptest.NewRecorder()
				c := e.NewContext(req, rec)
				c.SetParamNames("id")
				c.SetParamValues("1")
				require.NoError(t, h.ReviewDetection(c))
				require.Equal(t, http.StatusOK, rec.Code)
			},
		},
		{
			name: "LockDetection",
			setup: func(m *mocks.MockInterface) {
				m.EXPECT().Get("1").Return(datastore.Note{ID: 1}, nil).Once()
				m.EXPECT().IsNoteLocked("1").Return(false, nil).Once()
				m.EXPECT().LockNote("1").Return(nil).Once()
			},
			call: func(t *testing.T, e *echo.Echo, h *Handler) {
				t.Helper()
				req := httptest.NewRequest(http.MethodPost, "/api/v2/detections/1/lock", bytes.NewReader([]byte(`{"locked":true}`)))
				req.Header.Set(echo.HeaderContentType, echo.MIMEApplicationJSON)
				rec := httptest.NewRecorder()
				c := e.NewContext(req, rec)
				c.SetParamNames("id")
				c.SetParamValues("1")
				require.NoError(t, h.LockDetection(c))
				require.Equal(t, http.StatusNoContent, rec.Code)
			},
		},
		{
			name: "BatchDeleteDetections",
			setup: func(m *mocks.MockInterface) {
				m.EXPECT().Get("1").Return(datastore.Note{ID: 1}, nil).Once()
				m.EXPECT().Delete("1").Return(nil).Once()
			},
			call: func(t *testing.T, e *echo.Echo, h *Handler) {
				t.Helper()
				postBatch(t, e, h.BatchDeleteDetections, BatchIDsRequest{IDs: []string{"1"}})
			},
		},
		{
			name: "BatchReviewDetections",
			setup: func(m *mocks.MockInterface) {
				m.EXPECT().Get("1").Return(datastore.Note{ID: 1}, nil).Once()
				m.EXPECT().SaveNoteReview(mock.Anything).Return(nil).Once()
			},
			call: func(t *testing.T, e *echo.Echo, h *Handler) {
				t.Helper()
				postBatch(t, e, h.BatchReviewDetections, BatchReviewRequest{IDs: []string{"1"}, Verified: "correct"})
			},
		},
		{
			name: "BatchLockDetections",
			setup: func(m *mocks.MockInterface) {
				m.EXPECT().Get("1").Return(datastore.Note{ID: 1}, nil).Once()
				m.EXPECT().LockNote("1").Return(nil).Once()
			},
			call: func(t *testing.T, e *echo.Echo, h *Handler) {
				t.Helper()
				postBatch(t, e, h.BatchLockDetections, BatchLockRequest{IDs: []string{"1"}, Locked: true})
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			e, mockDS, h := setupTestEnvironment(t)
			// Two list queries are expected: the one that primes the cache and
			// the one after the write.
			mockDS.EXPECT().SearchNotes("Crow", false, 10, 0).Return(cacheTestNotes(), int64(1), nil).Twice()
			tt.setup(mockDS)
			query := url.Values{"queryType": {"search"}, "search": {"Crow"}, "numResults": {"10"}}

			getDetectionsBody(t, e, h, query)
			require.Equal(t, 1, h.DetectionCache.Len(), "the first list request primed the cache")

			tt.call(t, e, h)
			assert.Equal(t, 0, h.DetectionCache.Len(), "the write handler invalidated the cache")

			getDetectionsBody(t, e, h, query)
		})
	}
}

// postBatch posts body to a batch handler and requires a 200 response.
func postBatch(t *testing.T, e *echo.Echo, handler func(echo.Context) error, body any) {
	t.Helper()
	raw, err := json.Marshal(body)
	require.NoError(t, err)
	req := httptest.NewRequest(http.MethodPost, "/api/v2/detections/batch", bytes.NewReader(raw))
	req.Header.Set(echo.HeaderContentType, echo.MIMEApplicationJSON)
	rec := httptest.NewRecorder()
	require.NoError(t, handler(e.NewContext(req, rec)))
	require.Equal(t, http.StatusOK, rec.Code)
}

// TestGetDetections_ClientDisconnectIsNotAnError pins that a request whose context
// ended before the page loaded gets the client-closed status without querying the
// datastore (the strict mock fails on any unexpected call) and leaves no cache entry.
func TestGetDetections_ClientDisconnectIsNotAnError(t *testing.T) {
	e, _, h := setupTestEnvironment(t)
	ctx, cancel := context.WithCancel(t.Context())
	cancel()

	req := httptest.NewRequest(http.MethodGet, "/api/v2/detections?queryType=search&search=Crow", http.NoBody).WithContext(ctx)
	rec := httptest.NewRecorder()
	c := e.NewContext(req, rec)
	c.SetPath("/api/v2/detections")

	require.NoError(t, h.GetDetections(c))
	assert.Equal(t, apicore.StatusClientClosedRequest, rec.Code)
	assert.Equal(t, 0, h.DetectionCache.Len())
}

// TestGetDetections_LoaderErrorIsNotCached pins that a datastore failure still
// returns 500 and that the next request queries again.
func TestGetDetections_LoaderErrorIsNotCached(t *testing.T) {
	e, mockDS, h := setupTestEnvironment(t)
	mockDS.EXPECT().SearchNotes("Crow", false, 10, 0).Return(nil, int64(0), assert.AnError).Once()
	mockDS.EXPECT().SearchNotes("Crow", false, 10, 0).Return(cacheTestNotes(), int64(1), nil).Once()
	query := url.Values{"queryType": {"search"}, "search": {"Crow"}, "numResults": {"10"}}

	status, _ := getDetectionsBody(t, e, h, query)
	assert.Equal(t, http.StatusInternalServerError, status)
	status, _ = getDetectionsBody(t, e, h, query)
	assert.Equal(t, http.StatusOK, status)
}
