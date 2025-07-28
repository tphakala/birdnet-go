package processor

import (
	"fmt"
	"sync"
	"testing"
	"time"

	"github.com/tphakala/birdnet-go/internal/conf"
	"github.com/tphakala/birdnet-go/internal/datastore"
)

// Test constants for day counts and time windows
const (
	oldSpeciesDays    = 20 // Days ago for old species (outside window)
	recentSpeciesDays = 5  // Days ago for recent species (within window)
	newSpeciesWindow  = 14 // Default window for considering species "new"
	syncIntervalMins  = 60 // Default sync interval in minutes
	yearlyWindowDays  = 30 // Default yearly tracking window
	seasonalWindowDays = 21 // Default seasonal tracking window
)


func TestNewSpeciesTracker_NewSpecies(t *testing.T) {
	// Create mock datastore with some historical species data
	ds := &mockSpeciesDatastore{
		species: []datastore.NewSpeciesData{
			{
				ScientificName: "Parus major",
				CommonName:     "Great Tit",
				FirstSeenDate:  time.Now().Add(-oldSpeciesDays * 24 * time.Hour).Format("2006-01-02"),
			},
			{
				ScientificName: "Turdus merula",
				CommonName:     "Common Blackbird",
				FirstSeenDate:  time.Now().Add(-recentSpeciesDays * 24 * time.Hour).Format("2006-01-02"),
			},
		},
	}

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
	tracker := NewSpeciesTrackerFromSettings(ds, settings)
	
	// Initialize from database
	err := tracker.InitFromDatabase()
	if err != nil {
		t.Fatalf("Failed to initialize tracker: %v", err)
	}

	// Test new species (not in database)
	currentTime := time.Now()
	status := tracker.GetSpeciesStatus("Cyanistes caeruleus", currentTime)
	if !status.IsNew {
		t.Errorf("Expected Cyanistes caeruleus to be a new species")
	}
	if status.DaysSinceFirst != 0 {
		t.Errorf("Expected DaysSinceFirst to be 0 for new species, got %d", status.DaysSinceFirst)
	}

	// Test old species (outside window)
	status = tracker.GetSpeciesStatus("Parus major", currentTime)
	if status.IsNew {
		t.Errorf("Expected Parus major to not be a new species (%d days old)", oldSpeciesDays)
	}
	if status.DaysSinceFirst != oldSpeciesDays {
		t.Errorf("Expected DaysSinceFirst to be %d, got %d", oldSpeciesDays, status.DaysSinceFirst)
	}

	// Test recent species (within window)
	status = tracker.GetSpeciesStatus("Turdus merula", currentTime)
	if !status.IsNew {
		t.Errorf("Expected Turdus merula to be a new species (%d days old, within %d-day window)", recentSpeciesDays, newSpeciesWindow)
	}
	if status.DaysSinceFirst != recentSpeciesDays {
		t.Errorf("Expected DaysSinceFirst to be %d, got %d", recentSpeciesDays, status.DaysSinceFirst)
	}
}

