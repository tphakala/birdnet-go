package ttlcache

import (
	"context"
	"fmt"
	"math/rand/v2"
	"runtime"
	"sync"
	"sync/atomic"
	"testing"
	"testing/synctest"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/tphakala/birdnet-go/internal/errors"
	"github.com/tphakala/birdnet-go/internal/testutil"
)

const testTTL = time.Minute

// fakeClock is a manually advanced clock.
type fakeClock struct {
	mu sync.Mutex
	t  time.Time
}

func newFakeClock() *fakeClock {
	return &fakeClock{t: time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)}
}

func (f *fakeClock) Now() time.Time {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.t
}

func (f *fakeClock) Advance(d time.Duration) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.t = f.t.Add(d)
}

func newTestCache(ttl time.Duration, opts ...Option) (*Cache[string, int], *fakeClock) {
	clk := newFakeClock()
	all := append([]Option{WithClock(clk.Now)}, opts...)
	return New[string, int](ttl, all...), clk
}

// constLoader returns a loader that counts calls and returns v.
func constLoader(calls *atomic.Int32, v int) func(context.Context) (int, error) {
	return func(context.Context) (int, error) {
		calls.Add(1)
		return v, nil
	}
}

func TestGet_MissThenHitAfterSet(t *testing.T) {
	t.Parallel()
	c, _ := newTestCache(testTTL)

	_, ok := c.Get("a")
	assert.False(t, ok)
	c.Set("a", 1)
	v, ok := c.Get("a")
	assert.True(t, ok)
	assert.Equal(t, 1, v)
}

func TestSet_OverwriteReplacesValueAndRestartsTTL(t *testing.T) {
	t.Parallel()
	c, clk := newTestCache(testTTL)

	c.Set("a", 1)
	clk.Advance(testTTL / 2)
	c.Set("a", 2)
	clk.Advance(testTTL - 1)
	v, ok := c.Get("a")
	require.True(t, ok)
	assert.Equal(t, 2, v)
	clk.Advance(testTTL/2 + 1)
	_, ok = c.Get("a")
	assert.False(t, ok)
}

func TestGet_ExpiryBoundary(t *testing.T) {
	t.Parallel()
	c, clk := newTestCache(testTTL)

	c.Set("a", 1)
	clk.Advance(testTTL - time.Nanosecond)
	_, ok := c.Get("a")
	assert.True(t, ok, "one nanosecond before expiry")
	clk.Advance(time.Nanosecond)
	_, ok = c.Get("a")
	assert.False(t, ok, "exactly at expiry")
	assert.Empty(t, c.entries, "expired entry is removed by the read")
	assert.Equal(t, 0, c.Len())
}

func TestNew_NonPositiveTTLNeverExpires(t *testing.T) {
	t.Parallel()
	for _, ttl := range []time.Duration{0, -time.Second} {
		c, clk := newTestCache(ttl)
		c.Set("a", 1)
		clk.Advance(1000 * time.Hour)
		v, ok := c.Get("a")
		assert.True(t, ok, "ttl %v", ttl)
		assert.Equal(t, 1, v)
		assert.Equal(t, 1, c.Len())
	}
}

func TestDelete_RemovesOnlyThatKey(t *testing.T) {
	t.Parallel()
	c, _ := newTestCache(testTTL)
	c.Set("a", 1)
	c.Set("b", 2)
	c.Delete("a")
	_, ok := c.Get("a")
	assert.False(t, ok)
	v, ok := c.Get("b")
	assert.True(t, ok)
	assert.Equal(t, 2, v)
	assert.Equal(t, 1, c.Len())
}

func TestClear_RemovesAllEntries(t *testing.T) {
	t.Parallel()
	c, _ := newTestCache(testTTL)
	c.Set("a", 1)
	c.Set("b", 2)
	c.Clear()
	assert.Equal(t, 0, c.Len())
	_, ok := c.Get("a")
	assert.False(t, ok)
	// The list must be usable after a clear.
	c.Set("c", 3)
	assert.Equal(t, 1, c.Len())
}

