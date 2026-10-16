package processor

import (
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/tphakala/birdnet-go/internal/classifier"
	"github.com/tphakala/birdnet-go/internal/conf"
	"github.com/tphakala/birdnet-go/internal/datastore"
	"github.com/tphakala/birdnet-go/internal/observability/metrics"
)

// confirmSource is the audio source of the label confirmation tests.
const confirmSource = "src1"

// confirmProcessor builds a Processor with the privacy filter on and the given
// birdnet.overlap in seconds.
func confirmProcessor(overlap float64) (*Processor, *conf.Settings) {
	p := &Processor{
		LastHumanDetection: map[string]HumanDetection{},
		vadGate:            newTestGate(&fakeStreamer{prob: 0.9, flushOK: true}, &fakeSession{}, nil),
	}
	settings := vadSettings(true, 0.35)
	settings.Realtime.PrivacyFilter.Confidence = 0.05
	settings.BirdNET.Overlap = overlap
	return p, settings
}

// labelHit feeds one human label result stamped with the audio end `end`.
func labelHit(p *Processor, settings *conf.Settings, source, model string, end time.Time) {
	item := classifier.Results{Audio: classifier.AudioTiming{EndAt: end}, ModelID: model}
	item.Source.ID = source
	p.handleHumanDetection(settings, item, datastore.Results{Species: "Speech", Confidence: 0.9})
}

func TestLabelConfirmationActive_OnlyAboveHalfClipOverlap(t *testing.T) {
	t.Parallel()
	tests := []struct {
		overlap time.Duration
		want    bool
	}{
		{0, false},
		{time.Second, false},
		{1500 * time.Millisecond, false},
		{1500*time.Millisecond + time.Nanosecond, true},
		{2400 * time.Millisecond, true},
	}
	for _, tt := range tests {
		assert.Equal(t, tt.want, labelConfirmationActive(tt.overlap), "overlap %v", tt.overlap)
	}
}

func TestHandleHumanDetection_SingleHitCountsAtOrBelowBoundary(t *testing.T) {
	t.Parallel()
	for _, overlap := range []float64{0, 1.0, 1.5} {
		p, settings := confirmProcessor(overlap)
		at := time.Unix(1000, 0)
		labelHit(p, settings, confirmSource, testVADModel, at)
		got, ok := p.LastHumanDetection[confirmSource]
		require.True(t, ok, "overlap %v", overlap)
		assert.True(t, at.Equal(got.Time))
		assert.Equal(t, metrics.TriggerLabel, got.Trigger)
	}
}

func TestHandleHumanDetection_PairRule(t *testing.T) {
	t.Parallel()
	base := time.Unix(1000, 0)
	type hit struct {
		source string
		model  string
		after  time.Duration
	}
	tests := []struct {
		name      string
		overlap   float64
		hits      []hit
		wantCount bool          // some hit counted
		wantTime  time.Duration // stored time when counted
	}{
		{"single hit is ignored", 2.4, []hit{{confirmSource, "BirdNET_V2.4", 0}}, false, 0},
		{"two hits within the window count", 2.4, []hit{{confirmSource, "BirdNET_V2.4", 0}, {confirmSource, "BirdNET_V2.4", 600 * time.Millisecond}}, true, 600 * time.Millisecond},
		{"window edge counts", 2.4, []hit{{confirmSource, "BirdNET_V2.4", 0}, {confirmSource, "BirdNET_V2.4", labelConfirmWindow}}, true, labelConfirmWindow},
		{"more than the window apart does not confirm", 2.4, []hit{{confirmSource, "BirdNET_V2.4", 0}, {confirmSource, "BirdNET_V2.4", labelConfirmWindow + time.Millisecond}}, false, 0},
		{"same instant, same chunk, confirms", 2.4, []hit{{confirmSource, "Perch_V2", 0}, {confirmSource, "Perch_V2", 0}}, true, 0},
		{"different models confirm", 2.4, []hit{{confirmSource, "BirdNET_V2.4", 0}, {confirmSource, "Perch_V2", time.Second}}, true, time.Second},
		{"5 s model follows the same rule", 2.4, []hit{{confirmSource, "Perch_V2", 0}, {confirmSource, "Perch_V2", 2 * time.Second}}, true, 2 * time.Second},
		{"another source does not confirm", 2.4, []hit{{confirmSource, "BirdNET_V2.4", 0}, {"other", "BirdNET_V2.4", time.Second}}, false, 0},
		{"1.5 s counts a single hit", 1.5, []hit{{confirmSource, "BirdNET_V2.4", 0}}, true, 0},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			p, settings := confirmProcessor(tt.overlap)
			for _, h := range tt.hits {
				labelHit(p, settings, h.source, h.model, base.Add(h.after))
			}
			got, ok := p.LastHumanDetection[confirmSource]
			require.Equal(t, tt.wantCount, ok)
			if tt.wantCount {
				assert.True(t, base.Add(tt.wantTime).Equal(got.Time), "got %v", got.Time.Sub(base))
			}
		})
	}
}

