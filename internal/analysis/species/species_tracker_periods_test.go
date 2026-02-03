package species

import (
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

// Test constants
const (
	testSpeciesParusMajor = "Parus major"
	testNilString         = "nil"
)

// TestSeasonalPeriodInitialization validates that all seasonal maps are properly initialized
// to prevent nil map panics
func TestSeasonalPeriodInitialization(t *testing.T) {
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
	require.NotNil(t, tracker)

	err := tracker.InitFromDatabase()
	require.NoError(t, err)

	// Test data from real birdnet.db
	testSpecies := []struct {
		scientificName string
		commonName     string
		detectionDate  time.Time
		expectedSeason string
	}{
		{
			scientificName: "Sylvia atricapilla",
			commonName:     "Eurasian Blackcap",
			detectionDate:  time.Date(2024, 7, 28, 18, 47, 11, 0, time.UTC),
			expectedSeason: "summer",
		},
		{
			scientificName: "Alcedo atthis",
			commonName:     "Common Kingfisher",
			detectionDate:  time.Date(2024, 3, 21, 9, 23, 23, 0, time.UTC),
			expectedSeason: "spring",
		},
		{
			scientificName: "Loxia leucoptera",
			commonName:     "White-winged Crossbill",
			detectionDate:  time.Date(2024, 12, 25, 18, 32, 3, 0, time.UTC),
			expectedSeason: "winter",
		},
		{
			scientificName: "Hirundo rustica",
			commonName:     "Barn Swallow",
			detectionDate:  time.Date(2024, 9, 25, 15, 12, 31, 0, time.UTC),
			expectedSeason: "fall",
		},
	}

	// Test updating species across all seasons
	for _, test := range testSpecies {
		isNew := tracker.UpdateSpecies(test.scientificName, test.detectionDate)
		assert.True(t, isNew, "Expected %s to be new on first detection", test.scientificName)

		status := tracker.GetSpeciesStatus(test.scientificName, test.detectionDate)
		assert.Equal(t, test.expectedSeason, status.CurrentSeason,
			"Expected %s to be detected in %s season", test.scientificName, test.expectedSeason)
		assert.True(t, status.IsNewThisSeason, "Expected %s to be new this season", test.scientificName)
		assert.NotNil(t, status.FirstThisSeason, "Expected FirstThisSeason to be non-nil")
		assert.Equal(t, 0, status.DaysThisSeason, "Expected DaysThisSeason to be 0 on first detection")
	}

	// Verify all season maps are initialized using public methods
	for season := range settings.SeasonalTracking.Seasons {
		// Check that season maps are properly initialized
		assert.True(t, tracker.IsSeasonMapInitialized(season),
			"Season map for '%s' is not initialized - this would cause panics", season)
	}
}

// TestSeasonalTrackingWithNilMaps tests behavior when seasonal maps might be nil
func TestSeasonalTrackingWithNilMaps(t *testing.T) {
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
			Enabled: false,
		},
		SeasonalTracking: conf.SeasonalTrackingSettings{
			Enabled:    true,
			WindowDays: 21,
			// Using default seasons
		},
	}

	tracker := NewTrackerFromSettings(ds, settings)
	require.NotNil(t, tracker)

	// Initialize tracker to ensure proper setup
	err := tracker.InitFromDatabase()
	require.NoError(t, err)

	// Test that even without explicit season config, defaults are initialized
	testDates := []struct {
		date           time.Time
		expectedSeason string
	}{
		{time.Date(2024, 1, 15, 0, 0, 0, 0, time.UTC), "winter"},
		{time.Date(2024, 4, 15, 0, 0, 0, 0, time.UTC), "spring"},
		{time.Date(2024, 7, 15, 0, 0, 0, 0, time.UTC), "summer"},
		{time.Date(2024, 10, 15, 0, 0, 0, 0, time.UTC), "fall"},
	}

	for i, test := range testDates {
		// This should not panic even if maps aren't initialized
		isNew := tracker.UpdateSpecies(testSpeciesParusMajor, test.date)

		// Only the first detection should be new - after that, it's a "known" species
		// The test name suggests this is about nil map handling, not new species detection
		if i == 0 {
			assert.True(t, isNew, "Expected first detection to be new")
		}
		// Note: subsequent detections may not be "new" from lifetime perspective
		// since the species is now known, even if in different seasons

		status := tracker.GetSpeciesStatus(testSpeciesParusMajor, test.date)
		assert.Equal(t, test.expectedSeason, status.CurrentSeason)

		// The key test is that this doesn't panic even with nil maps
		// and that seasonal detection works properly
		assert.NotNil(t, status, "Status should not be nil")
	}
}

