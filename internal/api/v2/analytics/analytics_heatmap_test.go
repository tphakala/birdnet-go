package analytics

import (
	"encoding/csv"
	"encoding/json"
	"net/http"
	"strings"
	"testing"

	"github.com/labstack/echo/v4"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/require"

	"github.com/tphakala/birdnet-go/internal/datastore"
)

// heatmapJSON mirrors the columnar wire shape from the design spec.
type heatmapJSON struct {
	Dates                 []string `json:"dates"`
	SlotResolutionMinutes int      `json:"slotResolutionMinutes"`
	Cells                 struct {
		DateIndex []int `json:"dateIndex"`
		Slot      []int `json:"slot"`
		Count     []int `json:"count"`
	} `json:"cells"`
}

func sampleHeatmap() datastore.ActivityHeatmapData {
	return datastore.ActivityHeatmapData{
		Dates:                 []string{"2026-03-01", "2026-03-02"},
		SlotResolutionMinutes: 15,
		CellDateIndex:         []int{0, 1},
		CellSlot:              []int{0, 95},
		CellCount:             []int{3, 7},
	}
}

func TestGetActivityHeatmap_ColumnarShape(t *testing.T) {
	t.Parallel()
	e, mockDS, controller := setupAnalyticsTestEnvironment(t)

	mockDS.On("GetActivityHeatmap", mock.Anything, "2026-03-01", "2026-03-02", "").
		Return(sampleHeatmap(), nil)

	c, rec := newAnalyticsContext(e, "/api/v2/analytics/time/heatmap?start_date=2026-03-01&end_date=2026-03-02")
	require.NoError(t, controller.GetActivityHeatmap(c))
	require.Equal(t, http.StatusOK, rec.Code)

	var resp heatmapJSON
	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &resp))
	assert.Equal(t, []string{"2026-03-01", "2026-03-02"}, resp.Dates)
	assert.Equal(t, 15, resp.SlotResolutionMinutes)
	assert.Equal(t, []int{0, 1}, resp.Cells.DateIndex)
	assert.Equal(t, []int{0, 95}, resp.Cells.Slot)
	assert.Equal(t, []int{3, 7}, resp.Cells.Count)
	mockDS.AssertExpectations(t)
}

func TestGetActivityHeatmap_EmptyArraysNotNull(t *testing.T) {
	t.Parallel()
	e, mockDS, controller := setupAnalyticsTestEnvironment(t)

	// Datastore returns an empty grid (e.g. unknown species) with no cells.
	mockDS.On("GetActivityHeatmap", mock.Anything, "2026-03-01", "2026-03-02", "Nonexistent species").
		Return(datastore.ActivityHeatmapData{
			Dates:                 []string{"2026-03-01", "2026-03-02"},
			SlotResolutionMinutes: 15,
		}, nil)

	c, rec := newAnalyticsContext(e, "/api/v2/analytics/time/heatmap?start_date=2026-03-01&end_date=2026-03-02&species=Nonexistent+species")
	require.NoError(t, controller.GetActivityHeatmap(c))
	require.Equal(t, http.StatusOK, rec.Code)

	// Cells arrays must serialise as [] not null so the client can read .length safely.
	assert.Contains(t, rec.Body.String(), `"dateIndex":[]`)
	assert.NotContains(t, rec.Body.String(), `"cells":null`)
}

func TestGetActivityHeatmap_CSVExport(t *testing.T) {
	t.Parallel()
	e, mockDS, controller := setupAnalyticsTestEnvironment(t)

	mockDS.On("GetActivityHeatmap", mock.Anything, "2026-03-01", "2026-03-02", "").
		Return(sampleHeatmap(), nil)

	c, rec := newAnalyticsContext(e, "/api/v2/analytics/time/heatmap?start_date=2026-03-01&end_date=2026-03-02&format=csv")
	require.NoError(t, controller.GetActivityHeatmap(c))
	require.Equal(t, http.StatusOK, rec.Code)
	assert.Contains(t, rec.Header().Get(echo.HeaderContentType), "text/csv")

	records, err := csv.NewReader(strings.NewReader(rec.Body.String())).ReadAll()
	require.NoError(t, err)
	require.Len(t, records, 3) // header + 2 cells
	assert.Equal(t, []string{"date", "slot", "slot_start", "count"}, records[0])
	assert.Equal(t, []string{"2026-03-01", "0", "00:00", "3"}, records[1])
	// slot 95 at 15-minute resolution starts at 23:45.
	assert.Equal(t, []string{"2026-03-02", "95", "23:45", "7"}, records[2])
	mockDS.AssertExpectations(t)
}
