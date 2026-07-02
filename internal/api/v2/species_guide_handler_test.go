// species_guide_handler_test.go: HTTP handler-level tests for the species guide,
// similar-species, and notes endpoints. These exercise status-code mapping,
// feature gating, cache-unavailable handling, datastore error translation, and
// the auth gating on note writes — the wiring the pure-helper tests in
// species_guide_test.go do not cover.
package api

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/labstack/echo/v4"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/require"

	"github.com/tphakala/birdnet-go/internal/api/v2/apicore"
	"github.com/tphakala/birdnet-go/internal/conf"
	"github.com/tphakala/birdnet-go/internal/conf/conftest"
	"github.com/tphakala/birdnet-go/internal/datastore"
	"github.com/tphakala/birdnet-go/internal/datastore/mocks"
	"github.com/tphakala/birdnet-go/internal/errors"
	"github.com/tphakala/birdnet-go/internal/guideprovider"
)

// Shared literals (kept as constants to satisfy goconst and avoid drift).
const (
	commonBlackbird      = "Common Blackbird"
	paramScientificName  = "scientific_name"
	sciCarrionCrow       = "Corvus corone"
	commonCarrionCrow    = "Carrion Crow"
	sciEurasianBlackbird = "Turdus merula"
)

// --- guide cache test doubles (the exported guideprovider interfaces) ---

type noopGuideMetrics struct{}

func (noopGuideMetrics) RecordCacheHit(_, _ string)           {}
func (noopGuideMetrics) RecordCacheMiss(_ string)             {}
func (noopGuideMetrics) RecordFetch(_, _ string, _ float64)   {}
func (noopGuideMetrics) RecordDBError(_, _ string)            {}
func (noopGuideMetrics) RecordNegativeEntry()                 {}
func (noopGuideMetrics) UpdateCachePopulationRatio(_ float64) {}

// emptyGuideStore is a GuideStore whose Get is always a miss, so the cache always
// falls through to the provider. Writes are no-ops.
type emptyGuideStore struct{}

func (emptyGuideStore) Get(_ context.Context, _, _, _ string) (*guideprovider.GuideCacheEntry, error) {
	return nil, guideprovider.ErrCacheEntryNotFound
}
func (emptyGuideStore) Save(_ context.Context, _ *guideprovider.GuideCacheEntry) error { return nil }
func (emptyGuideStore) GetAll(_ context.Context) ([]guideprovider.GuideCacheEntry, error) {
	return nil, nil
}
func (emptyGuideStore) GetRecent(_ context.Context, _ int) ([]guideprovider.GuideCacheEntry, error) {
	return nil, nil
}
func (emptyGuideStore) Delete(_ context.Context, _, _, _ string) error { return nil }
func (emptyGuideStore) DeleteAll(_ context.Context) error              { return nil }

// stubGuideProvider returns a fixed guide, or ErrGuideNotFound when result is nil.
type stubGuideProvider struct {
	result *guideprovider.SpeciesGuide
}

func (stubGuideProvider) Name() string { return guideprovider.WikipediaProviderName }

func (p stubGuideProvider) Fetch(_ context.Context, _ string, _ guideprovider.FetchOptions) (*guideprovider.SpeciesGuide, error) {
	if p.result == nil {
		return nil, guideprovider.ErrGuideNotFound
	}
	g := *p.result
	return &g, nil
}

// blockingGuideProvider blocks in Fetch until its (background) context is
// canceled. The detached Tier-3 fetch therefore never completes during a request,
// so a caller whose own request context is already canceled deterministically
// wins the select in GuideCache.Get and surfaces context.Canceled.
type blockingGuideProvider struct{}

func (blockingGuideProvider) Name() string { return guideprovider.WikipediaProviderName }

func (blockingGuideProvider) Fetch(ctx context.Context, _ string, _ guideprovider.FetchOptions) (*guideprovider.SpeciesGuide, error) {
	<-ctx.Done()
	return nil, ctx.Err()
}

