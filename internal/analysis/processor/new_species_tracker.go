// new_species_tracker.go
package processor

import (
	"sync"
	"time"

	"github.com/tphakala/birdnet-go/internal/conf"
	"github.com/tphakala/birdnet-go/internal/datastore"
	"github.com/tphakala/birdnet-go/internal/errors"
)

// SpeciesStatus represents the tracking status of a species across multiple periods
type SpeciesStatus struct {
	// Existing lifetime tracking
	FirstSeenTime    time.Time
	IsNew            bool
	DaysSinceFirst   int
	LastUpdatedTime  time.Time // For cache management
	
	// Multi-period tracking
	FirstThisYear    *time.Time // First detection this calendar year
	FirstThisSeason  *time.Time // First detection this season
	CurrentSeason    string     // Current season name
	
	// Status flags for each period
	IsNewThisYear    bool       // First time this year
	IsNewThisSeason  bool       // First time this season
	DaysThisYear     int        // Days since first this year
	DaysThisSeason   int        // Days since first this season
}

// NewSpeciesTracker tracks species detections and identifies new species
// within a configurable time window. Designed for minimal memory allocations.
type NewSpeciesTracker struct {
	mu               sync.RWMutex
	
	// Lifetime tracking (existing)
	speciesFirstSeen map[string]time.Time // scientificName -> first detection time
	windowDays       int                  // Days to consider a species "new"
	
	// Multi-period tracking
	speciesThisYear  map[string]time.Time // scientificName -> first detection this year
	speciesBySeason  map[string]map[string]time.Time // season -> scientificName -> first detection time
	currentYear      int
	currentSeason    string
	seasons          map[string]seasonDates // season name -> start dates
	
	// Configuration
	ds               datastore.Interface
	lastSyncTime     time.Time
	syncIntervalMins int
	yearlyEnabled    bool
	seasonalEnabled  bool
	yearlyWindowDays int
	seasonalWindowDays int
	resetMonth       int // Month to reset yearly tracking (1-12)
	resetDay         int // Day to reset yearly tracking (1-31)
	
	// Pre-allocated for efficiency
	statusBuffer     SpeciesStatus // Reusable buffer for status calculations
}

// seasonDates represents the start date for a season
type seasonDates struct {
	month int
	day   int
}

// NewSpeciesTracker creates a new species tracker with the specified window
func NewSpeciesTrackerWithConfig(ds datastore.Interface, windowDays, syncIntervalMins int) *NewSpeciesTracker {
	if windowDays <= 0 {
		windowDays = 14 // Default to 14 days
	}
	if syncIntervalMins <= 0 {
		syncIntervalMins = 60 // Default to 1 hour
	}
	
	now := time.Now()
	tracker := &NewSpeciesTracker{
		// Lifetime tracking
		speciesFirstSeen: make(map[string]time.Time, 100), // Pre-allocate for ~100 species
		windowDays:       windowDays,
		
		// Multi-period tracking
		speciesThisYear:  make(map[string]time.Time, 100),
		speciesBySeason:  make(map[string]map[string]time.Time),
		currentYear:      now.Year(),
		seasons:          make(map[string]seasonDates),
		
		// Configuration
		ds:               ds,
		syncIntervalMins: syncIntervalMins,
		yearlyEnabled:    false,  // Will be set by NewSpeciesTrackerFromSettings
		seasonalEnabled:  false,  // Will be set by NewSpeciesTrackerFromSettings
		yearlyWindowDays: 30,     // Default
		seasonalWindowDays: 21,   // Default
		resetMonth:       1,      // January
		resetDay:         1,      // 1st
	}
	
	// Initialize with default seasons
	tracker.initializeDefaultSeasons()
	tracker.currentSeason = tracker.getCurrentSeason(now)
	
	return tracker
}