func TestStats_OneCounterPerCall(t *testing.T) {
	t.Parallel()
	c, _ := newTestCache(testTTL)
	var calls atomic.Int32
	ctx := t.Context()

	steps := []struct {
		name string
		op   func()
		want Stats
	}{
		{"get miss", func() { c.Get("a") }, Stats{Misses: 1}},
		{"getorload miss runs loader", func() { _, _ = c.GetOrLoad(ctx, "a", constLoader(&calls, 1)) }, Stats{Misses: 2}},
		{"get hit", func() { c.Get("a") }, Stats{Hits: 1, Misses: 2}},
		{"getorload hit", func() { _, _ = c.GetOrLoad(ctx, "a", constLoader(&calls, 1)) }, Stats{Hits: 2, Misses: 2}},
		{"set does not count", func() { c.Set("b", 1) }, Stats{Hits: 2, Misses: 2}},
		{"len does not count", func() { c.Len() }, Stats{Hits: 2, Misses: 2}},
	}
	for _, s := range steps {
		s.op()
		assert.Equal(t, s.want, c.Stats(), s.name)
	}
	assert.Equal(t, int32(1), calls.Load(), "second GetOrLoad was a hit")
}

func TestMaxEntries_EvictsOldestWriteAndCountsEviction(t *testing.T) {
	t.Parallel()
	c, _ := newTestCache(testTTL, WithMaxEntries(2))
	c.Set("a", 1)
	c.Set("b", 2)
	c.Set("c", 3)

	assert.Len(t, c.entries, 2)
	_, ok := c.Get("a")
	assert.False(t, ok, "oldest write evicted")
	_, ok = c.Get("b")
	assert.True(t, ok)
	_, ok = c.Get("c")
	assert.True(t, ok)
	assert.Equal(t, uint64(1), c.Stats().Evictions)
}

func TestMaxEntries_OverwriteMovesEntryToNewest(t *testing.T) {
	t.Parallel()
	c, _ := newTestCache(testTTL, WithMaxEntries(2))
	c.Set("a", 1)
	c.Set("b", 2)
	c.Set("a", 10) // a is now the newest write
	c.Set("c", 3)  // evicts b

	_, ok := c.Get("b")
	assert.False(t, ok)
	v, ok := c.Get("a")
	assert.True(t, ok)
	assert.Equal(t, 10, v)
}

func TestMaxEntries_SweepsExpiredBeforeEvicting(t *testing.T) {
	t.Parallel()
	c, clk := newTestCache(testTTL, WithMaxEntries(2))
	c.Set("a", 1)
	c.Set("b", 2)
	clk.Advance(testTTL)
	c.Set("c", 3)

	assert.Len(t, c.entries, 1)
	assert.Equal(t, uint64(0), c.Stats().Evictions, "expired removals are not evictions")
}

func TestMaxEntries_ExpiredHeadRemovedWithoutEviction(t *testing.T) {
	t.Parallel()
	const (
		expired = maxSweepPerWrite + 8
		bound   = 5
	)
	c, clk := newTestCache(testTTL)
	for i := range expired {
		c.Set(fmt.Sprintf("old%d", i), i)
	}
	// With a public bound the backlog of expired entries can never outgrow one
	// sweep, so tighten the bound after filling to reach the branch.
	c.maxEntries = bound
	clk.Advance(testTTL)
	c.Set("fresh", 1)

	assert.Len(t, c.entries, bound, "the bound trimmed the expired backlog the sweep left")
	assert.Equal(t, uint64(0), c.Stats().Evictions, "only expired entries were removed")
	_, ok := c.Get("fresh")
	assert.True(t, ok)
}

func TestSet_SweepIsBoundedPerWrite(t *testing.T) {
	t.Parallel()
	const total = 100
	c, clk := newTestCache(testTTL)
	for i := range total {
		c.Set(fmt.Sprintf("k%d", i), i)
	}
	clk.Advance(testTTL)
	c.Set("fresh", 0)
	assert.Len(t, c.entries, total-maxSweepPerWrite+1)

	for range total {
		c.Set("fresh", 0)
	}
	assert.Len(t, c.entries, 1, "repeated writes drain the backlog")
}

