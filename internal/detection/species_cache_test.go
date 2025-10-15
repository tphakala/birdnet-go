package detection

import (
	"context"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// Note: MockSpeciesRepository has been moved to testing.go and is now exported
// for reuse in Phase 2 integration testing.

func TestSpeciesCache_GetByScientificName(t *testing.T) {
	repo := NewMockSpeciesRepository()
	cache := NewSpeciesCache(repo, time.Hour)

	species := &Species{
		ID:             1,
		SpeciesCode:    "amecro",
		ScientificName: "Corvus brachyrhynchos",
		CommonName:     "American Crow",
	}
	repo.AddSpecies(species)

	ctx := context.Background()

	// First call - cache miss, should hit database
	result, err := cache.GetByScientificName(ctx, "Corvus brachyrhynchos")
	require.NoError(t, err)
	assert.Equal(t, species.ScientificName, result.ScientificName)
	assert.Equal(t, 1, repo.CallCount("GetByScientificName"))

	// Second call - cache hit, should NOT hit database
	result, err = cache.GetByScientificName(ctx, "Corvus brachyrhynchos")
	require.NoError(t, err)
	assert.Equal(t, species.ScientificName, result.ScientificName)
	assert.Equal(t, 1, repo.CallCount("GetByScientificName")) // Still 1!
}

func TestSpeciesCache_GetByID(t *testing.T) {
	repo := NewMockSpeciesRepository()
	cache := NewSpeciesCache(repo, time.Hour)

	species := &Species{
		ID:             42,
		SpeciesCode:    "norcar",
		ScientificName: "Cardinalis cardinalis", //nolint:misspell // Latin species name
		CommonName:     "Northern Cardinal",
	}
	repo.AddSpecies(species)

	ctx := context.Background()

	// First call - cache miss
	result, err := cache.GetByID(ctx, 42)
	require.NoError(t, err)
	assert.Equal(t, species.ID, result.ID)
	assert.Equal(t, 1, repo.CallCount("GetByID"))

	// Second call - cache hit
	result, err = cache.GetByID(ctx, 42)
	require.NoError(t, err)
	assert.Equal(t, species.ID, result.ID)
	assert.Equal(t, 1, repo.CallCount("GetByID"))
}

func TestSpeciesCache_GetByEbirdCode(t *testing.T) {
	repo := NewMockSpeciesRepository()
	cache := NewSpeciesCache(repo, time.Hour)

	species := &Species{
		ID:             99,
		SpeciesCode:    "rebwoo",
		ScientificName: "Melanerpes carolinus",
		CommonName:     "Red-bellied Woodpecker",
	}
	repo.AddSpecies(species)

	ctx := context.Background()

	// First call - cache miss
	result, err := cache.GetByEbirdCode(ctx, "rebwoo")
	require.NoError(t, err)
	assert.Equal(t, species.SpeciesCode, result.SpeciesCode)
	assert.Equal(t, 1, repo.CallCount("GetByEbirdCode"))

	// Second call - cache hit
	result, err = cache.GetByEbirdCode(ctx, "rebwoo")
	require.NoError(t, err)
	assert.Equal(t, species.SpeciesCode, result.SpeciesCode)
	assert.Equal(t, 1, repo.CallCount("GetByEbirdCode"))
}

func TestSpeciesCache_ConcurrentAccess(t *testing.T) {
	repo := NewMockSpeciesRepository()
	cache := NewSpeciesCache(repo, time.Hour)

	species := &Species{
		ID:             1,
		SpeciesCode:    "amecro",
		ScientificName: "Corvus brachyrhynchos",
		CommonName:     "American Crow",
	}
	repo.AddSpecies(species)

	ctx := context.Background()

	// Launch 100 concurrent goroutines
	var wg sync.WaitGroup
	for i := 0; i < 100; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			result, err := cache.GetByScientificName(ctx, "Corvus brachyrhynchos")
			assert.NoError(t, err)
			assert.Equal(t, species.ScientificName, result.ScientificName)
		}()
	}
	wg.Wait()

	// Should only call database once despite 100 concurrent requests
	// (some may race, so allow a small number of extra calls)
	assert.LessOrEqual(t, repo.CallCount("GetByScientificName"), 10)
}

