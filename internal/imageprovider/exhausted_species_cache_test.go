// exhausted_species_cache_test.go: unit tests for the registry-adjacent
// "exhausted species" memory cache. These tests live in the internal package
// so they can construct a minimal BirdImageCache and manipulate the
// exhaustedSpecies map directly for TTL and concurrency edge cases.
package imageprovider

import (
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
)

// newMinimalCache returns a BirdImageCache with just enough state to exercise
// the exhausted-species helpers. It does not start background refresh and has
// no datastore or file cache.
func newMinimalCache(t *testing.T, providerName string) *BirdImageCache {
	t.Helper()
	return &BirdImageCache{
		providerName: providerName,
		quit:         make(chan struct{}),
	}
}

// TestRecordSpeciesExhausted verifies that recordSpeciesExhausted stores a
// timestamp that isSpeciesExhausted can observe.
func TestRecordSpeciesExhausted(t *testing.T) {
	t.Parallel()
	c := newMinimalCache(t, "avicommons")

	assert.False(t, c.isSpeciesExhausted("Siren"), "no entry should exist initially")

	c.recordSpeciesExhausted("Siren")
	assert.True(t, c.isSpeciesExhausted("Siren"), "entry should be observable after recording")
	assert.False(t, c.isSpeciesExhausted("Turdus merula"), "unrelated species should be unaffected")
}

// TestRecordSpeciesExhaustedEmptyName verifies that empty scientific names are
// silently ignored. This matches Get()'s empty-name guard and prevents poisoning
// the map with an unusable key.
func TestRecordSpeciesExhaustedEmptyName(t *testing.T) {
	t.Parallel()
	c := newMinimalCache(t, "avicommons")

	c.recordSpeciesExhausted("")
	assert.False(t, c.isSpeciesExhausted(""), "empty-name entries must not be stored")
}

// TestIsSpeciesExhaustedExpiresLazily backdates a stored entry past the TTL
// and verifies that isSpeciesExhausted reports false and removes the entry.
func TestIsSpeciesExhaustedExpiresLazily(t *testing.T) {
	t.Parallel()
	c := newMinimalCache(t, "avicommons")

	// Record an entry manually with a backdated timestamp so we do not have
	// to wait through a real TTL. This bypasses recordSpeciesExhausted
	// because it stamps the entry with the current time.
	stale := time.Now().Add(-exhaustedSpeciesTTL - time.Minute)
	c.exhaustedSpecies.Store("Siren", stale)

	assert.False(t, c.isSpeciesExhausted("Siren"), "stale entry must expire")

	// The entry should have been deleted by the lazy-expiration path.
	_, stillPresent := c.exhaustedSpecies.Load("Siren")
	assert.False(t, stillPresent, "stale entry should be evicted from the map")
}

// TestIsSpeciesExhaustedRejectsBogusType is a defensive check: if something
// stores a non-time.Time value in the map, isSpeciesExhausted must not panic
// and must drop the bogus entry.
func TestIsSpeciesExhaustedRejectsBogusType(t *testing.T) {
	t.Parallel()
	c := newMinimalCache(t, "avicommons")

	c.exhaustedSpecies.Store("Siren", "not a timestamp")
	assert.False(t, c.isSpeciesExhausted("Siren"))

	_, stillPresent := c.exhaustedSpecies.Load("Siren")
	assert.False(t, stillPresent, "bogus-type entry should be evicted")
}

// TestExhaustedSpeciesCacheConcurrentAccess verifies that concurrent callers
// can record and query exhaustion state without panicking or racing. The
// -race flag used by `go test -race` catches data races; the assertions
// verify that at least one record operation is visible at the end.
func TestExhaustedSpeciesCacheConcurrentAccess(t *testing.T) {
	t.Parallel()
	c := newMinimalCache(t, "avicommons")

	const (
		workers        = 64
		iterations     = 200
		scientificName = "Siren"
	)

	var wg sync.WaitGroup
	wg.Add(workers)
	for range workers {
		go func() {
			defer wg.Done()
			for range iterations {
				// Mix of record and query calls. If the map is not safe,
				// -race will flag it; if the logic is wrong, the assertion
				// after Wait will fail.
				c.recordSpeciesExhausted(scientificName)
				_ = c.isSpeciesExhausted(scientificName)
			}
		}()
	}
	wg.Wait()

	assert.True(t, c.isSpeciesExhausted(scientificName),
		"after concurrent writes the entry should be present")
}

// TestSynthesizeExhaustedResponse verifies the short-circuit response carries
// ErrImageNotFound so callers can rely on the existing error type.
func TestSynthesizeExhaustedResponse(t *testing.T) {
	t.Parallel()
	c := newMinimalCache(t, "avicommons")

	img, err := c.synthesizeExhaustedResponse("Siren")
	assert.Empty(t, img.URL, "response image should be empty")
	assert.ErrorIs(t, err, ErrImageNotFound,
		"response error must match ErrImageNotFound so existing callers unwrap correctly")
}