// NewSpeciesTrackerFromSettings creates a tracker from configuration settings
func NewSpeciesTrackerFromSettings(ds datastore.Interface, settings *conf.SpeciesTrackingSettings) *NewSpeciesTracker {
	now := time.Now()
	tracker := &NewSpeciesTracker{
		// Lifetime tracking
		speciesFirstSeen: make(map[string]time.Time, 100),
		windowDays:       settings.NewSpeciesWindowDays,
		
		// Multi-period tracking
		speciesThisYear:    make(map[string]time.Time, 100),
		speciesBySeason:    make(map[string]map[string]time.Time),
		currentYear:        now.Year(),
		seasons:            make(map[string]seasonDates),
		
		// Configuration
		ds:                 ds,
		syncIntervalMins:   settings.SyncIntervalMinutes,
		yearlyEnabled:      settings.YearlyTracking.Enabled,
		seasonalEnabled:    settings.SeasonalTracking.Enabled,
		yearlyWindowDays:   settings.YearlyTracking.WindowDays,
		seasonalWindowDays: settings.SeasonalTracking.WindowDays,
		resetMonth:         settings.YearlyTracking.ResetMonth,
		resetDay:           settings.YearlyTracking.ResetDay,
	}
	
	// Initialize seasons from configuration
	if settings.SeasonalTracking.Enabled && len(settings.SeasonalTracking.Seasons) > 0 {
		for name, season := range settings.SeasonalTracking.Seasons {
			tracker.seasons[name] = seasonDates{
				month: season.StartMonth,
				day:   season.StartDay,
			}
		}
	} else {
		tracker.initializeDefaultSeasons()
	}
	
	tracker.currentSeason = tracker.getCurrentSeason(now)
	
	return tracker
}

// initializeDefaultSeasons sets up the default Northern Hemisphere seasons
func (t *NewSpeciesTracker) initializeDefaultSeasons() {
	t.seasons["spring"] = seasonDates{month: 3, day: 20}  // March 20
	t.seasons["summer"] = seasonDates{month: 6, day: 21}  // June 21  
	t.seasons["fall"] = seasonDates{month: 9, day: 22}    // September 22
	t.seasons["winter"] = seasonDates{month: 12, day: 21} // December 21
}

// getCurrentSeason determines which season we're currently in
func (t *NewSpeciesTracker) getCurrentSeason(currentTime time.Time) string {
	currentMonth := int(currentTime.Month())
	
	// Find the most recent season start date
	var currentSeason string
	var latestDate time.Time
	
	for seasonName, seasonStart := range t.seasons {
		// Create a date for this year's season start
		seasonDate := time.Date(currentTime.Year(), time.Month(seasonStart.month), seasonStart.day, 0, 0, 0, 0, currentTime.Location())
		
		// Handle winter season that might start in previous year
		if seasonStart.month >= 12 && currentMonth < 6 {
			seasonDate = time.Date(currentTime.Year()-1, time.Month(seasonStart.month), seasonStart.day, 0, 0, 0, 0, currentTime.Location())
		}
		
		// If this season has started and is more recent than our current candidate
		if (currentTime.After(seasonDate) || currentTime.Equal(seasonDate)) && 
		   (currentSeason == "" || seasonDate.After(latestDate)) {
			currentSeason = seasonName
			latestDate = seasonDate
		}
	}
	
	// Default to winter if we couldn't determine the season
	if currentSeason == "" {
		currentSeason = "winter"
	}
	
	return currentSeason
}

// checkAndResetPeriods checks if we need to reset yearly or seasonal tracking
func (t *NewSpeciesTracker) checkAndResetPeriods(currentTime time.Time) {
	// Check for yearly reset
	if t.yearlyEnabled && t.shouldResetYear(currentTime) {
		t.speciesThisYear = make(map[string]time.Time)
		t.currentYear = currentTime.Year()
	}
	
	// Check for seasonal reset
	if t.seasonalEnabled {
		newSeason := t.getCurrentSeason(currentTime)
		if newSeason != t.currentSeason {
			t.currentSeason = newSeason
			// Initialize season map if it doesn't exist
			if t.speciesBySeason[newSeason] == nil {
				t.speciesBySeason[newSeason] = make(map[string]time.Time)
			}
		}
	}
}

