package processor

import (
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/require"
	"github.com/tphakala/birdnet-go/internal/audiocore/buffer"
	"github.com/tphakala/birdnet-go/internal/classifier"
	"github.com/tphakala/birdnet-go/internal/conf"
	"github.com/tphakala/birdnet-go/internal/datastore"
	"github.com/tphakala/birdnet-go/internal/datastore/mocks"
	"github.com/tphakala/birdnet-go/internal/detection"
	"github.com/tphakala/birdnet-go/internal/errors"
	"github.com/tphakala/birdnet-go/internal/speciesindex"
)

var firstDailyTestNow = time.Date(2026, 6, 11, 8, 0, 0, 0, time.UTC)

type speciesSupportFunc func(string, []string, int) (bool, bool)

func (f speciesSupportFunc) SpeciesSharedByBirdModels(sci string, modelIDs []string, minModels int) (shared, ok bool) {
	return f(sci, modelIDs, minModels)
}

const (
	firstDailyTestSource       = "consensus-test"
	firstDailyTestModel        = "BirdNET_V2.4"
	firstDailyTestSecondModel  = classifier.RegistryIDPerchV2
	firstDailyTestSpecies      = "Passer domesticus"
	firstDailyTestThreshold    = 0.5
	firstDailyTestConfidence   = 0.8
	firstDailyTestDynamicMin   = 0.1
	firstDailyTestDynamicLevel = 2
)

func TestFirstDailyConsensusSeed(t *testing.T) {
	t.Parallel()
	now := firstDailyTestNow
	day := now.Format(time.DateOnly)
	settings := &conf.Settings{}
	settings.Realtime.FirstDailyConsensus.Enabled = true
	ds := mocks.NewMockInterface(t)
	ds.EXPECT().GetSpeciesSummaryData(mock.Anything, day, day).Return([]datastore.SpeciesSummaryData{{ScientificName: " PASSER DOMESTICUS "}}, nil).Once()
	p := &Processor{Ds: ds}
	item := &PendingDetection{Detection: Detections{Result: detection.Result{Timestamp: now, Species: detection.Species{ScientificName: "Parus major"}}}}
	// Seeding must preserve approvals that have not reached asynchronous persistence yet.
	p.noteAcceptedDetection(item, settings)
	p.prepareFirstDailyConsensus(now, settings)
	p.prepareFirstDailyConsensus(now, settings)
	assert.True(t, p.firstDaily.days[day].loaded)
	assert.True(t, p.firstDaily.days[day].accepted["passer domesticus"])
	assert.True(t, p.firstDaily.days[day].accepted["parus major"])
	settings.Realtime.FirstDailyConsensus.Enabled = false
	p.prepareFirstDailyConsensus(now, settings)
	assert.Empty(t, p.firstDaily.days)
}

func TestFirstDailyConsensusRetry(t *testing.T) {
	t.Parallel()
	now := firstDailyTestNow
	day := now.Format(time.DateOnly)
	settings := &conf.Settings{}
	settings.Realtime.FirstDailyConsensus.Enabled = true
	ds := mocks.NewMockInterface(t)
	ds.EXPECT().GetSpeciesSummaryData(mock.Anything, day, day).Return(nil, errors.Newf("seed unavailable").Component("processor").Build()).Once()
	ds.EXPECT().GetSpeciesSummaryData(mock.Anything, day, day).Return(nil, nil).Once()
	p := &Processor{Ds: ds}
	p.prepareFirstDailyConsensus(now, settings)
	assert.False(t, p.firstDaily.days[day].loaded)
	// A failed seed must fail open without querying again before the retry deadline.
	p.prepareFirstDailyConsensus(now.Add(firstDailySeedRetry/2), settings)
	assert.False(t, p.firstDaily.days[day].loaded)
	p.prepareFirstDailyConsensus(now.Add(firstDailySeedRetry), settings)
	assert.True(t, p.firstDaily.days[day].loaded)
}

