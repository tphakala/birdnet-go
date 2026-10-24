package detections

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/tphakala/birdnet-go/internal/datastore"
	datastoreV2 "github.com/tphakala/birdnet-go/internal/datastore/v2"
	"github.com/tphakala/birdnet-go/internal/datastore/v2/entities"
	"github.com/tphakala/birdnet-go/internal/datastore/v2/repository"
)

const (
	contribTestDBName     = "contributions.db"
	contribTestSpeciesID  = "Turdus merula"
	contribTestHitsA      = 2
	contribTestHitsB      = 1
	contribTestMaxA       = 0.8
	contribTestMinA       = 0.6
	contribTestSumA       = 1.7
	contribTestMaxB       = 0.5
	contribTestNoRowsNote = 9999
)

// contribFixture is a v2 database with one detection that has two contributions.
type contribFixture struct {
	mgr         datastoreV2.Manager
	detectionID uint
	firstHit    time.Time
	lastHit     time.Time
}

// newContribFixture opens a real v2 SQLite manager and seeds a detection with
// two model contributions: the seeded default model with full statistics (its
// summed confidences exceed hit count times max, so the mean must be clamped)
// and a second model without statistics.
func newContribFixture(t *testing.T) *contribFixture {
	t.Helper()
	mgr, err := datastoreV2.NewSQLiteManager(datastoreV2.Config{
		DirectPath: filepath.Join(t.TempDir(), contribTestDBName),
	})
	require.NoError(t, err)
	require.NoError(t, mgr.Initialize())
	t.Cleanup(func() { assert.NoError(t, mgr.Close()) })

	db := mgr.DB()
	var modelA entities.AIModel
	require.NoError(t, db.First(&modelA).Error)
	modelB := entities.AIModel{Name: "Perch", Version: "2", Variant: "default", ModelType: entities.ModelTypeMulti}
	require.NoError(t, db.Create(&modelB).Error)

	var labelType entities.LabelType
	require.NoError(t, db.First(&labelType).Error)
	label := entities.Label{ScientificName: contribTestSpeciesID, ModelID: modelA.ID, LabelTypeID: labelType.ID}
	require.NoError(t, db.Create(&label).Error)

	det := entities.Detection{LabelID: label.ID, ModelID: modelA.ID, Confidence: contribTestMaxA, DetectedAt: time.Now().UnixMilli()}
	require.NoError(t, db.Create(&det).Error)

	f := &contribFixture{
		mgr:         mgr,
		detectionID: det.ID,
		firstHit:    time.Date(2026, 10, 24, 6, 30, 15, 0, time.Local),
		lastHit:     time.Date(2026, 10, 24, 6, 30, 24, 0, time.Local),
	}
	first, last := f.firstHit.UnixMilli(), f.lastHit.UnixMilli()
	minConf, sum := contribTestMinA, contribTestSumA
	repo := repository.NewDetectionRepository(db, nil, false, false)
	require.NoError(t, repo.SaveModelContributions(t.Context(), det.ID, []*entities.DetectionModelContribution{
		{ModelID: modelA.ID, HitCount: contribTestHitsA, MaxConfidence: contribTestMaxA,
			FirstHitAt: &first, LastHitAt: &last, MinConfidence: &minConf, ConfidenceSum: &sum},
		{ModelID: modelB.ID, HitCount: contribTestHitsB, MaxConfidence: contribTestMaxB},
	}))
	return f
}

func contribTestNote(id uint) datastore.Note {
	return datastore.Note{
		ID:             id,
		Date:           "2026-10-24",
		Time:           "06:30:24",
		Source:         testRealtimeSource(),
		ScientificName: contribTestSpeciesID,
		CommonName:     "Eurasian Blackbird",
		Confidence:     contribTestMaxA,
		BeginTime:      time.Now().Add(-time.Hour),
		EndTime:        time.Now(),
	}
}

