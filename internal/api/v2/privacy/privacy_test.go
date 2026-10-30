package privacy

// No test in this file that reaches a handler may call t.Parallel: the
// enhanced-database flag and the published settings are process global.

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"net/url"
	"path/filepath"
	"strconv"
	"testing"
	"time"

	"github.com/labstack/echo/v4"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/tphakala/birdnet-go/internal/api/v2/apicore"
	"github.com/tphakala/birdnet-go/internal/api/v2/apitest"
	datastoreV2 "github.com/tphakala/birdnet-go/internal/datastore/v2"
	"github.com/tphakala/birdnet-go/internal/datastore/v2/entities"
	"github.com/tphakala/birdnet-go/internal/datastore/v2/repository"
	"github.com/tphakala/birdnet-go/internal/testutil"
)

const (
	testDBName       = "privacy_test.db"
	testDate         = "2026-03-10"
	testMarkerURI    = "rtsp://marker-uri-host/x"
	testNamedDisplay = "Garden rtsp://admin:s3cret@cam.local/stream"
	testActivityPath = "/api/v2/privacy-filter/activity"
	testDiscardsPath = "/api/v2/privacy-filter/discards"
	testExpectedKey  = "errors.privacyFilter.v2Required"
)

// passAuth and denyAuth stand in for the production auth middleware.
func passAuth(next echo.HandlerFunc) echo.HandlerFunc { return next }

func denyAuth(_ echo.HandlerFunc) echo.HandlerFunc {
	return func(ctx echo.Context) error { return ctx.NoContent(http.StatusUnauthorized) }
}

// fixture is a core with the privacy routes registered on a real v2 SQLite
// database.
type fixture struct {
	core *apicore.Core
	db   datastoreV2.Manager
}

func newFixture(t *testing.T, auth echo.MiddlewareFunc, withManager bool) *fixture {
	t.Helper()
	core := apitest.NewCore(t)
	core.AuthMiddleware = auth
	f := &fixture{core: core}
	if withManager {
		mgr, err := datastoreV2.NewSQLiteManager(datastoreV2.Config{DirectPath: filepath.Join(t.TempDir(), testDBName)})
		require.NoError(t, err)
		require.NoError(t, mgr.Initialize())
		t.Cleanup(func() { assert.NoError(t, mgr.Close()) })
		core.V2Manager = mgr
		f.db = mgr
	}
	New(core).RegisterRoutes(core.Group)
	return f
}

func (f *fixture) get(t *testing.T, path string, query url.Values) *httptest.ResponseRecorder {
	t.Helper()
	target := path
	if len(query) > 0 {
		target += "?" + query.Encode()
	}
	rec := httptest.NewRecorder()
	f.core.Echo.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, target, http.NoBody))
	return rec
}

func localNoon() time.Time { return time.Date(2026, 3, 10, 12, 0, 0, 0, time.Local) }

