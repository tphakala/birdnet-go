package processor

import (
	"log/slog"
	"regexp"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/tphakala/birdnet-go/internal/classifier"
	"github.com/tphakala/birdnet-go/internal/classifier/cadence"
	"github.com/tphakala/birdnet-go/internal/conf"
	"github.com/tphakala/birdnet-go/internal/datastore"
	"github.com/tphakala/birdnet-go/internal/logger/logtest"
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

// held reports whether source has a label hit waiting for a second hit.
func held(p *Processor, source string) (humanHit, bool) {
	h, ok := p.heldLabelHits[source]
	return h, ok
}

// lastHit returns the latest counted hit of confirmSource.
func lastHit(p *Processor) (HumanDetection, bool) {
	h, ok := p.LastHumanDetection[confirmSource]
	return h, ok
}

func TestHandleHumanDetection_PairRule(t *testing.T) {
	t.Parallel()
	base := time.Unix(1000, 0)
	type hit struct {
		source string
		after  time.Duration
	}
	one := func(after time.Duration) []hit { return []hit{{confirmSource, after}} }
	pair := func(gap time.Duration) []hit { return []hit{{confirmSource, 0}, {confirmSource, gap}} }
	tests := []struct {
		name      string
		overlap   float64
		hits      []hit
		wantCount bool          // some hit counted
		wantTime  time.Duration // stored time when counted
	}{
		{"0 s counts a single hit", 0, one(0), true, 0},
		{"1 s counts a single hit", 1.0, one(0), true, 0},
		{"1.5 s boundary counts a single hit", 1.5, one(0), true, 0},
		{"2.4 s ignores a single hit", 2.4, one(0), false, 0},
		{"two hits within the window count", 2.4, pair(600 * time.Millisecond), true, 600 * time.Millisecond},
		{"window edge counts", 2.4, pair(labelConfirmWindow), true, labelConfirmWindow},
		{"more than the window apart does not confirm", 2.4, pair(labelConfirmWindow + time.Millisecond), false, 0},
		{"same instant confirms", 2.4, pair(0), true, 0},
		{"another source does not confirm", 2.4, []hit{{confirmSource, 0}, {"other", time.Second}}, false, 0},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			p, settings := confirmProcessor(tt.overlap)
			for _, h := range tt.hits {
				labelHit(p, settings, h.source, testVADModel, base.Add(h.after))
			}
			got, ok := lastHit(p)
			require.Equal(t, tt.wantCount, ok)
			if tt.wantCount {
				assert.True(t, base.Add(tt.wantTime).Equal(got.Time), "got %v", got.Time.Sub(base))
				assert.Equal(t, metrics.TriggerLabel, got.Trigger)
			}
		})
	}
}

func TestRecordLabelHit_RecordsBothHitsWithOwnStamps(t *testing.T) {
	t.Parallel()
	p, settings := confirmProcessor(2.4)
	t0 := time.Unix(1000, 0)

	first := p.recordLabelHit(settings, confirmSource, labelHumanHit(t0, "", 0.4, 0))
	assert.True(t, first.held)
	assert.Empty(t, p.LastHumanDetection)

	second := p.recordLabelHit(settings, confirmSource, labelHumanHit(t0.Add(5*time.Second), "", 0.3, 0))
	assert.False(t, second.held)
	require.NotNil(t, second.confirmed)
	assert.True(t, t0.Equal(second.confirmed.at), "the held hit keeps its own stamp")
	assert.True(t, t0.Add(5*time.Second).Equal(p.LastHumanDetection[confirmSource].Time))
	_, stillHeld := held(p, confirmSource)
	assert.False(t, stillHeld)

	// Once confirmed, a further hit inside the window counts at once.
	third := p.recordLabelHit(settings, confirmSource, labelHumanHit(t0.Add(10*time.Second), "", 0.3, 0))
	assert.False(t, third.held)
	assert.Nil(t, third.confirmed)
	assert.True(t, t0.Add(10*time.Second).Equal(p.LastHumanDetection[confirmSource].Time))
}