// newStubGuideCache builds a real GuideCache wired to a stub provider. It runs no
// background loop (Start is not called) and is closed on cleanup.
func newStubGuideCache(t *testing.T, result *guideprovider.SpeciesGuide) *guideprovider.GuideCache {
	t.Helper()
	gc := guideprovider.NewGuideCache(emptyGuideStore{}, noopGuideMetrics{})
	gc.RegisterProvider(guideprovider.WikipediaProviderName, stubGuideProvider{result: result})
	t.Cleanup(gc.Close)
	return gc
}

// guideEnabledSettings returns settings with the species guide feature enabled.
func guideEnabledSettings() *conf.Settings {
	s := &conf.Settings{}
	s.Realtime.Dashboard.SpeciesGuide.Enabled = true
	return s
}

// guideTestController publishes s both globally (for currentSettings) and
// per-controller (for nil-safe error responses), restoring the global snapshot
// on cleanup. Tests using it must be serial (the global snapshot is process-wide).
func guideTestController(t *testing.T, s *conf.Settings) *Controller {
	t.Helper()
	withRestoredGlobalSettings(t)
	conftest.SetTestSettings(s)
	c := &Controller{Core: &apicore.Core{}}
	c.Settings.Store(s)
	return c
}

// guideCtx builds an Echo context for a guide/similar request with the
// scientific_name path param populated.
func guideCtx(t *testing.T, name string) (echo.Context, *httptest.ResponseRecorder) {
	t.Helper()
	e := echo.New()
	req := httptest.NewRequest(http.MethodGet, "/api/v2/species/x/guide", http.NoBody)
	rec := httptest.NewRecorder()
	ctx := e.NewContext(req, rec)
	ctx.SetParamNames(paramScientificName)
	ctx.SetParamValues(name)
	return ctx, rec
}

// --- GetSpeciesGuide ---

func TestGetSpeciesGuide_DisabledReturns404(t *testing.T) {
	c := guideTestController(t, &conf.Settings{}) // SpeciesGuide.Enabled == false
	ctx, rec := guideCtx(t, sciEurasianBlackbird)
	require.NoError(t, c.GetSpeciesGuide(ctx))
	assert.Equal(t, http.StatusNotFound, rec.Code)
}

func TestGetSpeciesGuide_CacheUnavailableReturns503(t *testing.T) {
	c := guideTestController(t, guideEnabledSettings()) // enabled, but no cache wired
	ctx, rec := guideCtx(t, sciEurasianBlackbird)
	require.NoError(t, c.GetSpeciesGuide(ctx))
	assert.Equal(t, http.StatusServiceUnavailable, rec.Code)
}

func TestGetSpeciesGuide_NotFoundReturns404(t *testing.T) {
	c := guideTestController(t, guideEnabledSettings())
	c.SetGuideCache(newStubGuideCache(t, nil)) // provider yields ErrGuideNotFound -> negative entry
	ctx, rec := guideCtx(t, "Nonexistent species")
	require.NoError(t, c.GetSpeciesGuide(ctx))
	assert.Equal(t, http.StatusNotFound, rec.Code)
}

func TestGetSpeciesGuide_SuccessReturns200(t *testing.T) {
	c := guideTestController(t, guideEnabledSettings())
	c.SetGuideCache(newStubGuideCache(t, &guideprovider.SpeciesGuide{
		CommonName:  commonBlackbird,
		Description: "A widespread thrush of gardens and woodland across much of Europe, Asia, and North Africa.",
	}))
	ctx, rec := guideCtx(t, sciEurasianBlackbird)
	require.NoError(t, c.GetSpeciesGuide(ctx))
	require.Equal(t, http.StatusOK, rec.Code)

	var data SpeciesGuideData
	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &data))
	assert.Equal(t, sciEurasianBlackbird, data.ScientificName)
	assert.Equal(t, commonBlackbird, data.CommonName)
}

