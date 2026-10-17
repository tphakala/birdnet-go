package spectrogram

import (
	"context"
	"os"
	"path/filepath"
	"sync"
	"sync/atomic"
	"testing"
	"testing/synctest"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/tphakala/birdnet-go/internal/audiocore/transcode"
	"github.com/tphakala/birdnet-go/internal/conf"
)

const (
	mib = 1 << 20
	gib = 1 << 30

	// testWorstCaseWeight is the weight of a render that decodes the largest clip
	// the decoder accepts: twice 128 MiB plus the 8 MiB fixed share.
	testWorstCaseWeight = 264 * mib

	// testHoldTime is how long a simulated render holds its budget.
	testHoldTime = time.Second
)

func TestNativeRenderWeight(t *testing.T) {
	t.Parallel()

	assert.Equal(t, int64(nativeRenderFixedBytes), nativeRenderWeight(0))
	assert.Equal(t, int64(nativeRenderFixedBytes), nativeRenderWeight(-5), "a negative bound is treated as zero")
	assert.Equal(t, int64(2*mib+nativeRenderFixedBytes), nativeRenderWeight(mib))
	assert.Equal(t, int64(testWorstCaseWeight), nativeRenderWeight(transcode.MaxDecodedPCMBytes))
	assert.Equal(t, nativeRenderWeight(transcode.MaxDecodedPCMBytes+2), nativeRenderWeight(1<<62),
		"a bound above the decode cap plus a frame cannot inflate the weight")
}

func TestNativeRenderBudget(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name  string
		total int64
		cpus  int
		want  int64
	}{
		{name: "unknown memory gets the ceiling", total: 0, cpus: 4, want: 4 * testWorstCaseWeight},
		{name: "unknown memory and no cpu count", total: 0, cpus: 0, want: testWorstCaseWeight},
		{name: "512 MiB is lifted to the floor", total: 512 * mib, cpus: 4, want: testWorstCaseWeight},
		{name: "1 GiB is lifted to the floor", total: gib, cpus: 4, want: testWorstCaseWeight},
		{name: "4 GiB is an eighth", total: 4 * gib, cpus: 4, want: 512 * mib},
		{name: "16 GiB with 4 cpus is capped by the ceiling", total: 16 * gib, cpus: 4, want: 4 * testWorstCaseWeight},
		{name: "16 GiB with 16 cpus is an eighth", total: 16 * gib, cpus: 16, want: 2 * gib},
		{name: "16 GiB with 1 cpu is one worst case", total: 16 * gib, cpus: 1, want: testWorstCaseWeight},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			got := nativeRenderBudget(tt.total, tt.cpus)
			assert.Equal(t, tt.want, got)
			assert.GreaterOrEqual(t, got, int64(testWorstCaseWeight), "one worst-case render always fits")
		})
	}
}

func TestAcquireRenderBudget_WeightIsClamped(t *testing.T) {
	t.Parallel()

	const size = 10 * mib
	for _, weight := range []int64{-1, 0, 1, size - 1, size, size + 1, 1 << 62} {
		g := &Generator{}
		g.setRenderBudget(size)
		release, err := g.acquireRenderBudget(t.Context(), weight)
		require.NoError(t, err, "weight %d", weight)
		assert.False(t, g.renderBudget.TryAcquire(size), "weight %d holds part of the budget", weight)
		release()
		assert.True(t, g.renderBudget.TryAcquire(size), "weight %d is released in full", weight)
	}
}

func TestAcquireRenderBudget_ContextCancelWhileWaiting(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		g := &Generator{}
		g.setRenderBudget(testWorstCaseWeight)
		release, err := g.acquireRenderBudget(t.Context(), testWorstCaseWeight)
		require.NoError(t, err)

		ctx, cancel := context.WithTimeout(t.Context(), testHoldTime)
		defer cancel()
		_, err = g.acquireRenderBudget(ctx, testWorstCaseWeight)
		require.ErrorIs(t, err, context.DeadlineExceeded, "a waiter gives up with its context")

		release()
		release2, err := g.acquireRenderBudget(t.Context(), testWorstCaseWeight)
		require.NoError(t, err, "the cancelled waiter left no reservation behind")
		release2()
	})
}

