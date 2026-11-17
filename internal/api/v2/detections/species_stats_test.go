package detections

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"net/url"
	"testing"
	"time"

	"github.com/labstack/echo/v4"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/tphakala/birdnet-go/internal/api/v2/apicore"
	"github.com/tphakala/birdnet-go/internal/datastore"
	datastoreV2 "github.com/tphakala/birdnet-go/internal/datastore/v2"
	"github.com/tphakala/birdnet-go/internal/datastore/v2/entities"
	"github.com/tphakala/birdnet-go/internal/datastore/v2/repository"
	"github.com/tphakala/birdnet-go/internal/errors"
)

const (
	statsTestSpecies      = "Parus major"
	statsTestPaddedName   = "  Parus major_Great Tit  "
	statsTestFirstEpoch   = int64(1_700_000_000)
	statsTestLastEpoch    = int64(1_760_000_000)
	statsTestAvg          = 0.5
	statsTestMax          = 0.75
	statsTestCorrect      = 4
	statsTestFalsePos     = 2
	statsTestCount        = 10
	statsTestPre1970Epoch = int64(-86_400)
)

// fakeSpeciesStatsReader is a speciesStatsReader with a canned result. It records
// the names it was asked for.
type fakeSpeciesStatsReader struct {
	result *repository.SpeciesStats
	err    error
	asked  []string
}

func (f *fakeSpeciesStatsReader) GetSpeciesStats(_ context.Context, name string) (*repository.SpeciesStats, error) {
	f.asked = append(f.asked, name)
	return f.result, f.err
}

// getSpeciesStats calls the handler with the given request context and name and
// returns the status and raw body. A nil name sends no query parameter. No test
// using it may run in parallel: the enhanced database mode is global state.
func getSpeciesStats(ctx context.Context, t *testing.T, h *Handler, e *echo.Echo, name *string) (code int, body string) {
	t.Helper()
	target := "/api/v2/species/stats"
	if name != nil {
		target += "?scientific_name=" + url.QueryEscape(*name)
	}
	req := httptest.NewRequestWithContext(ctx, http.MethodGet, target, http.NoBody)
	rec := httptest.NewRecorder()
	require.NoError(t, h.GetSpeciesStats(e.NewContext(req, rec)))
	return rec.Code, rec.Body.String()
}

func localRFC3339(epoch int64) string {
	return time.Unix(epoch, 0).In(datastore.Timezone()).Format(time.RFC3339)
}

// TestGetSpeciesStats pins the JSON contract: every key is present, a value that
// does not exist is null, and the name is handed to the repository unchanged
// while the response echoes the extracted species.
func TestGetSpeciesStats(t *testing.T) {
	tests := []struct {
		name   string
		result repository.SpeciesStats
		want   string
	}{
		{
			"full statistics",
			repository.SpeciesStats{
				Detections: statsTestCount, FirstDetectedAt: statsTestFirstEpoch, LastDetectedAt: statsTestLastEpoch,
				AvgConfidence: statsTestAvg, MaxConfidence: statsTestMax,
				CorrectReviews: statsTestCorrect, FalsePositiveReviews: statsTestFalsePos,
			},
			fmt.Sprintf(`{"scientificName":%q,"count":%d,"firstHeard":%q,"lastHeard":%q,"avgConfidence":%v,"maxConfidence":%v,"reviews":{"correct":%d,"falsePositive":%d}}`,
				statsTestSpecies, statsTestCount, localRFC3339(statsTestFirstEpoch), localRFC3339(statsTestLastEpoch),
				statsTestAvg, statsTestMax, statsTestCorrect, statsTestFalsePos),
		},
		{
			"zero statistics",
			repository.SpeciesStats{},
			fmt.Sprintf(`{"scientificName":%q,"count":0,"firstHeard":null,"lastHeard":null,"avgConfidence":null,"maxConfidence":null,"reviews":{"correct":0,"falsePositive":0}}`, statsTestSpecies),
		},
		{
			"times at and before the epoch",
			repository.SpeciesStats{Detections: 1, FirstDetectedAt: statsTestPre1970Epoch, LastDetectedAt: 0, AvgConfidence: statsTestAvg, MaxConfidence: statsTestMax},
			fmt.Sprintf(`{"scientificName":%q,"count":1,"firstHeard":%q,"lastHeard":%q,"avgConfidence":%v,"maxConfidence":%v,"reviews":{"correct":0,"falsePositive":0}}`,
				statsTestSpecies, localRFC3339(statsTestPre1970Epoch), localRFC3339(0), statsTestAvg, statsTestMax),
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			e, _, h := setupTestEnvironment(t)
			reader := &fakeSpeciesStatsReader{result: &tt.result}
			h.speciesStats = reader
			datastoreV2.SetEnhancedDatabaseMode()
			t.Cleanup(datastoreV2.ResetDatabaseMode)

			name := statsTestPaddedName
			code, body := getSpeciesStats(t.Context(), t, h, e, &name)

			require.Equal(t, http.StatusOK, code, body)
			assert.JSONEq(t, tt.want, body)
			assert.Equal(t, []string{statsTestPaddedName}, reader.asked, "the repository must get the name unchanged")
		})
	}
}

