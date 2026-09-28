package processor

import (
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/require"

	"github.com/tphakala/birdnet-go/internal/analysis/jobqueue"
	audioBuffer "github.com/tphakala/birdnet-go/internal/audiocore/buffer"
	"github.com/tphakala/birdnet-go/internal/conf"
	"github.com/tphakala/birdnet-go/internal/conf/conftest"
	"github.com/tphakala/birdnet-go/internal/datastore"
	"github.com/tphakala/birdnet-go/internal/datastore/mocks"
	"github.com/tphakala/birdnet-go/internal/detection"
	"github.com/tphakala/birdnet-go/internal/errors"
	"github.com/tphakala/birdnet-go/internal/speciesindex"
)

const (
	fdSource    = "src"
	fdModelA    = "BirdNET_V2.4"
	fdModelB    = "Perch_V2"
	fdSpecies   = "Parus major"
	fdOtherBird = "Turdus merula"
)

// Analysis buffer geometry for the test sources; the rule only reads which
// models have a buffer on a source, so the sizes are arbitrary.
const (
	fdBufferCapacity = 1024
	fdBufferOverlap  = 0
	fdBufferRead     = 512
)

var fdNow = time.Date(2026, 5, 10, 12, 0, 0, 0, time.Local)

// firstDailySettings returns settings with the first-daily consensus rule set to enabled.
func firstDailySettings(enabled bool) *conf.Settings {
	s := &conf.Settings{}
	s.Realtime.FirstDailyConsensus.Enabled = enabled
	return s
}

// newFirstDailyProcessor returns a processor whose source is analyzed by models,
// and its mock datastore, on which the caller sets the seed expectation.
func newFirstDailyProcessor(t *testing.T, models ...string) (*Processor, *mocks.MockInterface) {
	t.Helper()
	mgr := audioBuffer.NewManager(GetLogger())
	for _, id := range models {
		require.NoError(t, mgr.AllocateAnalysis(fdSource, id, fdBufferCapacity, fdBufferOverlap, fdBufferRead))
	}
	ds := mocks.NewMockInterface(t)
	return &Processor{Ds: ds, BufferMgr: mgr}, ds
}

// expectSeed makes the next seed query for fdNow's day return the given species or error.
func expectSeed(ds *mocks.MockInterface, err error, species ...string) {
	rows := make([]datastore.SpeciesSummaryData, 0, len(species))
	for _, sci := range species {
		rows = append(rows, datastore.SpeciesSummaryData{ScientificName: sci})
	}
	today := fdNow.Format(time.DateOnly)
	ds.EXPECT().GetSpeciesSummaryData(mock.Anything, today, today).Return(rows, err).Once()
}

// firstDailyItem builds a pending detection of sci at ts contributed by models.
func firstDailyItem(sci string, ts time.Time, models ...string) *PendingDetection {
	contribs := make(map[string]ModelContribution, len(models))
	for _, id := range models {
		contribs[id] = ModelContribution{HitCount: 1, MaxConfidence: 0.9}
	}
	return &PendingDetection{
		Detection:          Detections{Result: detection.Result{Timestamp: ts, Species: detection.Species{ScientificName: sci}}},
		Source:             fdSource,
		ModelContributions: contribs,
	}
}

// setModelSpecies makes each of models able to predict every species in species.
// It stands in for the orchestrator, which these tests do not load.
func setModelSpecies(p *Processor, species []string, models ...string) {
	p.firstDaily.modelSpecies = make(map[string]map[string]struct{}, len(models))
	for _, id := range models {
		set := make(map[string]struct{}, len(species))
		for _, sci := range species {
			set[speciesindex.CanonicalKey(sci)] = struct{}{}
		}
		p.firstDaily.modelSpecies[id] = set
	}
}

// allSpecies is every species the tests use.
var allSpecies = []string{fdSpecies, fdOtherBird}

func TestLacksFirstDailyConsensus(t *testing.T) {
	t.Parallel()
	bothModels := []string{fdModelA, fdModelB}
	on := firstDailySettings(true)
	whitelisted := firstDailySettings(true)
	whitelisted.Realtime.FirstDailyConsensus.Whitelist = []string{"parus MAJOR"}

	tests := []struct {
		name          string
		sourceModels  []string
		knowingModels []string
		item          *PendingDetection
		settings      *conf.Settings
		want          bool
	}{
		{"single model first of day is discarded", bothModels, bothModels, firstDailyItem(fdSpecies, fdNow, fdModelA), on, true},
		{"two models confirm", bothModels, bothModels, firstDailyItem(fdSpecies, fdNow, fdModelA, fdModelB), on, false},
		{"species already recorded today", bothModels, bothModels, firstDailyItem(fdOtherBird, fdNow, fdModelA), on, false},
		{"detection dated yesterday fails open", bothModels, bothModels, firstDailyItem(fdSpecies, fdNow.AddDate(0, 0, -1), fdModelA), on, false},
		{"only one source model knows the species", bothModels, []string{fdModelA}, firstDailyItem(fdSpecies, fdNow, fdModelA), on, false},
		{"source analyzed by one model", []string{fdModelA}, bothModels, firstDailyItem(fdSpecies, fdNow, fdModelA), on, false},
		{"whitelisted species", bothModels, bothModels, firstDailyItem(fdSpecies, fdNow, fdModelA), whitelisted, false},
		{"rule disabled", bothModels, bothModels, firstDailyItem(fdSpecies, fdNow, fdModelA), firstDailySettings(false), false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			p, ds := newFirstDailyProcessor(t, tt.sourceModels...)
			expectSeed(ds, nil, fdOtherBird)
			p.prepareFirstDailyConsensus(fdNow, firstDailySettings(true))
			setModelSpecies(p, allSpecies, tt.knowingModels...)
			assert.Equal(t, tt.want, p.lacksFirstDailyConsensus(tt.item, tt.settings))
		})
	}
}

