// species_info_test.go: shared fixtures and end-to-end rarity tests for
// GET /api/v2/species.
//
// The fixtures are deterministic and self-contained: the species-name snapshot is
// built from the embedded v2.4 label corpus plus the inline Perch- and bat-like fixtures
// (so the openfauna alias memo maps are the real ones, exercising the Streptopelia/
// Spilopelia alias pair and the Dicrurus colliding pair), and a stub backend injects
// fixed rarity contexts so no native model is loaded and no network is touched.
package species

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"net/url"
	"slices"
	"strings"
	"testing"
	"time"
	"unicode/utf8"

	"github.com/labstack/echo/v4"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/tphakala/birdnet-go/internal/api/v2/apitest"
	"github.com/tphakala/birdnet-go/internal/classifier"
	"github.com/tphakala/birdnet-go/internal/conf"
	"github.com/tphakala/birdnet-go/internal/detection"
	"github.com/tphakala/birdnet-go/internal/speciesindex"
)

// TestCorpusScientificNamesAreASCII pins the assumption resolveSpeciesLabel relies
// on: scientific names in every label set are ASCII, so strings.EqualFold (exact
// pass) and strings.ToLower via CanonicalKey (the candidate group) agree. A dataset
// refresh that introduced a non-ASCII scientific name would break the
// exact-before-alias equivalence silently, so fail loudly here instead.
func TestCorpusScientificNamesAreASCII(t *testing.T) {
	t.Parallel()
	for _, label := range labelCorpus(t) {
		sci := detection.ExtractScientificName(label)
		for i := range len(sci) {
			require.Less(t, sci[i], byte(utf8.RuneSelf),
				"non-ASCII byte in scientific name %q (from label %q)", sci, label)
		}
	}
}

// Names of the rarity contexts rarityContexts builds, and the tolerances the rarity
// assertions compare floats with. The threshold is stored as a float32, so its
// tolerance is wider than the others.
const (
	rarityUniversal = "universal"
	rarityOverride  = "override"
	rarityLegacy    = "legacy"
	rarityInactive  = "inactive"

	scoreTolerance      = 1e-9
	coordinateTolerance = 1e-9
	thresholdTolerance  = 1e-6
)

// Location and range-filter threshold configured by locatedSettings.
const (
	fixtureLatitude       = 60.1699
	fixtureLongitude      = 24.9384
	fixtureRangeThreshold = 0.03
)

// Scientific names used to build the synthetic rarity contexts and the requests.
// The alias and colliding pairs are the same real OpenFauna cases the unit tests
// use, so the handler tests exercise the exact-before-alias rule end to end.
const (
	sciRobin        = "Turdus migratorius"        // scored, in the corpus
	sciBlackbird    = "Turdus merula"             // in the corpus and the Perch set
	sciCanonDove    = "Spilopelia senegalensis"   // canonical; the corpus label is legacy
	sciLegacyDove   = "Streptopelia senegalensis" // legacy; the form the corpus ships
	sciDrongoA      = "Dicrurus adsimilis"        // colliding pair member A
	sciDrongoB      = "Dicrurus divaricatus"      // colliding pair member B (alias of A)
	sciBat          = "Myotis daubentonii"        // secondary-model only (bat-like fixture)
	sciPerchOnly    = "Tachyspiza badia"          // scientific-only Perch label (localized via backend)
	sciGeomodelOnly = "Zzz Geomodelonly"          // in the geomodel vocabulary, not the classifier
)

// stubBackend is a deterministic speciesBackend fake: a fixed localized-name map
// for resolveEitherName and a fixed RarityContext for the rarity lookup.
type stubBackend struct {
	names map[string]string
	rc    classifier.RarityContext
}

func (b *stubBackend) ResolveName(scientificName, _ string) string {
	return b.names[scientificName]
}

func (b *stubBackend) GetRarityContext(_ time.Time) (classifier.RarityContext, error) {
	return b.rc, nil
}

