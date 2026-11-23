package processor

import (
	"slices"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/tphakala/birdnet-go/internal/classifier"
	"github.com/tphakala/birdnet-go/internal/conf"
	"github.com/tphakala/birdnet-go/internal/datastore"
	"github.com/tphakala/birdnet-go/internal/detection"
)

// Stub model IDs that are not in the model registry, so the consensus rows prove
// the filter is driven by the injected group lookup and not by registry IDs.
const (
	consensusStubBirdA = "Stub_Bird_A"
	consensusStubBirdB = "Stub_Bird_B"
	consensusStubBirdC = "Stub_Bird_C"
	consensusStubBat1  = "Stub_Bat_1"
	consensusStubBat2  = "Stub_Bat_2"
	consensusStubOther = "Stub_Ungrouped"

	consensusTestSource  = "src"
	consensusOtherSource = "other"
	consensusTestSci     = "Parus major"
	consensusTestCommon  = "Great Tit"
	consensusTestClip    = 3 * time.Second
)

var consensusBase = time.Date(2026, 10, 24, 12, 0, 0, 0, time.UTC)

// consensusAt is a point on the test audio timeline, in seconds from the base.
func consensusAt(sec int) time.Time {
	return consensusBase.Add(time.Duration(sec) * time.Second)
}

// stubConsensusModels groups the stub IDs and lets the listed models know every species.
func stubConsensusModels(knowing ...string) consensusModels {
	groups := map[string]classifier.TaxonGroup{
		consensusStubBirdA: classifier.TaxonGroupBird,
		consensusStubBirdB: classifier.TaxonGroupBird,
		consensusStubBirdC: classifier.TaxonGroupBird,
		consensusStubBat1:  classifier.TaxonGroupBat,
		consensusStubBat2:  classifier.TaxonGroupBat,
	}
	return consensusModels{
		knows: func(modelID, _ string) bool {
			return slices.Contains(knowing, modelID)
		},
		groupOf: func(modelID string) classifier.TaxonGroup { return groups[modelID] },
		clipOf:  func(string) (time.Duration, bool) { return consensusTestClip, true },
		// Stub rules name the stub IDs directly.
		registryIDOf: func(alias string) (string, bool) { return alias, slices.Contains(consensusStubIDs, alias) },
	}
}

// consensusStubIDs are the stub model IDs a stub rule may name.
var consensusStubIDs = []string{
	consensusStubBirdA, consensusStubBirdB, consensusStubBirdC, consensusStubBat1, consensusStubBat2, consensusStubOther,
}

// newConsensusTick builds a tick as prepareModelConsensus does, compiling the
// settings' rules with the models' resolver.
func newConsensusTick(models consensusModels, runs map[scoredRunKey]scoredRun, settings *conf.Settings) *consensusTick {
	return &consensusTick{
		models: models, runs: runs,
		rules: compileConsensusRules(settings.Realtime.ModelConsensus.Rules, models.registryIDOf),
	}
}

// consensusItem is a pending detection first heard at second 10 of the audio
// timeline by the given contributors; the first contributor is the best model.
func consensusItem(contributors ...string) *PendingDetection {
	item := &PendingDetection{
		Detection: Detections{Result: detection.Result{Species: detection.Species{
			CommonName: consensusTestCommon, ScientificName: consensusTestSci,
		}}},
		Source:             consensusTestSource,
		Count:              5,
		ModelContributions: map[string]ModelContribution{},
	}
	if len(contributors) > 0 {
		item.BestModelID = contributors[0]
	}
	for _, id := range contributors {
		item.ModelContributions[id] = ModelContribution{HitCount: 2, FirstHitAt: consensusAt(10), LastHitAt: consensusAt(12)}
	}
	return item
}

// runCovering is a scored run that started well before the trigger audio and
// reaches past it.
func runCovering() scoredRun { return scoredRun{from: consensusAt(0), to: consensusAt(20)} }

func consensusSettings(minModels int, allEligible bool) *conf.Settings {
	s := &conf.Settings{}
	s.Realtime.ModelConsensus = conf.ModelConsensusSettings{Rules: []conf.ModelConsensusRule{{MinModels: minModels, AllEligible: allEligible}}}
	return s
}

// rulesSettings is a settings snapshot with exactly the given rules.
func rulesSettings(rules ...conf.ModelConsensusRule) *conf.Settings {
	s := &conf.Settings{}
	s.Realtime.ModelConsensus = conf.ModelConsensusSettings{Rules: rules}
	return s
}

func runsFor(models map[string]scoredRun) map[scoredRunKey]scoredRun {
	runs := make(map[scoredRunKey]scoredRun, len(models))
	for id, r := range models {
		runs[scoredRunKey{source: consensusTestSource, model: id}] = r
	}
	return runs
}

// TestConsensusRequired pins the requirement clamp: capped at the eligible count,
// and never above one when at most one model is eligible.
func TestConsensusRequired(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name         string
		minModels    int
		allEligible  bool
		eligible     int
		agreeing     int
		wantRequired int
		wantPass     bool
	}{
		{"N2 one eligible passes", 2, false, 1, 1, 1, true},
		{"N2 two eligible one agrees discards", 2, false, 2, 1, 2, false},
		{"N2 two eligible two agree passes", 2, false, 2, 2, 2, true},
		{"N3 two eligible two agree passes (capped)", 3, false, 2, 2, 2, true},
		{"N3 three eligible two agree discards", 3, false, 3, 2, 3, false},
		{"N4 three eligible three agree passes (capped)", 4, false, 3, 3, 3, true},
		{"N4 four eligible three agree discards", 4, false, 4, 3, 4, false},
		{"all three eligible two agree discards", 2, true, 3, 2, 3, false},
		{"all three eligible three agree passes", 2, true, 3, 3, 3, true},
		{"all one eligible passes", 2, true, 1, 1, 1, true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			required := consensusRequired(tt.minModels, tt.allEligible, tt.eligible)
			assert.Equal(t, tt.wantRequired, required)
			assert.Equal(t, tt.wantPass, tt.agreeing >= required, "required %d", required)
		})
	}
}

