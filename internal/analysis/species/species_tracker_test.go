package species

import (
	"fmt"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/require"
	"github.com/tphakala/birdnet-go/internal/conf"
	"github.com/tphakala/birdnet-go/internal/datastore"
	"github.com/tphakala/birdnet-go/internal/datastore/mocks"
)

// Test constants for day counts and time windows
const (
	oldSpeciesDays     = 20 // Days ago for old species (outside window)
	recentSpeciesDays  = 5  // Days ago for recent species (within window)
	newSpeciesWindow   = 14 // Default window for considering species "new"
	syncIntervalMins   = 60 // Default sync interval in minutes
	yearlyWindowDays   = 30 // Default yearly tracking window
	seasonalWindowDays = 21 // Default seasonal tracking window
)

func TestSpeciesTracker_NewSpecies(t *testing.T) {
	t.Parallel()
	// Create mock datastore with some historical species data
	ds := mocks.NewMockInterface(t)
	historicalData := []datastore.NewSpeciesData{
		{
			ScientificName: testSpeciesParusMajor,
			CommonName:     "Great Tit",
			FirstSeenDate:  time.Now().Add(-oldSpeciesDays * 24 * time.Hour).Format(time.DateOnly),
		},
		{
			ScientificName: "Turdus merula",
			CommonName:     "Common Blackbird",
			FirstSeenDate:  time.Now().Add(-recentSpeciesDays * 24 * time.Hour).Format(time.DateOnly),
		},
	}
	ds.On("GetNewSpeciesDetections", mock.Anything, mock.AnythingOfType("string"), mock.AnythingOfType("string"), mock.AnythingOfType("int"), mock.AnythingOfType("int")).
		Return(historicalData, nil).Maybe()
	// BG-17: InitFromDatabase now loads notification history
	ds.On("GetActiveNotificationHistory", mock.AnythingOfType("time.Time")).
		Return([]datastore.NotificationHistory{}, nil).Maybe()

	// Create tracker with new species window
	settings := &conf.SpeciesTrackingSettings{
		Enabled:              true,
		NewSpeciesWindowDays: newSpeciesWindow,
		SyncIntervalMinutes:  syncIntervalMins,
		YearlyTracking: conf.YearlyTrackingSettings{
			Enabled: false,
		},
		SeasonalTracking: conf.SeasonalTrackingSettings{
			Enabled: false,
		},
	}
	tracker := NewTrackerFromSettings(ds, settings)

	// Initialize from database
	err := tracker.InitFromDatabase()
	require.NoError(t, err)

	currentTime := time.Now()

	t.Run("new species not in database", func(t *testing.T) {
		status := tracker.GetSpeciesStatus("Cyanistes caeruleus", currentTime)
		assert.True(t, status.IsNew, "Expected Cyanistes caeruleus to be a new species")
		assert.Equal(t, 0, status.DaysSinceFirst, "Expected DaysSinceFirst to be 0 for new species")
	})

	t.Run("old species outside window", func(t *testing.T) {
		status := tracker.GetSpeciesStatus(testSpeciesParusMajor, currentTime)
		assert.False(t, status.IsNew, "Expected Parus major to not be a new species (%d days old)", oldSpeciesDays)
		assert.Equal(t, oldSpeciesDays, status.DaysSinceFirst, "Expected DaysSinceFirst to be %d", oldSpeciesDays)
	})

	t.Run("recent species within window", func(t *testing.T) {
		status := tracker.GetSpeciesStatus("Turdus merula", currentTime)
		assert.True(t, status.IsNew, "Expected Turdus merula to be a new species (%d days old, within %d-day window)", recentSpeciesDays, newSpeciesWindow)
		assert.Equal(t, recentSpeciesDays, status.DaysSinceFirst, "Expected DaysSinceFirst to be %d", recentSpeciesDays)
	})
}

// TestSpeciesTracker_ConcurrentAccess tests thread safety of the species tracker.
// Run this test with the Go race detector enabled: go test -race
func TestSpeciesTracker_ConcurrentAccess(t *testing.T) {
	t.Parallel()

	ds := mocks.NewMockInterface(t)
	ds.On("GetNewSpeciesDetections", mock.Anything, mock.AnythingOfType("string"), mock.AnythingOfType("string"), mock.AnythingOfType("int"), mock.AnythingOfType("int")).
		Return([]datastore.NewSpeciesData{}, nil).Maybe()
	// BG-17: InitFromDatabase now loads notification history
	ds.On("GetActiveNotificationHistory", mock.AnythingOfType("time.Time")).
		Return([]datastore.NotificationHistory{}, nil).Maybe()

	settings := &conf.SpeciesTrackingSettings{
		Enabled:              true,
		NewSpeciesWindowDays: newSpeciesWindow,
		SyncIntervalMinutes:  syncIntervalMins,
		YearlyTracking: conf.YearlyTrackingSettings{
			Enabled: false,
		},
		SeasonalTracking: conf.SeasonalTrackingSettings{
			Enabled: false,
		},
	}
	tracker := NewTrackerFromSettings(ds, settings)
	err := tracker.InitFromDatabase()
	require.NoError(t, err)

	// Test concurrent reads and writes
	var wg sync.WaitGroup
	species := []string{"Species1", "Species2", "Species3", "Species4", "Species5"}
	currentTime := time.Now()

	// Start multiple goroutines
	for i := range 10 {
		wg.Add(1)
		go func(id int) {
			defer wg.Done()
			for j := range 100 {
				speciesName := species[j%len(species)]
				if id%2 == 0 {
					// Read operation
					_ = tracker.GetSpeciesStatus(speciesName, currentTime)
				} else {
					// Write operation
					_ = tracker.UpdateSpecies(speciesName, currentTime)
				}
			}
		}(i)
	}

	wg.Wait()
}

