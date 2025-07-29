// new_species_tracker.go
package processor

import (
	"log/slog"
	"sync"
	"time"

	"github.com/tphakala/birdnet-go/internal/conf"
	"github.com/tphakala/birdnet-go/internal/datastore"
	"github.com/tphakala/birdnet-go/internal/errors"
	"github.com/tphakala/birdnet-go/internal/logging"
)

// Package-level logger for species tracking
var (
	logger          *slog.Logger
	serviceLevelVar = new(slog.LevelVar) // Dynamic level control
	closeLogger     func() error
)

func init() {
	// Initialize species tracking logger
	// This creates a dedicated log file at logs/species-tracking.log
	var err error
	
	// Set initial level to Debug for comprehensive logging
	serviceLevelVar.Set(slog.LevelDebug)
	
	logger, closeLogger, err = logging.NewFileLogger(
		"logs/species-tracking.log",
		"species-tracking",
		serviceLevelVar,
	)
	
	if err != nil || logger == nil {
		// Fallback to default logger if file logger creation fails
		logger = slog.Default().With("service", "species-tracking")
		closeLogger = func() error { return nil }
		// Log the error so we know why the file logger failed
		if err != nil {
			logger.Error("Failed to initialize species tracking file logger", "error", err)
		}
	}
}

// SpeciesDatastore defines the minimal interface needed by NewSpeciesTracker
type SpeciesDatastore interface {
	GetNewSpeciesDetections(startDate, endDate string, limit, offset int) ([]datastore.NewSpeciesData, error)
	GetSpeciesFirstDetectionInPeriod(startDate, endDate string, limit, offset int) ([]datastore.NewSpeciesData, error)
}

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

// cachedSpeciesStatus represents a cached species status result with timestamp
type cachedSpeciesStatus struct {
	status    SpeciesStatus
	timestamp time.Time
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
	ds               SpeciesDatastore
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
	
	// Status result caching for performance optimization
	statusCache      map[string]cachedSpeciesStatus // scientificName -> cached status with TTL
	cacheTTL         time.Duration                  // Time-to-live for cached results
	lastCacheCleanup time.Time                      // Last time cache cleanup was performed
	
	// Season calculation caching for performance optimization
	cachedSeason       string        // Cached current season name
	seasonCacheTime    time.Time     // Timestamp when season was cached
	seasonCacheForTime time.Time     // The input time for which season was cached
	seasonCacheTTL     time.Duration // Time-to-live for season cache (1 hour)
}

// seasonDates represents the start date for a season
type seasonDates struct {
	month int
	day   int
}


// NewSpeciesTrackerFromSettings creates a tracker from configuration settings
func NewSpeciesTrackerFromSettings(ds SpeciesDatastore, settings *conf.SpeciesTrackingSettings) *NewSpeciesTracker {
	now := time.Now()
	
	// Log initialization
	logger.Debug("Creating new species tracker",
		"enabled", settings.Enabled,
		"window_days", settings.NewSpeciesWindowDays,
		"yearly_enabled", settings.YearlyTracking.Enabled,
		"seasonal_enabled", settings.SeasonalTracking.Enabled,
		"current_time", now.Format("2006-01-02 15:04:05"))
	
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
		
		// Status result caching
		statusCache:        make(map[string]cachedSpeciesStatus, 100), // Pre-allocate for ~100 species
		cacheTTL:           30 * time.Second,                          // 30-second TTL for cached results
		lastCacheCleanup:   now,
		
		// Season calculation caching
		seasonCacheTTL:     time.Hour, // 1-hour TTL for season cache
	}
	
	// Initialize seasons from configuration
	if settings.SeasonalTracking.Enabled && len(settings.SeasonalTracking.Seasons) > 0 {
		for name, season := range settings.SeasonalTracking.Seasons {
			tracker.seasons[name] = seasonDates{
				month: season.StartMonth,
				day:   season.StartDay,
			}
			logger.Debug("Configured season",
				"name", name,
				"start_month", season.StartMonth,
				"start_day", season.StartDay)
		}
	} else {
		tracker.initializeDefaultSeasons()
	}
	
	tracker.currentSeason = tracker.getCurrentSeason(now)
	
	logger.Debug("Species tracker initialized",
		"current_season", tracker.currentSeason,
		"current_year", tracker.currentYear,
		"total_seasons", len(tracker.seasons))
	
	return tracker
}