func TestGetDetection_ModelContributions(t *testing.T) {
	f := newContribFixture(t)

	get := func(t *testing.T) map[string]any {
		t.Helper()
		e, mockDS, h := setupTestEnvironment(t)
		h.V2Manager = f.mgr
		h.RegisterDetectionRoutes(h.Group)
		mockDS.On("Get", "1").Return(contribTestNote(f.detectionID), nil)
		mockDS.On("GetHourlyWeather", "2026-10-24").Return([]datastore.HourlyWeather{}, nil)

		req := httptest.NewRequest(http.MethodGet, "/api/v2/detections/1", http.NoBody)
		rec := httptest.NewRecorder()
		c := e.NewContext(req, rec)
		c.SetParamNames("id")
		c.SetParamValues("1")
		require.NoError(t, h.GetDetection(c))
		require.Equal(t, http.StatusOK, rec.Code)
		var payload map[string]any
		require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &payload))
		return payload
	}

	t.Run("enhanced database", func(t *testing.T) {
		datastoreV2.SetEnhancedDatabaseMode()
		t.Cleanup(datastoreV2.ResetDatabaseMode)

		payload := get(t)
		raw, ok := payload["modelContributions"].([]any)
		require.True(t, ok, "modelContributions must be present")
		require.Len(t, raw, 2)

		a, ok := raw[0].(map[string]any)
		require.True(t, ok)
		assert.InDelta(t, float64(contribTestHitsA), a["hitCount"], 0)
		assert.InDelta(t, contribTestMaxA, a["maxConfidence"], 1e-9)
		assert.InDelta(t, contribTestMinA, a["minConfidence"], 1e-9)
		assert.InDelta(t, contribTestMaxA, a["meanConfidence"], 1e-9, "mean is clamped to the maximum")
		assert.Equal(t, f.firstHit.Format(time.RFC3339), a["firstHitAt"])
		assert.Equal(t, f.lastHit.Format(time.RFC3339), a["lastHitAt"])
		assert.NotEmpty(t, a["modelName"])
		assert.NotEmpty(t, a["modelVersion"])
		assert.NotEmpty(t, a["modelVariant"])
		assert.NotEmpty(t, a["modelType"])

		b, ok := raw[1].(map[string]any)
		require.True(t, ok)
		assert.Equal(t, "Perch", b["modelName"])
		assert.InDelta(t, float64(contribTestHitsB), b["hitCount"], 0)
		assert.InDelta(t, contribTestMaxB, b["maxConfidence"], 1e-9)
		for _, key := range []string{"minConfidence", "meanConfidence", "firstHitAt", "lastHitAt"} {
			assert.NotContains(t, b, key, "unknown statistics must be absent, not zero")
		}
	})

	t.Run("migration mode", func(t *testing.T) {
		datastoreV2.ResetDatabaseMode()
		assert.NotContains(t, get(t), "modelContributions",
			"a manager without the enhanced flag means legacy ids; contributions must not be read")
	})
}

func TestGetRecentDetections_ModelContributions(t *testing.T) {
	f := newContribFixture(t)
	datastoreV2.SetEnhancedDatabaseMode()
	t.Cleanup(datastoreV2.ResetDatabaseMode)

	e, mockDS, h := setupTestEnvironment(t)
	h.V2Manager = f.mgr
	h.RegisterDetectionRoutes(h.Group)
	mockDS.On("GetLastDetections", 10).Return([]datastore.Note{
		contribTestNote(f.detectionID),
		contribTestNote(contribTestNoRowsNote),
	}, nil)

	req := httptest.NewRequest(http.MethodGet, "/api/v2/detections/recent", http.NoBody)
	rec := httptest.NewRecorder()
	require.NoError(t, h.GetRecentDetections(e.NewContext(req, rec)))
	require.Equal(t, http.StatusOK, rec.Code)

	var payload []map[string]any
	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &payload))
	require.Len(t, payload, 2)
	assert.Contains(t, payload[0], "modelContributions")
	assert.NotContains(t, payload[1], "modelContributions", "no rows means the field is omitted, not empty")
}