func TestSpeciesTracker_UpdateSpecies(t *testing.T) {
	t.Parallel()

	ds := mocks.NewMockInterface(t)
	ds.On("GetNewSpeciesDetections", mock.Anything, mock.AnythingOfType("string"), mock.AnythingOfType("string"), mock.AnythingOfType("int"), mock.AnythingOfType("int")).
		Return([]datastore.NewSpeciesData{}, nil).Maybe()
	// BG-17: InitFromDatabase now loads notification history
	ds.On("GetActiveNotificationHistory", mock.AnythingOfType("time.Time")).
		Return([]datastore.NotificationHistory{}, nil).Maybe()

	settings := &conf.SpeciesTrackingSettings{
		Enabled:              true,
		NewSpeciesWindowDays: newSpeciesWindow,
		SyncIntervalMinutes:  syncIntervalMins,
		YearlyTracking: conf.YearlyTrackingSettings{
			Enabled: false,
		},
		SeasonalTracking: conf.SeasonalTrackingSettings{
			Enabled: false,
		},
	}
	tracker := NewTrackerFromSettings(ds, settings)
	err := tracker.InitFromDatabase()
	require.NoError(t, err)

	currentTime := time.Now()

	// Track a new species
	isNew := tracker.UpdateSpecies(testSpeciesParusMajor, currentTime)
	assert.True(t, isNew, "Expected UpdateSpecies to return true for new species")

	// Verify it's now tracked
	status := tracker.GetSpeciesStatus(testSpeciesParusMajor, currentTime)
	assert.True(t, status.IsNew, "Expected newly tracked species to be marked as new")
	assert.Equal(t, 0, status.DaysSinceFirst, "Expected DaysSinceFirst to be 0 for just-tracked species")

	// Update same species again
	isNew = tracker.UpdateSpecies(testSpeciesParusMajor, currentTime.Add(time.Hour))
	assert.False(t, isNew, "Expected UpdateSpecies to return false for existing species")
}

func TestSpeciesTracker_EdgeCases(t *testing.T) {
	t.Parallel()

	t.Run("species at exact window boundary", func(t *testing.T) {
		t.Parallel()

		// Create tracker with exactly 14 days old species
		ds := mocks.NewMockInterface(t)
		historicalData := []datastore.NewSpeciesData{
			{
				ScientificName: testSpeciesParusMajor,
				FirstSeenDate:  time.Now().Add(-14 * 24 * time.Hour).Format(time.DateOnly), // Exactly 14 days ago
			},
		}
		ds.On("GetNewSpeciesDetections", mock.Anything, mock.AnythingOfType("string"), mock.AnythingOfType("string"), mock.AnythingOfType("int"), mock.AnythingOfType("int")).
			Return(historicalData, nil).Maybe()
		// BG-17: InitFromDatabase now loads notification history
		ds.On("GetActiveNotificationHistory", mock.AnythingOfType("time.Time")).
			Return([]datastore.NotificationHistory{}, nil).Maybe()

		settings := &conf.SpeciesTrackingSettings{
			Enabled:              true,
			NewSpeciesWindowDays: newSpeciesWindow,
			SyncIntervalMinutes:  syncIntervalMins,
			YearlyTracking: conf.YearlyTrackingSettings{
				Enabled: false,
			},
			SeasonalTracking: conf.SeasonalTrackingSettings{
				Enabled: false,
			},
		}
		tracker := NewTrackerFromSettings(ds, settings)
		err := tracker.InitFromDatabase()
		require.NoError(t, err)

		currentTime := time.Now()

		// Test species exactly at the window boundary
		status := tracker.GetSpeciesStatus(testSpeciesParusMajor, currentTime)
		// Should be considered new since it's within the window (14 days is inclusive)
		assert.True(t, status.IsNew, "Expected species at exact window boundary to be considered new")
	})

	t.Run("empty species name", func(t *testing.T) {
		t.Parallel()

		ds := mocks.NewMockInterface(t)
		ds.On("GetNewSpeciesDetections", mock.Anything, mock.AnythingOfType("string"), mock.AnythingOfType("string"), mock.AnythingOfType("int"), mock.AnythingOfType("int")).
			Return([]datastore.NewSpeciesData{}, nil).Maybe()
		// BG-17: InitFromDatabase now loads notification history
		ds.On("GetActiveNotificationHistory", mock.AnythingOfType("time.Time")).
			Return([]datastore.NotificationHistory{}, nil).Maybe()
		// BG-17: CleanupOldNotificationRecords deletes from database
		ds.On("DeleteExpiredNotificationHistory", mock.AnythingOfType("time.Time")).
			Return(int64(0), nil).Maybe()

		settings := &conf.SpeciesTrackingSettings{
			Enabled:              true,
			NewSpeciesWindowDays: newSpeciesWindow,
			SyncIntervalMinutes:  syncIntervalMins,
			YearlyTracking: conf.YearlyTrackingSettings{
				Enabled: false,
			},
			SeasonalTracking: conf.SeasonalTrackingSettings{
				Enabled: false,
			},
		}
		tracker := NewTrackerFromSettings(ds, settings)
		err := tracker.InitFromDatabase()
		require.NoError(t, err)

		currentTime := time.Now()

		// Test empty species name
		status := tracker.GetSpeciesStatus("", currentTime)
		assert.True(t, status.IsNew, "Empty species name should be considered new (not in database)")
		assert.Equal(t, 0, status.DaysSinceFirst, "Expected DaysSinceFirst to be 0 for empty species name")
	})
}

