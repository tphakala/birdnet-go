// ratelimit_store_test.go: tests for the capped rate limiter store.

package apicore

import (
	"fmt"
	"sync"
	"testing"
	"testing/synctest"
	"time"

	"github.com/labstack/echo/v4/middleware"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"golang.org/x/time/rate"
)

const (
	// storeTestBurst is the burst of the test store; with a negligible refill rate
	// an identifier is throttled after this many calls.
	storeTestBurst = 2
	// storeTestRate refills far slower than any test runs, so a throttled
	// identifier stays throttled unless the store forgets it.
	storeTestRate rate.Limit = 1.0 / (24 * 60 * 60)
	// storeTestExpiresIn is long enough that idle cleanup never fires unless a
	// test sleeps past it.
	storeTestExpiresIn = time.Hour
	// storeTestNamespaceCap is the per-namespace cap in the namespace test.
	storeTestNamespaceCap = 2
	// storeTestStoreCap is the global cap in the global cap test.
	storeTestStoreCap = 4
	// storeTestRoomyCap is a cap that a test never reaches.
	storeTestRoomyCap = 100
	// storeTestPeer is the namespace used by the implicit-proxy style keys.
	storeTestPeer = "192.0.2.1"
	// storeTestSecondPeer is a second namespace for the overflow expiry test.
	storeTestSecondPeer = "192.0.2.2"
	// storeTestThirdPeer is a third namespace for the overflow bucket cap test.
	storeTestThirdPeer = "192.0.2.3"
	// storeTestOverflowCap is the store cap, and with it the cap on overflow
	// buckets, in the overflow bucket cap test.
	storeTestOverflowCap = 2
	// storeTestPlainKey is a key without a namespace separator.
	storeTestPlainKey = "198.51.100.7"
	// storeTestDefaultRate is a fractional rate whose default burst is its ceiling.
	storeTestDefaultRate rate.Limit = 2.5
	// storeTestDefaultBurst is the burst Echo derives from storeTestDefaultRate.
	storeTestDefaultBurst = 3
	// storeTestSingleBurst lets an identifier through once, so its second call
	// shows whether the store still remembers it.
	storeTestSingleBurst = 1
	// storeTestPastDefaultExpiry is the sleep that takes the default expiry test
	// from two minutes after the first request to past Echo's three minute expiry.
	storeTestPastDefaultExpiry = 2 * storeTestIdleMargin
	// storeTestOtherKey is a key the default expiry test uses for the request that
	// would run cleanup after two minutes if the expiry were shorter than that.
	storeTestOtherKey = "198.51.100.8"
	// storeTestSecondKey is a key that the default expiry test leaves idle.
	storeTestSecondKey = "198.51.100.10"
	// storeTestThirdKey is a key the default expiry test uses for the request that
	// runs cleanup after the default expiry has passed.
	storeTestThirdKey = "198.51.100.9"
	// storeTestWorkers and storeTestCalls size the concurrency test;
	// storeTestWorkerPeers is how many namespaces the workers share.
	storeTestWorkers     = 8
	storeTestCalls       = 200
	storeTestWorkerPeers = 3
	// storeTestTinyCap makes a namespace or the store full after one entry.
	storeTestTinyCap = 1
	// storeTestFlood is how many forged keys a test sends; it exceeds every cap.
	storeTestFlood = 8
	// storeTestIPv6Block formats the i-th /64 key of one /48.
	storeTestIPv6Block = "2001:db8:1:%x::"
	// storeTestRefillRate is one token per minute. With a burst of storeTestBurst,
	// a bucket spent by one request is full again after two storeTestRefillStep,
	// and a bucket spent by storeTestBurst requests after four.
	storeTestRefillRate rate.Limit = 1.0 / 60
	storeTestRefillStep            = time.Minute / 2
	// storeTestRefillWait is the sleep after which a bucket spent by one request
	// is full again, with a margin so the comparison with the burst is not exact.
	storeTestRefillWait = 2*storeTestRefillStep + storeTestRefillStep/3
	// storeTestIdleMargin is how far past ExpiresIn the idle test sleeps.
	storeTestIdleMargin = time.Minute
)

