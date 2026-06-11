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