// TestRuleCoversSpecies pins which species a rule's list covers: it reuses the
// shipped list matcher, so an any-model rule keeps the old filter's coverage.
func TestRuleCoversSpecies(t *testing.T) {
	t.Parallel()
	item := consensusItem(consensusStubBirdA)
	tests := []struct {
		name    string
		species []string
		want    bool
	}{
		{"empty list covers every species", nil, true},
		{"common name", []string{"great tit"}, true},
		{"scientific name", []string{consensusTestSci}, true},
		{"other species", []string{"Corvus corax"}, false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			assert.Equal(t, tt.want, ruleCoversSpecies(item, tt.species))
		})
	}
}

// TestConsensusTick_Evaluate pins who counts as agreeing and eligible.
func TestConsensusTick_Evaluate(t *testing.T) {
	t.Parallel()
	type row struct {
		name         string
		contributors []string
		runs         map[string]scoredRun
		models       consensusModels
		settings     *conf.Settings
		firstHitSec  map[string]int       // overrides the first hit second of a contributor
		lastHitSec   map[string]int       // overrides the last hit second of a contributor
		otherRuns    map[string]scoredRun // runs on a different source
		wantDiscard  bool
		wantEligible int
	}
	covering := runCovering()
	rangeFiltered := consensusSettings(2, false)
	rangeFiltered.BirdNET.LocationConfigured = true
	rangeFiltered.BirdNET.RangeFilter.IncludedScientificNames = map[string]struct{}{"corvus corax": {}}
	rangeFiltered.BirdNET.RangeFilter.Species = []string{"Corvus corax_Common Raven"}

	rangeIncluded := consensusSettings(2, false)
	rangeIncluded.BirdNET.LocationConfigured = true
	rangeIncluded.BirdNET.RangeFilter.IncludedScientificNames = map[string]struct{}{"parus major": {}}
	rangeIncluded.BirdNET.RangeFilter.Species = []string{"Parus major_Great Tit"}

	rows := []row{
		{name: "candidate knows and covers the trigger audio", contributors: []string{consensusStubBirdA},
			runs:   map[string]scoredRun{consensusStubBirdA: covering, consensusStubBirdB: covering},
			models: stubConsensusModels(consensusStubBirdA, consensusStubBirdB), settings: consensusSettings(2, false),
			wantDiscard: true, wantEligible: 2},
		{name: "candidate does not know the species", contributors: []string{consensusStubBirdA},
			runs:   map[string]scoredRun{consensusStubBirdA: covering, consensusStubBirdB: covering},
			models: stubConsensusModels(consensusStubBirdA), settings: consensusSettings(2, false),
			wantEligible: 1},
		{name: "candidate run starts after the trigger audio", contributors: []string{consensusStubBirdA},
			runs:   map[string]scoredRun{consensusStubBirdA: covering, consensusStubBirdB: {from: consensusAt(9), to: consensusAt(20)}},
			models: stubConsensusModels(consensusStubBirdA, consensusStubBirdB), settings: consensusSettings(2, false),
			wantEligible: 1},
		{name: "candidate run ends before the trigger audio (lagging)", contributors: []string{consensusStubBirdA},
			runs:   map[string]scoredRun{consensusStubBirdA: covering, consensusStubBirdB: {from: consensusAt(0), to: consensusAt(9)}},
			models: stubConsensusModels(consensusStubBirdA, consensusStubBirdB), settings: consensusSettings(2, false),
			wantEligible: 1},
		{name: "candidate run starts exactly at the span start and ends exactly at the latest hit", contributors: []string{consensusStubBirdA},
			runs:   map[string]scoredRun{consensusStubBirdA: covering, consensusStubBirdB: {from: consensusAt(7), to: consensusAt(12)}},
			models: stubConsensusModels(consensusStubBirdA, consensusStubBirdB), settings: consensusSettings(2, false),
			wantDiscard: true, wantEligible: 2},
		{name: "single hit window: the span ends at the first hit", contributors: []string{consensusStubBirdA},
			lastHitSec: map[string]int{consensusStubBirdA: 10},
			runs:       map[string]scoredRun{consensusStubBirdA: covering, consensusStubBirdB: {from: consensusAt(7), to: consensusAt(10)}},
			models:     stubConsensusModels(consensusStubBirdA, consensusStubBirdB), settings: consensusSettings(2, false),
			wantDiscard: true, wantEligible: 2},
		{name: "a longer clip of another agreeing model moves the span start earlier, candidate misses it", contributors: []string{consensusStubBirdA, consensusStubBirdB},
			firstHitSec:  map[string]int{consensusStubBirdA: 10, consensusStubBirdB: 11},
			runs:         map[string]scoredRun{consensusStubBirdA: covering, consensusStubBirdB: covering, consensusStubBirdC: {from: consensusAt(7), to: consensusAt(20)}},
			models:       stubConsensusModelsWithClips(map[string]time.Duration{consensusStubBirdB: 5 * time.Second}),
			settings:     consensusSettings(3, false),
			wantEligible: 2},
		{name: "a longer clip of another agreeing model moves the span start earlier, candidate covers it", contributors: []string{consensusStubBirdA, consensusStubBirdB},
			firstHitSec: map[string]int{consensusStubBirdA: 10, consensusStubBirdB: 11},
			runs:        map[string]scoredRun{consensusStubBirdA: covering, consensusStubBirdB: covering, consensusStubBirdC: {from: consensusAt(6), to: consensusAt(20)}},
			models:      stubConsensusModelsWithClips(map[string]time.Duration{consensusStubBirdB: 5 * time.Second}),
			settings:    consensusSettings(3, false),
			wantDiscard: true, wantEligible: 3},
		{name: "an agreeing model without a clip length passes", contributors: []string{consensusStubBirdA, consensusStubBirdB},
			runs: map[string]scoredRun{consensusStubBirdA: covering, consensusStubBirdB: covering, consensusStubBirdC: covering},
			models: func() consensusModels {
				m := stubConsensusModels(consensusStubBirdA, consensusStubBirdB, consensusStubBirdC)
				m.clipOf = func(id string) (time.Duration, bool) { return consensusTestClip, id != consensusStubBirdB }
				return m
			}(), settings: consensusSettings(3, false)},
		{name: "candidate run starts one second after the span start", contributors: []string{consensusStubBirdA},
			runs:   map[string]scoredRun{consensusStubBirdA: covering, consensusStubBirdB: {from: consensusAt(8), to: consensusAt(20)}},
			models: stubConsensusModels(consensusStubBirdA, consensusStubBirdB), settings: consensusSettings(2, false),
			wantEligible: 1},
		{name: "candidate scored the first hit but is still behind the latest hit", contributors: []string{consensusStubBirdA},
			runs:   map[string]scoredRun{consensusStubBirdA: covering, consensusStubBirdB: {from: consensusAt(0), to: consensusAt(11)}},
			models: stubConsensusModels(consensusStubBirdA, consensusStubBirdB), settings: consensusSettings(2, false),
			wantEligible: 1},
		{name: "candidate is behind the latest hit of another contributor", contributors: []string{consensusStubBirdA, consensusStubBirdB},
			lastHitSec: map[string]int{consensusStubBirdB: 18},
			runs:       map[string]scoredRun{consensusStubBirdA: covering, consensusStubBirdB: covering, consensusStubBirdC: {from: consensusAt(0), to: consensusAt(15)}},
			models:     stubConsensusModels(consensusStubBirdA, consensusStubBirdB, consensusStubBirdC), settings: consensusSettings(3, false),
			wantEligible: 2},
		{name: "candidate reaches the latest hit of another contributor", contributors: []string{consensusStubBirdA, consensusStubBirdB},
			lastHitSec: map[string]int{consensusStubBirdB: 18},
			runs:       map[string]scoredRun{consensusStubBirdA: covering, consensusStubBirdB: covering, consensusStubBirdC: {from: consensusAt(0), to: consensusAt(18)}},
			models:     stubConsensusModels(consensusStubBirdA, consensusStubBirdB, consensusStubBirdC), settings: consensusSettings(3, false),
			wantDiscard: true, wantEligible: 3},
		{name: "candidate without any run (no audio)", contributors: []string{consensusStubBirdA},
			runs:   map[string]scoredRun{consensusStubBirdA: covering},
			models: stubConsensusModels(consensusStubBirdA, consensusStubBirdB), settings: consensusSettings(2, false),
			wantEligible: 1},
		{name: "bat candidate does not count for a bird detection", contributors: []string{consensusStubBirdA, consensusStubBat1},
			runs:   map[string]scoredRun{consensusStubBirdA: covering, consensusStubBirdB: covering, consensusStubBat1: covering, consensusStubBat2: covering},
			models: stubConsensusModels(consensusStubBirdA, consensusStubBirdB, consensusStubBat1, consensusStubBat2), settings: consensusSettings(2, false),
			wantDiscard: true, wantEligible: 2},
		{name: "bird contributors do not count for a bat detection", contributors: []string{consensusStubBat1, consensusStubBirdA, consensusStubBirdB},
			runs:   map[string]scoredRun{consensusStubBat1: covering, consensusStubBirdA: covering, consensusStubBirdB: covering},
			models: stubConsensusModels(consensusStubBat1, consensusStubBirdA, consensusStubBirdB), settings: consensusSettings(2, false),
			wantEligible: 1},
		{name: "second bat model agrees", contributors: []string{consensusStubBat1, consensusStubBat2},
			runs:   map[string]scoredRun{consensusStubBat1: covering, consensusStubBat2: covering},
			models: stubConsensusModels(consensusStubBat1, consensusStubBat2), settings: consensusSettings(2, false),
			wantEligible: 2},
		{name: "second bat model silent", contributors: []string{consensusStubBat1},
			runs:   map[string]scoredRun{consensusStubBat1: covering, consensusStubBat2: covering},
			models: stubConsensusModels(consensusStubBat1, consensusStubBat2), settings: consensusSettings(2, false),
			wantDiscard: true, wantEligible: 2},
		{name: "two stub birds agree", contributors: []string{consensusStubBirdA, consensusStubBirdB},
			runs:   map[string]scoredRun{consensusStubBirdA: covering, consensusStubBirdB: covering},
			models: stubConsensusModels(consensusStubBirdA, consensusStubBirdB), settings: consensusSettings(2, false),
			wantEligible: 2},
		{name: "ungrouped best model passes", contributors: []string{consensusStubOther},
			runs:   map[string]scoredRun{consensusStubOther: covering, consensusStubBirdB: covering},
			models: stubConsensusModels(consensusStubOther, consensusStubBirdB), settings: consensusSettings(2, false)},
		{name: "earliest contributor has no run (timing unknown)", contributors: []string{consensusStubBirdA},
			runs:   map[string]scoredRun{consensusStubBirdB: covering},
			models: stubConsensusModels(consensusStubBirdA, consensusStubBirdB), settings: consensusSettings(2, false)},
		{name: "earliest contributor has no clip length", contributors: []string{consensusStubBirdA},
			runs: map[string]scoredRun{consensusStubBirdA: covering, consensusStubBirdB: covering},
			models: func() consensusModels {
				m := stubConsensusModels(consensusStubBirdA, consensusStubBirdB)
				m.clipOf = func(string) (time.Duration, bool) { return 0, false }
				return m
			}(), settings: consensusSettings(2, false)},
		{name: "three eligible, two agree, N3", contributors: []string{consensusStubBirdA, consensusStubBirdB},
			runs:   map[string]scoredRun{consensusStubBirdA: covering, consensusStubBirdB: covering, consensusStubBirdC: covering},
			models: stubConsensusModels(consensusStubBirdA, consensusStubBirdB, consensusStubBirdC), settings: consensusSettings(3, false),
			wantDiscard: true, wantEligible: 3},
		{name: "three eligible, two agree, all eligible", contributors: []string{consensusStubBirdA, consensusStubBirdB},
			runs:   map[string]scoredRun{consensusStubBirdA: covering, consensusStubBirdB: covering, consensusStubBirdC: covering},
			models: stubConsensusModels(consensusStubBirdA, consensusStubBirdB, consensusStubBirdC), settings: consensusSettings(2, true),
			wantDiscard: true, wantEligible: 3},
		{name: "later contributor without a run does not hide the earliest contributor's timing", contributors: []string{consensusStubBirdA, consensusStubBirdB},
			firstHitSec: map[string]int{consensusStubBirdB: 10, consensusStubBirdA: 20},
			runs:        map[string]scoredRun{consensusStubBirdB: covering, consensusStubBirdC: covering},
			models:      stubConsensusModels(consensusStubBirdA, consensusStubBirdB, consensusStubBirdC), settings: consensusSettings(3, false),
			wantDiscard: true, wantEligible: 3},
		{name: "earliest contributor without a run passes though a later one has a run", contributors: []string{consensusStubBirdA, consensusStubBirdB},
			firstHitSec: map[string]int{consensusStubBirdB: 10, consensusStubBirdA: 20},
			runs:        map[string]scoredRun{consensusStubBirdA: covering, consensusStubBirdC: covering},
			models:      stubConsensusModels(consensusStubBirdA, consensusStubBirdB, consensusStubBirdC), settings: consensusSettings(3, false)},
		{name: "candidate scored only a different source", contributors: []string{consensusStubBirdA},
			runs:      map[string]scoredRun{consensusStubBirdA: covering},
			otherRuns: map[string]scoredRun{consensusStubBirdB: covering},
			models:    stubConsensusModels(consensusStubBirdA, consensusStubBirdB), settings: consensusSettings(2, false),
			wantEligible: 1},
		{name: "earliest contributor scored only a different source (timing unknown)", contributors: []string{consensusStubBirdA},
			runs:      map[string]scoredRun{consensusStubBirdB: covering},
			otherRuns: map[string]scoredRun{consensusStubBirdA: covering},
			models:    stubConsensusModels(consensusStubBirdA, consensusStubBirdB), settings: consensusSettings(2, false)},
		{name: "range filter keeps the candidate when the species is included", contributors: []string{classifier.RegistryIDBSG},
			runs:   map[string]scoredRun{classifier.RegistryIDBSG: covering, classifier.RegistryIDBirdNETV24: covering},
			models: registryConsensusModels(classifier.RegistryIDBSG, classifier.RegistryIDBirdNETV24), settings: rangeIncluded,
			wantDiscard: true, wantEligible: 2},
		{name: "range filter excludes the candidate", contributors: []string{classifier.RegistryIDBSG},
			runs:   map[string]scoredRun{classifier.RegistryIDBSG: covering, classifier.RegistryIDBirdNETV24: covering},
			models: registryConsensusModels(classifier.RegistryIDBSG, classifier.RegistryIDBirdNETV24), settings: rangeFiltered,
			wantEligible: 1},
	}
	for _, r := range rows {
		t.Run(r.name, func(t *testing.T) {
			t.Parallel()
			runs := runsFor(r.runs)
			for id, run := range r.otherRuns {
				runs[scoredRunKey{source: consensusOtherSource, model: id}] = run
			}
			tick := newConsensusTick(r.models, runs, r.settings)
			item := consensusItem(r.contributors...)
			for id, sec := range r.firstHitSec {
				c := item.ModelContributions[id]
				c.FirstHitAt = consensusAt(sec)
				item.ModelContributions[id] = c
			}
			for id, sec := range r.lastHitSec {
				c := item.ModelContributions[id]
				c.LastHitAt = consensusAt(sec)
				item.ModelContributions[id] = c
			}
			v := tick.evaluate(item, r.settings)
			assert.Equal(t, r.wantDiscard, v.discard, "agreeing %v eligible %v required %d", v.agreeing, v.eligible, v.required)
			assert.Len(t, v.eligible, r.wantEligible, "eligible %v", v.eligible)
		})
	}

	t.Run("no contributors passes", func(t *testing.T) {
		t.Parallel()
		item := consensusItem(consensusStubBirdA)
		item.ModelContributions = nil
		settings := consensusSettings(2, false)
		tick := newConsensusTick(stubConsensusModels(consensusStubBirdA), runsFor(nil), settings)
		assert.False(t, tick.evaluate(item, settings).discard)
	})

	t.Run("empty scientific name passes", func(t *testing.T) {
		t.Parallel()
		item := consensusItem(consensusStubBirdA)
		item.Detection.Result.Species.ScientificName = ""
		models := stubConsensusModels(consensusStubBirdA, consensusStubBirdB)
		// A model that claims to know every key, so only the empty-key guard stops the discard.
		models.knows = func(string, string) bool { return true }
		settings := consensusSettings(2, false)
		tick := newConsensusTick(models, runsFor(map[string]scoredRun{consensusStubBirdA: runCovering(), consensusStubBirdB: runCovering()}), settings)
		assert.False(t, tick.evaluate(item, settings).discard)
	})

	// Two contributors tie on the first hit. The one with the smaller ID is the
	// earliest, whatever order the map iterates in, so the verdict cannot flap.
	t.Run("a tie on the first hit picks the smaller ID", func(t *testing.T) {
		t.Parallel()
		models := stubConsensusModels(consensusStubBirdA, consensusStubBirdB, consensusStubBirdC)
		settings := consensusSettings(3, false)
		for range 50 {
			// Only B has a run: A is the earliest and has none, so timing is unknown and the item passes.
			item := consensusItem(consensusStubBirdA, consensusStubBirdB)
			tick := newConsensusTick(models, runsFor(map[string]scoredRun{consensusStubBirdB: runCovering(), consensusStubBirdC: runCovering()}), settings)
			require.False(t, tick.evaluate(item, settings).discard)

			// Only A has a run: A is the earliest and has one, so C counts and the item is discarded.
			tick = newConsensusTick(models, runsFor(map[string]scoredRun{consensusStubBirdA: runCovering(), consensusStubBirdC: runCovering()}), settings)
			require.True(t, tick.evaluate(item, settings).discard)
		}
	})

	t.Run("reports the agreeing and eligible models sorted", func(t *testing.T) {
		t.Parallel()
		item := consensusItem(consensusStubBirdB, consensusStubBirdA)
		settings := consensusSettings(3, false)
		tick := newConsensusTick(stubConsensusModels(consensusStubBirdA, consensusStubBirdB, consensusStubBirdC),
			runsFor(map[string]scoredRun{
				consensusStubBirdA: runCovering(), consensusStubBirdB: runCovering(), consensusStubBirdC: runCovering(),
			}), settings)
		v := tick.evaluate(item, settings)
		assert.Equal(t, []string{consensusStubBirdA, consensusStubBirdB}, v.agreeing)
		assert.Equal(t, []string{consensusStubBirdA, consensusStubBirdB, consensusStubBirdC}, v.eligible)
		assert.Equal(t, 3, v.required)
		assert.True(t, v.discard)
	})
}