// shouldResetYear determines if we should reset yearly tracking
func (t *NewSpeciesTracker) shouldResetYear(currentTime time.Time) bool {
	// Check if we've crossed the yearly reset date
	resetDate := time.Date(currentTime.Year(), time.Month(t.resetMonth), t.resetDay, 0, 0, 0, 0, currentTime.Location())
	
	// If current time is after reset date and we haven't reset for this year
	if currentTime.After(resetDate) && currentTime.Year() > t.currentYear {
		return true
	}
	
	// Handle case where reset date hasn't occurred yet this year
	if currentTime.Year() > t.currentYear {
		return true
	}
	
	return false
}

// InitFromDatabase populates the tracker from historical data
// This should be called once during initialization
func (t *NewSpeciesTracker) InitFromDatabase() error {
	if t.ds == nil {
		return errors.Newf("datastore is nil").
			Component("new-species-tracker").
			Category(errors.CategoryConfiguration).
			Build()
	}

	// Calculate date range for initial load
	// Load data from 2x window period to ensure we catch all relevant species
	endDate := time.Now().Format("2006-01-02")
	startDate := time.Now().AddDate(0, 0, -t.windowDays*2).Format("2006-01-02")

	// Use existing analytics method to get new species data
	newSpeciesData, err := t.ds.GetNewSpeciesDetections(startDate, endDate, 1000, 0)
	if err != nil {
		return errors.New(err).
			Component("new-species-tracker").
			Category(errors.CategoryDatabase).
			Context("operation", "init_from_database").
			Build()
	}

	t.mu.Lock()
	defer t.mu.Unlock()

	// Clear existing data
	for k := range t.speciesFirstSeen {
		delete(t.speciesFirstSeen, k)
	}

	// Populate map with first seen dates
	for _, species := range newSpeciesData {
		if species.FirstSeenDate != "" {
			firstSeen, err := time.Parse("2006-01-02", species.FirstSeenDate)
			if err == nil {
				t.speciesFirstSeen[species.ScientificName] = firstSeen
			}
		}
	}

	t.lastSyncTime = time.Now()
	return nil
}

// GetSpeciesStatus returns the tracking status for a species
// This method reuses a pre-allocated buffer to minimize allocations
func (t *NewSpeciesTracker) GetSpeciesStatus(scientificName string, currentTime time.Time) SpeciesStatus {
	t.mu.Lock()
	defer t.mu.Unlock()
	
	// Check and reset periods if needed
	t.checkAndResetPeriods(currentTime)
	
	// Lifetime tracking
	firstSeen, exists := t.speciesFirstSeen[scientificName]
	
	// Yearly tracking
	var firstThisYear *time.Time
	if t.yearlyEnabled {
		if yearTime, yearExists := t.speciesThisYear[scientificName]; yearExists {
			firstThisYear = &yearTime
		}
	}
	
	// Seasonal tracking
	var firstThisSeason *time.Time
	currentSeason := t.getCurrentSeason(currentTime)
	if t.seasonalEnabled && t.speciesBySeason[currentSeason] != nil {
		if seasonTime, seasonExists := t.speciesBySeason[currentSeason][scientificName]; seasonExists {
			firstThisSeason = &seasonTime
		}
	}
	
	// Reuse the pre-allocated status buffer
	status := &t.statusBuffer
	status.FirstSeenTime = firstSeen
	status.IsNew = false
	status.DaysSinceFirst = -1
	status.LastUpdatedTime = currentTime
	status.FirstThisYear = firstThisYear
	status.FirstThisSeason = firstThisSeason
	status.CurrentSeason = currentSeason
	status.IsNewThisYear = false
	status.IsNewThisSeason = false
	status.DaysThisYear = -1
	status.DaysThisSeason = -1
	
	// Calculate lifetime status
	if exists {
		daysSince := int(currentTime.Sub(firstSeen).Hours() / 24)
		status.DaysSinceFirst = daysSince
		status.IsNew = daysSince <= t.windowDays
	} else {
		// Species not seen before
		status.IsNew = true
		status.DaysSinceFirst = 0
	}
	
	// Calculate yearly status
	if t.yearlyEnabled {
		if firstThisYear != nil {
			daysThisYear := int(currentTime.Sub(*firstThisYear).Hours() / 24)
			status.DaysThisYear = daysThisYear
			status.IsNewThisYear = daysThisYear <= t.yearlyWindowDays
		} else {
			// First time this year
			status.IsNewThisYear = true
			status.DaysThisYear = 0
		}
	}
	
	// Calculate seasonal status
	if t.seasonalEnabled {
		if firstThisSeason != nil {
			daysThisSeason := int(currentTime.Sub(*firstThisSeason).Hours() / 24)
			status.DaysThisSeason = daysThisSeason
			status.IsNewThisSeason = daysThisSeason <= t.seasonalWindowDays
		} else {
			// First time this season
			status.IsNewThisSeason = true
			status.DaysThisSeason = 0
		}
	}
	
	return *status
}

