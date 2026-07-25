package classifier

import (
	"bytes"
	"log/slog"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/tphakala/birdnet-go/internal/conf"
	"github.com/tphakala/birdnet-go/internal/conf/conftest"
	"github.com/tphakala/birdnet-go/internal/logger"
	"github.com/tphakala/birdnet-go/internal/openfauna"
)

// overrideTestSettings builds a v3-geomodel settings snapshot where the geomodel
// labels are English ("Scientific_English") and the active classifier labels are
// localized ("Scientific_LocalizedCommon"), mirroring a Finnish-locale install.
// Parus major is deliberately kept OUT of the geomodel scores, so the only way it
// reaches the inclusion set is the user override listed by its Finnish common name.
func overrideTestSettings(t *testing.T, locale string) (*conf.Settings, *fakeUniversalRangeFilter) {
	t.Helper()
	settings := conftest.GetTestSettings()
	settings.BirdNET.Latitude = 60.0
	settings.BirdNET.Longitude = 25.0
	settings.BirdNET.LocationConfigured = true
	settings.BirdNET.RangeFilter.Threshold = 0.01
	settings.BirdNET.Locale = locale
	// Active classifier labels carry the localized (Finnish) common names.
	settings.BirdNET.Labels = []string{
		"Turdus merula_Mustarastas",
		"Parus major_Talitiainen",
	}
	// User force-includes Parus major by its bare Finnish common name.
	settings.Realtime.Species.Include = []string{"Talitiainen"}

	rf := &fakeUniversalRangeFilter{
		// Geomodel labels are English and independent of the classifier locale.
		geoLabels: []string{"Turdus merula_Common Blackbird", "Parus major_Great Tit"},
		// Only Turdus merula scores above threshold; Parus major is out of range.
		scores:    []SpeciesScore{{Score: 0.9, Label: "Turdus merula_Common Blackbird"}},
		rawScores: []float32{0.9},
	}
	return settings, rf
}

// TestBuildRangeFilter_BareLocalizedCommonNameOverride_ForceIncludesViaGate proves
// the core of issue #982: a bare localized common name in realtime.species.include
// must canonicalize to its "Scientific_Common" label so the inclusion gate keys on
// the scientific name. Before the fix, the bare "Talitiainen" is appended verbatim,
// IncludedScientificNames stores the useless key "talitiainen", and a real
// Parus major detection is silently dropped at the gate.
func TestBuildRangeFilter_BareLocalizedCommonNameOverride_ForceIncludesViaGate(t *testing.T) {
	settings, rf := overrideTestSettings(t, "fi")
	conftest.SetTestSettings(settings)
	t.Cleanup(func() { conftest.SetTestSettings(nil) })

	o := buildTestOrchestrator(t, settings, rf)
	require.NoError(t, BuildRangeFilter(o))

	included := conf.GetSettings().GetIncludedSpecies()
	assert.Contains(t, included, "Parus major_Talitiainen",
		"override must be canonicalized to the classifier's Scientific_Common label")
	assert.NotContains(t, included, "Talitiainen",
		"the bare common name must not survive in the inclusion working set")

	// The force-include gate must accept a real detection of the overridden species,
	// regardless of whether the detection label carries the localized or English common.
	assert.True(t, conf.GetSettings().IsSpeciesIncluded("Parus major_Talitiainen"),
		"force-included species must pass the inclusion gate (localized label)")
	assert.True(t, conf.GetSettings().IsSpeciesIncluded("Parus major_Great Tit"),
		"force-included species must pass the inclusion gate (geomodel label)")
}

// TestBuildRangeFilter_BareLocalizedCommonNameOverride_DoesNotPolluteNameResolver
// proves the cosmetic half of issue #982: once the override is canonicalized, the
// OpenFauna resolver receives the scientific name "Parus major" (resolvable in fi)
// instead of the bare "Talitiainen", so the "could not localize" WARN no longer
// names a fully localizable species. Not parallel: mutates the global logger.
func TestBuildRangeFilter_BareLocalizedCommonNameOverride_DoesNotPolluteNameResolver(t *testing.T) {
	settings, rf := overrideTestSettings(t, "fi")
	conftest.SetTestSettings(settings)
	t.Cleanup(func() { conftest.SetTestSettings(nil) })

	var buf bytes.Buffer
	capture := slog.NewTextHandler(&buf, &slog.HandlerOptions{Level: slog.LevelDebug})
	cl, err := logger.NewCentralLogger(
		&logger.LoggingConfig{
			Console:      &logger.ConsoleOutput{Enabled: false},
			FileOutput:   &logger.FileOutput{Enabled: false},
			DefaultLevel: "debug",
		},
		capture,
	)
	require.NoError(t, err)
	t.Cleanup(func() { _ = cl.Close() })
	prev := logger.Global()
	logger.SetGlobal(cl)
	t.Cleanup(func() { logger.SetGlobal(prev) })

	o := buildTestOrchestrator(t, settings, rf)
	o.openfauna = openfauna.NewResolver()
	require.NoError(t, BuildRangeFilter(o))

	out := buf.String()
	assert.NotContains(t, out, "Talitiainen",
		"a canonicalized override must not appear in the openfauna unresolved WARN")
}