// TestConsensusTick_EvaluateRules pins how the rules of one species combine. Every
// stub model knows the species and covers the audio unless a row says otherwise.
func TestConsensusTick_EvaluateRules(t *testing.T) {
	t.Parallel()
	const (
		a, b, c = consensusStubBirdA, consensusStubBirdB, consensusStubBirdC
		unknown = "no_such_model"
	)
	named := func(models ...string) conf.ModelConsensusRule {
		return conf.ModelConsensusRule{Species: []string{consensusTestSci}, Models: models}
	}
	anyTwo := conf.ModelConsensusRule{MinModels: 2}
	type row struct {
		name        string
		rules       []conf.ModelConsensusRule
		contrib     []string
		knowing     []string // models that know the species; defaults to A, B and C
		wantDiscard bool
		wantIndex   int
		wantOutside []string
	}
	rows := []row{
		{name: "named rule, only the listed model agrees, an outside model is eligible",
			rules: []conf.ModelConsensusRule{named(a)}, contrib: []string{a}, wantDiscard: true, wantOutside: []string{b, c}},
		{name: "named rule, listed and outside models agree",
			rules: []conf.ModelConsensusRule{named(a)}, contrib: []string{a, b}},
		{name: "named rule, only an unlisted model agrees",
			rules: []conf.ModelConsensusRule{named(a)}, contrib: []string{b}},
		{name: "named rule, no outside model knows the species",
			rules: []conf.ModelConsensusRule{named(a)}, contrib: []string{a}, knowing: []string{a}},
		{name: "named rule with two listed models agreeing, an outside model is eligible",
			rules: []conf.ModelConsensusRule{named(a, b)}, contrib: []string{a, b}, wantDiscard: true, wantOutside: []string{c}},
		{name: "named rule with two listed models agreeing, the outside model cannot know the species",
			rules: []conf.ModelConsensusRule{named(a, b)}, contrib: []string{a, b}, knowing: []string{a, b}},
		{name: "named rule whose only entry does not resolve stays inert",
			rules: []conf.ModelConsensusRule{named(unknown)}, contrib: []string{a}},
		{name: "an unknown entry does not disable the known one",
			rules: []conf.ModelConsensusRule{named(a, unknown)}, contrib: []string{a}, wantDiscard: true, wantOutside: []string{b, c}},
		{name: "first of two named rules fails",
			rules: []conf.ModelConsensusRule{named(a), named(b)}, contrib: []string{a}, wantDiscard: true, wantIndex: 0, wantOutside: []string{b, c}},
		{name: "second of two named rules fails",
			rules: []conf.ModelConsensusRule{named(a), named(b)}, contrib: []string{b}, wantDiscard: true, wantIndex: 1, wantOutside: []string{a, c}},
		{name: "two named rules, both listed models agree",
			rules: []conf.ModelConsensusRule{named(a), named(b)}, contrib: []string{a, b}},
		{name: "any-model rule and named rule both fail, the first in config order is reported",
			rules: []conf.ModelConsensusRule{anyTwo, named(a)}, contrib: []string{a}, wantDiscard: true, wantIndex: 0},
		{name: "reordered, the named rule is reported",
			rules: []conf.ModelConsensusRule{named(a), anyTwo}, contrib: []string{a}, wantDiscard: true, wantIndex: 0, wantOutside: []string{b, c}},
		{name: "rule for another species does not apply",
			rules: []conf.ModelConsensusRule{{Species: []string{"Corvus corax"}, Models: []string{a}}}, contrib: []string{a}},
	}
	for _, r := range rows {
		t.Run(r.name, func(t *testing.T) {
			t.Parallel()
			knowing := r.knowing
			if knowing == nil {
				knowing = []string{a, b, c}
			}
			settings := rulesSettings(r.rules...)
			tick := newConsensusTick(stubConsensusModels(knowing...),
				runsFor(map[string]scoredRun{a: runCovering(), b: runCovering(), c: runCovering()}), settings)

			v := tick.evaluate(consensusItem(r.contrib...), settings)

			require.Equal(t, r.wantDiscard, v.discard)
			if r.wantDiscard {
				require.NotNil(t, v.rule)
				assert.Equal(t, r.wantIndex, v.rule.index)
				assert.Equal(t, r.wantOutside, v.outside)
			}
		})
	}
}

