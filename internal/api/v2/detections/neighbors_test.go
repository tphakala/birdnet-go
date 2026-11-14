package detections

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strconv"
	"testing"

	"github.com/labstack/echo/v4"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/tphakala/birdnet-go/internal/api/v2/apicore"
	datastoreV2 "github.com/tphakala/birdnet-go/internal/datastore/v2"
	"github.com/tphakala/birdnet-go/internal/datastore/v2/repository"
	"github.com/tphakala/birdnet-go/internal/errors"
)

const neighborsInvalidID = "0"

// fakeNeighborReader is a neighborReader with a canned result. It records the IDs
// it was asked for.
type fakeNeighborReader struct {
	result *repository.DetectionNeighbors
	err    error
	asked  []uint
}

func (f *fakeNeighborReader) GetNeighborIDs(_ context.Context, id uint) (*repository.DetectionNeighbors, error) {
	f.asked = append(f.asked, id)
	return f.result, f.err
}

// getNeighbors calls the handler for id with the given request context and
// returns the status and raw response body. No test using it may run in parallel:
// the enhanced database mode is global state.
func getNeighbors(ctx context.Context, t *testing.T, h *Handler, e *echo.Echo, id string) (code int, body string) {
	t.Helper()
	req := httptest.NewRequestWithContext(ctx, http.MethodGet, "/api/v2/detections/"+id+"/neighbors", http.NoBody)
	rec := httptest.NewRecorder()
	c := e.NewContext(req, rec)
	c.SetParamNames("id")
	c.SetParamValues(id)
	require.NoError(t, h.GetDetectionNeighbors(c))
	return rec.Code, rec.Body.String()
}

// TestGetDetectionNeighbors pins the JSON contract: every key is present and a
// missing neighbor is null.
func TestGetDetectionNeighbors(t *testing.T) {
	tests := []struct {
		name   string
		result repository.DetectionNeighbors
		want   string
	}{
		{
			"all four neighbors",
			repository.DetectionNeighbors{Previous: 4, Next: 6, PreviousSameSpecies: 2, NextSameSpecies: 9},
			`{"detectionId":5,"previous":4,"next":6,"previousSameSpecies":2,"nextSameSpecies":9}`,
		},
		{
			"only next",
			repository.DetectionNeighbors{Next: 6},
			`{"detectionId":5,"previous":null,"next":6,"previousSameSpecies":null,"nextSameSpecies":null}`,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			e, _, h := setupTestEnvironment(t)
			reader := &fakeNeighborReader{result: &tt.result}
			h.neighbors = reader
			datastoreV2.SetEnhancedDatabaseMode()
			t.Cleanup(datastoreV2.ResetDatabaseMode)

			code, body := getNeighbors(t.Context(), t, h, e, predTestID)

			require.Equal(t, http.StatusOK, code, body)
			assert.JSONEq(t, tt.want, body)
			assert.Equal(t, []uint{5}, reader.asked)
		})
	}
}

// TestGetDetectionNeighbors_StatusMapping covers one row per branch of the
// handler's error handling.
func TestGetDetectionNeighbors_StatusMapping(t *testing.T) {
	canceled, cancel := context.WithCancel(t.Context())
	cancel()

	tests := []struct {
		name        string
		legacy      bool
		noReader    bool
		ctx         context.Context
		id          string
		err         error
		wantStatus  int
		wantQueried bool
	}{
		{"not the enhanced database", true, false, t.Context(), predTestID, nil, http.StatusConflict, false},
		{"no reader", false, true, t.Context(), predTestID, nil, http.StatusConflict, false},
		{"invalid id", false, false, t.Context(), neighborsInvalidID, nil, http.StatusBadRequest, false},
		{"detection not found", false, false, t.Context(), predTestID, fmt.Errorf("lookup: %w", repository.ErrDetectionNotFound), http.StatusNotFound, true},
		{"database error", false, false, t.Context(), predTestID, errors.NewStd("disk failure"), http.StatusInternalServerError, true},
		{"client disconnect", false, false, canceled, predTestID, fmt.Errorf("lookup: %w", context.Canceled), apicore.StatusClientClosedRequest, true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			e, _, h := setupTestEnvironment(t)
			reader := &fakeNeighborReader{err: tt.err}
			if !tt.noReader {
				h.neighbors = reader
			}
			if !tt.legacy {
				datastoreV2.SetEnhancedDatabaseMode()
			}
			t.Cleanup(datastoreV2.ResetDatabaseMode)

			code, body := getNeighbors(tt.ctx, t, h, e, tt.id)

			assert.Equal(t, tt.wantStatus, code, body)
			assert.Equal(t, tt.wantQueried, len(reader.asked) > 0, "reader asked: %v", reader.asked)
		})
	}
}

// TestGetDetectionNeighbors_WiredToV2Repository goes through the registered route
// with a real v2 database. It covers what a fake cannot: the route exists at this
// path, it is outside the authenticated group, and the handler reads from the v2
// repository.
func TestGetDetectionNeighbors_WiredToV2Repository(t *testing.T) {
	f := newContribFixture(t)
	e, _, h := setupTestEnvironment(t)
	h.V2Manager = f.mgr
	h.AuthMiddleware = func(echo.HandlerFunc) echo.HandlerFunc {
		return func(c echo.Context) error { return c.NoContent(http.StatusUnauthorized) }
	}
	datastoreV2.SetEnhancedDatabaseMode()
	t.Cleanup(datastoreV2.ResetDatabaseMode)
	h.RegisterDetectionRoutes(h.Group)

	id := strconv.FormatUint(uint64(f.detectionID), 10)
	req := httptest.NewRequestWithContext(t.Context(), http.MethodGet, "/api/v2/detections/"+id+"/neighbors", http.NoBody)
	rec := httptest.NewRecorder()
	e.ServeHTTP(rec, req)

	require.Equal(t, http.StatusOK, rec.Code, rec.Body.String())
	assert.JSONEq(t,
		fmt.Sprintf(`{"detectionId":%s,"previous":null,"next":null,"previousSameSpecies":null,"nextSameSpecies":null}`, id),
		rec.Body.String())
}
