package speciesindex

import (
	"sync"
	"sync/atomic"

	"github.com/tphakala/birdnet-go/internal/datastore"
)

// Service owns the current Snapshot behind an atomic.Pointer for lock-free
// reads. Rebuild swaps in a freshly built Snapshot under buildMu; readers using
// Snapshot are never blocked by a rebuild. The resolver is held separately (also
// atomically) because both original sites install it after construction.
type Service struct {
	cur      atomic.Pointer[Snapshot]
	buildMu  sync.Mutex
	resolver atomic.Pointer[datastore.SpeciesNameResolver]
}

// New returns a Service seeded with the empty snapshot and the given resolver
// (a nil resolver is ignored, matching both call sites). Snapshot is safe to
// call before the first Rebuild; it returns Empty().
func New(resolver datastore.SpeciesNameResolver) *Service {
	s := &Service{}
	s.cur.Store(Empty())
	if !datastore.IsNilResolver(resolver) {
		r := resolver
		s.resolver.Store(&r)
	}
	return s
}

// SetResolver installs the authoritative localized name resolver. A nil resolver
// is ignored (mirrors both sites' SetNameResolver). The change takes effect on
// the next Rebuild.
func (s *Service) SetResolver(r datastore.SpeciesNameResolver) {
	if datastore.IsNilResolver(r) {
		return
	}
	rr := r
	s.resolver.Store(&rr)
}

// Resolver returns the installed resolver, or nil if none has been set.
func (s *Service) Resolver() datastore.SpeciesNameResolver {
	if p := s.resolver.Load(); p != nil {
		return *p
	}
	return nil
}

// Rebuild builds a new snapshot from labels, the current resolver and locale,
// then atomically publishes it. It is serialized by buildMu and runs the build
// outside every caller lock, so concurrent Snapshot readers are never blocked.
func (s *Service) Rebuild(labels []string, locale string) {
	s.buildMu.Lock()
	defer s.buildMu.Unlock()
	snap := Build(labels, s.Resolver(), locale)
	s.cur.Store(snap)
}

// Snapshot returns the current snapshot. It never returns nil: Empty() before
// the first Rebuild, and never nil after.
func (s *Service) Snapshot() *Snapshot {
	if snap := s.cur.Load(); snap != nil {
		return snap
	}
	return Empty()
}
