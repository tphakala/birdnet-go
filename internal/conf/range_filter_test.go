package conf

import (
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// setupGlobalSettings publishes a test settings snapshot and returns a
// cleanup function that restores the previous snapshot.
func setupGlobalSettings(t *testing.T, s *Settings) {
	t.Helper()
	prev := GetSettings()
	StoreSettings(s)
	t.Cleanup(func() { StoreSettings(prev) })
}

// TestShouldUpdateRangeFilterToday_SingleThread verifies basic functionality
func TestShouldUpdateRangeFilterToday_SingleThread(t *testing.T) {
	settings := &Settings{}
	settings.BirdNET.RangeFilter.LastUpdated = time.Now().Add(-25 * time.Hour)
	setupGlobalSettings(t, settings)

	assert.True(t, ShouldUpdateRangeFilterToday(), "First call should return true when LastUpdated is yesterday")
	assert.False(t, ShouldUpdateRangeFilterToday(), "Second call should return false (already updated today)")
}

// TestShouldUpdateRangeFilterToday_ConcurrentAccess tests for race conditions.
// Only one goroutine should get true per day (GitHub issue #1357).
func TestShouldUpdateRangeFilterToday_ConcurrentAccess(t *testing.T) {
	settings := &Settings{}
	settings.BirdNET.RangeFilter.LastUpdated = time.Now().Add(-25 * time.Hour)
	setupGlobalSettings(t, settings)

	const numGoroutines = 100
	var wg sync.WaitGroup
	trueCount := 0
	var mu sync.Mutex

	for range numGoroutines {
		wg.Go(func() {
			if ShouldUpdateRangeFilterToday() {
				mu.Lock()
				trueCount++
				mu.Unlock()
			}
		})
	}

	wg.Wait()
	assert.Equal(t, 1, trueCount, "Expected exactly 1 goroutine to receive true")
}

// TestShouldUpdateRangeFilterToday_AlreadyUpdated verifies that when
// LastUpdated is already today, no updates are triggered.
func TestShouldUpdateRangeFilterToday_AlreadyUpdated(t *testing.T) {
	settings := &Settings{}
	settings.BirdNET.RangeFilter.LastUpdated = time.Now()
	setupGlobalSettings(t, settings)

	assert.False(t, ShouldUpdateRangeFilterToday(), "Should return false when already updated today")
}

// TestShouldUpdateRangeFilterToday_PublishesNewSnapshot verifies that the
// function publishes a new snapshot with LastUpdated set to today.
func TestShouldUpdateRangeFilterToday_PublishesNewSnapshot(t *testing.T) {
	originalUpdatedAt := time.Now().Add(-25 * time.Hour)
	original := &Settings{}
	original.BirdNET.RangeFilter.LastUpdated = originalUpdatedAt
	original.BirdNET.RangeFilter.Species = []string{"Original Species"}
	setupGlobalSettings(t, original)

	require.True(t, ShouldUpdateRangeFilterToday())

	published := GetSettings()
	require.NotSame(t, original, published, "Must publish a new snapshot, not mutate original")
	assert.False(t, published.BirdNET.RangeFilter.LastUpdated.Before(time.Now().Truncate(24*time.Hour)),
		"Published snapshot should have LastUpdated >= today")
	assert.Equal(t, []string{"Original Species"}, published.BirdNET.RangeFilter.Species,
		"Species list should be preserved in the published snapshot")
	assert.Equal(t, originalUpdatedAt.Truncate(time.Second),
		original.BirdNET.RangeFilter.LastUpdated.Truncate(time.Second),
		"Original snapshot must not be mutated")
}

// TestGetLastRangeFilterUpdate verifies reading of LastUpdated from a snapshot.
func TestGetLastRangeFilterUpdate(t *testing.T) {
	t.Parallel()

	settings := &Settings{}
	expectedTime := time.Now().Add(-1 * time.Hour)
	settings.BirdNET.RangeFilter.LastUpdated = expectedTime

	got := settings.GetLastRangeFilterUpdate()
	assert.True(t, got.Equal(expectedTime), "Expected time to match")
}

// TestUpdateIncludedSpecies_ThreadSafety verifies concurrent updates are safe.
func TestUpdateIncludedSpecies_ThreadSafety(t *testing.T) {
	settings := &Settings{}
	setupGlobalSettings(t, settings)

	const numGoroutines = 50
	var wg sync.WaitGroup

	for range numGoroutines {
		wg.Go(func() {
			UpdateIncludedSpecies([]string{"Species A", "Species B"})
		})
	}

	wg.Wait()

	species := GetSettings().GetIncludedSpecies()
	assert.Len(t, species, 2, "Expected 2 species")
}

// TestUpdateIncludedSpecies_PublishesNewSnapshot verifies clone-mutate-publish.
func TestUpdateIncludedSpecies_PublishesNewSnapshot(t *testing.T) {
	original := &Settings{}
	original.BirdNET.RangeFilter.Species = []string{"Old Species"}
	setupGlobalSettings(t, original)

	UpdateIncludedSpecies([]string{"New Species A", "New Species B"})

	published := GetSettings()
	require.NotSame(t, original, published, "Must publish a new snapshot")
	assert.Equal(t, []string{"New Species A", "New Species B"}, published.BirdNET.RangeFilter.Species)
	assert.False(t, published.BirdNET.RangeFilter.LastUpdated.IsZero(), "LastUpdated should be set")

	assert.Equal(t, []string{"Old Species"}, original.BirdNET.RangeFilter.Species,
		"Original snapshot must not be mutated")
}

// TestIsSpeciesIncluded_ThreadSafety verifies concurrent reads during updates.
func TestIsSpeciesIncluded_ThreadSafety(t *testing.T) {
	settings := &Settings{}
	settings.BirdNET.RangeFilter.Species = []string{
		"Turdus merula_Eurasian Blackbird",
		"Parus major_Great Tit",
	}
	setupGlobalSettings(t, settings)

	const numReaders = 100
	var wg sync.WaitGroup
	var mu sync.Mutex
	var errs []string

	for range numReaders {
		wg.Go(func() {
			snapshot := GetSettings()
			if !snapshot.IsSpeciesIncluded("Turdus merula") {
				mu.Lock()
				errs = append(errs, "Expected species to be included")
				mu.Unlock()
			}
		})
	}

	wg.Go(func() {
		time.Sleep(1 * time.Millisecond)
		UpdateIncludedSpecies([]string{
			"Turdus merula_Eurasian Blackbird",
			"Parus major_Great Tit",
			"Corvus cornix_Hooded Crow",
		})
	})

	wg.Wait()
	assert.Empty(t, errs, "Concurrent reads failed: found %d errors", len(errs))
}

// TestResetRangeFilterUpdateFlag verifies that ResetRangeFilterUpdateFlag
// correctly resets the LastUpdated timestamp to allow retries.
func TestResetRangeFilterUpdateFlag(t *testing.T) {
	settings := &Settings{}
	settings.BirdNET.RangeFilter.LastUpdated = time.Now()
	setupGlobalSettings(t, settings)

	assert.False(t, settings.BirdNET.RangeFilter.LastUpdated.IsZero(), "LastUpdated should not be zero initially")

	ResetRangeFilterUpdateFlag()

	published := GetSettings()
	assert.True(t, published.BirdNET.RangeFilter.LastUpdated.IsZero(), "LastUpdated should be zero after reset")
	assert.True(t, ShouldUpdateRangeFilterToday(), "ShouldUpdateRangeFilterToday should return true after reset")
}

// TestResetRangeFilterUpdateFlag_PublishesNewSnapshot verifies immutability.
func TestResetRangeFilterUpdateFlag_PublishesNewSnapshot(t *testing.T) {
	original := &Settings{}
	original.BirdNET.RangeFilter.LastUpdated = time.Now()
	original.BirdNET.RangeFilter.Species = []string{"Preserved Species"}
	setupGlobalSettings(t, original)

	ResetRangeFilterUpdateFlag()

	published := GetSettings()
	require.NotSame(t, original, published, "Must publish a new snapshot")
	assert.True(t, published.BirdNET.RangeFilter.LastUpdated.IsZero())
	assert.Equal(t, []string{"Preserved Species"}, published.BirdNET.RangeFilter.Species,
		"Species list should be preserved")
	assert.False(t, original.BirdNET.RangeFilter.LastUpdated.IsZero(),
		"Original snapshot must not be mutated")
}

// TestResetRangeFilterUpdateFlag_ThreadSafety verifies concurrent resets.
func TestResetRangeFilterUpdateFlag_ThreadSafety(t *testing.T) {
	settings := &Settings{}
	settings.BirdNET.RangeFilter.LastUpdated = time.Now()
	setupGlobalSettings(t, settings)

	const numGoroutines = 50
	var wg sync.WaitGroup

	for range numGoroutines {
		wg.Go(func() {
			ResetRangeFilterUpdateFlag()
		})
	}

	wg.Wait()

	published := GetSettings()
	assert.True(t, published.BirdNET.RangeFilter.LastUpdated.IsZero(),
		"LastUpdated should be zero after concurrent resets")
}

// TestErrorRecoveryScenario simulates the full error recovery flow:
// 1. Update is scheduled (ShouldUpdateRangeFilterToday returns true)
// 2. Update fails (simulated by ResetRangeFilterUpdateFlag)
// 3. Next detection retries (ShouldUpdateRangeFilterToday returns true again)
func TestErrorRecoveryScenario(t *testing.T) {
	settings := &Settings{}
	settings.BirdNET.RangeFilter.LastUpdated = time.Now().Add(-25 * time.Hour)
	setupGlobalSettings(t, settings)

	assert.True(t, ShouldUpdateRangeFilterToday(), "First call should return true when LastUpdated is yesterday")

	ResetRangeFilterUpdateFlag()

	assert.True(t, ShouldUpdateRangeFilterToday(), "Should return true after failed update (reset flag)")

	UpdateIncludedSpecies([]string{"Test Species"})

	assert.False(t, ShouldUpdateRangeFilterToday(), "Should return false after successful update")
}

// TestErrorRecoveryScenario_Concurrent simulates concurrent error recovery.
func TestErrorRecoveryScenario_Concurrent(t *testing.T) {
	settings := &Settings{}
	settings.BirdNET.RangeFilter.LastUpdated = time.Now()
	setupGlobalSettings(t, settings)

	ResetRangeFilterUpdateFlag()

	const numGoroutines = 100
	var wg sync.WaitGroup
	trueCount := 0
	var mu sync.Mutex

	for range numGoroutines {
		wg.Go(func() {
			if ShouldUpdateRangeFilterToday() {
				mu.Lock()
				trueCount++
				mu.Unlock()
			}
		})
	}

	wg.Wait()
	assert.Equal(t, 1, trueCount, "Expected exactly 1 goroutine to receive true after reset")
}

// TestIsSpeciesIncluded_ScientificNameMap verifies that IsSpeciesIncluded uses
// the O(1) map path when IncludedScientificNames is populated.
func TestIsSpeciesIncluded_ScientificNameMap(t *testing.T) {
	t.Parallel()

	settings := &Settings{}
	settings.BirdNET.RangeFilter.Species = []string{
		"Turdus merula_Common Blackbird",
		"Parus major_Great Tit",
		"Corvus corax_Northern Raven",
	}
	settings.BirdNET.RangeFilter.IncludedScientificNames = map[string]struct{}{
		"turdus merula": {},
		"parus major":   {},
		"corvus corax":  {},
	}

	tests := []struct {
		name     string
		input    string
		expected bool
	}{
		{"full label match", "Turdus merula_Common Blackbird", true},
		{"scientific name only", "Turdus merula", true},
		{"case insensitive", "TURDUS MERULA", true},
		{"case insensitive full label", "PARUS MAJOR_Great Tit", true},
		{"not included", "Ficedula hypoleuca_Pied Flycatcher", false},
		{"not included sci only", "Ficedula hypoleuca", false},
		{"empty string", "", false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			assert.Equal(t, tt.expected, settings.IsSpeciesIncluded(tt.input))
		})
	}
}