func newTestStore(storeCap, namespaceCap int) *rateLimiterStore {
	return newRateLimiterStore(middleware.RateLimiterMemoryStoreConfig{
		Rate:      storeTestRate,
		Burst:     storeTestBurst,
		ExpiresIn: storeTestExpiresIn,
	}, storeCap, namespaceCap)
}

// exhaust calls Allow until the identifier is throttled and asserts the burst
// was granted exactly.
func exhaust(t *testing.T, store middleware.RateLimiterStore, id string) {
	t.Helper()
	for range storeTestBurst {
		ok, err := store.Allow(id)
		require.NoError(t, err)
		require.True(t, ok, "%s is within its burst", id)
	}
	require.False(t, allowed(t, store, id), "%s is throttled after its burst", id)
}

// allowed makes one Allow call.
func allowed(t *testing.T, store middleware.RateLimiterStore, id string) bool {
	t.Helper()
	ok, err := store.Allow(id)
	require.NoError(t, err)
	return ok
}

// TestRateLimiterStoreNeverResetsLiveBucketWhenFull verifies that a full
// namespace or store never forgets a throttled bucket to make room: a flood of
// forged keys in one namespace and a cycle through more /64 keys of one /48 than
// the cap holds both leave the exhausted keys denied, the newcomers share the
// overflow bucket, and another namespace is untouched.
func TestRateLimiterStoreNeverResetsLiveBucketWhenFull(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		store := newTestStore(storeTestRoomyCap, storeTestNamespaceCap)
		victim := storeTestPeer + keyNamespaceSep + "203.0.113.5"

		exhaust(t, store, storeTestPlainKey)
		exhaust(t, store, victim)
		overflowGranted := 0
		for i := range storeTestFlood {
			if allowed(t, store, fmt.Sprintf("%s%sforged-%d", storeTestPeer, keyNamespaceSep, i)) {
				overflowGranted++
			}
		}

		assert.False(t, allowed(t, store, victim), "a throttled key stays throttled when its namespace is full")
		assert.False(t, allowed(t, store, storeTestPlainKey), "another namespace is untouched")
		// One forged key fits next to the victim; the rest share the overflow bucket.
		assert.Equal(t, 1+storeTestBurst, overflowGranted,
			"the first forged key and the overflow bucket grant their bursts once")
		assert.LessOrEqual(t, store.len(), 1+storeTestNamespaceCap)

		first := fmt.Sprintf(storeTestIPv6Block, 0)
		exhaust(t, store, first)
		for i := 1; i <= storeTestFlood; i++ {
			allowed(t, store, fmt.Sprintf(storeTestIPv6Block, i))
		}
		assert.False(t, allowed(t, store, first), "cycling more /64 keys of one /48 than the cap does not reset a bucket")
	})
}

// TestRateLimiterStoreEvictsOnlyRefilledEntries verifies that a full store makes
// room only by dropping the least recently used bucket that has refilled to its
// burst; when that bucket is still short of tokens the newcomer is charged to the
// overflow bucket and nothing is forgotten.
func TestRateLimiterStoreEvictsOnlyRefilledEntries(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		store := newRateLimiterStore(middleware.RateLimiterMemoryStoreConfig{
			Rate: storeTestRefillRate, Burst: storeTestBurst, ExpiresIn: storeTestExpiresIn,
		}, storeTestStoreCap-1, storeTestRoomyCap)

		require.True(t, allowed(t, store, "a"))
		time.Sleep(storeTestRefillStep)
		exhaust(t, store, "b")
		time.Sleep(storeTestRefillStep)
		exhaust(t, store, "c")
		time.Sleep(storeTestRefillStep / 3)

		require.True(t, allowed(t, store, "d"), "a is full again, so d takes its place")
		assert.Equal(t, storeTestStoreCap-1, store.len())
		assert.False(t, allowed(t, store, "b"), "b was not evicted")
		assert.False(t, allowed(t, store, "c"), "c was not evicted")

		granted := 0
		for _, id := range []string{"e", "f", "g"} {
			if allowed(t, store, id) {
				granted++
			}
		}
		assert.Equal(t, storeTestBurst, granted, "the tail is not full, so newcomers share one overflow bucket")
		assert.Equal(t, storeTestStoreCap-1, store.len())
	})
}

