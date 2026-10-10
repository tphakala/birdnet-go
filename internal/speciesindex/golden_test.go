package speciesindex

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/tphakala/birdnet-go/internal/datastore"
	"github.com/tphakala/birdnet-go/internal/openfauna"
)

// assertMatchesLegacy asserts Build's three name maps equal both frozen legacy
// builders' output for the same inputs.
func assertMatchesLegacy(t *testing.T, labels []string, resolver datastore.SpeciesNameResolver) {
	t.Helper()
	got := Build(labels, resolver, "")
	for _, ref := range []struct {
		name  string
		build func([]string, datastore.SpeciesNameResolver) legacyMaps
	}{
		{"datastore", legacyDatastoreBuild},
		{"api", legacyAPIBuild},
	} {
		want := ref.build(labels, resolver)
		assert.Equalf(t, want.sciToCommon, got.SciToCommon, "SciToCommon vs legacy %s", ref.name)
		assert.Equalf(t, want.sciToCommonFolded, got.SciToCommonFolded, "SciToCommonFolded vs legacy %s", ref.name)
		assert.Equalf(t, want.commonToSci, got.CommonToSci, "CommonToSci vs legacy %s", ref.name)
	}
}

func TestBuild_ThreeMaps_GoldenAgainstLegacyBuilders(t *testing.T) {
	t.Parallel()

	corpus := loadCorpus(t)
	perch := loadTestdata(t, "perch_like_labels.txt")
	bat := loadTestdata(t, "bat_like_labels.txt")
	all := make([]string, 0, len(corpus)+len(perch)+len(bat))
	all = append(all, corpus...)
	all = append(all, perch...)
	all = append(all, bat...)

	corpora := []struct {
		name   string
		labels []string
	}{
		{"v2.4_en_uk", corpus},
		{"perch_like", perch},
		{"bat_like", bat},
		{"concatenated", all},
	}

	// A few localized overrides so the ResolveLocal hit and miss branches both fire.
	local := localResolver{m: map[string]string{
		"Turdus merula":      "Localized Blackbird",
		"Myotis daubentonii": "Localized Water Bat",
		"Passer domesticus":  "Localized Sparrow",
	}}

	resolvers := []struct {
		name string
		r    datastore.SpeciesNameResolver
	}{
		{"nil", nil},
		{"local_only", local},
		{"batch_localizer", batchAllResolver{}},
	}

	for _, c := range corpora {
		for _, rv := range resolvers {
			t.Run(c.name+"/"+rv.name, func(t *testing.T) {
				t.Parallel()
				assertMatchesLegacy(t, c.labels, rv.r)
			})
		}
	}
}

// TestBuild_RealOpenFauna_GoldenEquality runs the full builder against a real
// OpenFauna resolver (including its batch-localizer path) for two locales, and
// asserts equality with both legacy copies. Skipped under -short because it
// rebuilds the OpenFauna working set. Dataset refreshes cannot break it because
// both sides see the same dataset.
func TestBuild_RealOpenFauna_GoldenEquality(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping real OpenFauna golden equality in -short mode")
	}
	t.Parallel()

	corpus := loadCorpus(t)
	sci := corpusScientificNames(corpus)

	for _, locale := range []string{"en", "fi"} {
		t.Run(locale, func(t *testing.T) {
			t.Parallel()
			r := openfauna.NewResolver()
			require.NoError(t, r.Rebuild(sci, locale))
			assertMatchesLegacy(t, corpus, r)
		})
	}
}