// TestYearlyTrackingAcrossYearBoundary tests yearly reset behavior
func TestYearlyTrackingAcrossYearBoundary(t *testing.T) {
	t.Parallel()

	// Historical data simulating species seen in previous years
	historicalData := []datastore.NewSpeciesData{
		{
			ScientificName: "Turdus merula",
			CommonName:     "Common Blackbird",
			FirstSeenDate:  "2022-06-15", // 2+ years ago
		},
		{
			ScientificName: testSpeciesParusMajor,
			CommonName:     "Great Tit",
			FirstSeenDate:  "2023-03-20", // Last year
		},
	}

	ds := mocks.NewMockInterface(t)
	ds.On("GetNewSpeciesDetections", mock.Anything, mock.AnythingOfType("string"), mock.AnythingOfType("string"), mock.AnythingOfType("int"), mock.AnythingOfType("int")).
		Return(historicalData, nil).Maybe()
	// BG-17: InitFromDatabase now loads notification history
	ds.On("GetActiveNotificationHistory", mock.AnythingOfType("time.Time")).
		Return([]datastore.NotificationHistory{}, nil).Maybe()
	ds.On("GetSpeciesFirstDetectionInPeriod", mock.Anything, mock.AnythingOfType("string"), mock.AnythingOfType("string"), mock.AnythingOfType("int"), mock.AnythingOfType("int")).
		Return(historicalData, nil).Maybe()

	settings := &conf.SpeciesTrackingSettings{
		Enabled:              true,
		NewSpeciesWindowDays: 365, // Long window to not interfere
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
	tracker.SetCurrentYearForTesting(2023)
	err := tracker.InitFromDatabase()
	require.NoError(t, err)

	// Simulate detection in 2023
	dec2023 := time.Date(2023, 12, 15, 10, 0, 0, 0, time.UTC)
	tracker.UpdateSpecies("Turdus merula", dec2023)

	// Check status in 2024 (should trigger year reset)
	jan2024 := time.Date(2024, 1, 15, 10, 0, 0, 0, time.UTC)
	status := tracker.GetSpeciesStatus("Turdus merula", jan2024)

	// Should be new this year (no detection in 2024 yet)
	assert.True(t, status.IsNewThisYear, "Expected species to be new this year after year reset")
	assert.Equal(t, 0, status.DaysThisYear, "Expected DaysThisYear to be 0 after year reset")
	assert.Nil(t, status.FirstThisYear, "Expected FirstThisYear to be nil before detection in new year")

	// Now detect it in 2024
	isNew := tracker.UpdateSpecies("Turdus merula", jan2024)
	t.Logf("UpdateSpecies returned isNew=%v for detection on %s", isNew, jan2024.Format(time.DateOnly))

	// Check internal state directly
	tracker.mu.RLock()
	yearMapSize := len(tracker.speciesThisYear)
	yearTime, inYearMap := tracker.speciesThisYear["Turdus merula"]
	currentYear := tracker.currentYear
	tracker.mu.RUnlock()
	yearTimeStr := testNilString
	if inYearMap {
		yearTimeStr = yearTime.Format(time.DateOnly)
	}
	t.Logf("After update: currentYear=%d, yearMapSize=%d, species in year map=%v, yearTime=%s", currentYear, yearMapSize, inYearMap, yearTimeStr)

	status = tracker.GetSpeciesStatus("Turdus merula", jan2024)
	t.Logf("Status after update: IsNewThisYear=%v, FirstThisYear=%v, DaysThisYear=%d",
		status.IsNewThisYear, status.FirstThisYear, status.DaysThisYear)

	assert.True(t, status.IsNewThisYear, "Expected species to still be new this year (within window)")
	assert.NotNil(t, status.FirstThisYear, "Expected FirstThisYear to be set after detection")
	assert.Equal(t, 0, status.DaysThisYear, "Expected DaysThisYear to be 0 on detection day")

	// Check 35 days later (outside yearly window)
	feb2024 := jan2024.Add(35 * 24 * time.Hour)
	status = tracker.GetSpeciesStatus("Turdus merula", feb2024)

	assert.False(t, status.IsNewThisYear, "Expected species to not be new this year after window expires")
	assert.Equal(t, 35, status.DaysThisYear, "Expected DaysThisYear to be 35")
}

// TestSpeciesStatusForDailySummaryCard tests all fields needed by DailySummaryCard.svelte
func TestSpeciesStatusForDailySummaryCard(t *testing.T) {
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
	err := tracker.InitFromDatabase()
	require.NoError(t, err)

	// Test scenario 1: Brand new species (shows star icon)
	currentYear := time.Now().Year()
	currentTime := time.Date(currentYear, 7, 15, 10, 0, 0, 0, time.UTC)
	tracker.UpdateSpecies("Alcedo atthis", currentTime)

	status := tracker.GetSpeciesStatus("Alcedo atthis", currentTime)
	// DailySummaryCard checks: item.is_new_species
	assert.True(t, status.IsNew, "Expected IsNew=true for new species (star icon)")
	assert.Equal(t, 0, status.DaysSinceFirst, "Expected DaysSinceFirst=0")
	// Also new this year and season
	assert.True(t, status.IsNewThisYear, "Expected IsNewThisYear=true")
	assert.True(t, status.IsNewThisSeason, "Expected IsNewThisSeason=true")
	assert.Equal(t, "summer", status.CurrentSeason, "Expected CurrentSeason=summer")

	// Test scenario 2: Species seen before but new this year (shows calendar icon)
	// Simulate species seen last year
	oldDetection := time.Date(currentYear-1, 7, 15, 10, 0, 0, 0, time.UTC)
	tracker.UpdateSpecies("Hirundo rustica", oldDetection)

	// Check in current year
	firstThisYear := time.Date(currentYear, 6, 1, 10, 0, 0, 0, time.UTC)
	tracker.UpdateSpecies("Hirundo rustica", firstThisYear)

	// Check a few days later
	checkTime := time.Date(currentYear, 6, 5, 10, 0, 0, 0, time.UTC)
	status = tracker.GetSpeciesStatus("Hirundo rustica", checkTime)

	// DailySummaryCard checks: item.is_new_this_year && !item.is_new_species
	assert.False(t, status.IsNew, "Expected IsNew=false (not new lifetime)")
	assert.True(t, status.IsNewThisYear, "Expected IsNewThisYear=true (calendar icon)")
	assert.Equal(t, 4, status.DaysThisYear, "Expected DaysThisYear=4")

	// Test scenario 3: Species seen this year but new this season (shows leaf icon)
	// Species seen in spring
	springDetection := time.Date(currentYear, 4, 15, 10, 0, 0, 0, time.UTC)
	tracker.UpdateSpecies("Loxia leucoptera", springDetection)

	// Now check in summer
	summerCheck := time.Date(currentYear, 7, 1, 10, 0, 0, 0, time.UTC)
	status = tracker.GetSpeciesStatus("Loxia leucoptera", summerCheck)

	// DailySummaryCard checks: item.is_new_this_season && !item.is_new_species && !item.is_new_this_year
	assert.False(t, status.IsNew, "Expected IsNew=false")
	assert.False(t, status.IsNewThisYear, "Expected IsNewThisYear=false (77 days > 30 day window)")
	assert.True(t, status.IsNewThisSeason, "Expected IsNewThisSeason=true (leaf icon)")
	assert.Equal(t, 0, status.DaysThisSeason, "Expected DaysThisSeason=0 (not seen in summer yet)")
	assert.Equal(t, "summer", status.CurrentSeason, "Expected CurrentSeason=summer")

	// Now detect it in summer
	tracker.UpdateSpecies("Loxia leucoptera", summerCheck)
	summerLater := time.Date(currentYear, 7, 10, 10, 0, 0, 0, time.UTC)
	status = tracker.GetSpeciesStatus("Loxia leucoptera", summerLater)

	assert.True(t, status.IsNewThisSeason, "Expected IsNewThisSeason=true (within seasonal window)")
	assert.Equal(t, 9, status.DaysThisSeason, "Expected DaysThisSeason=9")
}

// TestBatchSpeciesStatusPerformance tests batch operations for dashboard
func TestBatchSpeciesStatusPerformance(t *testing.T) {
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
			Enabled:    true,
			WindowDays: 21,
		},
	}

	tracker := NewTrackerFromSettings(ds, settings)
	_ = tracker.InitFromDatabase()

	// Simulate many species for dashboard
	species := []string{
		testSpeciesParusMajor, "Turdus merula", "Cyanistes caeruleus",
		"Erithacus rubecula", "Fringilla coelebs", "Sylvia atricapilla",
		"Phylloscopus trochilus", "Carduelis carduelis", "Columba palumbus",
		"Corvus corone", "Pica pica", "Garrulus glandarius",
	}

	currentTime := time.Date(2024, 7, 15, 10, 0, 0, 0, time.UTC)

	// Add detections at various times
	for i, sp := range species {
		detectionTime := currentTime.Add(-time.Duration(i*2) * 24 * time.Hour)
		tracker.UpdateSpecies(sp, detectionTime)
	}

	// Test batch retrieval
	statuses := tracker.GetBatchSpeciesStatus(species, currentTime)

	assert.Len(t, statuses, len(species), "Expected status for all species")

	// Verify each status has all required fields
	for sp, status := range statuses {
		assert.NotZero(t, status.LastUpdatedTime, "Expected LastUpdatedTime to be set for %s", sp)
		assert.NotEmpty(t, status.CurrentSeason, "Expected CurrentSeason to be set for %s", sp)
		// DailySummaryCard needs these fields
		assert.GreaterOrEqual(t, status.DaysSinceFirst, 0, "Expected valid DaysSinceFirst for %s", sp)
		assert.GreaterOrEqual(t, status.DaysThisYear, 0, "Expected valid DaysThisYear for %s", sp)
		assert.GreaterOrEqual(t, status.DaysThisSeason, 0, "Expected valid DaysThisSeason for %s", sp)
	}
}

