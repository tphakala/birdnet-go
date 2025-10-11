package conf

import (
	"sync"
	"testing"
	"time"
)

// TestShouldUpdateRangeFilterToday_SingleThread verifies basic functionality
func TestShouldUpdateRangeFilterToday_SingleThread(t *testing.T) {
	t.Parallel()

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
	t.Parallel()

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
	t.Parallel()

	settings := &Settings{}
	settings.BirdNET.RangeFilter.LastUpdated = time.Now() // Already updated today

	if settings.ShouldUpdateRangeFilterToday() {
		t.Error("Should return false when already updated today")
	}
}

// TestGetLastRangeFilterUpdate verifies thread-safe reading of LastUpdated
func TestGetLastRangeFilterUpdate(t *testing.T) {
	t.Parallel()

	settings := &Settings{}
	expectedTime := time.Now().Add(-1 * time.Hour)
	settings.BirdNET.RangeFilter.LastUpdated = expectedTime

	// Read from multiple goroutines concurrently
	const numReaders = 50
	var wg sync.WaitGroup
	var mu sync.Mutex
	var errors []string

	for i := 0; i < numReaders; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			got := settings.GetLastRangeFilterUpdate()
			if !got.Equal(expectedTime) {
				mu.Lock()
				errors = append(errors, "Expected time mismatch")
				mu.Unlock()
			}
		}()
	}

	wg.Wait()

	if len(errors) > 0 {
		t.Errorf("Concurrent reads failed: found %d errors", len(errors))
	}
}

// TestUpdateIncludedSpecies_ThreadSafety verifies concurrent updates are safe
func TestUpdateIncludedSpecies_ThreadSafety(t *testing.T) {
	t.Parallel()

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
	t.Parallel()

	settings := &Settings{}
	settings.UpdateIncludedSpecies([]string{
		"Turdus merula_Eurasian Blackbird",
		"Parus major_Great Tit",
	})

	const numReaders = 100
	var wg sync.WaitGroup
	var mu sync.Mutex
	var errors []string

	// Start readers
	for i := 0; i < numReaders; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			// These should consistently return true
			if !settings.IsSpeciesIncluded("Turdus merula") {
				mu.Lock()
				errors = append(errors, "Expected species to be included")
				mu.Unlock()
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

	if len(errors) > 0 {
		t.Errorf("Concurrent reads failed: found %d errors", len(errors))
	}
}

// TestResetRangeFilterUpdateFlag verifies that ResetRangeFilterUpdateFlag
// correctly resets the LastUpdated timestamp to allow retries
func TestResetRangeFilterUpdateFlag(t *testing.T) {
	t.Parallel()

	settings := &Settings{}
	settings.BirdNET.RangeFilter.LastUpdated = time.Now()

	// Verify it's set
	if settings.BirdNET.RangeFilter.LastUpdated.IsZero() {
		t.Error("LastUpdated should not be zero initially")
	}

	// Reset the flag
	settings.ResetRangeFilterUpdateFlag()

	// Verify it's been reset to zero time
	if !settings.BirdNET.RangeFilter.LastUpdated.IsZero() {
		t.Error("LastUpdated should be zero after reset")
	}

	// Verify that after reset, ShouldUpdateRangeFilterToday returns true
	if !settings.ShouldUpdateRangeFilterToday() {
		t.Error("ShouldUpdateRangeFilterToday should return true after reset")
	}
}

// TestResetRangeFilterUpdateFlag_ThreadSafety verifies concurrent resets are safe
func TestResetRangeFilterUpdateFlag_ThreadSafety(t *testing.T) {
	t.Parallel()

	settings := &Settings{}
	settings.BirdNET.RangeFilter.LastUpdated = time.Now()

	const numGoroutines = 50
	var wg sync.WaitGroup

	// Concurrently reset the flag
	for i := 0; i < numGoroutines; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			settings.ResetRangeFilterUpdateFlag()
		}()
	}

	wg.Wait()

	// Verify the final state is zero time
	if !settings.BirdNET.RangeFilter.LastUpdated.IsZero() {
		t.Error("LastUpdated should be zero after concurrent resets")
	}
}

// TestErrorRecoveryScenario simulates the full error recovery flow:
// 1. Update is scheduled (ShouldUpdateRangeFilterToday returns true)
// 2. Update fails (simulated by ResetRangeFilterUpdateFlag)
// 3. Next detection should be able to retry (ShouldUpdateRangeFilterToday returns true again)
func TestErrorRecoveryScenario(t *testing.T) {
	t.Parallel()

	settings := &Settings{}
	settings.BirdNET.RangeFilter.LastUpdated = time.Now().Add(-25 * time.Hour) // Yesterday

	// First detection: should trigger update
	if !settings.ShouldUpdateRangeFilterToday() {
		t.Error("First call should return true when LastUpdated is yesterday")
	}

	// Simulate update failure by resetting the flag
	// (In production, this is called in UpdateRangeFilterAction.Execute on error)
	settings.ResetRangeFilterUpdateFlag()

	// Next detection: should be able to retry
	if !settings.ShouldUpdateRangeFilterToday() {
		t.Error("Should return true after failed update (reset flag)")
	}

	// Simulate successful update by calling UpdateIncludedSpecies
	settings.UpdateIncludedSpecies([]string{"Test Species"})

	// Next detection: should NOT trigger update (already succeeded)
	if settings.ShouldUpdateRangeFilterToday() {
		t.Error("Should return false after successful update")
	}
}

// TestErrorRecoveryScenario_Concurrent simulates concurrent error recovery
// This ensures that even with concurrent resets and checks, only one goroutine
// will trigger the retry
func TestErrorRecoveryScenario_Concurrent(t *testing.T) {
	t.Parallel()

	settings := &Settings{}
	settings.BirdNET.RangeFilter.LastUpdated = time.Now() // Set to today

	// Simulate a failed update by resetting the flag
	settings.ResetRangeFilterUpdateFlag()

	const numGoroutines = 100
	var wg sync.WaitGroup
	trueCount := 0
	var mu sync.Mutex

	// Launch multiple goroutines that all check if retry is needed
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
	// This ensures that even after a reset, we don't get duplicate retries
	if trueCount != 1 {
		t.Errorf("Expected exactly 1 goroutine to receive true after reset, got %d", trueCount)
	}
}

// TestResetAndCheckInterleaved tests interleaved reset and check operations
// to ensure they work correctly together under concurrent access
func TestResetAndCheckInterleaved(t *testing.T) {
	t.Parallel()

	settings := &Settings{}
	settings.BirdNET.RangeFilter.LastUpdated = time.Now()

	const numIterations = 10
	var wg sync.WaitGroup

	// Goroutine 1: Repeatedly resets
	wg.Add(1)
	go func() {
		defer wg.Done()
		for i := 0; i < numIterations; i++ {
			settings.ResetRangeFilterUpdateFlag()
			time.Sleep(1 * time.Millisecond)
		}
	}()

	// Goroutine 2: Repeatedly checks
	wg.Add(1)
	go func() {
		defer wg.Done()
		for i := 0; i < numIterations; i++ {
			settings.ShouldUpdateRangeFilterToday()
			time.Sleep(1 * time.Millisecond)
		}
	}()

	// Goroutine 3: Repeatedly reads
	wg.Add(1)
	go func() {
		defer wg.Done()
		for i := 0; i < numIterations; i++ {
			settings.GetLastRangeFilterUpdate()
			time.Sleep(1 * time.Millisecond)
		}
	}()

	wg.Wait()
	// If we reach here without deadlock or panic, the test passes
}
