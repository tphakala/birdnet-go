//go:build linux

package spectrogram

import (
	"os"
	"path/filepath"
	"strconv"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

const (
	// priorityClipSeconds makes the render last long enough for the polling below
	// to see its thread: a clip of this length takes between half a second and a second.
	priorityClipSeconds = 600

	// priorityPollInterval is the pause between scans of the process's threads.
	priorityPollInterval = time.Millisecond
)

// threadsAtRenderNice returns the ids of the threads of this process that are at
// the render nice value now.
func threadsAtRenderNice() (map[int]bool, error) {
	entries, err := os.ReadDir(procSelfTaskDir)
	if err != nil {
		return nil, err
	}
	out := make(map[int]bool)
	for _, e := range entries {
		tid, err := strconv.Atoi(e.Name())
		if err != nil {
			return nil, err
		}
		nice, alive, err := threadNice(tid)
		if err != nil {
			return nil, err
		}
		if alive && nice == nativeRenderNice {
			out[tid] = true
		}
	}
	return out, nil
}

// requireRenderRunsAtLowPriority runs render on its own goroutine and scans the
// process's threads until it returns, failing when no thread that was not at the
// render nice value before the render started reached it. A thread left over
// from an earlier render is therefore not counted.
func requireRenderRunsAtLowPriority(t *testing.T, render func() error) {
	t.Helper()
	if baselinePriority(t) == rawNiceOffset-nativeRenderNice {
		t.Skip("the test process already runs at the render priority")
	}
	before, err := threadsAtRenderNice()
	require.NoError(t, err)
	done := make(chan error, 1)
	go func() { done <- render() }()

	seen := false
	for finished := false; !finished; {
		select {
		case err := <-done:
			require.NoError(t, err)
			finished = true
		default:
			if !seen {
				now, err := threadsAtRenderNice()
				require.NoError(t, err)
				for tid := range now {
					seen = seen || !before[tid]
				}
			}
			time.Sleep(priorityPollInterval)
		}
	}
	assert.True(t, seen, "the render never ran on a new thread at nice %d", nativeRenderNice)
}

func TestNativeRenders_RunAtLowPriority(t *testing.T) {
	g, env := newNativeGenerator(t)
	samples := toneSamples(nativeCodecRate, priorityClipSeconds, 2000)
	temp := filepath.Join(env.TempDir, "out.png.temp")

	t.Run("file", func(t *testing.T) {
		clip := writeNativeWAV(t, env.TempDir, "long.wav", samples, nativeCodecRate)
		requireRenderRunsAtLowPriority(t, func() error {
			return g.generateNativeFromFile(t.Context(), env.Settings, clip, temp, nativeTestWidth, false, BirdProfile(), nil)
		})
	})
	t.Run("pcm", func(t *testing.T) {
		pcm := samplesToPCM(samples)
		requireRenderRunsAtLowPriority(t, func() error {
			return g.generateNativeFromPCM(t.Context(), env.Settings, pcm, temp, nativeTestWidth, false, nativeCodecRate, BirdProfile())
		})
	})
}