// TestGetProbableSpecies_BareLocalizedCommonNameOverride_CanonicalizesLabel covers
// the sibling appender addUserOverrideSpeciesScores on the getProbableSpecies path
// (the daily UpdateRangeFilterAction and the UI/test species-list endpoints). It must
// receive the same canonicalization so those surfaces never show a bare common name.
func TestGetProbableSpecies_BareLocalizedCommonNameOverride_CanonicalizesLabel(t *testing.T) {
	settings, rf := overrideTestSettings(t, "fi")

	bn := &BirdNET{
		Settings:     settings,
		rangeFilter:  rf,
		speciesCache: make(map[string]*speciesCacheEntry),
	}

	scores, _, err := bn.getProbableSpecies(time.Now(), 0, settings)
	require.NoError(t, err)

	labels := make([]string, 0, len(scores))
	for _, ss := range scores {
		labels = append(labels, ss.Label)
	}
	assert.Contains(t, labels, "Parus major_Talitiainen",
		"override must be canonicalized to the classifier's Scientific_Common label")
	assert.NotContains(t, labels, "Talitiainen",
		"the bare common name must not survive as a species score label")
}

// TestBuildRangeFilter_NonPrimaryLocalizedCommonOverride_ReverseResolvesToScientific
// covers the non-primary-model case: a species detected only by a non-primary model
// emits a scientific-only label, so its localized common name matches no primary
// geomodel or classifier label. The override must reverse-resolve through OpenFauna to
// the scientific name so the inclusion gate keys on it (force-include works) and the
// name resolver can localize it. The Finnish fox "Kettu" -> "Vulpes vulpes" is the
// reverse mapping; none of the (bird) labels carry it, so only OpenFauna can.
func TestBuildRangeFilter_NonPrimaryLocalizedCommonOverride_ReverseResolvesToScientific(t *testing.T) {
	settings, rf := overrideTestSettings(t, "fi")
	settings.Realtime.Species.Include = []string{"Kettu"}
	conftest.SetTestSettings(settings)
	t.Cleanup(func() { conftest.SetTestSettings(nil) })

	o := buildTestOrchestrator(t, settings, rf)
	require.NoError(t, BuildRangeFilter(o))

	included := conf.GetSettings().GetIncludedSpecies()
	assert.Contains(t, included, "Vulpes vulpes",
		"a non-primary localized common-name override must reverse-resolve to the scientific name")
	assert.NotContains(t, included, "Kettu",
		"the bare localized common name must not survive in the inclusion working set")
	assert.True(t, conf.GetSettings().IsSpeciesIncluded("Vulpes vulpes"),
		"force-include must work: a real Vulpes vulpes detection passes the inclusion gate")
}

// TestGetProbableSpecies_NonPrimaryLocalizedCommonOverride_ReverseResolvesToScientific
// proves the sibling appender addUserOverrideSpeciesScores (the daily
// UpdateRangeFilterAction / species-list endpoints) gets the same reverse
// resolution, so the daily refresh does not revert the canonicalization a day later.
func TestGetProbableSpecies_NonPrimaryLocalizedCommonOverride_ReverseResolvesToScientific(t *testing.T) {
	settings, rf := overrideTestSettings(t, "fi")
	settings.Realtime.Species.Include = []string{"Kettu"}

	bn := &BirdNET{
		Settings:     settings,
		rangeFilter:  rf,
		speciesCache: make(map[string]*speciesCacheEntry),
	}

	scores, _, err := bn.getProbableSpecies(time.Now(), 0, settings)
	require.NoError(t, err)

	labels := make([]string, 0, len(scores))
	for _, ss := range scores {
		labels = append(labels, ss.Label)
	}
	assert.Contains(t, labels, "Vulpes vulpes",
		"the daily path must also reverse-resolve a non-primary localized common name")
	assert.NotContains(t, labels, "Kettu",
		"the bare localized common name must not survive as a species score label")
}

