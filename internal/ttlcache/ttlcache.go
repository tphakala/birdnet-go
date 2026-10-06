// Package ttlcache provides a small generic in-memory cache with one
// time-to-live per cache, an optional entry bound, and a load-once
// GetOrLoad that deduplicates concurrent loads of the same key.
//
// The cache owns no goroutine. Expired entries are dropped lazily: when a read
// finds one, in a bounded sweep on every write, and in a full sweep when Len is
// called. A cache that is no longer referenced is garbage-collected with its
// contents, so owners need no Close method and goroutine-leak gates need no
// ignores for it.
//
// Because the TTL is per cache, write order equals expiry order. One intrusive
// list in write order therefore serves as both the expiry queue and the
// eviction order: when a bound is set, the oldest write is evicted first.
//
// Constraints that callers must respect:
//   - The clock passed to WithClock must be non-decreasing. A clock that moves
//     backwards only weakens the sweep, never the guarantee that a read does not
//     return an expired entry, because every read checks the entry's own expiry.
//   - Keys must be reflexive (key == key). Float keys holding NaN are
//     unsupported. With an interface key type, every dynamic key value must be
//     comparable; an uncomparable one panics, as it would in any Go map.
//   - A loader must not call GetOrLoad for the same key on the same cache (it
//     would wait for itself).
//   - A loader must not call runtime.Goexit, so no testing.T.FailNow or
//     testify require inside a test loader. The deduplication layer never
//     answers such a flight and callers without a deadline block forever.
//     Panics are recovered and returned as *PanicError.
package ttlcache

import (
	"context"
	"fmt"
	"runtime/debug"
	"strconv"
	"sync"
	"sync/atomic"
	"time"

	"golang.org/x/sync/singleflight"

	"github.com/tphakala/birdnet-go/internal/errors"
)

// maxSweepPerWrite bounds how many expired entries one write removes, which
// keeps the cost of a write constant while still draining any backlog over
// repeated writes.
const maxSweepPerWrite = 32

// sweepAll is the sweep limit used by Len to remove every expired entry.
const sweepAll = -1

// flightKeySeparator separates the generation from the key in a
// deduplication key. NUL cannot appear in a decimal generation number.
const flightKeySeparator = "\x00"

// ErrLoaderPanicked is wrapped by *PanicError, so errors.Is(err,
// ErrLoaderPanicked) reports a loader that panicked.
var ErrLoaderPanicked = errors.NewStd("ttlcache: loader panicked")

// PanicError is returned by GetOrLoad when the loader panics. The cache
// recovers the panic because the deduplication layer would otherwise
// re-raise it on a goroutine nobody can recover.
type PanicError struct {
	// Value is the value passed to panic.
	Value any
	// Stack is the stack of the panicking goroutine. It is not part of Error().
	Stack []byte
}

// Error implements the error interface.
func (e *PanicError) Error() string {
	return fmt.Sprintf("%s: %v", ErrLoaderPanicked.Error(), e.Value)
}

// Unwrap returns ErrLoaderPanicked.
func (e *PanicError) Unwrap() error { return ErrLoaderPanicked }

// Stats holds cumulative cache counters.
type Stats struct {
	// Hits counts Get and GetOrLoad calls served from the cache.
	Hits uint64
	// Misses counts Get and GetOrLoad calls that found no live entry.
	Misses uint64
	// Evictions counts live entries removed to honour the entry bound.
	// Removing an expired entry is not an eviction.
	Evictions uint64
}

type options struct {
	now        func() time.Time
	maxEntries int
}

// Option configures a Cache.
type Option func(*options)

// WithClock sets the time source. A nil function keeps time.Now. The clock
// must be non-decreasing.
func WithClock(now func() time.Time) Option {
	return func(o *options) {
		if now != nil {
			o.now = now
		}
	}
}

// WithMaxEntries bounds the number of entries. When a write would exceed the
// bound, expired entries are dropped first and then the oldest writes are
// evicted. n <= 0 means unbounded.
func WithMaxEntries(n int) Option {
	return func(o *options) { o.maxEntries = n }
}

type entry[K comparable, V any] struct {
	key        K
	value      V
	expiresAt  time.Time
	prev, next *entry[K, V]
}

// flightResult is what a deduplicated load returns. It carries the key the
// load ran for, so a caller whose key merely encodes to the same flight key
// can detect that the value is not its own.
type flightResult[K comparable, V any] struct {
	key   K
	value V
}