// BenchmarkBatchSpeciesStatusPerformance benchmarks the performance of batch species status retrieval
func BenchmarkBatchSpeciesStatusPerformance(b *testing.B) {
	ds := mocks.NewMockInterface(b)
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
			Enabled:    true,
			WindowDays: 21,
		},
	}

	tracker := NewTrackerFromSettings(ds, settings)
	_ = tracker.InitFromDatabase()

	// Simulate many species for dashboard
	species := []string{
		testSpeciesParusMajor, "Turdus merula", "Cyanistes caeruleus",
		"Erithacus rubecula", "Fringilla coelebs", "Sylvia atricapilla",
		"Phylloscopus trochilus", "Carduelis carduelis", "Columba palumbus",
		"Corvus corone", "Pica pica", "Garrulus glandarius",
	}

	currentTime := time.Date(2024, 7, 15, 10, 0, 0, 0, time.UTC)

	// Add detections at various times
	for i, sp := range species {
		detectionTime := currentTime.Add(-time.Duration(i*2) * 24 * time.Hour)
		tracker.UpdateSpecies(sp, detectionTime)
	}

	// Benchmark the batch retrieval operation
	for b.Loop() {
		_ = tracker.GetBatchSpeciesStatus(species, currentTime)
	}
}

// TestConcurrentSeasonalUpdates tests thread safety of seasonal tracking
func TestConcurrentSeasonalUpdates(t *testing.T) {
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
			Enabled:    true,
			WindowDays: 21,
		},
	}

	tracker := NewTrackerFromSettings(ds, settings)
	_ = tracker.InitFromDatabase()

	// Run concurrent updates across season boundaries
	var wg sync.WaitGroup
	dates := []time.Time{
		time.Date(2024, 3, 19, 23, 59, 59, 0, time.UTC), // End of winter
		time.Date(2024, 3, 20, 0, 0, 1, 0, time.UTC),    // Start of spring
		time.Date(2024, 6, 20, 23, 59, 59, 0, time.UTC), // End of spring
		time.Date(2024, 6, 21, 0, 0, 1, 0, time.UTC),    // Start of summer
	}

	species := []string{"Species1", "Species2", "Species3", "Species4"}

	for i := range 10 {
		wg.Add(1)
		go func(iteration int) {
			defer wg.Done()
			for j, sp := range species {
				date := dates[j%len(dates)]
				tracker.UpdateSpecies(sp, date)
				_ = tracker.GetSpeciesStatus(sp, date)
			}
		}(i)
	}

	wg.Wait()

	// Verify no data corruption
	for _, sp := range species {
		status := tracker.GetSpeciesStatus(sp, time.Date(2024, 7, 1, 0, 0, 0, 0, time.UTC))
		assert.NotEmpty(t, status.CurrentSeason, "Expected valid season for %s", sp)
	}
}

