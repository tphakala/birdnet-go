package analysis

import (
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/tphakala/birdnet-go/internal/audiocore/soundlevel"
)

// waitFanOut waits for the fan-out goroutines, failing the test instead of
// hanging when they do not stop.
func waitFanOut(t *testing.T, wg *sync.WaitGroup) {
	t.Helper()
	stopped := make(chan struct{})
	go func() {
		wg.Wait()
		close(stopped)
	}()
	select {
	case <-stopped:
	case <-time.After(fanOutTestTimeout):
		require.Fail(t, "fan-out did not stop")
	}
}

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
	waitFanOut(t, &wg)
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

	// The source send returns before the fan-out has delivered that value, so
	// receive with a timeout instead of inspecting the channel length.
	for i := range fanOutTestIntervals {
		select {
		case got := <-healthy:
			assert.Equal(t, i+1, got.Duration)
		case <-time.After(fanOutTestTimeout):
			require.Failf(t, "timeout", "healthy publisher missed interval %d", i)
		}
	}

	close(done)
	waitFanOut(t, &wg)
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
	waitFanOut(t, &wg)

	for _, dst := range dsts {
		_, ok := <-dst
		assert.False(t, ok)
	}
}
