package apicore

import (
	"context"
	"sync/atomic"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/tphakala/birdnet-go/internal/datastore"
	"github.com/tphakala/birdnet-go/internal/errors"
	"github.com/tphakala/birdnet-go/internal/ttlcache"
)

func TestDetectionPageCache_NilReceiverIsSafe(t *testing.T) {
	t.Parallel()
	var c *DetectionPageCache
	var calls atomic.Int32
	load := func(context.Context) (DetectionPage, error) {
		calls.Add(1)
		return DetectionPage{Total: 3}, nil
	}

	for range 2 {
		page, err := c.GetOrLoad(t.Context(), &DetectionPageKey{Kind: DetectionPageSearch}, load)
		require.NoError(t, err)
		assert.Equal(t, int64(3), page.Total)
	}
	assert.Equal(t, int32(2), calls.Load(), "a nil cache loads every time")

	assert.NotPanics(t, c.Invalidate)
	assert.Equal(t, 0, c.Len())
	assert.Equal(t, ttlcache.Stats{}, c.Stats())
}

func TestNewDetectionPageCache_AppliesTTLAndBound(t *testing.T) {
	t.Parallel()
	now := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	clock := func() time.Time { return now }
	c := NewDetectionPageCache(ttlcache.WithClock(clock))
	ctx := t.Context()
	var calls atomic.Int32
	load := func(context.Context) (DetectionPage, error) {
		calls.Add(1)
		return DetectionPage{Notes: []datastore.Note{{ID: 1}}, Total: 1}, nil
	}
	key := &DetectionPageKey{Kind: DetectionPageHourly, Date: "2026-01-01"}

	_, err := c.GetOrLoad(ctx, key, load)
	require.NoError(t, err)
	now = now.Add(detectionCacheExpiry - time.Nanosecond)
	_, err = c.GetOrLoad(ctx, key, load)
	require.NoError(t, err)
	assert.Equal(t, int32(1), calls.Load(), "hit just before the TTL")
	now = now.Add(time.Nanosecond)
	_, err = c.GetOrLoad(ctx, key, load)
	require.NoError(t, err)
	assert.Equal(t, int32(2), calls.Load(), "miss at the TTL")

	for i := range detectionCacheMaxEntries + 1 {
		_, err := c.GetOrLoad(ctx, &DetectionPageKey{Kind: DetectionPageSearch, Offset: i}, load)
		require.NoError(t, err)
	}
	assert.Equal(t, detectionCacheMaxEntries, c.Len())
	assert.Positive(t, c.Stats().Evictions)
}

func TestDetectionPageCache_InvalidateAndLoaderError(t *testing.T) {
	t.Parallel()
	c := NewDetectionPageCache()
	ctx := t.Context()
	key := &DetectionPageKey{Kind: DetectionPageSpecies, Species: "Parus major"}
	sentinel := errors.NewStd("datastore down")

	_, err := c.GetOrLoad(ctx, key, func(context.Context) (DetectionPage, error) { return DetectionPage{}, sentinel })
	require.ErrorIs(t, err, sentinel)
	assert.Equal(t, 0, c.Len(), "errors are not cached")

	_, err = c.GetOrLoad(ctx, key, func(context.Context) (DetectionPage, error) { return DetectionPage{Total: 1}, nil })
	require.NoError(t, err)
	assert.Equal(t, 1, c.Len())
	c.Invalidate()
	assert.Equal(t, 0, c.Len())
}

func TestDetectionPageCache_LargePagesBypassCache(t *testing.T) {
	t.Parallel()
	c := NewDetectionPageCache()
	ctx := t.Context()
	var calls atomic.Int32
	load := func(context.Context) (DetectionPage, error) {
		calls.Add(1)
		return DetectionPage{Total: 1}, nil
	}

	atBound := &DetectionPageKey{Kind: DetectionPageSearch, Limit: detectionCacheMaxPageNotes}
	for range 2 {
		_, err := c.GetOrLoad(ctx, atBound, load)
		require.NoError(t, err)
	}
	assert.Equal(t, int32(1), calls.Load(), "a page at the bound is cached")
	assert.Equal(t, 1, c.Len())

	overBound := &DetectionPageKey{Kind: DetectionPageSearch, Limit: detectionCacheMaxPageNotes + 1}
	for range 2 {
		_, err := c.GetOrLoad(ctx, overBound, load)
		require.NoError(t, err)
	}
	assert.Equal(t, int32(3), calls.Load(), "a page over the bound loads every time")
	assert.Equal(t, 1, c.Len(), "a page over the bound is not stored")
	assert.Equal(t, uint64(1), c.Stats().Hits, "bypassed loads do not touch the stats")

	done, cancel := context.WithCancel(ctx)
	cancel()
	_, err := c.GetOrLoad(done, overBound, load)
	require.ErrorIs(t, err, context.Canceled)
	assert.Equal(t, int32(3), calls.Load(), "a done context does not start a load")
}

func TestDetectionPageCache_DirectLoadPanicBecomesPanicError(t *testing.T) {
	t.Parallel()
	panicking := func(context.Context) (DetectionPage, error) { panic("datastore exploded") }

	for name, c := range map[string]*DetectionPageCache{
		"nil cache":       nil,
		"page over bound": NewDetectionPageCache(),
	} {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			key := &DetectionPageKey{Kind: DetectionPageSearch, Limit: detectionCacheMaxPageNotes + 1}
			_, err := c.GetOrLoad(t.Context(), key, panicking)
			pe, ok := errors.AsType[*ttlcache.PanicError](err)
			require.True(t, ok, "got %v", err)
			assert.Equal(t, "datastore exploded", pe.Value)
			assert.NotEmpty(t, pe.Stack)
		})
	}
}
