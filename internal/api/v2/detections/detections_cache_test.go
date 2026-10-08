package detections

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"sync"
	"testing"
	"time"

	"github.com/labstack/echo/v4"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/require"

	"github.com/tphakala/birdnet-go/internal/api/v2/apicore"
	"github.com/tphakala/birdnet-go/internal/datastore"
	"github.com/tphakala/birdnet-go/internal/datastore/mocks"
	"github.com/tphakala/birdnet-go/internal/logger"
	"github.com/tphakala/birdnet-go/internal/ttlcache"
)

// concurrencyWaitTimeout bounds how long a test waits for its concurrent
// callers to reach the expected state. It is generous so a loaded CI runner
// under -race does not fail the test; a correct run finishes in milliseconds.
const concurrencyWaitTimeout = time.Minute

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
// identical requests share one datastore load. The datastore call blocks until
// every caller has missed the cache, so the requests are forced to overlap and
// a get-then-set cache that only works for sequential callers fails.
func TestGetDetections_ConcurrentIdenticalRequestsQueryOnce(t *testing.T) {
	const callers = 20
	e, mockDS, h := setupTestEnvironment(t)

	release := make(chan struct{})
	var releaseOnce sync.Once
	t.Cleanup(func() { releaseOnce.Do(func() { close(release) }) })
	mockDS.EXPECT().SearchNotes("Crow", false, 10, 0).RunAndReturn(
		func(string, bool, int, int) ([]datastore.Note, int64, error) {
			<-release
			return cacheTestNotes(), 1, nil
		}).Once()

	var wg sync.WaitGroup
	statuses := make([]int, callers)
	errs := make([]error, callers)
	for i := range callers {
		wg.Go(func() {
			req := httptest.NewRequest(http.MethodGet, "/api/v2/detections?queryType=search&search=Crow&numResults=10", http.NoBody)
			rec := httptest.NewRecorder()
			c := e.NewContext(req, rec)
			c.SetPath("/api/v2/detections")
			errs[i] = h.GetDetections(c)
			statuses[i] = rec.Code
		})
	}
	require.Eventually(t, func() bool { return h.DetectionCache.Stats().Misses == callers },
		concurrencyWaitTimeout, time.Millisecond, "every caller reaches the cache before the load finishes")
	releaseOnce.Do(func() { close(release) })
	wg.Wait()

	for i := range callers {
		require.NoError(t, errs[i], "caller %d", i)
		assert.Equal(t, http.StatusOK, statuses[i], "caller %d", i)
	}
}

// TestBatchResolveDetections_BypassesCache pins that batch resolve, which asks
// for maxBatchSize+1 notes, is larger than the cacheable page bound and so
// always reads the datastore: a batch selection must see current rows.
func TestBatchResolveDetections_BypassesCache(t *testing.T) {
	e, mockDS, h := setupTestEnvironment(t)
	mockDS.EXPECT().SearchNotes("Crow", false, maxBatchSize+1, 0).Return(cacheTestNotes(), int64(1), nil).Twice()

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
	assert.Equal(t, 0, h.DetectionCache.Len())
	assert.Equal(t, ttlcache.Stats{}, h.DetectionCache.Stats())
}

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

// levelRecorder records the debug and error messages of a handler logger.
// Everything else goes to the embedded discarding logger, so a call the test
// does not expect (Module, With, Log) cannot hit a nil interface.
type levelRecorder struct {
	logger.Logger
	mu          sync.Mutex
	debugs      []string
	debugFields map[string][]logger.Field
	errs        []string
}

func newLevelRecorder() *levelRecorder {
	return &levelRecorder{Logger: logger.NewSlogLogger(io.Discard, logger.LogLevelError, time.UTC)}
}

func (r *levelRecorder) Trace(string, ...logger.Field) {}
func (r *levelRecorder) Info(string, ...logger.Field)  {}
func (r *levelRecorder) Warn(string, ...logger.Field)  {}

func (r *levelRecorder) Debug(msg string, fields ...logger.Field) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.debugs = append(r.debugs, msg)
	if r.debugFields == nil {
		r.debugFields = make(map[string][]logger.Field)
	}
	r.debugFields[msg] = fields
}

// debugField returns the value of key in the fields of the last debug line
// logged with msg.
func (r *levelRecorder) debugField(msg, key string) (any, bool) {
	r.mu.Lock()
	defer r.mu.Unlock()
	for _, f := range r.debugFields[msg] {
		if f.Key == key {
			return f.Value, true
		}
	}
	return nil, false
}

func (r *levelRecorder) Error(msg string, _ ...logger.Field) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.errs = append(r.errs, msg)
}

