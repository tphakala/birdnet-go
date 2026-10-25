package processor

import (
	"log/slog"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/tphakala/birdnet-go/internal/conf"
	"github.com/tphakala/birdnet-go/internal/logger/logtest"
	"github.com/tphakala/birdnet-go/internal/observability/metrics"
)

const (
	attrModelPerch   = "Perch_V2"
	attrModelBirdNET = "BirdNET_V2.4"
)

func TestRecordHumanHit_LastHitCarriesModelAndScore(t *testing.T) {
	t.Parallel()
	p, settings := confirmProcessor(0)
	t0 := time.Unix(1000, 0)

	labelHit(p, settings, confirmSource, attrModelPerch, t0)

	got, ok := lastHit(p)
	require.True(t, ok)
	assert.Equal(t, attrModelPerch, got.ModelID)
	assert.InDelta(t, 0.9, got.Score, 1e-6)
	assert.Equal(t, metrics.TriggerLabel, got.Trigger)
}

func TestHitContributions_AggregatesAcrossModelsAndDetectors(t *testing.T) {
	t.Parallel()
	p, settings := confirmProcessor(0)
	t0 := time.Unix(1000, 0)

	labelHit(p, settings, confirmSource, attrModelPerch, t0)
	labelHit(p, settings, confirmSource, attrModelBirdNET, t0.Add(time.Second))
	labelHit(p, settings, confirmSource, attrModelPerch, t0.Add(2*time.Second))
	p.recordVADHit(settings, confirmSource, vadHumanHit(t0.Add(3*time.Second), 0.81, 0))

	p.detectionMutex.RLock()
	all := p.hitContributionsLocked(confirmSource, t0)
	late := p.hitContributionsLocked(confirmSource, t0.Add(2*time.Second))
	p.detectionMutex.RUnlock()

	require.Len(t, all, 3)
	assert.Equal(t, hitContribution{trigger: metrics.TriggerLabel, modelID: attrModelPerch, count: 2, maxScore: float64(float32(0.9))}, all[0], "most hits first")
	assert.Equal(t, 1, all[1].count)
	assert.Equal(t, 1, all[2].count)
	assert.Contains(t, formatHitContributions(all), "Perch_V2/label x2 max 0.900")
	assert.Contains(t, formatHitContributions(all), "silero_vad/vad x1 max 0.810")
	assert.Contains(t, formatHitContributions(all), "BirdNET_V2.4/label x1")

	require.Len(t, late, 2, "hits before since are left out")
	assert.NotContains(t, formatHitContributions(late), attrModelBirdNET)
	assert.Empty(t, formatHitContributions(nil))
	assert.Equal(t, "unknown/label x1 max 0.100",
		formatHitContributions([]hitContribution{{trigger: metrics.TriggerLabel, count: 1, maxScore: 0.1}}),
		"a hit recorded without a model is named unknown")
}

func TestAppendHumanHit_PrunesByAgeAtTheCutoff(t *testing.T) {
	t.Parallel()
	p := &Processor{}
	t0 := time.Unix(1000, 0)
	hit := func(at time.Time) humanHit { return humanHit{at: at, trigger: metrics.TriggerLabel} }

	p.appendHumanHitLocked(confirmSource, hit(t0))
	p.appendHumanHitLocked(confirmSource, hit(t0.Add(humanHitRetention)))
	require.Len(t, p.humanHits[confirmSource], 2, "a hit exactly at the cutoff is kept")

	p.appendHumanHitLocked(confirmSource, hit(t0.Add(humanHitRetention+time.Nanosecond)))
	got := p.humanHits[confirmSource]
	require.Len(t, got, 2, "a hit one tick before the cutoff is dropped")
	assert.True(t, t0.Add(humanHitRetention).Equal(got[0].at))
	assert.True(t, t0.Add(humanHitRetention+time.Nanosecond).Equal(got[1].at))
}

func TestAppendHumanHit_CapKeepsTheNewestHits(t *testing.T) {
	t.Parallel()
	p := &Processor{}
	t0 := time.Unix(1000, 0)
	total := 2 * humanHitMaxPerSource

	for i := range total {
		p.appendHumanHitLocked(confirmSource, humanHit{at: t0.Add(time.Duration(i) * time.Millisecond), trigger: metrics.TriggerLabel})
	}

	got := p.humanHits[confirmSource]
	require.Len(t, got, humanHitMaxPerSource)
	assert.True(t, t0.Add(time.Duration(total-humanHitMaxPerSource)*time.Millisecond).Equal(got[0].at), "the oldest entries are the ones dropped")
	assert.True(t, t0.Add(time.Duration(total-1)*time.Millisecond).Equal(got[len(got)-1].at), "the newest entry is kept")
}