// TestRateLimiterStoreNamespaceTailIsLeastRecentlyUsed verifies that a hit
// moves an entry to the front of its namespace, so the entry replaced from a
// full namespace is the least recently used one and not the oldest inserted.
func TestRateLimiterStoreNamespaceTailIsLeastRecentlyUsed(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		store := newRateLimiterStore(middleware.RateLimiterMemoryStoreConfig{
			Rate: storeTestRefillRate, Burst: storeTestBurst, ExpiresIn: storeTestExpiresIn,
		}, storeTestRoomyCap, storeTestNamespaceCap)
		spent := storeTestPeer + keyNamespaceSep + "spent"
		used := storeTestPeer + keyNamespaceSep + "used"
		newcomer := storeTestPeer + keyNamespaceSep + "newcomer"
		exhaust(t, store, spent)
		require.True(t, allowed(t, store, used))
		require.False(t, allowed(t, store, spent), "this hit makes the older entry the most recently used")

		time.Sleep(storeTestRefillWait)
		require.True(t, allowed(t, store, newcomer))

		assert.False(t, store.tracked(used), "the least recently used entry was full again and made room")
		assert.True(t, store.tracked(spent))
		assert.Equal(t, storeTestNamespaceCap, store.namespaceEntries(storeTestPeer), "the namespace kept both live entries")
		assert.False(t, store.hasOverflow(storeTestPeer), "no overflow bucket was needed")
	})
}

// TestRateLimiterStoreReplacesRefilledTailOfItsOwnNamespace verifies that when
// the entry made room for is in the newcomer's own namespace and was the last one
// there, the newcomer still lands in a registered namespace: the namespace
// holds it.
func TestRateLimiterStoreReplacesRefilledTailOfItsOwnNamespace(t *testing.T) {
	tests := []struct {
		name                   string
		storeCap, namespaceCap int
	}{
		{"namespace cap", storeTestRoomyCap, storeTestTinyCap},
		{"store cap", storeTestTinyCap, storeTestRoomyCap},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			synctest.Test(t, func(t *testing.T) {
				store := newRateLimiterStore(middleware.RateLimiterMemoryStoreConfig{
					Rate: storeTestRefillRate, Burst: storeTestBurst, ExpiresIn: storeTestExpiresIn,
				}, tt.storeCap, tt.namespaceCap)
				older := storeTestPeer + keyNamespaceSep + "older"
				newer := storeTestPeer + keyNamespaceSep + "newer"
				require.True(t, allowed(t, store, older))

				time.Sleep(storeTestRefillWait)
				require.True(t, allowed(t, store, newer))

				assert.False(t, store.tracked(older))
				assert.Equal(t, 1, store.namespaceCount(), "the newcomer's namespace is registered")
				assert.Equal(t, 1, store.namespaceEntries(storeTestPeer))
			})
		})
	}
}

