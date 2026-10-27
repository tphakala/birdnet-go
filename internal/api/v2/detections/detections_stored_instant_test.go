package detections

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"net/url"
	"testing"
	"time"

	"github.com/labstack/echo/v4"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"

	"github.com/tphakala/birdnet-go/internal/api/v2/apicore"
	"github.com/tphakala/birdnet-go/internal/datastore"
	"github.com/tphakala/birdnet-go/internal/datastore/mocks"
	"github.com/tphakala/birdnet-go/internal/datastore/v2/repository"
	"github.com/tphakala/birdnet-go/internal/detection"
)

// repeatedHourZone names a zone and the two UTC instants at which its clock reads
// the same wall time at the end of DST (first the DST occurrence, then standard
// time), with the RFC3339 offset each occurrence must be shown with.
type repeatedHourZone struct {
	name          string
	zone          string
	first, second time.Time
	firstOffset   string
	secondOffset  string
}

// repeatedHourZones are the zones the repeated-hour tests cover. Lord Howe shifts
// by 30 minutes at its transition (see the weather handler test with the same instants), so an hour-only match cannot pass.
func repeatedHourZones() []repeatedHourZone {
	return []repeatedHourZone{
		{
			name: "New York", zone: "America/New_York",
			first:       time.Date(2026, time.November, 1, 5, 30, 0, 0, time.UTC),
			second:      time.Date(2026, time.November, 1, 6, 30, 0, 0, time.UTC),
			firstOffset: "-04:00", secondOffset: "-05:00",
		},
		{
			name: "Helsinki", zone: "Europe/Helsinki",
			first:       time.Date(2026, time.October, 25, 0, 30, 0, 0, time.UTC),
			second:      time.Date(2026, time.October, 25, 1, 30, 0, 0, time.UTC),
			firstOffset: "+03:00", secondOffset: "+02:00",
		},
		{
			name: "Lord Howe", zone: "Australia/Lord_Howe",
			first:       time.Date(2026, time.April, 4, 14, 50, 0, 0, time.UTC),
			second:      time.Date(2026, time.April, 4, 15, 20, 0, 0, time.UTC),
			firstOffset: "+11:00", secondOffset: "+10:30",
		},
	}
}

// results returns one Result per occurrence in the zone, as the v2
// repository reads them: the stored instant expressed in the station zone.
func (z *repeatedHourZone) results(t *testing.T) []*detection.Result {
	t.Helper()
	loc, err := time.LoadLocation(z.zone)
	require.NoError(t, err)
	mk := func(id uint, at time.Time) *detection.Result {
		return &detection.Result{
			ID: id, Timestamp: at.In(loc),
			AudioSource: detection.AudioSource{ID: "realtime", SafeString: "realtime", DisplayName: "realtime"},
			Species:     detection.Species{Code: "PARMAJ", ScientificName: "Parus major", CommonName: "Great Tit"},
			Confidence:  0.9,
		}
	}
	return []*detection.Result{mk(1, z.first), mk(2, z.second)}
}

// detectionsRepoHandler builds a handler whose detection reads go to a strict
// repository mock. The datastore mock only serves the weather reads.
func detectionsRepoHandler(t *testing.T) (*echo.Echo, *mocks.MockInterface, *mocks.MockDetectionRepository, *Handler) {
	t.Helper()
	e, mockDS, h := setupTestEnvironment(t)
	repo := mocks.NewMockDetectionRepository(t)
	h.Repo = repo
	return e, mockDS, repo, h
}

// listBody is the paginated list response the tests decode.
type listBody struct {
	Data []DetectionResponse `json:"data"`
}