// syntheticLabel wraps a scientific name in a throwaway "Sci_Common" label. Only
// the scientific part drives coverage and score matching, so the common part is
// arbitrary; using a distinct marker keeps these labels out of the real corpus.
func syntheticLabel(sci string) string { return sci + "_syn" }

// locatedSettings returns a settings snapshot with a configured location so the
// rarity coordinates and threshold serialize deterministically.
func locatedSettings() *conf.Settings {
	s := &conf.Settings{}
	s.BirdNET.LocationConfigured = true
	s.BirdNET.Latitude = fixtureLatitude
	s.BirdNET.Longitude = fixtureLongitude
	s.BirdNET.RangeFilter.Threshold = fixtureRangeThreshold
	return s
}

// Secondary-model label fixtures, kept in step with internal/speciesindex/testdata/perch_like_labels.txt
// and bat_like_labels.txt. Perch labels carry only a scientific name; bat labels carry an
// embedded common name, so the two exercise both localization branches.
var (
	perchLikeLabels = []string{
		"Turdus merula",
		"Passer domesticus",
		"Accipiter badius",
		"Tachyspiza badia",
		"Corvus corone",
		"Turdus merula",
		"Fringilla coelebs",
	}
	batLikeLabels = []string{
		"Myotis daubentonii_Water Bat",
		"Pipistrellus pipistrellus_Common Pipistrelle",
		"Nyctalus noctula_Noctule",
		"Eptesicus serotinus_Serotine",
		"Myotis nattereri_Natterer's Bat",
		"Barbastella barbastellus_Barbastelle",
		"Plecotus auritus_Long-eared Bat",
		"Plecotus austriacus_Long-eared Bat",
	}
)

// labelCorpus returns the embedded v2.4 corpus plus the Perch- and bat-like fixtures,
// concatenated. The union exercises embedded-common (v2.4/bat) and scientific-only
// (Perch) labels and the real alias pairs.
func labelCorpus(tb testing.TB) []string {
	tb.Helper()
	// The English (UK) label file of the embedded v2.4 corpus.
	const corpusLocale = "en-uk"
	res := classifier.GetLabelFileDataWithResult(classifier.BirdNET_V2_4, corpusLocale, nil)
	require.NoError(tb, res.Error)
	require.False(tb, res.FallbackOccurred, "the %s label file must load without a fallback", corpusLocale)

	var corpus []string
	for line := range strings.Lines(string(res.Data)) {
		if line = strings.TrimSpace(line); line != "" {
			corpus = append(corpus, line)
		}
	}
	return slices.Concat(corpus, perchLikeLabels, batLikeLabels)
}

