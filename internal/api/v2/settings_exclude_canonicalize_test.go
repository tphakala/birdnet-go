package api

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/labstack/echo/v4"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/tphakala/birdnet-go/internal/conf"
)

// Shared fixture names so the resolver map and the assertions cannot drift apart:
// the localized common name resolves to the scientific name.
const (
	testExcludeLocalizedName  = "mopsilepakko"
	testExcludeScientificName = "Barbastella barbastellus"
)

// installExcludeTestResolver wires a batch-capable fake resolver and rebuilds the
// controller's common-name map so the localized name resolves to its scientific
// name, mirroring the detection-side localized-name tests.
func installExcludeTestResolver(t *testing.T, c *Controller) {
	t.Helper()
	c.SetNameResolver(&analyticsBatchFakeResolver{batch: map[string]string{
		testExcludeScientificName: testExcludeLocalizedName,
	}})
	c.UpdateCommonNameMap([]string{testExcludeScientificName})
}

// patchSection drives UpdateSectionSettings for an arbitrary section. It asserts
// HTTP 200: HandleError returns a nil error after writing a 4xx/5xx body, so a
// rejected save would otherwise pass require.NoError and surface only as a
// confusing downstream mismatch.
func patchSection(t *testing.T, e *echo.Echo, c *Controller, section string, payload any) *httptest.ResponseRecorder {
	t.Helper()
	data, err := json.Marshal(payload)
	require.NoError(t, err)
	req := httptest.NewRequest(http.MethodPatch, "/api/v2/settings/"+section, bytes.NewReader(data))
	req.Header.Set(echo.HeaderContentType, echo.MIMEApplicationJSON)
	rec := httptest.NewRecorder()
	ctx := e.NewContext(req, rec)
	ctx.SetParamNames("section")
	ctx.SetParamValues(section)
	require.NoError(t, c.UpdateSectionSettings(ctx))
	require.Equal(t, http.StatusOK, rec.Code, "section save must succeed; body: %s", rec.Body.String())
	return rec
}

// putFullSettings drives UpdateSettings (PUT /api/v2/settings) with a full
// settings snapshot, matching what the frontend settings page sends. Asserts
// HTTP 200 for the same reason patchSection does.
func putFullSettings(t *testing.T, e *echo.Echo, c *Controller, s *conf.Settings) *httptest.ResponseRecorder {
	t.Helper()
	body, err := json.Marshal(s)
	require.NoError(t, err)
	req := httptest.NewRequest(http.MethodPut, "/api/v2/settings", bytes.NewReader(body))
	req.Header.Set(echo.HeaderContentType, echo.MIMEApplicationJSON)
	rec := httptest.NewRecorder()
	require.NoError(t, c.UpdateSettings(e.NewContext(req, rec)))
	require.Equal(t, http.StatusOK, rec.Code, "full settings save must succeed; body: %s", rec.Body.String())
	return rec
}

// TestCanonicalizeExcludeList unit-tests the resolution + dedup + cleanup helper
// directly, independent of the HTTP save handlers.
func TestCanonicalizeExcludeList(t *testing.T) {
	t.Parallel()

	c := &Controller{}
	installExcludeTestResolver(t, c)

	tests := []struct {
		name  string
		input []string
		want  []string
	}{
		{
			name:  "nil passthrough",
			input: nil,
			want:  nil,
		},
		{
			name:  "empty passthrough",
			input: []string{},
			want:  []string{},
		},
		{
			name:  "localized name resolved to scientific",
			input: []string{testExcludeLocalizedName},
			want:  []string{testExcludeScientificName},
		},
		{
			name:  "scientific name passes through unchanged",
			input: []string{testExcludeScientificName},
			want:  []string{testExcludeScientificName},
		},
		{
			name:  "unknown common name passes through trimmed",
			input: []string{"  American Crow  "},
			want:  []string{"American Crow"},
		},
		{
			name:  "whitespace-only entries dropped",
			input: []string{"   ", "American Crow", ""},
			want:  []string{"American Crow"},
		},
		{
			// A non-empty input whose entries all drop returns a non-nil empty
			// slice (distinct from the nil/empty passthrough path above).
			name:  "all entries dropped yields empty slice",
			input: []string{"   ", ""},
			want:  []string{},
		},
		{
			name:  "mixed localized + scientific de-duplicated to one scientific",
			input: []string{testExcludeLocalizedName, testExcludeScientificName},
			want:  []string{testExcludeScientificName},
		},
		{
			name:  "case-insensitive dedup of passthrough names keeps first occurrence",
			input: []string{"American Crow", "american crow"},
			want:  []string{"American Crow"},
		},
		{
			name:  "order preserved across resolved and passthrough entries",
			input: []string{"American Crow", testExcludeLocalizedName, "Blue Jay"},
			want:  []string{"American Crow", testExcludeScientificName, "Blue Jay"},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			got := c.canonicalizeExcludeList(tt.input)
			assert.Equal(t, tt.want, got)

			// Idempotency: canonicalizing an already-canonical list is a no-op.
			again := c.canonicalizeExcludeList(got)
			assert.Equal(t, got, again, "canonicalization must be idempotent")
		})
	}
}

