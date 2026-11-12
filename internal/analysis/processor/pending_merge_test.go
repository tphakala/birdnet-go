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
	"github.com/tphakala/birdnet-go/internal/detection"
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

// TestMergePendingHit_UnlikelyOnlyWhenEveryHitIsFlat pins that a pending detection
// keeps the unlikely tag only while every hit window merged into it was judged
// flat, independent of arrival order, confidence ties and which window won.
func TestMergePendingHit_UnlikelyOnlyWhenEveryHitIsFlat(t *testing.T) {
	const threshold = 0.15
	type hit struct {
		confidence float64
		unlikely   bool
		cv         float64
		threshold  float64
	}
	flat := func(confidence, cv float64) hit { return hit{confidence, true, cv, threshold} }
	flatAt := func(confidence, cv, judgedAt float64) hit { return hit{confidence, true, cv, judgedAt} }
	active := func(confidence float64) hit { return hit{confidence, false, 0, 0} }

	tests := []struct {
		name         string
		hits         []hit
		wantUnlikely bool
		wantCV       float64
		wantThresh   float64
	}{
		{"flat high then active low", []hit{flat(0.95, 0.03), active(0.80)}, false, 0, 0},
		{"active low then flat high", []hit{active(0.80), flat(0.95, 0.03)}, false, 0, 0},
		{"flat then active tie", []hit{flat(0.90, 0.03), active(0.90)}, false, 0, 0},
		{"active then flat tie", []hit{active(0.90), flat(0.90, 0.03)}, false, 0, 0},
		{"flat CV rises on a lower confidence hit", []hit{flat(0.95, 0.03), flat(0.80, 0.08)}, true, 0.08, 0.15},
		{"highest CV kept when a higher confidence hit has a lower CV", []hit{flat(0.80, 0.08), flat(0.95, 0.03)}, true, 0.08, 0.15},
		{"single flat hit", []hit{flat(0.90, 0.04)}, true, 0.04, 0.15},
		{"tag does not come back after an active hit", []hit{flat(0.70, 0.03), flat(0.80, 0.04), active(0.85), flat(0.95, 0.05)}, false, 0, 0},
		{"max CV keeps the threshold that judged it, lower confidence hit later", []hit{flatAt(0.95, 0.12, 0.15), flatAt(0.80, 0.05, 0.10)}, true, 0.12, 0.15},
		{"max CV keeps the threshold that judged it, higher confidence hit later", []hit{flatAt(0.80, 0.12, 0.15), flatAt(0.95, 0.05, 0.10)}, true, 0.12, 0.15},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			p := mergeTestProcessor(t)
			base := time.Date(2026, 11, 10, 12, 0, 0, 0, time.UTC)
			for i, h := range tt.hits {
				endAt := base.Add(time.Duration(i+1) * mergeTestClip)
				item := classifier.Results{
					Source:    datastore.AudioSource{ID: mergeTestSource},
					ModelID:   classifier.RegistryIDBirdNETV24,
					StartTime: endAt.Add(-mergeTestClip),
				}
				det := detectionFor(mergeTestScientific, mergeTestCommon)
				det.Result.Confidence = h.confidence
				det.Result.Unlikely = h.unlikely
				if h.unlikely {
					det.Result.UnlikelySources = detection.UnlikelySourceUltrasonicCV
				}
				det.Result.UltrasonicCV = h.cv
				det.Result.UltrasonicCVThreshold = h.threshold
				p.mergePendingHit(&item, det, endAt, mergeTestWindow)
			}

			got := onlyPending(t, p).Detection.Result
			assert.Equal(t, tt.wantUnlikely, got.Unlikely)
			assert.InDelta(t, tt.wantCV, got.UltrasonicCV, mergeTestEpsilon)
			assert.InDelta(t, tt.wantThresh, got.UltrasonicCVThreshold, mergeTestEpsilon)
			wantSources := detection.UnlikelySource(0)
			if tt.wantUnlikely {
				wantSources = detection.UnlikelySourceUltrasonicCV
			}
			assert.Equal(t, wantSources, got.UnlikelySources, "the source bits survive only while the detection is unlikely")
		})
	}
}