// A client that disconnects mid-fetch (canceled request context) must get the
// client-closed status, not a misleading 502 Bad Gateway.
func TestGetSpeciesGuide_ClientCanceledReturns499(t *testing.T) {
	c := guideTestController(t, guideEnabledSettings())
	gc := guideprovider.NewGuideCache(emptyGuideStore{}, noopGuideMetrics{})
	gc.RegisterProvider(guideprovider.WikipediaProviderName, blockingGuideProvider{})
	t.Cleanup(gc.Close)
	c.SetGuideCache(gc)

	e := echo.New()
	reqCtx, cancel := context.WithCancel(t.Context())
	cancel() // client already gone before the handler runs
	req := httptest.NewRequest(http.MethodGet, "/api/v2/species/x/guide", http.NoBody).WithContext(reqCtx)
	rec := httptest.NewRecorder()
	ec := e.NewContext(req, rec)
	ec.SetParamNames(paramScientificName)
	ec.SetParamValues(sciEurasianBlackbird)

	require.NoError(t, c.GetSpeciesGuide(ec))
	assert.Equal(t, apicore.StatusClientClosedRequest, rec.Code)
}

func TestGetSpeciesGuide_EmptyNameReturns400(t *testing.T) {
	c := guideTestController(t, guideEnabledSettings())
	ctx, rec := guideCtx(t, "")
	require.NoError(t, c.GetSpeciesGuide(ctx))
	assert.Equal(t, http.StatusBadRequest, rec.Code)
}

// A non-name-shaped scientific_name is rejected at the boundary with a 400 before it
// can reach the providers (an outbound Wikipedia title) or the embedded-dataset memo,
// rather than being passed through and 404-ing later.
func TestGetSpeciesGuide_InvalidCharactersReturns400(t *testing.T) {
	c := guideTestController(t, guideEnabledSettings())
	// A cache wired here would only matter if the handler reached it; it must not.
	c.SetGuideCache(newStubGuideCache(t, &guideprovider.SpeciesGuide{Description: "should never be returned"}))
	for _, name := range []string{
		"Turdus merula; DROP TABLE",
		"../../etc/passwd",
		"<script>alert(1)</script>",
		"Turdus_merula", // underscore is not a scientific-name character
		strings.Repeat("a", scientificNameMaxLength+1),
	} {
		ctx, rec := guideCtx(t, name)
		require.NoError(t, c.GetSpeciesGuide(ctx))
		assert.Equalf(t, http.StatusBadRequest, rec.Code, "name %q should be rejected", name)
	}
}

func TestIsPlausibleScientificName(t *testing.T) {
	t.Parallel()
	valid := []string{
		sciEurasianBlackbird,          // "Turdus merula"
		"Larus argentatus argentatus", // trinomial
		"Phylloscopus collybita tristis",
		"Saxicola torquatus",
		"Anas platyrhynchos x Anas rubripes", // hybrid notation ("x" is a letter)
		"Œnanthe œnanthe",                    // non-ASCII letters
		"Power tools",                        // BirdNET non-species label
	}
	for _, s := range valid {
		assert.Truef(t, isPlausibleScientificName(s), "%q should be accepted", s)
	}
	invalid := []string{
		"", // empty is never plausible
		"Turdus_merula",
		"Turdus/merula",
		"Turdus merula 2",
		"name\twith\ttabs",
		"emoji 🦅",
		"<b>",
	}
	for _, s := range invalid {
		assert.Falsef(t, isPlausibleScientificName(s), "%q should be rejected", s)
	}
}

// --- GetSimilarSpecies (gating branches; full resolution needs a TaxonomyDB) ---

func TestGetSimilarSpecies_DisabledReturns404(t *testing.T) {
	c := guideTestController(t, &conf.Settings{})
	ctx, rec := guideCtx(t, sciEurasianBlackbird)
	require.NoError(t, c.GetSimilarSpecies(ctx))
	assert.Equal(t, http.StatusNotFound, rec.Code)
}

func TestGetSimilarSpecies_NoTaxonomyReturns503(t *testing.T) {
	c := guideTestController(t, guideEnabledSettings()) // c.TaxonomyDB is nil
	ctx, rec := guideCtx(t, sciEurasianBlackbird)
	require.NoError(t, c.GetSimilarSpecies(ctx))
	assert.Equal(t, http.StatusServiceUnavailable, rec.Code)
}