func TestFirstDailyConsensus_ApprovalLiftsRule(t *testing.T) {
	t.Parallel()
	p, ds := newFirstDailyProcessor(t, fdModelA, fdModelB)
	expectSeed(ds, nil)
	p.prepareFirstDailyConsensus(fdNow, firstDailySettings(true))
	setModelSpecies(p, allSpecies, fdModelA, fdModelB)

	p.noteFirstDailyApproval(firstDailyItem(fdSpecies, fdNow, fdModelA, fdModelB))

	assert.False(t, p.lacksFirstDailyConsensus(firstDailyItem(fdSpecies, fdNow, fdModelA), firstDailySettings(true)))
}

func TestPrepareFirstDailyConsensus_SeedFailureFailsOpenAndRetries(t *testing.T) {
	t.Parallel()
	on := firstDailySettings(true)
	p, ds := newFirstDailyProcessor(t, fdModelA, fdModelB)
	expectSeed(ds, errors.NewStd("database unavailable"))
	item := firstDailyItem(fdSpecies, fdNow, fdModelA)

	p.prepareFirstDailyConsensus(fdNow, on)
	setModelSpecies(p, allSpecies, fdModelA, fdModelB)
	assert.False(t, p.lacksFirstDailyConsensus(item, on), "an unloaded day fails open")

	p.prepareFirstDailyConsensus(fdNow.Add(time.Second), on) // within the retry delay: no query
	expectSeed(ds, nil)
	p.prepareFirstDailyConsensus(fdNow.Add(firstDailySeedRetry), on)
	assert.True(t, p.lacksFirstDailyConsensus(item, on), "a successful retry activates the rule")
}

func TestPrepareFirstDailyConsensus_DisableResetsState(t *testing.T) {
	t.Parallel()
	p, ds := newFirstDailyProcessor(t, fdModelA, fdModelB)
	expectSeed(ds, nil, fdOtherBird)
	p.prepareFirstDailyConsensus(fdNow, firstDailySettings(true))

	p.prepareFirstDailyConsensus(fdNow, firstDailySettings(false))

	assert.Equal(t, firstDailyConsensus{}, p.firstDaily, "re-enabling must reseed from the datastore")
}

// TestFlushPendingDetections_FirstDailyConsensus drives the rule through the real
// flush cycle: the seed, the gate and the approval hook. It stores global
// settings, so it must not run in parallel.
func TestFlushPendingDetections_FirstDailyConsensus(t *testing.T) {
	now := time.Now()
	settings := firstDailySettings(true)
	previous := conf.GetSettings()
	conftest.SetTestSettings(settings)
	t.Cleanup(func() { conf.StoreSettings(previous) })

	p, ds := newFirstDailyProcessor(t, fdModelA, fdModelB)
	ds.EXPECT().GetSpeciesSummaryData(mock.Anything, now.Format(time.DateOnly), now.Format(time.DateOnly)).
		Return(nil, nil).Once()
	p.Settings = settings
	// A queue that is never started rejects the approved detection's actions, so
	// approval runs without touching the mock datastore.
	p.JobQueue = jobqueue.NewJobQueue()
	p.prepareFirstDailyConsensus(now, settings) // seeds today and the (empty) model sets
	setModelSpecies(p, allSpecies, fdModelA, fdModelB)

	due := func(models ...string) PendingDetection {
		item := firstDailyItem(fdSpecies, now, models...)
		item.Count = 1
		item.FlushDeadline = now.Add(-time.Second)
		return *item
	}
	p.pendingDetections = map[string]PendingDetection{"single": due(fdModelA)}

	_, flushed := p.flushPendingDetections()

	assert.Zero(t, flushed, "a single-model first detection is discarded")
	assert.Empty(t, p.pendingDetections)

	p.pendingDetections = map[string]PendingDetection{"confirmed": due(fdModelA, fdModelB)}
	_, flushed = p.flushPendingDetections()
	require.Equal(t, 1, flushed, "a two-model first detection is approved")

	p.pendingDetections = map[string]PendingDetection{"single": due(fdModelA)}
	_, flushed = p.flushPendingDetections()
	assert.Equal(t, 1, flushed, "once approved, the species needs only one model")
}