// TestRateLimiterStoreOverflowOutlivesEntries verifies that a namespace keeps
// its overflow bucket after its entries expired, and that overflow buckets
// expire in the order they were last charged, not the order they were created.
func TestRateLimiterStoreOverflowOutlivesEntries(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		store := newTestStore(storeTestRoomyCap, storeTestTinyCap)
		first := storeTestPeer
		second := storeTestSecondPeer
		for _, peer := range []string{first, second} {
			require.True(t, allowed(t, store, peer+keyNamespaceSep+"member"))
		}
		require.True(t, allowed(t, store, first+keyNamespaceSep+"overflow"))
		time.Sleep(storeTestExpiresIn / 6)
		require.True(t, allowed(t, store, second+keyNamespaceSep+"overflow"))
		time.Sleep(storeTestExpiresIn / 6)
		require.True(t, allowed(t, store, first+keyNamespaceSep+"overflow"), "this charge makes the first overflow the newest")

		time.Sleep(storeTestExpiresIn*5/6 + storeTestIdleMargin/2)
		require.True(t, allowed(t, store, storeTestPlainKey), "this request runs the expiry")

		assert.False(t, store.hasOverflow(second), "idle for longer than ExpiresIn")
		assert.True(t, store.hasOverflow(first), "idle for less than ExpiresIn, though its entries expired")
	})
}

// TestRateLimiterStoreBoundsNamespaceOverflowBuckets verifies that the number of
// namespaces holding an overflow bucket is capped: past the cap a full namespace
// charges the store-wide bucket instead of opening another, so overflow-only
// namespaces cannot accumulate after their entries were replaced.
func TestRateLimiterStoreBoundsNamespaceOverflowBuckets(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		store := newRateLimiterStore(middleware.RateLimiterMemoryStoreConfig{
			Rate: storeTestRefillRate, Burst: storeTestBurst, ExpiresIn: storeTestExpiresIn,
		}, storeTestOverflowCap, storeTestTinyCap)
		peers := []string{storeTestPeer, storeTestSecondPeer, storeTestThirdPeer}
		key := func(peer, name string) string { return peer + keyNamespaceSep + name }

		for _, peer := range peers[:storeTestOverflowCap] {
			require.True(t, allowed(t, store, key(peer, "member")))
			require.True(t, allowed(t, store, key(peer, "overflow")), "the namespace is full, so it opens an overflow bucket")
		}
		time.Sleep(storeTestRefillWait)
		last := peers[storeTestOverflowCap]
		require.True(t, allowed(t, store, key(last, "member")), "a refilled tail makes room in the store")

		require.True(t, allowed(t, store, key(last, "overflow")))
		assert.False(t, store.hasOverflow(last), "the cap on overflow buckets is reached, so the store-wide bucket is charged")
		assert.True(t, store.hasGlobalOverflow())
	})
}

// TestRateLimiterStoreGlobalOverflowExpires verifies that the store-wide
// overflow bucket expires like an entry.
func TestRateLimiterStoreGlobalOverflowExpires(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		store := newTestStore(storeTestTinyCap, storeTestRoomyCap)
		require.True(t, allowed(t, store, "held"))
		require.True(t, allowed(t, store, "overflowing"))
		require.True(t, store.hasGlobalOverflow())

		time.Sleep(storeTestExpiresIn + storeTestIdleMargin)
		require.True(t, allowed(t, store, "later"))
		assert.False(t, store.hasGlobalOverflow())
	})
}

// TestRateLimiterStoreExpiresFromTail verifies that every Allow drops the
// identifiers idle longer than ExpiresIn from the tail, and that a hit keeps an
// identifier alive.
func TestRateLimiterStoreExpiresFromTail(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		store := newTestStore(storeTestRoomyCap, storeTestRoomyCap)
		for _, id := range []string{"a", "b", "c"} {
			require.True(t, allowed(t, store, id))
		}
		time.Sleep(storeTestExpiresIn * 2 / 3)
		require.True(t, allowed(t, store, "a"))
		time.Sleep(storeTestExpiresIn / 2)

		require.True(t, allowed(t, store, "d"))
		assert.Equal(t, 2, store.len(), "b and c expired, a was kept alive by its hit")
		assert.Equal(t, 2, store.namespaceCount())

		time.Sleep(storeTestExpiresIn + storeTestIdleMargin)
		require.True(t, allowed(t, store, "e"))
		assert.Equal(t, 1, store.len())
		assert.Equal(t, 1, store.namespaceCount(), "the namespaces of the expired identifiers are gone")
	})
}

