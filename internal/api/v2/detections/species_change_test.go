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
	"github.com/tphakala/birdnet-go/internal/api/v2/apitest"
	"github.com/tphakala/birdnet-go/internal/classifier"
	"github.com/tphakala/birdnet-go/internal/conf"
	"github.com/tphakala/birdnet-go/internal/datastore/mocks"
	datastoreV2 "github.com/tphakala/birdnet-go/internal/datastore/v2"
	"github.com/tphakala/birdnet-go/internal/datastore/v2/entities"
	"github.com/tphakala/birdnet-go/internal/datastore/v2/repository"
	"github.com/tphakala/birdnet-go/internal/errors"
	"github.com/tphakala/birdnet-go/internal/speciesindex"
)

const speciesChangeTestBody = `{"scientificName":"` + predTestMallard + `"}`

// speciesChangeCall is one call a fakeSpeciesChanger received.
type speciesChangeCall struct {
	id             uint
	scientificName string
	offer          *repository.SpeciesOffer
}

// fakeSpeciesChanger is a speciesChanger with a canned result. It records the calls
// it received.
type fakeSpeciesChanger struct {
	err   error
	calls []speciesChangeCall
}

func (f *fakeSpeciesChanger) ChangeSpecies(_ context.Context, id uint, scientificName string, offer *repository.SpeciesOffer) error {
	f.calls = append(f.calls, speciesChangeCall{id: id, scientificName: scientificName, offer: offer})
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
			if errors.Is(tc.err, repository.ErrLabelNotFound) {
				assert.Contains(t, body, `"error_key":"`+speciesChangeUnknownKey+`"`)
			}
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
		"a body that is not JSON":       {`{"scientificName":`, speciesChangeInvalidMessage},
		"no scientific name":            {`{}`, speciesChangeMissingMessage},
		"a blank scientific name":       {`{"scientificName":"  "}`, speciesChangeMissingMessage},
		"a name without a species part": {`{"scientificName":"_Blackbird"}`, speciesChangeNoSpeciesMessage},
		"a name of the wrong type":      {`{"scientificName":5}`, speciesChangeInvalidMessage},
	} {
		t.Run(name, func(t *testing.T) {
			changer := &fakeSpeciesChanger{}
			e, _, h := speciesChangeFixture(t, changer)
			code, body := putSpecies(t.Context(), t, e, h, predTestID, tc.body)
			assert.Equal(t, http.StatusBadRequest, code)
			assert.Contains(t, body, tc.message)
			assert.NotContains(t, body, "error_key", "a plain 400 carries no key")
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

	e, _, h := setupTestEnvironment(t)
	// The species has no label yet: the name map offers it and the default model,
	// the registry's BirdNET v2.4, knows it.
	h.loadCommonNameMap = func() map[string]string { return map[string]string{predTestRaven: "Common Raven"} }
	h.speciesKnowledge = func() speciesKnowledge {
		return fakeSpeciesKnowledge{classifier.RegistryIDBirdNETV24: {speciesindex.CanonicalKey(predTestRaven): predTestRaven}}
	}
	h.V2Manager = f.mgr
	h.AuthMiddleware = func(next echo.HandlerFunc) echo.HandlerFunc { return next }
	datastoreV2.SetEnhancedDatabaseMode()
	t.Cleanup(datastoreV2.ResetDatabaseMode)
	h.RegisterDetectionRoutes(h.Group)

	req := httptest.NewRequest(http.MethodPut, "/api/v2/detections/"+strconv.FormatUint(uint64(f.detectionID), 10)+"/species",
		strings.NewReader(`{"scientificName":"corvus corax"}`))
	req.Header.Set(echo.HeaderContentType, echo.MIMEApplicationJSON)
	rec := httptest.NewRecorder()
	e.ServeHTTP(rec, req)

	require.Equal(t, http.StatusOK, rec.Code, rec.Body.String())
	var stored entities.Detection
	require.NoError(t, db.First(&stored, f.detectionID).Error)
	var created entities.Label
	require.NoError(t, db.First(&created, stored.LabelID).Error)
	assert.Equal(t, predTestRaven, created.ScientificName)
	assert.Equal(t, original.ModelID, created.ModelID, "the label belongs to the detection's model")
	var labelType entities.LabelType
	require.NoError(t, db.First(&labelType, created.LabelTypeID).Error)
	assert.Equal(t, entities.LabelTypeSpecies, labelType.Name)
	require.NotNil(t, created.TaxonomicClassID)
	var class entities.TaxonomicClass
	require.NoError(t, db.First(&class, *created.TaxonomicClassID).Error)
	assert.Equal(t, entities.TaxonomicClassAves, class.Name)
	require.NotNil(t, stored.OriginalLabelID)
	assert.Equal(t, original.ID, *stored.OriginalLabelID)
}

// fakeSpeciesKnowledge is a speciesKnowledge over a fixed registry ID to canonical
// key to scientific name table.
type fakeSpeciesKnowledge map[string]map[string]string

func (f fakeSpeciesKnowledge) ScientificName(modelID, key string) (string, bool) {
	name, ok := f[modelID][key]
	return name, ok
}

// TestChangeDetectionSpecies_Offer verifies the offer the handler passes to the
// repository: which sources make a species offered, and what the models say.
func TestChangeDetectionSpecies_Offer(t *testing.T) {
	const (
		blackbird       = "Turdus merula"
		blackbirdLabel  = "Turdus merula_Common Blackbird"
		perchSpelling   = "Turdus merula (Perch)"
		dove            = "Spilopelia senegalensis"
		doveOldSpelling = "Streptopelia senegalensis"
	)
	v24 := classifier.RegistryIDBirdNETV24
	perch := classifier.RegistryIDPerchV2
	tests := []struct {
		name     string
		request  string
		nameMap  map[string]string
		included []string
		labels   []string
		know     fakeSpeciesKnowledge
		want     *repository.SpeciesOffer
	}{
		{
			name: "known by two models and in the name map", request: blackbird,
			nameMap: map[string]string{blackbird: "Common Blackbird"},
			know: fakeSpeciesKnowledge{
				perch: {speciesindex.CanonicalKey(blackbird): perchSpelling},
				v24:   {speciesindex.CanonicalKey(blackbird): blackbird},
			},
			want: &repository.SpeciesOffer{Models: []repository.OfferedModel{
				{Model: classifier.DetectionModelInfoForID(v24), ScientificName: blackbird},
				{Model: classifier.DetectionModelInfoForID(perch), ScientificName: perchSpelling},
			}},
		},
		{
			name: "only in the name map", request: blackbird,
			nameMap: map[string]string{blackbird: "Common Blackbird"},
			want:    &repository.SpeciesOffer{},
		},
		{
			name: "only in the range list, under an alias spelling", request: doveOldSpelling,
			nameMap: map[string]string{"Other species": "Other"}, included: []string{dove + "_African Collared Dove"},
			want: &repository.SpeciesOffer{},
		},
		{
			name: "a prefix of an included label", request: "Turdus",
			nameMap: map[string]string{"Other species": "Other"}, included: []string{blackbirdLabel},
		},
		{
			name: "nowhere", request: "Corvus corax",
			nameMap: map[string]string{"Other species": "Other"}, included: []string{blackbirdLabel},
		},
		{
			name: "startup fallback with an empty name map", request: blackbird,
			labels: []string{blackbirdLabel}, want: &repository.SpeciesOffer{},
		},
		{
			name: "no fallback once the name map is built", request: blackbird,
			nameMap: map[string]string{"Other species": "Other"}, labels: []string{blackbirdLabel},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			changer := &fakeSpeciesChanger{}
			e, _, h := setupTestEnvironment(t, apitest.WithSettingsFunc(func(s *conf.Settings) {
				s.BirdNET.RangeFilter.Species = tt.included
				s.BirdNET.Labels = tt.labels
			}))
			h.speciesChanger = changer
			datastoreV2.SetEnhancedDatabaseMode()
			t.Cleanup(datastoreV2.ResetDatabaseMode)
			h.loadCommonNameMap = func() map[string]string { return tt.nameMap }
			h.speciesKnowledge = func() speciesKnowledge { return tt.know }

			code, body := putSpecies(t.Context(), t, e, h, predTestID, `{"scientificName":"`+tt.request+`"}`)

			require.Equal(t, http.StatusOK, code, body)
			require.Len(t, changer.calls, 1)
			assert.Equal(t, tt.want, changer.calls[0].offer)
		})
	}
}