// seed writes two sources (one named with a credentialed URL, one unnamed), two
// hourly rows and three discards, one of them without a source or hits.
func (f *fixture) seed(t *testing.T) (named, unnamed *entities.AudioSource) {
	t.Helper()
	ctx := t.Context()
	db := f.db.DB()
	sources := repository.NewAudioSourceRepository(db, nil, false, false)
	hourly := repository.NewPrivacyFilterHourlyRepository(db, nil, false, false)
	discards := repository.NewPrivacyFilterDiscardRepository(db, nil, false)

	name := testNamedDisplay
	named, err := sources.GetOrCreate(ctx, testMarkerURI, "node-marker", &name, entities.SourceType(""))
	require.NoError(t, err)
	unnamed, err = sources.GetOrCreate(ctx, "plain-source", "node-marker", nil, entities.SourceType(""))
	require.NoError(t, err)

	hour := localNoon().UTC().Truncate(time.Hour).Unix()
	require.NoError(t, hourly.AddDelta(ctx, &entities.PrivacyFilterHourly{
		HourStart: hour, SourceID: named.ID, ModelID: "m1", TriggerType: "label", ThresholdMilli: 50,
		Hits: 5, Held: 2, Confirmed: 1, Discards: 1, MaxScore: 0.9,
		MarginLt50: 1, MarginLt100: 2, MarginLt200: 3, MarginGe200: 4,
	}))
	require.NoError(t, hourly.AddDelta(ctx, &entities.PrivacyFilterHourly{
		HourStart: hour, SourceID: unnamed.ID, ModelID: "", TriggerType: "vad", ThresholdMilli: 0, UnscannedSaves: 3,
	}))

	at := localNoon().UnixMilli()
	mk := func(offset int64, src *uint, hits ...entities.PrivacyFilterDiscardHit) *entities.PrivacyFilterDiscard {
		return &entities.PrivacyFilterDiscard{
			DiscardedAt: at + offset, SourceID: src, ScientificName: "Parus major", CommonName: "Great Tit",
			Confidence: 0.8, ModelID: "m1", ClipBegin: at + offset - 3000, ClipEnd: at + offset + 3000,
			LabelThresholdMilli: 50, VADEnabled: true, VADThresholdMilli: 350, HitCountsLowerBound: true, Hits: hits,
		}
	}
	for _, d := range []*entities.PrivacyFilterDiscard{
		mk(1, &named.ID, entities.PrivacyFilterDiscardHit{Detector: "m1", TriggerType: "label", HitCount: 2, MinScore: 0.2, MaxScore: 0.7}),
		mk(2, nil),
		mk(3, &unnamed.ID, entities.PrivacyFilterDiscardHit{Detector: "silero_vad", TriggerType: "vad", HitCount: 1, MinScore: 0.5, MaxScore: 0.5}),
	} {
		require.NoError(t, discards.Save(ctx, d))
	}
	return named, unnamed
}

func TestPrivacyRoutes_GateOrder(t *testing.T) {
	query := url.Values{"start_date": {testDate}}
	for _, path := range []string{testActivityPath, testDiscardsPath} {
		t.Run("legacy mode denied auth gives 401 "+path, func(t *testing.T) {
			datastoreV2.ResetDatabaseMode()
			f := newFixture(t, denyAuth, true)
			assert.Equal(t, http.StatusUnauthorized, f.get(t, path, query).Code)
		})
		t.Run("legacy mode passing auth gives 409 "+path, func(t *testing.T) {
			datastoreV2.ResetDatabaseMode()
			f := newFixture(t, passAuth, true)
			f.seed(t)
			rec := f.get(t, path, query)
			require.Equal(t, http.StatusConflict, rec.Code)
			var body map[string]any
			require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &body))
			assert.Equal(t, testExpectedKey, body["error_key"])
			assert.NotContains(t, rec.Body.String(), "Parus")
		})
		t.Run("enhanced mode without manager gives 409 "+path, func(t *testing.T) {
			datastoreV2.SetEnhancedDatabaseMode()
			t.Cleanup(datastoreV2.ResetDatabaseMode)
			f := newFixture(t, passAuth, false)
			assert.Equal(t, http.StatusConflict, f.get(t, path, query).Code)
		})
	}
}