// TestRateLimiterStoreOverflowExpires verifies that a namespace's overflow
// bucket expires like an entry: after ExpiresIn the namespace is gone and its
// overflow starts with a full burst.
func TestRateLimiterStoreOverflowExpires(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		store := newTestStore(storeTestRoomyCap, storeTestTinyCap)
		member := storeTestPeer + keyNamespaceSep + "a"
		overflow := storeTestPeer + keyNamespaceSep + "b"
		require.True(t, allowed(t, store, member))
		for range storeTestBurst {
			require.True(t, allowed(t, store, overflow))
		}
		require.False(t, allowed(t, store, overflow), "the overflow bucket is spent")

		time.Sleep(storeTestExpiresIn + storeTestIdleMargin)
		require.True(t, allowed(t, store, storeTestPlainKey))
		assert.Equal(t, 1, store.namespaceCount(), "the namespace and its overflow bucket expired")
		assert.True(t, allowed(t, store, overflow), "a new entry starts with a full burst")
	})
}

// TestRateLimiterStoreIdleCleanupKeepsFreshEntries verifies that cleanup drops
// only the identifiers idle longer than ExpiresIn: a throttled identifier used
// within that time stays throttled while an older one starts afresh.
func TestRateLimiterStoreIdleCleanupKeepsFreshEntries(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		store := newTestStore(storeTestRoomyCap, storeTestRoomyCap)
		exhaust(t, store, "old")
		time.Sleep(storeTestExpiresIn / 2)
		exhaust(t, store, "fresh")
		time.Sleep(storeTestExpiresIn/2 + storeTestIdleMargin)

		require.True(t, allowed(t, store, storeTestPlainKey), "this request runs the cleanup")
		assert.False(t, allowed(t, store, "fresh"), "idle for less than ExpiresIn")
		assert.True(t, allowed(t, store, "old"), "idle for longer than ExpiresIn")
	})
}

// TestRateLimiterStoreDefaults verifies through Allow what a zero Burst and a
// zero ExpiresIn mean: Echo's defaults, the rate rounded up as the burst and a
// three minute idle expiry.
func TestRateLimiterStoreDefaults(t *testing.T) {
	t.Run("zero burst is the rate rounded up", func(t *testing.T) {
		synctest.Test(t, func(t *testing.T) {
			store := newRateLimiterStore(middleware.RateLimiterMemoryStoreConfig{Rate: storeTestDefaultRate}, storeTestRoomyCap, storeTestRoomyCap)
			granted := 0
			for range storeTestDefaultBurst + 1 {
				if allowed(t, store, storeTestPlainKey) {
					granted++
				}
			}
			assert.Equal(t, storeTestDefaultBurst, granted)
		})
	})

	t.Run("zero expiry is Echo's default", func(t *testing.T) {
		synctest.Test(t, func(t *testing.T) {
			store := newRateLimiterStore(middleware.RateLimiterMemoryStoreConfig{Rate: storeTestRate, Burst: storeTestSingleBurst}, storeTestRoomyCap, storeTestRoomyCap)
			defaultExpiry := middleware.DefaultRateLimiterMemoryStoreConfig.ExpiresIn
			require.True(t, allowed(t, store, storeTestPlainKey))
			require.True(t, allowed(t, store, storeTestSecondKey))

			time.Sleep(defaultExpiry - storeTestIdleMargin)
			require.True(t, allowed(t, store, storeTestOtherKey), "this request runs the cleanup if the expiry is shorter")
			assert.False(t, allowed(t, store, storeTestPlainKey), "idle for less than the default expiry")

			time.Sleep(storeTestPastDefaultExpiry)
			require.True(t, allowed(t, store, storeTestThirdKey), "this request runs the cleanup")
			assert.True(t, allowed(t, store, storeTestSecondKey), "idle for longer than the default expiry")
		})
	})
}