func TestSpeciesCache_GetOrCreate(t *testing.T) {
	repo := NewMockSpeciesRepository()
	cache := NewSpeciesCache(repo, time.Hour)

	ctx := context.Background()

	// Create new species
	newSpecies := &Species{
		SpeciesCode:    "easblu",
		ScientificName: "Sialia sialis",
		CommonName:     "Eastern Bluebird",
	}

	result, err := cache.GetOrCreate(ctx, newSpecies)
	require.NoError(t, err)
	assert.NotZero(t, result.ID) // ID assigned by repo
	assert.Equal(t, newSpecies.ScientificName, result.ScientificName)

	// Second call with same species should return cached
	result2, err := cache.GetOrCreate(ctx, newSpecies)
	require.NoError(t, err)
	assert.Equal(t, result.ID, result2.ID)

	// Should only call GetOrCreate once per unique species
	assert.Equal(t, 1, repo.CallCount("GetOrCreate"))
}

func TestSpeciesCache_GetOrCreate_ByCodeOnly(t *testing.T) {
	repo := NewMockSpeciesRepository()
	cache := NewSpeciesCache(repo, time.Hour)

	ctx := context.Background()

	// Create species with only eBird code (empty scientific name)
	newSpecies := &Species{
		SpeciesCode: "easblu",
		CommonName:  "Eastern Bluebird",
		// ScientificName intentionally empty
	}

	// GetOrCreate should fail because mock requires scientificName
	_, err := cache.GetOrCreate(ctx, newSpecies)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "scientificName is required")

	// Now test with proper eBird code lookup after adding to repo
	properSpecies := &Species{
		ID:             50,
		SpeciesCode:    "rebwoo",
		ScientificName: "Melanerpes carolinus",
		CommonName:     "Red-bellied Woodpecker",
	}
	repo.AddSpecies(properSpecies)

	// Subsequent lookup by code should find it via cache
	got, err := cache.GetByEbirdCode(ctx, "rebwoo")
	require.NoError(t, err)
	assert.Equal(t, properSpecies.ID, got.ID)
	assert.Equal(t, "Melanerpes carolinus", got.ScientificName)
}

func TestSpeciesCache_Invalidate(t *testing.T) {
	repo := NewMockSpeciesRepository()
	cache := NewSpeciesCache(repo, time.Hour)

	species := &Species{
		ID:             1,
		ScientificName: "Test Species",
	}
	repo.AddSpecies(species)

	ctx := context.Background()

	// Load into cache
	_, err := cache.GetByScientificName(ctx, "Test Species")
	require.NoError(t, err)
	assert.Equal(t, 1, cache.Size())

	// Invalidate cache
	cache.Invalidate()
	assert.Equal(t, 0, cache.Size())

	// Next call should hit database again
	_, err = cache.GetByScientificName(ctx, "Test Species")
	require.NoError(t, err)
	assert.Equal(t, 2, repo.CallCount("GetByScientificName"))
}

func TestSpeciesCache_Refresh(t *testing.T) {
	repo := NewMockSpeciesRepository()
	cache := NewSpeciesCache(repo, time.Hour)

	// Add some species to repo
	repo.AddSpecies(&Species{ID: 1, ScientificName: "Species 1"})
	repo.AddSpecies(&Species{ID: 2, ScientificName: "Species 2"})
	repo.AddSpecies(&Species{ID: 3, ScientificName: "Species 3"})

	ctx := context.Background()

	// Refresh cache
	err := cache.Refresh(ctx)
	require.NoError(t, err)

	// Cache should contain all species
	assert.Equal(t, 3, cache.Size())

	// Should be able to access all species without database calls
	_, err = cache.GetByScientificName(ctx, "Species 1")
	require.NoError(t, err)
	_, err = cache.GetByScientificName(ctx, "Species 2")
	require.NoError(t, err)
	_, err = cache.GetByScientificName(ctx, "Species 3")
	require.NoError(t, err)

	// Only the List call from Refresh, no GetByScientificName calls
	assert.Equal(t, 1, repo.CallCount("List"))
	assert.Equal(t, 0, repo.CallCount("GetByScientificName"))
}

