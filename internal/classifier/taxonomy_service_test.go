package classifier

import (
	"bufio"
	"fmt"
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/tphakala/birdnet-go/internal/conf/conftest"
)

// v24EnUKLabelsPath is the embedded v2.4 en_uk label corpus, read from disk so the
// taxonomy parity tests run over the real label set instead of a synthetic sample.
const v24EnUKLabelsPath = "data/labels/V2.4/BirdNET_GLOBAL_6K_V2.4_Labels_en_uk.txt"

// readV24Corpus reads the v2.4 en_uk label corpus. It fails hard if the file is
// missing so a moved or renamed corpus surfaces as a loud failure, not a silent skip.
func readV24Corpus(t *testing.T) []string {
	t.Helper()
	f, err := os.Open(v24EnUKLabelsPath)
	require.NoError(t, err, "v2.4 label corpus must be present at %s", v24EnUKLabelsPath)
	t.Cleanup(func() { assert.NoError(t, f.Close()) })

	var labels []string
	sc := bufio.NewScanner(f)
	for sc.Scan() {
		if line := sc.Text(); line != "" {
			labels = append(labels, line)
		}
	}
	require.NoError(t, sc.Err())
	require.NotEmpty(t, labels, "v2.4 label corpus must not be empty")
	return labels
}

func TestNewTaxonomyService_EmbeddedLoads(t *testing.T) {
	t.Parallel()
	svc, err := newTaxonomyService("")
	require.NoError(t, err)
	require.NotNil(t, svc)
	assert.NotEmpty(t, svc.taxonomyMap, "embedded taxonomy map must be populated")
	assert.NotEmpty(t, svc.sciIndex, "scientific index must be populated")
}

func TestNewTaxonomyService_CustomPathMissingFails(t *testing.T) {
	t.Parallel()
	missing := filepath.Join(t.TempDir(), "does-not-exist-taxonomy.json")
	svc, err := newTaxonomyService(missing)
	require.Error(t, err)
	assert.Nil(t, svc)
	assert.Contains(t, err.Error(), "does-not-exist-taxonomy", "error must name the unreadable taxonomy file")
}

// TestTaxonomyService_SpeciesCode_MatchesFreeFunction pins that the service returns
// the same code and found flag as the free GetSpeciesCodeFromName over the whole
// v2.4 corpus, plus the legacy-remap and placeholder paths.
func TestTaxonomyService_SpeciesCode_MatchesFreeFunction(t *testing.T) {
	t.Parallel()
	freeMap, freeIndex, err := LoadTaxonomyData("")
	require.NoError(t, err)
	svc, err := newTaxonomyService("")
	require.NoError(t, err)

	labels := readV24Corpus(t)
	// Exercise the legacy-remap ("hergul" -> "amhgul1") and placeholder paths too.
	labels = append(labels,
		"Larus argentatus_Herring Gull",
		"Definitely Notaspecies_Placeholder Only",
	)
	for _, label := range labels {
		gotCode, gotOK := svc.speciesCode(label)
		wantCode, wantOK := GetSpeciesCodeFromName(freeMap, freeIndex, label)
		assert.Equal(t, wantOK, gotOK, "found flag must match for %q", label)
		assert.Equal(t, wantCode, gotCode, "code must match for %q", label)
	}
}

// TestTaxonomyService_NameFromCode_MatchesFreeFunction pins that the service returns
// the same name and found flag as the free GetSpeciesNameFromCode over every code.
func TestTaxonomyService_NameFromCode_MatchesFreeFunction(t *testing.T) {
	t.Parallel()
	freeMap, _, err := LoadTaxonomyData("")
	require.NoError(t, err)
	svc, err := newTaxonomyService("")
	require.NoError(t, err)

	for code := range freeMap {
		gotName, gotOK := svc.nameFromCode(code)
		wantName, wantOK := GetSpeciesNameFromCode(freeMap, code)
		assert.Equal(t, wantOK, gotOK, "found flag must match for code %q", code)
		assert.Equal(t, wantName, gotName, "name must match for code %q", code)
	}
	_, ok := svc.nameFromCode("definitely-not-a-code")
	assert.False(t, ok, "an unknown code must report not-found")
}