// Cache is a TTL cache safe for concurrent use. The zero value is not usable;
// create one with New.
type Cache[K comparable, V any] struct {
	ttl        time.Duration
	now        func() time.Time
	maxEntries int

	mu         sync.Mutex
	entries    map[K]*entry[K, V]
	head, tail *entry[K, V] // write order: head is the oldest write
	// generation is bumped by every explicit mutation (Set, Delete, Clear). A
	// load stores its result only if the generation is unchanged since the
	// caller's miss, so a mutation discards the stores of loads in flight.
	generation uint64

	flights singleflight.Group

	hits, misses, evictions atomic.Uint64
}

// New creates a cache whose entries live for ttl. ttl <= 0 means entries never
// expire.
func New[K comparable, V any](ttl time.Duration, opts ...Option) *Cache[K, V] {
	o := options{now: time.Now}
	for _, opt := range opts {
		opt(&o)
	}
	return &Cache[K, V]{
		ttl:        ttl,
		now:        o.now,
		maxEntries: o.maxEntries,
		entries:    make(map[K]*entry[K, V]),
	}
}

// expiredLocked reports whether e is at or past its expiry. An entry lives in
// [writtenAt, writtenAt+ttl).
func (c *Cache[K, V]) expiredLocked(e *entry[K, V], now time.Time) bool {
	return c.ttl > 0 && !now.Before(e.expiresAt)
}

func (c *Cache[K, V]) unlinkLocked(e *entry[K, V]) {
	if e.prev != nil {
		e.prev.next = e.next
	} else {
		c.head = e.next
	}
	if e.next != nil {
		e.next.prev = e.prev
	} else {
		c.tail = e.prev
	}
	e.prev, e.next = nil, nil
}

func (c *Cache[K, V]) pushTailLocked(e *entry[K, V]) {
	e.prev, e.next = c.tail, nil
	if c.tail != nil {
		c.tail.next = e
	} else {
		c.head = e
	}
	c.tail = e
}

func (c *Cache[K, V]) removeLocked(e *entry[K, V]) {
	c.unlinkLocked(e)
	delete(c.entries, e.key)
}

// lookupLocked returns the live value for key. An expired entry is removed.
// It does not touch the counters.
func (c *Cache[K, V]) lookupLocked(key K, now time.Time) (V, bool) {
	e, ok := c.entries[key]
	if !ok {
		var zero V
		return zero, false
	}
	if c.expiredLocked(e, now) {
		c.removeLocked(e)
		var zero V
		return zero, false
	}
	return e.value, true
}

// sweepLocked removes up to limit expired entries from the oldest end. A
// negative limit removes all of them.
func (c *Cache[K, V]) sweepLocked(now time.Time, limit int) {
	for c.head != nil && limit != 0 && c.expiredLocked(c.head, now) {
		c.removeLocked(c.head)
		limit--
	}
}

// insertLocked stores value under key as the newest write, then sweeps expired
// entries and enforces the entry bound.
func (c *Cache[K, V]) insertLocked(key K, value V, now time.Time) {
	if e, ok := c.entries[key]; ok {
		c.unlinkLocked(e)
		e.value = value
		e.expiresAt = now.Add(c.ttl)
		c.pushTailLocked(e)
	} else {
		e = &entry[K, V]{key: key, value: value, expiresAt: now.Add(c.ttl)}
		c.entries[key] = e
		c.pushTailLocked(e)
	}
	c.sweepLocked(now, maxSweepPerWrite)
	for c.maxEntries > 0 && len(c.entries) > c.maxEntries {
		oldest := c.head
		// The bounded sweep can leave expired entries at the head; removing
		// one of those is not an eviction.
		if !c.expiredLocked(oldest, now) {
			c.evictions.Add(1)
		}
		c.removeLocked(oldest)
	}
}

// Get returns the live value for key. It counts one hit or one miss.
func (c *Cache[K, V]) Get(key K) (V, bool) {
	c.mu.Lock()
	v, ok := c.lookupLocked(key, c.now())
	c.mu.Unlock()
	if ok {
		c.hits.Add(1)
	} else {
		c.misses.Add(1)
	}
	return v, ok
}

// Set stores value under key and restarts its TTL. Loads in flight will not
// overwrite it.
func (c *Cache[K, V]) Set(key K, value V) {
	c.mu.Lock()
	c.generation++
	c.insertLocked(key, value, c.now())
	c.mu.Unlock()
}