func TestFirstDailyConsensusRetention(t *testing.T) {
	t.Parallel()
	day := firstDailyTestNow.Format(time.DateOnly)
	nextDay := firstDailyTestNow.AddDate(0, 0, 1).Format(time.DateOnly)
	oldDay := firstDailyTestNow.AddDate(0, 0, -10).Format(time.DateOnly)
	c := firstDailyConsensus{}
	c.day(day).accepted["parus major"] = true
	c.day(nextDay)
	// A late old detection must remain usable without evicting either recent day.
	assert.NotPanics(t, func() { c.day(oldDay).accepted["old"] = true })
	assert.Len(t, c.days, firstDailyRetainedDays)
	assert.Contains(t, c.days, nextDay)
	assert.True(t, c.days[day].accepted["parus major"])
}

func TestFirstDailyConsensusStrictThreshold(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name       string
		confidence float64
		want       int
	}{
		{name: "at threshold", confidence: firstDailyTestThreshold, want: 0},
		{name: "above threshold", confidence: firstDailyTestConfidence, want: 1},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			settings := &conf.Settings{}
			settings.BirdNET.Threshold = firstDailyTestThreshold
			p := &Processor{}
			item := &PendingDetection{ModelContributions: map[string]ModelContribution{
				firstDailyTestModel: {MaxConfidence: tt.confidence},
			}}
			// Equality cannot count as independent confirmation because admission uses strict >.
			assert.Equal(t, tt.want, p.countConfirmingModels(settings, item))
		})
	}
}

func TestFirstDailyConsensusDynamicThreshold(t *testing.T) {
	t.Parallel()
	const commonName = "great tit"
	now := firstDailyTestNow
	tests := []struct {
		name    string
		elapsed time.Duration
		want    bool
	}{
		{name: "active", elapsed: 0, want: true},
		{name: "expired", elapsed: 2 * time.Hour, want: false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			dt := &DynamicThreshold{Level: firstDailyTestDynamicLevel, Timer: now.Add(time.Hour)}
			p := &Processor{DynamicThresholds: map[string]*DynamicThreshold{commonName: dt}}
			assert.Equal(t, tt.want, p.dynamicThresholdLowered(commonName, firstDailyTestThreshold, firstDailyTestDynamicMin, now.Add(tt.elapsed)))
			// The gate is a reader; expiry must not reset threshold state.
			assert.Equal(t, firstDailyTestDynamicLevel, dt.Level)
			assert.Equal(t, now.Add(time.Hour), dt.Timer)
		})
	}
	assert.Equal(t, "parus major", dynamicThresholdKey("", "Parus major"))
}

func newFirstDailyConsensusGateTest(t *testing.T, scientificName string) (*Processor, *conf.Settings, *PendingDetection) {
	t.Helper()
	const capacity = 16
	now := firstDailyTestNow
	settings := &conf.Settings{}
	settings.BirdNET.Threshold = firstDailyTestThreshold
	settings.Realtime.FirstDailyConsensus.Enabled = true
	manager := buffer.NewManager(GetLogger())
	t.Cleanup(func() { manager.DeallocateSource(firstDailyTestSource) })
	for _, model := range []string{firstDailyTestModel, firstDailyTestSecondModel} {
		require.NoError(t, manager.AllocateAnalysis(firstDailyTestSource, model, capacity, 0, capacity))
	}
	p := &Processor{BufferMgr: manager}
	item := &PendingDetection{
		Source: firstDailyTestSource, BestModelID: firstDailyTestModel,
		Detection:          Detections{Result: detection.Result{Timestamp: now, Species: detection.Species{ScientificName: scientificName}}},
		ModelContributions: map[string]ModelContribution{firstDailyTestModel: {MaxConfidence: firstDailyTestConfidence}},
	}
	p.firstDaily.day(item.Detection.Result.Date()).loaded = true
	p.firstDaily.support = speciesSupportFunc(func(sci string, modelIDs []string, minModels int) (bool, bool) {
		assert.Equal(t, scientificName, sci)
		assert.ElementsMatch(t, []string{firstDailyTestModel, firstDailyTestSecondModel}, modelIDs)
		assert.Equal(t, firstDailyMinModels, minModels)
		return true, true
	})
	return p, settings, item
}

