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

	assert.Equal(t, int64(4), a.Count)
	assert.Equal(t, 0, a.Min)
	assert.Equal(t, 70, a.Max)
	assert.Equal(t, int64(150), a.Sum)
	assert.Equal(t, int64(1), a.ZeroCount)
	assert.Equal(t, int64(0), a.ClipCount)
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
	assert.Equal(t, int64(2), a.ClipCount)
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

	zeroPct := int(a.ZeroCount * 100 / a.Count)
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
	assert.Equal(t, int64(2), als.stats["Mic A"].Count)
	assert.Equal(t, int64(1), als.stats["Mic B"].Count)
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
