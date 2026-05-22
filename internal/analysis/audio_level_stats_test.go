package analysis

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestAudioLevelStats_BasicAccumulation(t *testing.T) {
	t.Parallel()

	als := NewAudioLevelStats()

	als.Record("Backyard Mic", 50, false)
	als.Record("Backyard Mic", 30, false)
	als.Record("Backyard Mic", 70, false)
	als.Record("Backyard Mic", 0, false)

	als.mu.Lock()
	defer als.mu.Unlock()

	require.Contains(t, als.stats, "Backyard Mic")
	a := als.stats["Backyard Mic"]

	assert.Equal(t, int64(4), a.count)
	assert.Equal(t, 0, a.min)
	assert.Equal(t, 70, a.max)
	assert.Equal(t, int64(150), a.sum)
	assert.Equal(t, int64(1), a.zeroCount)
	assert.Equal(t, int64(0), a.clipCount)
}

func TestAudioLevelStats_ClippingTracking(t *testing.T) {
	t.Parallel()

	als := NewAudioLevelStats()

	als.Record("Mic", 95, true)
	als.Record("Mic", 80, false)
	als.Record("Mic", 98, true)
	als.Record("Mic", 50, false)

	als.mu.Lock()
	defer als.mu.Unlock()

	a := als.stats["Mic"]
	assert.Equal(t, int64(2), a.clipCount)
}

func TestAudioLevelStats_ZeroPercentage(t *testing.T) {
	t.Parallel()

	als := NewAudioLevelStats()

	for range 8 {
		als.Record("Mic", 0, false)
	}
	als.Record("Mic", 10, false)
	als.Record("Mic", 20, false)

	als.mu.Lock()
	a := als.stats["Mic"]
	als.mu.Unlock()

	zeroPct := int(a.zeroCount * 100 / a.count)
	assert.Equal(t, 80, zeroPct)
}

func TestAudioLevelStats_MultipleSources(t *testing.T) {
	t.Parallel()

	als := NewAudioLevelStats()

	als.Record("Mic A", 50, false)
	als.Record("Mic B", 30, false)
	als.Record("Mic A", 60, false)

	als.mu.Lock()
	defer als.mu.Unlock()

	require.Len(t, als.stats, 2)
	assert.Equal(t, int64(2), als.stats["Mic A"].count)
	assert.Equal(t, int64(1), als.stats["Mic B"].count)
}

func TestAudioLevelStats_LogAndResetClearsStats(t *testing.T) {
	t.Parallel()

	als := NewAudioLevelStats()

	als.Record("Mic", 50, false)

	log := GetLogger()
	als.logAndReset(log)

	als.mu.Lock()
	defer als.mu.Unlock()
	assert.Empty(t, als.stats)
}