// TestGetSpeciesStats_StatusMapping covers one row per branch of the handler.
func TestGetSpeciesStats_StatusMapping(t *testing.T) {
	canceled, cancel := context.WithCancel(t.Context())
	cancel()
	valid, blank, noSpecies := statsTestSpecies, "   ", "_Odd"

	tests := []struct {
		name        string
		legacy      bool
		noReader    bool
		ctx         context.Context
		query       *string
		err         error
		wantStatus  int
		wantQueried bool
	}{
		{"not the enhanced database", true, false, t.Context(), &valid, nil, http.StatusConflict, false},
		{"no reader", false, true, t.Context(), &valid, nil, http.StatusConflict, false},
		{"missing name", false, false, t.Context(), nil, nil, http.StatusBadRequest, false},
		{"blank name", false, false, t.Context(), &blank, nil, http.StatusBadRequest, false},
		{"name without a species part", false, false, t.Context(), &noSpecies, nil, http.StatusBadRequest, false},
		{"database error", false, false, t.Context(), &valid, errors.NewStd("disk failure"), http.StatusInternalServerError, true},
		{"client disconnect", false, false, canceled, &valid, fmt.Errorf("aggregate: %w", context.Canceled), apicore.StatusClientClosedRequest, true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			e, _, h := setupTestEnvironment(t)
			reader := &fakeSpeciesStatsReader{result: &repository.SpeciesStats{}, err: tt.err}
			if !tt.noReader {
				h.speciesStats = reader
			}
			if !tt.legacy {
				datastoreV2.SetEnhancedDatabaseMode()
			}
			t.Cleanup(datastoreV2.ResetDatabaseMode)

			code, body := getSpeciesStats(tt.ctx, t, h, e, tt.query)

			assert.Equal(t, tt.wantStatus, code, body)
			assert.Equal(t, tt.wantQueried, len(reader.asked) > 0, "reader asked: %v", reader.asked)
		})
	}
}

// TestGetSpeciesStats_WiredToV2Repository goes through the registered route with a
// real v2 database. It covers what a fake cannot: the route exists at this path,
// it is outside the authenticated group, the query value is decoded, and the
// handler reads from the v2 repository.
func TestGetSpeciesStats_WiredToV2Repository(t *testing.T) {
	f := newContribFixture(t)
	db := f.mgr.DB()
	var seeded entities.Detection
	require.NoError(t, db.First(&seeded, f.detectionID).Error)
	// The fixture stores a millisecond value in DetectedAt, which no station zone
	// renders sensibly; review it as a false positive so it stays out of the count
	// and seed detections with epochs in seconds.
	require.NoError(t, db.Create(&entities.DetectionReview{DetectionID: seeded.ID, Verified: entities.VerificationFalsePositive}).Error)
	for _, epoch := range []int64{statsTestFirstEpoch, statsTestLastEpoch} {
		det := entities.Detection{LabelID: seeded.LabelID, ModelID: seeded.ModelID, Confidence: statsTestAvg, DetectedAt: epoch}
		require.NoError(t, db.Create(&det).Error)
	}

	e, _, h := setupTestEnvironment(t)
	h.V2Manager = f.mgr
	h.AuthMiddleware = func(echo.HandlerFunc) echo.HandlerFunc {
		return func(c echo.Context) error { return c.NoContent(http.StatusUnauthorized) }
	}
	datastoreV2.SetEnhancedDatabaseMode()
	t.Cleanup(datastoreV2.ResetDatabaseMode)
	h.RegisterDetectionRoutes(h.Group)

	req := httptest.NewRequestWithContext(t.Context(), http.MethodGet, "/api/v2/species/stats?scientific_name="+url.QueryEscape(contribTestSpeciesID), http.NoBody)
	rec := httptest.NewRecorder()
	e.ServeHTTP(rec, req)

	require.Equal(t, http.StatusOK, rec.Code, rec.Body.String())
	assert.JSONEq(t,
		fmt.Sprintf(`{"scientificName":%q,"count":2,"firstHeard":%q,"lastHeard":%q,"avgConfidence":%v,"maxConfidence":%v,"reviews":{"correct":0,"falsePositive":1}}`,
			contribTestSpeciesID, localRFC3339(statsTestFirstEpoch), localRFC3339(statsTestLastEpoch), statsTestAvg, statsTestAvg),
		rec.Body.String())
}
