// detections_filter_errors_test.go: how the detections endpoints report filters
// the request or the serving datastore cannot satisfy, and how the new filters
// are mapped for the datastore.
package detections

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/labstack/echo/v4"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/require"

	"github.com/tphakala/birdnet-go/internal/datastore"
	"github.com/tphakala/birdnet-go/internal/errors"
)

// legacyRejection is the error the legacy datastore returns for a filter only the
// normalized datastore implements.
func legacyRejection() error {
	return errors.Newf("the legacy datastore does not support the review verdict filter").
		Component("datastore").
		Category(errors.CategoryValidation).
		Build()
}

// TestGetDetections_StoreValidationErrorIsA400 pins that a filter the serving
// datastore refuses surfaces as a 400 carrying the reason, not as a generic 500
// that looks like a server fault.
func TestGetDetections_StoreValidationErrorIsA400(t *testing.T) {
	t.Run("validation error", func(t *testing.T) {
		e, mockDS, h := setupTestEnvironment(t)
		mockDS.On("SearchNotesAdvanced", mock.Anything).Return(nil, int64(0), legacyRejection())

		req := httptest.NewRequest(http.MethodGet, "/api/v2/detections?verified=correct", http.NoBody)
		rec := httptest.NewRecorder()
		require.NoError(t, h.GetDetections(e.NewContext(req, rec)))

		assert.Equal(t, http.StatusBadRequest, rec.Code)
		assert.Contains(t, rec.Body.String(), "legacy datastore does not support")
	})

	t.Run("other errors stay a 500", func(t *testing.T) {
		e, mockDS, h := setupTestEnvironment(t)
		mockDS.On("SearchNotesAdvanced", mock.Anything).Return(nil, int64(0), assert.AnError)

		req := httptest.NewRequest(http.MethodGet, "/api/v2/detections?verified=correct", http.NoBody)
		rec := httptest.NewRecorder()
		require.NoError(t, h.GetDetections(e.NewContext(req, rec)))

		assert.Equal(t, http.StatusInternalServerError, rec.Code)
		assert.NotContains(t, rec.Body.String(), assert.AnError.Error())
	})
}

// TestGetDetections_MapsAdvancedFiltersForTheDatastore pins the vocabulary the
// datastore receives: legacy aliases normalized, verdicts and lock state carried.
func TestGetDetections_MapsAdvancedFiltersForTheDatastore(t *testing.T) {
	e, mockDS, h := setupTestEnvironment(t)
	var got *datastore.AdvancedSearchFilters
	mockDS.On("SearchNotesAdvanced", mock.Anything).
		Run(func(args mock.Arguments) {
			filters, ok := args.Get(0).(*datastore.AdvancedSearchFilters)
			require.True(t, ok)
			got = filters
		}).
		Return([]datastore.Note{}, int64(0), nil)

	req := httptest.NewRequest(http.MethodGet,
		"/api/v2/detections?timeOfDay=dawn&verified=false_positive&locked=true&confidenceMax=0", http.NoBody)
	rec := httptest.NewRecorder()
	require.NoError(t, h.GetDetections(e.NewContext(req, rec)))
	require.Equal(t, http.StatusOK, rec.Code, rec.Body.String())

	require.NotNil(t, got)
	assert.Equal(t, []string{datastore.TimeOfDaySunrise}, got.TimeOfDay, "the dawn alias must be normalized")
	assert.Equal(t, datastore.VerifiedStatusFalsePositive, got.VerifiedStatus)
	require.NotNil(t, got.Locked)
	assert.True(t, *got.Locked)
	require.NotNil(t, got.ConfidenceRange)
	assert.InDelta(t, 0.0, got.ConfidenceRange.Max, 0, "a maximum of 0 is a cap, not an unset value")
}

// TestBatchResolveDetections_RejectsInvalidDatesAndHours pins that "select all
// matching" validates dates and hours like the list does: a filter dropped for
// being malformed would otherwise resolve to every detection and hand it to a
// bulk delete, review or lock.
func TestBatchResolveDetections_RejectsInvalidDatesAndHours(t *testing.T) {
	tests := []struct {
		name string
		body BatchResolveRequest
	}{
		{"unparseable start date", BatchResolveRequest{StartDate: "not-a-date"}},
		{"inverted range", BatchResolveRequest{StartDate: "2024-06-10", EndDate: "2024-06-01"}},
		{"out of range hour", BatchResolveRequest{Hour: "25"}},
		{"inverted hour range", BatchResolveRequest{Hour: "9-6"}},
		{"unparseable single date", BatchResolveRequest{Date: "2024-13-45"}},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			e, mockDS, h := setupTestEnvironment(t)

			body, err := json.Marshal(tc.body)
			require.NoError(t, err)
			req := httptest.NewRequest(http.MethodPost, "/api/v2/detections/batch/resolve", bytes.NewReader(body))
			req.Header.Set("Content-Type", "application/json")
			rec := httptest.NewRecorder()

			err = h.BatchResolveDetections(e.NewContext(req, rec))

			var httpErr *echo.HTTPError
			require.ErrorAs(t, err, &httpErr)
			assert.Equal(t, http.StatusBadRequest, httpErr.Code)
			mockDS.AssertNotCalled(t, "SearchNotes", mock.Anything, mock.Anything, mock.Anything, mock.Anything)
		})
	}
}