func TestRecordLabelHit_RecordsBothHitsWithOwnStamps(t *testing.T) {
	t.Parallel()
	p, settings := confirmProcessor(2.4)
	t0 := time.Unix(1000, 0)

	first := p.recordLabelHit(settings, confirmSource, t0, 0.4)
	assert.True(t, first.held)
	assert.False(t, first.counts)
	assert.Empty(t, p.LastHumanDetection)

	second := p.recordLabelHit(settings, confirmSource, t0.Add(5*time.Second), 0.3)
	assert.True(t, second.counts)
	require.NotNil(t, second.confirmed)
	assert.True(t, t0.Equal(second.confirmed.at), "the held hit keeps its own stamp")
	assert.True(t, t0.Add(5*time.Second).Equal(p.LastHumanDetection[confirmSource].Time))

	// Once confirmed, a further hit inside the window counts at once.
	third := p.recordLabelHit(settings, confirmSource, t0.Add(10*time.Second), 0.3)
	assert.True(t, third.counts)
	assert.Nil(t, third.confirmed)
	assert.True(t, t0.Add(10*time.Second).Equal(p.LastHumanDetection[confirmSource].Time))
}

func TestRecordLabelHit_ExpiredHeldHitIsDroppedAndNeverCounts(t *testing.T) {
	t.Parallel()
	p, settings := confirmProcessor(2.4)
	t0 := time.Unix(1000, 0)

	p.recordLabelHit(settings, confirmSource, t0, 0.4)
	late := p.recordLabelHit(settings, confirmSource, t0.Add(labelConfirmWindow+time.Second), 0.3)
	assert.True(t, late.held, "the late hit is held itself")
	require.NotNil(t, late.dropped)
	assert.True(t, t0.Equal(late.dropped.at))
	assert.Empty(t, p.LastHumanDetection)
	assert.True(t, t0.Add(labelConfirmWindow+time.Second).Equal(p.labelConfirm[confirmSource].held.at), "only the latest hit is kept")
}

func TestRecordVADHit_ConfirmsHeldLabelHit(t *testing.T) {
	t.Parallel()
	t0 := time.Unix(1000, 0)
	tests := []struct {
		name         string
		overlap      float64
		vadAfter     time.Duration
		wantLabel    bool
		wantStoredAt time.Duration
	}{
		{"VAD inside the window confirms the label hit", 2.4, 3 * time.Second, true, 3 * time.Second},
		{"VAD earlier than the label hit confirms it", 2.4, -time.Second, true, 0},
		{"VAD outside the window drops the held hit", 2.4, labelConfirmWindow + time.Second, false, labelConfirmWindow + time.Second},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			p, settings := confirmProcessor(tt.overlap)
			held := p.recordLabelHit(settings, confirmSource, t0, 0.4)
			require.True(t, held.held)
			d := p.recordVADHit(settings, confirmSource, t0.Add(tt.vadAfter))
			assert.True(t, d.counts)
			assert.Equal(t, tt.wantLabel, d.confirmed != nil)
			assert.Equal(t, !tt.wantLabel, d.dropped != nil)
			assert.True(t, t0.Add(tt.wantStoredAt).Equal(p.LastHumanDetection[confirmSource].Time))
			assert.Nil(t, p.labelConfirm[confirmSource].held)
		})
	}
}

func TestRecordVADHit_AloneStillCounts(t *testing.T) {
	t.Parallel()
	for _, overlap := range []float64{0, 1.5, 2.4} {
		p, settings := confirmProcessor(overlap)
		at := time.Unix(1000, 0)
		d := p.recordVADHit(settings, confirmSource, at)
		assert.True(t, d.counts)
		got := p.LastHumanDetection[confirmSource]
		assert.True(t, at.Equal(got.Time))
		assert.Equal(t, metrics.TriggerVAD, got.Trigger)
	}
}

func TestRecordLabelHit_ReadsOverlapPerCall(t *testing.T) {
	t.Parallel()
	p, settings := confirmProcessor(2.4)
	t0 := time.Unix(1000, 0)

	p.recordLabelHit(settings, confirmSource, t0, 0.4)
	require.Empty(t, p.LastHumanDetection)

	// Hot reload down to the boundary: a single hit counts again at once.
	settings.BirdNET.Overlap = 1.5
	d := p.recordLabelHit(settings, confirmSource, t0.Add(time.Hour), 0.4)
	assert.True(t, d.counts)
	assert.True(t, t0.Add(time.Hour).Equal(p.LastHumanDetection[confirmSource].Time))

	// And back up: a lone hit far from the counted one is held again.
	settings.BirdNET.Overlap = 2.4
	d = p.recordLabelHit(settings, confirmSource, t0.Add(2*time.Hour), 0.4)
	assert.True(t, d.held)
	assert.True(t, t0.Add(time.Hour).Equal(p.LastHumanDetection[confirmSource].Time))
}