func TestGetOrLoad_ConcurrentMissesRunOneLoader(t *testing.T) {
	t.Parallel()
	synctest.Test(t, func(t *testing.T) {
		const callers = 50
		c := New[string, int](testTTL)
		release := make(chan struct{})
		var calls atomic.Int32
		loader := func(context.Context) (int, error) {
			calls.Add(1)
			<-release
			return 7, nil
		}

		results := make([]int, callers)
		var wg sync.WaitGroup
		for i := range callers {
			wg.Go(func() {
				v, err := c.GetOrLoad(t.Context(), "k", loader)
				if err == nil {
					results[i] = v
				}
			})
		}
		synctest.Wait()
		close(release)
		wg.Wait()

		assert.Equal(t, int32(1), calls.Load())
		for i, v := range results {
			assert.Equal(t, 7, v, "caller %d", i)
		}
		assert.Equal(t, uint64(callers), c.Stats().Misses)
		assert.Equal(t, uint64(0), c.Stats().Hits)
	})
}

func TestGetOrLoad_CallerAfterFlightFinishesDoesNotReload(t *testing.T) {
	t.Parallel()
	c, _ := newTestCache(testTTL)
	var calls atomic.Int32
	ctx := t.Context()

	v, err := c.GetOrLoad(ctx, "k", constLoader(&calls, 5))
	require.NoError(t, err)
	assert.Equal(t, 5, v)

	v, err = c.GetOrLoad(ctx, "k", constLoader(&calls, 6))
	require.NoError(t, err)
	assert.Equal(t, 5, v, "the finished flight's value is served from the cache")
	assert.Equal(t, int32(1), calls.Load())
	assert.Equal(t, Stats{Hits: 1, Misses: 1}, c.Stats())
	c.mu.Lock()
	assert.Empty(t, c.flights, "a finished flight is unregistered")
	c.mu.Unlock()
}

func TestGetOrLoad_LoaderErrorNotCached(t *testing.T) {
	t.Parallel()
	c, _ := newTestCache(testTTL)
	sentinel := errors.NewStd("boom")
	var calls atomic.Int32
	failing := func(context.Context) (int, error) {
		calls.Add(1)
		return 0, sentinel
	}

	_, err := c.GetOrLoad(t.Context(), "k", failing)
	require.ErrorIs(t, err, sentinel)
	assert.Same(t, sentinel, err, "error is returned unwrapped")
	_, err = c.GetOrLoad(t.Context(), "k", failing)
	require.ErrorIs(t, err, sentinel)
	assert.Equal(t, int32(2), calls.Load())
	assert.Equal(t, 0, c.Len())
}

func TestGetOrLoad_ClearDuringLoadDiscardsResult(t *testing.T) {
	t.Parallel()
	synctest.Test(t, func(t *testing.T) {
		c := New[string, int](testTTL)
		release := make(chan struct{})
		done := make(chan int, 1)
		go func() {
			v, _ := c.GetOrLoad(t.Context(), "k", func(context.Context) (int, error) {
				<-release
				return 9, nil
			})
			done <- v
		}()
		synctest.Wait()
		c.Clear()
		close(release)
		assert.Equal(t, 9, <-done, "the caller still receives the value")
		_, ok := c.Get("k")
		assert.False(t, ok)
	})
}

