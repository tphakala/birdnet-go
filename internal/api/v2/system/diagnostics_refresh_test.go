package system

import (
	"testing"

	"github.com/stretchr/testify/assert"

	"github.com/tphakala/birdnet-go/internal/datastore/mocks"
)

// invalidatingDatastore is a datastore.Interface (via the generated mock, which
// needs no expectations because refreshIntegrityCache only type-asserts and calls
// InvalidateIntegrityCache) that additionally records integrity-cache
// invalidations, mirroring what the v2 datastore exposes.
type invalidatingDatastore struct {
	*mocks.MockInterface
	invalidations int
}

func (d *invalidatingDatastore) InvalidateIntegrityCache() { d.invalidations++ }

// TestRefreshIntegrityCache covers the item-5 opt-in wiring: an explicit
// refresh_integrity=true clears the cached integrity result so RunDiagnostics
// reflects current database integrity, while the default path preserves the 24h
// cache (keeping the passive status page cheap, #3939).
func TestRefreshIntegrityCache(t *testing.T) {
	t.Parallel()

	t.Run("refresh true invalidates a caching datastore", func(t *testing.T) {
		t.Parallel()
		ds := &invalidatingDatastore{MockInterface: mocks.NewMockInterface(t)}
		refreshIntegrityCache(ds, true)
		assert.Equal(t, 1, ds.invalidations, "an explicit refresh clears the cached integrity result")
	})

	t.Run("refresh false leaves the cache alone", func(t *testing.T) {
		t.Parallel()
		ds := &invalidatingDatastore{MockInterface: mocks.NewMockInterface(t)}
		refreshIntegrityCache(ds, false)
		assert.Zero(t, ds.invalidations, "without a refresh the cached result is preserved")
	})

	t.Run("datastore without invalidation support is a no-op", func(t *testing.T) {
		t.Parallel()
		// A plain mock implements datastore.Interface but not
		// integrityCacheInvalidator, so the assertion misses and nothing happens.
		assert.NotPanics(t, func() {
			refreshIntegrityCache(mocks.NewMockInterface(t), true)
		})
	})

	t.Run("nil datastore is a no-op", func(t *testing.T) {
		t.Parallel()
		assert.NotPanics(t, func() {
			refreshIntegrityCache(nil, true)
		})
	})
}