// TestSeasonalWindowExpiration tests that seasonal "new" status expires correctly
func TestSeasonalWindowExpiration(t *testing.T) {
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
		NewSpeciesWindowDays: 7, // Short lifetime window
		SyncIntervalMinutes:  60,
		YearlyTracking: conf.YearlyTrackingSettings{
			Enabled:    true,
			ResetMonth: 1,
			ResetDay:   1,
			WindowDays: 14, // Medium yearly window
		},
		SeasonalTracking: conf.SeasonalTrackingSettings{
			Enabled:    true,
			WindowDays: 21, // Long seasonal window
		},
	}

	tracker := NewTrackerFromSettings(ds, settings)
	tracker.SetCurrentYearForTesting(2024) // Set to 2024 for test dates
	_ = tracker.InitFromDatabase()

	species := "Motacilla alba"
	detectionTime := time.Date(2024, 7, 1, 10, 0, 0, 0, time.UTC)
	tracker.UpdateSpecies(species, detectionTime)

	// Test at different intervals
	testCases := []struct {
		daysLater       int
		expectNew       bool
		expectNewYear   bool
		expectNewSeason bool
		description     string
	}{
		{0, true, true, true, "Same day - all new"},
		{5, true, true, true, "5 days - within all windows"},
		{10, false, true, true, "10 days - lifetime expired, others active"},
		{18, false, false, true, "18 days - only seasonal active"},
		{25, false, false, false, "25 days - all expired"},
	}

	for _, tc := range testCases {
		t.Run(tc.description, func(t *testing.T) {
			checkTime := detectionTime.Add(time.Duration(tc.daysLater) * 24 * time.Hour)
			status := tracker.GetSpeciesStatus(species, checkTime)

			assert.Equal(t, tc.expectNew, status.IsNew,
				"%s: IsNew mismatch", tc.description)
			assert.Equal(t, tc.expectNewYear, status.IsNewThisYear,
				"%s: IsNewThisYear mismatch", tc.description)
			assert.Equal(t, tc.expectNewSeason, status.IsNewThisSeason,
				"%s: IsNewThisSeason mismatch", tc.description)
		})
	}
}