// Delete removes key. Loads in flight will not store their result, including
// loads for a key that was absent.
func (c *Cache[K, V]) Delete(key K) {
	c.mu.Lock()
	c.generation++
	if e, ok := c.entries[key]; ok {
		c.removeLocked(e)
	}
	c.mu.Unlock()
}

// Clear removes every entry. Loads in flight will not store their result, and
// a GetOrLoad that starts afterwards runs its own load.
func (c *Cache[K, V]) Clear() {
	c.mu.Lock()
	c.generation++
	clear(c.entries)
	c.head, c.tail = nil, nil
	c.mu.Unlock()
}

// Len returns the number of live entries. It first removes every expired
// entry, which is amortized because each entry is removed once.
func (c *Cache[K, V]) Len() int {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.sweepLocked(c.now(), sweepAll)
	return len(c.entries)
}

// Stats returns the cumulative counters. It does not take the cache lock.
func (c *Cache[K, V]) Stats() Stats {
	return Stats{
		Hits:      c.hits.Load(),
		Misses:    c.misses.Load(),
		Evictions: c.evictions.Load(),
	}
}

// GetOrLoad returns the live value for key, or calls load once for all
// concurrent callers that miss on the same key and stores a successful result.
//
// It counts one hit or one miss per call. Errors from load are returned as-is
// and are never cached. A panic in load is recovered and returned as
// *PanicError.
//
// The loader receives a context that carries the values of the caller that
// started the load but not its cancellation, so one caller giving up cannot
// fail the others; the loader must bound its own run time. Each caller waits on
// its own ctx and returns ctx.Err() when it is done first, while the load
// continues and still fills the cache. If ctx is already done on a miss,
// GetOrLoad returns ctx.Err() without loading.
//
// A Set, Delete or Clear that runs while a load is in flight discards that
// load's store; its callers still receive the loaded value.
func (c *Cache[K, V]) GetOrLoad(ctx context.Context, key K, load func(context.Context) (V, error)) (V, error) {
	var zero V

	c.mu.Lock()
	if v, ok := c.lookupLocked(key, c.now()); ok {
		c.mu.Unlock()
		c.hits.Add(1)
		return v, nil
	}
	gen := c.generation
	c.mu.Unlock()
	c.misses.Add(1)

	if err := ctx.Err(); err != nil {
		return zero, err
	}

	detached := context.WithoutCancel(ctx)
	ch := c.flights.DoChan(flightKey(gen, key), func() (any, error) {
		return c.loadAndStore(detached, gen, key, load)
	})

	select {
	case <-ctx.Done():
		return zero, ctx.Err()
	case res := <-ch:
		r, ok := res.Val.(flightResult[K, V])
		if ok && r.key == key {
			return r.value, res.Err
		}
		// The flight ran for a different key that encodes to the same flight
		// key. Run our own load against the current generation, so the result
		// is storable even if a mutation happened meanwhile. This load runs
		// synchronously and is not deduplicated, so the caller cannot give up
		// early. It needs two keys whose %#v forms collide, which the flat
		// key types used in this repository cannot produce.
		c.mu.Lock()
		gen = c.generation
		c.mu.Unlock()
		own, err := c.loadAndStore(detached, gen, key, load)
		return own.value, err
	}
}

// loadAndStore runs one load. The result always carries key, including on the
// error and panic paths, so waiters never mistake them for a collision.
func (c *Cache[K, V]) loadAndStore(ctx context.Context, gen uint64, key K, load func(context.Context) (V, error)) (res flightResult[K, V], err error) {
	res.key = key
	defer func() {
		if r := recover(); r != nil {
			var zero V
			res.value = zero
			err = &PanicError{Value: r, Stack: debug.Stack()}
		}
	}()

	// A flight that finished just before this one started may have stored the
	// value already; the peek closes that window without counting a hit.
	c.mu.Lock()
	if v, ok := c.lookupLocked(key, c.now()); ok {
		c.mu.Unlock()
		res.value = v
		return res, nil
	}
	c.mu.Unlock()

	v, err := load(ctx)
	if err != nil {
		return res, err
	}
	res.value = v

	c.mu.Lock()
	if c.generation == gen {
		c.insertLocked(key, v, c.now())
	}
	c.mu.Unlock()
	return res, nil
}

// flightKey builds the deduplication key. It is only a hint: callers compare
// the returned key before trusting a shared value.
func flightKey[K comparable](gen uint64, key K) string {
	return strconv.FormatUint(gen, 10) + flightKeySeparator + fmt.Sprintf("%#v", key)
}
