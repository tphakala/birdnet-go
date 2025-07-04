package audiocore

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestBufferPoolTierStats(t *testing.T) {
	t.Parallel()
	config := BufferPoolConfig{
		SmallBufferSize:   1024,
		MediumBufferSize:  4096,
		LargeBufferSize:   16384,
		MaxBuffersPerSize: 10,
	}

	pool := NewBufferPool(config)
	require.NotNil(t, pool)

	// Get buffers from different tiers
	smallBuf := pool.Get(512)    // Should use small tier
	mediumBuf := pool.Get(2048)  // Should use medium tier
	largeBuf := pool.Get(8192)   // Should use large tier
	customBuf := pool.Get(32768) // Should use custom tier

	// Check tier-specific stats
	t.Run("SmallTierStats", func(t *testing.T) {
		stats, ok := pool.TierStats("small")
		assert.True(t, ok)
		assert.Equal(t, 1, stats.TotalBuffers)
		assert.Equal(t, 1, stats.ActiveBuffers)
	})

	t.Run("MediumTierStats", func(t *testing.T) {
		stats, ok := pool.TierStats("medium")
		assert.True(t, ok)
		assert.Equal(t, 1, stats.TotalBuffers)
		assert.Equal(t, 1, stats.ActiveBuffers)
	})

	t.Run("LargeTierStats", func(t *testing.T) {
		stats, ok := pool.TierStats("large")
		assert.True(t, ok)
		assert.Equal(t, 1, stats.TotalBuffers)
		assert.Equal(t, 1, stats.ActiveBuffers)
	})

	t.Run("CustomTierStats", func(t *testing.T) {
		stats, ok := pool.TierStats("custom")
		assert.True(t, ok)
		assert.Equal(t, 1, stats.TotalBuffers)
		assert.Equal(t, 1, stats.ActiveBuffers)
	})

	// Return buffers to pool
	pool.Put(smallBuf)
	pool.Put(mediumBuf)
	pool.Put(largeBuf)
	pool.Put(customBuf) // Custom buffer will be discarded

	// Check stats after returning buffers
	t.Run("StatsAfterReturn", func(t *testing.T) {
		// Small tier should have buffer returned
		stats, ok := pool.TierStats("small")
		assert.True(t, ok)
		assert.Equal(t, 1, stats.TotalBuffers)
		assert.Equal(t, 0, stats.ActiveBuffers)

		// Custom tier should still show as active (discarded but counted)
		stats, ok = pool.TierStats("custom")
		assert.True(t, ok)
		assert.Equal(t, 1, stats.TotalBuffers)
		assert.Equal(t, 0, stats.ActiveBuffers)
	})

	// Test non-existent tier
	t.Run("NonExistentTier", func(t *testing.T) {
		_, ok := pool.TierStats("invalid")
		assert.False(t, ok)
	})
}

func TestBufferPoolReportMetrics(t *testing.T) {
	t.Parallel()
	config := BufferPoolConfig{
		SmallBufferSize:   1024,
		MediumBufferSize:  4096,
		LargeBufferSize:   16384,
		MaxBuffersPerSize: 10,
	}

	pool := NewBufferPool(config)
	require.NotNil(t, pool)

	// Get some buffers to create stats
	buf1 := pool.Get(512)
	buf2 := pool.Get(2048)
	defer pool.Put(buf1)
	defer pool.Put(buf2)

	// Call ReportMetrics - should not panic even if metrics collector is nil
	pool.ReportMetrics()
}
