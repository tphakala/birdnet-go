package conf

import (
	"sync"
	"testing"
	"time"
)

// TestShouldUpdateRangeFilterToday_SingleThread verifies basic functionality
func TestShouldUpdateRangeFilterToday_SingleThread(t *testing.T) {
	settings := &Settings{}
	settings.BirdNET.RangeFilter.LastUpdated = time.Now().Add(-25 * time.Hour) // Yesterday

	// First call should return true
	if !settings.ShouldUpdateRangeFilterToday() {
		t.Error("First call should return true when LastUpdated is yesterday")
	}

	// Second call should return false (already updated today)
	if settings.ShouldUpdateRangeFilterToday() {
		t.Error("Second call should return false (already updated today)")
	}
}

// TestShouldUpdateRangeFilterToday_ConcurrentAccess tests for race conditions
// This is the critical test that would fail with the old implementation
func TestShouldUpdateRangeFilterToday_ConcurrentAccess(t *testing.T) {
	settings := &Settings{}
	settings.BirdNET.RangeFilter.LastUpdated = time.Now().Add(-25 * time.Hour) // Yesterday

	const numGoroutines = 100
	var wg sync.WaitGroup
	trueCount := 0
	var mu sync.Mutex

	// Launch multiple goroutines that all check if update is needed
	for i := 0; i < numGoroutines; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			if settings.ShouldUpdateRangeFilterToday() {
				mu.Lock()
				trueCount++
				mu.Unlock()
			}
		}()
	}

	wg.Wait()

	// CRITICAL: Only ONE goroutine should have received true
	// This prevents the bug in issue #1357 where multiple goroutines
	// would all create UpdateRangeFilterAction, causing race conditions
	if trueCount != 1 {
		t.Errorf("Expected exactly 1 goroutine to receive true, got %d", trueCount)
	}
}

// TestShouldUpdateRangeFilterToday_AlreadyUpdated verifies that when
// LastUpdated is already today, no updates are triggered
func TestShouldUpdateRangeFilterToday_AlreadyUpdated(t *testing.T) {
	settings := &Settings{}
	settings.BirdNET.RangeFilter.LastUpdated = time.Now() // Already updated today

	if settings.ShouldUpdateRangeFilterToday() {
		t.Error("Should return false when already updated today")
	}
}

// TestGetLastRangeFilterUpdate verifies thread-safe reading of LastUpdated
func TestGetLastRangeFilterUpdate(t *testing.T) {
	settings := &Settings{}
	expectedTime := time.Now().Add(-1 * time.Hour)
	settings.BirdNET.RangeFilter.LastUpdated = expectedTime

	// Read from multiple goroutines concurrently
	const numReaders = 50
	var wg sync.WaitGroup

	for i := 0; i < numReaders; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			got := settings.GetLastRangeFilterUpdate()
			if !got.Equal(expectedTime) {
				t.Errorf("Expected %v, got %v", expectedTime, got)
			}
		}()
	}

	wg.Wait()
}

// TestUpdateIncludedSpecies_ThreadSafety verifies concurrent updates are safe
func TestUpdateIncludedSpecies_ThreadSafety(t *testing.T) {
	settings := &Settings{}

	const numGoroutines = 50
	var wg sync.WaitGroup

	// Concurrently update species lists
	for i := 0; i < numGoroutines; i++ {
		wg.Add(1)
		go func(id int) {
			defer wg.Done()
			species := []string{"Species A", "Species B"}
			settings.UpdateIncludedSpecies(species)
		}(i)
	}

	wg.Wait()

	// Verify the final state is valid
	species := settings.GetIncludedSpecies()
	if len(species) != 2 {
		t.Errorf("Expected 2 species, got %d", len(species))
	}
}

// TestIsSpeciesIncluded_ThreadSafety verifies concurrent reads during updates
func TestIsSpeciesIncluded_ThreadSafety(t *testing.T) {
	settings := &Settings{}
	settings.UpdateIncludedSpecies([]string{
		"Turdus merula_Eurasian Blackbird",
		"Parus major_Great Tit",
	})

	const numReaders = 100
	var wg sync.WaitGroup

	// Start readers
	for i := 0; i < numReaders; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			// These should consistently return true
			if !settings.IsSpeciesIncluded("Turdus merula") {
				t.Error("Expected species to be included")
			}
		}()
	}

	// While reading, also update the list
	wg.Add(1)
	go func() {
		defer wg.Done()
		time.Sleep(1 * time.Millisecond) // Let some readers start
		settings.UpdateIncludedSpecies([]string{
			"Corvus cornix_Hooded Crow",
		})
	}()

	wg.Wait()
}
