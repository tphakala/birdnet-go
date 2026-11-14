package detections

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strconv"
	"strings"
	"testing"

	"github.com/labstack/echo/v4"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/tphakala/birdnet-go/internal/api/v2/apicore"
	"github.com/tphakala/birdnet-go/internal/datastore/mocks"
	datastoreV2 "github.com/tphakala/birdnet-go/internal/datastore/v2"
	"github.com/tphakala/birdnet-go/internal/datastore/v2/entities"
	"github.com/tphakala/birdnet-go/internal/datastore/v2/repository"
	"github.com/tphakala/birdnet-go/internal/errors"
)

const speciesChangeTestBody = `{"scientificName":"` + predTestMallard + `"}`

// speciesChangeCall is one call a fakeSpeciesChanger received.
type speciesChangeCall struct {
	id             uint
	scientificName string
}

// fakeSpeciesChanger is a speciesChanger with a canned result. It records the calls
// it received.
type fakeSpeciesChanger struct {
	err   error
	calls []speciesChangeCall
}

func (f *fakeSpeciesChanger) ChangeSpecies(_ context.Context, id uint, scientificName string) error {
	f.calls = append(f.calls, speciesChangeCall{id: id, scientificName: scientificName})
	return f.err
}

// speciesChangeFixture builds a handler that changes species through changer, with
// the enhanced database mode on. It also returns the strict datastore mock.
func speciesChangeFixture(t *testing.T, changer *fakeSpeciesChanger) (*echo.Echo, *mocks.MockInterface, *Handler) {
	t.Helper()
	e, mockDS, h := setupTestEnvironment(t)
	h.speciesChanger = changer
	datastoreV2.SetEnhancedDatabaseMode()
	t.Cleanup(datastoreV2.ResetDatabaseMode)
	return e, mockDS, h
}

// putSpecies calls the handler for id with a JSON body and the given request
// context, and returns the status and raw response body.
func putSpecies(ctx context.Context, t *testing.T, e *echo.Echo, h *Handler, id, body string) (code int, respBody string) {
	t.Helper()
	req := httptest.NewRequestWithContext(ctx, http.MethodPut, "/api/v2/detections/"+url.PathEscape(id)+"/species", strings.NewReader(body))
	req.Header.Set(echo.HeaderContentType, echo.MIMEApplicationJSON)
	rec := httptest.NewRecorder()
	c := e.NewContext(req, rec)
	c.SetParamNames("id")
	c.SetParamValues(id)
	require.NoError(t, h.ChangeDetectionSpecies(c))
	return rec.Code, rec.Body.String()
}

// TestChangeDetectionSpecies verifies a change: the trimmed name reaches the
// repository with the parsed id, the answer is the success body, and the cached
// detection list pages are dropped.
func TestChangeDetectionSpecies(t *testing.T) {
	changer := &fakeSpeciesChanger{}
	e, mockDS, h := speciesChangeFixture(t, changer)
	mockDS.EXPECT().SearchNotesAdvanced(searchDefaultMatcher("Crow", 10, 0)).Return(cacheTestNotes(), int64(1), nil).Once()
	getDetectionsBody(t, e, h, url.Values{"queryType": {"search"}, "search": {"Crow"}, "numResults": {"10"}})
	require.Equal(t, 1, h.DetectionCache.Len(), "the list request primed the cache")

	code, body := putSpecies(t.Context(), t, e, h, predTestID, `{"scientificName":"  `+predTestMallard+` "}`)

	assert.Equal(t, http.StatusOK, code)
	assert.JSONEq(t, `{"status":"success"}`, body)
	assert.Equal(t, []speciesChangeCall{{id: 5, scientificName: predTestMallard}}, changer.calls)
	assert.Equal(t, 0, h.DetectionCache.Len(), "the change invalidated the cache")
}

// TestChangeDetectionSpecies_LargestID verifies that the largest id a 32-bit build
// holds, also with leading zeros, reaches the repository as that number.
func TestChangeDetectionSpecies_LargestID(t *testing.T) {
	const largest = 1<<32 - 1
	for _, id := range []string{predTestMaxID, "0" + predTestMaxID} {
		changer := &fakeSpeciesChanger{}
		e, _, h := speciesChangeFixture(t, changer)
		code, _ := putSpecies(t.Context(), t, e, h, id, speciesChangeTestBody)
		assert.Equal(t, http.StatusOK, code, id)
		assert.Equal(t, []speciesChangeCall{{id: largest, scientificName: predTestMallard}}, changer.calls, id)
	}
}

// TestChangeDetectionSpecies_StatusMapping verifies one status and message per
// repository sentinel, also when the sentinel is wrapped.
func TestChangeDetectionSpecies_StatusMapping(t *testing.T) {
	tests := []struct {
		name    string
		err     error
		code    int
		message string
	}{
		{"not found", repository.ErrDetectionNotFound, http.StatusNotFound, detectionNotFoundMessage},
		{"not found wrapped", fmt.Errorf("change: %w", repository.ErrDetectionNotFound), http.StatusNotFound, detectionNotFoundMessage},
		{"locked", repository.ErrDetectionLocked, http.StatusConflict, speciesChangeLockedMessage},
		{"locked wrapped", fmt.Errorf("change: %w", repository.ErrDetectionLocked), http.StatusConflict, speciesChangeLockedMessage},
		{"unknown species", repository.ErrLabelNotFound, http.StatusBadRequest, speciesChangeUnknownMessage},
		{"unknown species wrapped", fmt.Errorf("change: %w", repository.ErrLabelNotFound), http.StatusBadRequest, speciesChangeUnknownMessage},
		{"other error", errors.NewStd("database is closed"), http.StatusInternalServerError, speciesChangeFailedMessage},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			e, _, h := speciesChangeFixture(t, &fakeSpeciesChanger{err: tc.err})
			code, body := putSpecies(t.Context(), t, e, h, predTestID, speciesChangeTestBody)
			assert.Equal(t, tc.code, code)
			assert.Contains(t, body, tc.message)
		})
	}

	t.Run("canceled request", func(t *testing.T) {
		e, _, h := speciesChangeFixture(t, &fakeSpeciesChanger{err: fmt.Errorf("change: %w", context.Canceled)})
		ctx, cancel := context.WithCancel(t.Context())
		cancel()
		code, _ := putSpecies(ctx, t, e, h, predTestID, speciesChangeTestBody)
		assert.Equal(t, apicore.StatusClientClosedRequest, code)
	})
}

