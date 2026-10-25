package processor

import (
	"maps"
	"slices"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/tphakala/birdnet-go/internal/classifier"
	"github.com/tphakala/birdnet-go/internal/conf"
	"github.com/tphakala/birdnet-go/internal/datastore"
)

const (
	mergeTestSource     = "src"
	mergeTestModelB     = classifier.RegistryIDPerchV2
	mergeTestScientific = "Parus major"
	mergeTestCommon     = "Great Tit"
	mergeTestLabel      = mergeTestScientific + "_" + mergeTestCommon
	mergeTestWindow     = 12 * time.Second
	mergeTestClip       = 3 * time.Second
	mergeTestEpsilon    = 1e-9
)

// mergeTestProcessor builds a processor with only what the pending merge needs.
// Global settings are reset so currentSettings falls back to p.Settings.
func mergeTestProcessor(t *testing.T) *Processor {
	t.Helper()
	conf.StoreSettings(nil)
	t.Cleanup(func() { conf.StoreSettings(nil) })
	return &Processor{
		Settings:          &conf.Settings{},
		pendingDetections: map[string]PendingDetection{},
	}
}

// onlyPending returns the single pending entry of p.
func onlyPending(t *testing.T, p *Processor) PendingDetection {
	t.Helper()
	pending := slices.Collect(maps.Values(p.pendingDetections))
	require.Len(t, pending, 1)
	return pending[0]
}

// TestMergePendingHit_ModelHitStatsSumToCount pins that every merged hit lands in
// its own model's statistics through both the create and the update branch, so the
// per-model hit counts always add up to the pending count.
func TestMergePendingHit_ModelHitStatsSumToCount(t *testing.T) {
	base := time.Date(2026, 10, 24, 12, 0, 0, 0, time.UTC)
	at := func(sec int) time.Time { return base.Add(time.Duration(sec) * time.Second) }

	type hit struct {
		model      string
		confidence float64
		endSec     int
	}
	type want struct {
		count    int
		min, max float64
		sum      float64
		firstSec int
		lastSec  int
	}
	tests := []struct {
		name      string
		hits      []hit
		wantBest  string
		wantConf  float64
		wantModel map[string]want
	}{
		{
			name:     "first model creates the entry",
			hits:     []hit{{classifier.RegistryIDBirdNETV24, 0.6, 3}, {classifier.RegistryIDBirdNETV24, 0.9, 6}, {mergeTestModelB, 0.95, 7}, {classifier.RegistryIDBirdNETV24, 0.7, 9}},
			wantBest: mergeTestModelB,
			wantConf: 0.95,
			wantModel: map[string]want{
				classifier.RegistryIDBirdNETV24: {count: 3, min: 0.6, max: 0.9, sum: 2.2, firstSec: 3, lastSec: 9},
				mergeTestModelB:                 {count: 1, min: 0.95, max: 0.95, sum: 0.95, firstSec: 7, lastSec: 7},
			},
		},
		{
			name:     "second model creates the entry",
			hits:     []hit{{mergeTestModelB, 0.8, 3}, {classifier.RegistryIDBirdNETV24, 0.6, 6}, {classifier.RegistryIDBirdNETV24, 0.9, 9}, {classifier.RegistryIDBirdNETV24, 0.7, 12}, {mergeTestModelB, 0.9, 15}},
			wantBest: classifier.RegistryIDBirdNETV24,
			wantConf: 0.9,
			wantModel: map[string]want{
				mergeTestModelB:                 {count: 2, min: 0.8, max: 0.9, sum: 1.7, firstSec: 3, lastSec: 15},
				classifier.RegistryIDBirdNETV24: {count: 3, min: 0.6, max: 0.9, sum: 2.2, firstSec: 6, lastSec: 12},
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			p := mergeTestProcessor(t)
			before := time.Now()
			for _, h := range tt.hits {
				item := classifier.Results{
					Source:    datastore.AudioSource{ID: mergeTestSource},
					ModelID:   h.model,
					StartTime: at(h.endSec).Add(-mergeTestClip),
				}
				det := detectionFor(mergeTestScientific, mergeTestCommon)
				det.Result.Confidence = h.confidence
				p.mergePendingHit(&item, det, at(h.endSec), mergeTestWindow)
			}
			after := time.Now()

			pd := onlyPending(t, p)
			assert.Equal(t, len(tt.hits), pd.Count)
			sum := 0
			for _, c := range pd.ModelContributions {
				sum += c.HitCount
			}
			assert.Equal(t, pd.Count, sum, "model hit counts must add up to the pending count")

			assert.Equal(t, tt.wantBest, pd.BestModelID)
			assert.InDelta(t, tt.wantConf, pd.Confidence, mergeTestEpsilon)
			assert.Equal(t, at(tt.hits[0].endSec).Add(-mergeTestClip), pd.FirstDetected)
			assert.False(t, pd.FlushDeadline.Before(before.Add(mergeTestWindow)))
			assert.False(t, pd.FlushDeadline.After(after.Add(mergeTestWindow)))

			require.Len(t, pd.ModelContributions, len(tt.wantModel))
			for model, w := range tt.wantModel {
				got := pd.ModelContributions[model]
				assert.Equal(t, w.count, got.HitCount, model)
				assert.InDelta(t, w.min, got.MinConfidence, mergeTestEpsilon, model)
				assert.InDelta(t, w.max, got.MaxConfidence, mergeTestEpsilon, model)
				assert.InDelta(t, w.sum, got.ConfidenceSum, mergeTestEpsilon, model)
				assert.Equal(t, at(w.firstSec), got.FirstHitAt, model)
				assert.Equal(t, at(w.lastSec), got.LastHitAt, model)
			}
		})
	}
}

