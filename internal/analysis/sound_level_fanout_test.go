package analysis

import (
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/tphakala/birdnet-go/internal/audiocore/soundlevel"
)

const (
	fanOutTestPublishers = 3
	fanOutTestIntervals  = 6
	fanOutTestTimeout    = 5 * time.Second
)

func newFanOutTestChans(n int) []chan soundlevel.SoundLevelData {
	chans := make([]chan soundlevel.SoundLevelData, n)
	for i := range chans {
		chans[i] = make(chan soundlevel.SoundLevelData, soundLevelPublisherChanSize)
	}
	return chans
}

// TestFanOutSoundLevel_EveryPublisherSeesEveryInterval pins the fix for the
// publishers competing for one channel: each destination receives all values.
func TestFanOutSoundLevel_EveryPublisherSeesEveryInterval(t *testing.T) {
	t.Parallel()

	var wg sync.WaitGroup
	done := make(chan struct{})
	src := make(chan soundlevel.SoundLevelData, fanOutTestIntervals)
	dsts := newFanOutTestChans(fanOutTestPublishers)

	fanOutSoundLevel(&wg, done, src, dsts...)
	for i := range fanOutTestIntervals {
		src <- soundlevel.SoundLevelData{Duration: i + 1}
	}

	for p, dst := range dsts {
		for i := range fanOutTestIntervals {
			select {
			case got := <-dst:
				assert.Equal(t, i+1, got.Duration, "publisher %d interval %d", p, i)
			case <-time.After(fanOutTestTimeout):
				require.Failf(t, "timeout", "publisher %d missed interval %d", p, i)
			}
		}
	}

	close(done)
	wg.Wait()
	for _, dst := range dsts {
		_, ok := <-dst
		assert.False(t, ok, "destination must be closed after the fan-out stops")
	}
}

// TestFanOutSoundLevel_SlowPublisherDoesNotBlockOthers pins that a full
// destination drops its own values and the other destinations keep receiving.
func TestFanOutSoundLevel_SlowPublisherDoesNotBlockOthers(t *testing.T) {
	t.Parallel()

	var wg sync.WaitGroup
	done := make(chan struct{})
	src := make(chan soundlevel.SoundLevelData)
	stuck := make(chan soundlevel.SoundLevelData) // unbuffered and never read
	healthy := make(chan soundlevel.SoundLevelData, fanOutTestIntervals)

	fanOutSoundLevel(&wg, done, src, stuck, healthy)
	for i := range fanOutTestIntervals {
		select {
		case src <- soundlevel.SoundLevelData{Duration: i + 1}:
		case <-time.After(fanOutTestTimeout):
			require.Failf(t, "timeout", "fan-out stopped reading the source at interval %d", i)
		}
	}

	assert.Len(t, healthy, fanOutTestIntervals)

	close(done)
	wg.Wait()
}

// TestFanOutSoundLevel_SourceCloseStopsAndClosesDestinations pins shutdown on a
// closed source.
func TestFanOutSoundLevel_SourceCloseStopsAndClosesDestinations(t *testing.T) {
	t.Parallel()

	var wg sync.WaitGroup
	done := make(chan struct{})
	defer close(done)
	src := make(chan soundlevel.SoundLevelData)
	dsts := newFanOutTestChans(2)

	fanOutSoundLevel(&wg, done, src, dsts...)
	close(src)
	wg.Wait()

	for _, dst := range dsts {
		_, ok := <-dst
		assert.False(t, ok)
	}
}