func TestGetOrLoad_CallerAfterClearStartsNewLoad(t *testing.T) {
	t.Parallel()
	synctest.Test(t, func(t *testing.T) {
		c := New[string, int](testTTL)
		releaseA := make(chan struct{})
		releaseB := make(chan struct{})
		var calls atomic.Int32
		doneA := make(chan int, 1)
		doneB := make(chan int, 1)

		go func() {
			v, _ := c.GetOrLoad(t.Context(), "k", func(context.Context) (int, error) {
				calls.Add(1)
				<-releaseA
				return 1, nil
			})
			doneA <- v
		}()
		synctest.Wait()
		c.Clear()
		go func() {
			v, _ := c.GetOrLoad(t.Context(), "k", func(context.Context) (int, error) {
				calls.Add(1)
				<-releaseB
				return 2, nil
			})
			doneB <- v
		}()
		synctest.Wait()
		assert.Equal(t, int32(2), calls.Load(), "the post-Clear caller did not join the old flight")

		close(releaseB)
		assert.Equal(t, 2, <-doneB)
		close(releaseA)
		assert.Equal(t, 1, <-doneA)
		synctest.Wait()

		v, ok := c.Get("k")
		require.True(t, ok)
		assert.Equal(t, 2, v, "the pre-Clear load did not overwrite the newer value")
	})
}

func TestGetOrLoad_SetAndDeleteDuringLoadWin(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name   string
		mutate func(c *Cache[string, int])
		want   int
		wantOK bool
	}{
		{"set wins", func(c *Cache[string, int]) { c.Set("k", 100) }, 100, true},
		{"delete wins", func(c *Cache[string, int]) { c.Delete("k") }, 0, false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			synctest.Test(t, func(t *testing.T) {
				c := New[string, int](testTTL)
				release := make(chan struct{})
				done := make(chan struct{})
				go func() {
					defer close(done)
					_, _ = c.GetOrLoad(t.Context(), "k", func(context.Context) (int, error) {
						<-release
						return 1, nil
					})
				}()
				synctest.Wait()
				tt.mutate(c)
				close(release)
				<-done
				v, ok := c.Get("k")
				assert.Equal(t, tt.wantOK, ok)
				assert.Equal(t, tt.want, v)
			})
		})
	}
}

func TestDelete_AbsentKeyDiscardsInFlightLoad(t *testing.T) {
	t.Parallel()
	synctest.Test(t, func(t *testing.T) {
		c := New[string, int](testTTL)
		release := make(chan struct{})
		done := make(chan struct{})
		go func() {
			defer close(done)
			_, _ = c.GetOrLoad(t.Context(), "k", func(context.Context) (int, error) {
				<-release
				return 1, nil
			})
		}()
		synctest.Wait()
		c.Delete("k") // the key is absent
		close(release)
		<-done
		_, ok := c.Get("k")
		assert.False(t, ok)
	})
}

func TestGetOrLoad_WaiterCancellationDoesNotCancelLoad(t *testing.T) {
	t.Parallel()
	synctest.Test(t, func(t *testing.T) {
		c := New[string, int](testTTL)
		release := make(chan struct{})
		var loaderErr atomic.Value
		loader := func(ctx context.Context) (int, error) {
			<-release
			loaderErr.Store(fmt.Sprint(ctx.Err()))
			return 3, nil
		}

		ctxA, cancelA := context.WithCancel(t.Context())
		errA := make(chan error, 1)
		go func() {
			_, err := c.GetOrLoad(ctxA, "k", loader)
			errA <- err
		}()
		synctest.Wait()
		cancelA()
		require.ErrorIs(t, <-errA, context.Canceled)

		close(release)
		synctest.Wait()
		assert.Equal(t, "<nil>", loaderErr.Load(), "the loader context was not cancelled")
		v, ok := c.Get("k")
		require.True(t, ok, "the abandoned load still filled the cache")
		assert.Equal(t, 3, v)
	})
}

func TestGetOrLoad_CancelledContextSkipsLoad(t *testing.T) {
	t.Parallel()
	c, _ := newTestCache(testTTL)
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	var calls atomic.Int32
	_, err := c.GetOrLoad(ctx, "k", constLoader(&calls, 1))
	require.ErrorIs(t, err, context.Canceled)
	assert.Equal(t, int32(0), calls.Load())
}

