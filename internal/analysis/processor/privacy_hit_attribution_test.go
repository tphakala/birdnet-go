package processor

import (
	"log/slog"
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
	p.recordVADHit(settings, confirmSource, t0.Add(3*time.Second), 0.81)

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

func TestAppendHumanHit_PrunesByAgeAndCapsPerSource(t *testing.T) {
	t.Parallel()
	p := &Processor{}
	t0 := time.Unix(1000, 0)

	p.appendHumanHitLocked(confirmSource, humanHit{at: t0, trigger: metrics.TriggerLabel})
	p.appendHumanHitLocked(confirmSource, humanHit{at: t0.Add(humanHitRetention + time.Second), trigger: metrics.TriggerLabel})
	require.Len(t, p.humanHits[confirmSource], 1, "a hit older than the retention before the newest is dropped")

	for i := range 2 * humanHitMaxPerSource {
		p.appendHumanHitLocked(confirmSource, humanHit{at: t0.Add(humanHitRetention + time.Duration(i)*time.Millisecond), trigger: metrics.TriggerLabel})
	}
	assert.Len(t, p.humanHits[confirmSource], humanHitMaxPerSource)
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

	p.recordHumanHitLocked(confirmSource, t0.Add(time.Second), metrics.TriggerLabel, attrModelPerch, 0.2)
	p.recordHumanHitLocked(confirmSource, t0, metrics.TriggerLabel, attrModelBirdNET, 0.3)

	assert.Equal(t, attrModelPerch, p.LastHumanDetection[confirmSource].ModelID, "the stored latest hit never moves backward")
	assert.Len(t, p.humanHits[confirmSource], 2, "the earlier hit still counts as a contributor")
}

// Not parallel: logtest.CaptureBufferAt replaces the global logger.
func TestLogLabelHitDecision_NamesModelAndConfirmer(t *testing.T) {
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
func TestLogLabelHitDecision_VADConfirmerAndDroppedModel(t *testing.T) {
	infoLog := logtest.CaptureBufferAt(t, slog.LevelInfo)
	p, settings := confirmProcessor(2.4)
	t0 := time.Unix(1000, 0)

	labelHit(p, settings, confirmSource, attrModelPerch, t0)
	d := p.recordVADHit(settings, confirmSource, t0.Add(time.Second), 0.8)
	p.logLabelHitDecision(settings, confirmSource, nil, &d)
	assert.Contains(t, infoLog.String(), "confirmed_by="+vadHitModelID)

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