// TestMergeUnlikelyVerdict_Sources pins the source composition of the merge in both
// arrival orders: the detection stays unlikely only while both sides are unlikely,
// the bits of both sides are kept, and the CV and its threshold come only from a
// side that carries the CV bit. otherSource stands for a validator other than the
// CV filter, whose windows carry no CV.
func TestMergeUnlikelyVerdict_Sources(t *testing.T) {
	const (
		cvSource    = detection.UnlikelySourceUltrasonicCV
		otherSource = detection.UnlikelySource(1 << 1)
		threshold   = 0.15
	)
	cvWindow := func(cv, judgedAt float64) unlikelyVerdict {
		return unlikelyVerdict{unlikely: true, sources: cvSource, cv: cv, cvThreshold: judgedAt}
	}
	otherWindow := unlikelyVerdict{unlikely: true, sources: otherSource}
	active := unlikelyVerdict{}

	tests := []struct {
		name string
		a, b unlikelyVerdict
		want unlikelyVerdict
	}{
		{"cv with cv keeps the higher cv and its threshold", cvWindow(0.03, 0.10), cvWindow(0.08, threshold), cvWindow(0.08, threshold)},
		{"cv with other keeps both bits and the cv side values", cvWindow(0.04, threshold), otherWindow, unlikelyVerdict{true, cvSource | otherSource, 0.04, threshold}},
		{"other with other has no cv", otherWindow, otherWindow, otherWindow},
		{"an untagged window clears the bits and the cv", active, cvWindow(0.04, threshold), active},
	}
	for _, tt := range tests {
		for _, order := range []struct {
			name      string
			prev, hit unlikelyVerdict
		}{{"a then b", tt.a, tt.b}, {"b then a", tt.b, tt.a}} {
			t.Run(tt.name+"/"+order.name, func(t *testing.T) {
				// Seeded with stale verdict values: every row must overwrite or clear them.
				merged := detection.Result{
					Confidence:            0.9,
					UnlikelySources:       cvSource | otherSource,
					UltrasonicCV:          0.5,
					UltrasonicCVThreshold: 0.5,
				}
				mergeUnlikelyVerdict(&merged, order.prev, order.hit)
				assert.Equal(t, tt.want, verdictOf(&merged))
				assert.InDelta(t, 0.9, merged.Confidence, mergeTestEpsilon, "only the verdict fields change")
			})
		}
	}
}

// TestMergeUnlikelyVerdict_TieReportsLaterThreshold pins that two windows with the
// same CV report the threshold of the later one, so the order of arrival decides
// which threshold is shown but never the CV.
func TestMergeUnlikelyVerdict_TieReportsLaterThreshold(t *testing.T) {
	const cv = 0.08
	first := unlikelyVerdict{unlikely: true, sources: detection.UnlikelySourceUltrasonicCV, cv: cv, cvThreshold: 0.10}
	second := unlikelyVerdict{unlikely: true, sources: detection.UnlikelySourceUltrasonicCV, cv: cv, cvThreshold: 0.15}

	var merged detection.Result
	mergeUnlikelyVerdict(&merged, first, second)
	assert.InDelta(t, cv, merged.UltrasonicCV, mergeTestEpsilon)
	assert.InDelta(t, 0.15, merged.UltrasonicCVThreshold, mergeTestEpsilon, "second arrival is the later window")

	mergeUnlikelyVerdict(&merged, second, first)
	assert.InDelta(t, cv, merged.UltrasonicCV, mergeTestEpsilon)
	assert.InDelta(t, 0.10, merged.UltrasonicCVThreshold, mergeTestEpsilon, "first window arriving last is the later window")
}