func TestGetOrLoad_LoaderPanicBecomesPanicError(t *testing.T) {
	t.Parallel()
	synctest.Test(t, func(t *testing.T) {
		c := New[string, int](testTTL)
		release := make(chan struct{})
		var calls atomic.Int32
		loader := func(context.Context) (int, error) {
			calls.Add(1)
			<-release
			panic("kaboom")
		}
		errs := make(chan error, 2)
		for range 2 {
			go func() {
				_, err := c.GetOrLoad(t.Context(), "k", loader)
				errs <- err
			}()
		}
		synctest.Wait()
		close(release)
		for range 2 {
			err := <-errs
			pe, ok := errors.AsType[*PanicError](err)
			require.True(t, ok, "got %v", err)
			assert.Equal(t, "kaboom", pe.Value)
			assert.NotEmpty(t, pe.Stack)
			require.ErrorIs(t, err, ErrLoaderPanicked)
			assert.Equal(t, "ttlcache: loader panicked: kaboom", err.Error())
		}
		assert.Equal(t, int32(1), calls.Load(), "a panicking loader runs once for joined callers")
		assert.Equal(t, 0, c.Len())
	})
}

func TestGetOrLoad_JoinedCallersShareLoaderError(t *testing.T) {
	t.Parallel()
	synctest.Test(t, func(t *testing.T) {
		c := New[string, int](testTTL)
		release := make(chan struct{})
		var calls atomic.Int32
		sentinel := errors.NewStd("fail")
		loader := func(context.Context) (int, error) {
			calls.Add(1)
			<-release
			return 0, sentinel
		}
		errs := make(chan error, 2)
		for range 2 {
			go func() {
				_, err := c.GetOrLoad(t.Context(), "k", loader)
				errs <- err
			}()
		}
		synctest.Wait()
		close(release)
		for range 2 {
			require.ErrorIs(t, <-errs, sentinel)
		}
		assert.Equal(t, int32(1), calls.Load())
		assert.Equal(t, 0, c.Len())
	})
}

func TestGetOrLoad_LoaderGoexitReleasesCallers(t *testing.T) {
	t.Parallel()
	synctest.Test(t, func(t *testing.T) {
		c := New[string, int](testTTL)
		release := make(chan struct{})
		loader := func(context.Context) (int, error) {
			<-release
			runtime.Goexit()
			return 0, nil
		}
		errs := make(chan error, 2)
		for range 2 {
			go func() {
				_, err := c.GetOrLoad(t.Context(), "k", loader)
				errs <- err
			}()
		}
		synctest.Wait()
		close(release)
		for range 2 {
			require.ErrorIs(t, <-errs, ErrLoaderExited)
		}
		assert.Equal(t, 0, c.Len())

		// The flight is unregistered, so the next caller loads again.
		v, err := c.GetOrLoad(t.Context(), "k", func(context.Context) (int, error) { return 7, nil })
		require.NoError(t, err)
		assert.Equal(t, 7, v)
	})
}

// modelEntry is the reference model's view of one key.
type modelEntry struct {
	value int
	exp   time.Time
}