// rarityContexts returns the fixed rarity contexts. Each name selects a distinct
// code path through computeRarity.
func rarityContexts(classifierLabels []string) map[string]classifier.RarityContext {
	settings := locatedSettings()

	// Universal geomodel: a vocabulary covering both alias-pair members, both
	// colliding members, robin, blackbird and a geomodel-only species, with a
	// descending score list for a subset.
	universalVocab := []string{
		syntheticLabel(sciRobin),
		syntheticLabel(sciBlackbird),
		syntheticLabel(sciLegacyDove),
		syntheticLabel(sciCanonDove),
		syntheticLabel(sciDrongoA),
		syntheticLabel(sciDrongoB),
		syntheticLabel(sciGeomodelOnly),
	}
	universalScores := []classifier.SpeciesScore{
		{Label: syntheticLabel(sciRobin), Score: 0.9},
		{Label: syntheticLabel(sciDrongoA), Score: 0.7},
		{Label: syntheticLabel(sciBlackbird), Score: 0.3},
		{Label: syntheticLabel(sciLegacyDove), Score: 0.1},
		{Label: syntheticLabel(sciDrongoB), Score: 0.05},
	}

	// Override: the universal context plus the rows addUserOverrideSpeciesScores
	// appends. The synthetic 1.0 rows are placed BEFORE the native scores on
	// purpose: robin's synthetic 1.0 precedes its native 0.9, so the expected 0.9
	// only holds if findNativeSpeciesScore skips the synthetic row in the exact pass
	// (without the skip the 1.0 sentinel would win first and the score would flip to
	// 1.0). A synthetic 1.0 for a species outside the vocabulary (the bat) must not
	// read as covered.
	overrideScores := make([]classifier.SpeciesScore, 0, len(universalScores)+2)
	overrideScores = append(overrideScores,
		classifier.SpeciesScore{Label: syntheticLabel(sciRobin), Score: 1.0, IsSyntheticOverride: true, IsManuallyIncluded: true},
		classifier.SpeciesScore{Label: syntheticLabel(sciBat), Score: 1.0, IsSyntheticOverride: true, IsManuallyIncluded: true},
	)
	overrideScores = append(overrideScores, universalScores...)

	// Legacy backend: no geomodel vocabulary, so coverage falls back to the
	// classifier labels.
	legacyScores := []classifier.SpeciesScore{
		{Label: syntheticLabel(sciRobin), Score: 0.6},
		{Label: syntheticLabel(sciBlackbird), Score: 0.2},
	}

	return map[string]classifier.RarityContext{
		rarityUniversal: {
			Scores:           universalScores,
			Geomodel:         classifier.NewLabelVocabulary(universalVocab),
			ClassifierLabels: classifierLabels,
			FilterActive:     true,
			Settings:         settings,
		},
		rarityOverride: {
			Scores:           overrideScores,
			Geomodel:         classifier.NewLabelVocabulary(universalVocab),
			ClassifierLabels: classifierLabels,
			FilterActive:     true,
			Settings:         settings,
		},
		rarityLegacy: {
			Scores:           legacyScores,
			Geomodel:         nil,
			ClassifierLabels: classifierLabels,
			FilterActive:     true,
			Settings:         settings,
		},
		rarityInactive: {
			Scores:           nil,
			Geomodel:         nil,
			ClassifierLabels: classifierLabels,
			FilterActive:     false,
			Settings:         settings,
		},
	}
}

// speciesInfoCase is one request through GetSpeciesInfo and the response fields that
// matter: whether the species is found, the resolved common name and the rarity
// block.
type speciesInfoCase struct {
	query        string
	wantNotFound bool
	wantCommon   string
	wantRarity   RarityStatus
	wantScore    float64
}

