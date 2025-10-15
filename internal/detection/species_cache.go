package detection

import (
	"context"
	"fmt"
	"sync"
	"time"
)

// maxSpeciesBatchSize is the maximum number of species to load in a single batch.
// This limit accommodates the global bird taxonomy (approximately 10,000 known species)
// with ample headroom for future growth and regional subspecies variations.
const maxSpeciesBatchSize = 100000

// SpeciesCache provides fast in-memory lookup for species data.
// Species information rarely changes, making caching highly effective.
// The cache supports multiple lookup indexes for different access patterns.
type SpeciesCache struct {
	mu           sync.RWMutex
	byID         map[uint]*Species
	byScientific map[string]*Species
	byEbird      map[string]*Species
	ttl          time.Duration
	lastLoad     time.Time
	repo         SpeciesRepository
}

// NewSpeciesCache creates a new species cache with the given repository and TTL.
// The cache will automatically refresh after the TTL expires.
func NewSpeciesCache(repo SpeciesRepository, ttl time.Duration) *SpeciesCache {
	return &SpeciesCache{
		byID:         make(map[uint]*Species),
		byScientific: make(map[string]*Species),
		byEbird:      make(map[string]*Species),
		ttl:          ttl,
		repo:         repo,
	}
}

// GetByID retrieves species by database ID.
// Uses cache with fallback to database.
func (c *SpeciesCache) GetByID(ctx context.Context, id uint) (*Species, error) {
	// Check cache with read lock
	c.mu.RLock()
	if species, ok := c.byID[id]; ok {
		c.mu.RUnlock()
		return species, nil
	}
	c.mu.RUnlock()

	// Load from database with write lock
	c.mu.Lock()
	defer c.mu.Unlock()

	// Double-check after acquiring write lock (another goroutine may have loaded)
	if species, ok := c.byID[id]; ok {
		return species, nil
	}

	// Load from repository
	species, err := c.repo.GetByID(ctx, id)
	if err != nil {
		return nil, fmt.Errorf("failed to load species by ID %d: %w", id, err)
	}

	// Update all cache indexes
	c.cache(species)

	return species, nil
}

// GetByScientificName retrieves species by scientific name.
// Uses cache with fallback to database.
func (c *SpeciesCache) GetByScientificName(ctx context.Context, name string) (*Species, error) {
	// Check cache with read lock
	c.mu.RLock()
	if species, ok := c.byScientific[name]; ok {
		c.mu.RUnlock()
		return species, nil
	}
	c.mu.RUnlock()

	// Load from database with write lock
	c.mu.Lock()
	defer c.mu.Unlock()

	// Double-check after acquiring write lock
	if species, ok := c.byScientific[name]; ok {
		return species, nil
	}

	// Load from repository
	species, err := c.repo.GetByScientificName(ctx, name)
	if err != nil {
		return nil, fmt.Errorf("failed to load species by scientific name %q: %w", name, err)
	}

	// Update all cache indexes
	c.cache(species)

	return species, nil
}

// GetByEbirdCode retrieves species by eBird taxonomy code.
// Uses cache with fallback to database.
func (c *SpeciesCache) GetByEbirdCode(ctx context.Context, code string) (*Species, error) {
	// Check cache with read lock
	c.mu.RLock()
	if species, ok := c.byEbird[code]; ok {
		c.mu.RUnlock()
		return species, nil
	}
	c.mu.RUnlock()

	// Load from database with write lock
	c.mu.Lock()
	defer c.mu.Unlock()

	// Double-check after acquiring write lock
	if species, ok := c.byEbird[code]; ok {
		return species, nil
	}

	// Load from repository
	species, err := c.repo.GetByEbirdCode(ctx, code)
	if err != nil {
		return nil, fmt.Errorf("failed to load species by eBird code %q: %w", code, err)
	}

	// Update all cache indexes
	c.cache(species)

	return species, nil
}

// GetOrCreate retrieves species from cache or creates it if not found.
// This is useful during detection processing where species may not exist yet.
func (c *SpeciesCache) GetOrCreate(ctx context.Context, species *Species) (*Species, error) {
	// Try cache lookup first by scientific name
	if species.ScientificName != "" {
		c.mu.RLock()
		if cached, ok := c.byScientific[species.ScientificName]; ok {
			c.mu.RUnlock()
			return cached, nil
		}
		c.mu.RUnlock()
	}

	// Not in cache, delegate to repository
	c.mu.Lock()
	defer c.mu.Unlock()

	// Double-check after acquiring write lock
	if species.ScientificName != "" {
		if cached, ok := c.byScientific[species.ScientificName]; ok {
			return cached, nil
		}
	}

	// Create via repository
	created, err := c.repo.GetOrCreate(ctx, species)
	if err != nil {
		return nil, fmt.Errorf("failed to get or create species: %w", err)
	}

	// Cache the result
	c.cache(created)

	return created, nil
}

// cache updates all cache indexes for a species.
// Must be called with write lock held.
func (c *SpeciesCache) cache(species *Species) {
	if species == nil {
		return
	}

	// Update all indexes
	if species.ID != 0 {
		c.byID[species.ID] = species
	}
	if species.ScientificName != "" {
		c.byScientific[species.ScientificName] = species
	}
	if species.SpeciesCode != "" {
		c.byEbird[species.SpeciesCode] = species
	}
}

// Invalidate clears the entire cache.
// This should be called when species data is updated outside this cache.
func (c *SpeciesCache) Invalidate() {
	c.mu.Lock()
	defer c.mu.Unlock()

	c.byID = make(map[uint]*Species)
	c.byScientific = make(map[string]*Species)
	c.byEbird = make(map[string]*Species)
	c.lastLoad = time.Time{}
}

// Refresh reloads all species from the database into the cache.
// This is useful for periodic cache updates.
func (c *SpeciesCache) Refresh(ctx context.Context) error {
	c.mu.Lock()
	defer c.mu.Unlock()

	// Load all species from repository
	species, err := c.repo.List(ctx, maxSpeciesBatchSize, 0)
	if err != nil {
		return fmt.Errorf("failed to refresh species cache: %w", err)
	}

	// Clear existing cache
	c.byID = make(map[uint]*Species)
	c.byScientific = make(map[string]*Species)
	c.byEbird = make(map[string]*Species)

	// Repopulate cache
	for _, s := range species {
		c.cache(s)
	}

	c.lastLoad = time.Now()

	return nil
}

// IsExpired checks if the cache has expired based on TTL.
func (c *SpeciesCache) IsExpired() bool {
	c.mu.RLock()
	defer c.mu.RUnlock()

	if c.lastLoad.IsZero() {
		return true
	}

	return time.Since(c.lastLoad) > c.ttl
}

// Size returns the number of species in the cache.
func (c *SpeciesCache) Size() int {
	c.mu.RLock()
	defer c.mu.RUnlock()

	return len(c.byScientific)
}

// Stats returns cache statistics for monitoring.
type CacheStats struct {
	Size         int
	LastLoad     time.Time
	IsExpired    bool
	ByIDCount    int
	ByEbirdCount int
}

// Stats returns current cache statistics.
func (c *SpeciesCache) Stats() CacheStats {
	c.mu.RLock()
	defer c.mu.RUnlock()

	return CacheStats{
		Size:         len(c.byScientific),
		LastLoad:     c.lastLoad,
		IsExpired:    time.Since(c.lastLoad) > c.ttl,
		ByIDCount:    len(c.byID),
		ByEbirdCount: len(c.byEbird),
	}
}
