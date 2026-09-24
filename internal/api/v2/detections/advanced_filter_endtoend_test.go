// advanced_filter_endtoend_test.go: the advanced filter parameters exercised
// through GET /api/v2/detections against a real datastore.
//
// The rest of this package's coverage stops at the parsed parameters or at the
// filter struct the handler builds. That leaves a gap: a parameter can be parsed,
// validated, mapped into AdvancedSearchFilters and still not narrow anything,
// because nothing asserts on rows actually coming back. These tests close it by
// seeding notes and checking what the endpoint returns, which is the only place a
// silently-dropped filter shows up.
package detections

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/labstack/echo/v4"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"gorm.io/driver/sqlite"
	"gorm.io/gorm"

	"github.com/tphakala/birdnet-go/internal/api/v2/apitest"
	"github.com/tphakala/birdnet-go/internal/datastore"
)

// seededConfidences are spread across the percentage range so a band can exclude
// rows on both sides of it.
var seededConfidences = []float64{0.05, 0.11, 0.22, 0.33, 0.44, 0.55, 0.70, 0.88, 0.95}

// newSeededHandler builds a detections handler backed by an in-memory SQLite
// datastore holding one note per seeded confidence.
func newSeededHandler(t *testing.T) (*echo.Echo, *Handler) {
	t.Helper()

	db, err := gorm.Open(sqlite.Open(":memory:"), &gorm.Config{})
	require.NoError(t, err)
	require.NoError(t, db.AutoMigrate(
		&datastore.Note{}, &datastore.NoteReview{}, &datastore.NoteLock{}, &datastore.NoteComment{},
	))

	date := time.Now().Format(time.DateOnly)
	for i, confidence := range seededConfidences {
		note := datastore.Note{
			Date:           date,
			Time:           fmt.Sprintf("%02d:30:00", i+6),
			ScientificName: "Turdus merula",
			CommonName:     "Eurasian Blackbird",
			SpeciesCode:    "eurbla",
			Confidence:     confidence,
		}
		require.NoError(t, db.Create(&note).Error)
	}

	// SQLiteStore is the concrete store the application runs; DataStore alone does
	// not satisfy datastore.Interface.
	ds := &datastore.SQLiteStore{DB: db}
	e := echo.New()
	core := apitest.NewCore(t, apitest.WithEcho(e), apitest.WithDatastore(ds))
	return e, buildTestHandler(t, core, map[string]string{}, map[string]string{})
}

// getDetections calls the endpoint with the given query string and returns the
// confidences of the rows it produced.
func getDetections(t *testing.T, e *echo.Echo, h *Handler, query string) []float64 {
	t.Helper()

	req := httptest.NewRequest(http.MethodGet, "/api/v2/detections?"+query, http.NoBody)
	rec := httptest.NewRecorder()
	ctx := e.NewContext(req, rec)

	require.NoError(t, h.GetDetections(ctx))
	require.Equal(t, http.StatusOK, rec.Code, "body: %s", rec.Body.String())

	var response struct {
		Data []struct {
			Confidence float64 `json:"confidence"`
		} `json:"data"`
	}
	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &response))

	confidences := make([]float64, 0, len(response.Data))
	for _, row := range response.Data {
		confidences = append(confidences, row.Confidence)
	}
	return confidences
}

// TestGetDetections_ConfidenceBandNarrowsResults is the regression guard for a
// confidence band that reached the datastore as a filter but never reduced the
// result set. The parameters are whole percentages, as the filter panel sends them.
func TestGetDetections_ConfidenceBandNarrowsResults(t *testing.T) {
	e, h := newSeededHandler(t)

	t.Run("everything is returned without a band", func(t *testing.T) {
		assert.Len(t, getDetections(t, e, h, "numResults=100"), len(seededConfidences))
	})

	t.Run("a band excludes rows on both sides of it", func(t *testing.T) {
		got := getDetections(t, e, h, "numResults=100&confidenceMin=11&confidenceMax=44")
		assert.ElementsMatch(t, []float64{0.11, 0.22, 0.33, 0.44}, got,
			"the band is inclusive and must drop everything outside it")
	})

	t.Run("a minimum alone drops everything below it", func(t *testing.T) {
		got := getDetections(t, e, h, "numResults=100&confidenceMin=70")
		assert.ElementsMatch(t, []float64{0.70, 0.88, 0.95}, got)
	})

	t.Run("a maximum alone drops everything above it", func(t *testing.T) {
		got := getDetections(t, e, h, "numResults=100&confidenceMax=22")
		assert.ElementsMatch(t, []float64{0.05, 0.11, 0.22}, got)
	})

	t.Run("a full-width band changes nothing", func(t *testing.T) {
		got := getDetections(t, e, h, "numResults=100&confidenceMin=0&confidenceMax=100")
		assert.Len(t, got, len(seededConfidences))
	})
}

// TestGetDetections_ConfidenceBandAppliesAlongsideOtherFilters guards the routing:
// the band must survive being combined with a filter that selects a different
// query path, rather than one of them winning.
func TestGetDetections_ConfidenceBandAppliesAlongsideOtherFilters(t *testing.T) {
	e, h := newSeededHandler(t)

	got := getDetections(t, e, h, "numResults=100&confidenceMin=11&confidenceMax=44&hourRange=6-9")
	// Seeded hours run 06:30 upward in confidence order, so 06:00-09:59 holds the
	// four lowest confidences; intersecting with the band drops the 0.05 row.
	assert.ElementsMatch(t, []float64{0.11, 0.22, 0.33}, got)
}

// TestGetDetections_InvalidConfidenceBoundIsRejected pins the validation, which is
// how a mistyped bound is distinguished from a filter that silently did nothing.
func TestGetDetections_InvalidConfidenceBoundIsRejected(t *testing.T) {
	e, h := newSeededHandler(t)

	for _, query := range []string{"confidenceMin=abc", "confidenceMin=101", "confidenceMin=80&confidenceMax=10"} {
		t.Run(query, func(t *testing.T) {
			req := httptest.NewRequest(http.MethodGet, "/api/v2/detections?"+query, http.NoBody)
			rec := httptest.NewRecorder()
			err := h.GetDetections(e.NewContext(req, rec))

			// The handler either returns the error for Echo to render or writes the
			// status itself; a 200 means the bad bound was accepted and ignored.
			if err == nil {
				assert.NotEqual(t, http.StatusOK, rec.Code, "a malformed bound must not be silently ignored")
				return
			}
			var httpErr *echo.HTTPError
			require.ErrorAs(t, err, &httpErr)
			assert.Equal(t, http.StatusBadRequest, httpErr.Code)
		})
	}
}
