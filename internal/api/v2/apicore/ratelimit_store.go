// ratelimit_store.go provides the capped limiter store the api/v2 rate limiters use.
package apicore

import (
	"container/list"
	"math"
	"sync"
	"time"

	"github.com/labstack/echo/v4/middleware"
	"golang.org/x/time/rate"

	"github.com/tphakala/birdnet-go/internal/logger"
)

const (
	// rateLimiterStoreMaxEntries caps the identifiers one limiter store tracks.
	rateLimiterStoreMaxEntries = 4096
	// rateLimiterNamespaceMaxEntries caps the identifiers one namespace holds, so
	// a single peer or IPv6 /48 that mints keys only fills its own namespace.
	rateLimiterNamespaceMaxEntries = 512
)

// rateLimiterEntry is one tracked identifier.
type rateLimiterEntry struct {
	id       string
	limiter  *rate.Limiter
	lastSeen time.Time
	// global and local are the entry's elements in the store-wide and the
	// namespace's recency lists; both hold the entry as their value.
	global *list.Element
	local  *list.Element
	ns     *rateLimiterNamespace
}

// rateLimiterBucket is a shared bucket that charges the requests of identifiers
// the store has no room to track.
type rateLimiterBucket struct {
	limiter  *rate.Limiter
	lastSeen time.Time
}

// rateLimiterNamespace holds the entries that share a namespace, most recently
// used first, and the overflow bucket that charges its untracked identifiers.
type rateLimiterNamespace struct {
	name    string
	entries *list.List
	// overflow is nil until the namespace first has no room; overflowElem is the
	// namespace's element in the store's overflowing list while it is set.
	overflow     *rateLimiterBucket
	overflowElem *list.Element
}

// rateLimiterStore is a middleware.RateLimiterStore with two caps that never
// forgets a bucket that still holds state. A namespace (see rateLimitNamespace)
// holds at most namespaceCap entries and the store at most storeCap. A new
// identifier that finds its namespace or the store full replaces the least
// recently used entry only if that entry's bucket is full again, which loses
// nothing; otherwise the request is charged to the namespace's overflow bucket
// or the store's global overflow bucket, which have the per-identifier rate and
// burst; at most storeCap namespaces hold an overflow bucket, and later ones use
// the store-wide bucket. Identifiers idle longer than ExpiresIn, overflow
// buckets included, are dropped from the tail of their recency list on every
// Allow.
type rateLimiterStore struct {
	mu           sync.Mutex
	rate         rate.Limit
	burst        int
	expiresIn    time.Duration
	storeCap     int
	namespaceCap int

	entries    map[string]*rateLimiterEntry
	namespaces map[string]*rateLimiterNamespace
	// recency lists every entry, most recently used first, so it is ordered by
	// lastSeen. overflowing lists the namespaces that have an overflow bucket,
	// most recently charged first.
	recency        *list.List
	overflowing    *list.List
	globalOverflow *rateLimiterBucket
	lastWarn       time.Time
}

// NewRateLimiterStore returns an Echo rate limiter store that bounds its memory:
// at most rateLimiterStoreMaxEntries identifiers overall and
// rateLimiterNamespaceMaxEntries per namespace. At a cap it reuses the room of a
// bucket that has refilled and otherwise charges the request to a shared
// overflow bucket, so a client that mints keys cannot reset a throttled bucket.
// Rate, Burst and ExpiresIn are read as for
// middleware.NewRateLimiterMemoryStoreWithConfig. Limiters whose keys a client
// can vary use this instead of Echo's unbounded store.
func NewRateLimiterStore(cfg middleware.RateLimiterMemoryStoreConfig) middleware.RateLimiterStore {
	return newRateLimiterStore(cfg, rateLimiterStoreMaxEntries, rateLimiterNamespaceMaxEntries)
}

// newRateLimiterStore is NewRateLimiterStore with explicit caps, for tests.
func newRateLimiterStore(cfg middleware.RateLimiterMemoryStoreConfig, storeCap, namespaceCap int) *rateLimiterStore {
	s := &rateLimiterStore{
		rate:         cfg.Rate,
		burst:        cfg.Burst,
		expiresIn:    cfg.ExpiresIn,
		storeCap:     storeCap,
		namespaceCap: namespaceCap,
		entries:      make(map[string]*rateLimiterEntry),
		namespaces:   make(map[string]*rateLimiterNamespace),
		recency:      list.New(),
		overflowing:  list.New(),
	}
	if s.expiresIn == 0 {
		s.expiresIn = middleware.DefaultRateLimiterMemoryStoreConfig.ExpiresIn
	}
	if s.burst == 0 {
		// Echo's default: the rate rounded up, at least 1.
		s.burst = int(math.Max(1, math.Ceil(float64(s.rate))))
	}
	return s
}

// Allow implements middleware.RateLimiterStore.
func (s *rateLimiterStore) Allow(identifier string) (bool, error) {
	s.mu.Lock()
	now := time.Now()
	s.expire(now)
	allow, newOverflow := s.admit(identifier, now)
	warn := newOverflow && (s.lastWarn.IsZero() || now.Sub(s.lastWarn) > s.expiresIn)
	if warn {
		s.lastWarn = now
	}
	s.mu.Unlock()

	if warn {
		GetLogger().Warn("Rate limiter store or one of its namespaces is full, charging new clients to a shared overflow bucket",
			logger.Int("store_cap", s.storeCap), logger.Int("namespace_cap", s.namespaceCap))
	}
	return allow, nil
}