func TestSpeciesTracker_PruneOldEntries(t *testing.T) {
	ds := mocks.NewMockInterface(t)
	historicalData := []datastore.NewSpeciesData{
		{
			ScientificName: "Very Old Species",
			FirstSeenDate:  time.Now().Add(-11 * 365 * 24 * time.Hour).Format(time.DateOnly), // 11 years ago (should be pruned)
		},
		{
			ScientificName: "Old Species",
			FirstSeenDate:  time.Now().Add(-30 * 24 * time.Hour).Format(time.DateOnly), // 30 days ago (should NOT be pruned)
		},
		{
			ScientificName: "Recent Species",
			FirstSeenDate:  time.Now().Add(-5 * 24 * time.Hour).Format(time.DateOnly), // 5 days ago (should NOT be pruned)
		},
	}
	ds.On("GetNewSpeciesDetections", mock.Anything, mock.AnythingOfType("string"), mock.AnythingOfType("string"), mock.AnythingOfType("int"), mock.AnythingOfType("int")).
		Return(historicalData, nil).Maybe()
	// BG-17: InitFromDatabase now loads notification history
	ds.On("GetActiveNotificationHistory", mock.AnythingOfType("time.Time")).
		Return([]datastore.NotificationHistory{}, nil).Maybe()
	// BG-17: CleanupOldNotificationRecords deletes from database
	ds.On("DeleteExpiredNotificationHistory", mock.AnythingOfType("time.Time")).
		Return(int64(0), nil).Maybe()

	settings := &conf.SpeciesTrackingSettings{
		Enabled:              true,
		NewSpeciesWindowDays: newSpeciesWindow,
		SyncIntervalMinutes:  syncIntervalMins,
		YearlyTracking: conf.YearlyTrackingSettings{
			Enabled: false,
		},
		SeasonalTracking: conf.SeasonalTrackingSettings{
			Enabled: false,
		},
	}
	tracker := NewTrackerFromSettings(ds, settings)
	err := tracker.InitFromDatabase()
	require.NoError(t, err)

	// Initial species count
	assert.Equal(t, 3, tracker.GetSpeciesCount(), "Expected 3 species initially")

	// Prune old entries (only entries older than 10 years should be pruned for lifetime tracking)
	pruned := tracker.PruneOldEntries()
	assert.Equal(t, 1, pruned, "Expected 1 species to be pruned (11 years old)")

	// Should have both recent and 30-day-old species left
	assert.Equal(t, 2, tracker.GetSpeciesCount(), "Expected 2 species after pruning")

	// Both remaining species should still be tracked
	status := tracker.GetSpeciesStatus("Recent Species", time.Now())
	assert.True(t, status.IsNew, "Recent species should still be marked as new after pruning")

	status30Days := tracker.GetSpeciesStatus("Old Species", time.Now())
	assert.False(t, status30Days.IsNew, "30-day-old species should not be new but should still be tracked")
}

// Benchmark tests
func BenchmarkSpeciesTracker_GetSpeciesStatus(b *testing.B) {
	ds := mocks.NewMockInterface(b)
	ds.On("GetNewSpeciesDetections", mock.Anything, mock.AnythingOfType("string"), mock.AnythingOfType("string"), mock.AnythingOfType("int"), mock.AnythingOfType("int")).
		Return([]datastore.NewSpeciesData{}, nil).Maybe()
	// BG-17: InitFromDatabase now loads notification history
	ds.On("GetActiveNotificationHistory", mock.AnythingOfType("time.Time")).
		Return([]datastore.NotificationHistory{}, nil).Maybe()

	settings := &conf.SpeciesTrackingSettings{
		Enabled:              true,
		NewSpeciesWindowDays: newSpeciesWindow,
		SyncIntervalMinutes:  syncIntervalMins,
		YearlyTracking: conf.YearlyTrackingSettings{
			Enabled: false,
		},
		SeasonalTracking: conf.SeasonalTrackingSettings{
			Enabled: false,
		},
	}
	tracker := NewTrackerFromSettings(ds, settings)
	if err := tracker.InitFromDatabase(); err != nil {
		b.Fatalf("Failed to initialize tracker from database: %v", err)
	}

	// Pre-populate with some species
	currentTime := time.Now()
	species := make([]string, 100)
	for i := range 100 {
		species[i] = fmt.Sprintf("Species%d", i)
		tracker.UpdateSpecies(species[i], currentTime.Add(time.Duration(-i)*24*time.Hour))
	}

	b.ResetTimer()
	b.ReportAllocs()

	// Go 1.24: Use b.Loop() instead of manual for i := 0; i < b.N; i++
	i := 0
	for b.Loop() {
		_ = tracker.GetSpeciesStatus(species[i%100], currentTime)
		i++
	}
}

func BenchmarkSpeciesTracker_UpdateSpecies(b *testing.B) {
	ds := mocks.NewMockInterface(b)
	ds.On("GetNewSpeciesDetections", mock.Anything, mock.AnythingOfType("string"), mock.AnythingOfType("string"), mock.AnythingOfType("int"), mock.AnythingOfType("int")).
		Return([]datastore.NewSpeciesData{}, nil).Maybe()
	// BG-17: InitFromDatabase now loads notification history
	ds.On("GetActiveNotificationHistory", mock.AnythingOfType("time.Time")).
		Return([]datastore.NotificationHistory{}, nil).Maybe()

	settings := &conf.SpeciesTrackingSettings{
		Enabled:              true,
		NewSpeciesWindowDays: newSpeciesWindow,
		SyncIntervalMinutes:  syncIntervalMins,
		YearlyTracking: conf.YearlyTrackingSettings{
			Enabled: false,
		},
		SeasonalTracking: conf.SeasonalTrackingSettings{
			Enabled: false,
		},
	}
	tracker := NewTrackerFromSettings(ds, settings)
	if err := tracker.InitFromDatabase(); err != nil {
		b.Fatalf("Failed to initialize tracker from database: %v", err)
	}

	currentTime := time.Now()
	species := make([]string, b.N)
	for i := 0; i < b.N; i++ {
		species[i] = fmt.Sprintf("Species%d", i)
	}

	b.ResetTimer()
	b.ReportAllocs()

	// Go 1.24: Use b.Loop() instead of manual for i := 0; i < b.N; i++
	i := 0
	for b.Loop() {
		tracker.UpdateSpecies(species[i], currentTime)
		i++
	}
}