// TestOrchestrator_TaxonomyAccessors_AfterDelete documents the intentional
// post-Delete behavior change: the taxonomy is orchestrator-owned and immutable, so
// the taxonomy accessors still answer after the primary model is released, where
// they previously returned empty. No running install can observe this (Delete runs
// only at shutdown).
func TestOrchestrator_TaxonomyAccessors_AfterDelete(t *testing.T) {
	settings := conftest.GetTestSettings()
	o, err := NewOrchestrator(settings)
	if err != nil {
		t.Skipf("Skipping: model not available in test environment: %v", err)
	}

	const label = "Turdus merula_Eurasian Blackbird"
	wantCode, wantCodeOK := o.GetSpeciesCode(label)
	wantName, wantNameOK := o.GetSpeciesNameFromCode(wantCode)

	o.Delete()

	gotCode, gotCodeOK := o.GetSpeciesCode(label)
	assert.Equal(t, wantCodeOK, gotCodeOK, "GetSpeciesCode must still answer after Delete")
	assert.Equal(t, wantCode, gotCode, "GetSpeciesCode must return the same code after Delete")

	gotName, gotNameOK := o.GetSpeciesNameFromCode(wantCode)
	assert.Equal(t, wantNameOK, gotNameOK, "GetSpeciesNameFromCode must still answer after Delete")
	assert.Equal(t, wantName, gotName, "GetSpeciesNameFromCode must return the same name after Delete")
}

// TestOrchestrator_TaxonomyWrappers exercises the orchestrator's taxonomy accessors
// end to end on a real orchestrator, so a regression in the delegation to the
// taxonomy service, the placeholder-code path, or the ResolveName override is caught
// (the service-level parity tests cover speciesCode/nameFromCode directly, but not
// the public wrappers).
func TestOrchestrator_TaxonomyWrappers(t *testing.T) {
	settings := conftest.GetTestSettings()
	o, err := NewOrchestrator(settings)
	if err != nil {
		t.Skipf("Skipping: model not available in test environment: %v", err)
	}
	t.Cleanup(func() { o.Delete() })

	// A species present in the taxonomy splits correctly and resolves to a code; a
	// real (non-placeholder) code round-trips through GetSpeciesNameFromCode.
	const known = "Turdus merula_Eurasian Blackbird"
	sci, _, code := o.EnrichResultWithTaxonomy(known)
	assert.Equal(t, "Turdus merula", sci, "scientific name must be split from the label")
	require.NotEmpty(t, code, "a known species must resolve to a code")
	if name, ok := o.GetSpeciesNameFromCode(code); ok {
		assert.NotEmpty(t, name, "a resolvable code must map to a non-empty name")
	}

	// A species absent from the taxonomy still splits and gets the deterministic
	// placeholder code (never empty), matching the free function.
	const unknown = "Zzz notaspecies_Placeholder Only"
	usci, _, ucode := o.EnrichResultWithTaxonomy(unknown)
	assert.Equal(t, "Zzz notaspecies", usci)
	assert.Equal(t, GeneratePlaceholderCode(unknown), ucode, "an unknown species must get the placeholder code")
}

// TestTaxonomyService_LogMissingCodes exercises the debug-logging branches directly:
// the orchestrator path only reaches logMissingCodes with a complete corpus and, by
// default, debug off, so the missing-species loop and the ">N more" truncation are
// otherwise untested. The log output is debug-only; this asserts the branch logic
// runs without panicking for both phrasings and both debug states.
func TestTaxonomyService_LogMissingCodes(t *testing.T) {
	t.Parallel()
	// A tiny taxonomy that knows one species, so every other label is "missing".
	svc := &taxonomyService{
		taxonomyMap: TaxonomyMap{"Turdus merula_Eurasian Blackbird": "eurbla"},
		sciIndex:    ScientificNameIndex{"Turdus merula": "eurbla"},
	}
	labels := make([]string, 0, 1+maxMissingSpeciesLogged+5)
	labels = append(labels, "Turdus merula_Eurasian Blackbird")
	for i := range maxMissingSpeciesLogged + 5 {
		labels = append(labels, fmt.Sprintf("Genus species%d_Common %d", i, i))
	}
	// Both phrasings with debug on exercise the loop and the truncation branch.
	assert.NotPanics(t, func() { svc.logMissingCodes(labels, true, true) })
	assert.NotPanics(t, func() { svc.logMissingCodes(labels, false, true) })
	// Debug off is the early-return path (no scan, no logging).
	assert.NotPanics(t, func() { svc.logMissingCodes(labels, false, false) })
}
