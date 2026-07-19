// test_helpers_test.go: shared scaffolding for the analytics domain tests.
//
// Core-level scaffolding (settings builder, mock metrics/image cache, the
// importable test image provider) lives in the internal/api/v2/apitest package.
// The helpers here build an analytics Handler around a minimal *apicore.Core,
// mirroring the package-api setupAnalyticsTestEnvironment the tests used before
// the domain was extracted: a Group + mock datastore + valid settings, with nil
// BirdImageCache/SunCalc/Metrics so a test that needs them sets the promoted Core
// field itself. The facade-injected dependencies (the auth check and the name-map
// accessors) get in-memory doubles: isClientAuthenticated defaults to
// unauthenticated and the name-map accessors return the supplied maps so a test
// can seed name resolution without the facade name-map plumbing (which is tested
// in its own package-api tests).
package analytics

import (
	"testing"

	"github.com/labstack/echo/v4"
	"github.com/stretchr/testify/assert"

	"github.com/tphakala/birdnet-go/internal/api/v2/apicore"
	"github.com/tphakala/birdnet-go/internal/api/v2/apitest"
	"github.com/tphakala/birdnet-go/internal/datastore/mocks"
)

// apiV2Prefix mirrors the facade's /api/v2 route-group prefix so handler tests
// register routes the same way the facade does.
const apiV2Prefix = "/api/v2"

// Test coordinates (Helsinki, Finland) for the SunCalc-dependent analytics tests.
const (
	testHelsinkiLatitude  = 60.1699
	testHelsinkiLongitude = 24.9384
)

// Finnish bat name fixtures: "mopsilepakko" is the localized common name for the
// secondary-model species "Barbastella barbastellus", whose label carries no
// embedded common name, so it only becomes resolvable via the reverse name map.
const (
	batCommonName     = "mopsilepakko"
	batScientificName = "Barbastella barbastellus"
)

// newTestHandler builds an analytics Handler around the supplied core with empty
// name maps and an unauthenticated auth check.
func newTestHandler(core *apicore.Core) *Handler {
	return newTestHandlerWithMaps(core, map[string]string{}, map[string]string{})
}

// newTestHandlerWithMaps builds an analytics Handler with the supplied name-map
// snapshots injected (sciToCommon for localization, commonToSci for the reverse
// species-query resolution) and an unauthenticated auth check.
func newTestHandlerWithMaps(core *apicore.Core, sciToCommon, commonToSci map[string]string) *Handler {
	return New(core,
		func(echo.Context) bool { return false },
		func() map[string]string { return sciToCommon },
		func() map[string]string { return commonToSci },
	)
}

// setupAnalyticsTestEnvironment builds an Echo, a mock datastore, and an analytics
// Handler wired through a minimal core (Group + datastore + valid settings, nil
// image cache). It mirrors the package-api helper of the same name that returned a
// *Controller before the domain was extracted.
func setupAnalyticsTestEnvironment(t *testing.T) (*echo.Echo, *mocks.MockInterface, *Handler) {
	t.Helper()
	e := echo.New()
	mockDS := mocks.NewMockInterface(t)
	core := &apicore.Core{Group: e.Group(apiV2Prefix), DS: mockDS}
	core.Settings.Store(apitest.NewValidTestSettings())
	return e, mockDS, newTestHandler(core)
}

// setupAnalyticsTestEnvironmentWithBatName is setupAnalyticsTestEnvironment with the
// name maps pre-seeded so the Finnish bat common name "mopsilepakko" resolves to
// "Barbastella barbastellus". It replaces the package-api tests' facade
// SetNameResolver + UpdateCommonNameMap setup, exercising the same observable
// name-resolution behavior (the map-builder wiring itself is covered by the
// package-api name-map tests).
func setupAnalyticsTestEnvironmentWithBatName(t *testing.T) (*echo.Echo, *mocks.MockInterface, *Handler) {
	t.Helper()
	e := echo.New()
	mockDS := mocks.NewMockInterface(t)
	core := &apicore.Core{Group: e.Group(apiV2Prefix), DS: mockDS}
	core.Settings.Store(apitest.NewValidTestSettings())
	commonToSci := map[string]string{apicore.NormalizeForLookup(batCommonName): batScientificName}
	sciToCommon := map[string]string{batScientificName: batCommonName}
	return e, mockDS, newTestHandlerWithMaps(core, sciToCommon, commonToSci)
}

// SpeciesDailySummaryExpected contains expected values for species daily summary assertions.
type SpeciesDailySummaryExpected struct {
	CommonName          string
	SpeciesCode         string
	Count               int
	HourlyCounts        []int
	FirstHeard          string
	LatestHeard         string
	HighConfidence      bool
	ThumbnailURLContain string // Substring that ThumbnailURL should contain
}

// assertSpeciesDailySummary verifies that a SpeciesDailySummary matches expected values.
// This reduces duplication in tests that verify species summary fields.
func assertSpeciesDailySummary(t *testing.T, species *SpeciesDailySummary, expected *SpeciesDailySummaryExpected) {
	t.Helper()

	assert.Equal(t, expected.CommonName, species.CommonName, "%s common name mismatch", expected.CommonName)
	assert.Equal(t, expected.SpeciesCode, species.SpeciesCode, "%s species code mismatch", expected.CommonName)
	assert.Equal(t, expected.Count, species.Count, "%s count mismatch", expected.CommonName)
	assert.Equal(t, expected.HourlyCounts, species.HourlyCounts, "%s hourly counts mismatch", expected.CommonName)
	assert.Equal(t, expected.FirstHeard, species.FirstHeard, "%s first heard time mismatch", expected.CommonName)
	assert.Equal(t, expected.LatestHeard, species.LatestHeard, "%s latest heard time mismatch", expected.CommonName)
	assert.Equal(t, expected.HighConfidence, species.HighConfidence, "%s high confidence mismatch", expected.CommonName)
	assert.Contains(t, species.ThumbnailURL, expected.ThumbnailURLContain, "%s thumbnail URL mismatch", expected.CommonName)
}

type LimitClampTestCase struct {
	Name      string
	LimitParm string
	WantLimit int
}

func runLimitClampTests(
	t *testing.T,
	tests []LimitClampTestCase,
	run func(t *testing.T, tc LimitClampTestCase),
) {
	t.Helper()

	for _, tc := range tests {
		t.Run(tc.Name, func(t *testing.T) {
			t.Parallel()
			run(t, tc)
		})
	}
}