func TestCache_ModelSequences(t *testing.T) {
	t.Parallel()
	const (
		seeds    = 20
		steps    = 400
		keySpace = 12
	)
	for _, maxEntries := range []int{0, 5} {
		for seed := range uint64(seeds) {
			t.Run(fmt.Sprintf("max%d/seed%d", maxEntries, seed), func(t *testing.T) {
				t.Parallel()
				rng := rand.New(rand.NewPCG(seed, seed+1))
				clk := newFakeClock()
				c := New[string, int](testTTL, WithClock(clk.Now), WithMaxEntries(maxEntries))
				model := map[string]modelEntry{}
				ctx := t.Context()

				for step := range steps {
					key := fmt.Sprintf("k%d", rng.IntN(keySpace))
					val := rng.IntN(1000)
					switch rng.IntN(7) {
					case 0:
						c.Set(key, val)
						model[key] = modelEntry{val, clk.Now().Add(testTTL)}
					case 1:
						c.Delete(key)
						delete(model, key)
					case 2:
						if rng.IntN(10) == 0 {
							c.Clear()
							clear(model)
						}
					case 3:
						clk.Advance(time.Duration(rng.IntN(int(testTTL / 2))))
					case 4:
						v, err := c.GetOrLoad(ctx, key, func(context.Context) (int, error) { return val, nil })
						require.NoError(t, err)
						if m, ok := model[key]; ok && clk.Now().Before(m.exp) {
							// A live model entry may have been evicted, in which
							// case the loader value is returned instead.
							assert.Contains(t, []int{m.value, val}, v, "step %d", step)
							if v == val {
								model[key] = modelEntry{val, clk.Now().Add(testTTL)}
							}
						} else {
							assert.Equal(t, val, v, "step %d", step)
							model[key] = modelEntry{val, clk.Now().Add(testTTL)}
						}
					default:
						got, ok := c.Get(key)
						m, inModel := model[key]
						live := inModel && clk.Now().Before(m.exp)
						switch {
						case !live:
							assert.False(t, ok, "step %d: expired or removed key returned", step)
						case ok:
							assert.Equal(t, m.value, got, "step %d", step)
						default:
							assert.NotZero(t, maxEntries, "step %d: only a bounded cache may lose a live key", step)
							delete(model, key)
						}
					}
					if maxEntries > 0 {
						assert.LessOrEqual(t, c.Len(), maxEntries, "step %d", step)
					}
				}
			})
		}
	}
}

func TestCache_ConcurrentMixedAccess(t *testing.T) {
	t.Parallel()
	const (
		workers  = 8
		ops      = 2000
		keySpace = 16
		maxSize  = 6
	)
	valueFor := func(k int) int { return k*31 + 7 }
	clk := newFakeClock()
	c := New[int, int](testTTL, WithClock(clk.Now), WithMaxEntries(maxSize))
	ctx := t.Context()

	// Workers record invariant violations here; the test asserts on its own
	// goroutine after the join.
	var mu sync.Mutex
	var failures []string
	fail := func(format string, args ...any) {
		mu.Lock()
		defer mu.Unlock()
		failures = append(failures, fmt.Sprintf(format, args...))
	}

	var wg sync.WaitGroup
	for w := range workers {
		wg.Go(func() {
			rng := rand.New(rand.NewPCG(uint64(w), 99))
			for range ops {
				k := rng.IntN(keySpace)
				switch rng.IntN(8) {
				case 0:
					c.Set(k, valueFor(k))
				case 1:
					c.Delete(k)
				case 2:
					if rng.IntN(20) == 0 {
						c.Clear()
					}
				case 3:
					clk.Advance(time.Duration(rng.IntN(int(testTTL / 4))))
				case 4:
					v, err := c.GetOrLoad(ctx, k, func(context.Context) (int, error) { return valueFor(k), nil })
					if err != nil {
						fail("GetOrLoad(%d): %v", k, err)
						return
					}
					if v != valueFor(k) {
						fail("GetOrLoad(%d) = %d, want %d", k, v, valueFor(k))
					}
				case 5:
					if n := c.Len(); n > maxSize {
						fail("Len() = %d, want at most %d", n, maxSize)
					}
				default:
					if v, ok := c.Get(k); ok && v != valueFor(k) {
						fail("Get(%d) = %d, want %d", k, v, valueFor(k))
					}
				}
			}
		})
	}
	wg.Wait()

	assert.Empty(t, failures)
	assert.LessOrEqual(t, c.Len(), maxSize)
}

func TestLen_SweepsExpiredEntriesWithoutARead(t *testing.T) {
	t.Parallel()
	// More entries than one write sweeps, so only a full sweep empties the cache.
	const entries = maxSweepPerWrite + 8
	c, clk := newTestCache(testTTL)
	for i := range entries {
		c.Set(fmt.Sprintf("k%d", i), i)
	}
	assert.Equal(t, entries, c.Len())

	clk.Advance(testTTL)
	// No Get touched the entries, so only Len's own sweep can drop them.
	assert.Equal(t, 0, c.Len())
	assert.Empty(t, c.entries)
}