// stubConsensusModelsWithClips is stubConsensusModels for the three stub birds
// with a per-model clip length; models not listed use consensusTestClip.
func stubConsensusModelsWithClips(clips map[string]time.Duration) consensusModels {
	m := stubConsensusModels(consensusStubBirdA, consensusStubBirdB, consensusStubBirdC)
	m.clipOf = func(id string) (time.Duration, bool) {
		if clip, ok := clips[id]; ok {
			return clip, true
		}
		return consensusTestClip, true
	}
	return m
}

// registryConsensusModels uses the real registry groups for the given models and
// lets all of them know every species.
func registryConsensusModels(knowing ...string) consensusModels {
	m := stubConsensusModels(knowing...)
	m.groupOf = classifier.TaxonGroupFor
	m.registryIDOf = classifier.ResolveConfigModelID
	return m
}

// TestRecordScoredRun pins how scored runs start, grow and restart.
func TestRecordScoredRun(t *testing.T) {
	t.Parallel()
	p := &Processor{}
	window := func(source, model string, endSec int, gap bool) *classifier.Results {
		r := &classifier.Results{Source: datastore.AudioSource{ID: source}, ModelID: model}
		r.Audio.Gap = gap
		if endSec >= 0 {
			r.Audio.EndAt = consensusAt(endSec)
		}
		return r
	}
	key := func(source, model string) scoredRunKey { return scoredRunKey{source: source, model: model} }

	p.recordScoredRun(window("s1", "m1", 3, true))
	assert.Equal(t, scoredRun{from: consensusAt(3), to: consensusAt(3)}, p.scoredRuns[key("s1", "m1")], "first window starts a run at its end")

	p.recordScoredRun(window("s1", "m1", 6, false))
	p.recordScoredRun(window("s1", "m1", 9, false))
	assert.Equal(t, scoredRun{from: consensusAt(3), to: consensusAt(9)}, p.scoredRuns[key("s1", "m1")], "contiguous windows extend the run")

	p.recordScoredRun(window("s1", "m1", 5, false))
	assert.Equal(t, consensusAt(9), p.scoredRuns[key("s1", "m1")].to, "an older window does not shrink the run")

	p.recordScoredRun(window("s1", "m1", 30, true))
	assert.Equal(t, scoredRun{from: consensusAt(30), to: consensusAt(30)}, p.scoredRuns[key("s1", "m1")], "a gap window restarts the run")

	p.recordScoredRun(window("s1", "m1", -1, false))
	assert.Equal(t, consensusAt(30), p.scoredRuns[key("s1", "m1")].to, "a window without timing records nothing")

	p.recordScoredRun(window("s2", "m1", 3, false))
	p.recordScoredRun(window("s1", "m2", 3, false))
	assert.Len(t, p.scoredRuns, 3, "other sources and models have their own runs")
	assert.Equal(t, scoredRun{from: consensusAt(3), to: consensusAt(3)}, p.scoredRuns[key("s2", "m1")], "a first window without a gap still starts at its own end")
	assert.Equal(t, scoredRun{from: consensusAt(3), to: consensusAt(3)}, p.scoredRuns[key("s1", "m2")], "a first window without a gap still starts at its own end")
	assert.Equal(t, scoredRun{from: consensusAt(30), to: consensusAt(30)}, p.scoredRuns[key("s1", "m1")])

	onlyUntimed := &Processor{}
	onlyUntimed.recordScoredRun(window("s1", "m1", -1, true))
	assert.Empty(t, onlyUntimed.scoredRuns, "no run without timing")
}