// A candidate whose guide resolves but carries no prose (an OpenFauna stub when
// Wikipedia is disabled or the species was never warmed) must NOT be marked
// has_guide, and (with enrichments on) must carry localized resource links so the
// picker rail can offer links instead of an empty comparison card.
func TestResolveSimilarSpecies_StubWithoutDescriptionGetsLocalizedLinks(t *testing.T) {
	c := guideTestController(t, guideEnabledSettings())
	c.SetGuideCache(newStubGuideCache(t, &guideprovider.SpeciesGuide{
		CommonName:  commonCarrionCrow,
		Description: "",
	}))
	entries := c.resolveSimilarSpecies(
		t.Context(),
		[]similarCandidate{{scientificName: sciCarrionCrow, relationship: relationshipSameGenus}},
		"de", true, false, // locale=de, enrichments on, supplementary off
	)
	require.Len(t, entries, 1)
	assert.False(t, entries[0].HasGuide, "stub guide with empty description must not be has_guide")
	require.NotEmpty(t, entries[0].ExternalLinks, "description-less entry should carry resource links")
	var foundLocalizedWiki bool
	for _, l := range entries[0].ExternalLinks {
		if l.Icon == "wikipedia" {
			assert.Contains(t, l.URL, "dewiki", "Wikipedia link should localize to the German locale")
			foundLocalizedWiki = true
		}
	}
	assert.True(t, foundLocalizedWiki, "expected a Wikipedia link in the fallback set")
}

// With enrichments off, a description-less entry carries no links (links are
// enrichment data, consistent with the main guide modal).
func TestResolveSimilarSpecies_StubWithEnrichmentsOffHasNoLinks(t *testing.T) {
	c := guideTestController(t, guideEnabledSettings())
	c.SetGuideCache(newStubGuideCache(t, &guideprovider.SpeciesGuide{
		CommonName:  commonCarrionCrow,
		Description: "",
	}))
	entries := c.resolveSimilarSpecies(
		t.Context(),
		[]similarCandidate{{scientificName: sciCarrionCrow, relationship: relationshipSameGenus}},
		defaultWikiLang, false, false, // enrichments off, supplementary off
	)
	require.Len(t, entries, 1)
	assert.False(t, entries[0].HasGuide)
	assert.Empty(t, entries[0].ExternalLinks, "no links when enrichments are off")
}

func TestResolveSimilarSpecies_WithDescriptionIsMarkedHasGuideAndNoLinks(t *testing.T) {
	c := guideTestController(t, guideEnabledSettings())
	c.SetGuideCache(newStubGuideCache(t, &guideprovider.SpeciesGuide{
		CommonName:  commonCarrionCrow,
		Description: "The carrion crow is a passerine bird of the family Corvidae.",
	}))
	entries := c.resolveSimilarSpecies(
		t.Context(),
		[]similarCandidate{{scientificName: sciCarrionCrow, relationship: relationshipSameGenus}},
		defaultWikiLang, true, false, // enrichments on, supplementary off
	)
	require.Len(t, entries, 1)
	assert.True(t, entries[0].HasGuide, "guide with prose must be has_guide")
	assert.Equal(t, commonCarrionCrow, entries[0].CommonName)
	assert.Empty(t, entries[0].ExternalLinks, "described entry should not carry fallback links")
}

// --- Notes (no global-settings dependency, so these can run in parallel) ---

// notesController returns a controller backed by a fresh mock datastore.
func notesController(t *testing.T) (*Controller, *mocks.MockInterface) {
	t.Helper()
	mockDS := mocks.NewMockInterface(t)
	c := &Controller{Core: &apicore.Core{DS: mockDS}}
	c.Settings.Store(&conf.Settings{})
	return c, mockDS
}

// notesCtx builds an Echo context for a notes request, setting the named path
// params from the supplied key/value pairs.
func notesCtx(t *testing.T, method, target, body string, params map[string]string) (echo.Context, *httptest.ResponseRecorder) {
	t.Helper()
	e := echo.New()
	var req *http.Request
	if body == "" {
		req = httptest.NewRequest(method, target, http.NoBody)
	} else {
		req = httptest.NewRequest(method, target, strings.NewReader(body))
		req.Header.Set(echo.HeaderContentType, echo.MIMEApplicationJSON)
	}
	rec := httptest.NewRecorder()
	ctx := e.NewContext(req, rec)
	names := make([]string, 0, len(params))
	values := make([]string, 0, len(params))
	for k, v := range params {
		names = append(names, k)
		values = append(values, v)
	}
	ctx.SetParamNames(names...)
	ctx.SetParamValues(values...)
	return ctx, rec
}

