// species_golden_test.go: end-to-end golden tests for GET /api/v2/species.
//
// These pin the exact JSON the endpoint returns so the Phase 2a species-index
// snapshot refactor can be proven byte-for-byte equivalent (invariant I1). The
// goldens are recorded from the pre-refactor helpers in this PR's first commit and
// must stay identical as each helper is rewritten onto the snapshot memo maps.
//
// The harness is deterministic and self-contained: it builds the species-name
// snapshot from the real vendored v2.4 label corpus plus the Perch- and bat-like
// testdata (so the openfauna alias memo maps are the real ones, exercising the
// Dicrurus/Mirafra and Streptopelia/Spilopelia alias pairs), and injects a fake
// backend with fixed rarity contexts so no native model is loaded and no network
// is touched. Regenerate with:
//
//	go test ./internal/api/v2/species -run Golden -update
package species

import (
	"bufio"
	"encoding/json"
	"flag"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/labstack/echo/v4"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/tphakala/birdnet-go/internal/api/v2/apitest"
	"github.com/tphakala/birdnet-go/internal/classifier"
	"github.com/tphakala/birdnet-go/internal/conf"
	"github.com/tphakala/birdnet-go/internal/speciesindex"
)

var updateGolden = flag.Bool("update", false, "update species golden files")

// Scientific names used to build the synthetic rarity contexts and the request
// list. The alias and colliding pairs are the same real OpenFauna cases the unit
// tests use, so the goldens exercise the exact-before-alias rule end to end.
const (
	goldRobin        = "Turdus migratorius"        // scored, in the corpus
	goldBlackbird    = "Turdus merula"             // in the corpus and the Perch set
	goldCanonDove    = "Spilopelia senegalensis"   // canonical; the corpus label is legacy
	goldLegacyDove   = "Streptopelia senegalensis" // legacy; the form the corpus ships
	goldDrongoA      = "Dicrurus adsimilis"        // colliding pair member A
	goldDrongoB      = "Dicrurus divaricatus"      // colliding pair member B (alias of A)
	goldBat          = "Myotis daubentonii"        // secondary-model only (bat testdata)
	goldPerchOnlySci = "Tachyspiza badia"          // scientific-only Perch label (localized via backend)
	goldGeomodelOnly = "Zzz Geomodelonly"          // in the geomodel vocabulary, not the classifier
)

// goldenBackend is a deterministic speciesBackend fake: a fixed localized-name map
// for resolveEitherName and a fixed RarityContext for the rarity lookup.
type goldenBackend struct {
	names map[string]string
	rc    classifier.RarityContext
}

func (g *goldenBackend) ResolveName(scientificName, _ string) string {
	return g.names[scientificName]
}

func (g *goldenBackend) GetRarityContext(_ time.Time) (classifier.RarityContext, error) {
	return g.rc, nil
}

// syntheticLabel wraps a scientific name in a throwaway "Sci_Common" label. Only
// the scientific part drives coverage and score matching, so the common part is
// arbitrary; using a distinct marker keeps these labels out of the real corpus.
func syntheticLabel(sci string) string { return sci + "_syn" }

// goldenSettings returns a settings snapshot with a configured location so the
// rarity coordinates and threshold serialize deterministically.
func goldenSettings() *conf.Settings {
	s := &conf.Settings{}
	s.BirdNET.LocationConfigured = true
	s.BirdNET.Latitude = 60.1699
	s.BirdNET.Longitude = 24.9384
	s.BirdNET.RangeFilter.Threshold = 0.03
	return s
}

// loadLabelLines reads a newline-delimited label file, trimming blanks.
func loadLabelLines(t *testing.T, path string) []string {
	t.Helper()
	f, err := os.Open(path) //nolint:gosec // test-only read of a repo-vendored label file
	require.NoError(t, err, "open label file %s", path)
	t.Cleanup(func() { _ = f.Close() })

	var out []string
	sc := bufio.NewScanner(f)
	for sc.Scan() {
		if line := strings.TrimSpace(sc.Text()); line != "" {
			out = append(out, line)
		}
	}
	require.NoError(t, sc.Err())
	return out
}