// TestDetectionResponses_RepeatedHourTimestamp verifies that the two detections of a
// repeated DST hour are shown with their own UTC offsets and their own hourly
// weather. Rebuilding the instant from the offset-free date and time text resolves one
// occurrence of each pair to the other.
func TestDetectionResponses_RepeatedHourTimestamp(t *testing.T) {
	t.Attr("component", "detections")
	t.Attr("feature", "repeated-hour")
	const (
		firstTemperature  = 11.0
		secondTemperature = 22.0
	)

	for _, z := range repeatedHourZones() {
		t.Run(z.name, func(t *testing.T) {
			e, mockDS, repo, h := detectionsRepoHandler(t)
			results := z.results(t)
			date := results[0].Date()
			hour := results[0].Timestamp.Format("15")

			repo.EXPECT().GetHourly(mock.Anything, date, hour, 1, 10, 0).Return(results, int64(2), nil).Once()
			mockDS.EXPECT().GetHourlyWeather(date).Return([]datastore.HourlyWeather{
				{Time: z.first, Temperature: firstTemperature, WeatherIcon: "01d"},
				{Time: z.second, Temperature: secondTemperature, WeatherIcon: "01d"},
			}, nil)

			status, body := getDetectionsBody(t, e, h, url.Values{
				"queryType": {"hourly"}, "date": {date}, "hour": {hour}, "duration": {"1"},
				"numResults": {"10"}, "includeWeather": {"true"},
			})
			require.Equal(t, http.StatusOK, status)
			var resp listBody
			require.NoError(t, json.Unmarshal([]byte(body), &resp))
			require.Len(t, resp.Data, 2)

			assert.Equal(t, results[0].Timestamp.Format(time.RFC3339), resp.Data[0].Timestamp)
			assert.Equal(t, results[1].Timestamp.Format(time.RFC3339), resp.Data[1].Timestamp)
			assert.Contains(t, resp.Data[0].Timestamp, z.firstOffset)
			assert.Contains(t, resp.Data[1].Timestamp, z.secondOffset)
			assert.Equal(t, resp.Data[0].Date, resp.Data[1].Date, "the display date stays the same wall text")
			assert.Equal(t, resp.Data[0].Time, resp.Data[1].Time, "the display time stays the same wall text")

			require.NotNil(t, resp.Data[0].Weather)
			require.NotNil(t, resp.Data[1].Weather)
			assert.InDelta(t, firstTemperature, resp.Data[0].Weather.Temperature, 0.01)
			assert.InDelta(t, secondTemperature, resp.Data[1].Weather.Temperature, 0.01,
				"the second occurrence must match the weather row of its own instant")
		})
	}
}