func TestAppendHumanHit_RetentionCoversLongestExtendedCapture(t *testing.T) {
	t.Parallel()
	p := &Processor{}
	t0 := time.Unix(1000, 0)
	longestClip := time.Duration(conf.MaxExtendedCaptureDuration) * time.Second

	p.appendHumanHitLocked(confirmSource, humanHit{at: t0, trigger: metrics.TriggerLabel})
	p.appendHumanHitLocked(confirmSource, humanHit{at: t0.Add(longestClip), trigger: metrics.TriggerLabel})

	assert.Len(t, p.humanHits[confirmSource], 2, "a hit at the start of the longest extended capture clip is still listed")
}

func TestRecordHumanHit_OlderHitStaysInAttributionButDoesNotMoveLastHit(t *testing.T) {
	t.Parallel()
	p := &Processor{LastHumanDetection: map[string]HumanDetection{}}
	t0 := time.Unix(1000, 0)

	p.recordHumanHitLocked(confirmSource, labelHumanHit(t0.Add(time.Second), attrModelPerch, 0.2, 0))
	p.recordHumanHitLocked(confirmSource, labelHumanHit(t0, attrModelBirdNET, 0.3, 0))

	assert.Equal(t, attrModelPerch, p.LastHumanDetection[confirmSource].ModelID, "the stored latest hit never moves backward")
	assert.Len(t, p.humanHits[confirmSource], 2, "the earlier hit still counts as a contributor")
}

// Not parallel: logtest.CaptureBufferAt replaces the global logger.
func TestReportHitDecision_NamesModelAndConfirmer(t *testing.T) {
	infoLog := logtest.CaptureBufferAt(t, slog.LevelInfo)
	p, settings := confirmProcessor(2.4)
	t0 := time.Unix(1000, 0)

	labelHit(p, settings, confirmSource, attrModelPerch, t0)
	labelHit(p, settings, confirmSource, attrModelBirdNET, t0.Add(time.Second))

	out := infoLog.String()
	assert.Contains(t, out, "human detection filtered")
	assert.Contains(t, out, "model_id="+attrModelPerch, "the held hit is counted under its own model")
	assert.Contains(t, out, "confirmed_by="+attrModelBirdNET, "the confirming model is named")
}

// Not parallel: logtest.CaptureBufferAt replaces the global logger.
func TestReportHitDecision_VADConfirmerAndDroppedModel(t *testing.T) {
	infoLog := logtest.CaptureBufferAt(t, slog.LevelInfo)
	p, settings := confirmProcessor(2.4)
	t0 := time.Unix(1000, 0)

	labelHit(p, settings, confirmSource, attrModelPerch, t0)
	vad := vadHumanHit(t0.Add(time.Second), 0.8, 0)
	d := p.recordVADHit(settings, confirmSource, vad)
	p.reportHitDecision(settings, confirmSource, &vad, &d)
	assert.Contains(t, infoLog.String(), "confirmed_by="+vadHitModelID)
	assert.Equal(t, 1, strings.Count(infoLog.String(), "human detection filtered"), "only the confirmed label hit is logged; the VAD hit logs its own line elsewhere")

	// A held hit whose window runs out is logged with the model that scored it.
	labelHit(p, settings, confirmSource, attrModelBirdNET, t0.Add(10*labelConfirmWindow))
	labelHit(p, settings, confirmSource, attrModelPerch, t0.Add(20*labelConfirmWindow))
	assert.Contains(t, infoLog.String(), "ignored")
	assert.Contains(t, infoLog.String(), "model_id="+attrModelBirdNET)
}