func TestFirstDailyConsensusTaxonomy(t *testing.T) {
	t.Parallel()
	p := &Processor{}
	_, genus, known := p.getTaxonomyDB().LookupGenusByScientificName(firstDailyTestSpecies)
	require.True(t, known)
	require.NotNil(t, genus)
	assert.Equal(t, avesClass, genus.Class)
	_, genus, known = p.getTaxonomyDB().LookupGenusByScientificName("Hyla arborea")
	require.True(t, known)
	require.NotNil(t, genus)
	assert.NotEqual(t, avesClass, genus.Class)
	_, _, known = p.getTaxonomyDB().LookupGenusByScientificName("Unknownconsensus species")
	assert.False(t, known)
}

func TestFirstDailyConsensusGate(t *testing.T) {
	t.Parallel()
	lowerDynamicThreshold := func(p *Processor, settings *conf.Settings, item *PendingDetection) {
		settings.Realtime.DynamicThreshold.Enabled = true
		settings.Realtime.DynamicThreshold.Min = firstDailyTestDynamicMin
		species := item.Detection.Result.Species
		p.DynamicThresholds = map[string]*DynamicThreshold{
			dynamicThresholdKey(species.CommonName, species.ScientificName): {
				Level: firstDailyTestDynamicLevel, Timer: time.Now().Add(time.Hour),
			},
		}
	}
	tests := []struct {
		name             string
		setup            func(p *Processor, settings *conf.Settings, item *PendingDetection)
		wantDiscard      bool
		wantSupportCalls int
	}{
		{name: "single bird model first detection", wantDiscard: true, wantSupportCalls: 1},
		{name: "two bird models above normal threshold", setup: func(p *Processor, settings *conf.Settings, item *PendingDetection) {
			item.ModelContributions[firstDailyTestSecondModel] = ModelContribution{MaxConfidence: firstDailyTestConfidence}
		}},
		{name: "second model exactly at normal threshold", wantDiscard: true, wantSupportCalls: 1, setup: func(p *Processor, settings *conf.Settings, item *PendingDetection) {
			item.ModelContributions[firstDailyTestSecondModel] = ModelContribution{MaxConfidence: firstDailyTestThreshold}
		}},
		{name: "species already accepted today", setup: func(p *Processor, settings *conf.Settings, item *PendingDetection) {
			p.noteAcceptedDetection(item, settings)
		}},
		{name: "seeded species with different case and whitespace", setup: func(p *Processor, settings *conf.Settings, item *PendingDetection) {
			p.firstDaily.day(item.Detection.Result.Date()).accepted[speciesindex.CanonicalKey(" PASSER DOMESTICUS ")] = true
		}},
		{name: "day not loaded", setup: func(p *Processor, settings *conf.Settings, item *PendingDetection) {
			p.firstDaily.days[item.Detection.Result.Date()].loaded = false
		}},
		{name: "feature disabled", setup: func(p *Processor, settings *conf.Settings, item *PendingDetection) {
			settings.Realtime.FirstDailyConsensus.Enabled = false
		}},
		{name: "best model is bat", setup: func(p *Processor, settings *conf.Settings, item *PendingDetection) {
			item.BestModelID = classifier.RegistryIDBat
		}},
		{name: "known non-bird taxon", setup: func(p *Processor, settings *conf.Settings, item *PendingDetection) {
			item.Detection.Result.Species.ScientificName = "Hyla arborea"
		}},
		{name: "unknown taxon", setup: func(p *Processor, settings *conf.Settings, item *PendingDetection) {
			item.Detection.Result.Species.ScientificName = "Unknownconsensus species"
		}},
		{name: "species not shared", wantSupportCalls: 1, setup: func(p *Processor, settings *conf.Settings, item *PendingDetection) {
			p.firstDaily.support = speciesSupportFunc(func(string, []string, int) (bool, bool) { return false, true })
		}},
		{name: "model support unevaluable", wantSupportCalls: 1, setup: func(p *Processor, settings *conf.Settings, item *PendingDetection) {
			p.firstDaily.support = speciesSupportFunc(func(string, []string, int) (bool, bool) { return false, false })
		}},
		{name: "unknown source topology", wantSupportCalls: 1, setup: func(p *Processor, settings *conf.Settings, item *PendingDetection) {
			p.BufferMgr.DeallocateSource(item.Source)
			// Answer "shared" whenever model IDs arrive, so a topology leak discards and
			// fails the row instead of hiding behind an unevaluable answer.
			p.firstDaily.support = speciesSupportFunc(func(_ string, modelIDs []string, _ int) (shared, ok bool) {
				return true, len(modelIDs) > 0
			})
		}},
		{name: "active lowered dynamic threshold", setup: lowerDynamicThreshold},
		{name: "custom threshold overrides dynamic exemption", wantDiscard: true, wantSupportCalls: 1, setup: func(p *Processor, settings *conf.Settings, item *PendingDetection) {
			lowerDynamicThreshold(p, settings, item)
			settings.Realtime.Species.Config = map[string]conf.SpeciesConfig{
				speciesindex.CanonicalKey(firstDailyTestSpecies): {Threshold: firstDailyTestThreshold},
			}
		}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			p, settings, item := newFirstDailyConsensusGateTest(t, firstDailyTestSpecies)
			if tt.setup != nil {
				tt.setup(p, settings, item)
			}
			support := p.speciesSupportSource()
			supportCalls := 0
			// Count calls so each row also proves how far the gate got; the argument
			// checks live in the injected support built by the helper.
			p.firstDaily.support = speciesSupportFunc(func(sci string, modelIDs []string, minModels int) (bool, bool) {
				supportCalls++
				return support.SpeciesSharedByBirdModels(sci, modelIDs, minModels)
			})
			discarded, reason := p.shouldDiscardFirstDailyDetection(item, settings)
			assert.Equal(t, tt.wantDiscard, discarded)
			if tt.wantDiscard {
				assert.Equal(t, reasonFirstDailyConsensus, reason)
			} else {
				assert.Empty(t, reason)
			}
			assert.Equal(t, tt.wantSupportCalls, supportCalls)
		})
	}
}

func TestFirstDailyConsensusAcceptanceSequence(t *testing.T) {
	t.Parallel()
	p, settings, item := newFirstDailyConsensusGateTest(t, firstDailyTestSpecies)
	const attempts = 2
	for range attempts {
		discarded, reason := p.shouldDiscardFirstDailyDetection(item, settings)
		assert.True(t, discarded, "a discard must not mark the species accepted")
		assert.Equal(t, reasonFirstDailyConsensus, reason)
		assert.NotContains(t, p.firstDaily.days[item.Detection.Result.Date()].accepted, speciesindex.CanonicalKey(firstDailyTestSpecies))
	}
	// Record a confirmed detection as the flusher does before testing a later single-model window.
	item.ModelContributions[firstDailyTestSecondModel] = ModelContribution{MaxConfidence: firstDailyTestConfidence}
	discarded, reason := p.shouldDiscardFirstDailyDetection(item, settings)
	require.False(t, discarded)
	require.Empty(t, reason)
	p.noteAcceptedDetection(item, settings)
	delete(item.ModelContributions, firstDailyTestSecondModel)
	discarded, reason = p.shouldDiscardFirstDailyDetection(item, settings)
	assert.False(t, discarded)
	assert.Empty(t, reason)
}