// runBudgeted starts n renders of the given weight against g and returns the
// highest number that held the budget at the same time.
func runBudgeted(t *testing.T, g *Generator, n int, weight int64) int32 {
	t.Helper()
	var running, peak atomic.Int32
	errs := make([]error, n)
	var wg sync.WaitGroup
	for i := range n {
		wg.Go(func() {
			release, err := g.acquireRenderBudget(t.Context(), weight)
			if err != nil {
				errs[i] = err
				return
			}
			defer release()
			cur := running.Add(1)
			for {
				old := peak.Load()
				if cur <= old || peak.CompareAndSwap(old, cur) {
					break
				}
			}
			time.Sleep(testHoldTime)
			running.Add(-1)
		})
	}
	wg.Wait()
	for _, err := range errs {
		require.NoError(t, err)
	}
	return peak.Load()
}

func TestRenderBudget_LargeRendersSerialize(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		g := &Generator{}
		g.setRenderBudget(testWorstCaseWeight)

		assert.Equal(t, int32(1), runBudgeted(t, g, 3, testWorstCaseWeight), "worst-case renders run one at a time")
		assert.Equal(t, int32(8), runBudgeted(t, g, 8, nativeRenderWeight(3*mib)), "ordinary clips run together")
	})
}

// writeBudgetClip writes a short WAV to dir and returns its path.
func writeBudgetClip(t *testing.T, dir string) string {
	t.Helper()
	return writeNativeWAV(t, dir, "clip.wav", nativeTestSamples(nativeCodecRate), nativeCodecRate)
}

func TestGenerateNativeFromFile_HoldsTheBudgetWhileRendering(t *testing.T) {
	t.Setenv(conf.EnvNativeSpectrogramRenderer, "native")
	synctest.Test(t, func(t *testing.T) {
		g, env := newNativeGenerator(t)
		g.setRenderBudget(testWorstCaseWeight)
		clip := writeBudgetClip(t, env.TempDir)
		temp := filepath.Join(env.TempDir, "out.png.temp")

		// Another render holds the whole budget: this one must wait for it.
		require.NoError(t, g.renderBudget.Acquire(t.Context(), testWorstCaseWeight))
		result := make(chan error, 1)
		go func() {
			result <- g.generateNativeFromFile(t.Context(), env.Settings, clip, temp, nativeTestWidth, false, BirdProfile(), nil)
		}()
		synctest.Wait()
		select {
		case err := <-result:
			require.FailNow(t, "the render ran without a share of the budget", "err: %v", err)
		default:
		}

		g.renderBudget.Release(testWorstCaseWeight)
		require.NoError(t, <-result)
		assert.True(t, g.renderBudget.TryAcquire(testWorstCaseWeight), "the render released its share")
	})
}

func TestGenerateNativeFromFile_ReleasesTheBudgetWhenDecodeFails(t *testing.T) {
	t.Setenv(conf.EnvNativeSpectrogramRenderer, "native")
	g, env := newNativeGenerator(t)
	g.setRenderBudget(testWorstCaseWeight)

	// A header that declares the whole clip over half the data passes the header
	// probe, so the render takes its share and then fails in the decode.
	clip := writeBudgetClip(t, env.TempDir)
	data, err := os.ReadFile(clip) //nolint:gosec // G304: test path
	require.NoError(t, err)
	require.NoError(t, os.WriteFile(clip, data[:len(data)/2], 0o600))

	err = g.generateNativeFromFile(t.Context(), env.Settings, clip, filepath.Join(env.TempDir, "out.png.temp"), nativeTestWidth, false, BirdProfile(), nil)
	require.ErrorIs(t, err, transcode.ErrTruncatedSource)
	assert.True(t, g.renderBudget.TryAcquire(testWorstCaseWeight), "the failed render released its share")
}