// TestPrepareModelConsensus_CopiesRunsUnderLock records runs from several
// goroutines while the per-tick view copies them and the test reads the copy; run
// under -race it pins that the copy takes the lock and does not alias the live map.
func TestPrepareModelConsensus_CopiesRunsUnderLock(t *testing.T) {
	p := &Processor{Bn: &classifier.Orchestrator{}}
	settings := consensusSettings(2, false)

	var wg sync.WaitGroup
	for g := range 4 {
		wg.Go(func() {
			for i := range 50 {
				r := &classifier.Results{Source: datastore.AudioSource{ID: consensusTestSource}, ModelID: "m"}
				r.Audio.EndAt = consensusAt(g*100 + i)
				p.recordScoredRun(r)
			}
		})
	}
	for range 50 {
		tick := p.prepareModelConsensus(settings)
		require.NotNil(t, tick)
		// Touch every entry so the race detector sees the reads of the copy.
		for range tick.runs {
		}
	}
	wg.Wait()

	live := p.scoredRuns[scoredRunKey{source: consensusTestSource, model: "m"}]
	assert.Equal(t, consensusAt(349), live.to, "the live run holds the latest window of the busiest writer")

	key := scoredRunKey{source: consensusTestSource, model: "m"}
	snapshot := p.prepareModelConsensus(settings)
	late := &classifier.Results{Source: datastore.AudioSource{ID: consensusTestSource}, ModelID: "m"}
	late.Audio.EndAt = consensusAt(1000)
	p.recordScoredRun(late)
	assert.Equal(t, consensusAt(349), snapshot.runs[key].to, "a window recorded after the copy does not change the tick")
}