func TestRecordLabelHit_ExpiredHeldHitIsDroppedAndNeverCounts(t *testing.T) {
	t.Parallel()
	p, settings := confirmProcessor(2.4)
	t0 := time.Unix(1000, 0)
	lateAt := t0.Add(labelConfirmWindow + time.Second)

	p.recordLabelHit(settings, confirmSource, labelHumanHit(t0, "", 0.4, 0))
	late := p.recordLabelHit(settings, confirmSource, labelHumanHit(lateAt, "", 0.3, 0))
	assert.True(t, late.held, "the late hit is held itself")
	require.NotNil(t, late.dropped)
	assert.True(t, t0.Equal(late.dropped.at))
	assert.Empty(t, p.LastHumanDetection)
	h, ok := held(p, confirmSource)
	require.True(t, ok)
	assert.True(t, lateAt.Equal(h.at), "only the latest hit is kept")
}

func TestRecordVADHit(t *testing.T) {
	t.Parallel()
	t0 := time.Unix(1000, 0)
	tests := []struct {
		name         string
		overlap      float64
		holdLabel    bool          // a label hit stamped t0 is held first
		vadAfter     time.Duration // VAD hit stamp relative to t0
		wantConfirm  bool
		wantDropped  bool
		wantStoredAt time.Duration
	}{
		{"alone counts at 0 s", 0, false, 0, false, false, 0},
		{"alone counts at the boundary", 1.5, false, 0, false, false, 0},
		{"alone counts above the boundary", 2.4, false, 0, false, false, 0},
		{"inside the window confirms the label hit", 2.4, true, 3 * time.Second, true, false, 3 * time.Second},
		{"earlier than the label hit confirms it", 2.4, true, -time.Second, true, false, 0},
		{"outside the window drops the held hit", 2.4, true, labelConfirmWindow + time.Second, false, true, labelConfirmWindow + time.Second},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			p, settings := confirmProcessor(tt.overlap)
			if tt.holdLabel {
				require.True(t, p.recordLabelHit(settings, confirmSource, labelHumanHit(t0, "", 0.4, 0)).held)
			}
			d := p.recordVADHit(settings, confirmSource, vadHumanHit(t0.Add(tt.vadAfter), 0, 0))
			assert.Equal(t, tt.wantConfirm, d.confirmed != nil)
			assert.Equal(t, tt.wantDropped, d.dropped != nil)
			got, ok := lastHit(p)
			require.True(t, ok)
			assert.True(t, t0.Add(tt.wantStoredAt).Equal(got.Time))
			if !tt.wantConfirm {
				assert.Equal(t, metrics.TriggerVAD, got.Trigger)
			}
			_, stillHeld := held(p, confirmSource)
			assert.False(t, stillHeld)
		})
	}
}

func TestRecordLabelHit_ReadsOverlapPerCall(t *testing.T) {
	t.Parallel()
	p, settings := confirmProcessor(2.4)
	t0 := time.Unix(1000, 0)

	p.recordLabelHit(settings, confirmSource, labelHumanHit(t0, "", 0.4, 0))
	require.Empty(t, p.LastHumanDetection)

	// Hot reload down to the boundary: a single hit counts again at once.
	settings.BirdNET.Overlap = 1.5
	d := p.recordLabelHit(settings, confirmSource, labelHumanHit(t0.Add(time.Hour), "", 0.4, 0))
	assert.False(t, d.held)
	assert.True(t, t0.Add(time.Hour).Equal(p.LastHumanDetection[confirmSource].Time))

	// And back up: a lone hit far from the counted one is held again.
	settings.BirdNET.Overlap = 2.4
	d = p.recordLabelHit(settings, confirmSource, labelHumanHit(t0.Add(2*time.Hour), "", 0.4, 0))
	assert.True(t, d.held)
	assert.True(t, t0.Add(time.Hour).Equal(p.LastHumanDetection[confirmSource].Time))

	// A hit counted while confirmation was off still confirms a close hit after
	// the reload up, since both are speech evidence inside the window.
	d = p.recordLabelHit(settings, confirmSource, labelHumanHit(t0.Add(time.Hour+5*time.Second), "", 0.4, 0))
	assert.False(t, d.held)
	assert.True(t, t0.Add(time.Hour+5*time.Second).Equal(p.LastHumanDetection[confirmSource].Time))
}