// TestDetectionEndpoints_ReadThroughRepository verifies that every converted
// endpoint reads through the detection repository and shows the stored instant of
// the second occurrence of a repeated hour (New York, UTC-05:00).
func TestDetectionEndpoints_ReadThroughRepository(t *testing.T) {
	t.Attr("component", "detections")
	t.Attr("feature", "repeated-hour")

	z := repeatedHourZones()[0]
	second := z.results(t)[1]
	date, hour := second.Date(), second.Timestamp.Format("15")

	// positiveLimit pins that a search always carries a positive limit: the
	// repository does not cap a limit of zero or less.
	advanced := func(f *datastore.AdvancedSearchFilters) bool { return f.Limit > 0 }
	searchDefault := func(f *datastore.AdvancedSearchFilters) bool {
		return advanced(f) && f.SortBy == datastore.SortBySearchDefault
	}

	listCases := []struct {
		name   string
		query  url.Values
		expect func(repo *mocks.MockDetectionRepository)
	}{
		{
			name:  "hourly",
			query: url.Values{"queryType": {"hourly"}, "date": {date}, "hour": {hour}, "duration": {"1"}, "numResults": {"10"}},
			expect: func(repo *mocks.MockDetectionRepository) {
				repo.EXPECT().GetHourly(mock.Anything, date, hour, 1, 10, 0).Return([]*detection.Result{second}, int64(1), nil)
			},
		},
		{
			name:  "species",
			query: url.Values{"queryType": {"species"}, "species": {"Parus major"}, "date": {date}, "hour": {hour}, "duration": {"2"}, "numResults": {"10"}},
			expect: func(repo *mocks.MockDetectionRepository) {
				repo.EXPECT().GetBySpecies(mock.Anything, "Parus major", date, hour, 2, 10, 0).Return([]*detection.Result{second}, int64(1), nil)
			},
		},
		{
			name:  "search",
			query: url.Values{"queryType": {"search"}, "search": {"Parus"}, "numResults": {"10"}},
			expect: func(repo *mocks.MockDetectionRepository) {
				repo.EXPECT().SearchAdvanced(mock.Anything, mock.MatchedBy(func(f *datastore.AdvancedSearchFilters) bool {
					return searchDefault(f) && f.TextQuery == "Parus"
				})).Return([]*detection.Result{second}, int64(1), nil)
			},
		},
		{
			name:  "all",
			query: url.Values{"queryType": {"all"}, "numResults": {"10"}},
			expect: func(repo *mocks.MockDetectionRepository) {
				repo.EXPECT().SearchAdvanced(mock.Anything, mock.MatchedBy(func(f *datastore.AdvancedSearchFilters) bool {
					return searchDefault(f) && f.TextQuery == "" && f.Limit == 10
				})).Return([]*detection.Result{second}, int64(1), nil)
			},
		},
		{
			name:  "advanced",
			query: url.Values{"queryType": {"search"}, "search": {"Parus"}, "confidence": {">0.5"}, "numResults": {"10"}},
			expect: func(repo *mocks.MockDetectionRepository) {
				repo.EXPECT().SearchAdvanced(mock.Anything, mock.MatchedBy(func(f *datastore.AdvancedSearchFilters) bool {
					return advanced(f) && f.TextQuery == "Parus" && f.Confidence != nil && f.SortBy == ""
				})).Return([]*detection.Result{second}, int64(1), nil)
			},
		},
	}
	for _, tc := range listCases {
		t.Run(tc.name, func(t *testing.T) {
			e, _, repo, h := detectionsRepoHandler(t)
			tc.expect(repo)
			status, body := getDetectionsBody(t, e, h, tc.query)
			require.Equal(t, http.StatusOK, status)
			var resp listBody
			require.NoError(t, json.Unmarshal([]byte(body), &resp))
			require.Len(t, resp.Data, 1)
			assert.Equal(t, "2026-11-01T01:30:00-05:00", resp.Data[0].Timestamp)
			assert.Equal(t, date, resp.Data[0].Date)
			assert.Equal(t, "01:30:00", resp.Data[0].Time)
		})
	}

	t.Run("detail", func(t *testing.T) {
		e, mockDS, repo, h := detectionsRepoHandler(t)
		repo.EXPECT().Get(mock.Anything, "2").Return(second, nil)
		mockDS.EXPECT().GetHourlyWeather(date).Return(nil, gorm.ErrRecordNotFound).Maybe()
		req := httptest.NewRequest(http.MethodGet, "/api/v2/detections/2", http.NoBody)
		rec := httptest.NewRecorder()
		c := e.NewContext(req, rec)
		c.SetPath("/api/v2/detections/:id")
		c.SetParamNames("id")
		c.SetParamValues("2")
		require.NoError(t, h.GetDetection(c))
		require.Equal(t, http.StatusOK, rec.Code)
		var resp DetectionResponse
		require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &resp))
		assert.Equal(t, "2026-11-01T01:30:00-05:00", resp.Timestamp)
	})

	t.Run("recent", func(t *testing.T) {
		e, _, repo, h := detectionsRepoHandler(t)
		repo.EXPECT().GetRecent(mock.Anything, 5).Return([]*detection.Result{second}, nil)
		req := httptest.NewRequest(http.MethodGet, "/api/v2/detections/recent?limit=5", http.NoBody)
		rec := httptest.NewRecorder()
		c := e.NewContext(req, rec)
		c.SetPath("/api/v2/detections/recent")
		require.NoError(t, h.GetRecentDetections(c))
		require.Equal(t, http.StatusOK, rec.Code)
		var resp []DetectionResponse
		require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &resp))
		require.Len(t, resp, 1)
		assert.Equal(t, "2026-11-01T01:30:00-05:00", resp[0].Timestamp)
	})
}

