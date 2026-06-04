package checks

import (
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/tphakala/birdnet-go/internal/health"
	"github.com/tphakala/birdnet-go/internal/observability"
)

func newTestHealthStore(t *testing.T) *observability.HealthMetricsStore {
	t.Helper()
	return observability.NewHealthMetricsStore()
}

func newTestEventBuffer(t *testing.T) *observability.HealthEventBuffer {
	t.Helper()
	return observability.NewHealthEventBuffer(100)
}

func eventGetter(buf *observability.HealthEventBuffer) func(string, int) []observability.HealthEvent {
	return buf.Recent
}

func TestBufferDropsCheck_NilStore(t *testing.T) {
	t.Parallel()
	check := NewBufferDropsCheck(nil, nil)
	result := check.Run(t.Context())
	assert.Equal(t, health.StatusSkipped, result.Status)
	assert.Equal(t, "buffer_drops", result.Name)
}

func TestBufferDropsCheck_NoData(t *testing.T) {
	t.Parallel()
	store := newTestHealthStore(t)
	check := NewBufferDropsCheck(store, nil)
	result := check.Run(t.Context())
	assert.Equal(t, health.StatusSkipped, result.Status)
}

func TestBufferDropsCheck_HealthyWithHistory(t *testing.T) {
	t.Parallel()
	store := newTestHealthStore(t)
	buf := newTestEventBuffer(t)

	now := time.Now()
	store.RecordAt("audio.drops.src1", 50, now.Add(-3*time.Hour))
	buf.Add(observability.HealthEvent{Time: now.Add(-3 * time.Hour), Source: "src1", Delta: 50, Metric: "drops"})
	store.RecordAt("audio.drops.src1", 0, now)

	check := NewBufferDropsCheck(store, eventGetter(buf))
	result := check.Run(t.Context())
	assert.Equal(t, health.StatusHealthy, result.Status)
	assert.Contains(t, result.Message, "No drops in last 1h")
	assert.Contains(t, result.Message, "50 lifetime")
}

func TestBufferDropsCheck_Warning(t *testing.T) {
	t.Parallel()
	store := newTestHealthStore(t)

	store.RecordAt("audio.drops.src1", 15, time.Now())

	check := NewBufferDropsCheck(store, nil)
	result := check.Run(t.Context())
	assert.Equal(t, health.StatusWarning, result.Status)
	assert.Contains(t, result.Message, "15 drops in 1h")
}

func TestBufferDropsCheck_Critical(t *testing.T) {
	t.Parallel()
	store := newTestHealthStore(t)

	store.RecordAt("audio.drops.src1", 150, time.Now())

	check := NewBufferDropsCheck(store, nil)
	result := check.Run(t.Context())
	assert.Equal(t, health.StatusCritical, result.Status)
}

func TestBufferDropsCheck_WithWindow(t *testing.T) {
	t.Parallel()
	store := newTestHealthStore(t)
	now := time.Now()

	store.RecordAt("audio.drops.src1", 20, now.Add(-3*time.Hour))
	store.RecordAt("audio.drops.src1", 0, now)

	check := NewBufferDropsCheck(store, nil)

	narrowed := check.WithWindow(1 * time.Hour)
	result := narrowed.Run(t.Context())
	assert.Equal(t, health.StatusHealthy, result.Status)

	wide := check.WithWindow(6 * time.Hour)
	result = wide.Run(t.Context())
	assert.Equal(t, health.StatusWarning, result.Status)
}

func TestBufferDropsCheck_SparklineInDetails(t *testing.T) {
	t.Parallel()
	store := newTestHealthStore(t)

	store.RecordAt("audio.drops.src1", 10, time.Now().Add(-2*time.Hour))
	store.RecordAt("audio.drops.src1", 5, time.Now())

	check := NewBufferDropsCheck(store, nil)
	result := check.Run(t.Context())

	require.NotNil(t, result.Details)
	assert.NotNil(t, result.Details["sparkline"])
	assert.Equal(t, "1h", result.Details["window"])
}