func BenchmarkSpeciesTracker_ConcurrentOperations(b *testing.B) {
	ds := mocks.NewMockInterface(b)
	ds.On("GetNewSpeciesDetections", mock.Anything, mock.AnythingOfType("string"), mock.AnythingOfType("string"), mock.AnythingOfType("int"), mock.AnythingOfType("int")).
		Return([]datastore.NewSpeciesData{}, nil).Maybe()
	// BG-17: InitFromDatabase now loads notification history
	ds.On("GetActiveNotificationHistory", mock.AnythingOfType("time.Time")).
		Return([]datastore.NotificationHistory{}, nil).Maybe()

	settings := &conf.SpeciesTrackingSettings{
		Enabled:              true,
		NewSpeciesWindowDays: newSpeciesWindow,
		SyncIntervalMinutes:  syncIntervalMins,
		YearlyTracking: conf.YearlyTrackingSettings{
			Enabled: false,
		},
		SeasonalTracking: conf.SeasonalTrackingSettings{
			Enabled: false,
		},
	}
	tracker := NewTrackerFromSettings(ds, settings)
	if err := tracker.InitFromDatabase(); err != nil {
		b.Fatalf("Failed to initialize tracker from database: %v", err)
	}

	// Pre-populate with some species
	currentTime := time.Now()
	species := make([]string, 50)
	for i := range 50 {
		species[i] = fmt.Sprintf("Species%d", i)
		tracker.UpdateSpecies(species[i], currentTime)
	}

	// Pre-generate new species names before benchmark to avoid string formatting overhead
	newSpeciesNames := make([]string, b.N)
	for i := 0; i < b.N; i++ {
		newSpeciesNames[i] = fmt.Sprintf("NewSpecies%d", i)
	}

	b.ResetTimer()
	b.ReportAllocs()
	b.RunParallel(func(pb *testing.PB) {
		i := 0
		for pb.Next() {
			if i%2 == 0 {
				_ = tracker.GetSpeciesStatus(species[i%50], currentTime)
			} else {
				tracker.UpdateSpecies(newSpeciesNames[i%len(newSpeciesNames)], currentTime)
			}
			i++
		}
	})
}

func BenchmarkSpeciesTracker_MapMemoryUsage(b *testing.B) {
	ds := mocks.NewMockInterface(b)
	ds.On("GetNewSpeciesDetections", mock.Anything, mock.AnythingOfType("string"), mock.AnythingOfType("string"), mock.AnythingOfType("int"), mock.AnythingOfType("int")).
		Return([]datastore.NewSpeciesData{}, nil).Maybe()
	// BG-17: InitFromDatabase now loads notification history
	ds.On("GetActiveNotificationHistory", mock.AnythingOfType("time.Time")).
		Return([]datastore.NotificationHistory{}, nil).Maybe()

	settings := &conf.SpeciesTrackingSettings{
		Enabled:              true,
		NewSpeciesWindowDays: newSpeciesWindow,
		SyncIntervalMinutes:  syncIntervalMins,
		YearlyTracking: conf.YearlyTrackingSettings{
			Enabled: false,
		},
		SeasonalTracking: conf.SeasonalTrackingSettings{
			Enabled: false,
		},
	}
	tracker := NewTrackerFromSettings(ds, settings)
	if err := tracker.InitFromDatabase(); err != nil {
		b.Fatalf("Failed to initialize tracker from database: %v", err)
	}

	// Pre-generate all unique species names to isolate map growth measurements
	uniqueSpeciesNames := make([]string, b.N)
	for i := 0; i < b.N; i++ {
		uniqueSpeciesNames[i] = fmt.Sprintf("UniqueSpecies%d", i)
	}

	currentTime := time.Now()
	b.ResetTimer()
	b.ReportAllocs()

	// Benchmark memory allocation when adding many species
	// Go 1.24: Use b.Loop() instead of manual for i := 0; i < b.N; i++
	i := 0
	for b.Loop() {
		tracker.UpdateSpecies(uniqueSpeciesNames[i], currentTime)
		if i%1000 == 0 {
			// Periodically check a species to prevent optimization
			_ = tracker.GetSpeciesStatus(uniqueSpeciesNames[0], currentTime)
		}
		i++
	}
}

// Multi-period tracking tests

