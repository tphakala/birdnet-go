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
//   - A loader runs on its own goroutine. A panic in it, or while its result
//     is stored, is recovered and returned as *PanicError, and a runtime.Goexit (such as testing.T.FailNow
//     or testify require in a test loader) is returned as ErrLoaderExited, so
//     waiting callers are always released.
package ttlcache

import (
	"context"
	"fmt"
	"runtime/debug"
	"sync"
	"sync/atomic"
	"time"

	"github.com/tphakala/birdnet-go/internal/errors"
)

// maxSweepPerWrite bounds how many expired entries one write removes, which
// keeps the cost of a write constant while still draining any backlog over
// repeated writes.
const maxSweepPerWrite = 32

// sweepAll is the sweep limit used by Len to remove every expired entry.
const sweepAll = -1

// ErrLoaderPanicked is wrapped by *PanicError, so errors.Is(err,
// ErrLoaderPanicked) reports a load that panicked, in the loader or while its
// result was stored.
var ErrLoaderPanicked = errors.NewStd("ttlcache: loader panicked")

// ErrLoaderExited is returned by GetOrLoad when the loader goroutine exits
// through runtime.Goexit without returning.
var ErrLoaderExited = errors.NewStd("ttlcache: loader exited without returning")

// PanicError is returned by GetOrLoad when a load panics, in the loader or
// while its result is stored (for example in a WithClock clock). The loader runs
// on a goroutine of its own, where an unrecovered panic would crash the
// process, so the cache recovers it and hands it to every waiting caller.
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

// flightID identifies one in-flight load: the key and the generation the
// loading callers missed at. Callers that miss after a mutation get a new
// generation and therefore start a load of their own.
type flightID[K comparable] struct {
	gen uint64
	key K
}

// flight is one in-flight load shared by every caller that missed on the same
// flightID. value and err are written once, before done is closed.
type flight[V any] struct {
	done  chan struct{}
	value V
	err   error
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
	// flights holds the loads in progress, keyed by key and generation.
	flights map[flightID[K]]*flight[V]

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
		flights:    make(map[flightID[K]]*flight[V]),
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
	defer c.mu.Unlock()
	v, ok := c.lookupLocked(key, c.now())
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
	defer c.mu.Unlock()
	c.generation++
	c.insertLocked(key, value, c.now())
}

// Delete removes key. Loads in flight will not store their result, including
// loads for a key that was absent.
func (c *Cache[K, V]) Delete(key K) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.generation++
	if e, ok := c.entries[key]; ok {
		c.removeLocked(e)
	}
}

// Clear removes every entry. Loads in flight will not store their result, and
// a GetOrLoad that starts afterwards runs its own load.
func (c *Cache[K, V]) Clear() {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.generation++
	clear(c.entries)
	c.head, c.tail = nil, nil
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
// and are never cached. A panic in load, or while its result is stored, is
// returned as *PanicError, and a runtime.Goexit in load as ErrLoaderExited.
//
// The loader runs on its own goroutine with a context that carries the values
// of the caller that started the load but not its cancellation, so one caller
// giving up cannot fail the others; the loader must bound its own run time.
// Each caller waits on its own ctx and returns ctx.Err() when it is done first,
// while the load continues and still fills the cache. If ctx is already done on
// a miss, GetOrLoad returns ctx.Err() without loading.
//
// A Set, Delete or Clear that runs while a load is in flight discards that
// load's store; its callers still receive the loaded value.
func (c *Cache[K, V]) GetOrLoad(ctx context.Context, key K, load func(context.Context) (V, error)) (V, error) {
	v, f, err := c.hitOrJoin(ctx, key, load)
	if f == nil {
		return v, err
	}

	select {
	case <-ctx.Done():
		var zero V
		return zero, ctx.Err()
	case <-f.done:
		return f.value, f.err
	}
}

// hitOrJoin returns the live value for key, or the flight the caller waits
// on, or ctx.Err() when ctx is done on a miss. The lookup and the flight
// registration run under one lock, so a load that has already stored its value
// is a hit and no store can be missed in between. The deferred unlock keeps
// the cache usable when a key or the clock panics under the lock.
func (c *Cache[K, V]) hitOrJoin(ctx context.Context, key K, load func(context.Context) (V, error)) (V, *flight[V], error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if v, ok := c.lookupLocked(key, c.now()); ok {
		c.hits.Add(1)
		return v, nil, nil
	}
	c.misses.Add(1)
	var zero V
	if err := ctx.Err(); err != nil {
		return zero, nil, err
	}
	return zero, c.joinOrStartLocked(ctx, key, load), nil
}

// joinOrStartLocked returns the flight for key at the current generation,
// starting a load on a new goroutine when there is none. It is separate from
// GetOrLoad so that only the miss path copies the key to the heap.
func (c *Cache[K, V]) joinOrStartLocked(ctx context.Context, key K, load func(context.Context) (V, error)) *flight[V] {
	id := flightID[K]{gen: c.generation, key: key}
	if f, ok := c.flights[id]; ok {
		return f
	}
	f := &flight[V]{done: make(chan struct{})}
	c.flights[id] = f
	go c.runFlight(context.WithoutCancel(ctx), id, f, load)
	return f
}

// runFlight runs one load, stores a successful result if no mutation happened
// since the miss, and releases the waiting callers. A panic or Goexit in load,
// or a panic while storing the result, still releases them, with an error.
func (c *Cache[K, V]) runFlight(ctx context.Context, id flightID[K], f *flight[V], load func(context.Context) (V, error)) {
	returned := false
	defer func() {
		if !returned {
			if r := recover(); r != nil {
				f.err = &PanicError{Value: r, Stack: debug.Stack()}
			} else {
				f.err = ErrLoaderExited
			}
		}
		c.unregister(id)
		close(f.done)
	}()

	v, err := load(ctx)
	if err != nil {
		f.err = err
	} else {
		c.storeIfCurrent(id, v)
		f.value = v
	}
	returned = true
}

// storeIfCurrent stores a loaded value unless a mutation happened since the
// miss that started the load.
func (c *Cache[K, V]) storeIfCurrent(id flightID[K], v V) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.generation == id.gen {
		c.insertLocked(id.key, v, c.now())
	}
}

// unregister removes a finished flight.
func (c *Cache[K, V]) unregister(id flightID[K]) {
	c.mu.Lock()
	defer c.mu.Unlock()
	delete(c.flights, id)
}