// TestPrepareModelConsensus_NilWhenNotApplicable pins the fail-open exits.
func TestPrepareModelConsensus_NilWhenNotApplicable(t *testing.T) {
	off := consensusSettings(2, false)
	off.Realtime.ModelConsensus.Rules = nil

	assert.Nil(t, (&Processor{Bn: &classifier.Orchestrator{}}).prepareModelConsensus(off), "filter off")
	assert.Nil(t, (&Processor{}).prepareModelConsensus(consensusSettings(2, false)), "no model view")
	tick := (&Processor{Bn: &classifier.Orchestrator{}}).prepareModelConsensus(consensusSettings(2, false))
	require.NotNil(t, tick)
	assert.Equal(t, classifier.TaxonGroupBird, tick.models.groupOf(classifier.RegistryIDBirdNETV24), "groups come from the model registry")
	assert.Equal(t, classifier.TaxonGroupBat, tick.models.groupOf(classifier.RegistryIDBat), "groups come from the model registry")
	clip, ok := tick.models.clipOf(classifier.RegistryIDBirdNETV24)
	assert.True(t, ok, "a registered model has a clip length")
	assert.Positive(t, clip)
	assert.False(t, tick.models.knows(classifier.RegistryIDBirdNETV24, "parus major"), "an orchestrator with no models knows no species")
}

// TestShouldDiscardDetection_PrivacyBeforeConsensus pins that consensus is the
// last check, so an earlier filter keeps the discard attribution.
func TestShouldDiscardDetection_PrivacyBeforeConsensus(t *testing.T) {
	t.Parallel()

	p := &Processor{LastHumanDetection: map[string]HumanDetection{}}
	settings := consensusSettings(2, false)
	settings.Realtime.PrivacyFilter.Enabled = true
	// The ultrasonic filter would discard the tagged item too, so the privacy reason
	// proves the privacy check runs first.
	settings.Bat.UltrasonicFilter = conf.UltrasonicFilterConfig{Enabled: true, DiscardUnlikely: true}
	item := consensusItem(consensusStubBirdA)
	item.Detection.Result.Unlikely = true
	item.FirstDetected = consensusAt(10)
	p.LastHumanDetection[item.Source] = HumanDetection{Time: consensusAt(10)}
	tick := newConsensusTick(stubConsensusModels(consensusStubBirdA, consensusStubBirdB),
		runsFor(map[string]scoredRun{consensusStubBirdA: runCovering(), consensusStubBirdB: runCovering()}), settings)

	priv := p.checkPrivacy(settings, item, time.Now())
	discard, reason := p.shouldDiscardDetection(item, settings, 1, &priv, tick)
	assert.True(t, discard)
	assert.Equal(t, reasonPrivacyFilter, reason)

	settings.Realtime.PrivacyFilter.Enabled = false
	settings.Bat.UltrasonicFilter.DiscardUnlikely = false
	priv = p.checkPrivacy(settings, item, time.Now())
	discard, reason = p.shouldDiscardDetection(item, settings, 1, &priv, tick)
	assert.True(t, discard)
	assert.Equal(t, reasonModelConsensus, reason)

	discard, _ = p.shouldDiscardDetection(item, settings, 1, &priv, nil)
	assert.False(t, discard, "a nil tick skips the check")
}