func TestGetSpeciesNotes_Returns200(t *testing.T) {
	t.Parallel()
	c, mockDS := notesController(t)
	mockDS.EXPECT().GetSpeciesNotes(mock.Anything, sciEurasianBlackbird).Return(
		[]datastore.SpeciesNote{{ID: 1, ScientificName: sciEurasianBlackbird, Entry: "Seen at dawn", CreatedAt: time.Now(), UpdatedAt: time.Now()}},
		nil).Once()

	ctx, rec := notesCtx(t, http.MethodGet, "/api/v2/species/Turdus/notes", "",
		map[string]string{paramScientificName: sciEurasianBlackbird})
	require.NoError(t, c.GetSpeciesNotes(ctx))
	require.Equal(t, http.StatusOK, rec.Code)

	var notes []SpeciesNoteData
	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &notes))
	require.Len(t, notes, 1)
	assert.Equal(t, "Seen at dawn", notes[0].Entry)
}

func TestGetSpeciesNotes_DatastoreUnavailableReturns503(t *testing.T) {
	t.Parallel()
	c := &Controller{Core: &apicore.Core{}} // DS nil
	c.Settings.Store(&conf.Settings{})

	ctx, rec := notesCtx(t, http.MethodGet, "/api/v2/species/Turdus/notes", "",
		map[string]string{paramScientificName: sciEurasianBlackbird})
	// requireDatastore writes the 503 response AND returns the error, so the
	// handler returns non-nil here; assert on the written status, not the return.
	_ = c.GetSpeciesNotes(ctx)
	assert.Equal(t, http.StatusServiceUnavailable, rec.Code)
}

func TestCreateSpeciesNote_Returns201(t *testing.T) {
	t.Parallel()
	c, mockDS := notesController(t)
	mockDS.EXPECT().SaveSpeciesNote(mock.Anything, mock.AnythingOfType("*datastore.SpeciesNote")).
		Return(nil).Once()

	ctx, rec := notesCtx(t, http.MethodPost, "/api/v2/species/Turdus/notes", `{"entry":"A note"}`,
		map[string]string{paramScientificName: sciEurasianBlackbird})
	require.NoError(t, c.CreateSpeciesNote(ctx))
	assert.Equal(t, http.StatusCreated, rec.Code)
}

func TestCreateSpeciesNote_TooLongReturns400WithKey(t *testing.T) {
	t.Parallel()
	c, mockDS := notesController(t)
	mockDS.EXPECT().SaveSpeciesNote(mock.Anything, mock.Anything).
		Return(datastore.ErrSpeciesNoteTooLong).Once()

	ctx, rec := notesCtx(t, http.MethodPost, "/api/v2/species/Turdus/notes", `{"entry":"too long"}`,
		map[string]string{paramScientificName: sciEurasianBlackbird})
	require.NoError(t, c.CreateSpeciesNote(ctx))
	require.Equal(t, http.StatusBadRequest, rec.Code)

	var body ErrorResponse
	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &body))
	assert.Equal(t, "analytics.species.notes.tooLong", body.ErrorKey)
}

// A validation error that is NOT the too-long case (e.g. an empty entry or an
// invalid note ID) must return 400 but must NOT be mislabeled with the "too long"
// key — that conflation was the bug this mapping fixes.
func TestCreateSpeciesNote_OtherValidationErrorReturns400WithoutTooLongKey(t *testing.T) {
	t.Parallel()
	c, mockDS := notesController(t)
	valErr := errors.Newf("entry cannot be empty").
		Component("datastore").Category(errors.CategoryValidation).Build()
	mockDS.EXPECT().SaveSpeciesNote(mock.Anything, mock.Anything).Return(valErr).Once()

	ctx, rec := notesCtx(t, http.MethodPost, "/api/v2/species/Turdus/notes", `{"entry":"x"}`,
		map[string]string{paramScientificName: sciEurasianBlackbird})
	require.NoError(t, c.CreateSpeciesNote(ctx))
	require.Equal(t, http.StatusBadRequest, rec.Code)

	var body ErrorResponse
	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &body))
	assert.NotEqual(t, "analytics.species.notes.tooLong", body.ErrorKey)
}