func TestNewSpeciesTracker_ConcurrentAccess(t *testing.T) {
	t.Parallel()
	
	ds := &mockSpeciesDatastore{
		species: []datastore.NewSpeciesData{},
	}

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
	tracker := NewSpeciesTrackerFromSettings(ds, settings)
	_ = tracker.InitFromDatabase()

	// Test concurrent reads and writes
	var wg sync.WaitGroup
	species := []string{"Species1", "Species2", "Species3", "Species4", "Species5"}
	currentTime := time.Now()

	// Start multiple goroutines
	for i := 0; i < 10; i++ {
		wg.Add(1)
		go func(id int) {
			defer wg.Done()
			for j := 0; j < 100; j++ {
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

func TestNewSpeciesTracker_UpdateSpecies(t *testing.T) {
	ds := &mockSpeciesDatastore{
		species: []datastore.NewSpeciesData{},
	}

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
	tracker := NewSpeciesTrackerFromSettings(ds, settings)
	_ = tracker.InitFromDatabase()

	currentTime := time.Now()

	// Track a new species
	isNew := tracker.UpdateSpecies("Parus major", currentTime)
	if !isNew {
		t.Errorf("Expected UpdateSpecies to return true for new species")
	}

	// Verify it's now tracked
	status := tracker.GetSpeciesStatus("Parus major", currentTime)
	if !status.IsNew {
		t.Errorf("Expected newly tracked species to be marked as new")
	}
	if status.DaysSinceFirst != 0 {
		t.Errorf("Expected DaysSinceFirst to be 0 for just-tracked species")
	}

	// Update same species again
	isNew = tracker.UpdateSpecies("Parus major", currentTime.Add(time.Hour))
	if isNew {
		t.Errorf("Expected UpdateSpecies to return false for existing species")
	}
}

func TestNewSpeciesTracker_EdgeCases(t *testing.T) {
	// Create tracker with exactly 14 days old species
	ds := &mockSpeciesDatastore{
		species: []datastore.NewSpeciesData{
			{
				ScientificName: "Parus major",
				FirstSeenDate:  time.Now().Add(-14 * 24 * time.Hour).Format("2006-01-02"), // Exactly 14 days ago
			},
		},
	}

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
	tracker := NewSpeciesTrackerFromSettings(ds, settings)
	_ = tracker.InitFromDatabase()

	currentTime := time.Now()

	// Test species exactly at the window boundary
	status := tracker.GetSpeciesStatus("Parus major", currentTime)
	// Should be considered new since it's within the window (14 days is inclusive)
	if !status.IsNew {
		t.Errorf("Expected species at exact window boundary to be considered new")
	}

	// Test empty species name
	status = tracker.GetSpeciesStatus("", currentTime)
	if !status.IsNew {
		t.Errorf("Empty species name should be considered new (not in database)")
	}
	if status.DaysSinceFirst != 0 {
		t.Errorf("Expected DaysSinceFirst to be 0 for empty species name")
	}
}

func TestNewSpeciesTracker_PruneOldEntries(t *testing.T) {
	ds := &mockSpeciesDatastore{
		species: []datastore.NewSpeciesData{
			{
				ScientificName: "Old Species",
				FirstSeenDate:  time.Now().Add(-30 * 24 * time.Hour).Format("2006-01-02"), // 30 days ago
			},
			{
				ScientificName: "Recent Species",
				FirstSeenDate:  time.Now().Add(-5 * 24 * time.Hour).Format("2006-01-02"), // 5 days ago
			},
		},
	}

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
	tracker := NewSpeciesTrackerFromSettings(ds, settings)
	_ = tracker.InitFromDatabase()

	// Initial species count
	if tracker.GetSpeciesCount() != 2 {
		t.Errorf("Expected 2 species, got %d", tracker.GetSpeciesCount())
	}

	// Prune old entries (older than 28 days)
	pruned := tracker.PruneOldEntries()
	if pruned != 1 {
		t.Errorf("Expected 1 species to be pruned, got %d", pruned)
	}

	// Should only have recent species left
	if tracker.GetSpeciesCount() != 1 {
		t.Errorf("Expected 1 species after pruning, got %d", tracker.GetSpeciesCount())
	}

	// Recent species should still be there
	status := tracker.GetSpeciesStatus("Recent Species", time.Now())
	if !status.IsNew {
		t.Errorf("Recent species should still be marked as new after pruning")
	}
}

// Benchmark tests
func BenchmarkNewSpeciesTracker_GetSpeciesStatus(b *testing.B) {
	ds := &mockSpeciesDatastore{
		species: []datastore.NewSpeciesData{},
	}

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
	tracker := NewSpeciesTrackerFromSettings(ds, settings)
	_ = tracker.InitFromDatabase()

	// Pre-populate with some species
	currentTime := time.Now()
	species := make([]string, 100)
	for i := 0; i < 100; i++ {
		species[i] = fmt.Sprintf("Species%d", i)
		tracker.UpdateSpecies(species[i], currentTime.Add(time.Duration(-i)*24*time.Hour))
	}

	b.ResetTimer()

	for i := 0; i < b.N; i++ {
		_ = tracker.GetSpeciesStatus(species[i%100], currentTime)
	}
}

func BenchmarkNewSpeciesTracker_UpdateSpecies(b *testing.B) {
	ds := &mockSpeciesDatastore{
		species: []datastore.NewSpeciesData{},
	}

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
	tracker := NewSpeciesTrackerFromSettings(ds, settings)
	_ = tracker.InitFromDatabase()

	currentTime := time.Now()
	species := make([]string, b.N)
	for i := 0; i < b.N; i++ {
		species[i] = fmt.Sprintf("Species%d", i)
	}

	b.ResetTimer()

	for i := 0; i < b.N; i++ {
		tracker.UpdateSpecies(species[i], currentTime)
	}
}

func BenchmarkNewSpeciesTracker_ConcurrentOperations(b *testing.B) {
	ds := &mockSpeciesDatastore{
		species: []datastore.NewSpeciesData{},
	}

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
	tracker := NewSpeciesTrackerFromSettings(ds, settings)
	_ = tracker.InitFromDatabase()

	// Pre-populate with some species
	currentTime := time.Now()
	species := make([]string, 50)
	for i := 0; i < 50; i++ {
		species[i] = fmt.Sprintf("Species%d", i)
		tracker.UpdateSpecies(species[i], currentTime)
	}

	b.ResetTimer()
	b.RunParallel(func(pb *testing.PB) {
		i := 0
		for pb.Next() {
			if i%2 == 0 {
				_ = tracker.GetSpeciesStatus(species[i%50], currentTime)
			} else {
				tracker.UpdateSpecies(fmt.Sprintf("NewSpecies%d", i), currentTime)
			}
			i++
		}
	})
}

func BenchmarkNewSpeciesTracker_MapMemoryUsage(b *testing.B) {
	ds := &mockSpeciesDatastore{
		species: []datastore.NewSpeciesData{},
	}

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
	tracker := NewSpeciesTrackerFromSettings(ds, settings)
	_ = tracker.InitFromDatabase()

	currentTime := time.Now()
	b.ResetTimer()

	// Benchmark memory allocation when adding many species
	for i := 0; i < b.N; i++ {
		tracker.UpdateSpecies(fmt.Sprintf("UniqueSpecies%d", i), currentTime)
		if i%1000 == 0 {
			// Periodically check a species to prevent optimization
			_ = tracker.GetSpeciesStatus("UniqueSpecies0", currentTime)
		}
	}
}

// Multi-period tracking tests

func TestNewSpeciesTrackerFromSettings_BasicConfiguration(t *testing.T) {
	ds := &mockSpeciesDatastore{species: []datastore.NewSpeciesData{}}
	
	// Create basic configuration
	settings := &conf.SpeciesTrackingSettings{
		Enabled:                true,
		NewSpeciesWindowDays:   30,
		SyncIntervalMinutes:    60,
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
	
	tracker := NewSpeciesTrackerFromSettings(ds, settings)
	if tracker == nil {
		t.Fatal("Expected tracker to be created")
	}
	
	// Verify window settings are applied
	if tracker.GetWindowDays() != 30 {
		t.Errorf("Expected window days to be 30, got %d", tracker.GetWindowDays())
	}
}

func TestMultiPeriodTracking_YearlyTracking(t *testing.T) {
	ds := &mockSpeciesDatastore{species: []datastore.NewSpeciesData{}}
	
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
	
	tracker := NewSpeciesTrackerFromSettings(ds, settings)
	_ = tracker.InitFromDatabase()
	
	currentTime := time.Now()
	speciesName := "Parus major"
	
	// First detection - should be new for all periods
	isNew := tracker.UpdateSpecies(speciesName, currentTime)
	if !isNew {
		t.Error("Expected first detection to be new")
	}
	
	status := tracker.GetSpeciesStatus(speciesName, currentTime)
	
	// Check lifetime tracking
	if !status.IsNew {
		t.Error("Expected species to be new (lifetime)")
	}
	if status.DaysSinceFirst != 0 {
		t.Errorf("Expected DaysSinceFirst to be 0, got %d", status.DaysSinceFirst)
	}
	
	// Check yearly tracking
	if !status.IsNewThisYear {
		t.Error("Expected species to be new this year")
	}
	if status.DaysThisYear != 0 {
		t.Errorf("Expected DaysThisYear to be 0, got %d", status.DaysThisYear)
	}
}

func TestMultiPeriodTracking_SeasonalTracking(t *testing.T) {
	ds := &mockSpeciesDatastore{species: []datastore.NewSpeciesData{}}
	
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
	
	tracker := NewSpeciesTrackerFromSettings(ds, settings)
	_ = tracker.InitFromDatabase()
	
	// Test during spring season (April)
	springTime := time.Date(2024, 4, 15, 12, 0, 0, 0, time.UTC)
	speciesName := "Turdus migratorius"
	
	// First detection in spring
	isNew := tracker.UpdateSpecies(speciesName, springTime)
	if !isNew {
		t.Error("Expected first detection to be new")
	}
	
	status := tracker.GetSpeciesStatus(speciesName, springTime)
	
	// Check seasonal tracking
	if !status.IsNewThisSeason {
		t.Error("Expected species to be new this season")
	}
	if status.DaysThisSeason != 0 {
		t.Errorf("Expected DaysThisSeason to be 0, got %d", status.DaysThisSeason)
	}
	if status.CurrentSeason != "spring" {
		t.Errorf("Expected current season to be 'spring', got '%s'", status.CurrentSeason)
	}
}

func TestSeasonDetection(t *testing.T) {
	ds := &mockSpeciesDatastore{species: []datastore.NewSpeciesData{}}
	
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
	
	tracker := NewSpeciesTrackerFromSettings(ds, settings)
	
	testCases := []struct {
		date           time.Time
		expectedSeason string
	}{
		{time.Date(2024, 1, 15, 12, 0, 0, 0, time.UTC), "winter"},
		{time.Date(2024, 3, 25, 12, 0, 0, 0, time.UTC), "spring"},
		{time.Date(2024, 6, 25, 12, 0, 0, 0, time.UTC), "summer"},
		{time.Date(2024, 9, 25, 12, 0, 0, 0, time.UTC), "fall"},
		{time.Date(2024, 12, 25, 12, 0, 0, 0, time.UTC), "winter"},
	}
	
	for _, tc := range testCases {
		season := tracker.getCurrentSeason(tc.date)
		if season != tc.expectedSeason {
			t.Errorf("For date %v, expected season '%s', got '%s'", 
				tc.date.Format("2006-01-02"), tc.expectedSeason, season)
		}
	}
}

func TestMultiPeriodTracking_CrossPeriodScenarios(t *testing.T) {
	ds := &mockSpeciesDatastore{species: []datastore.NewSpeciesData{}}
	
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
	
	tracker := NewSpeciesTrackerFromSettings(ds, settings)
	_ = tracker.InitFromDatabase()
	
	speciesName := "Cyanistes caeruleus"
	
	// First detection in spring, many days ago (lifetime not new, but season/year new)
	springTime := time.Date(2024, 4, 1, 12, 0, 0, 0, time.UTC)
	tracker.UpdateSpecies(speciesName, springTime)
	
	// Check status much later (after lifetime window expires)
	laterTime := time.Date(2024, 4, 20, 12, 0, 0, 0, time.UTC)
	status := tracker.GetSpeciesStatus(speciesName, laterTime)
	
	// Lifetime should not be new (19 days > 7 day window)
	if status.IsNew {
		t.Error("Expected species to not be new (lifetime) after window expired")
	}
	if status.DaysSinceFirst != 19 {
		t.Errorf("Expected DaysSinceFirst to be 19, got %d", status.DaysSinceFirst)
	}
	
	// Yearly should not be new (19 days > 14 day window)
	// The species was detected this year, but outside the yearly window
	if status.IsNewThisYear {
		t.Errorf("Expected species to not be new this year (19 days > 14 day window). DaysThisYear: %d", status.DaysThisYear)
	}
	if status.DaysThisYear != 19 {
		t.Errorf("Expected DaysThisYear to be 19, got %d", status.DaysThisYear)
	}
	
	// Seasonal should not be new (19 days > 10 day window)
	if status.IsNewThisSeason {
		t.Error("Expected species to not be new this season after window expired")
	}
	if status.DaysThisSeason != 19 {
		t.Errorf("Expected DaysThisSeason to be 19, got %d", status.DaysThisSeason)
	}
}

func TestMultiPeriodTracking_SeasonTransition(t *testing.T) {
	ds := &mockSpeciesDatastore{species: []datastore.NewSpeciesData{}}
	
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
	
	tracker := NewSpeciesTrackerFromSettings(ds, settings)
	_ = tracker.InitFromDatabase()
	
	speciesName := "Hirundo rustica" // Barn Swallow
	
	// First seen in spring
	springTime := time.Date(2024, 4, 15, 12, 0, 0, 0, time.UTC)
	tracker.UpdateSpecies(speciesName, springTime)
	
	// Check in summer (after season transition)
	summerTime := time.Date(2024, 7, 15, 12, 0, 0, 0, time.UTC)
	status := tracker.GetSpeciesStatus(speciesName, summerTime)
	
	// Should be new this season (first time in summer)
	if !status.IsNewThisSeason {
		t.Error("Expected species to be new this season after season transition")
	}
	if status.CurrentSeason != "summer" {
		t.Errorf("Expected current season to be 'summer', got '%s'", status.CurrentSeason)
	}
	
	// Should not be new this year (91 days > 30 day window)
	if status.IsNewThisYear {
		t.Error("Expected species to not be new this year (91 days > 30 day window)")
	}
	
	// Now detect it in summer
	tracker.UpdateSpecies(speciesName, summerTime)
	
	// Check status later in summer
	laterSummerTime := time.Date(2024, 8, 1, 12, 0, 0, 0, time.UTC)
	status = tracker.GetSpeciesStatus(speciesName, laterSummerTime)
	
	// Should now have records for both seasons
	if status.DaysThisSeason != 17 { // Days since July 15
		t.Errorf("Expected DaysThisSeason to be 17, got %d", status.DaysThisSeason)
	}
}

func TestMultiPeriodTracking_YearReset(t *testing.T) {
	ds := &mockSpeciesDatastore{species: []datastore.NewSpeciesData{}}
	
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
	tracker := NewSpeciesTrackerFromSettings(ds, settings)
	tracker.SetCurrentYearForTesting(2023) // Use test helper method
	_ = tracker.InitFromDatabase()
	
	speciesName := "Poecile palustris"
	
	// First detection in 2023
	year2023Time := time.Date(2023, 6, 15, 12, 0, 0, 0, time.UTC)
	tracker.UpdateSpecies(speciesName, year2023Time)
	
	// Verify state after 2023 detection
	status := tracker.GetSpeciesStatus(speciesName, year2023Time)
	if !status.IsNewThisYear {
		t.Error("Expected species to be new in 2023 when first detected")
	}
	if status.DaysThisYear != 0 {
		t.Errorf("Expected DaysThisYear to be 0 in 2023, got %d", status.DaysThisYear)
	}
	
	// Check in 2024 (after year transition) - this should trigger yearly reset
	year2024Time := time.Date(2024, 6, 15, 12, 0, 0, 0, time.UTC)
	status = tracker.GetSpeciesStatus(speciesName, year2024Time)
	
	// After year reset, species should be "new this year" because it wasn't detected in 2024 yet
	if !status.IsNewThisYear {
		t.Errorf("Expected species to be new this year after yearly reset. IsNewThisYear=%v, DaysThisYear=%d", status.IsNewThisYear, status.DaysThisYear)
	}
	if status.DaysThisYear != 0 {
		t.Errorf("Expected DaysThisYear to be 0 after yearly reset, got %d", status.DaysThisYear)
	}
	
	// Now detect it in 2024
	tracker.UpdateSpecies(speciesName, year2024Time)
	
	// Check status after detection in 2024
	status = tracker.GetSpeciesStatus(speciesName, year2024Time)
	
	// Should still be new this year (first detection in 2024)
	if !status.IsNewThisYear {
		t.Error("Expected species to be new this year after first detection in 2024")
	}
	if status.DaysThisYear != 0 {
		t.Errorf("Expected DaysThisYear to be 0 (just detected), got %d", status.DaysThisYear)
	}
	
	// Should not be new lifetime (seen in 2023)
	if status.IsNew {
		t.Error("Expected species to not be new (lifetime) - seen in previous year")
	}
	
	// Days since first should be 365 (roughly)
	expectedDays := 365
	if status.DaysSinceFirst < expectedDays-1 || status.DaysSinceFirst > expectedDays+1 {
		t.Errorf("Expected DaysSinceFirst to be around %d, got %d", expectedDays, status.DaysSinceFirst)
	}
	
	// Test that species becomes "not new this year" after the yearly window expires
	laterTime := year2024Time.Add(35 * 24 * time.Hour) // 35 days later (beyond 30-day window)
	status = tracker.GetSpeciesStatus(speciesName, laterTime)
	
	if status.IsNewThisYear {
		t.Error("Expected species to not be new this year after yearly window expires")
	}
	if status.DaysThisYear != 35 {
		t.Errorf("Expected DaysThisYear to be 35, got %d", status.DaysThisYear)
	}
}