// TestIsSpeciesIncluded_TaxonomicAlias verifies that a legacy classifier label is
// matched against a canonical included-species key via the OpenFauna alias map.
// The v3 geomodel lists the species under its current eBird name, so the included
// set holds the canonical key; a BirdNET v2.4 detection arrives under the legacy
// name and must still resolve to "included", or the range-filtered species would
// be dropped by the inclusion gate.
func TestIsSpeciesIncluded_TaxonomicAlias(t *testing.T) {
	t.Parallel()

	settings := &Settings{}
	// Included set keyed by the canonical (geomodel) name, as BuildRangeFilter
	// would produce from the geomodel's labels.
	settings.BirdNET.RangeFilter.IncludedScientificNames = map[string]struct{}{
		"spilopelia senegalensis": {},
	}

	tests := []struct {
		name     string
		input    string
		expected bool
	}{
		{"legacy label resolves to canonical", "Streptopelia senegalensis_Laughing Dove", true},
		{"legacy scientific only", "Streptopelia senegalensis", true},
		{"canonical label matches directly", "Spilopelia senegalensis_Laughing Dove", true},
		{"legacy label with surrounding whitespace", "  Streptopelia senegalensis  ", true},
		{"legacy label with CRLF carriage return", "Streptopelia senegalensis\r", true},
		{"unrelated species not included", "Ficedula hypoleuca", false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			assert.Equal(t, tt.expected, settings.IsSpeciesIncluded(tt.input))
		})
	}
}

