package processor

import (
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/tphakala/birdnet-go/internal/analysis/privacyrollup"
	"github.com/tphakala/birdnet-go/internal/audiocore"
	"github.com/tphakala/birdnet-go/internal/conf"
	"github.com/tphakala/birdnet-go/internal/observability/metrics"
)

const (
	rollupTestNode       = "rollup-node"
	rollupTestConnection = "hw:9,9"
	rollupTestDisplay    = "Rollup mic"
	rollupLabelModel     = "BirdNET_V2.4"
	// rollupLabelThreshold is confirmProcessor's label threshold (0.05) in thousandths.
	rollupLabelThreshold = 50
)

// recordingRollup is a privacyrollup.Recorder that keeps the events and checks,
// on every call, that neither processor mutex is held: the hit sites must hand
// off outside pendingMutex and detectionMutex.
type recordingRollup struct {
	t      *testing.T
	p      *Processor
	mu     sync.Mutex
	events []privacyrollup.Event
}

func newRecordingRollup(t *testing.T, p *Processor) *recordingRollup {
	t.Helper()
	r := &recordingRollup{t: t, p: p}
	p.SetPrivacyRollup(r)
	return r
}

//nolint:gocritic // hugeParam: must match privacyrollup.Recorder
func (r *recordingRollup) Record(ev privacyrollup.Event) {
	if r.p.pendingMutex.TryLock() {
		r.p.pendingMutex.Unlock()
	} else {
		r.t.Error("Record called with pendingMutex held")
	}
	if r.p.detectionMutex.TryLock() {
		r.p.detectionMutex.Unlock()
	} else {
		r.t.Error("Record called with detectionMutex held")
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	r.events = append(r.events, ev)
}

func (r *recordingRollup) take() []privacyrollup.Event {
	r.mu.Lock()
	defer r.mu.Unlock()
	out := r.events
	r.events = nil
	return out
}

func (r *recordingRollup) ofKind(kind privacyrollup.Kind) []privacyrollup.Event {
	r.mu.Lock()
	defer r.mu.Unlock()
	var out []privacyrollup.Event
	for i := range r.events {
		if r.events[i].Kind == kind {
			out = append(out, r.events[i])
		}
	}
	return out
}

func kindsOf(events []privacyrollup.Event) []privacyrollup.Kind {
	kinds := make([]privacyrollup.Kind, 0, len(events))
	for i := range events {
		kinds = append(kinds, events[i].Kind)
	}
	return kinds
}

// rollupRegistry registers one source under id and returns the registry and the
// safe string the events must carry.
func rollupRegistry(t *testing.T, id string) (reg *audiocore.SourceRegistry, safe string) {
	t.Helper()
	reg = audiocore.NewSourceRegistry(audiocore.GetLogger())
	src, err := reg.Register(&audiocore.SourceConfig{
		ID: id, ConnectionString: rollupTestConnection, DisplayName: rollupTestDisplay, Type: audiocore.SourceTypeAudioCard,
	})
	require.NoError(t, err)
	return reg, src.SafeString
}

// rollupProcessor is confirmProcessor with the rollup recorder, the source
// registry and a node name.
func rollupProcessor(t *testing.T, overlap float64) (p *Processor, settings *conf.Settings, rec *recordingRollup, safe string) {
	t.Helper()
	p, settings = confirmProcessor(overlap)
	settings.Main.Name = rollupTestNode
	rec = newRecordingRollup(t, p)
	var reg *audiocore.SourceRegistry
	reg, safe = rollupRegistry(t, confirmSource)
	p.SetRegistry(reg)
	return p, settings, rec, safe
}

func TestPrivacyRollup_LabelDecisionEvents(t *testing.T) {
	t.Parallel()
	t0 := time.Unix(1000, 0)
	window := labelConfirmWindow

	t.Run("a lone hit at overlap 0 counts with its own stamp, model, trigger and threshold", func(t *testing.T) {
		t.Parallel()
		p, settings, rec, safe := rollupProcessor(t, 0)
		labelHit(p, settings, confirmSource, rollupLabelModel, t0)
		events := rec.take()
		require.Len(t, events, 1)
		ev := events[0]
		assert.Equal(t, privacyrollup.KindCountedHit, ev.Kind)
		assert.True(t, t0.Equal(ev.At))
		assert.Equal(t, rollupLabelModel, ev.ModelID)
		assert.Equal(t, metrics.TriggerLabel, ev.Trigger)
		assert.Equal(t, rollupLabelThreshold, ev.ThresholdMilli)
		assert.InDelta(t, 0.9, ev.Score, 1e-6)
		assert.Equal(t, safe, ev.SourceURI)
		assert.Equal(t, rollupTestNode, ev.NodeName)
		assert.Equal(t, rollupTestDisplay, ev.DisplayName)
	})

	t.Run("a held hit is confirmed by a second hit and keyed on the held hit", func(t *testing.T) {
		t.Parallel()
		p, settings, rec, _ := rollupProcessor(t, 2.4)
		labelHit(p, settings, confirmSource, rollupLabelModel, t0)
		first := rec.take()
		require.Equal(t, []privacyrollup.Kind{privacyrollup.KindHeldHit}, kindsOf(first))
		assert.True(t, t0.Equal(first[0].At))

		labelHit(p, settings, confirmSource, "Perch_V2", t0.Add(2*time.Second))
		events := rec.take()
		require.Equal(t, []privacyrollup.Kind{privacyrollup.KindConfirmedHeld, privacyrollup.KindCountedHit}, kindsOf(events))
		assert.True(t, t0.Equal(events[0].At), "the confirmation is keyed on the held hit")
		assert.Equal(t, rollupLabelModel, events[0].ModelID)
		assert.Equal(t, "Perch_V2", events[1].ModelID)
		assert.True(t, t0.Add(2*time.Second).Equal(events[1].At))
	})

	t.Run("a held hit with no second hit in the window is ignored and keyed on itself", func(t *testing.T) {
		t.Parallel()
		p, settings, rec, _ := rollupProcessor(t, 2.4)
		labelHit(p, settings, confirmSource, rollupLabelModel, t0)
		rec.take()
		labelHit(p, settings, confirmSource, "Perch_V2", t0.Add(window+time.Second))
		events := rec.take()
		require.Equal(t, []privacyrollup.Kind{privacyrollup.KindIgnoredHeld, privacyrollup.KindHeldHit}, kindsOf(events))
		assert.True(t, t0.Equal(events[0].At))
		assert.Equal(t, rollupLabelModel, events[0].ModelID)
	})

	t.Run("a VAD hit confirms a held label hit", func(t *testing.T) {
		t.Parallel()
		p, settings, rec, safe := rollupProcessor(t, 2.4)
		labelHit(p, settings, confirmSource, rollupLabelModel, t0)
		rec.take()
		vadEnd := t0.Add(3 * time.Second)
		_, ran := runGate(p, settings, makeVADItem(testVADModel, confirmSource, vadEnd, testVADChunkBytes))
		require.True(t, ran)

		events := rec.take()
		require.ElementsMatch(t, []privacyrollup.Kind{privacyrollup.KindCountedHit, privacyrollup.KindConfirmedHeld}, kindsOf(events))
		for i := range events {
			ev := &events[i]
			assert.Equal(t, safe, ev.SourceURI)
			assert.Equal(t, rollupTestNode, ev.NodeName)
			if ev.Kind == privacyrollup.KindCountedHit {
				assert.Equal(t, metrics.TriggerVAD, ev.Trigger)
				assert.Equal(t, vadHitModelID, ev.ModelID)
				assert.Equal(t, privacyrollup.ThresholdMilli(clampVADThreshold(float64(settings.Realtime.PrivacyFilter.VAD.Threshold))), ev.ThresholdMilli)
				assert.True(t, vadEnd.Equal(ev.At))
			} else {
				assert.Equal(t, metrics.TriggerLabel, ev.Trigger)
				assert.Equal(t, rollupLabelThreshold, ev.ThresholdMilli)
				assert.True(t, t0.Equal(ev.At))
			}
		}
	})

	t.Run("a threshold reload between hits leaves the held hit's threshold alone", func(t *testing.T) {
		t.Parallel()
		p, settings, rec, _ := rollupProcessor(t, 2.4)
		labelHit(p, settings, confirmSource, rollupLabelModel, t0)
		rec.take()
		settings.Realtime.PrivacyFilter.Confidence = 0.1
		labelHit(p, settings, confirmSource, rollupLabelModel, t0.Add(time.Second))
		events := rec.take()
		require.Equal(t, []privacyrollup.Kind{privacyrollup.KindConfirmedHeld, privacyrollup.KindCountedHit}, kindsOf(events))
		assert.Equal(t, rollupLabelThreshold, events[0].ThresholdMilli, "the held hit keeps the threshold it was scored with")
		assert.Equal(t, 100, events[1].ThresholdMilli)
	})

	t.Run("an overlap reload drops the stranded held hit and counts the own hit in one decision", func(t *testing.T) {
		t.Parallel()
		p, settings, rec, _ := rollupProcessor(t, 2.4)
		labelHit(p, settings, confirmSource, rollupLabelModel, t0) // held
		settings.BirdNET.Overlap = 0
		labelHit(p, settings, confirmSource, rollupLabelModel, t0.Add(20*time.Second)) // counts at once
		rec.take()
		settings.BirdNET.Overlap = 2.4
		labelHit(p, settings, confirmSource, rollupLabelModel, t0.Add(25*time.Second))
		events := rec.take()
		require.Equal(t, []privacyrollup.Kind{privacyrollup.KindIgnoredHeld, privacyrollup.KindCountedHit}, kindsOf(events))
		assert.True(t, t0.Equal(events[0].At))
	})

	t.Run("without a registry the source URI is empty and the node falls back to default", func(t *testing.T) {
		t.Parallel()
		p, settings := confirmProcessor(0)
		rec := newRecordingRollup(t, p)
		labelHit(p, settings, confirmSource, rollupLabelModel, t0)
		events := rec.take()
		require.Len(t, events, 1)
		assert.Empty(t, events[0].SourceURI)
		assert.Equal(t, defaultRollupNodeName, events[0].NodeName)
	})
}

// rollupRig prepares a privacyRig with a rollup recorder and a source registry.
func rollupRig(t *testing.T, ago time.Duration) (*privacyRig, *recordingRollup) {
	t.Helper()
	r := newPrivacyRig(t, "BirdNET_V2.4", ago)
	r.settings.Main.Name = rollupTestNode
	reg, _ := rollupRegistry(t, e2eSource)
	r.p.SetRegistry(reg)
	return r, newRecordingRollup(t, r.p)
}

func TestPrivacyRollup_FlushEvents(t *testing.T) {
	t.Run("speech reaching the clip gives one discard keyed on the causing hit", func(t *testing.T) {
		r, rec := rollupRig(t, 45*time.Second)
		chunkStart := 40*time.Second - r.clip
		r.speech = []speechInterval{{chunkStart - sec(1.7), chunkStart - sec(1.2)}}
		r.feed(r.clip, 40*time.Second+sec(11), sec(3))
		r.pend(40 * time.Second)
		require.Equal(t, "discarded", r.outcome())

		discards := rec.ofKind(privacyrollup.KindDiscard)
		require.Len(t, discards, 1)
		hit := r.p.LastHumanDetection[e2eSource]
		assert.True(t, hit.Time.Equal(discards[0].At), "the discard is stamped with the causing hit, not the flush")
		assert.Equal(t, hit.ModelID, discards[0].ModelID)
		assert.Equal(t, hit.Trigger, discards[0].Trigger)
		assert.Equal(t, hit.ThresholdMilli, discards[0].ThresholdMilli)
		assert.Equal(t, metrics.TriggerVAD, discards[0].Trigger)
		assert.NotZero(t, discards[0].ThresholdMilli)
		assert.NotEmpty(t, discards[0].SourceURI)
		assert.Equal(t, rollupTestNode, discards[0].NodeName)
	})

	t.Run("two pending detections dropped by the same hit give two discards on one key", func(t *testing.T) {
		r, rec := rollupRig(t, 45*time.Second)
		chunkStart := 40*time.Second - r.clip
		r.speech = []speechInterval{{chunkStart - sec(1.7), chunkStart - sec(1.2)}}
		r.feed(r.clip, 40*time.Second+sec(11), sec(3))
		r.pend(40 * time.Second)
		first := r.p.pendingDetections[pendingDetectionKey(e2eSource, e2eSpecies)]
		r.p.pendingDetections[pendingDetectionKey(e2eSource, "blue tit")] = first
		_, flushed := r.p.flushPendingDetections()
		require.Zero(t, flushed)

		discards := rec.ofKind(privacyrollup.KindDiscard)
		require.Len(t, discards, 2)
		assert.Equal(t, discards[0], discards[1])
	})

	t.Run("a coverage timeout gives one unscanned save keyed on the required detector and the clip start", func(t *testing.T) {
		r, rec := rollupRig(t, 100*time.Second)
		r.feedWithGapAt(r.clip, sec(40), sec(55), sec(1))
		r.pend(40 * time.Second)
		require.Equal(t, "approved", r.outcome())

		saves := rec.ofKind(privacyrollup.KindUnscannedSave)
		require.Len(t, saves, 1)
		assert.True(t, r.clipBegin(40*time.Second).Equal(saves[0].At))
		assert.Equal(t, privacyrollup.NoModelID, saves[0].ModelID)
		assert.Equal(t, privacyrollup.NoThresholdMilli, saves[0].ThresholdMilli)
		assert.Equal(t, metrics.TriggerVAD, saves[0].Trigger, "the VAD is the required detector in this rig")
		assert.Empty(t, rec.ofKind(privacyrollup.KindDiscard))
	})

	t.Run("a coverage timeout with the VAD off is keyed on the label detector", func(t *testing.T) {
		r, rec := rollupRig(t, 100*time.Second)
		r.settings.Realtime.PrivacyFilter.VAD.Enabled = false
		// Label coverage stops short of the clip end, and the wait has long expired.
		r.feed(r.clip, sec(36), sec(1))
		r.pend(40 * time.Second)
		require.Equal(t, "approved", r.outcome())

		saves := rec.ofKind(privacyrollup.KindUnscannedSave)
		require.Len(t, saves, 1)
		assert.Equal(t, metrics.TriggerLabel, saves[0].Trigger)
	})

	t.Run("a minimum count discard is not a privacy discard", func(t *testing.T) {
		r, rec := rollupRig(t, 45*time.Second)
		r.feed(r.clip, 40*time.Second+sec(11), sec(3))
		r.pend(40 * time.Second)
		key := pendingDetectionKey(e2eSource, e2eSpecies)
		item := r.p.pendingDetections[key]
		item.Count = 0
		r.p.pendingDetections[key] = item
		require.Equal(t, "discarded", r.outcome())
		assert.Empty(t, rec.ofKind(privacyrollup.KindDiscard))
	})
}