// initializeDefaultSeasons sets up the default Northern Hemisphere seasons
func (t *NewSpeciesTracker) initializeDefaultSeasons() {
	t.seasons["spring"] = seasonDates{month: 3, day: 20}  // March 20
	t.seasons["summer"] = seasonDates{month: 6, day: 21}  // June 21  
	t.seasons["fall"] = seasonDates{month: 9, day: 22}    // September 22
	t.seasons["winter"] = seasonDates{month: 12, day: 21} // December 21
}

// SetCurrentYearForTesting sets the current year for testing purposes only
// This method provides controlled access to the currentYear field for test scenarios
func (t *NewSpeciesTracker) SetCurrentYearForTesting(year int) {
	t.mu.Lock()
	defer t.mu.Unlock()
	t.currentYear = year
}

// getCurrentSeason determines which season we're currently in with intelligent caching
func (t *NewSpeciesTracker) getCurrentSeason(currentTime time.Time) string {
	// Check cache first - if valid entry exists and the input time is reasonably close to cached time
	if t.cachedSeason != "" && 
	   t.isSameSeasonPeriod(currentTime, t.seasonCacheForTime) &&
	   time.Since(t.seasonCacheTime) < t.seasonCacheTTL {
		// Cache hit - return cached season directly
		return t.cachedSeason
	}
	
	// Cache miss or expired - compute fresh season
	season := t.computeCurrentSeason(currentTime)
	
	// Cache the computed result for future requests
	t.cachedSeason = season
	t.seasonCacheTime = time.Now() // Cache time is when we computed it
	t.seasonCacheForTime = currentTime // Input time for which we computed
	
	return season
}

// isSameSeasonPeriod checks if two times are likely in the same season period
// This helps avoid cache misses for times that are very close together
func (t *NewSpeciesTracker) isSameSeasonPeriod(time1, time2 time.Time) bool {
	// If times are in different years, they could be in different seasons
	if time1.Year() != time2.Year() {
		return false
	}
	
	// If times are within the same day, they're definitely in the same season
	if time1.YearDay() == time2.YearDay() {
		return true
	}
	
	// If times are within 7 days of each other, they're very likely in the same season
	// (seasons typically last ~90 days, so 7 days is a safe buffer)
	timeDiff := time1.Sub(time2)
	if timeDiff < 0 {
		timeDiff = -timeDiff
	}
	return timeDiff < 7*24*time.Hour
}

// computeCurrentSeason performs the actual season calculation (moved from getCurrentSeason)
func (t *NewSpeciesTracker) computeCurrentSeason(currentTime time.Time) string {
	currentMonth := int(currentTime.Month())
	
	// Log season calculation
	logger.Debug("Computing current season",
		"input_time", currentTime.Format("2006-01-02 15:04:05"),
		"current_month", currentMonth,
		"current_year", currentTime.Year())
	
	// Find the most recent season start date
	var currentSeason string
	var latestDate time.Time
	
	for seasonName, seasonStart := range t.seasons {
		// Create a date for this year's season start
		seasonDate := time.Date(currentTime.Year(), time.Month(seasonStart.month), seasonStart.day, 0, 0, 0, 0, currentTime.Location())
		
		// Handle winter season that might start in previous year
		if seasonStart.month >= 12 && currentMonth < 6 {
			seasonDate = time.Date(currentTime.Year()-1, time.Month(seasonStart.month), seasonStart.day, 0, 0, 0, 0, currentTime.Location())
			logger.Debug("Adjusting winter season to previous year",
				"season", seasonName,
				"adjusted_date", seasonDate.Format("2006-01-02"))
		}
		
		// If this season has started and is more recent than our current candidate
		if (currentTime.After(seasonDate) || currentTime.Equal(seasonDate)) && 
		   (currentSeason == "" || seasonDate.After(latestDate)) {
			logger.Debug("Season candidate found",
				"season", seasonName,
				"start_date", seasonDate.Format("2006-01-02"),
				"is_after_current", currentTime.After(seasonDate) || currentTime.Equal(seasonDate))
			currentSeason = seasonName
			latestDate = seasonDate
		}
	}
	
	// Default to winter if we couldn't determine the season
	if currentSeason == "" {
		currentSeason = "winter"
		logger.Debug("Defaulting to winter season - no match found")
	}
	
	logger.Debug("Computed season result",
		"season", currentSeason,
		"season_start_date", latestDate.Format("2006-01-02"))
	
	return currentSeason
}