func TestRunVADGate_ConfirmsHeldLabelHit(t *testing.T) {
	t.Parallel()
	p, settings := confirmProcessor(2.4)
	t0 := time.Unix(1000, 0)
	vadEnd := t0.Add(3 * time.Second)

	labelHit(p, settings, confirmSource, testVADModel, t0)
	_, isHeld := held(p, confirmSource)
	require.True(t, isHeld, "the lone label hit is held")
	require.Empty(t, p.LastHumanDetection)

	_, ran := runGate(p, settings, makeVADItem(testVADModel, confirmSource, vadEnd, testVADChunkBytes))
	require.True(t, ran)

	_, isHeld = held(p, confirmSource)
	assert.False(t, isHeld, "the VAD hit through the gate confirms the held label hit")
	got, _ := lastHit(p)
	assert.True(t, vadEnd.Equal(got.Time))
	assert.Equal(t, metrics.TriggerVAD, got.Trigger)

	// A label hit after the VAD hit counts at once.
	labelAt := vadEnd.Add(2 * time.Second)
	labelHit(p, settings, confirmSource, testVADModel, labelAt)
	got, _ = lastHit(p)
	assert.True(t, labelAt.Equal(got.Time))
	assert.Equal(t, metrics.TriggerLabel, got.Trigger)
}

// countedLogLine matches the operation of a hit that counts as speech exactly;
// privacy_filter_label_confirm must not match.
var countedLogLine = regexp.MustCompile(`operation=privacy_filter(\s|$)`)

// Not parallel: logtest.CaptureBufferAt replaces the global logger.
func TestReportHitDecision_OperationAndLevel(t *testing.T) {
	infoLog := logtest.CaptureBufferAt(t, slog.LevelInfo)
	p, settings := confirmProcessor(2.4)
	t0 := time.Unix(1000, 0)

	// hit 1 is held, hit 2 drops it (ignored) and is held, hit 3 confirms hit 2.
	labelHit(p, settings, confirmSource, testVADModel, t0)
	labelHit(p, settings, confirmSource, testVADModel, t0.Add(labelConfirmWindow+time.Second))
	labelHit(p, settings, confirmSource, testVADModel, t0.Add(labelConfirmWindow+2*time.Second))

	out := infoLog.String()
	assert.Len(t, countedLogLine.FindAllString(out, -1), 2, "only the confirmed pair logs as counted: %s", out)
	assert.Equal(t, 2, strings.Count(out, "human detection filtered"))
	assert.Equal(t, 1, strings.Count(out, "operation=privacy_filter_label_confirm"), "the ignored hit logs once at Info")
	assert.Contains(t, out, "ignored")
	assert.NotContains(t, out, "held for confirmation", "held hits log at Debug only")
}

// Not parallel: logtest.CaptureBufferAt replaces the global logger.
func TestReportHitDecision_DropWithConfirmationOffLogsItsReason(t *testing.T) {
	infoLog := logtest.CaptureBufferAt(t, slog.LevelInfo)
	p, settings := confirmProcessor(2.4)
	t0 := time.Unix(1000, 0)

	labelHit(p, settings, confirmSource, testVADModel, t0)
	settings.BirdNET.Overlap = 0
	labelHit(p, settings, confirmSource, testVADModel, t0.Add(time.Second))

	// The same drop through a VAD hit, which logs nothing else of its own.
	vadProc, vadSettings := confirmProcessor(2.4)
	labelHit(vadProc, vadSettings, confirmSource, testVADModel, t0)
	vadSettings.BirdNET.Overlap = 0
	vadHit := vadHumanHit(t0.Add(time.Second), 0, 0)
	d := vadProc.recordVADHit(vadSettings, confirmSource, vadHit)
	vadProc.reportHitDecision(vadSettings, confirmSource, &vadHit, &d)

	out := infoLog.String()
	assert.Equal(t, 2, strings.Count(out, "operation=privacy_filter_label_confirm"), "each dropped hit logs once under the confirm operation")
	assert.Equal(t, 2, strings.Count(out, "label confirmation no longer applies"), "a label hit and a VAD hit each log the reason")
	assert.NotContains(t, out, "no second hit", "no window is involved when confirmation is off")
	assert.NotContains(t, out, "window=", "the window field belongs to the expiry message only")
}

// Not parallel: logtest.CaptureBufferAt replaces the global logger.
func TestReportHitDecision_HeldHitLogsAtDebugWithoutCountedOperation(t *testing.T) {
	debugLog := logtest.CaptureBufferAt(t, slog.LevelDebug)
	p, settings := confirmProcessor(2.4)

	labelHit(p, settings, confirmSource, testVADModel, time.Unix(1000, 0))

	out := debugLog.String()
	assert.Contains(t, out, "held for confirmation")
	assert.Contains(t, out, "operation=privacy_filter_label_confirm")
	assert.Empty(t, countedLogLine.FindAllString(out, -1), "a held hit is not logged as a privacy_filter hit")
}