func TestUpdateSpeciesNote_NotFoundReturns404(t *testing.T) {
	t.Parallel()
	c, mockDS := notesController(t)
	mockDS.EXPECT().UpdateSpeciesNote(mock.Anything, "42", "edited").
		Return(datastore.ErrSpeciesNoteNotFound).Once()

	ctx, rec := notesCtx(t, http.MethodPut, "/api/v2/species/notes/42", `{"entry":"edited"}`,
		map[string]string{"id": "42"})
	require.NoError(t, c.UpdateSpeciesNote(ctx))
	assert.Equal(t, http.StatusNotFound, rec.Code)
}

func TestUpdateSpeciesNote_EmptyIDReturns400(t *testing.T) {
	t.Parallel()
	c, _ := notesController(t) // no DS call expected: empty id is rejected first
	ctx, rec := notesCtx(t, http.MethodPut, "/api/v2/species/notes/", `{"entry":"edited"}`,
		map[string]string{"id": ""})
	require.NoError(t, c.UpdateSpeciesNote(ctx))
	assert.Equal(t, http.StatusBadRequest, rec.Code)
}

func TestDeleteSpeciesNote_NotFoundReturns404(t *testing.T) {
	t.Parallel()
	c, mockDS := notesController(t)
	mockDS.EXPECT().DeleteSpeciesNote(mock.Anything, "42").
		Return(datastore.ErrSpeciesNoteNotFound).Once()

	ctx, rec := notesCtx(t, http.MethodDelete, "/api/v2/species/notes/42", "",
		map[string]string{"id": "42"})
	require.NoError(t, c.DeleteSpeciesNote(ctx))
	assert.Equal(t, http.StatusNotFound, rec.Code)
}

// --- auth gating (route-level: writes require auth, reads are public) ---

func TestSpeciesGuideRoutes_NotesAreAuthGated(t *testing.T) {
	t.Parallel()
	e := echo.New()
	c := &Controller{Core: &apicore.Core{Echo: e, Group: e.Group("/api/v2")}}
	c.Settings.Store(&conf.Settings{})
	// A denying middleware stands in for the real authenticator: it short-circuits
	// before the handler, so a gated route returns 401 without touching the DS.
	c.AuthMiddleware = func(echo.HandlerFunc) echo.HandlerFunc {
		return func(ctx echo.Context) error {
			return ctx.NoContent(http.StatusUnauthorized)
		}
	}
	c.initSpeciesGuideRoutes()

	// All notes endpoints — reads included — are auth-gated, because notes are
	// user-authored and may hold sensitive content. The concrete 401 (vs a 404)
	// also confirms each route is registered and reachable.
	gated := []struct{ method, target, body string }{
		{http.MethodGet, "/api/v2/species/Turdus/notes", ""},
		{http.MethodPost, "/api/v2/species/Turdus/notes", `{"entry":"x"}`},
		{http.MethodPut, "/api/v2/species/notes/1", `{"entry":"x"}`},
		{http.MethodDelete, "/api/v2/species/notes/1", ""},
	}
	for _, g := range gated {
		t.Run(g.method+" "+g.target, func(t *testing.T) {
			t.Parallel()
			var body io.Reader = http.NoBody
			if g.body != "" {
				body = strings.NewReader(g.body)
			}
			req := httptest.NewRequest(g.method, g.target, body)
			req.Header.Set(echo.HeaderContentType, echo.MIMEApplicationJSON)
			rec := httptest.NewRecorder()
			e.ServeHTTP(rec, req)
			assert.Equalf(t, http.StatusUnauthorized, rec.Code, "%s %s must be auth-gated", g.method, g.target)
		})
	}
}
