package observability

import (
	"fmt"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestHealthEventBuffer_AddAndRecent(t *testing.T) {
	t.Parallel()
	buf := NewHealthEventBuffer(10)

	now := time.Now()
	buf.Add(HealthEvent{Time: now, Source: "src1", Delta: 5, Metric: "drops"})
	buf.Add(HealthEvent{Time: now.Add(time.Second), Source: "src2", Delta: 3, Metric: "drops"})

	events := buf.Recent("drops", 10)
	require.Len(t, events, 2)
	assert.Equal(t, "src2", events[0].Source)
	assert.Equal(t, "src1", events[1].Source)
}

func TestHealthEventBuffer_FilterByMetric(t *testing.T) {
	t.Parallel()
	buf := NewHealthEventBuffer(10)

	now := time.Now()
	buf.Add(HealthEvent{Time: now, Source: "s1", Delta: 1, Metric: "drops"})
	buf.Add(HealthEvent{Time: now, Source: "s2", Delta: 2, Metric: "overruns"})
	buf.Add(HealthEvent{Time: now, Source: "s3", Delta: 3, Metric: "drops"})

	drops := buf.Recent("drops", 10)
	assert.Len(t, drops, 2)

	overruns := buf.Recent("overruns", 10)
	assert.Len(t, overruns, 1)
	assert.Equal(t, int64(2), overruns[0].Delta)
}

func TestHealthEventBuffer_Overflow(t *testing.T) {
	t.Parallel()
	buf := NewHealthEventBuffer(3)

	for i := range 5 {
		buf.Add(HealthEvent{
			Time:   time.Now(),
			Source: fmt.Sprintf("src%d", i),
			Delta:  int64(i),
			Metric: "drops",
		})
	}

	events := buf.Recent("drops", 10)
	require.Len(t, events, 3)
	assert.Equal(t, "src4", events[0].Source)
	assert.Equal(t, "src3", events[1].Source)
	assert.Equal(t, "src2", events[2].Source)
}

func TestHealthEventBuffer_LimitN(t *testing.T) {
	t.Parallel()
	buf := NewHealthEventBuffer(10)

	for i := range 5 {
		buf.Add(HealthEvent{
			Time:   time.Now(),
			Source: fmt.Sprintf("src%d", i),
			Delta:  1,
			Metric: "drops",
		})
	}

	events := buf.Recent("drops", 2)
	assert.Len(t, events, 2)
}

func TestHealthEventBuffer_EmptyBuffer(t *testing.T) {
	t.Parallel()
	buf := NewHealthEventBuffer(10)

	events := buf.Recent("drops", 10)
	assert.Nil(t, events)
}

func TestHealthEventBuffer_ZeroN(t *testing.T) {
	t.Parallel()
	buf := NewHealthEventBuffer(10)
	buf.Add(HealthEvent{Time: time.Now(), Source: "s1", Delta: 1, Metric: "drops"})

	events := buf.Recent("drops", 0)
	assert.Nil(t, events)
}

func TestHealthEventBuffer_RecentAll(t *testing.T) {
	t.Parallel()
	buf := NewHealthEventBuffer(10)

	buf.Add(HealthEvent{Time: time.Now(), Source: "s1", Delta: 1, Metric: "drops"})
	buf.Add(HealthEvent{Time: time.Now(), Source: "s2", Delta: 2, Metric: "overruns"})

	events := buf.RecentAll(10)
	assert.Len(t, events, 2)
}

func TestHealthEventBuffer_ConcurrentAccess(t *testing.T) {
	t.Parallel()
	buf := NewHealthEventBuffer(50)

	var wg sync.WaitGroup
	for i := range 100 {
		wg.Add(1)
		go func(n int) {
			defer wg.Done()
			buf.Add(HealthEvent{
				Time:   time.Now(),
				Source: fmt.Sprintf("src%d", n),
				Delta:  1,
				Metric: "drops",
			})
			_ = buf.Recent("drops", 10)
		}(i)
	}
	wg.Wait()

	events := buf.RecentAll(100)
	assert.Len(t, events, 50)
}

func TestHealthEventBuffer_MostRecentFirst(t *testing.T) {
	t.Parallel()
	buf := NewHealthEventBuffer(10)

	base := time.Date(2026, 5, 24, 10, 0, 0, 0, time.UTC)
	for i := range 5 {
		buf.Add(HealthEvent{
			Time:   base.Add(time.Duration(i) * time.Minute),
			Source: fmt.Sprintf("src%d", i),
			Delta:  1,
			Metric: "drops",
		})
	}

	events := buf.Recent("drops", 5)
	require.Len(t, events, 5)
	for i := range len(events) - 1 {
		assert.True(t, events[i].Time.After(events[i+1].Time) || events[i].Time.Equal(events[i+1].Time))
	}
}