// TestBuildRangeFilter_NonPrimaryLocalizedCommonOverride_DoesNotPolluteNameResolver
// proves the cosmetic half: once "Kettu" is canonicalized to "Vulpes vulpes"
// (resolvable in fi), the working set no longer feeds a bare common name to the
// resolver, so the "could not localize" WARN does not fire. Not parallel: global logger.
func TestBuildRangeFilter_NonPrimaryLocalizedCommonOverride_DoesNotPolluteNameResolver(t *testing.T) {
	settings, rf := overrideTestSettings(t, "fi")
	settings.Realtime.Species.Include = []string{"Kettu"}
	conftest.SetTestSettings(settings)
	t.Cleanup(func() { conftest.SetTestSettings(nil) })

	var buf bytes.Buffer
	capture := slog.NewTextHandler(&buf, &slog.HandlerOptions{Level: slog.LevelDebug})
	cl, err := logger.NewCentralLogger(
		&logger.LoggingConfig{
			Console:      &logger.ConsoleOutput{Enabled: false},
			FileOutput:   &logger.FileOutput{Enabled: false},
			DefaultLevel: "debug",
		},
		capture,
	)
	require.NoError(t, err)
	t.Cleanup(func() { _ = cl.Close() })
	prev := logger.Global()
	logger.SetGlobal(cl)
	t.Cleanup(func() { logger.SetGlobal(prev) })

	o := buildTestOrchestrator(t, settings, rf)
	o.openfauna = openfauna.NewResolver()
	require.NoError(t, BuildRangeFilter(o))

	assert.NotContains(t, buf.String(), "could not localize",
		"a reverse-resolved override must not leave an unresolvable working-set entry")
}

// TestBuildRangeFilter_UnresolvableOverride_StaysVerbatim guards against the reverse
// lookup over-matching: a string that is neither a label match nor a real localized
// fauna name must still be appended verbatim, so the fix does not start force-mapping
// arbitrary user strings.
func TestBuildRangeFilter_UnresolvableOverride_StaysVerbatim(t *testing.T) {
	settings, rf := overrideTestSettings(t, "fi")
	settings.Realtime.Species.Include = []string{"drone"}
	conftest.SetTestSettings(settings)
	t.Cleanup(func() { conftest.SetTestSettings(nil) })

	o := buildTestOrchestrator(t, settings, rf)
	require.NoError(t, BuildRangeFilter(o))

	included := conf.GetSettings().GetIncludedSpecies()
	assert.Contains(t, included, "drone",
		"a non-fauna override must still be appended verbatim (no spurious reverse match)")
}

// TestGetProbableSpecies_LegacyPath_NonPrimaryLocalizedCommonOverride_ReverseResolves
// covers the legacy (non-UniversalSpeciesPredictor) range-filter path, which builds
// the working set via getProbableSpecies' legacy branch. A non-universal range filter
// (fakeRangeFilter implements only the base inference.RangeFilter interface) forces
// that branch, and the localized non-primary override must reverse-resolve there too,
// matching the universal path.
func TestGetProbableSpecies_LegacyPath_NonPrimaryLocalizedCommonOverride_ReverseResolves(t *testing.T) {
	settings, _ := overrideTestSettings(t, "fi")
	settings.Realtime.Species.Include = []string{"Kettu"}

	bn := &BirdNET{
		Settings: settings,
		// Non-universal range filter: forces the legacy getProbableSpecies branch.
		// Two scores aligned with the two classifier labels; only the first clears
		// the threshold, so the legacy filter contributes Turdus merula.
		rangeFilter:  &fakeRangeFilter{scores: []float32{0.9, 0.0}},
		speciesCache: make(map[string]*speciesCacheEntry),
	}

	scores, _, err := bn.getProbableSpecies(time.Now(), 0, settings)
	require.NoError(t, err)

	labels := make([]string, 0, len(scores))
	for _, ss := range scores {
		labels = append(labels, ss.Label)
	}
	assert.Contains(t, labels, "Vulpes vulpes",
		"the legacy (non-universal) path must also reverse-resolve a localized non-primary override")
	assert.NotContains(t, labels, "Kettu",
		"the bare localized common name must not survive as a species score label")
}