// Not parallel: logtest.CaptureBufferAt replaces the global logger.
func TestShouldDiscardDetection_DebugSummaryNamesContributors(t *testing.T) {
	infoLog := logtest.CaptureBufferAt(t, slog.LevelInfo)
	p, settings := confirmProcessor(0)
	settings.Realtime.PrivacyFilter.Debug = true
	t0 := time.Unix(1000, 0)

	labelHit(p, settings, confirmSource, attrModelPerch, t0)
	labelHit(p, settings, confirmSource, attrModelBirdNET, t0.Add(time.Second))
	labelHit(p, settings, confirmSource, attrModelPerch, t0.Add(2*time.Second))
	item := newPrivacyFilterDetection(confirmSource, t0)

	discard, reason := discardNow(p, item, settings, 1)

	require.True(t, discard)
	assert.Equal(t, "privacy filter", reason)
	out := infoLog.String()
	assert.Contains(t, out, "privacy filter dropped detection")
	assert.Contains(t, out, "operation="+privacyFilterDiscardOperation)
	assert.Contains(t, out, "Perch_V2/label x2")
	assert.Contains(t, out, "BirdNET_V2.4/label x1")
	assert.Contains(t, out, "last_hit_model_id="+attrModelPerch)
}

// Not parallel: logtest.CaptureBufferAt replaces the global logger.
func TestShouldDiscardDetection_NoDebugSummaryWithoutDebugFlag(t *testing.T) {
	infoLog := logtest.CaptureBufferAt(t, slog.LevelInfo)
	p, settings := confirmProcessor(0)
	t0 := time.Unix(1000, 0)

	labelHit(p, settings, confirmSource, attrModelPerch, t0)
	discard, _ := discardNow(p, newPrivacyFilterDetection(confirmSource, t0), settings, 1)

	require.True(t, discard)
	assert.NotContains(t, infoLog.String(), "privacy filter dropped detection")
}

func TestRecordLabelHit_ConfirmedHitKeepsItsOwnModelAndScore(t *testing.T) {
	t.Parallel()
	p, settings := confirmProcessor(2.4)
	t0 := time.Unix(1000, 0)

	p.recordLabelHit(settings, confirmSource, labelHumanHit(t0, attrModelPerch, 0.5, 0))
	p.recordLabelHit(settings, confirmSource, labelHumanHit(t0.Add(5*time.Second), attrModelBirdNET, 0.25, 0))

	require.Len(t, p.humanHits[confirmSource], 2)
	held, confirming := p.humanHits[confirmSource][0], p.humanHits[confirmSource][1]
	assert.Equal(t, humanHit{at: t0, trigger: metrics.TriggerLabel, modelID: attrModelPerch, score: 0.5}, held, "the held hit is counted under its own model, stamp and score")
	assert.Equal(t, humanHit{at: t0.Add(5 * time.Second), trigger: metrics.TriggerLabel, modelID: attrModelBirdNET, score: 0.25}, confirming)
	last, ok := lastHit(p)
	require.True(t, ok)
	assert.Equal(t, attrModelBirdNET, last.ModelID)
	assert.InDelta(t, 0.25, last.Score, 1e-9)

	// Once confirmed, a further hit inside the window counts at once under its own model.
	p.recordLabelHit(settings, confirmSource, labelHumanHit(t0.Add(10*time.Second), attrModelPerch, 0.75, 0))
	require.Len(t, p.humanHits[confirmSource], 3)
	assert.Equal(t, attrModelPerch, p.humanHits[confirmSource][2].modelID)
	assert.InDelta(t, 0.75, p.humanHits[confirmSource][2].score, 1e-9)
}

func TestRecordVADHit_ConfirmationAttributesBothHits(t *testing.T) {
	t.Parallel()
	p, settings := confirmProcessor(2.4)
	t0 := time.Unix(1000, 0)

	p.recordLabelHit(settings, confirmSource, labelHumanHit(t0, attrModelPerch, 0.5, 0))
	p.recordVADHit(settings, confirmSource, vadHumanHit(t0.Add(time.Second), 0.8125, 0))

	require.Len(t, p.humanHits[confirmSource], 2)
	// The VAD hit is recorded first, then the held label hit it confirms.
	assert.Equal(t, humanHit{at: t0.Add(time.Second), trigger: metrics.TriggerVAD, modelID: vadHitModelID, score: 0.8125}, p.humanHits[confirmSource][0])
	assert.Equal(t, humanHit{at: t0, trigger: metrics.TriggerLabel, modelID: attrModelPerch, score: 0.5}, p.humanHits[confirmSource][1], "the confirmed label hit keeps its model, stamp and score")
	last, ok := lastHit(p)
	require.True(t, ok)
	assert.Equal(t, vadHitModelID, last.ModelID)
	assert.InDelta(t, 0.8125, last.Score, 1e-9)
}