// TestMergePendingHit_ExtendedCapture pins that a merged hit marks the pending
// entry and sets its absolute deadline when extended capture applies to the
// species, and leaves the entry alone when it does not.
func TestMergePendingHit_ExtendedCapture(t *testing.T) {
	const maxDurationSeconds = 300
	base := time.Date(2026, 10, 24, 12, 0, 0, 0, time.UTC)

	tests := []struct {
		name    string
		enabled bool
	}{
		{name: "enabled marks the entry", enabled: true},
		{name: "disabled leaves the entry alone", enabled: false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			p := mergeTestProcessor(t)
			p.Settings.Realtime.ExtendedCapture = conf.ExtendedCaptureSettings{Enabled: tt.enabled, MaxDuration: maxDurationSeconds}
			p.extendedCaptureAll = true

			item := classifier.Results{
				Source:    datastore.AudioSource{ID: mergeTestSource},
				ModelID:   classifier.RegistryIDBirdNETV24,
				StartTime: base,
			}
			det := detectionFor(mergeTestScientific, mergeTestCommon)
			det.Result.Confidence = 0.8
			p.mergePendingHit(&item, det, base.Add(mergeTestClip), mergeTestWindow)

			pd := onlyPending(t, p)
			assert.Equal(t, tt.enabled, pd.ExtendedCapture)
			assert.Equal(t, !tt.enabled, pd.MaxDeadline.IsZero(), "the absolute deadline is set only when extended capture applies")
		})
	}
}

// TestProcessDetections_StampsModelHitsWithAudioEnd pins that processDetections
// stamps each hit with the window's audio end, or with the processing time when
// the producer gave none.
func TestProcessDetections_StampsModelHitsWithAudioEnd(t *testing.T) {
	base := time.Date(2026, 10, 24, 12, 0, 0, 0, time.UTC)

	const chunkConfidence = 0.5
	chunk := func(end time.Time) classifier.Results {
		return classifier.Results{
			Source:    datastore.AudioSource{ID: mergeTestSource},
			ModelID:   classifier.RegistryIDBirdNETV24,
			StartTime: end.Add(-mergeTestClip),
			Audio:     classifier.AudioTiming{EndAt: end},
			Results:   []datastore.Results{{Species: mergeTestLabel, Confidence: chunkConfidence}},
		}
	}
	processorWithOrchestrator := func(t *testing.T) *Processor {
		t.Helper()
		p := mergeTestProcessor(t)
		p.Bn = &classifier.Orchestrator{}
		return p
	}

	t.Run("known audio end", func(t *testing.T) {
		p := processorWithOrchestrator(t)
		ends := []time.Time{base.Add(3 * time.Second), base.Add(6 * time.Second), base.Add(9 * time.Second)}
		for _, end := range ends {
			p.processDetections(chunk(end))
		}

		pd := onlyPending(t, p)
		got := pd.ModelContributions[classifier.RegistryIDBirdNETV24]
		assert.Equal(t, len(ends), pd.Count)
		assert.Equal(t, len(ends), got.HitCount)
		assert.Equal(t, ends[0], got.FirstHitAt)
		assert.Equal(t, ends[len(ends)-1], got.LastHitAt)
	})

	t.Run("unknown audio end", func(t *testing.T) {
		p := processorWithOrchestrator(t)

		before1 := time.Now()
		p.processDetections(chunk(time.Time{}))
		after1 := time.Now()
		before2 := time.Now()
		p.processDetections(chunk(time.Time{}))
		after2 := time.Now()

		got := onlyPending(t, p).ModelContributions[classifier.RegistryIDBirdNETV24]
		assert.Equal(t, 2, got.HitCount)
		assert.False(t, got.FirstHitAt.Before(before1) || got.FirstHitAt.After(after1), "first hit stamped at processing time")
		assert.False(t, got.LastHitAt.Before(before2) || got.LastHitAt.After(after2), "last hit stamped at processing time")
	})
}
