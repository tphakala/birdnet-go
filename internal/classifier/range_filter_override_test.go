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

// TestAddUserOverrideSpeciesScores_Provenance is the regression test for issue
// #3974 and its neighbours. Each case configures the overrides differently and
// asserts the provenance that reaches the settings UI, plus the score, since a
// species the range filter already scored must be flagged in place rather than
// promoted to the always-active 1.0 sentinel.
func TestAddUserOverrideSpeciesScores_Provenance(t *testing.T) {
	tests := []struct {
		name              string
		include           []string
		config            map[string]conf.SpeciesConfig
		label             string
		wantCustomConfig  bool
		wantManualInclude bool
		wantScore         float64
	}{
		{
			// Issue #3974: the config key is an alias ("Great Tit", the geomodel's
			// English common name) that resolves to a canonical label the settings UI
			// cannot match against the key, so the provenance has to travel with the
			// score instead. A config key alone must not claim manual inclusion: the
			// two badges mean different things.
			name:             "config alias carries custom-config provenance",
			config:           map[string]conf.SpeciesConfig{"Great Tit": {Threshold: 0.5}},
			label:            "Parus major_Great Tit",
			wantCustomConfig: true,
			wantScore:        1.0,
		},
		{
			// The range filter already scored this species, so the override appends
			// nothing and the flag must be set on the existing entry. The
			// range-filter score has to survive, or a merely configured species would
			// be silently promoted to always-active.
			name:             "config on an already-scored species flags in place",
			config:           map[string]conf.SpeciesConfig{"Common Blackbird": {Threshold: 0.5}},
			label:            "Turdus merula_Common Blackbird",
			wantCustomConfig: true,
			wantScore:        0.9,
		},
		{
			name:              "include entry is not reported as configured",
			include:           []string{"Great Tit"},
			label:             "Parus major_Great Tit",
			wantManualInclude: true,
			wantScore:         1.0,
		},
		{
			name:              "include and config union onto one entry",
			include:           []string{"Great Tit"},
			config:            map[string]conf.SpeciesConfig{"Great Tit": {Threshold: 0.5}},
			label:             "Parus major_Great Tit",
			wantCustomConfig:  true,
			wantManualInclude: true,
			wantScore:         1.0,
		},
		{
			// Guards against the flags leaking onto species the user never named.
			name:      "species the user never named carries no provenance",
			config:    map[string]conf.SpeciesConfig{"Great Tit": {Threshold: 0.5}},
			label:     "Turdus merula_Common Blackbird",
			wantScore: 0.9,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			settings, rf := overrideProvenanceSettings(t)
			settings.Realtime.Species.Include = tt.include
			settings.Realtime.Species.Config = tt.config

			scores := probableSpeciesFor(t, settings, rf)

			// A species named by both settings must be appended once, not twice, or
			// it would show up doubled in the Active Species list and the CSV export.
			count := 0
			for _, s := range scores {
				if s.Label == tt.label {
					count++
				}
			}
			assert.Equal(t, 1, count, "species must appear exactly once")

			got := requireScoreForLabel(t, scores, tt.label)
			assert.Equal(t, tt.wantCustomConfig, got.HasCustomConfig, "HasCustomConfig")
			assert.Equal(t, tt.wantManualInclude, got.IsManuallyIncluded, "IsManuallyIncluded")
			assert.InDelta(t, tt.wantScore, got.Score, 1e-9, "score")
		})
	}
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