// TestGetDetection_NotFound verifies that a detection missing from either store
// answers 404: the legacy bridge reports gorm.ErrRecordNotFound and the v2
// repository reports repository.ErrDetectionNotFound.
func TestGetDetection_NotFound(t *testing.T) {
	for name, repoErr := range map[string]error{
		"legacy bridge": gorm.ErrRecordNotFound,
		"v2 repository": repository.ErrDetectionNotFound,
	} {
		t.Run(name, func(t *testing.T) {
			e, _, repo, h := detectionsRepoHandler(t)
			repo.EXPECT().Get(mock.Anything, "404").Return(nil, repoErr)
			req := httptest.NewRequest(http.MethodGet, "/api/v2/detections/404", http.NoBody)
			rec := httptest.NewRecorder()
			c := e.NewContext(req, rec)
			c.SetPath("/api/v2/detections/:id")
			c.SetParamNames("id")
			c.SetParamValues("404")
			require.NoError(t, h.GetDetection(c))
			assert.Equal(t, http.StatusNotFound, rec.Code)
		})
	}
}

// TestGetDetection_RequestEndedIsNotNotFound verifies that a cancelled request is
// answered as ended (499), not as a missing detection.
func TestGetDetection_RequestEndedIsNotNotFound(t *testing.T) {
	e, _, repo, h := detectionsRepoHandler(t)
	repo.EXPECT().Get(mock.Anything, "2").Return(nil, context.Canceled)
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	req := httptest.NewRequest(http.MethodGet, "/api/v2/detections/2", http.NoBody).WithContext(ctx)
	rec := httptest.NewRecorder()
	c := e.NewContext(req, rec)
	c.SetPath("/api/v2/detections/:id")
	c.SetParamNames("id")
	c.SetParamValues("2")
	require.NoError(t, h.GetDetection(c))
	assert.Equal(t, apicore.StatusClientClosedRequest, rec.Code)
}

// TestGetDetections_DirectLoadCancellation verifies that a page above the cache
// size loads on the request context and that a query cancelled while it runs is
// answered as an ended request instead of a 500.
func TestGetDetections_DirectLoadCancellation(t *testing.T) {
	e, _, repo, h := detectionsRepoHandler(t)
	ctx, cancel := context.WithCancel(t.Context())
	t.Cleanup(cancel)
	repo.EXPECT().GetHourly(mock.Anything, "2026-11-01", "01", 1, 500, 0).
		Run(func(loadCtx context.Context, _, _ string, _, _, _ int) {
			assert.Equal(t, ctx, loadCtx, "a direct load runs on the request context")
			cancel()
		}).
		Return(nil, int64(0), context.Canceled)
	req := httptest.NewRequest(http.MethodGet,
		"/api/v2/detections?queryType=hourly&date=2026-11-01&hour=01&numResults=500", http.NoBody).WithContext(ctx)
	rec := httptest.NewRecorder()
	c := e.NewContext(req, rec)
	c.SetPath("/api/v2/detections")
	require.NoError(t, h.GetDetections(c))
	assert.Equal(t, apicore.StatusClientClosedRequest, rec.Code)
}