// TestNewRateLimiterStoreProductionCaps verifies that the exported constructor
// applies both production caps: a full namespace or store admits one more key
// through its overflow bucket only, and keeps its throttled keys.
func TestNewRateLimiterStoreProductionCaps(t *testing.T) {
	cfg := middleware.RateLimiterMemoryStoreConfig{Rate: storeTestRate, Burst: storeTestSingleBurst, ExpiresIn: storeTestExpiresIn}
	tests := []struct {
		name string
		cap  int
		key  func(i int) string
		// sibling is a key in another namespace; a full namespace still has room for
		// it, a full store does not.
		siblingAllowed bool
	}{
		{"namespace", rateLimiterNamespaceMaxEntries, func(i int) string {
			return fmt.Sprintf("%s%s%d", storeTestPeer, keyNamespaceSep, i)
		}, true},
		{"store", rateLimiterStoreMaxEntries, func(i int) string { return fmt.Sprintf("client%d", i) }, false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			synctest.Test(t, func(t *testing.T) {
				store := NewRateLimiterStore(cfg)
				for i := range tt.cap {
					require.True(t, allowed(t, store, tt.key(i)))
				}

				assert.True(t, allowed(t, store, tt.key(tt.cap)), "the first key past the cap uses the overflow bucket")
				assert.False(t, allowed(t, store, tt.key(tt.cap+1)), "the overflow bucket is spent")
				assert.False(t, allowed(t, store, tt.key(0)), "a full %s keeps its throttled keys", tt.name)
				assert.Equal(t, tt.siblingAllowed, allowed(t, store, storeTestPlainKey), "a key of another namespace")
			})
		})
	}
}

// TestRateLimiterStoreConcurrentAllow verifies that parallel callers can share
// one store, under the race detector, and that the store-wide cap holds afterwards.
func TestRateLimiterStoreConcurrentAllow(t *testing.T) {
	t.Parallel()
	store := newTestStore(storeTestStoreCap, storeTestNamespaceCap)
	var wg sync.WaitGroup
	for worker := range storeTestWorkers {
		wg.Go(func() {
			for i := range storeTestCalls {
				_, err := store.Allow(fmt.Sprintf("peer%d%sclient%d", worker%storeTestWorkerPeers, keyNamespaceSep, i))
				assert.NoError(t, err)
			}
		})
	}
	wg.Wait()

	assert.LessOrEqual(t, store.len(), storeTestStoreCap)
}

// len returns the number of tracked identifiers.
func (s *rateLimiterStore) len() int {
	s.mu.Lock()
	defer s.mu.Unlock()
	return len(s.entries)
}

// namespaceCount returns the number of namespaces with an entry or an overflow
// bucket.
func (s *rateLimiterStore) namespaceCount() int {
	s.mu.Lock()
	defer s.mu.Unlock()
	return len(s.namespaces)
}

// tracked reports whether the identifier has an entry.
func (s *rateLimiterStore) tracked(id string) bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	_, ok := s.entries[id]
	return ok
}

// hasOverflow reports whether the namespace has an overflow bucket.
func (s *rateLimiterStore) hasOverflow(namespace string) bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	ns := s.namespaces[namespace]
	return ns != nil && ns.overflow != nil
}

// hasGlobalOverflow reports whether the store-wide overflow bucket exists.
func (s *rateLimiterStore) hasGlobalOverflow() bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.globalOverflow != nil
}

// namespaceEntries returns the number of entries the namespace holds.
func (s *rateLimiterStore) namespaceEntries(namespace string) int {
	s.mu.Lock()
	defer s.mu.Unlock()
	ns := s.namespaces[namespace]
	if ns == nil {
		return 0
	}
	return ns.entries.Len()
}