// TestShouldDiscardDetection_OtherChecksBeforeConsensus pins that the minimum count,
// dog bark and daylight checks run before the ultrasonic filter, and the ultrasonic
// filter runs before consensus, so each keeps its own discard reason when a later
// check would discard the same detection.
func TestShouldDiscardDetection_OtherChecksBeforeConsensus(t *testing.T) {
	const minDetections = 5
	sunTimes, err := newTestSunCalc().GetSunEventTimes(referenceDate())
	require.NoError(t, err)

	tests := []struct {
		name       string
		count      int
		enable     func(p *Processor, s *conf.Settings, on bool)
		wantReason func(t *testing.T, reason string)
	}{
		{
			name:   "minimum count",
			count:  minDetections - 1,
			enable: func(*Processor, *conf.Settings, bool) {},
			wantReason: func(t *testing.T, reason string) {
				t.Helper()
				assert.Contains(t, reason, "false positive, matched")
			},
		},
		{
			name:  "dog bark",
			count: minDetections,
			enable: func(p *Processor, s *conf.Settings, on bool) {
				s.Realtime.DogBarkFilter = conf.DogBarkFilterSettings{
					Enabled:  on,
					Remember: 10,
					Species:  []string{strings.ToLower(consensusTestCommon)},
				}
				p.LastDogDetection = map[string]time.Time{consensusTestSource: time.Now()}
			},
			wantReason: func(t *testing.T, reason string) {
				t.Helper()
				assert.Equal(t, "recent dog bark", reason)
			},
		},
		{
			name:  "daylight",
			count: minDetections,
			enable: func(p *Processor, s *conf.Settings, on bool) {
				s.Realtime.DaylightFilter = conf.DaylightFilterSettings{Enabled: on}
				p.sunCalc = newTestSunCalc()
				p.daylightFilterAll = true
			},
			wantReason: func(t *testing.T, reason string) {
				t.Helper()
				assert.Equal(t, reasonDaylightFilter, reason)
			},
		},
		{
			name:   "ultrasonic filter",
			count:  minDetections,
			enable: func(*Processor, *conf.Settings, bool) {}, // the loop turns the ultrasonic filter on for every row
			wantReason: func(t *testing.T, reason string) {
				t.Helper()
				assert.Equal(t, reasonUltrasonicFilter, reason)
			},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			p := mergeTestProcessor(t)
			settings := consensusSettings(2, false)
			item := consensusItem(consensusStubBirdA)
			item.Count = tt.count
			item.Detection.Result.Unlikely = true
			item.FirstDetected = sunTimes.CivilDawn.Add(3 * time.Hour)
			tick := newConsensusTick(stubConsensusModels(consensusStubBirdA, consensusStubBirdB),
				runsFor(map[string]scoredRun{consensusStubBirdA: runCovering(), consensusStubBirdB: runCovering()}), settings)
			discard := func() (bool, string) {
				p.Settings = settings
				conf.StoreSettings(settings)
				priv := p.checkPrivacy(settings, item, time.Now())
				return p.shouldDiscardDetection(item, settings, minDetections, &priv, tick)
			}

			// Every row also has the ultrasonic filter discarding the tagged item, so a
			// row's own reason proves its check runs before the ultrasonic one.
			setFilters := func(on bool) {
				tt.enable(p, settings, on)
				settings.Bat.UltrasonicFilter = conf.UltrasonicFilterConfig{Enabled: true, DiscardUnlikely: on}
			}

			setFilters(true)
			discarded, reason := discard()
			require.True(t, discarded)
			tt.wantReason(t, reason)

			// Control: with the other filters off, the same item is discarded by consensus.
			setFilters(false)
			if tt.name == "minimum count" {
				item.Count = minDetections
			}
			discarded, reason = discard()
			assert.True(t, discarded)
			assert.Equal(t, reasonModelConsensus, reason)
		})
	}
}

// consensusFlushRig drives windows of two registry bird models through
// processDetections on one audio timeline, then flushes.
type consensusFlushRig struct {
	p       *Processor
	models  []string
	knowing []string
	endSec  int
	fed     bool
}

// newConsensusFlushRig scores windows for every model in models; the models that
// can classify the species are the ones in rig.knowing, which a test may extend.
func newConsensusFlushRig(t *testing.T, models ...string) *consensusFlushRig {
	t.Helper()
	p := mergeTestProcessor(t)
	// The hit path resolves and canonicalizes species through the orchestrator, so
	// the rig needs one even though the model view comes from consensusModelsFn.
	p.Bn = &classifier.Orchestrator{}
	r := &consensusFlushRig{p: p, models: models}
	p.consensusModelsFn = func() consensusModels { return registryConsensusModels(r.knowing...) }
	return r
}

// scenario feeds three windows per model (only hitters detect the species), makes
// the pending item due, flushes, and reports whether the item was approved.
func (r *consensusFlushRig) scenario(t *testing.T, hitters ...string) (approved bool) {
	t.Helper()
	for range 3 {
		r.endSec += int(consensusTestClip / time.Second)
		for _, m := range r.models {
			w := classifier.Results{
				Source:    datastore.AudioSource{ID: mergeTestSource},
				ModelID:   m,
				StartTime: consensusAt(r.endSec).Add(-consensusTestClip),
				Audio:     classifier.AudioTiming{EndAt: consensusAt(r.endSec), Gap: !r.fed},
			}
			if slices.Contains(hitters, m) {
				w.Results = []datastore.Results{{Species: mergeTestLabel, Confidence: 0.9}}
			}
			r.p.processDetections(w)
		}
		r.fed = true
	}
	r.p.pendingMutex.Lock()
	for k := range r.p.pendingDetections {
		item := r.p.pendingDetections[k]
		item.FlushDeadline = time.Now().Add(-time.Second)
		r.p.pendingDetections[k] = item
	}
	r.p.pendingMutex.Unlock()

	pending, flushed := r.p.flushPendingDetections()
	require.Equal(t, 1, pending)
	assert.Empty(t, r.p.pendingDetections, "the item is either approved or discarded")
	return flushed == 1
}