// TestUpdateIncludedSpecies_BuildsScientificNameMap verifies that
// UpdateIncludedSpecies populates IncludedScientificNames with lowercased
// scientific names extracted from BirdNET-style labels.
func TestUpdateIncludedSpecies_BuildsScientificNameMap(t *testing.T) {
	settings := &Settings{}
	setupGlobalSettings(t, settings)

	UpdateIncludedSpecies([]string{
		"Turdus merula_Common Blackbird",
		"Parus major_Great Tit",
		"Canis lupus_Gray Wolf",
	})

	published := GetSettings()
	require.NotNil(t, published.BirdNET.RangeFilter.IncludedScientificNames)
	assert.Len(t, published.BirdNET.RangeFilter.IncludedScientificNames, 3)

	_, hasTurdus := published.BirdNET.RangeFilter.IncludedScientificNames["turdus merula"]
	assert.True(t, hasTurdus, "should contain lowercased scientific name")

	_, hasParus := published.BirdNET.RangeFilter.IncludedScientificNames["parus major"]
	assert.True(t, hasParus)

	_, hasCanis := published.BirdNET.RangeFilter.IncludedScientificNames["canis lupus"]
	assert.True(t, hasCanis)
}

// TestUpdateIncludedSpecies_ScientificNameOnly verifies that labels without an
// underscore (i.e. scientific name only) are stored using the full string.
func TestUpdateIncludedSpecies_ScientificNameOnly(t *testing.T) {
	settings := &Settings{}
	setupGlobalSettings(t, settings)

	UpdateIncludedSpecies([]string{"Parus major", "Corvus corax"})

	published := GetSettings()
	_, hasParus := published.BirdNET.RangeFilter.IncludedScientificNames["parus major"]
	assert.True(t, hasParus, "labels without underscore should use full string as scientific name")
}

// TestResetAndCheckInterleaved tests interleaved reset and check operations.
func TestResetAndCheckInterleaved(t *testing.T) {
	settings := &Settings{}
	settings.BirdNET.RangeFilter.LastUpdated = time.Now()
	setupGlobalSettings(t, settings)

	const numIterations = 10
	var wg sync.WaitGroup

	wg.Go(func() {
		for range numIterations {
			ResetRangeFilterUpdateFlag()
			time.Sleep(1 * time.Millisecond)
		}
	})

	wg.Go(func() {
		for range numIterations {
			ShouldUpdateRangeFilterToday()
			time.Sleep(1 * time.Millisecond)
		}
	})

	wg.Go(func() {
		for range numIterations {
			GetSettings().GetLastRangeFilterUpdate()
			time.Sleep(1 * time.Millisecond)
		}
	})

	wg.Wait()
}