// TestOverrideSpeciesNames_SortsConfigKeysDeterministically guards the deterministic
// override order: include entries keep the user's order, and the config map keys
// (whose Go iteration order is random) follow in sorted order, so the inclusion
// working set, debug logs, and species-list API stay stable across runs.
func TestOverrideSpeciesNames_SortsConfigKeysDeterministically(t *testing.T) {
	t.Parallel()

	settings := conftest.GetTestSettings()
	settings.Realtime.Species.Include = []string{"UserOrderTwo", "UserOrderOne"}
	settings.Realtime.Species.Config = map[string]conf.SpeciesConfig{
		"zzz species": {}, "aaa species": {}, "mmm species": {},
	}

	got := overrideSpeciesNames(settings)
	assert.Equal(t,
		[]string{"UserOrderTwo", "UserOrderOne", "aaa species", "mmm species", "zzz species"},
		got,
		"include entries keep user order; config keys follow in sorted order")
}

// overrideProvenanceSettings builds the issue #3974 scenario on top of the shared
// override harness: the geomodel labels carry English common names while the active
// classifier is Finnish, so an English config key resolves to a canonical label whose
// displayed (Finnish) common name is nothing like the key the user typed. That is what
// broke the settings UI's string matching. Parus major stays out of the range-filter
// scores so it can only arrive via the override, and Turdus merula stays in them so the
// flag-in-place path is exercised too.
func overrideProvenanceSettings(t *testing.T) (*conf.Settings, *fakeUniversalRangeFilter) {
	t.Helper()
	settings, rf := overrideTestSettings(t, "fi")
	settings.Realtime.Species.Include = nil
	settings.Realtime.Species.Config = nil
	return settings, rf
}

// requireScoreForLabel returns the SpeciesScore carrying label, failing the test if
// absent. It wraps the shared scoreForLabel lookup so each assertion below can read
// the entry directly instead of repeating the found check.
func requireScoreForLabel(t *testing.T, scores []SpeciesScore, label string) SpeciesScore {
	t.Helper()
	got, ok := scoreForLabel(scores, label)
	require.True(t, ok, "label %q not present in scores %+v", label, scores)
	return got
}

// probableSpeciesFor runs the override-appending path over the given settings.
func probableSpeciesFor(t *testing.T, settings *conf.Settings, rf *fakeUniversalRangeFilter) []SpeciesScore {
	t.Helper()
	bn := &BirdNET{
		Settings:     settings,
		rangeFilter:  rf,
		speciesCache: make(map[string]*speciesCacheEntry),
	}
	scores, _, err := bn.getProbableSpecies(time.Now(), 0, settings)
	require.NoError(t, err)
	return scores
}

// TestAddUserOverrideSpeciesScores_ConfigAliasCarriesCustomConfigProvenance is the
// regression test for issue #3974. A config key given as an alias ("Great Tit", the
// geomodel's English common name) resolves to a canonical label the settings UI cannot
// match against the key, so the provenance has to travel with the score instead. The
// species must be flagged as configured and NOT as manually included: the two badges
// mean different things and the config key alone does not put a species in the include
// list.
func TestAddUserOverrideSpeciesScores_ConfigAliasCarriesCustomConfigProvenance(t *testing.T) {
	settings, rf := overrideProvenanceSettings(t)
	settings.Realtime.Species.Config = map[string]conf.SpeciesConfig{
		"Great Tit": {Threshold: 0.5},
	}

	got := requireScoreForLabel(t, probableSpeciesFor(t, settings, rf), "Parus major_Great Tit")
	assert.True(t, got.HasCustomConfig,
		"a config key that resolves through an alias must still be marked as configured")
	assert.False(t, got.IsManuallyIncluded,
		"a config key alone must not claim the species is manually included")
	assert.InDelta(t, 1.0, got.Score, 1e-9,
		"an override-appended species is always-active at the 1.0 sentinel")
}

// TestAddUserOverrideSpeciesScores_ConfigOnScoredSpeciesFlagsInPlace covers the other
// half: when the range filter already scored the configured species, the override does
// not append a second entry, so the flag has to be set on the existing one. The
// range-filter score must survive, or a configured species would be silently promoted
// to always-active.
func TestAddUserOverrideSpeciesScores_ConfigOnScoredSpeciesFlagsInPlace(t *testing.T) {
	settings, rf := overrideProvenanceSettings(t)
	settings.Realtime.Species.Config = map[string]conf.SpeciesConfig{
		"Common Blackbird": {Threshold: 0.5},
	}

	got := requireScoreForLabel(t, probableSpeciesFor(t, settings, rf), "Turdus merula_Common Blackbird")
	assert.True(t, got.HasCustomConfig,
		"a species the range filter already scored must be flagged in place")
	assert.InDelta(t, 0.9, got.Score, 1e-9,
		"flagging must not overwrite the range-filter score with the 1.0 sentinel")
}