// UpdateSpecies updates the first seen time for a species if necessary
// Returns true if this is a new species detection
func (t *NewSpeciesTracker) UpdateSpecies(scientificName string, detectionTime time.Time) bool {
	t.mu.Lock()
	defer t.mu.Unlock()

	// Check and reset periods if needed
	t.checkAndResetPeriods(detectionTime)

	// Lifetime tracking
	firstSeen, exists := t.speciesFirstSeen[scientificName]
	isNewSpecies := false
	
	if !exists {
		// New species detected
		t.speciesFirstSeen[scientificName] = detectionTime
		isNewSpecies = true
	} else if detectionTime.Before(firstSeen) {
		// Update if this detection is earlier than recorded
		t.speciesFirstSeen[scientificName] = detectionTime
	}

	// Update yearly tracking
	if t.yearlyEnabled {
		if _, yearExists := t.speciesThisYear[scientificName]; !yearExists {
			t.speciesThisYear[scientificName] = detectionTime
		}
	}

	// Update seasonal tracking
	if t.seasonalEnabled {
		currentSeason := t.getCurrentSeason(detectionTime)
		if t.speciesBySeason[currentSeason] == nil {
			t.speciesBySeason[currentSeason] = make(map[string]time.Time)
		}
		if _, seasonExists := t.speciesBySeason[currentSeason][scientificName]; !seasonExists {
			t.speciesBySeason[currentSeason][scientificName] = detectionTime
		}
	}

	return isNewSpecies
}

// IsNewSpecies checks if a species is considered "new" within the configured window
func (t *NewSpeciesTracker) IsNewSpecies(scientificName string) bool {
	t.mu.RLock()
	firstSeen, exists := t.speciesFirstSeen[scientificName]
	t.mu.RUnlock()

	if !exists {
		return true // Never seen before
	}

	daysSince := int(time.Since(firstSeen).Hours() / 24)
	return daysSince <= t.windowDays
}

// SyncIfNeeded checks if a database sync is needed and performs it
// This helps keep the tracker updated with any database changes
func (t *NewSpeciesTracker) SyncIfNeeded() error {
	if time.Since(t.lastSyncTime).Minutes() < float64(t.syncIntervalMins) {
		return nil // No sync needed yet
	}

	return t.InitFromDatabase()
}

// GetWindowDays returns the configured window for new species
func (t *NewSpeciesTracker) GetWindowDays() int {
	return t.windowDays
}

// GetSpeciesCount returns the number of tracked species
func (t *NewSpeciesTracker) GetSpeciesCount() int {
	t.mu.RLock()
	defer t.mu.RUnlock()
	return len(t.speciesFirstSeen)
}

// PruneOldEntries removes species entries older than 2x the window period
// This prevents unbounded memory growth over time
func (t *NewSpeciesTracker) PruneOldEntries() int {
	t.mu.Lock()
	defer t.mu.Unlock()

	cutoffTime := time.Now().AddDate(0, 0, -t.windowDays*2)
	pruned := 0

	for scientificName, firstSeen := range t.speciesFirstSeen {
		if firstSeen.Before(cutoffTime) {
			delete(t.speciesFirstSeen, scientificName)
			pruned++
		}
	}

	return pruned
}