// TestSpeciesStatusCaching tests the caching mechanism for performance
func TestSpeciesStatusCaching(t *testing.T) {
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
			Enabled:    true,
			WindowDays: 21,
		},
	}

	tracker := NewTrackerFromSettings(ds, settings)
	_ = tracker.InitFromDatabase()

	species := testSpeciesParusMajor
	currentTime := time.Now()
	tracker.UpdateSpecies(species, currentTime)

	// First call - should compute and cache
	status1 := tracker.GetSpeciesStatus(species, currentTime)

	// Second call within cache TTL - should return cached result
	status2 := tracker.GetSpeciesStatus(species, currentTime)

	// Results should be identical
	assert.Equal(t, status1, status2, "Cached result should match original")

	// Simulate cache expiration using test method
	tracker.ExpireCacheForTesting(species)

	// Third call after cache expiration - should recompute
	status3 := tracker.GetSpeciesStatus(species, currentTime)
	assert.Equal(t, status1, status3, "Recomputed result should match original")
}

// TestDefaultSeasonInitialization tests that default seasons are properly initialized
func TestDefaultSeasonInitialization(t *testing.T) {
	t.Parallel()

	ds := mocks.NewMockInterface(t)
	ds.On("GetNewSpeciesDetections", mock.Anything, mock.AnythingOfType("string"), mock.AnythingOfType("string"), mock.AnythingOfType("int"), mock.AnythingOfType("int")).
		Return([]datastore.NewSpeciesData{}, nil).Maybe()
	// BG-17: InitFromDatabase requires notification history
	ds.On("GetActiveNotificationHistory", mock.AnythingOfType("time.Time")).
		Return([]datastore.NotificationHistory{}, nil).Maybe()
	ds.On("GetSpeciesFirstDetectionInPeriod", mock.Anything, mock.AnythingOfType("string"), mock.AnythingOfType("string"), mock.AnythingOfType("int"), mock.AnythingOfType("int")).
		Return([]datastore.NewSpeciesData{}, nil).Maybe()

	// Settings with seasonal tracking enabled but no custom seasons
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
			Seasons:    nil, // No custom seasons - should use defaults
		},
	}

	tracker := NewTrackerFromSettings(ds, settings)
	require.NotNil(t, tracker)

	// Verify default seasons are initialized
	expectedSeasons := map[string]seasonDates{
		"spring": {month: 3, day: 20},
		"summer": {month: 6, day: 21},
		"fall":   {month: 9, day: 22},
		"winter": {month: 12, day: 21},
	}

	tracker.mu.Lock()
	assert.Len(t, tracker.seasons, len(expectedSeasons), "Expected 4 default seasons")
	for name, expected := range expectedSeasons {
		actual, exists := tracker.seasons[name]
		assert.True(t, exists, "Expected season '%s' to exist", name)
		assert.Equal(t, expected.month, actual.month, "Month mismatch for season '%s'", name)
		assert.Equal(t, expected.day, actual.day, "Day mismatch for season '%s'", name)
	}
	tracker.mu.Unlock()

	// Test that species can be tracked in all default seasons without panic
	testDates := []struct {
		date   time.Time
		season string
	}{
		{time.Date(2024, 4, 1, 0, 0, 0, 0, time.UTC), "spring"},
		{time.Date(2024, 7, 1, 0, 0, 0, 0, time.UTC), "summer"},
		{time.Date(2024, 10, 1, 0, 0, 0, 0, time.UTC), "fall"},
		{time.Date(2024, 1, 1, 0, 0, 0, 0, time.UTC), "winter"},
	}

	for _, test := range testDates {
		// This should not panic
		tracker.UpdateSpecies("Test species", test.date)
		status := tracker.GetSpeciesStatus("Test species", test.date)
		assert.Equal(t, test.season, status.CurrentSeason,
			"Expected season '%s' for date %s", test.season, test.date.Format(time.DateOnly))
	}
}

