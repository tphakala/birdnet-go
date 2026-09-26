package classifier

import (
	"slices"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/tphakala/birdnet-go/internal/conf"
	"github.com/tphakala/birdnet-go/internal/conf/conftest"
)

// This file is an invariance harness for the range filter. It pins the observable
// range-filter surface for a fixed fixture so that a later refactor moving the
// range-filter and occurrence logic off the primary *BirdNET instance can be proven
// behavior-identical. The golden is recorded here against the CURRENT code; the
// expected values below must not change while the range-filter behavior is being
// preserved.
//
// The diff is deliberately wider than (label, score): it pins the FULL SpeciesScore
// tuple, and it probes the occurrence index built from those rows. Dropping the
// IsSyntheticOverride skip in buildOccurrenceIndex (the #3975 native-score leak, where
// a force-included species would read the 1.0 sentinel as a native probability) would
// surface here as a synthetic-only species reporting 1.0 as its occurrence instead of
// being absent.

// probableRow is the full-tuple projection of a SpeciesScore the snapshot pins.
type probableRow struct {
	Label               string
	Score               float64
	HasCustomConfig     bool
	IsManuallyIncluded  bool
	IsSyntheticOverride bool
}

// occurrenceProbe records a single occurrence-index lookup: the resolved native
// probability and whether the species was found. A synthetic-only override must be
// absent (Found=false), never present with the 1.0 sentinel.
type occurrenceProbe struct {
	Species string
	Score   float64
	Found   bool
}

// rangeFilterInvarianceSnapshot is the byte-comparable observable surface of the
// range filter for the fixed fixture below.
type rangeFilterInvarianceSnapshot struct {
	ProbableSpecies []probableRow
	Occurrence      []occurrenceProbe
	IncludedSpecies []string
	StatusActive    bool
	StatusFellBack  bool
	MappedSpecies   int
}

// buildInvarianceFixture wires a deterministic universal-geomodel primary with a
// controlled score set and two user overrides, one that collides with a natively
// scored species (flagged in place, native score kept) and one that is not natively
// scored (a synthetic 1.0 row). No model files, no locale resolution ambiguity: the
// classifier labels and the geomodel labels share the same English common names.
func buildInvarianceFixture(t *testing.T) (*Orchestrator, *conf.Settings) {
	t.Helper()

	labels := []string{
		"Turdus merula_Common Blackbird",
		"Parus major_Great Tit",
		"Corvus corax_Northern Raven",
	}

	settings := conftest.GetTestSettings()
	settings.BirdNET.Latitude = 60.0
	settings.BirdNET.Longitude = 25.0
	settings.BirdNET.LocationConfigured = true
	settings.BirdNET.RangeFilter.Threshold = 0.01
	settings.BirdNET.RangeFilter.PassUnmappedSpecies = false
	settings.BirdNET.Locale = "en-us"
	settings.BirdNET.Labels = labels
	// Great Tit is natively scored (flag in place); Northern Raven is not (synthetic).
	settings.Realtime.Species.Include = []string{"Great Tit", "Northern Raven"}
	settings.Realtime.Species.Config = nil
	settings.Realtime.Species.Exclude = nil
	conftest.SetTestSettings(settings)
	t.Cleanup(func() { conftest.SetTestSettings(nil) })

	rf := &fakeUniversalRangeFilter{
		geoLabels: labels,
		scores: []SpeciesScore{
			{Score: 0.9, Label: "Turdus merula_Common Blackbird"},
			{Score: 0.7, Label: "Parus major_Great Tit"},
		},
		rawScores: []float32{0.9, 0.7, 0.0},
	}

	return buildTestOrchestrator(t, settings, rf), settings
}

// buildInvarianceSnapshot captures the observable range-filter surface: the
// full-tuple probable-species rows, occurrence lookups for the three taxa (probing
// the synthetic-skip guard), the persisted inclusion list, and range-filter status.
func buildInvarianceSnapshot(t *testing.T) rangeFilterInvarianceSnapshot {
	t.Helper()

	o, settings := buildInvarianceFixture(t)
	date := time.Date(2026, 5, 14, 12, 0, 0, 0, time.UTC)

	scores, err := o.GetProbableSpeciesWithSettings(date, 0, settings)
	require.NoError(t, err)

	probable := make([]probableRow, 0, len(scores))
	for _, s := range scores {
		probable = append(probable, probableRow{
			Label:               s.Label,
			Score:               s.Score,
			HasCustomConfig:     s.HasCustomConfig,
			IsManuallyIncluded:  s.IsManuallyIncluded,
			IsSyntheticOverride: s.IsSyntheticOverride,
		})
	}

	idx := buildOccurrenceIndex(scores)
	occurrence := make([]occurrenceProbe, 0, 3)
	for _, sp := range []string{"Turdus merula", "Parus major", "Corvus corax"} {
		got, found := lookupOccurrence(idx, sp)
		occurrence = append(occurrence, occurrenceProbe{Species: sp, Score: got, Found: found})
	}

	require.NoError(t, BuildRangeFilter(o))
	included := slices.Clone(conf.GetSettings().GetIncludedSpecies())
	slices.Sort(included)

	status := o.RangeFilterStatus()

	return rangeFilterInvarianceSnapshot{
		ProbableSpecies: probable,
		Occurrence:      occurrence,
		IncludedSpecies: included,
		StatusActive:    status.Active,
		StatusFellBack:  status.FellBack,
		MappedSpecies:   status.MappedSpecies,
	}
}

// TestRangeFilterInvariance_Snapshot pins the recorded golden. See the file header;
// the expected values below are the current implementation's output and must not
// change while range-filter behavior is preserved.
func TestRangeFilterInvariance_Snapshot(t *testing.T) {
	// Not parallel: the fixture publishes a process-global settings snapshot that
	// getProbableSpecies/RangeFilterStatus read via conf.CurrentOrFallback.
	got := buildInvarianceSnapshot(t)

	want := rangeFilterInvarianceSnapshot{
		ProbableSpecies: []probableRow{
			{Label: "Corvus corax_Northern Raven", Score: 1.0, IsManuallyIncluded: true, IsSyntheticOverride: true},
			{Label: "Turdus merula_Common Blackbird", Score: 0.9},
			{Label: "Parus major_Great Tit", Score: 0.7, IsManuallyIncluded: true},
		},
		Occurrence: []occurrenceProbe{
			{Species: "Turdus merula", Score: 0.9, Found: true},
			{Species: "Parus major", Score: 0.7, Found: true},
			{Species: "Corvus corax", Score: 0.0, Found: false},
		},
		IncludedSpecies: []string{
			"Corvus corax_Northern Raven",
			"Parus major_Great Tit",
			"Turdus merula_Common Blackbird",
		},
		StatusActive:   true,
		StatusFellBack: false,
		MappedSpecies:  0,
	}

	assert.Equal(t, want, got, "range-filter observable surface must stay byte-identical while the behavior is preserved")
}