// admit charges one request to the identifier's bucket, tracking the identifier
// when there is room for it. It reports whether the request is allowed and
// whether it created an overflow bucket. The caller holds s.mu.
func (s *rateLimiterStore) admit(identifier string, now time.Time) (allow, newOverflow bool) {
	if entry, ok := s.entries[identifier]; ok {
		entry.lastSeen = now
		s.recency.MoveToFront(entry.global)
		entry.ns.entries.MoveToFront(entry.local)
		return entry.limiter.AllowN(now, 1), false
	}

	name := rateLimitNamespace(identifier)
	// Make room first: removing an entry deletes its namespace when nothing is
	// left in it, so the namespace is looked up (or created) afterwards.
	if ns := s.namespaces[name]; ns != nil && ns.entries.Len() >= s.namespaceCap {
		tail := entryOf(ns.entries.Back())
		if !s.refilled(tail, now) {
			return s.chargeNamespace(ns, now)
		}
		s.remove(tail)
	}
	if len(s.entries) >= s.storeCap {
		tail := entryOf(s.recency.Back())
		if !s.refilled(tail, now) {
			return s.chargeGlobal(now)
		}
		s.remove(tail)
	}

	ns := s.namespaces[name]
	if ns == nil {
		ns = &rateLimiterNamespace{name: name, entries: list.New()}
		s.namespaces[name] = ns
	}
	entry := &rateLimiterEntry{id: identifier, limiter: rate.NewLimiter(s.rate, s.burst), lastSeen: now, ns: ns}
	entry.global = s.recency.PushFront(entry)
	entry.local = ns.entries.PushFront(entry)
	s.entries[identifier] = entry
	return entry.limiter.AllowN(now, 1), false
}

// refilled reports whether the entry's bucket is full again, so that forgetting
// it changes nothing for its identifier.
func (s *rateLimiterStore) refilled(entry *rateLimiterEntry, now time.Time) bool {
	return entry.limiter.TokensAt(now) >= float64(s.burst)
}

// charge spends one token of the bucket at now and records the use.
func (b *rateLimiterBucket) charge(now time.Time) bool {
	b.lastSeen = now
	return b.limiter.AllowN(now, 1)
}

// chargeNamespace charges a request to the namespace's overflow bucket,
// creating it on first use. At most storeCap namespaces hold an overflow bucket;
// past that the request is charged to the store-wide bucket, so overflow buckets
// that outlive their namespace's entries cannot accumulate.
func (s *rateLimiterStore) chargeNamespace(ns *rateLimiterNamespace, now time.Time) (allow, created bool) {
	if ns.overflow == nil {
		if s.overflowing.Len() >= s.storeCap {
			return s.chargeGlobal(now)
		}
		ns.overflow = &rateLimiterBucket{limiter: rate.NewLimiter(s.rate, s.burst)}
		ns.overflowElem = s.overflowing.PushFront(ns)
		created = true
	} else {
		s.overflowing.MoveToFront(ns.overflowElem)
	}
	return ns.overflow.charge(now), created
}

// chargeGlobal charges a request to the store-wide overflow bucket, creating it
// on first use.
func (s *rateLimiterStore) chargeGlobal(now time.Time) (allow, created bool) {
	if s.globalOverflow == nil {
		s.globalOverflow = &rateLimiterBucket{limiter: rate.NewLimiter(s.rate, s.burst)}
		created = true
	}
	return s.globalOverflow.charge(now), created
}

// entryOf returns the entry a recency list element holds; the lists hold nothing
// else.
func entryOf(e *list.Element) *rateLimiterEntry {
	entry, _ := e.Value.(*rateLimiterEntry)
	return entry
}

// remove drops an entry, and its namespace when nothing is left in it. The
// caller holds s.mu.
func (s *rateLimiterStore) remove(entry *rateLimiterEntry) {
	s.recency.Remove(entry.global)
	entry.ns.entries.Remove(entry.local)
	delete(s.entries, entry.id)
	s.dropIfUnused(entry.ns)
}

// dropIfUnused deletes a namespace that has neither entries nor an overflow
// bucket.
func (s *rateLimiterStore) dropIfUnused(ns *rateLimiterNamespace) {
	if ns.entries.Len() == 0 && ns.overflow == nil {
		delete(s.namespaces, ns.name)
	}
}

// expire drops the entries and overflow buckets idle longer than ExpiresIn.
// Each list is ordered by last use, so it stops at the first fresh element and
// the work is amortised O(1) per request. The caller holds s.mu.
func (s *rateLimiterStore) expire(now time.Time) {
	for back := s.recency.Back(); back != nil; back = s.recency.Back() {
		entry := entryOf(back)
		if now.Sub(entry.lastSeen) <= s.expiresIn {
			break
		}
		s.remove(entry)
	}
	for back := s.overflowing.Back(); back != nil; back = s.overflowing.Back() {
		ns, _ := back.Value.(*rateLimiterNamespace)
		if now.Sub(ns.overflow.lastSeen) <= s.expiresIn {
			break
		}
		s.overflowing.Remove(back)
		ns.overflow, ns.overflowElem = nil, nil
		s.dropIfUnused(ns)
	}
	if s.globalOverflow != nil && now.Sub(s.globalOverflow.lastSeen) > s.expiresIn {
		s.globalOverflow = nil
	}
}