func TestCheckPrivacy_ContributionsNeedDebugAndAHitReachingTheClip(t *testing.T) {
	t.Parallel()
	t0 := time.Unix(1000, 0)
	item := newPrivacyFilterDetection(confirmSource, t0)
	tests := []struct {
		name  string
		debug bool
		hitAt func(clipFrom time.Time) time.Time
		want  bool
	}{
		{"debug off", false, func(from time.Time) time.Time { return from }, false},
		{"hit reaches the clip", true, func(from time.Time) time.Time { return from }, true},
		{"hit exactly at the guard boundary", true, func(from time.Time) time.Time { return from.Add(-privacyClipGuard) }, true},
		{"hit one tick before the guard", true, func(from time.Time) time.Time { return from.Add(-privacyClipGuard - time.Nanosecond) }, false},
		{"hit long before the clip", true, func(from time.Time) time.Time { return from.Add(-time.Minute) }, false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			p, settings := confirmProcessor(0)
			settings.Realtime.PrivacyFilter.Debug = tt.debug
			from := clipSpan(settings, item).from
			labelHit(p, settings, confirmSource, attrModelPerch, tt.hitAt(from))

			got := p.checkPrivacy(settings, item, time.Now())

			require.True(t, got.hasHit)
			if tt.want {
				assert.Equal(t, []hitContribution{{trigger: metrics.TriggerLabel, modelID: attrModelPerch, count: 1, maxScore: float64(float32(0.9))}}, got.contributions)
			} else {
				assert.Empty(t, got.contributions)
			}
		})
	}
}

// Not parallel: logtest.CaptureBufferAt replaces the global logger.
func TestReportHitDecision_CountedLineCarriesModelAndStamp(t *testing.T) {
	infoLog := logtest.CaptureBufferAt(t, slog.LevelInfo)
	p, settings := confirmProcessor(0)
	t0 := time.Unix(1000, 0).UTC()

	labelHit(p, settings, confirmSource, attrModelPerch, t0)

	out := infoLog.String()
	assert.Contains(t, out, "human detection filtered")
	assert.Contains(t, out, "confidence=0.9 ", "the float32 score renders as 0.9, not as the widened float64")
	assert.Contains(t, out, "model_id="+attrModelPerch)
	assert.Contains(t, out, "hit_time=1970-01-01T00:16:40")
	assert.NotContains(t, out, "confirmed_by", "a hit that needed no confirmation names no confirmer")
}

// Not parallel: logtest.CaptureBufferAt replaces the global logger.
func TestReportHitDecision_ConfirmedLineCarriesTheHeldStamp(t *testing.T) {
	infoLog := logtest.CaptureBufferAt(t, slog.LevelInfo)
	p, settings := confirmProcessor(2.4)
	t0 := time.Unix(1000, 0).UTC()

	labelHit(p, settings, confirmSource, attrModelPerch, t0)
	labelHit(p, settings, confirmSource, attrModelBirdNET, t0.Add(5*time.Second))

	out := infoLog.String()
	assert.Contains(t, out, "hit_time=1970-01-01T00:16:40", "the held hit is logged with its own stamp")
	assert.Contains(t, out, "hit_time=1970-01-01T00:16:45", "the confirming hit is logged with its own stamp")
}

// Not parallel: logtest.CaptureBufferAt replaces the global logger.
func TestShouldDiscardDetection_DebugLineCarriesHitModelAndScore(t *testing.T) {
	debugLog := logtest.CaptureBufferAt(t, slog.LevelDebug)
	p, settings := confirmProcessor(0)
	t0 := time.Unix(1000, 0)

	p.detectionMutex.Lock()
	p.recordHumanHitLocked(confirmSource, labelHumanHit(t0, attrModelPerch, 0.5, 0))
	p.detectionMutex.Unlock()
	discard, _ := discardNow(p, newPrivacyFilterDetection(confirmSource, t0), settings, 1)

	require.True(t, discard)
	out := debugLog.String()
	assert.Contains(t, out, "Detection discarded by privacy filter")
	assert.Contains(t, out, "hit_model_id="+attrModelPerch)
	assert.Contains(t, out, "hit_score=0.5")
}