// TestGetDetections_RequestEnded pins how a list request whose context is done
// is answered and logged: client disconnect as 499 and deadline as 408, both at
// debug with nothing at error and no datastore query (the strict mock fails on
// any unexpected call); a datastore error that only wraps a context error while
// the request is still live keeps the 500 response and its error log.
func TestGetDetections_RequestEnded(t *testing.T) {
	expired, cancelExpired := context.WithDeadline(t.Context(), time.Now().Add(-time.Second))
	t.Cleanup(cancelExpired)
	canceled, cancel := context.WithCancel(t.Context())
	cancel()

	tests := []struct {
		name       string
		ctx        context.Context
		setup      func(m *mocks.MockInterface)
		wantStatus int
		wantDebug  bool
	}{
		{"client disconnect", canceled, func(*mocks.MockInterface) {}, apicore.StatusClientClosedRequest, true},
		{"deadline", expired, func(*mocks.MockInterface) {}, http.StatusRequestTimeout, true},
		{
			"live request with a wrapped context error", t.Context(),
			func(m *mocks.MockInterface) {
				m.EXPECT().SearchNotes("Crow", false, 100, 0).Return(nil, int64(0), context.Canceled).Once()
			},
			http.StatusInternalServerError, false,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			e, mockDS, h := setupTestEnvironment(t)
			tt.setup(mockDS)
			rec := newLevelRecorder()
			h.APILogger = rec

			req := httptest.NewRequest(http.MethodGet, "/api/v2/detections?queryType=search&search=Crow", http.NoBody).WithContext(tt.ctx)
			resp := httptest.NewRecorder()
			c := e.NewContext(req, resp)
			c.SetPath("/api/v2/detections")

			require.NoError(t, h.GetDetections(c))
			assert.Equal(t, tt.wantStatus, resp.Code)
			assert.Equal(t, 0, h.DetectionCache.Len())
			if tt.wantDebug {
				assert.NotEmpty(t, rec.debugs, "logged at debug")
				assert.Empty(t, rec.errs, "nothing at error")
			} else {
				assert.Empty(t, rec.debugs)
				assert.NotEmpty(t, rec.errs, "a live request keeps its error log")
			}
		})
	}
}

// TestBatchResolveDetections_ClientDisconnect pins that batch resolve answers a
// disconnected client like the list endpoint: 499, debug log, no error log.
func TestBatchResolveDetections_ClientDisconnect(t *testing.T) {
	e, _, h := setupTestEnvironment(t)
	rec := newLevelRecorder()
	h.APILogger = rec
	ctx, cancel := context.WithCancel(t.Context())
	cancel()

	body, err := json.Marshal(BatchResolveRequest{QueryType: "search", Search: "Crow"})
	require.NoError(t, err)
	req := httptest.NewRequest(http.MethodPost, "/api/v2/detections/batch/resolve", bytes.NewReader(body)).WithContext(ctx)
	req.Header.Set(echo.HeaderContentType, echo.MIMEApplicationJSON)
	resp := httptest.NewRecorder()

	require.NoError(t, h.BatchResolveDetections(e.NewContext(req, resp)))
	assert.Equal(t, apicore.StatusClientClosedRequest, resp.Code)
	assert.NotEmpty(t, rec.debugs)
	assert.Empty(t, rec.errs)
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

// TestGetDetections_LoaderPanicIsLogged pins that a datastore panic inside a
// cached load becomes a 500 with the panic logged at error level, since the
// cache recovers it before Echo's recover middleware could see it.
func TestGetDetections_LoaderPanicIsLogged(t *testing.T) {
	e, mockDS, h := setupTestEnvironment(t)
	rec := newLevelRecorder()
	h.APILogger = rec
	mockDS.EXPECT().SearchNotes("Crow", false, 10, 0).Run(func(string, bool, int, int) {
		panic("datastore exploded")
	}).Once()
	query := url.Values{"queryType": {"search"}, "search": {"Crow"}, "numResults": {"10"}}

	status, _ := getDetectionsBody(t, e, h, query)
	assert.Equal(t, http.StatusInternalServerError, status)
	rec.mu.Lock()
	defer rec.mu.Unlock()
	assert.Contains(t, rec.errs, "Detection page loader panicked")
}

// TestGetDetections_AllQueryLogsAllDetections pins that the default query type
// keeps its own log messages after sharing the search loader.
func TestGetDetections_AllQueryLogsAllDetections(t *testing.T) {
	e, mockDS, h := setupTestEnvironment(t)
	rec := newLevelRecorder()
	h.APILogger = rec
	mockDS.EXPECT().SearchNotes("", false, 10, 0).Return(nil, int64(0), assert.AnError).Once()

	status, _ := getDetectionsBody(t, e, h, url.Values{"numResults": {"10"}})
	assert.Equal(t, http.StatusInternalServerError, status)
	rec.mu.Lock()
	defer rec.mu.Unlock()
	assert.Contains(t, rec.errs, "Failed to get all detections")
	assert.NotContains(t, rec.errs, "Failed to search notes")
}

// TestGetDetections_RequestEndedLogsCorrelationID pins that the debug line for a
// request that ended carries the correlation id the client received, so the
// response can be traced back to the log.
func TestGetDetections_RequestEndedLogsCorrelationID(t *testing.T) {
	canceled, cancel := context.WithCancel(t.Context())
	cancel()
	e, _, h := setupTestEnvironment(t)
	rec := newLevelRecorder()
	h.APILogger = rec

	req := httptest.NewRequest(http.MethodGet, "/api/v2/detections?queryType=search&search=Crow", http.NoBody).WithContext(canceled)
	resp := httptest.NewRecorder()
	c := e.NewContext(req, resp)
	c.SetPath("/api/v2/detections")
	require.NoError(t, h.GetDetections(c))
	require.Equal(t, apicore.StatusClientClosedRequest, resp.Code)

	var body apicore.ErrorResponse
	require.NoError(t, json.Unmarshal(resp.Body.Bytes(), &body))
	require.NotEmpty(t, body.CorrelationID)

	const msg = "Detections request ended before completion"
	id, ok := rec.debugField(msg, "correlation_id")
	require.True(t, ok, "the request-ended line logs correlation_id")
	assert.Equal(t, body.CorrelationID, id)
	method, ok := rec.debugField(msg, "method")
	require.True(t, ok, "the request-ended line logs method")
	assert.Equal(t, http.MethodGet, method)
}