func TestSpeciesCache_Stats(t *testing.T) {
	repo := NewMockSpeciesRepository()
	cache := NewSpeciesCache(repo, time.Hour)

	// Initially empty
	stats := cache.Stats()
	assert.Equal(t, 0, stats.Size)
	assert.True(t, stats.IsExpired) // No data loaded yet

	// Add some data
	repo.AddSpecies(&Species{ID: 1, ScientificName: "Species 1", SpeciesCode: "sp1"})
	repo.AddSpecies(&Species{ID: 2, ScientificName: "Species 2", SpeciesCode: "sp2"})

	ctx := context.Background()
	err := cache.Refresh(ctx)
	require.NoError(t, err)

	stats = cache.Stats()
	assert.Equal(t, 2, stats.Size)
	assert.Equal(t, 2, stats.ByIDCount)
	assert.Equal(t, 2, stats.ByEbirdCount)
	assert.False(t, stats.IsExpired)
}

func TestSpeciesCache_IsExpired(t *testing.T) {
	repo := NewMockSpeciesRepository()

	// Create cache with very short TTL
	cache := NewSpeciesCache(repo, 10*time.Millisecond)

	// Initially expired (no data)
	assert.True(t, cache.IsExpired())

	// Load data
	repo.AddSpecies(&Species{ID: 1, ScientificName: "Test"})
	ctx := context.Background()
	err := cache.Refresh(ctx)
	require.NoError(t, err)

	// Should not be expired immediately
	assert.False(t, cache.IsExpired())

	// Wait for TTL to expire
	time.Sleep(20 * time.Millisecond)

	// Should now be expired
	assert.True(t, cache.IsExpired())
}

func TestSpeciesCache_MultipleIndexes(t *testing.T) {
	repo := NewMockSpeciesRepository()
	cache := NewSpeciesCache(repo, time.Hour)

	species := &Species{
		ID:             100,
		SpeciesCode:    "test",
		ScientificName: "Testus testus",
		CommonName:     "Test Bird",
	}
	repo.AddSpecies(species)

	ctx := context.Background()

	// Load via scientific name
	_, err := cache.GetByScientificName(ctx, "Testus testus")
	require.NoError(t, err)

	// Now should be able to access by ID and eBird code without database calls
	resultByID, err := cache.GetByID(ctx, 100)
	require.NoError(t, err)
	assert.Equal(t, species.ScientificName, resultByID.ScientificName)

	resultByCode, err := cache.GetByEbirdCode(ctx, "test")
	require.NoError(t, err)
	assert.Equal(t, species.ScientificName, resultByCode.ScientificName)

	// Verify only one database call (initial load)
	assert.Equal(t, 1, repo.CallCount("GetByScientificName"))
	assert.Equal(t, 0, repo.CallCount("GetByID"))
	assert.Equal(t, 0, repo.CallCount("GetByEbirdCode"))
}

func TestSpeciesCache_ErrorHandling(t *testing.T) {
	repo := NewMockSpeciesRepository()
	cache := NewSpeciesCache(repo, time.Hour)

	ctx := context.Background()

	// Enable error mode
	repo.SetShouldFail(true)

	// Should propagate errors
	_, err := cache.GetByScientificName(ctx, "Nonexistent")
	require.Error(t, err)

	_, err = cache.GetByID(ctx, 999)
	require.Error(t, err)

	_, err = cache.GetByEbirdCode(ctx, "xxx")
	require.Error(t, err)
}
