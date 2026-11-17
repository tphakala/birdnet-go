package processor

import (
	"io"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/tphakala/birdnet-go/internal/audiocore/buffer"
	"github.com/tphakala/birdnet-go/internal/classifier"
	"github.com/tphakala/birdnet-go/internal/conf"
	"github.com/tphakala/birdnet-go/internal/logger"
)

// Analysis buffer dimensions for the wiring tests: the buffers are only
// allocated to mark a model as running on a source and are never read.
const (
	testBufferCapacity = 1024
	testBufferOverlap  = 0
	testBufferRead     = 256
)

// newAnalysisBufferManager returns a buffer manager holding an analysis buffer
// for every (source, model) pair in models, keyed by source ID.
func newAnalysisBufferManager(t *testing.T, models map[string][]string) *buffer.Manager {
	t.Helper()
	mgr := buffer.NewManager(logger.NewSlogLogger(io.Discard, logger.LogLevelError, time.UTC))
	for sourceID, modelIDs := range models {
		for _, modelID := range modelIDs {
			require.NoError(t, mgr.AllocateAnalysis(sourceID, modelID, testBufferCapacity, testBufferOverlap, testBufferRead))
		}
	}
	return mgr
}

func TestActiveModels(t *testing.T) {
	t.Parallel()

	buffers := map[string]*buffer.AnalysisBuffer{
		"BirdNET_V2.4": nil,
		"Perch_V2":     nil,
		"Bat":          nil,
	}
	got := activeModels(buffers, func(modelID string) bool { return modelID != "Bat" })

	assert.ElementsMatch(t, []string{"BirdNET_V2.4", "Perch_V2"}, got)
}

func TestResultModelContributions_AddsZeroHitEntriesForActiveModels(t *testing.T) {
	t.Parallel()

	base := time.Date(2026, 10, 17, 12, 0, 0, 0, time.UTC)
	hits := map[string]ModelContribution{
		"BirdNET_V2.4": {
			HitCount: 4, MaxConfidence: 0.9, MinConfidence: 0.6, ConfidenceSum: 3.0,
			FirstHitAt: base, LastHitAt: base.Add(3 * time.Second),
		},
		"Perch_V2": {HitCount: 1, MaxConfidence: 0.7},
	}

	got := resultModelContributions(hits, []string{"BirdNET_V2.4", "Perch_V2", "BirdNET_V3.0"})

	require.Len(t, got, 3)

	v24 := got["BirdNET_V2.4"]
	assert.Equal(t, 4, v24.HitCount, "an active model that also hit keeps its hit count")
	assert.InDelta(t, 0.9, v24.MaxConfidence, 1e-9)
	assert.InDelta(t, 0.6, v24.MinConfidence, 1e-9)
	assert.InDelta(t, 3.0, v24.ConfidenceSum, 1e-9)
	assert.True(t, v24.HasHitStats())
	assert.Equal(t, classifier.DetectionModelInfoForID("BirdNET_V2.4"), v24.Model)

	perch := got["Perch_V2"]
	assert.Equal(t, 1, perch.HitCount)
	assert.InDelta(t, 0.7, perch.MaxConfidence, 1e-9)

	v3 := got["BirdNET_V3.0"]
	assert.Zero(t, v3.HitCount)
	assert.Zero(t, v3.MaxConfidence)
	assert.False(t, v3.HasHitStats())
	assert.Equal(t, classifier.DetectionModelInfoForID("BirdNET_V3.0"), v3.Model)
}

// TestProcessor_activeModelIDs_ReadsTheSourceFromTheBufferManager pins the wiring
// of the snapshot: only the models with a buffer on the asked source are returned,
// and a processor without a buffer manager or orchestrator reports none.
func TestProcessor_activeModelIDs_ReadsTheSourceFromTheBufferManager(t *testing.T) {
	t.Parallel()

	mgr := newAnalysisBufferManager(t, map[string][]string{
		"source-a": {"BirdNET_V2.4", "Perch_V2"},
		"source-b": {"BirdNET_V3.0"},
	})
	p := &Processor{BufferMgr: mgr, Bn: &classifier.Orchestrator{}}

	assert.ElementsMatch(t, []string{"BirdNET_V2.4", "Perch_V2"}, p.activeModelIDs("source-a"))
	assert.ElementsMatch(t, []string{"BirdNET_V3.0"}, p.activeModelIDs("source-b"))
	assert.Empty(t, p.activeModelIDs("unknown-source"))

	assert.Nil(t, (&Processor{Bn: &classifier.Orchestrator{}}).activeModelIDs("source-a"), "no buffer manager")
	assert.Nil(t, (&Processor{BufferMgr: mgr}).activeModelIDs("source-a"), "no orchestrator")
}

// TestProcessApprovedDetection_RecordsActiveModelsWithoutAHit: an approved
// detection carries its hit model unchanged and a zero-hit entry for the model that
// is running on the detection's own source without a hit; a model running only on
// another source is not recorded.
func TestProcessApprovedDetection_RecordsActiveModelsWithoutAHit(t *testing.T) {
	conf.StoreSettings(nil)
	t.Cleanup(func() { conf.StoreSettings(nil) })

	mgr := newAnalysisBufferManager(t, map[string][]string{
		e2eSource:      {"BirdNET_V2.4", "Perch_V2"},
		"other-source": {"BirdNET_V3.0"},
	})
	p := &Processor{Settings: e2eSettings(), BufferMgr: mgr, Bn: &classifier.Orchestrator{}}

	base := time.Date(2026, 6, 11, 8, 0, 0, 0, time.UTC)
	contribs := map[string]ModelContribution{
		"BirdNET_V2.4": {
			HitCount: 3, MaxConfidence: 0.9, MinConfidence: 0.6, ConfidenceSum: 2.2,
			FirstHitAt: base, LastHitAt: base.Add(4 * time.Second),
		},
	}
	item := &PendingDetection{FirstDetected: base, Source: e2eSource, ModelContributions: contribs}
	p.processApprovedDetection(item, e2eSpecies, e2eSettings())

	got := item.Detection.Result.ModelContributions
	require.Len(t, got, 2)

	hit := got["BirdNET_V2.4"]
	assert.Equal(t, 3, hit.HitCount)
	assert.InDelta(t, 2.2, hit.ConfidenceSum, 1e-9)
	assert.True(t, hit.HasHitStats())

	require.Contains(t, got, "Perch_V2", "a model running on the source without a hit is recorded")
	perch := got["Perch_V2"]
	assert.Zero(t, perch.HitCount)
	assert.False(t, perch.HasHitStats())

	assert.NotContains(t, got, "BirdNET_V3.0", "a model running only on another source is not recorded")
}
