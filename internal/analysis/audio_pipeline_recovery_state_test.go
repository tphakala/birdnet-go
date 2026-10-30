package analysis

import (
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/tphakala/birdnet-go/internal/audiocore"
	"github.com/tphakala/birdnet-go/internal/audiocore/ffmpeg"
	"github.com/tphakala/birdnet-go/internal/errors"
)

const (
	// soundCardSourceID is an ID the FFmpeg stream manager never tracks, like a
	// sound-card source.
	soundCardSourceID = "audio_card_test"
	// probeComponent and probeOperation label the positive-control error.
	probeComponent = "recovery-state-test"
	probeOperation = "hook_probe"
)

// fakeHealthLookup returns a fixed health snapshot.
type fakeHealthLookup struct {
	health *audiocore.StreamHealth
}

func (f fakeHealthLookup) StreamHealth(string) (*audiocore.StreamHealth, error) {
	return f.health, nil
}

// Not parallel: error hooks are package-level state in internal/errors.
func TestStreamRecoveryState_SoundCardMissBuildsNoReportedError(t *testing.T) {
	var (
		mu       sync.Mutex
		captured []*errors.EnhancedError
	)
	errors.AddErrorHook(func(ee *errors.EnhancedError) {
		mu.Lock()
		defer mu.Unlock()
		captured = append(captured, ee)
	})
	t.Cleanup(errors.ClearErrorHooks)
	count := func() int {
		mu.Lock()
		defer mu.Unlock()
		return len(captured)
	}

	// Positive control: a built error reaches the hook, so a zero count below
	// means nothing was built rather than that hooks cannot fire here.
	_ = errors.Newf("probe").
		Component(probeComponent).
		Category(errors.CategoryValidation).
		Context("operation", probeOperation).
		Build()
	require.Equal(t, 1, count(), "hook must fire for a built error")
	mu.Lock()
	captured = nil
	mu.Unlock()

	mgr := ffmpeg.NewManager(t.Context(), nil, nil, nil, nil)
	t.Cleanup(func() { _ = mgr.Shutdown() })

	state, since := streamRecoveryState(mgr, soundCardSourceID)

	assert.Equal(t, audiocore.RecoveryUnknown, state)
	assert.True(t, since.IsZero())
	assert.Zero(t, count(), "an expected StreamHealth miss must not build a reported error")
}

func TestStreamRecoveryState_PassesThroughProducerIntent(t *testing.T) {
	t.Parallel()

	entered := time.Date(2026, 1, 2, 3, 4, 5, 0, time.UTC)
	lookup := fakeHealthLookup{health: &audiocore.StreamHealth{
		Recovery:        audiocore.RecoveryInProgress,
		RecoveryEntered: entered,
	}}

	state, since := streamRecoveryState(lookup, "any")

	assert.Equal(t, audiocore.RecoveryInProgress, state)
	assert.Equal(t, entered, since)
}
