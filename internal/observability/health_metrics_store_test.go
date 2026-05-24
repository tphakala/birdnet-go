package observability

import (
	"fmt"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestHealthMetricsStore_RecordAndSum(t *testing.T) {
	t.Parallel()
	s := NewHealthMetricsStore()
	now := time.Date(2026, 5, 24, 10, 30, 0, 0, time.UTC)

	s.RecordAt("audio.drops.src1", 5, now)
	s.RecordAt("audio.drops.src1", 3, now.Add(10*time.Minute))

	assert.Equal(t, int64(8), s.SumAt("audio.drops.src1", time.Hour, now.Add(10*time.Minute)))
}

func TestHealthMetricsStore_SumWindowBoundary(t *testing.T) {
	t.Parallel()
	s := NewHealthMetricsStore()
	base := time.Date(2026, 5, 24, 10, 0, 0, 0, time.UTC)

	s.RecordAt("drops", 10, base)
	s.RecordAt("drops", 20, base.Add(time.Hour))
	s.RecordAt("drops", 30, base.Add(2*time.Hour))

	now := base.Add(2*time.Hour + 30*time.Minute)

	// At 12:30 with a 1h window (cutoff 11:30), both the 12:00 bucket and
	// the 11:00 bucket overlap the window, so both are included. This
	// over-counts by up to one bucket-width of older data but never
	// under-counts recent activity at hour boundaries.
	assert.Equal(t, int64(50), s.SumAt("drops", time.Hour, now))
	assert.Equal(t, int64(60), s.SumAt("drops", 2*time.Hour, now))
	assert.Equal(t, int64(60), s.SumAt("drops", 3*time.Hour, now))
}

func TestHealthMetricsStore_BucketRollover(t *testing.T) {
	t.Parallel()
	s := NewHealthMetricsStoreWithSize(4)
	base := time.Date(2026, 5, 24, 10, 0, 0, 0, time.UTC)

	for i := range 6 {
		s.RecordAt("m", int64(i+1), base.Add(time.Duration(i)*time.Hour))
	}

	buckets := s.Buckets("m", 10)
	require.Len(t, buckets, 4)
	assert.Equal(t, int64(3), buckets[0].Count)
	assert.Equal(t, int64(6), buckets[3].Count)
}

func TestHealthMetricsStore_SevenDayEviction(t *testing.T) {
	t.Parallel()
	s := NewHealthMetricsStore()
	base := time.Date(2026, 5, 17, 0, 0, 0, 0, time.UTC)

	s.RecordAt("m", 100, base)

	for i := 1; i <= 168; i++ {
		s.RecordAt("m", 1, base.Add(time.Duration(i)*time.Hour))
	}

	buckets := s.Buckets("m", 200)
	require.Len(t, buckets, 168)
	assert.Equal(t, int64(1), buckets[0].Count)
}

func TestHealthMetricsStore_LastEventTime(t *testing.T) {
	t.Parallel()
	s := NewHealthMetricsStore()
	now := time.Date(2026, 5, 24, 10, 30, 0, 0, time.UTC)

	assert.True(t, s.LastEventTime("missing").IsZero())

	s.RecordAt("m", 5, now)
	assert.Equal(t, now, s.LastEventTime("m"))

	later := now.Add(5 * time.Minute)
	s.RecordAt("m", 0, later)
	assert.Equal(t, now, s.LastEventTime("m"))

	laterEvent := now.Add(10 * time.Minute)
	s.RecordAt("m", 1, laterEvent)
	assert.Equal(t, laterEvent, s.LastEventTime("m"))
}

func TestHealthMetricsStore_Keys(t *testing.T) {
	t.Parallel()
	s := NewHealthMetricsStore()
	now := time.Now()

	s.RecordAt("audio.drops.src1", 1, now)
	s.RecordAt("audio.drops.src2", 1, now)
	s.RecordAt("stream.restarts.abc", 1, now)

	keys := s.Keys()
	assert.Len(t, keys, 3)
	assert.ElementsMatch(t, []string{"audio.drops.src1", "audio.drops.src2", "stream.restarts.abc"}, keys)
}

func TestHealthMetricsStore_KeysWithPrefix(t *testing.T) {
	t.Parallel()
	s := NewHealthMetricsStore()
	now := time.Now()

	s.RecordAt("audio.drops.src1", 1, now)
	s.RecordAt("audio.drops.src2", 1, now)
	s.RecordAt("audio.overruns.src1", 1, now)
	s.RecordAt("stream.restarts.abc", 1, now)

	keys := s.KeysWithPrefix("audio.drops.")
	assert.Len(t, keys, 2)
	assert.ElementsMatch(t, []string{"audio.drops.src1", "audio.drops.src2"}, keys)
}

func TestHealthMetricsStore_LifetimeTotal(t *testing.T) {
	t.Parallel()
	s := NewHealthMetricsStore()
	base := time.Date(2026, 5, 24, 10, 0, 0, 0, time.UTC)

	s.RecordAt("m", 10, base)
	s.RecordAt("m", 20, base.Add(time.Hour))
	s.RecordAt("m", 30, base.Add(2*time.Hour))

	assert.Equal(t, int64(60), s.LifetimeTotal("m"))
	assert.Equal(t, int64(0), s.LifetimeTotal("nonexistent"))
}

func TestHealthMetricsStore_SumUnknownKey(t *testing.T) {
	t.Parallel()
	s := NewHealthMetricsStore()
	assert.Equal(t, int64(0), s.Sum("nonexistent", time.Hour))
}

func TestHealthMetricsStore_BucketsUnknownKey(t *testing.T) {
	t.Parallel()
	s := NewHealthMetricsStore()
	assert.Nil(t, s.Buckets("nonexistent", 10))
}

func TestHealthMetricsStore_LatestBucketTime(t *testing.T) {
	t.Parallel()
	s := NewHealthMetricsStore()
	now := time.Date(2026, 5, 24, 10, 30, 0, 0, time.UTC)

	assert.True(t, s.LatestBucketTime("missing").IsZero())

	s.RecordAt("m", 5, now)
	assert.Equal(t, bucketStart(now), s.LatestBucketTime("m"))
}

func TestHealthMetricsStore_SameHourAggregation(t *testing.T) {
	t.Parallel()
	s := NewHealthMetricsStore()
	base := time.Date(2026, 5, 24, 10, 0, 0, 0, time.UTC)

	s.RecordAt("m", 5, base)
	s.RecordAt("m", 3, base.Add(15*time.Minute))
	s.RecordAt("m", 2, base.Add(45*time.Minute))

	buckets := s.Buckets("m", 10)
	require.Len(t, buckets, 1)
	assert.Equal(t, int64(10), buckets[0].Count)
}

func TestHealthMetricsStore_ConcurrentAccess(t *testing.T) {
	t.Parallel()
	s := NewHealthMetricsStore()
	now := time.Date(2026, 5, 24, 10, 0, 0, 0, time.UTC)

	var wg sync.WaitGroup
	for i := range 100 {
		wg.Add(1)
		go func(n int) {
			defer wg.Done()
			key := fmt.Sprintf("metric.%d", n%5)
			s.RecordAt(key, 1, now.Add(time.Duration(n)*time.Minute))
			_ = s.SumAt(key, time.Hour, now.Add(time.Hour))
			_ = s.Buckets(key, 10)
			_ = s.LastEventTime(key)
			_ = s.Keys()
		}(i)
	}
	wg.Wait()

	var total int64
	for i := range 5 {
		total += s.SumAt(fmt.Sprintf("metric.%d", i), 24*time.Hour, now.Add(24*time.Hour))
	}
	assert.Equal(t, int64(100), total)
}

func TestHealthMetricsStore_Buckets_ChronologicalOrder(t *testing.T) {
	t.Parallel()
	s := NewHealthMetricsStore()
	base := time.Date(2026, 5, 24, 10, 0, 0, 0, time.UTC)

	s.RecordAt("m", 1, base)
	s.RecordAt("m", 2, base.Add(time.Hour))
	s.RecordAt("m", 3, base.Add(2*time.Hour))

	buckets := s.Buckets("m", 3)
	require.Len(t, buckets, 3)
	assert.True(t, buckets[0].Start.Before(buckets[1].Start))
	assert.True(t, buckets[1].Start.Before(buckets[2].Start))
	assert.Equal(t, int64(1), buckets[0].Count)
	assert.Equal(t, int64(3), buckets[2].Count)
}