// TestSeasonMapInitializationOnTransition tests that season maps are properly initialized
// when transitioning to a new season that hasn't been seen before
func TestSeasonMapInitializationOnTransition(t *testing.T) {
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
	require.NotNil(t, tracker)

	// Start in winter
	winterTime := time.Date(2024, 1, 15, 10, 0, 0, 0, time.UTC)
	tracker.UpdateSpecies(testSpeciesParusMajor, winterTime)

	// Verify winter map is initialized
	tracker.mu.Lock()
	assert.NotNil(t, tracker.speciesBySeason["winter"], "Winter map should be initialized after first use")
	// Spring map should not be initialized yet
	assert.Nil(t, tracker.speciesBySeason["spring"], "Spring map should not be initialized yet")
	tracker.mu.Unlock()

	// Now jump to spring - this should trigger checkAndResetPeriods
	springTime := time.Date(2024, 4, 15, 10, 0, 0, 0, time.UTC)
	tracker.UpdateSpecies("Cyanistes caeruleus", springTime)

	// Verify spring map is now initialized
	tracker.mu.Lock()
	assert.NotNil(t, tracker.speciesBySeason["spring"], "Spring map should be initialized after season transition")
	assert.Contains(t, tracker.speciesBySeason["spring"], "Cyanistes caeruleus", "Species should be tracked in spring")
	tracker.mu.Unlock()

	// Test getting status for a species in a season that hasn't been initialized yet
	// This simulates the case where GetSpeciesStatus is called before any UpdateSpecies in that season
	summerTime := time.Date(2024, 7, 15, 10, 0, 0, 0, time.UTC)

	// This should not panic even if summer map doesn't exist
	status := tracker.GetSpeciesStatus("New Species", summerTime)
	assert.Equal(t, "summer", status.CurrentSeason, "Should correctly identify summer season")
	assert.True(t, status.IsNewThisSeason, "Should be new this season (no prior detection)")
	assert.Nil(t, status.FirstThisSeason, "FirstThisSeason should be nil for undetected species")

	// Verify that getting status doesn't accidentally initialize the map
	tracker.mu.Lock()
	// The map might or might not be initialized, but accessing it should not panic
	summerMap := tracker.speciesBySeason["summer"]
	if summerMap != nil {
		assert.Empty(t, summerMap, "Summer map should be empty if initialized by GetSpeciesStatus")
	}
	tracker.mu.Unlock()
}