// Not parallel: logtest.CaptureBufferAt replaces the global logger.
func TestRunVADGate_VADHitDoesNotLogAsLabelHit(t *testing.T) {
	infoLog := logtest.CaptureBufferAt(t, slog.LevelInfo)
	p, settings := confirmProcessor(2.4)

	_, ran := runGate(p, settings, makeVADItem(testVADModel, confirmSource, time.Unix(1000, 0), testVADChunkBytes))
	require.True(t, ran)
	require.Contains(t, p.LastHumanDetection, confirmSource)

	out := infoLog.String()
	assert.Empty(t, countedLogLine.FindAllString(out, -1), "the VAD hit logs its own line, not a counted label hit: %s", out)
	assert.NotContains(t, out, "human detection filtered")
}

// attachCadencePlan gives p a backend whose published plan caps the configured
// overlap to effective, in seconds, so effectiveBaseOverlap no longer follows
// settings.BirdNET.Overlap.
func attachCadencePlan(t *testing.T, p *Processor, configured, effective float64) {
	t.Helper()
	require.NotEqual(t, configured, effective, "the plan must differ from the configured overlap")
	orch := &classifier.Orchestrator{}
	orch.SetCadencePlan(&cadence.Plan{
		ConfiguredBaseOverlap: time.Duration(configured * float64(time.Second)),
		EffectiveBaseOverlap:  time.Duration(effective * float64(time.Second)),
		Status:                cadence.StatusCapped,
	})
	p.Bn = orch
}

func TestRecordLabelHit_FollowsEffectiveOverlap(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name       string
		configured float64
		effective  float64
		wantHeld   bool
	}{
		{"capped below the boundary counts a single hit", 2.4, 1.0, false},
		{"capped to exactly the boundary counts a single hit", 2.4, 1.5, false},
		{"effective above the boundary holds a single hit", 1.0, 2.4, true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			p, settings := confirmProcessor(tt.configured)
			attachCadencePlan(t, p, tt.configured, tt.effective)

			d := p.recordLabelHit(settings, confirmSource, labelHumanHit(time.Unix(1000, 0), "", 0.4, 0))

			assert.Equal(t, tt.wantHeld, d.held)
			_, counted := lastHit(p)
			assert.Equal(t, !tt.wantHeld, counted)
		})
	}
}

func TestRecordVADHit_FollowsEffectiveOverlap(t *testing.T) {
	t.Parallel()
	p, settings := confirmProcessor(2.4)
	attachCadencePlan(t, p, 2.4, 2.4+0.5)
	t0 := time.Unix(1000, 0)

	require.True(t, p.recordLabelHit(settings, confirmSource, labelHumanHit(t0, "", 0.4, 0)).held)

	// The plan is republished with the overlap capped below the boundary while the
	// configured value stays above it: the VAD hit counts alone and drops the held
	// label hit, even inside the confirmation window, because confirmation is off.
	attachCadencePlan(t, p, 2.4, 1.0)
	d := p.recordVADHit(settings, confirmSource, vadHumanHit(t0.Add(time.Second), 0, 0))

	assert.Nil(t, d.confirmed)
	require.NotNil(t, d.dropped)
	assert.True(t, t0.Equal(d.dropped.at))
	assert.True(t, d.confirmationOff)
	_, stillHeld := held(p, confirmSource)
	assert.False(t, stillHeld)
	got, ok := lastHit(p)
	require.True(t, ok)
	assert.Equal(t, metrics.TriggerVAD, got.Trigger)
}

func TestHandleHumanDetection_OtherSourceLeavesHeldHitAlone(t *testing.T) {
	t.Parallel()
	p, settings := confirmProcessor(2.4)
	t0 := time.Unix(1000, 0)

	labelHit(p, settings, confirmSource, testVADModel, t0)
	labelHit(p, settings, "other", testVADModel, t0.Add(time.Second))

	assert.Empty(t, p.LastHumanDetection, "a hit from another source confirms nothing")
	first, ok := held(p, confirmSource)
	require.True(t, ok, "the first source keeps its held hit")
	assert.True(t, t0.Equal(first.at))
	second, ok := held(p, "other")
	require.True(t, ok, "the other source holds its own hit")
	assert.True(t, t0.Add(time.Second).Equal(second.at))
}