// goldenCorpus loads the vendored v2.4 corpus plus the Perch- and bat-like
// testdata, concatenated. The union exercises embedded-common (v2.4/bat) and
// scientific-only (Perch) labels and the real alias pairs.
func goldenCorpus(t *testing.T) []string {
	t.Helper()
	corpus := loadLabelLines(t, filepath.Join("..", "..", "..", "classifier", "data", "labels", "V2.4", "BirdNET_GLOBAL_6K_V2.4_Labels_en_uk.txt"))
	perch := loadLabelLines(t, filepath.Join("..", "..", "..", "speciesindex", "testdata", "perch_like_labels.txt"))
	bat := loadLabelLines(t, filepath.Join("..", "..", "..", "speciesindex", "testdata", "bat_like_labels.txt"))
	all := make([]string, 0, len(corpus)+len(perch)+len(bat))
	all = append(all, corpus...)
	all = append(all, perch...)
	all = append(all, bat...)
	return all
}

// goldenContexts returns the fixed rarity contexts, one golden file each. Each
// name selects a distinct code path through computeRarity.
func goldenContexts(classifierLabels []string) map[string]classifier.RarityContext {
	settings := goldenSettings()

	// Universal geomodel: a vocabulary covering both alias-pair members, both
	// colliding members, robin, blackbird and a geomodel-only species, with a
	// descending score list for a subset.
	universalVocab := []string{
		syntheticLabel(goldRobin),
		syntheticLabel(goldBlackbird),
		syntheticLabel(goldLegacyDove),
		syntheticLabel(goldCanonDove),
		syntheticLabel(goldDrongoA),
		syntheticLabel(goldDrongoB),
		syntheticLabel(goldGeomodelOnly),
	}
	universalScores := []classifier.SpeciesScore{
		{Label: syntheticLabel(goldRobin), Score: 0.9},
		{Label: syntheticLabel(goldDrongoA), Score: 0.7},
		{Label: syntheticLabel(goldBlackbird), Score: 0.3},
		{Label: syntheticLabel(goldLegacyDove), Score: 0.1},
		{Label: syntheticLabel(goldDrongoB), Score: 0.05},
	}

	// Override: the universal context plus the rows addUserOverrideSpeciesScores
	// appends. A synthetic 1.0 twin for robin (which also has a native 0.9) must
	// not shadow the native score; a synthetic 1.0 for a species outside the
	// vocabulary (the bat) must not read as covered.
	overrideScores := make([]classifier.SpeciesScore, 0, len(universalScores)+2)
	overrideScores = append(overrideScores, universalScores...)
	overrideScores = append(overrideScores,
		classifier.SpeciesScore{Label: syntheticLabel(goldRobin), Score: 1.0, IsSyntheticOverride: true, IsManuallyIncluded: true},
		classifier.SpeciesScore{Label: syntheticLabel(goldBat), Score: 1.0, IsSyntheticOverride: true, IsManuallyIncluded: true},
	)

	// Legacy backend: no geomodel vocabulary, so coverage falls back to the
	// classifier labels.
	legacyScores := []classifier.SpeciesScore{
		{Label: syntheticLabel(goldRobin), Score: 0.6},
		{Label: syntheticLabel(goldBlackbird), Score: 0.2},
	}

	return map[string]classifier.RarityContext{
		"universal": {
			Scores:           universalScores,
			GeomodelLabels:   universalVocab,
			ClassifierLabels: classifierLabels,
			FilterActive:     true,
			Settings:         settings,
		},
		"override": {
			Scores:           overrideScores,
			GeomodelLabels:   universalVocab,
			ClassifierLabels: classifierLabels,
			FilterActive:     true,
			Settings:         settings,
		},
		"legacy": {
			Scores:           legacyScores,
			GeomodelLabels:   nil,
			ClassifierLabels: classifierLabels,
			FilterActive:     true,
			Settings:         settings,
		},
		"inactive": {
			Scores:           nil,
			GeomodelLabels:   nil,
			ClassifierLabels: classifierLabels,
			FilterActive:     false,
			Settings:         settings,
		},
	}
}