func TestCache_NoGoroutineLeaks(t *testing.T) {
	testutil.VerifyNoLeaks(t)

	for range 3 {
		c := New[string, int](testTTL, WithMaxEntries(4))
		for i := range 10 {
			_, err := c.GetOrLoad(t.Context(), fmt.Sprintf("k%d", i), func(context.Context) (int, error) { return i, nil })
			require.NoError(t, err)
		}
		c.Clear()
	}
}

// largeKey has the size of the detection page key, large enough that copying
// it to the heap would show up as an allocation.
type largeKey struct {
	a, b, c, d, e, f, g, h, i, j string
	n, m                         int
}

func loadLargeKeyValue(context.Context) (int, error) { return 1, nil }

func TestGetOrLoad_HitDoesNotAllocate(t *testing.T) {
	c := New[largeKey, int](testTTL)
	ctx := t.Context()
	key := largeKey{a: "hourly", n: 25}
	_, err := c.GetOrLoad(ctx, key, loadLargeKeyValue)
	require.NoError(t, err)

	allocs := testing.AllocsPerRun(100, func() {
		_, _ = c.GetOrLoad(ctx, key, loadLargeKeyValue)
	})
	assert.Zero(t, allocs, "a hit must not copy the key or start a flight")
}

// TestCache_PanicUnderLockReleasesMutex pins that a panic raised while the
// cache lock is held (an uncomparable dynamic key, a panicking clock) unwinds
// with the lock released, so a caller that recovers can keep using the cache.
func TestCache_PanicUnderLockReleasesMutex(t *testing.T) {
	t.Parallel()
	uncomparable := []int{1}

	t.Run("uncomparable key", func(t *testing.T) {
		t.Parallel()
		c := New[any, int](testTTL)
		ops := map[string]func(){
			"Get":    func() { c.Get(uncomparable) },
			"Set":    func() { c.Set(uncomparable, 1) },
			"Delete": func() { c.Delete(uncomparable) },
			"GetOrLoad": func() {
				_, _ = c.GetOrLoad(t.Context(), uncomparable, func(context.Context) (int, error) { return 1, nil })
			},
		}
		for name, op := range ops {
			assert.Panics(t, op, name)
			require.True(t, c.mu.TryLock(), "%s left the cache locked", name)
			c.mu.Unlock()
		}
	})

	t.Run("panicking clock", func(t *testing.T) {
		t.Parallel()
		var panicNow atomic.Bool
		clock := func() time.Time {
			if panicNow.Load() {
				panic("clock failed")
			}
			return time.Unix(0, 0)
		}
		c := New[string, int](testTTL, WithClock(clock))
		panicNow.Store(true)
		ops := map[string]func(){
			"Get": func() { c.Get("k") },
			"Set": func() { c.Set("k", 1) },
			"Len": func() { c.Len() },
		}
		for name, op := range ops {
			assert.Panics(t, op, name)
			require.True(t, c.mu.TryLock(), "%s left the cache locked", name)
			c.mu.Unlock()
		}
	})
}

// TestGetOrLoad_PanickingClockInStoreReleasesCallers pins that a panic while a
// finished load stores its result still releases the waiting callers and the
// lock.
func TestGetOrLoad_PanickingClockInStoreReleasesCallers(t *testing.T) {
	t.Parallel()
	synctest.Test(t, func(t *testing.T) {
		var panicNow atomic.Bool
		clock := func() time.Time {
			if panicNow.Load() {
				panic("clock failed")
			}
			return time.Unix(0, 0)
		}
		c := New[string, int](testTTL, WithClock(clock))
		release := make(chan struct{})
		errs := make(chan error, 1)
		go func() {
			_, err := c.GetOrLoad(t.Context(), "k", func(context.Context) (int, error) {
				<-release
				panicNow.Store(true)
				return 1, nil
			})
			errs <- err
		}()
		synctest.Wait()
		close(release)
		_, isPanic := errors.AsType[*PanicError](<-errs)
		assert.True(t, isPanic)
		require.True(t, c.mu.TryLock(), "the store left the cache locked")
		c.mu.Unlock()
	})
}