// checkAndResetPeriods checks if we need to reset yearly or seasonal tracking
func (t *NewSpeciesTracker) checkAndResetPeriods(currentTime time.Time) {
	// Check for yearly reset
	if t.yearlyEnabled && t.shouldResetYear(currentTime) {
		oldYear := t.currentYear
		t.speciesThisYear = make(map[string]time.Time)
		t.currentYear = currentTime.Year()
		// Clear status cache when year resets to ensure fresh calculations
		t.statusCache = make(map[string]cachedSpeciesStatus)
		logger.Debug("Reset yearly tracking",
			"old_year", oldYear,
			"new_year", t.currentYear,
			"check_time", currentTime.Format("2006-01-02 15:04:05"))
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

	now := time.Now()
	
	logger.Debug("Initializing species tracker from database",
		"current_time", now.Format("2006-01-02 15:04:05"),
		"yearly_enabled", t.yearlyEnabled,
		"seasonal_enabled", t.seasonalEnabled)
	
	t.mu.Lock()
	defer t.mu.Unlock()

	// Step 1: Load lifetime tracking data (existing logic)
	if err := t.loadLifetimeDataFromDatabase(now); err != nil {
		return err
	}

	// Step 2: Load yearly tracking data if enabled
	if t.yearlyEnabled {
		if err := t.loadYearlyDataFromDatabase(now); err != nil {
			return err
		}
	}

	// Step 3: Load seasonal tracking data if enabled
	if t.seasonalEnabled {
		if err := t.loadSeasonalDataFromDatabase(now); err != nil {
			return err
		}
	}

	t.lastSyncTime = now
	
	logger.Debug("Database initialization complete",
		"lifetime_species", len(t.speciesFirstSeen),
		"yearly_species", len(t.speciesThisYear),
		"total_seasons", len(t.speciesBySeason))
	
	return nil
}

// loadLifetimeDataFromDatabase loads all-time first detection data
func (t *NewSpeciesTracker) loadLifetimeDataFromDatabase(now time.Time) error {
	endDate := now.Format("2006-01-02")
	startDate := "1900-01-01" // Load from beginning of time to get all historical data

	newSpeciesData, err := t.ds.GetNewSpeciesDetections(startDate, endDate, 10000, 0)
	if err != nil {
		return errors.New(err).
			Component("new-species-tracker").
			Category(errors.CategoryDatabase).
			Context("operation", "load_lifetime_data").
			Build()
	}

	// Clear and populate lifetime tracking map
	t.speciesFirstSeen = make(map[string]time.Time, len(newSpeciesData))
	for _, species := range newSpeciesData {
		if species.FirstSeenDate != "" {
			firstSeen, err := time.Parse("2006-01-02", species.FirstSeenDate)
			if err == nil {
				t.speciesFirstSeen[species.ScientificName] = firstSeen
			}
		}
	}

	return nil
}

// loadYearlyDataFromDatabase loads first detection data for the current year
func (t *NewSpeciesTracker) loadYearlyDataFromDatabase(now time.Time) error {
	startDate, endDate := t.getYearDateRange(now)
	
	// Use GetSpeciesFirstDetectionInPeriod for yearly tracking
	yearlyData, err := t.ds.GetSpeciesFirstDetectionInPeriod(startDate, endDate, 10000, 0)
	if err != nil {
		return errors.New(err).
			Component("new-species-tracker").
			Category(errors.CategoryDatabase).
			Context("operation", "load_yearly_data").
			Context("year_start", startDate).
			Context("year_end", endDate).
			Build()
	}

	// Clear and populate yearly tracking map
	t.speciesThisYear = make(map[string]time.Time, len(yearlyData))
	for _, species := range yearlyData {
		if species.FirstSeenDate != "" {
			firstSeen, err := time.Parse("2006-01-02", species.FirstSeenDate)
			if err == nil {
				t.speciesThisYear[species.ScientificName] = firstSeen
			}
		}
	}

	return nil
}

// loadSeasonalDataFromDatabase loads first detection data for each season in the current year
func (t *NewSpeciesTracker) loadSeasonalDataFromDatabase(now time.Time) error {
	// Initialize seasonal maps
	t.speciesBySeason = make(map[string]map[string]time.Time)
	
	logger.Debug("Loading seasonal data from database",
		"total_seasons", len(t.seasons))
	
	for seasonName := range t.seasons {
		startDate, endDate := t.getSeasonDateRange(seasonName, now)
		
		logger.Debug("Loading data for season",
			"season", seasonName,
			"start_date", startDate,
			"end_date", endDate)
		
		// Get first detection of each species within this season period
		seasonalData, err := t.ds.GetSpeciesFirstDetectionInPeriod(startDate, endDate, 10000, 0)
		if err != nil {
			return errors.New(err).
				Component("new-species-tracker").
				Category(errors.CategoryDatabase).
				Context("operation", "load_seasonal_data").
				Context("season", seasonName).
				Context("season_start", startDate).
				Context("season_end", endDate).
				Build()
		}

		logger.Debug("Retrieved seasonal detections",
			"season", seasonName,
			"total_records", len(seasonalData))

		// Initialize season map
		seasonMap := make(map[string]time.Time, len(seasonalData))
		for _, species := range seasonalData {
			if species.FirstSeenDate != "" {
				firstSeen, err := time.Parse("2006-01-02", species.FirstSeenDate)
				if err == nil {
					seasonMap[species.ScientificName] = firstSeen
					logger.Debug("Added species to season",
						"season", seasonName,
						"species", species.ScientificName,
						"first_seen", firstSeen.Format("2006-01-02"))
				}
			}
		}
		t.speciesBySeason[seasonName] = seasonMap
		
		logger.Debug("Season loading complete",
			"season", seasonName,
			"total_retrieved", len(seasonalData),
			"species_loaded", len(seasonMap))
	}

	return nil
}

// getYearDateRange calculates the start and end dates for yearly tracking
func (t *NewSpeciesTracker) getYearDateRange(now time.Time) (startDate, endDate string) {
	currentYear := now.Year()
	
	// Calculate year start based on reset settings
	yearStart := time.Date(currentYear, time.Month(t.resetMonth), t.resetDay, 0, 0, 0, 0, now.Location())
	
	// If we haven't reached the reset date this year, use last year's reset date
	if now.Before(yearStart) {
		yearStart = time.Date(currentYear-1, time.Month(t.resetMonth), t.resetDay, 0, 0, 0, 0, now.Location())
	}
	
	startDate = yearStart.Format("2006-01-02")
	endDate = now.Format("2006-01-02")
	
	return startDate, endDate
}

// getSeasonDateRange calculates the start and end dates for a specific season
func (t *NewSpeciesTracker) getSeasonDateRange(seasonName string, now time.Time) (startDate, endDate string) {
	season, exists := t.seasons[seasonName]
	if !exists {
		// Return empty range for unknown season
		return now.Format("2006-01-02"), now.Format("2006-01-02")
	}
	
	currentYear := now.Year()
	
	// Calculate season start date for this year
	seasonStart := time.Date(currentYear, time.Month(season.month), season.day, 0, 0, 0, 0, now.Location())
	
	// Handle winter season that might start in previous year
	if season.month >= 12 && now.Month() < time.Month(season.month) {
		seasonStart = time.Date(currentYear-1, time.Month(season.month), season.day, 0, 0, 0, 0, now.Location())
	}
	
	// If the season hasn't started yet this year, don't return any data
	if now.Before(seasonStart) {
		return now.Format("2006-01-02"), now.Format("2006-01-02") // Empty range
	}
	
	startDate = seasonStart.Format("2006-01-02")
	endDate = now.Format("2006-01-02")
	
	return startDate, endDate
}

// isWithinCurrentYear checks if a detection time falls within the current tracking year
func (t *NewSpeciesTracker) isWithinCurrentYear(detectionTime time.Time) bool {
	// Use the tracker's current year (respects SetCurrentYearForTesting)
	referenceTime := time.Date(t.currentYear, time.December, 31, 23, 59, 59, 0, time.UTC)
	startDate, _ := t.getYearDateRange(referenceTime)
	yearStart, err := time.Parse("2006-01-02", startDate)
	if err != nil {
		return false
	}
	
	// Check if the detection falls within this year's tracking period
	return detectionTime.After(yearStart) || detectionTime.Equal(yearStart)
}

// GetSpeciesStatus returns the tracking status for a species with caching for performance
// This method implements cache-first lookup with TTL validation to minimize expensive computations
func (t *NewSpeciesTracker) GetSpeciesStatus(scientificName string, currentTime time.Time) SpeciesStatus {
	t.mu.Lock()
	defer t.mu.Unlock()
	
	// Check cache first - if valid entry exists within TTL, return it
	if cached, exists := t.statusCache[scientificName]; exists {
		if currentTime.Sub(cached.timestamp) < t.cacheTTL {
			// Cache hit - return cached result directly
			return cached.status
		}
		// Cache expired - will recompute and update cache below
	}
	
	// Perform periodic cache cleanup to prevent unbounded growth
	if currentTime.Sub(t.lastCacheCleanup) > t.cacheTTL*10 { // Cleanup every 10 TTL periods (5 minutes)
		t.cleanupExpiredCache(currentTime)
		t.lastCacheCleanup = currentTime
	}
	
	// Cache miss or expired - compute fresh status
	t.checkAndResetPeriods(currentTime)
	currentSeason := t.getCurrentSeason(currentTime)
	
	// Build fresh status using the same logic as buildSpeciesStatusLocked but with buffer reuse
	status := t.buildSpeciesStatusWithBuffer(scientificName, currentTime, currentSeason)
	
	firstSeenStr := "never"
	if !status.FirstSeenTime.IsZero() {
		firstSeenStr = status.FirstSeenTime.Format("2006-01-02")
	}
	
	firstThisYearStr := "nil"
	if status.FirstThisYear != nil {
		firstThisYearStr = status.FirstThisYear.Format("2006-01-02")
	}
	
	firstThisSeasonStr := "nil"
	if status.FirstThisSeason != nil {
		firstThisSeasonStr = status.FirstThisSeason.Format("2006-01-02")
	}
	
	logger.Debug("Species status computed",
		"species", scientificName,
		"current_time", currentTime.Format("2006-01-02 15:04:05"),
		"current_season", currentSeason,
		"is_new", status.IsNew,
		"is_new_this_year", status.IsNewThisYear,
		"is_new_this_season", status.IsNewThisSeason,
		"days_since_first", status.DaysSinceFirst,
		"days_this_year", status.DaysThisYear,
		"days_this_season", status.DaysThisSeason,
		"first_seen", firstSeenStr,
		"first_this_year", firstThisYearStr,
		"first_this_season", firstThisSeasonStr)
	
	// Cache the computed result for future requests
	t.statusCache[scientificName] = cachedSpeciesStatus{
		status:    status,
		timestamp: currentTime,
	}
	
	return status
}

// buildSpeciesStatusWithBuffer builds species status reusing the pre-allocated buffer
// This method is used by GetSpeciesStatus to maintain the buffer optimization
func (t *NewSpeciesTracker) buildSpeciesStatusWithBuffer(scientificName string, currentTime time.Time, currentSeason string) SpeciesStatus {
	// Lifetime tracking
	firstSeen, exists := t.speciesFirstSeen[scientificName]
	
	// Yearly tracking
	var firstThisYear *time.Time
	if t.yearlyEnabled {
		if yearTime, yearExists := t.speciesThisYear[scientificName]; yearExists {
			// Create a copy to avoid pointer to loop variable issue
			timeCopy := yearTime
			firstThisYear = &timeCopy
		}
	}
	
	// Seasonal tracking - check current season only (matches original behavior)
	var firstThisSeason *time.Time
	if t.seasonalEnabled && t.speciesBySeason[currentSeason] != nil {
		if seasonTime, seasonExists := t.speciesBySeason[currentSeason][scientificName]; seasonExists {
			// Create a copy to avoid pointer to loop variable issue
			timeCopy := seasonTime
			firstThisSeason = &timeCopy
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

// cleanupExpiredCache removes expired entries from the status cache to prevent memory leaks
func (t *NewSpeciesTracker) cleanupExpiredCache(currentTime time.Time) {
	for scientificName := range t.statusCache {
		if currentTime.Sub(t.statusCache[scientificName].timestamp) >= t.cacheTTL {
			delete(t.statusCache, scientificName)
		}
	}
}

// GetBatchSpeciesStatus returns the tracking status for multiple species in a single operation
// This method significantly reduces mutex contention and redundant computations compared to 
// calling GetSpeciesStatus individually for each species. It performs expensive operations
// like checkAndResetPeriods() and getCurrentSeason() only once for the entire batch.
func (t *NewSpeciesTracker) GetBatchSpeciesStatus(scientificNames []string, currentTime time.Time) map[string]SpeciesStatus {
	if len(scientificNames) == 0 {
		return make(map[string]SpeciesStatus)
	}
	
	t.mu.Lock()
	defer t.mu.Unlock()
	
	// Perform expensive operations only once for the entire batch
	t.checkAndResetPeriods(currentTime)
	currentSeason := t.getCurrentSeason(currentTime)
	
	// Pre-allocate result map with exact capacity
	results := make(map[string]SpeciesStatus, len(scientificNames))
	
	// Process each species using the cached season information
	for _, scientificName := range scientificNames {
		status := t.buildSpeciesStatusLocked(scientificName, currentTime, currentSeason)
		results[scientificName] = status
	}
	
	return results
}

// buildSpeciesStatusLocked builds a species status without acquiring locks or performing
// expensive period checks. This is used internally by GetBatchSpeciesStatus.
// Assumes the caller already holds the mutex lock.
func (t *NewSpeciesTracker) buildSpeciesStatusLocked(scientificName string, currentTime time.Time, currentSeason string) SpeciesStatus {
	// Lifetime tracking
	firstSeen, exists := t.speciesFirstSeen[scientificName]
	
	// Yearly tracking
	var firstThisYear *time.Time
	if t.yearlyEnabled {
		if yearTime, yearExists := t.speciesThisYear[scientificName]; yearExists {
			// Create a copy to avoid pointer to loop variable issue
			timeCopy := yearTime
			firstThisYear = &timeCopy
		}
	}
	
	// Seasonal tracking - check current season only (matches original behavior)
	var firstThisSeason *time.Time
	if t.seasonalEnabled && t.speciesBySeason[currentSeason] != nil {
		if seasonTime, seasonExists := t.speciesBySeason[currentSeason][scientificName]; seasonExists {
			// Create a copy to avoid pointer to loop variable issue
			timeCopy := seasonTime
			firstThisSeason = &timeCopy
		}
	}
	
	// Build status struct (cannot reuse statusBuffer in batch operations)
	status := SpeciesStatus{
		FirstSeenTime:    firstSeen,
		IsNew:            false,
		DaysSinceFirst:   -1,
		LastUpdatedTime:  currentTime,
		FirstThisYear:    firstThisYear,
		FirstThisSeason:  firstThisSeason,
		CurrentSeason:    currentSeason,
		IsNewThisYear:    false,
		IsNewThisSeason:  false,
		DaysThisYear:     -1,
		DaysThisSeason:   -1,
	}
	
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
	
	return status
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
		logger.Debug("New lifetime species detected",
			"species", scientificName,
			"detection_time", detectionTime.Format("2006-01-02 15:04:05"))
	} else if detectionTime.Before(firstSeen) {
		// Update if this detection is earlier than recorded
		t.speciesFirstSeen[scientificName] = detectionTime
		logger.Debug("Updated lifetime first seen to earlier date",
			"species", scientificName,
			"old_date", firstSeen.Format("2006-01-02"),
			"new_date", detectionTime.Format("2006-01-02"))
	}

	// Update yearly tracking
	if t.yearlyEnabled {
		if t.isWithinCurrentYear(detectionTime) {
			if _, yearExists := t.speciesThisYear[scientificName]; !yearExists {
				t.speciesThisYear[scientificName] = detectionTime
				logger.Debug("New species for this year",
					"species", scientificName,
					"detection_time", detectionTime.Format("2006-01-02 15:04:05"),
					"current_year", t.currentYear)
			} else {
				// Update if this detection is earlier than the recorded one
				existingTime := t.speciesThisYear[scientificName]
				if detectionTime.Before(existingTime) {
					t.speciesThisYear[scientificName] = detectionTime
					logger.Debug("Updated yearly first seen to earlier date",
						"species", scientificName,
						"old_date", existingTime.Format("2006-01-02"),
						"new_date", detectionTime.Format("2006-01-02"),
						"current_year", t.currentYear)
				}
			}
		} else {
			logger.Debug("Detection not within current year - skipping yearly update",
				"species", scientificName,
				"detection_time", detectionTime.Format("2006-01-02"),
				"current_year", t.currentYear)
		}
	}

	// Update seasonal tracking
	if t.seasonalEnabled {
		currentSeason := t.getCurrentSeason(detectionTime)
		if t.speciesBySeason[currentSeason] == nil {
			t.speciesBySeason[currentSeason] = make(map[string]time.Time)
			logger.Debug("Initialized new season map",
				"season", currentSeason)
		}
		if _, seasonExists := t.speciesBySeason[currentSeason][scientificName]; !seasonExists {
			t.speciesBySeason[currentSeason][scientificName] = detectionTime
			logger.Debug("New species for this season",
				"species", scientificName,
				"season", currentSeason,
				"detection_time", detectionTime.Format("2006-01-02 15:04:05"))
		}
	}

	// Invalidate cache entry for this species to ensure fresh status calculations
	delete(t.statusCache, scientificName)

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
// This prevents unbounded memory growth over time across all tracking maps
func (t *NewSpeciesTracker) PruneOldEntries() int {
	t.mu.Lock()
	defer t.mu.Unlock()

	cutoffTime := time.Now().AddDate(0, 0, -t.windowDays*2)
	pruned := 0

	// Prune lifetime tracking map
	for scientificName, firstSeen := range t.speciesFirstSeen {
		if firstSeen.Before(cutoffTime) {
			delete(t.speciesFirstSeen, scientificName)
			pruned++
		}
	}

	// Prune yearly tracking map if enabled
	if t.yearlyEnabled {
		for scientificName, firstSeen := range t.speciesThisYear {
			if firstSeen.Before(cutoffTime) {
				delete(t.speciesThisYear, scientificName)
				pruned++
			}
		}
	}

	// Prune seasonal tracking maps if enabled
	if t.seasonalEnabled {
		for season, speciesMap := range t.speciesBySeason {
			for scientificName, firstSeen := range speciesMap {
				if firstSeen.Before(cutoffTime) {
					delete(speciesMap, scientificName)
					pruned++
				}
			}
			// Remove empty seasonal maps to prevent memory leaks
			if len(speciesMap) == 0 {
				delete(t.speciesBySeason, season)
			}
		}
	}

	return pruned
}

// CheckAndUpdateSpecies atomically checks if a species is new and updates the tracker
// This prevents race conditions where multiple concurrent detections of the same species
// could all be considered "new" before any of them update the tracker.
// Returns (isNew, daysSinceFirstSeen)
func (t *NewSpeciesTracker) CheckAndUpdateSpecies(scientificName string, detectionTime time.Time) (isNew bool, daysSinceFirstSeen int) {
	t.mu.Lock()
	defer t.mu.Unlock()

	// Check and reset periods if needed
	t.checkAndResetPeriods(detectionTime)

	// Check current status before any updates
	firstSeen, exists := t.speciesFirstSeen[scientificName]

	if !exists {
		// Species not seen before - definitely new
		isNew = true
		daysSinceFirstSeen = 0
		// Record this as the first detection
		t.speciesFirstSeen[scientificName] = detectionTime
	} else {
		// Calculate days since first seen
		daysSince := int(detectionTime.Sub(firstSeen).Hours() / 24)
		daysSinceFirstSeen = daysSince
		isNew = daysSince <= t.windowDays
		
		// Update if this detection is earlier than recorded
		if detectionTime.Before(firstSeen) {
			t.speciesFirstSeen[scientificName] = detectionTime
		}
	}

	// Update yearly tracking
	if t.yearlyEnabled {
		if t.isWithinCurrentYear(detectionTime) {
			if _, yearExists := t.speciesThisYear[scientificName]; !yearExists {
				t.speciesThisYear[scientificName] = detectionTime
			}
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

	return
}