// goldenRequests is the ordered request list recorded in every context.
func goldenRequests() []string {
	return []string{
		goldRobin,
		"turdus MIGRATORIUS", // case-insensitive exact
		goldCanonDove,        // canonical request, legacy label
		goldLegacyDove,       // legacy request, exact
		goldDrongoA,          // colliding pair A
		goldDrongoB,          // colliding pair B
		goldBlackbird,
		goldPerchOnlySci,  // scientific-only, localized via backend
		goldBat,           // secondary-model only
		goldGeomodelOnly,  // in the vocabulary but not the label set
		"Unknown species", // not in any label -> 404
		"Turdus",          // single word -> 400
		"",                // missing -> 400
	}
}

type goldenEntry struct {
	Query  string         `json:"query"`
	Status int            `json:"status"`
	Body   map[string]any `json:"body"`
}

// stripVolatile removes response fields that vary per run (correlation ids,
// timestamps, the rarity date) so the goldens stay stable.
func stripVolatile(v any) {
	switch t := v.(type) {
	case map[string]any:
		for _, k := range []string{"correlation_id", "timestamp", "date"} {
			delete(t, k)
		}
		for _, child := range t {
			stripVolatile(child)
		}
	case []any:
		for _, child := range t {
			stripVolatile(child)
		}
	}
}

func TestGetSpeciesInfo_Golden(t *testing.T) {
	// Not parallel: apitest.NewCore publishes settings to the process-global
	// snapshot, and the handler reads CurrentLocale() from it.
	core := apitest.NewCore(t)
	labels := goldenCorpus(t)
	snap := speciesindex.Build(labels, nil, "en")

	backend := &goldenBackend{
		names: map[string]string{
			goldPerchOnlySci: "Shikra",
			goldBat:          "Daubenton's Bat",
		},
	}
	h := New(core, func() *speciesindex.Snapshot { return snap }, nil)
	h.speciesBackendOverride = backend

	contexts := goldenContexts(labels)
	for name, rc := range contexts {
		t.Run(name, func(t *testing.T) {
			backend.rc = rc

			entries := make([]goldenEntry, 0, len(goldenRequests()))
			for _, q := range goldenRequests() {
				e := echo.New()
				target := "/api/v2/species"
				if q != "" {
					target += "?scientific_name=" + url.QueryEscape(q)
				}
				req := httptest.NewRequest(http.MethodGet, target, http.NoBody)
				rec := httptest.NewRecorder()
				ctx := e.NewContext(req, rec)
				require.NoError(t, h.GetSpeciesInfo(ctx))

				var body map[string]any
				require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &body), "decode response for %q", q)
				stripVolatile(body)
				entries = append(entries, goldenEntry{Query: q, Status: rec.Code, Body: body})
			}

			got, err := json.MarshalIndent(entries, "", "  ")
			require.NoError(t, err)
			got = append(got, '\n')

			goldenPath := filepath.Join("testdata", "golden", name+".json")
			if *updateGolden {
				require.NoError(t, os.MkdirAll(filepath.Dir(goldenPath), 0o755))
				require.NoError(t, os.WriteFile(goldenPath, got, 0o600))
				return
			}

			want, err := os.ReadFile(goldenPath) //nolint:gosec // test-only read of a repo-tracked golden file
			require.NoError(t, err, "read golden %s (run with -update to create)", goldenPath)
			assert.Equal(t, string(want), string(got), "golden mismatch for context %s; run -update if the change is intended", name)
		})
	}
}
