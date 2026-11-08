package analytics

import (
	"net/http"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/require"

	"github.com/tphakala/birdnet-go/internal/datastore"
)

// dawnOnsetJSON mirrors the dawn-chorus onset wire shape. OnsetRelMinutes is a pointer so a null
// day (too few detections / no civil dawn) round-trips as JSON null rather than 0.
type dawnOnsetJSON struct {
	Date            string `json:"date"`
	OnsetRelMinutes *int   `json:"onsetRelMinutes"`
	DetectionCount  int    `json:"detectionCount"`
}

func sampleDawnOnset() []datastore.DailyActivityOnset {
	return []datastore.DailyActivityOnset{
		{Date: "2026-03-01", OnsetRelMinutes: new(20), DetectionCount: 42},
		// A day below the min-count / with no civil dawn: nil onset, rendered as a gap by the client.
		{Date: "2026-03-02", OnsetRelMinutes: nil, DetectionCount: 3},
	}
}

func TestGetDawnChorusOnset_Shape(t *testing.T) {
	t.Parallel()
	e, mockDS, controller := setupAnalyticsTestEnvironment(t)

	mockDS.On("GetDailyActivityOnset", mock.Anything, "2026-03-01", "2026-03-02", "").
		Return(sampleDawnOnset(), nil)

	c, rec := newAnalyticsContext(e, "/api/v2/analytics/time/dawn-onset?start_date=2026-03-01&end_date=2026-03-02")
	require.NoError(t, controller.GetDawnChorusOnset(c))
	require.Equal(t, http.StatusOK, rec.Code)

	var resp []dawnOnsetJSON
	decodeExactJSON(t, rec.Body.Bytes(), &resp)
	// The gap day keeps a nil onset, which the client draws as a break in the trend line.
	assert.Equal(t, []dawnOnsetJSON{
		{Date: "2026-03-01", OnsetRelMinutes: new(20), DetectionCount: 42},
		{Date: "2026-03-02", OnsetRelMinutes: nil, DetectionCount: 3},
	}, resp)
	mockDS.AssertExpectations(t)
}

func TestGetDawnChorusOnset_NullOnsetSerialisesAsNull(t *testing.T) {
	t.Parallel()
	e, mockDS, controller := setupAnalyticsTestEnvironment(t)

	mockDS.On("GetDailyActivityOnset", mock.Anything, "2026-03-01", "2026-03-02", "").
		Return(sampleDawnOnset(), nil)

	c, rec := newAnalyticsContext(e, "/api/v2/analytics/time/dawn-onset?start_date=2026-03-01&end_date=2026-03-02")
	require.NoError(t, controller.GetDawnChorusOnset(c))
	require.Equal(t, http.StatusOK, rec.Code)

	// A gap day must serialise as null (not 0 or omitted) so the client breaks its trend line there.
	assert.Contains(t, rec.Body.String(), `"onsetRelMinutes":null`)
}

func TestGetDawnChorusOnset_EmptyArrayNotNull(t *testing.T) {
	t.Parallel()
	e, mockDS, controller := setupAnalyticsTestEnvironment(t)

	mockDS.On("GetDailyActivityOnset", mock.Anything, "2026-03-01", "2026-03-02", "").
		Return([]datastore.DailyActivityOnset{}, nil)

	c, rec := newAnalyticsContext(e, "/api/v2/analytics/time/dawn-onset?start_date=2026-03-01&end_date=2026-03-02")
	require.NoError(t, controller.GetDawnChorusOnset(c))
	require.Equal(t, http.StatusOK, rec.Code)
	assert.JSONEq(t, "[]", rec.Body.String())
}