// TestChangeDetectionSpecies_BadRequest verifies that a bad id or body is answered
// with 400 before anything reaches the repository.
func TestChangeDetectionSpecies_BadRequest(t *testing.T) {
	// Signs, spaces, zero and ids that do not fit in 32 bits are rejected.
	for _, id := range []string{"abc", "0", "-1", "+5", " 5", predTestFirstTooBigID} {
		t.Run("invalid id "+id, func(t *testing.T) {
			changer := &fakeSpeciesChanger{}
			e, _, h := speciesChangeFixture(t, changer)
			code, body := putSpecies(t.Context(), t, e, h, id, speciesChangeTestBody)
			assert.Equal(t, http.StatusBadRequest, code)
			assert.Contains(t, body, invalidDetectionIDMessage)
			assert.Empty(t, changer.calls)
		})
	}

	for name, tc := range map[string]struct{ body, message string }{
		"a body that is not JSON":  {`{"scientificName":`, speciesChangeInvalidMessage},
		"no scientific name":       {`{}`, speciesChangeMissingMessage},
		"a blank scientific name":  {`{"scientificName":"  "}`, speciesChangeMissingMessage},
		"a name of the wrong type": {`{"scientificName":5}`, speciesChangeInvalidMessage},
	} {
		t.Run(name, func(t *testing.T) {
			changer := &fakeSpeciesChanger{}
			e, _, h := speciesChangeFixture(t, changer)
			code, body := putSpecies(t.Context(), t, e, h, predTestID, tc.body)
			assert.Equal(t, http.StatusBadRequest, code)
			assert.Contains(t, body, tc.message)
			assert.Empty(t, changer.calls)
		})
	}
}

// TestChangeDetectionSpecies_RequiresEnhancedDatabase verifies the 409 of an install
// without the enhanced database, and of a handler that was given no v2 repository
// as in migration mode, and that nothing is written in either.
func TestChangeDetectionSpecies_RequiresEnhancedDatabase(t *testing.T) {
	t.Run("enhanced mode off", func(t *testing.T) {
		changer := &fakeSpeciesChanger{}
		e, _, h := speciesChangeFixture(t, changer)
		datastoreV2.ResetDatabaseMode()
		code, body := putSpecies(t.Context(), t, e, h, predTestID, speciesChangeTestBody)
		assert.Equal(t, http.StatusConflict, code)
		assert.Contains(t, body, speciesChangeV2RequiredMessage)
		assert.Empty(t, changer.calls)
	})

	t.Run("no repository", func(t *testing.T) {
		e, _, h := setupTestEnvironment(t)
		datastoreV2.SetEnhancedDatabaseMode()
		t.Cleanup(datastoreV2.ResetDatabaseMode)
		code, body := putSpecies(t.Context(), t, e, h, predTestID, speciesChangeTestBody)
		assert.Equal(t, http.StatusConflict, code)
		assert.Contains(t, body, speciesChangeV2RequiredMessage)
	})
}

// TestChangeDetectionSpecies_WiredToV2Repository sends the request through the
// registered route against a real v2 database. It fails when route registration
// forgets the route or the repository the handler changes species through.
func TestChangeDetectionSpecies_WiredToV2Repository(t *testing.T) {
	f := newContribFixture(t)
	db := f.mgr.DB()
	var original entities.Label
	require.NoError(t, db.First(&original, "scientific_name = ?", contribTestSpeciesID).Error)
	target := entities.Label{ScientificName: predTestRaven, ModelID: original.ModelID, LabelTypeID: original.LabelTypeID}
	require.NoError(t, db.Create(&target).Error)

	e, _, h := setupTestEnvironment(t)
	h.V2Manager = f.mgr
	h.AuthMiddleware = func(next echo.HandlerFunc) echo.HandlerFunc { return next }
	datastoreV2.SetEnhancedDatabaseMode()
	t.Cleanup(datastoreV2.ResetDatabaseMode)
	h.RegisterDetectionRoutes(h.Group)

	req := httptest.NewRequest(http.MethodPut, "/api/v2/detections/"+strconv.FormatUint(uint64(f.detectionID), 10)+"/species",
		strings.NewReader(`{"scientificName":"`+predTestRaven+`"}`))
	req.Header.Set(echo.HeaderContentType, echo.MIMEApplicationJSON)
	rec := httptest.NewRecorder()
	e.ServeHTTP(rec, req)

	require.Equal(t, http.StatusOK, rec.Code, rec.Body.String())
	var stored entities.Detection
	require.NoError(t, db.First(&stored, f.detectionID).Error)
	assert.Equal(t, target.ID, stored.LabelID)
	require.NotNil(t, stored.OriginalLabelID)
	assert.Equal(t, original.ID, *stored.OriginalLabelID)
}