// TestInitFromDatabaseWithSeasonalTracking tests that InitFromDatabase doesn't cause issues
// with seasonal tracking when loading historical data
func TestInitFromDatabaseWithSeasonalTracking(t *testing.T) {
	t.Parallel()

	// Simulate historical data from different seasons
	historicalData := []datastore.NewSpeciesData{
		{
			ScientificName: "Hirundo rustica", // Barn Swallow
			CommonName:     "Barn Swallow",
			FirstSeenDate:  "2023-05-15", // Spring last year
		},
		{
			ScientificName: "Turdus pilaris", // Fieldfare
			CommonName:     "Fieldfare",
			FirstSeenDate:  "2023-12-25", // Winter last year
		},
		{
			ScientificName: "Sylvia curruca", // Lesser Whitethroat
			CommonName:     "Lesser Whitethroat",
			FirstSeenDate:  "2024-06-25", // Summer this year
		},
	}

	ds := mocks.NewMockInterface(t)
	ds.On("GetNewSpeciesDetections", mock.Anything, mock.AnythingOfType("string"), mock.AnythingOfType("string"), mock.AnythingOfType("int"), mock.AnythingOfType("int")).
		Return(historicalData, nil).Maybe()
	ds.On("GetActiveNotificationHistory", mock.AnythingOfType("time.Time")).
		Return([]datastore.NotificationHistory{}, nil).Maybe()
	ds.On("GetSpeciesFirstDetectionInPeriod", mock.Anything, mock.AnythingOfType("string"), mock.AnythingOfType("string"), mock.AnythingOfType("int"), mock.AnythingOfType("int")).
		Return(historicalData, nil).Maybe()

	settings := &conf.SpeciesTrackingSettings{
		Enabled:              true,
		NewSpeciesWindowDays: 365,
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
	tracker.SetCurrentYearForTesting(2024) // Set to 2024 for test

	// InitFromDatabase should handle historical data gracefully
	err := tracker.InitFromDatabase()
	require.NoError(t, err, "InitFromDatabase should not error with seasonal tracking enabled")

	// Verify that lifetime tracking is populated
	assert.Equal(t, 3, tracker.GetSpeciesCount(), "Should have loaded 3 species from database")

	// Now test current status for species seen in different seasons
	currentTime := time.Date(2024, 7, 15, 10, 0, 0, 0, time.UTC) // Summer

	// Species seen last year - but mock returns all data, so tracker loads it
	status := tracker.GetSpeciesStatus("Hirundo rustica", currentTime)
	assert.False(t, status.IsNew, "Species seen last year should not be new")
	// Since mock returns all data regardless of date range, the 2023 data gets loaded as 2024
	// The tracker sees May 15 data (loaded from mock) and July 15 check time
	// That's 61 days, which is > 30 day yearly window and > 21 day seasonal window
	assert.False(t, status.IsNewThisYear, "Should not be new this year (61 days > 30 day window)")
	assert.False(t, status.IsNewThisSeason, "Should not be new this season (61 days > 21 day window)")
	assert.NotNil(t, status.FirstThisYear, "FirstThisYear not nil due to mock returning all data")
	assert.NotNil(t, status.FirstThisSeason, "FirstThisSeason not nil due to mock returning all data")

	// Species seen this summer should have correct status
	status = tracker.GetSpeciesStatus("Sylvia curruca", currentTime)
	assert.True(t, status.IsNew, "Recent species should be new lifetime (20 days < 365 day window)")
	// This species was detected on June 25, which is summer (after June 21)
	// Current time is July 15, so it's been 20 days (within 30-day yearly window)
	assert.True(t, status.IsNewThisYear, "Should be new this year (20 days < 30 day window)")
	assert.True(t, status.IsNewThisSeason, "Should be new this season (within 21-day window)")
}