// TestResultToDetectionResponse_MatchesBridgeNote verifies that a fully populated
// detection is shown identically whether the repository returns a Result directly
// or the legacy bridge converts the equivalent Note, and that no field is lost.
func TestResultToDetectionResponse_MatchesBridgeNote(t *testing.T) {
	loc := time.Local
	stamp := time.Date(2025, time.March, 7, 8, 15, 30, 0, loc)
	created := stamp.Add(time.Minute)
	note := datastore.Note{
		ID: 42, Date: "2025-03-07", Time: "08:15:30", Source: testRealtimeSource(),
		BeginTime: stamp, EndTime: stamp.Add(3 * time.Second),
		SpeciesCode: "PARMAJ", ScientificName: "Parus major", CommonName: "Great Tit",
		Confidence: 0.87, ClipName: "clips/2025/03/great_tit_87p.wav",
		ClipSampleRate: 48000, Verified: "correct", Locked: true, Unlikely: true,
		Comments: []datastore.NoteComment{{ID: 3, Entry: "nice", CreatedAt: created, UpdatedAt: created}},
	}
	for _, tc := range []struct {
		name string
		edit func(n *datastore.Note)
	}{
		{"ordinary row", func(*datastore.Note) {}},
		{"spectrogram only row", func(n *datastore.Note) { n.ClipName = ""; n.SpectrogramClipName = "clips/old.wav" }},
		{"false positive without comments", func(n *datastore.Note) { n.Verified = "false_positive"; n.Comments = nil }},
	} {
		t.Run(tc.name, func(t *testing.T) {
			n := note
			tc.edit(&n)

			e, mockDS, h := setupTestEnvironment(t)
			mockDS.EXPECT().GetLastDetections(1).Return([]datastore.Note{n}, nil)
			legacy := recentBody(t, e, h)

			repoE, _, repo, repoH := detectionsRepoHandler(t)
			repo.EXPECT().GetRecent(mock.Anything, 1).Return([]*detection.Result{resultFromNote(&n)}, nil)
			direct := recentBody(t, repoE, repoH)

			require.Len(t, legacy, 1)
			assert.Equal(t, legacy, direct)

			got := direct[0]
			assert.Equal(t, uint(42), got.ID)
			assert.Equal(t, "2025-03-07", got.Date)
			assert.Equal(t, "08:15:30", got.Time)
			assert.Equal(t, stamp.Format(time.RFC3339), got.Timestamp)
			assert.Equal(t, stamp.Format(time.RFC3339), got.BeginTime)
			assert.Equal(t, stamp.Add(3*time.Second).Format(time.RFC3339), got.EndTime)
			assert.Equal(t, "Parus major", got.ScientificName)
			assert.Equal(t, "Great Tit", got.CommonName)
			assert.Equal(t, "PARMAJ", got.SpeciesCode)
			assert.InDelta(t, 0.87, got.Confidence, 1e-9)
			assert.Equal(t, 48000, got.ClipSampleRate)
			assert.Equal(t, n.Locked, got.Locked)
			assert.True(t, got.Unlikely)
			assert.Equal(t, defaultModelType, got.ModelType)
			assert.Equal(t, n.ClipName == "" && n.SpectrogramClipName != "", got.SpectrogramOnly)
			if n.ClipName != "" {
				assert.Equal(t, "great_tit_87p.wav", got.ClipName)
			}
			assert.Equal(t, n.Verified, got.Verified)
			assert.Len(t, got.Comments, len(n.Comments))
		})
	}
}

// resultFromNote builds the Result the v2 repository returns for the same row.
func resultFromNote(n *datastore.Note) *detection.Result {
	ts, _ := time.ParseInLocation(time.DateTime, n.Date+" "+n.Time, time.Local)
	r := &detection.Result{
		ID: n.ID, Timestamp: ts, BeginTime: n.BeginTime, EndTime: n.EndTime,
		AudioSource: detection.AudioSource{
			ID: n.Source.ID, SafeString: n.Source.SafeString, DisplayName: n.Source.DisplayName,
		},
		Species:    detection.Species{Code: n.SpeciesCode, ScientificName: n.ScientificName, CommonName: n.CommonName},
		Confidence: n.Confidence, ClipName: n.ClipName, SpectrogramClipName: n.SpectrogramClipName,
		ClipSampleRate: n.ClipSampleRate, Verified: n.Verified, Locked: n.Locked, Unlikely: n.Unlikely,
		Model: detection.DefaultModelInfo(),
	}
	for _, c := range n.Comments {
		r.Comments = append(r.Comments, detection.Comment{ID: c.ID, Entry: c.Entry, CreatedAt: c.CreatedAt, UpdatedAt: c.UpdatedAt})
	}
	return r
}