// TestAddUserOverrideSpeciesScores_IncludeOnlyIsNotConfigured guards the negative
// direction: an include entry must not light up the "Configured" badge.
func TestAddUserOverrideSpeciesScores_IncludeOnlyIsNotConfigured(t *testing.T) {
	settings, rf := overrideProvenanceSettings(t)
	settings.Realtime.Species.Include = []string{"Great Tit"}

	got := requireScoreForLabel(t, probableSpeciesFor(t, settings, rf), "Parus major_Great Tit")
	assert.True(t, got.IsManuallyIncluded)
	assert.False(t, got.HasCustomConfig,
		"an include entry must not claim the species has a custom config")
}

// TestAddUserOverrideSpeciesScores_IncludeAndConfigUnionWithoutDuplicate proves the
// two provenance sources union onto one entry rather than appending the species twice,
// which would double it in the Active Species list and the CSV export.
func TestAddUserOverrideSpeciesScores_IncludeAndConfigUnionWithoutDuplicate(t *testing.T) {
	settings, rf := overrideProvenanceSettings(t)
	settings.Realtime.Species.Include = []string{"Great Tit"}
	settings.Realtime.Species.Config = map[string]conf.SpeciesConfig{
		"Great Tit": {Threshold: 0.5},
	}

	scores := probableSpeciesFor(t, settings, rf)

	count := 0
	for _, s := range scores {
		if s.Label == "Parus major_Great Tit" {
			count++
		}
	}
	assert.Equal(t, 1, count, "a species named by both settings must be appended once")

	got := requireScoreForLabel(t, scores, "Parus major_Great Tit")
	assert.True(t, got.HasCustomConfig)
	assert.True(t, got.IsManuallyIncluded)
}

// TestAddUserOverrideSpeciesScores_UnconfiguredSpeciesCarriesNoProvenance guards
// against the flags leaking onto species the user never named.
func TestAddUserOverrideSpeciesScores_UnconfiguredSpeciesCarriesNoProvenance(t *testing.T) {
	settings, rf := overrideProvenanceSettings(t)
	settings.Realtime.Species.Config = map[string]conf.SpeciesConfig{
		"Great Tit": {Threshold: 0.5},
	}

	got := requireScoreForLabel(t, probableSpeciesFor(t, settings, rf), "Turdus merula_Common Blackbird")
	assert.False(t, got.HasCustomConfig)
	assert.False(t, got.IsManuallyIncluded)
}

// TestResolveOverrideLabelsWithSource_MatchesResolveOverrideLabels pins the two
// resolvers together: resolveOverrideLabels is a thin wrapper, and the label slice must
// stay byte-identical (order included) so adding provenance cannot perturb the
// inclusion working set or the deterministic append order of the 1.0 species.
func TestResolveOverrideLabelsWithSource_MatchesResolveOverrideLabels(t *testing.T) {
	t.Parallel()

	settings := conftest.GetTestSettings()
	settings.BirdNET.Labels = []string{"Turdus merula_Mustarastas", "Parus major_Talitiainen"}
	settings.Realtime.Species.Include = []string{"Talitiainen", "definitely not a species"}
	settings.Realtime.Species.Config = map[string]conf.SpeciesConfig{
		"zzz species": {}, "Mustarastas": {},
	}
	geoLabels := []string{"Turdus merula_Common Blackbird", "Parus major_Great Tit"}

	withSource, sources := resolveOverrideLabelsWithSource(settings, geoLabels)

	// Assert the labels concretely rather than only against resolveOverrideLabels:
	// the wrapper delegates, so a self-comparison would pass even if both returned
	// nothing. Order is part of the contract, since these labels feed the inclusion
	// working set and the deterministic append order of the equal-scored 1.0 species.
	assert.Equal(t, []string{
		"Parus major_Talitiainen",   // include entry, resolved via the classifier labels
		"Turdus merula_Mustarastas", // config key, resolved via the classifier labels
		"definitely not a species",  // unresolvable include entry, kept verbatim
		"zzz species",               // unresolvable config key, kept verbatim
	}, withSource)

	assert.Equal(t, resolveOverrideLabels(settings, geoLabels), withSource,
		"the provenance-carrying resolver must return the same labels in the same order")

	assert.Equal(t, map[string]overrideSource{
		"Parus major_Talitiainen":   {manuallyIncluded: true},
		"definitely not a species":  {manuallyIncluded: true},
		"Turdus merula_Mustarastas": {customConfig: true},
		"zzz species":               {customConfig: true},
	}, sources, "each label must record exactly the setting that named it")
}