// TestFlushPendingDetections_ModelConsensus changes one setting between ticks and
// pins that it takes effect on the next flush, with the scored runs recorded by
// processDetections.
func TestFlushPendingDetections_ModelConsensus(t *testing.T) {
	const (
		modelA = classifier.RegistryIDBirdNETV24
		modelB = classifier.RegistryIDPerchV2
	)
	modelC := classifier.RegistryIDBirdNETV3
	rig := newConsensusFlushRig(t, modelA, modelB, modelC)
	rig.knowing = []string{modelA, modelB}
	set := func(c conf.ModelConsensusSettings) { rig.p.Settings.Realtime.ModelConsensus = c }

	anyRule := func(minModels int, allEligible bool, species ...string) conf.ModelConsensusSettings {
		return conf.ModelConsensusSettings{Rules: []conf.ModelConsensusRule{{Species: species, MinModels: minModels, AllEligible: allEligible}}}
	}

	set(conf.ModelConsensusSettings{})
	assert.True(t, rig.scenario(t, modelA), "no rules keeps a single-model detection")

	set(anyRule(2, false))
	assert.False(t, rig.scenario(t, modelA), "an every-species rule discards a single-model detection")
	assert.True(t, rig.scenario(t, modelA, modelB), "two agreeing models pass")

	set(anyRule(2, false, "Corvus corax"))
	assert.True(t, rig.scenario(t, modelA), "a rule for another species does not filter this one")

	set(anyRule(2, false, mergeTestScientific))
	assert.False(t, rig.scenario(t, modelA), "a rule for this species filters it")

	set(anyRule(2, false))
	rig.knowing = []string{modelA, modelB, modelC}
	assert.True(t, rig.scenario(t, modelA, modelB), "two of three agreeing meets minmodels 2")

	set(anyRule(3, false))
	assert.False(t, rig.scenario(t, modelA, modelB), "minmodels 3 needs the third model")

	set(anyRule(2, true))
	assert.False(t, rig.scenario(t, modelA, modelB), "all eligible needs the third model")
	assert.True(t, rig.scenario(t, modelA, modelB, modelC), "all three agree")
}

// TestFlushPendingDetections_ModelConsensusRules runs a rule that names Perch through
// the flush rig with real registry IDs and config aliases, so it covers alias
// resolution and the named-rule branch end to end.
func TestFlushPendingDetections_ModelConsensusRules(t *testing.T) {
	const (
		perch   = classifier.RegistryIDPerchV2
		plus    = classifier.RegistryIDBirdNETPlusV3
		birdnet = classifier.RegistryIDBirdNETV24
	)
	perchOnly := conf.ModelConsensusSettings{Rules: []conf.ModelConsensusRule{
		{Species: []string{mergeTestScientific}, Models: []string{"perch_v2"}},
	}}

	// The first scenario's windows carry Gap, so no other model covers the span and every
	// first verdict passes; warm the rig up with the filter off.
	warm := func(rig *consensusFlushRig) {
		rig.p.Settings.Realtime.ModelConsensus = conf.ModelConsensusSettings{}
		require.True(t, rig.scenario(t, perch))
		rig.p.Settings.Realtime.ModelConsensus = perchOnly
	}

	t.Run("the issue table", func(t *testing.T) {
		rig := newConsensusFlushRig(t, perch, plus, birdnet)
		rig.knowing = []string{perch, plus, birdnet}
		warm(rig)

		assert.False(t, rig.scenario(t, perch), "Perch alone is discarded")
		assert.True(t, rig.scenario(t, perch, plus), "Perch and BirdNET+ are accepted")
		assert.True(t, rig.scenario(t, plus), "BirdNET+ alone is accepted")
		assert.True(t, rig.scenario(t, birdnet), "BirdNET 2.4 alone is accepted")
	})

	t.Run("fail-open after the other models are unloaded", func(t *testing.T) {
		rig := newConsensusFlushRig(t, perch, plus, birdnet)
		rig.knowing = []string{perch, plus, birdnet}
		warm(rig)
		require.False(t, rig.scenario(t, perch), "control: Perch alone is discarded while the others are loaded")

		// The snapshot after an unload no longer knows the species for the others, while
		// their stale runs remain.
		rig.knowing = []string{perch}
		assert.True(t, rig.scenario(t, perch), "no eligible model outside the list: Perch alone passes")
	})

	t.Run("fail-open when the other models do not analyse the source", func(t *testing.T) {
		rig := newConsensusFlushRig(t, perch)
		rig.knowing = []string{perch, plus, birdnet}
		warm(rig)
		assert.True(t, rig.scenario(t, perch), "no other model scored this audio: Perch alone passes")
	})
}

// TestPrepareModelConsensus_ResolvesRuleModels pins the production branch: without an
// injected model view, rule aliases resolve through the model registry.
func TestPrepareModelConsensus_ResolvesRuleModels(t *testing.T) {
	p := &Processor{Bn: &classifier.Orchestrator{}}
	settings := rulesSettings(conf.ModelConsensusRule{Models: []string{"perch_v2", "future_model"}})

	tick := p.prepareModelConsensus(settings)

	require.NotNil(t, tick)
	require.Len(t, tick.rules, 1)
	assert.Equal(t, map[string]struct{}{classifier.RegistryIDPerchV2: {}}, tick.rules[0].listed)
	assert.True(t, tick.rules[0].namesModels())
}

// TestRegistryClipLength pins the production clip length lookup: registered models
// report their window length, unknown IDs report none.
func TestRegistryClipLength(t *testing.T) {
	t.Parallel()
	clip, ok := registryClipLength(classifier.RegistryIDBirdNETV24)
	assert.True(t, ok)
	assert.Equal(t, consensusTestClip, clip)

	_, ok = registryClipLength(consensusStubBirdA)
	assert.False(t, ok)
}

// TestDiscardForModelConsensus_VerdictIndependentOfDebug pins that the debug switch does not change
// the verdict.
func TestDiscardForModelConsensus_VerdictIndependentOfDebug(t *testing.T) {
	t.Parallel()
	p := &Processor{}
	tick := newConsensusTick(stubConsensusModels(consensusStubBirdA, consensusStubBirdB),
		runsFor(map[string]scoredRun{consensusStubBirdA: runCovering(), consensusStubBirdB: runCovering()}), consensusSettings(2, false))
	for _, debug := range []bool{false, true} {
		settings := consensusSettings(2, false)
		settings.Realtime.ModelConsensus.Debug = debug
		assert.True(t, p.discardForModelConsensus(consensusItem(consensusStubBirdA), settings, tick), "debug %v", debug)
		assert.False(t, p.discardForModelConsensus(consensusItem(consensusStubBirdA, consensusStubBirdB), settings, tick), "debug %v", debug)
	}
}