// recentBody runs GET /detections/recent?limit=1 and decodes the array.
func recentBody(t *testing.T, e *echo.Echo, h *Handler) []DetectionResponse {
	t.Helper()
	req := httptest.NewRequest(http.MethodGet, "/api/v2/detections/recent?limit=1", http.NoBody)
	rec := httptest.NewRecorder()
	c := e.NewContext(req, rec)
	c.SetPath("/api/v2/detections/recent")
	require.NoError(t, h.GetRecentDetections(c))
	require.Equal(t, http.StatusOK, rec.Code)
	var resp []DetectionResponse
	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &resp))
	return resp
}

// TestDetectionCache_DoesNotMutateCachedResults verifies that building responses
// leaves the shared cached Results untouched and that a repeat request is served
// from the cache.
func TestDetectionCache_DoesNotMutateCachedResults(t *testing.T) {
	e, _, repo, h := detectionsRepoHandler(t)
	results := repeatedHourZones()[0].results(t)
	results[0].ClipName = "clips/a.wav"
	snapshot := *results[0]
	repo.EXPECT().GetHourly(mock.Anything, "2026-11-01", "01", 1, 10, 0).Return(results, int64(2), nil).Once()

	query := url.Values{"queryType": {"hourly"}, "date": {"2026-11-01"}, "hour": {"01"}, "numResults": {"10"}}
	_, first := getDetectionsBody(t, e, h, query)
	_, second := getDetectionsBody(t, e, h, query)

	assert.JSONEq(t, first, second)
	assert.Equal(t, uint64(1), h.DetectionCache.Stats().Hits)
	assert.Equal(t, snapshot, *results[0], "the cached Result must not be written through")
}

// TestGetDetections_CachedLoadIgnoresRequestCancellation verifies that a cached page
// loads on the context the cache hands the loader, not on the request context, so one
// caller going away cannot fail the load other callers share.
func TestGetDetections_CachedLoadIgnoresRequestCancellation(t *testing.T) {
	e, _, repo, h := detectionsRepoHandler(t)
	reqCtx, cancel := context.WithCancel(t.Context())
	t.Cleanup(cancel)
	repo.EXPECT().GetHourly(mock.Anything, "2026-11-01", "01", 1, 10, 0).
		Run(func(loadCtx context.Context, _, _ string, _, _, _ int) {
			cancel()
			assert.NoError(t, loadCtx.Err(), "the shared load must survive the caller's cancellation")
		}).
		Return(repeatedHourZones()[0].results(t), int64(2), nil)
	req := httptest.NewRequest(http.MethodGet,
		"/api/v2/detections?queryType=hourly&date=2026-11-01&hour=01&numResults=10", http.NoBody).WithContext(reqCtx)
	c := e.NewContext(req, httptest.NewRecorder())
	c.SetPath("/api/v2/detections")
	require.NoError(t, h.GetDetections(c))
}

// TestGetDetections_InvalidSpeciesDateIsRejectedBeforeTheRepository verifies that a
// malformed date answers 400 without a repository call: the v2 store would ignore
// it and list every date.
func TestGetDetections_InvalidSpeciesDateIsRejectedBeforeTheRepository(t *testing.T) {
	e, _, _, h := detectionsRepoHandler(t)
	status, _ := getDetectionsBody(t, e, h, url.Values{
		"queryType": {"species"}, "species": {"Parus major"}, "date": {"2026-13-45"},
	})
	assert.Equal(t, http.StatusBadRequest, status)
}