func TestNewTrackerFromSettings_BasicConfiguration(t *testing.T) {
	t.Parallel()
	ds := mocks.NewMockInterface(t)
	ds.On("GetNewSpeciesDetections", mock.Anything, mock.AnythingOfType("string"), mock.AnythingOfType("string"), mock.AnythingOfType("int"), mock.AnythingOfType("int")).
		Return([]datastore.NewSpeciesData{}, nil).Maybe()
	// BG-17: InitFromDatabase requires notification history
	ds.On("GetActiveNotificationHistory", mock.AnythingOfType("time.Time")).
		Return([]datastore.NotificationHistory{}, nil).Maybe()
	ds.On("GetSpeciesFirstDetectionInPeriod", mock.Anything, mock.AnythingOfType("string"), mock.AnythingOfType("string"), mock.AnythingOfType("int"), mock.AnythingOfType("int")).
		Return([]datastore.NewSpeciesData{}, nil).Maybe()

	// Create comprehensive configuration
	settings := &conf.SpeciesTrackingSettings{
		Enabled:              true,
		NewSpeciesWindowDays: 30,
		SyncIntervalMinutes:  60,
		YearlyTracking: conf.YearlyTrackingSettings{
			Enabled:    true,
			ResetMonth: 1,
			ResetDay:   1,
			WindowDays: 30,
		},
		SeasonalTracking: conf.SeasonalTrackingSettings{
			Enabled:    true,
			WindowDays: 21,
			Seasons: map[string]conf.Season{
				"spring": {StartMonth: 3, StartDay: 20},
				"summer": {StartMonth: 6, StartDay: 21},
				"fall":   {StartMonth: 9, StartDay: 22},
				"winter": {StartMonth: 12, StartDay: 21},
			},
		},
	}

	tracker := NewTrackerFromSettings(ds, settings)
	require.NotNil(t, tracker, "Expected tracker to be created")

	// Initialize the tracker to properly set up internal state
	err := tracker.InitFromDatabase()
	require.NoError(t, err, "Expected tracker initialization to succeed")

	// Verify basic configuration fields that are publicly accessible
	assert.Equal(t, 30, tracker.GetWindowDays(), "Expected window days to be 30")

	// Test that tracker correctly handles species operations with the given configuration
	testTime := time.Now()
	testSpecies := testSpeciesParusMajor

	// First detection should be considered new
	isNew := tracker.UpdateSpecies(testSpecies, testTime)
	assert.True(t, isNew, "Expected first detection to be new")

	// Verify species count tracking works
	initialCount := tracker.GetSpeciesCount()
	assert.Equal(t, 1, initialCount, "Expected species count to be 1 after first detection")

	// Test species status functionality
	status := tracker.GetSpeciesStatus(testSpecies, testTime)
	assert.True(t, status.IsNew, "Expected species to be marked as new")
	assert.Equal(t, 0, status.DaysSinceFirst, "Expected DaysSinceFirst to be 0 for new detection")

	// Test yearly tracking functionality if enabled
	if status.IsNewThisYear {
		assert.Equal(t, 0, status.DaysThisYear, "Expected DaysThisYear to be 0 for new yearly detection")
	}

	// Test seasonal tracking functionality if enabled
	if status.IsNewThisSeason {
		assert.Equal(t, 0, status.DaysThisSeason, "Expected DaysThisSeason to be 0 for new seasonal detection")
		assert.NotEmpty(t, status.CurrentSeason, "Expected CurrentSeason to be populated")
	}
}

func TestMultiPeriodTracking_YearlyTracking(t *testing.T) {
	t.Parallel()

	ds := mocks.NewMockInterface(t)
	ds.On("GetNewSpeciesDetections", mock.Anything, mock.AnythingOfType("string"), mock.AnythingOfType("string"), mock.AnythingOfType("int"), mock.AnythingOfType("int")).
		Return([]datastore.NewSpeciesData{}, nil).Maybe()
	// BG-17: InitFromDatabase now loads notification history
	ds.On("GetActiveNotificationHistory", mock.AnythingOfType("time.Time")).
		Return([]datastore.NotificationHistory{}, nil).Maybe()
	ds.On("GetSpeciesFirstDetectionInPeriod", mock.Anything, mock.AnythingOfType("string"), mock.AnythingOfType("string"), mock.AnythingOfType("int"), mock.AnythingOfType("int")).
		Return([]datastore.NewSpeciesData{}, nil).Maybe()

	settings := &conf.SpeciesTrackingSettings{
		Enabled:              true,
		NewSpeciesWindowDays: 14,
		SyncIntervalMinutes:  60,
		YearlyTracking: conf.YearlyTrackingSettings{
			Enabled:    true,
			ResetMonth: 1,
			ResetDay:   1,
			WindowDays: 30,
		},
		SeasonalTracking: conf.SeasonalTrackingSettings{
			Enabled: false,
		},
	}

	tracker := NewTrackerFromSettings(ds, settings)
	err := tracker.InitFromDatabase()
	require.NoError(t, err)

	t.Run("first detection new for all periods", func(t *testing.T) {
		currentTime := time.Now()
		speciesName := testSpeciesParusMajor

		// First detection - should be new for all periods
		isNew := tracker.UpdateSpecies(speciesName, currentTime)
		assert.True(t, isNew, "Expected first detection to be new")

		status := tracker.GetSpeciesStatus(speciesName, currentTime)

		// Check lifetime tracking
		assert.True(t, status.IsNew, "Expected species to be new (lifetime)")
		assert.Equal(t, 0, status.DaysSinceFirst, "Expected DaysSinceFirst to be 0")

		// Check yearly tracking
		assert.True(t, status.IsNewThisYear, "Expected species to be new this year")
		assert.Equal(t, 0, status.DaysThisYear, "Expected DaysThisYear to be 0")
	})

	t.Run("detection just before yearly reset date", func(t *testing.T) {
		// Detection on December 31st (just before Jan 1 reset)
		beforeResetTime := time.Date(2023, 12, 31, 12, 0, 0, 0, time.UTC)
		speciesName := "Turdus merula"

		isNew := tracker.UpdateSpecies(speciesName, beforeResetTime)
		assert.True(t, isNew, "Expected detection before reset to be new")

		status := tracker.GetSpeciesStatus(speciesName, beforeResetTime)
		assert.True(t, status.IsNewThisYear, "Expected species to be new this year before reset")
	})

	t.Run("detection just after yearly reset date", func(t *testing.T) {
		// Detection on January 2nd (just after Jan 1 reset)
		afterResetTime := time.Date(2024, 1, 2, 12, 0, 0, 0, time.UTC)
		speciesName := "Erithacus rubecula"

		isNew := tracker.UpdateSpecies(speciesName, afterResetTime)
		assert.True(t, isNew, "Expected detection after reset to be new")

		status := tracker.GetSpeciesStatus(speciesName, afterResetTime)
		assert.True(t, status.IsNewThisYear, "Expected species to be new this year after reset")
	})

	t.Run("multiple detections within same year", func(t *testing.T) {
		currentYear := time.Now().Year()
		firstDetection := time.Date(currentYear, 3, 15, 12, 0, 0, 0, time.UTC)
		secondDetection := time.Date(currentYear, 8, 20, 12, 0, 0, 0, time.UTC)
		speciesName := "Corvus corvax"

		// First detection
		isNew := tracker.UpdateSpecies(speciesName, firstDetection)
		assert.True(t, isNew, "Expected first detection to be new")

		// Second detection in same year
		isNew = tracker.UpdateSpecies(speciesName, secondDetection)
		assert.False(t, isNew, "Expected second detection in same year to not be new")

		status := tracker.GetSpeciesStatus(speciesName, secondDetection)
		assert.False(t, status.IsNewThisYear, "Expected species to NOT be new this year (158 days > 30 day window)")
		assert.Equal(t, 158, status.DaysThisYear, "Expected correct days since first detection this year")
	})

	t.Run("detections spanning multiple years", func(t *testing.T) {
		// First detection in 2023
		firstYear := time.Date(2023, 6, 15, 12, 0, 0, 0, time.UTC)
		// Second detection in 2024
		secondYear := time.Date(2024, 6, 15, 12, 0, 0, 0, time.UTC)
		speciesName := "Phoenicurus phoenicurus"

		// First detection
		isNew := tracker.UpdateSpecies(speciesName, firstYear)
		assert.True(t, isNew, "Expected first detection to be new")

		// Second detection in following year
		isNew = tracker.UpdateSpecies(speciesName, secondYear)
		assert.False(t, isNew, "Expected detection in second year to not be new (lifetime)")

		status := tracker.GetSpeciesStatus(speciesName, secondYear)
		assert.False(t, status.IsNew, "Expected species to not be new (lifetime) in second year")
		assert.True(t, status.IsNewThisYear, "Expected species to be new this year in second year")
		assert.Equal(t, 0, status.DaysThisYear, "Expected DaysThisYear to be 0 for first detection this year")
	})
}