func TestBufferOverrunCheck_NilStore(t *testing.T) {
	t.Parallel()
	check := NewBufferOverrunCheck(nil, nil)
	result := check.Run(t.Context())
	assert.Equal(t, health.StatusSkipped, result.Status)
	assert.Equal(t, "buffer_overrun", result.Name)
}

func TestBufferOverrunCheck_Healthy(t *testing.T) {
	t.Parallel()
	store := newTestHealthStore(t)
	store.RecordAt("audio.overruns.src1", 0, time.Now())
	check := NewBufferOverrunCheck(store, nil)
	result := check.Run(t.Context())
	assert.Equal(t, health.StatusHealthy, result.Status)
}

func TestBufferOverrunCheck_NoData(t *testing.T) {
	t.Parallel()
	store := newTestHealthStore(t)
	check := NewBufferOverrunCheck(store, nil)
	result := check.Run(t.Context())
	assert.Equal(t, health.StatusSkipped, result.Status)
}

func TestBufferOverrunCheck_Warning(t *testing.T) {
	t.Parallel()
	store := newTestHealthStore(t)
	store.RecordAt("audio.overruns.src1", 15, time.Now())

	check := NewBufferOverrunCheck(store, nil)
	result := check.Run(t.Context())
	assert.Equal(t, health.StatusWarning, result.Status)
}

func TestBufferOverrunCheck_Critical(t *testing.T) {
	t.Parallel()
	store := newTestHealthStore(t)
	store.RecordAt("audio.overruns.src1", 60, time.Now())

	check := NewBufferOverrunCheck(store, nil)
	result := check.Run(t.Context())
	assert.Equal(t, health.StatusCritical, result.Status)
}

func TestBufferOverrunCheck_WithWindow(t *testing.T) {
	t.Parallel()
	store := newTestHealthStore(t)
	check := NewBufferOverrunCheck(store, nil)
	narrowed := check.WithWindow(15 * time.Minute)
	assert.NotNil(t, narrowed)
}

func TestBufferDropsCheck_CritAt50(t *testing.T) {
	t.Parallel()
	store := newTestHealthStore(t)

	// baseCritThreshold is 50; maxHourly(50) >= baseCrit(50) triggers peak safety net.
	store.RecordAt("audio.drops.src1", 50, time.Now())

	check := NewBufferDropsCheck(store, nil)
	result := check.Run(t.Context())
	assert.Equal(t, health.StatusCritical, result.Status)
}

func TestBufferOverrunCheck_LowerThresholds(t *testing.T) {
	t.Parallel()

	t.Run("WarnAt5", func(t *testing.T) {
		t.Parallel()
		store := newTestHealthStore(t)

		// baseWarnThreshold is 5; maxHourly(5) >= baseWarn(5) -> peakEscalated -> Warning.
		store.RecordAt("audio.overruns.src1", 5, time.Now())

		check := NewBufferOverrunCheck(store, nil)
		result := check.Run(t.Context())
		assert.Equal(t, health.StatusWarning, result.Status)
	})

	t.Run("CritAt25", func(t *testing.T) {
		t.Parallel()
		store := newTestHealthStore(t)

		// baseCritThreshold is 25; maxHourly(25) >= baseCrit(25) -> peak safety net -> Critical.
		store.RecordAt("audio.overruns.src1", 25, time.Now())

		check := NewBufferOverrunCheck(store, nil)
		result := check.Run(t.Context())
		assert.Equal(t, health.StatusCritical, result.Status)
	})
}

func TestAudioLevelCheck_NilProvider(t *testing.T) {
	t.Parallel()
	check := NewAudioLevelCheck(nil)
	result := check.Run(t.Context())
	assert.Equal(t, health.StatusSkipped, result.Status)
	assert.Equal(t, "audio_level", result.Name)
}

func TestAudioLevelCheck_Empty(t *testing.T) {
	t.Parallel()
	check := NewAudioLevelCheck(func() []AudioLevelInfo { return nil })
	result := check.Run(t.Context())
	assert.Equal(t, health.StatusSkipped, result.Status)
}