func TestGetActivity(t *testing.T) {
	datastoreV2.SetEnhancedDatabaseMode()
	t.Cleanup(datastoreV2.ResetDatabaseMode)
	f := newFixture(t, passAuth, true)
	named, unnamed := f.seed(t)

	rec := f.get(t, testActivityPath, url.Values{"start_date": {testDate}, "end_date": {testDate}})
	require.Equal(t, http.StatusOK, rec.Code, rec.Body.String())
	body := rec.Body.String()
	for _, leaked := range []string{testMarkerURI, "marker-uri-host", "s3cret", "admin", "source_uri", "node_name", "node-marker"} {
		assert.NotContains(t, body, leaked)
	}

	var resp ActivityResponse
	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &resp))
	assert.Equal(t, testDate, resp.StartDate)
	assert.Equal(t, testDate, resp.EndDate)
	require.Len(t, resp.Rows, 2)

	byTrigger := map[string]ActivityRow{}
	for i := range resp.Rows {
		byTrigger[resp.Rows[i].TriggerType] = resp.Rows[i]
	}
	label := byTrigger["label"]
	assert.InDelta(t, 0.05, label.Threshold, 1e-9)
	assert.Equal(t, "m1", label.ModelID)
	assert.Equal(t, int64(5), label.Hits)
	assert.Equal(t, int64(2), label.Held)
	assert.Equal(t, int64(1), label.Confirmed)
	assert.Equal(t, int64(1), label.Discards)
	assert.Equal(t, []int64{1, 2, 3, 4}, []int64{label.MarginLt50, label.MarginLt100, label.MarginLt200, label.MarginGe200})
	require.NotNil(t, label.Source)
	assert.Equal(t, named.ID, label.Source.ID)
	assert.Contains(t, label.Source.Name, "cam.local")
	assert.Contains(t, label.Source.Name, "Garden")

	vad := byTrigger["vad"]
	assert.Equal(t, int64(3), vad.UnscannedSaves)
	assert.Zero(t, vad.Threshold)
	require.NotNil(t, vad.Source)
	assert.Equal(t, "source-"+strconv.FormatUint(uint64(unnamed.ID), 10), vad.Source.Name)
}

func TestListDiscards(t *testing.T) {
	datastoreV2.SetEnhancedDatabaseMode()
	t.Cleanup(datastoreV2.ResetDatabaseMode)
	f := newFixture(t, passAuth, true)
	f.seed(t)

	rec := f.get(t, testDiscardsPath, url.Values{"start_date": {testDate}, "end_date": {testDate}, "limit": {"2"}})
	require.Equal(t, http.StatusOK, rec.Code, rec.Body.String())
	assert.NotContains(t, rec.Body.String(), testMarkerURI)

	var raw struct {
		Data        []json.RawMessage `json:"data"`
		Total       int64             `json:"total"`
		Limit       int               `json:"limit"`
		Offset      int               `json:"offset"`
		CurrentPage int               `json:"current_page"`
		TotalPages  int               `json:"total_pages"`
	}
	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &raw))
	assert.Equal(t, int64(3), raw.Total)
	assert.Equal(t, 2, raw.Limit)
	assert.Equal(t, 0, raw.Offset)
	assert.Equal(t, 1, raw.CurrentPage)
	assert.Equal(t, 2, raw.TotalPages)
	require.Len(t, raw.Data, 2)

	// Newest first: the unnamed-source discard, then the one without a source.
	var first, second DiscardItem
	require.NoError(t, json.Unmarshal(raw.Data[0], &first))
	require.NoError(t, json.Unmarshal(raw.Data[1], &second))
	require.NotNil(t, first.Source)
	assert.Equal(t, "silero_vad", first.Hits[0].Detector)
	assert.Equal(t, "vad", first.Hits[0].TriggerType)
	assert.InDelta(t, 0.05, first.Settings.LabelThreshold, 1e-9)
	assert.True(t, first.Settings.VADEnabled)
	assert.InDelta(t, 0.35, first.Settings.VADThreshold, 1e-9)
	assert.True(t, first.HitCountsLowerBound)
	assert.Equal(t, "Parus major", first.ScientificName)

	assert.Nil(t, second.Source)
	assert.Contains(t, string(raw.Data[1]), `"source":null`)
	assert.Contains(t, string(raw.Data[1]), `"hits":[]`)
}