func TestMultiPeriodTracking_SeasonalTracking(t *testing.T) {
	t.Parallel()

	ds := mocks.NewMockInterface(t)
	ds.On("GetNewSpeciesDetections", mock.Anything, mock.AnythingOfType("string"), mock.AnythingOfType("string"), mock.AnythingOfType("int"), mock.AnythingOfType("int")).
		Return([]datastore.NewSpeciesData{}, nil).Maybe()
	// BG-17: InitFromDatabase requires notification history
	ds.On("GetActiveNotificationHistory", mock.AnythingOfType("time.Time")).
		Return([]datastore.NotificationHistory{}, nil).Maybe()
	ds.On("GetSpeciesFirstDetectionInPeriod", mock.Anything, mock.AnythingOfType("string"), mock.AnythingOfType("string"), mock.AnythingOfType("int"), mock.AnythingOfType("int")).
		Return([]datastore.NewSpeciesData{}, nil).Maybe()

	settings := &conf.SpeciesTrackingSettings{
		Enabled:              true,
		NewSpeciesWindowDays: 14,
		SyncIntervalMinutes:  60,
		YearlyTracking: conf.YearlyTrackingSettings{
			Enabled: false,
		},
		SeasonalTracking: conf.SeasonalTrackingSettings{
			Enabled:    true,
			WindowDays: 21,
			Seasons: map[string]conf.Season{
				"spring": {StartMonth: 3, StartDay: 20},
				"summer": {StartMonth: 6, StartDay: 21},
				"fall":   {StartMonth: 9, StartDay: 22},
				"winter": {StartMonth: 12, StartDay: 21},
			},
		},
	}

	tracker := NewTrackerFromSettings(ds, settings)
	err := tracker.InitFromDatabase()
	require.NoError(t, err, "Expected tracker initialization to succeed")

	// Test during spring season (April)
	springTime := time.Date(2024, 4, 15, 12, 0, 0, 0, time.UTC)
	speciesName := "Turdus migratorius"

	// First detection in spring - should be marked as new
	isNew := tracker.UpdateSpecies(speciesName, springTime)
	assert.True(t, isNew, "Expected first detection to be marked as new species")

	status := tracker.GetSpeciesStatus(speciesName, springTime)

	// Verify seasonal tracking behavior
	assert.True(t, status.IsNewThisSeason, "Expected species to be marked as new this season")
	assert.Equal(t, 0, status.DaysThisSeason, "Expected DaysThisSeason to be 0 for first seasonal detection")
	assert.Equal(t, "spring", status.CurrentSeason, "Expected current season to be 'spring'")
}

func TestSeasonDetection(t *testing.T) {
	t.Parallel()
	ds := mocks.NewMockInterface(t)
	ds.On("GetNewSpeciesDetections", mock.Anything, mock.AnythingOfType("string"), mock.AnythingOfType("string"), mock.AnythingOfType("int"), mock.AnythingOfType("int")).
		Return([]datastore.NewSpeciesData{}, nil).Maybe()

	settings := &conf.SpeciesTrackingSettings{
		Enabled:              true,
		NewSpeciesWindowDays: 14,
		SyncIntervalMinutes:  60,
		SeasonalTracking: conf.SeasonalTrackingSettings{
			Enabled:    true,
			WindowDays: 21,
			Seasons: map[string]conf.Season{
				"spring": {StartMonth: 3, StartDay: 20},
				"summer": {StartMonth: 6, StartDay: 21},
				"fall":   {StartMonth: 9, StartDay: 22},
				"winter": {StartMonth: 12, StartDay: 21},
			},
		},
	}

	tracker := NewTrackerFromSettings(ds, settings)

	testCases := []struct {
		name           string
		date           time.Time
		expectedSeason string
	}{
		{"winter_january", time.Date(2024, 1, 15, 12, 0, 0, 0, time.UTC), "winter"},
		{"spring_march", time.Date(2024, 3, 25, 12, 0, 0, 0, time.UTC), "spring"},
		{"summer_june", time.Date(2024, 6, 25, 12, 0, 0, 0, time.UTC), "summer"},
		{"fall_september", time.Date(2024, 9, 25, 12, 0, 0, 0, time.UTC), "fall"},
		{"winter_december", time.Date(2024, 12, 25, 12, 0, 0, 0, time.UTC), "winter"},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			// Remove t.Parallel() since getCurrentSeason is not thread-safe for internal methods
			// It modifies cache fields without holding the mutex
			tracker.mu.Lock()
			season := tracker.getCurrentSeason(tc.date)
			tracker.mu.Unlock()
			assert.Equal(t, tc.expectedSeason, season,
				"Expected season '%s' for date %s, got '%s'",
				tc.expectedSeason, tc.date.Format(time.DateOnly), season)
		})
	}
}