// TestUpdateSectionSettingsCanonicalizesExclude verifies the PATCH
// /api/v2/settings/realtime path stores scientific names regardless of the form
// the user typed, and de-duplicates mixed forms.
func TestUpdateSectionSettingsCanonicalizesExclude(t *testing.T) {
	e, _, controller := setupTestEnvironment(t)
	clearExcludedSpeciesList(t, controller.Settings.Load())
	installExcludeTestResolver(t, controller)

	patchSection(t, e, controller, SettingsSectionRealtime, map[string]any{
		"species": map[string]any{
			"exclude": []string{testExcludeLocalizedName, testExcludeScientificName, "  American Crow  ", "  "},
		},
	})

	got := controller.Settings.Load().Realtime.Species.Exclude
	assert.Equal(t, []string{testExcludeScientificName, "American Crow"}, got,
		"localized entry resolved, scientific dup removed, name trimmed, blank dropped")
}

// TestUpdateSectionSettingsCanonicalizesExcludeViaSpeciesSection verifies the
// species section PATCH path is canonicalized too (it also carries the list).
func TestUpdateSectionSettingsCanonicalizesExcludeViaSpeciesSection(t *testing.T) {
	e, _, controller := setupTestEnvironment(t)
	clearExcludedSpeciesList(t, controller.Settings.Load())
	installExcludeTestResolver(t, controller)

	patchSection(t, e, controller, SettingsSectionSpecies, map[string]any{
		"exclude": []string{testExcludeLocalizedName},
	})

	got := controller.Settings.Load().Realtime.Species.Exclude
	assert.Equal(t, []string{testExcludeScientificName}, got)
}

// TestUpdateSettingsCanonicalizesExclude verifies the full-settings PUT path (the
// one the frontend settings page actually uses) canonicalizes the exclude list.
func TestUpdateSettingsCanonicalizesExclude(t *testing.T) {
	e, _, controller := setupTestEnvironment(t)
	clearExcludedSpeciesList(t, controller.Settings.Load())
	installExcludeTestResolver(t, controller)

	// Send a full snapshot with a localized exclude entry, as the settings page does.
	s := conf.CloneSettings(controller.Settings.Load())
	s.Realtime.Species.Exclude = []string{testExcludeLocalizedName, "American Crow"}
	putFullSettings(t, e, controller, s)

	// Exact assertion (order + dedup + count), matching the section tests.
	got := controller.Settings.Load().Realtime.Species.Exclude
	assert.Equal(t, []string{testExcludeScientificName, "American Crow"}, got)
}

// TestUpdateSectionSettingsSkipsExcludeForUnrelatedSection verifies that saving a
// section that does not carry the exclude list leaves a legacy (non-canonical)
// entry untouched, so an unrelated save does not rewrite the list or trigger a
// spurious range-filter rebuild.
func TestUpdateSectionSettingsSkipsExcludeForUnrelatedSection(t *testing.T) {
	e, _, controller := setupTestEnvironment(t)
	installExcludeTestResolver(t, controller)

	// Simulate a pre-upgrade exclude list stored as a localized common name.
	controller.Settings.Load().Realtime.Species.Exclude = []string{testExcludeLocalizedName}

	// Saving the audio section must not canonicalize the exclude list (patchSection
	// asserts the save returned 200, so this also proves the no-op save succeeded).
	patchSection(t, e, controller, SettingsSectionAudio, map[string]any{})

	got := controller.Settings.Load().Realtime.Species.Exclude
	assert.Equal(t, []string{testExcludeLocalizedName}, got,
		"unrelated section save must leave the legacy exclude entry untouched")
}