// TestGetSpeciesInfo_Rarity drives GetSpeciesInfo end to end against the real label
// corpus and a stub range-filter backend. Each context selects a different code path
// through name resolution and rarity: an active universal geomodel, user overrides
// layered over it, a legacy filter with no geomodel vocabulary, and an inactive filter.
func TestGetSpeciesInfo_Rarity(t *testing.T) {
	// Not parallel: apitest.NewCore publishes settings to the process-global
	// snapshot, and the handler reads CurrentLocale() from it.
	core := apitest.NewCore(t)
	labels := labelCorpus(t)
	snap := speciesindex.Build(labels, nil, "en")

	backend := &stubBackend{
		names: map[string]string{
			sciPerchOnly: "Shikra",
			sciBat:       "Daubenton's Bat",
		},
	}
	h := New(core, func() *speciesindex.Snapshot { return snap }, nil)
	h.speciesBackendOverride = backend

	tests := []struct {
		context string
		cases   []speciesInfoCase
	}{
		{
			context: rarityUniversal,
			cases: []speciesInfoCase{
				{query: sciRobin, wantCommon: "American Robin", wantRarity: RarityVeryCommon, wantScore: 0.9},
				{query: "turdus MIGRATORIUS", wantCommon: "American Robin", wantRarity: RarityVeryCommon, wantScore: 0.9},
				// Alias pair: the canonical request resolves to the legacy corpus label and
				// both spellings score the same species.
				{query: sciCanonDove, wantCommon: "Laughing Dove", wantRarity: RarityRare, wantScore: 0.1},
				{query: sciLegacyDove, wantCommon: "Laughing Dove", wantRarity: RarityRare, wantScore: 0.1},
				// Colliding pair: each member keeps its own name and score.
				{query: sciDrongoA, wantCommon: "Fork-tailed Drongo", wantRarity: RarityCommon, wantScore: 0.7},
				{query: sciDrongoB, wantCommon: "Glossy-backed Drongo", wantRarity: RarityVeryRare, wantScore: 0.05},
				{query: sciBlackbird, wantCommon: "Eurasian Blackbird", wantRarity: RarityUncommon, wantScore: 0.3},
				// A scientific-only label is localized via the backend; the bat's label carries its own name, which wins over the backend's, and both
				// have no geomodel coverage.
				{query: sciPerchOnly, wantCommon: "Shikra", wantRarity: RarityUnknown},
				{query: sciBat, wantCommon: "Water Bat", wantRarity: RarityUnknown},
				// In the geomodel vocabulary but not in any label set.
				{query: sciGeomodelOnly, wantNotFound: true},
				{query: "Unknown species", wantNotFound: true},
			},
		},
		{
			// The synthetic 1.0 override rows must not drive rarity: robin keeps its native
			// 0.9 and an overridden species outside the vocabulary stays unknown.
			context: rarityOverride,
			cases: []speciesInfoCase{
				{query: sciRobin, wantCommon: "American Robin", wantRarity: RarityVeryCommon, wantScore: 0.9},
				{query: sciBat, wantCommon: "Water Bat", wantRarity: RarityUnknown},
			},
		},
		{
			// Coverage falls back to the classifier labels, so species without a score read
			// as very rare instead of unknown.
			context: rarityLegacy,
			cases: []speciesInfoCase{
				{query: sciRobin, wantCommon: "American Robin", wantRarity: RarityCommon, wantScore: 0.6},
				{query: sciBlackbird, wantCommon: "Eurasian Blackbird", wantRarity: RarityRare, wantScore: 0.2},
				{query: sciCanonDove, wantCommon: "Laughing Dove", wantRarity: RarityVeryRare},
				{query: sciPerchOnly, wantCommon: "Shikra", wantRarity: RarityVeryRare},
			},
		},
		{
			context: rarityInactive,
			cases: []speciesInfoCase{
				{query: sciRobin, wantCommon: "American Robin", wantRarity: RarityUnknown},
				{query: sciDrongoB, wantCommon: "Glossy-backed Drongo", wantRarity: RarityUnknown},
			},
		},
	}

	contexts := rarityContexts(labels)
	for _, tc := range tests {
		t.Run(tc.context, func(t *testing.T) {
			rc, ok := contexts[tc.context]
			require.True(t, ok, "unknown rarity context %q", tc.context)
			backend.rc = rc

			for _, c := range tc.cases {
				req := httptest.NewRequest(http.MethodGet, "/api/v2/species?scientific_name="+url.QueryEscape(c.query), http.NoBody)
				rec := httptest.NewRecorder()
				require.NoError(t, h.GetSpeciesInfo(echo.New().NewContext(req, rec)), c.query)

				if c.wantNotFound {
					assert.Equal(t, http.StatusNotFound, rec.Code, c.query)
					continue
				}
				require.Equal(t, http.StatusOK, rec.Code, c.query)

				var info SpeciesInfo
				require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &info), c.query)
				assert.Equal(t, c.query, info.ScientificName, "the response echoes the requested name")
				assert.Equal(t, c.wantCommon, info.CommonName, c.query)
				assert.Equal(t, c.wantRarity, info.Rarity.Status, c.query)
				assert.InDelta(t, c.wantScore, info.Rarity.Score, scoreTolerance, c.query)
				// An inactive range filter does not report a location-based rarity.
				assert.Equal(t, rc.FilterActive, info.Rarity.LocationBased, c.query)
				// Coordinates and threshold come from the configured location whether or
				// not the range filter is active.
				require.NotNil(t, info.Rarity.Latitude, c.query)
				require.NotNil(t, info.Rarity.Longitude, c.query)
				assert.InDelta(t, fixtureLatitude, *info.Rarity.Latitude, coordinateTolerance, c.query)
				assert.InDelta(t, fixtureLongitude, *info.Rarity.Longitude, coordinateTolerance, c.query)
				assert.InDelta(t, fixtureRangeThreshold, info.Rarity.ThresholdApplied, thresholdTolerance, c.query)
			}
		})
	}
}