func TestMultiPeriodTracking_CrossPeriodScenarios(t *testing.T) {
	t.Parallel()
	ds := mocks.NewMockInterface(t)
	ds.On("GetNewSpeciesDetections", mock.Anything, mock.AnythingOfType("string"), mock.AnythingOfType("string"), mock.AnythingOfType("int"), mock.AnythingOfType("int")).
		Return([]datastore.NewSpeciesData{}, nil).Maybe()
	// BG-17: InitFromDatabase requires notification history
	ds.On("GetActiveNotificationHistory", mock.AnythingOfType("time.Time")).
		Return([]datastore.NotificationHistory{}, nil).Maybe()
	ds.On("GetSpeciesFirstDetectionInPeriod", mock.Anything, mock.AnythingOfType("string"), mock.AnythingOfType("string"), mock.AnythingOfType("int"), mock.AnythingOfType("int")).
		Return([]datastore.NewSpeciesData{}, nil).Maybe()

	settings := &conf.SpeciesTrackingSettings{
		Enabled:              true,
		NewSpeciesWindowDays: 7, // Short window for lifetime tracking
		SyncIntervalMinutes:  60,
		YearlyTracking: conf.YearlyTrackingSettings{
			Enabled:    true,
			ResetMonth: 1,
			ResetDay:   1,
			WindowDays: 14,
		},
		SeasonalTracking: conf.SeasonalTrackingSettings{
			Enabled:    true,
			WindowDays: 10,
			Seasons: map[string]conf.Season{
				"spring": {StartMonth: 3, StartDay: 20},
				"summer": {StartMonth: 6, StartDay: 21},
				"fall":   {StartMonth: 9, StartDay: 22},
				"winter": {StartMonth: 12, StartDay: 21},
			},
		},
	}

	tracker := NewTrackerFromSettings(ds, settings)
	tracker.SetCurrentYearForTesting(2024) // Set to 2024 for test dates
	err := tracker.InitFromDatabase()
	require.NoError(t, err)

	speciesName := "Cyanistes caeruleus"

	// First detection in spring, many days ago (lifetime not new, but season/year new)
	springTime := time.Date(2024, 4, 1, 12, 0, 0, 0, time.UTC)
	tracker.UpdateSpecies(speciesName, springTime)

	// Check status much later (after lifetime window expires)
	laterTime := time.Date(2024, 4, 20, 12, 0, 0, 0, time.UTC)
	status := tracker.GetSpeciesStatus(speciesName, laterTime)

	// Lifetime should not be new (19 days > 7 day window)
	assert.False(t, status.IsNew, "Expected species to not be new (lifetime) after window expired")
	assert.Equal(t, 19, status.DaysSinceFirst, "Expected DaysSinceFirst to be 19")

	// Yearly should not be new (19 days > 14 day window)
	// The species was detected this year, but outside the yearly window
	assert.False(t, status.IsNewThisYear, "Expected species to not be new this year (19 days > 14 day window). DaysThisYear: %d", status.DaysThisYear)
	assert.Equal(t, 19, status.DaysThisYear, "Expected DaysThisYear to be 19")

	// Seasonal should not be new (19 days > 10 day window)
	assert.False(t, status.IsNewThisSeason, "Expected species to not be new this season after window expired")
	assert.Equal(t, 19, status.DaysThisSeason, "Expected DaysThisSeason to be 19")
}