// TestListDiscards_CredentialedSource pins the source rendering on the discard
// list: the discard of the source named with a credentialed URL must show the
// sanitized name and leak neither the credentials nor the stored URI.
func TestListDiscards_CredentialedSource(t *testing.T) {
	datastoreV2.SetEnhancedDatabaseMode()
	t.Cleanup(datastoreV2.ResetDatabaseMode)
	f := newFixture(t, passAuth, true)
	named, _ := f.seed(t)

	rec := f.get(t, testDiscardsPath, url.Values{"start_date": {testDate}, "end_date": {testDate}})
	require.Equal(t, http.StatusOK, rec.Code, rec.Body.String())
	body := rec.Body.String()
	for _, leaked := range []string{testMarkerURI, "marker-uri-host", "s3cret", "admin", "source_uri", "node_name", "node-marker"} {
		assert.NotContains(t, body, leaked)
	}

	var resp struct {
		Data []DiscardItem `json:"data"`
	}
	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &resp))
	require.Len(t, resp.Data, 3)

	var found bool
	for i := range resp.Data {
		src := resp.Data[i].Source
		if src == nil || src.ID != named.ID {
			continue
		}
		found = true
		assert.Contains(t, src.Name, "Garden")
		assert.Contains(t, src.Name, "cam.local")
		require.Len(t, resp.Data[i].Hits, 1)
		assert.Equal(t, "m1", resp.Data[i].Hits[0].Detector)
		assert.Equal(t, 2, resp.Data[i].Hits[0].HitCount)
	}
	assert.True(t, found, "discard of the credentialed source is listed")
}

func TestPrivacyQueryValidation(t *testing.T) {
	datastoreV2.SetEnhancedDatabaseMode()
	t.Cleanup(datastoreV2.ResetDatabaseMode)
	f := newFixture(t, passAuth, true)

	cases := []struct {
		name  string
		query url.Values
		want  int
	}{
		{"missing start_date", url.Values{}, http.StatusBadRequest},
		{"one digit month", url.Values{"start_date": {"2026-1-05"}}, http.StatusBadRequest},
		{"32 days", url.Values{"start_date": {"2026-01-01"}, "end_date": {"2026-02-01"}}, http.StatusBadRequest},
		{"limit zero", url.Values{"start_date": {testDate}, "limit": {"0"}}, http.StatusBadRequest},
		{"limit too large", url.Values{"start_date": {testDate}, "limit": {"1001"}}, http.StatusBadRequest},
		{"limit not a number", url.Values{"start_date": {testDate}, "limit": {"abc"}}, http.StatusBadRequest},
		{"negative offset", url.Values{"start_date": {testDate}, "offset": {"-1"}}, http.StatusBadRequest},
		{"offset too large", url.Values{"start_date": {testDate}, "offset": {"1000001"}}, http.StatusBadRequest},
		{"limit at max", url.Values{"start_date": {testDate}, "limit": {"1000"}}, http.StatusOK},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			assert.Equal(t, tc.want, f.get(t, testDiscardsPath, tc.query).Code)
		})
	}
	t.Run("activity missing start_date", func(t *testing.T) {
		assert.Equal(t, http.StatusBadRequest, f.get(t, testActivityPath, url.Values{}).Code)
	})
}

// TestGetActivity_SkippedDateKeepsRequestedStartDate pins that the response reports the requested
// start date for a date that does not exist in the station zone (Samoa skipped 2011-12-30), where
// the range start is a later instant that would format as a different date. It swaps time.Local,
// so it must not run in parallel.
func TestGetActivity_SkippedDateKeepsRequestedStartDate(t *testing.T) {
	apia := testutil.ToolchainZones(t)["Pacific/Apia"]
	require.NotNil(t, apia)
	previous := time.Local
	time.Local = apia
	t.Cleanup(func() { time.Local = previous })

	datastoreV2.SetEnhancedDatabaseMode()
	t.Cleanup(datastoreV2.ResetDatabaseMode)
	f := newFixture(t, passAuth, true)

	const skipped = "2011-12-30"
	rec := f.get(t, testActivityPath, url.Values{"start_date": {skipped}, "end_date": {skipped}})
	require.Equal(t, http.StatusOK, rec.Code, rec.Body.String())

	var resp ActivityResponse
	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &resp))
	assert.Equal(t, skipped, resp.StartDate)
	assert.Equal(t, skipped, resp.EndDate)
	assert.Empty(t, resp.Rows)
	assert.True(t, resp.RangeStart.Equal(resp.RangeEnd), "a skipped date is an empty window")
}