// Wire text of the not-found response and the taxonomy source of an embedded
// database hit, repeated here so the test pins what clients see.
const (
	wantSpeciesNotFound = "Species not found"
	wantTaxonomyLocal   = "local"
)

// Species whose taxonomy the info response must carry. The dove is requested under
// its current and its legacy spelling; the corpus label carries the legacy one and the
// taxonomy database holds both, so the label's name is looked up first and a lookup
// that tried the request name first would report the current genus. The frog is
// requested under its current name, which the taxonomy database knows, while the
// corpus label carries the legacy synonym Hyliola regilla, which it does not, so only
// the request-name fallback finds it.
const (
	sciFrog      = "Pseudacris regilla"
	genusLegacy  = "Streptopelia"
	familyLegacy = "Columbidae" //nolint:misspell // Columbidae is the dove family name, not a misspelling of Columbia
	genusFrog    = "Pseudacris"
	familyFrog   = "Hylidae"
	genusRobin   = "Turdus"
	familyRobin  = "Turdidae"
)

// TestGetSpeciesInfo_Taxonomy checks the taxonomy block and metadata source that
// GetSpeciesInfo attaches to a found species, and the not-found message. Taxonomy does
// not depend on the range filter, so the inactive context is enough.
func TestGetSpeciesInfo_Taxonomy(t *testing.T) {
	// Not parallel: apitest.NewCore publishes settings to the process-global
	// snapshot, and the handler reads CurrentLocale() from it.
	core := apitest.NewCore(t)
	labels := labelCorpus(t)
	snap := speciesindex.Build(labels, nil, "en")

	backend := &stubBackend{rc: rarityContexts(labels)[rarityInactive]}
	h := New(core, func() *speciesindex.Snapshot { return snap }, nil)
	h.speciesBackendOverride = backend

	tests := []struct {
		name       string
		query      string
		wantGenus  string
		wantFamily string
		wantNone   bool // the species has no taxonomy block and no metadata
	}{
		{name: "robin", query: sciRobin, wantGenus: genusRobin, wantFamily: familyRobin},
		{name: "dove under its current name", query: sciCanonDove, wantGenus: genusLegacy, wantFamily: familyLegacy},
		{name: "dove under its legacy name", query: sciLegacyDove, wantGenus: genusLegacy, wantFamily: familyLegacy},
		{name: "found only under the request name", query: sciFrog, wantGenus: genusFrog, wantFamily: familyFrog},
		{name: "secondary-model species", query: sciBat, wantNone: true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			req := httptest.NewRequest(http.MethodGet, "/api/v2/species?scientific_name="+url.QueryEscape(tt.query), http.NoBody)
			rec := httptest.NewRecorder()
			require.NoError(t, h.GetSpeciesInfo(echo.New().NewContext(req, rec)))
			require.Equal(t, http.StatusOK, rec.Code)

			var info SpeciesInfo
			require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &info))
			if tt.wantNone {
				assert.Empty(t, info.Taxonomy.Genus)
				assert.Empty(t, info.Taxonomy.Family)
				assert.Nil(t, info.Metadata)
				return
			}
			assert.Equal(t, tt.wantGenus, info.Taxonomy.Genus)
			assert.Equal(t, tt.wantFamily, info.Taxonomy.Family)
			assert.Equal(t, wantTaxonomyLocal, info.Metadata["source"])
		})
	}

	t.Run("unknown species", func(t *testing.T) {
		req := httptest.NewRequest(http.MethodGet, "/api/v2/species?scientific_name="+url.QueryEscape("Unknown species"), http.NoBody)
		rec := httptest.NewRecorder()
		require.NoError(t, h.GetSpeciesInfo(echo.New().NewContext(req, rec)))
		apitest.AssertControllerError(t, nil, rec, http.StatusNotFound, wantSpeciesNotFound)
	})
}