func TestMultiPeriodTracking_SeasonTransition(t *testing.T) {
	t.Parallel()

	ds := mocks.NewMockInterface(t)
	ds.On("GetNewSpeciesDetections", mock.Anything, mock.AnythingOfType("string"), mock.AnythingOfType("string"), mock.AnythingOfType("int"), mock.AnythingOfType("int")).
		Return([]datastore.NewSpeciesData{}, nil).Maybe()
	// BG-17: InitFromDatabase requires notification history
	ds.On("GetActiveNotificationHistory", mock.AnythingOfType("time.Time")).
		Return([]datastore.NotificationHistory{}, nil).Maybe()
	// Mock should return empty data so the test scenario works as expected
	// The test wants to simulate first detection in spring, then check status in summer
	ds.On("GetSpeciesFirstDetectionInPeriod", mock.Anything, mock.AnythingOfType("string"), mock.AnythingOfType("string"), mock.AnythingOfType("int"), mock.AnythingOfType("int")).
		Return([]datastore.NewSpeciesData{}, nil).Maybe()

	settings := &conf.SpeciesTrackingSettings{
		Enabled:              true,
		NewSpeciesWindowDays: 30,
		SyncIntervalMinutes:  60,
		YearlyTracking: conf.YearlyTrackingSettings{
			Enabled:    true,
			ResetMonth: 1,
			ResetDay:   1,
			WindowDays: 30,
		},
		SeasonalTracking: conf.SeasonalTrackingSettings{
			Enabled:    true,
			WindowDays: 21,
			Seasons: map[string]conf.Season{
				"spring": {StartMonth: 3, StartDay: 20},
				"summer": {StartMonth: 6, StartDay: 21},
				"fall":   {StartMonth: 9, StartDay: 22},
				"winter": {StartMonth: 12, StartDay: 21},
			},
		},
	}

	tracker := NewTrackerFromSettings(ds, settings)
	tracker.SetCurrentYearForTesting(2024) // Set to 2024 for test dates
	err := tracker.InitFromDatabase()
	require.NoError(t, err)

	speciesName := "Hirundo rustica" // Barn Swallow

	// First seen in spring
	springTime := time.Date(2024, 4, 15, 12, 0, 0, 0, time.UTC)
	tracker.UpdateSpecies(speciesName, springTime)

	// Check in summer (after season transition)
	summerTime := time.Date(2024, 7, 15, 12, 0, 0, 0, time.UTC)
	status := tracker.GetSpeciesStatus(speciesName, summerTime)

	// Should be new this season (first time in summer)
	assert.True(t, status.IsNewThisSeason, "Expected species to be new this season after season transition")
	assert.Equal(t, "summer", status.CurrentSeason, "Expected current season to be 'summer'")

	// Should not be new this year (91 days > 30 day window)
	assert.False(t, status.IsNewThisYear, "Expected species to not be new this year (91 days > 30 day window)")
	assert.Equal(t, 91, status.DaysThisYear, "Expected DaysThisYear to be 91 (days since April 15)")

	// Now detect it in summer
	tracker.UpdateSpecies(speciesName, summerTime)

	// Check status later in summer
	laterSummerTime := time.Date(2024, 8, 1, 12, 0, 0, 0, time.UTC)
	status = tracker.GetSpeciesStatus(speciesName, laterSummerTime)

	// Should now have records for both seasons
	assert.Equal(t, 17, status.DaysThisSeason, "Expected DaysThisSeason to be 17 (days since July 15)")
}

func TestMultiPeriodTracking_YearReset(t *testing.T) {
	t.Parallel()
	ds := mocks.NewMockInterface(t)
	ds.On("GetNewSpeciesDetections", mock.Anything, mock.AnythingOfType("string"), mock.AnythingOfType("string"), mock.AnythingOfType("int"), mock.AnythingOfType("int")).
		Return([]datastore.NewSpeciesData{}, nil).Maybe()
	// BG-17: InitFromDatabase requires notification history
	ds.On("GetActiveNotificationHistory", mock.AnythingOfType("time.Time")).
		Return([]datastore.NotificationHistory{}, nil).Maybe()
	ds.On("GetSpeciesFirstDetectionInPeriod", mock.Anything, mock.AnythingOfType("string"), mock.AnythingOfType("string"), mock.AnythingOfType("int"), mock.AnythingOfType("int")).
		Return([]datastore.NewSpeciesData{}, nil).Maybe()

	settings := &conf.SpeciesTrackingSettings{
		Enabled:              true,
		NewSpeciesWindowDays: 365, // Long window so it doesn't interfere
		SyncIntervalMinutes:  60,
		YearlyTracking: conf.YearlyTrackingSettings{
			Enabled:    true,
			ResetMonth: 1,
			ResetDay:   1,
			WindowDays: 30,
		},
		SeasonalTracking: conf.SeasonalTrackingSettings{
			Enabled: false,
		},
	}

	// Create tracker and set to 2023 to simulate starting in previous year
	tracker := NewTrackerFromSettings(ds, settings)
	tracker.SetCurrentYearForTesting(2023) // Use test helper method
	err := tracker.InitFromDatabase()
	require.NoError(t, err)

	speciesName := "Poecile palustris"

	// First detection in 2023
	year2023Time := time.Date(2023, 6, 15, 12, 0, 0, 0, time.UTC)
	tracker.UpdateSpecies(speciesName, year2023Time)

	// Verify state after 2023 detection
	status := tracker.GetSpeciesStatus(speciesName, year2023Time)
	assert.True(t, status.IsNewThisYear, "Expected species to be new in 2023 when first detected")
	assert.Equal(t, 0, status.DaysThisYear, "Expected DaysThisYear to be 0 in 2023")

	// Check in 2024 (after year transition) - this should trigger yearly reset
	year2024Time := time.Date(2024, 6, 15, 12, 0, 0, 0, time.UTC)
	status = tracker.GetSpeciesStatus(speciesName, year2024Time)

	// After year reset, species should be "new this year" because it wasn't detected in 2024 yet
	assert.True(t, status.IsNewThisYear, "Expected species to be new this year after yearly reset. DaysThisYear=%d", status.DaysThisYear)
	assert.Equal(t, 0, status.DaysThisYear, "Expected DaysThisYear to be 0 after yearly reset")

	// Now detect it in 2024
	tracker.UpdateSpecies(speciesName, year2024Time)

	// Check status after detection in 2024
	status = tracker.GetSpeciesStatus(speciesName, year2024Time)

	// Should still be new this year (first detection in 2024)
	assert.True(t, status.IsNewThisYear, "Expected species to be new this year after first detection in 2024")
	assert.Equal(t, 0, status.DaysThisYear, "Expected DaysThisYear to be 0 (just detected)")

	// Should not be new lifetime (seen in 2023)
	assert.False(t, status.IsNew, "Expected species to not be new (lifetime) - seen in previous year")

	// Days since first should be 365 (roughly)
	expectedDays := 365
	assert.InDelta(t, expectedDays, status.DaysSinceFirst, 1, "Expected DaysSinceFirst to be around %d", expectedDays)

	// Test that species becomes "not new this year" after the yearly window expires
	laterTime := year2024Time.Add(35 * 24 * time.Hour) // 35 days later (beyond 30-day window)
	status = tracker.GetSpeciesStatus(speciesName, laterTime)

	assert.False(t, status.IsNewThisYear, "Expected species to not be new this year after yearly window expires")
	assert.Equal(t, 35, status.DaysThisYear, "Expected DaysThisYear to be 35")
}