func TestAudioLevelCheck_Healthy(t *testing.T) {
	t.Parallel()
	check := NewAudioLevelCheck(func() []AudioLevelInfo {
		return []AudioLevelInfo{
			{Source: "src1", Level: 42, Clipping: false},
			{Source: "src2", Level: 15, Clipping: false},
		}
	})
	result := check.Run(t.Context())
	assert.Equal(t, health.StatusHealthy, result.Status)
	assert.Contains(t, result.Message, "normal")
}

func TestAudioLevelCheck_Silence(t *testing.T) {
	t.Parallel()
	check := NewAudioLevelCheck(func() []AudioLevelInfo {
		return []AudioLevelInfo{
			{Source: "src1", Level: 0},
			{Source: "src2", Level: 0},
		}
	})
	result := check.Run(t.Context())
	assert.Equal(t, health.StatusWarning, result.Status)
	assert.Contains(t, result.Message, "silence")
}

func TestAudioLevelCheck_Clipping(t *testing.T) {
	t.Parallel()
	check := NewAudioLevelCheck(func() []AudioLevelInfo {
		return []AudioLevelInfo{
			{Source: "src1", Level: 99, Clipping: true},
			{Source: "src2", Level: 50, Clipping: false},
		}
	})
	result := check.Run(t.Context())
	assert.Equal(t, health.StatusWarning, result.Status)
	assert.Contains(t, result.Message, "Clipping")
}

func TestAudioLevelCheck_PartialSilence(t *testing.T) {
	t.Parallel()
	check := NewAudioLevelCheck(func() []AudioLevelInfo {
		return []AudioLevelInfo{
			{Source: "src1", Level: 0},
			{Source: "src2", Level: 50},
		}
	})
	result := check.Run(t.Context())
	assert.Equal(t, health.StatusWarning, result.Status)
	assert.Contains(t, result.Message, "Silence detected on 1")
}

func TestCaptureBufferCheck_NilProvider(t *testing.T) {
	t.Parallel()
	check := NewCaptureBufferCheck(nil)
	result := check.Run(t.Context())
	assert.Equal(t, health.StatusSkipped, result.Status)
	assert.Equal(t, "capture_buffer", result.Name)
}

func TestCaptureBufferCheck_Empty(t *testing.T) {
	t.Parallel()
	check := NewCaptureBufferCheck(func() []CaptureBufferInfo { return nil })
	result := check.Run(t.Context())
	assert.Equal(t, health.StatusSkipped, result.Status)
}

func TestCaptureBufferCheck_Healthy(t *testing.T) {
	t.Parallel()
	check := NewCaptureBufferCheck(func() []CaptureBufferInfo {
		return []CaptureBufferInfo{
			{SourceID: "src1", Capacity: 96000, Initialized: true},
		}
	})
	result := check.Run(t.Context())
	assert.Equal(t, health.StatusHealthy, result.Status)
	assert.Contains(t, result.Message, "allocated")
}

func TestCaptureBufferCheck_Uninitialized(t *testing.T) {
	t.Parallel()
	check := NewCaptureBufferCheck(func() []CaptureBufferInfo {
		return []CaptureBufferInfo{
			{SourceID: "src1", Capacity: 96000, Initialized: false},
		}
	})
	result := check.Run(t.Context())
	assert.Equal(t, health.StatusWarning, result.Status)
	assert.Contains(t, result.Message, "not yet initialized")
}

func TestCaptureBufferCheck_Details(t *testing.T) {
	t.Parallel()
	check := NewCaptureBufferCheck(func() []CaptureBufferInfo {
		return []CaptureBufferInfo{
			{SourceID: "src1", Capacity: 96000, Initialized: true},
			{SourceID: "src2", Capacity: 48000, Initialized: true},
		}
	})
	result := check.Run(t.Context())
	require.NotNil(t, result.Details)
	assert.Equal(t, 2, result.Details["buffers"])
	assert.Equal(t, 144000, result.Details["total_capacity"])
	assert.Equal(t, 0, result.Details["uninitialized"])
}
