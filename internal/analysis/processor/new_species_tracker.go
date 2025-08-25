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

// Constants for better maintainability and avoiding magic numbers
const (
	// Cache configuration
	defaultCacheTTL        = 30 * time.Second // Status cache TTL
	defaultSeasonCacheTTL  = time.Hour        // Season cache TTL
	defaultCacheExpiredAge = -time.Hour       // Age for marking cache as expired

	// Capacity hints for map allocations
	initialSpeciesCapacity = 100 // Initial capacity for species maps

	// Time calculations
	hoursPerDay               = 24
	seasonBufferDays          = 7 // Days buffer for season comparison
	seasonBufferDuration      = seasonBufferDays * hoursPerDay * time.Hour
	defaultSeasonDurationDays = 90 // Typical season duration

	// Season calculations
	winterAdjustmentCutoffMonth time.Month = time.June // June - first month where winter shouldn't adjust to previous year

	// Notification suppression
	defaultNotificationSuppressionWindow = 168 * time.Hour // Default suppression window (7 days)
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

// Close releases the file logger resources to prevent resource leaks.
// This should be called during application shutdown or when the logger is no longer needed.
func Close() error {
	if closeLogger != nil {
		return closeLogger()
	}
	return nil
}

// SpeciesDatastore defines the minimal interface needed by NewSpeciesTracker
type SpeciesDatastore interface {
	GetNewSpeciesDetections(startDate, endDate string, limit, offset int) ([]datastore.NewSpeciesData, error)
	GetSpeciesFirstDetectionInPeriod(startDate, endDate string, limit, offset int) ([]datastore.NewSpeciesData, error)
}

// SpeciesStatus represents the tracking status of a species across multiple periods
type SpeciesStatus struct {
	// Existing lifetime tracking
	FirstSeenTime   time.Time
	IsNew           bool
	DaysSinceFirst  int
	LastUpdatedTime time.Time // For cache management

	// Multi-period tracking
	FirstThisYear   *time.Time // First detection this calendar year
	FirstThisSeason *time.Time // First detection this season
	CurrentSeason   string     // Current season name

	// Status flags for each period
	IsNewThisYear   bool // First time this year
	IsNewThisSeason bool // First time this season
	DaysThisYear    int  // Days since first this year
	DaysThisSeason  int  // Days since first this season
}

// cachedSpeciesStatus represents a cached species status result with timestamp
type cachedSpeciesStatus struct {
	status    SpeciesStatus
	timestamp time.Time
}

// NewSpeciesTracker tracks species detections and identifies new species
// within a configurable time window. Designed for minimal memory allocations.
type NewSpeciesTracker struct {
	mu sync.RWMutex

	// Lifetime tracking (existing)
	speciesFirstSeen map[string]time.Time // scientificName -> first detection time
	windowDays       int                  // Days to consider a species "new"

	// Multi-period tracking
	speciesThisYear map[string]time.Time            // scientificName -> first detection this year
	speciesBySeason map[string]map[string]time.Time // season -> scientificName -> first detection time
	currentYear     int
	currentSeason   string
	seasons         map[string]seasonDates // season name -> start dates

	// Configuration
	ds                 SpeciesDatastore
	lastSyncTime       time.Time
	syncIntervalMins   int
	yearlyEnabled      bool
	seasonalEnabled    bool
	yearlyWindowDays   int
	seasonalWindowDays int
	resetMonth         int // Month to reset yearly tracking (1-12)
	resetDay           int // Day to reset yearly tracking (1-31)

	// Pre-allocated for efficiency
	statusBuffer SpeciesStatus // Reusable buffer for status calculations

	// Status result caching for performance optimization
	statusCache      map[string]cachedSpeciesStatus // scientificName -> cached status with TTL
	cacheTTL         time.Duration                  // Time-to-live for cached results
	lastCacheCleanup time.Time                      // Last time cache cleanup was performed

	// Season calculation caching for performance optimization
	cachedSeason       string        // Cached current season name
	seasonCacheTime    time.Time     // Timestamp when season was cached
	seasonCacheForTime time.Time     // The input time for which season was cached
	seasonCacheTTL     time.Duration // Time-to-live for season cache (1 hour)

	// Notification suppression tracking to prevent duplicate notifications
	// Simply maps scientific name -> last notification time
	notificationLastSent          map[string]time.Time
	notificationSuppressionWindow time.Duration // Duration to suppress duplicate notifications (default: 7 days)
}

// seasonDates represents the start date for a season
type seasonDates struct {
	month int
	day   int
}

// NewSpeciesTrackerFromSettings creates a tracker from configuration settings
// Note: All time calculations use the system's local timezone via time.Now()
func NewSpeciesTrackerFromSettings(ds SpeciesDatastore, settings *conf.SpeciesTrackingSettings) *NewSpeciesTracker {
	now := time.Now() // Uses system local timezone

	// Log initialization
	logger.Debug("Creating new species tracker",
		"enabled", settings.Enabled,
		"window_days", settings.NewSpeciesWindowDays,
		"yearly_enabled", settings.YearlyTracking.Enabled,
		"seasonal_enabled", settings.SeasonalTracking.Enabled,
		"current_time", now.Format("2006-01-02 15:04:05"))

	tracker := &NewSpeciesTracker{
		// Lifetime tracking
		speciesFirstSeen: make(map[string]time.Time, initialSpeciesCapacity),
		windowDays:       settings.NewSpeciesWindowDays,

		// Multi-period tracking
		speciesThisYear: make(map[string]time.Time, initialSpeciesCapacity),
		speciesBySeason: make(map[string]map[string]time.Time),
		currentYear:     now.Year(),
		seasons:         make(map[string]seasonDates),

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
		statusCache:      make(map[string]cachedSpeciesStatus, initialSpeciesCapacity), // Pre-allocate for species
		cacheTTL:         defaultCacheTTL,                                              // TTL for cached results
		lastCacheCleanup: now,

		// Season calculation caching
		seasonCacheTTL: defaultSeasonCacheTTL, // TTL for season cache

		// Notification suppression tracking
		notificationLastSent: make(map[string]time.Time, initialSpeciesCapacity),
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

	// Set notification suppression window from configuration
	// 0 is valid (disabled), negative values get default
	if settings.NotificationSuppressionHours < 0 {
		tracker.notificationSuppressionWindow = defaultNotificationSuppressionWindow
	} else {
		tracker.notificationSuppressionWindow = time.Duration(settings.NotificationSuppressionHours) * time.Hour
	}

	return tracker
}

// initializeDefaultSeasons sets up the default Northern Hemisphere seasons
func (t *NewSpeciesTracker) initializeDefaultSeasons() {
	t.seasons["spring"] = seasonDates{month: 3, day: 20}  // March 20
	t.seasons["summer"] = seasonDates{month: 6, day: 21}  // June 21
	t.seasons["fall"] = seasonDates{month: 9, day: 22}    // September 22
	t.seasons["winter"] = seasonDates{month: 12, day: 21} // December 21
}

// shouldAdjustWinter adjusts the season start year only when the season month is December
// and the current month is before winterAdjustmentCutoffMonth (Jan-May)
func (t *NewSpeciesTracker) shouldAdjustWinter(now time.Time, seasonMonth time.Month) bool {
	return seasonMonth == time.December && now.Month() < winterAdjustmentCutoffMonth
}

// SetCurrentYearForTesting sets the current year for testing purposes only.
//
// ⚠️  WARNING: THIS METHOD IS STRICTLY FOR TESTING AND SHOULD NEVER BE USED IN PRODUCTION CODE ⚠️
//
// This method bypasses the normal year tracking logic and directly manipulates the internal
// currentYear field, which can lead to:
// - Inconsistent tracking data between lifetime, yearly, and seasonal periods
// - Cache invalidation issues that may cause incorrect species status calculations
// - Data corruption if the year doesn't match the actual system time
// - Broken yearly reset logic that relies on time-based transitions
//
// Using this method in production code will result in unpredictable behavior and should be
// avoided at all costs. It exists solely to enable controlled testing scenarios where
// specific year boundaries need to be simulated.
//
// This method provides controlled access to the currentYear field for test scenarios only.
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
	t.seasonCacheTime = time.Now()     // Cache time is when we computed it
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

	// If times are within seasonBufferDays of each other, they're very likely in the same season
	// (seasons typically last ~defaultSeasonDurationDays days, so seasonBufferDays is a safe buffer)
	timeDiff := time1.Sub(time2)
	if timeDiff < 0 {
		timeDiff = -timeDiff
	}
	return timeDiff <= seasonBufferDuration
}

// computeCurrentSeason performs the actual season calculation (moved from getCurrentSeason)
func (t *NewSpeciesTracker) computeCurrentSeason(currentTime time.Time) string {
	currentMonth := int(currentTime.Month())
	currentDay := currentTime.Day()

	// Log season calculation
	logger.Debug("Computing current season",
		"input_time", currentTime.Format("2006-01-02 15:04:05"),
		"current_month", currentMonth,
		"current_day", currentDay,
		"current_year", currentTime.Year())

	// Check seasons in a deterministic order to handle boundaries correctly
	// Order: winter, spring, summer, fall (in chronological order within a year)
	seasonOrder := []string{"winter", "spring", "summer", "fall"}
	
	// Find the most recent season start date
	var currentSeason string
	var latestDate time.Time

	for _, seasonName := range seasonOrder {
		seasonStart, exists := t.seasons[seasonName]
		if !exists {
			continue
		}

		// Create a date for this year's season start
		seasonDate := time.Date(currentTime.Year(), time.Month(seasonStart.month), seasonStart.day, 0, 0, 0, 0, currentTime.Location())

		// Handle winter season that might start in previous year
		if t.shouldAdjustWinter(currentTime, time.Month(seasonStart.month)) {
			seasonDate = time.Date(currentTime.Year()-1, time.Month(seasonStart.month), seasonStart.day, 0, 0, 0, 0, currentTime.Location())
			logger.Debug("Adjusting winter season to previous year",
				"season", seasonName,
				"adjusted_date", seasonDate.Format("2006-01-02"))
		}

		// Check if current date is on or after this season's start
		if currentTime.Equal(seasonDate) || currentTime.After(seasonDate) {
			// Update if this is a more recent season start than what we have
			if currentSeason == "" || seasonDate.After(latestDate) {
				logger.Debug("Season match found",
					"season", seasonName,
					"start_date", seasonDate.Format("2006-01-02"),
					"is_current", currentTime.Equal(seasonDate) || currentTime.After(seasonDate),
					"replaces", currentSeason,
					"previous_date", latestDate.Format("2006-01-02"))
				currentSeason = seasonName
				latestDate = seasonDate
			}
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
	// If we've never reset before (currentYear is 0), we need to reset
	if t.currentYear == 0 {
		return true
	}

	// If we're in a later year than our tracked year, we need to reset
	if currentTime.Year() > t.currentYear {
		return true
	}

	// If we're in the same year as our tracked year, no reset needed
	if currentTime.Year() == t.currentYear {
		return false
	}

	// If we're somehow in an earlier year than our tracked year (shouldn't happen), no reset
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
		return errors.New(err).
			Component("new-species-tracker").
			Category(errors.CategoryDatabase).
			Context("operation", "load_lifetime_data").
			Context("sync_time", now.Format("2006-01-02 15:04:05")).
			Build()
	}

	// Step 2: Load yearly tracking data if enabled
	if t.yearlyEnabled {
		if err := t.loadYearlyDataFromDatabase(now); err != nil {
			return errors.New(err).
				Component("new-species-tracker").
				Category(errors.CategoryDatabase).
				Context("operation", "load_yearly_data").
				Context("current_year", t.currentYear).
				Build()
		}
	}

	// Step 3: Load seasonal tracking data if enabled
	if t.seasonalEnabled {
		if err := t.loadSeasonalDataFromDatabase(now); err != nil {
			return errors.New(err).
				Component("new-species-tracker").
				Category(errors.CategoryDatabase).
				Context("operation", "load_seasonal_data").
				Context("current_season", t.currentSeason).
				Build()
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
		return errors.Newf("failed to load lifetime species data from database: %w", err).
			Component("new-species-tracker").
			Category(errors.CategoryDatabase).
			Context("operation", "load_lifetime_data").
			Context("start_date", startDate).
			Context("end_date", endDate).
			Build()
	}

	// Only clear existing data if we have new data to replace it with
	// This prevents data loss if database returns empty results due to errors
	switch {
	case len(newSpeciesData) > 0:
		// Clear and populate lifetime tracking map with new data
		t.speciesFirstSeen = make(map[string]time.Time, len(newSpeciesData))
		for _, species := range newSpeciesData {
			if species.FirstSeenDate != "" {
				firstSeen, err := time.Parse("2006-01-02", species.FirstSeenDate)
				if err == nil {
					t.speciesFirstSeen[species.ScientificName] = firstSeen
				}
			}
		}
		logger.Debug("Loaded species data from database",
			"species_count", len(newSpeciesData))
	case len(t.speciesFirstSeen) == 0:
		// No data from database and no existing data - initialize empty map
		t.speciesFirstSeen = make(map[string]time.Time, initialSpeciesCapacity)
		logger.Debug("No species data from database, initialized empty tracking")
	default:
		// Database returned empty data but we have existing data - keep it
		logger.Debug("Database returned empty species data, preserving existing tracking data",
			"existing_species_count", len(t.speciesFirstSeen))
	}

	return nil
}

// loadYearlyDataFromDatabase loads first detection data for the current year
func (t *NewSpeciesTracker) loadYearlyDataFromDatabase(now time.Time) error {
	startDate, endDate := t.getYearDateRange(now)

	// Use GetSpeciesFirstDetectionInPeriod for yearly tracking
	yearlyData, err := t.ds.GetSpeciesFirstDetectionInPeriod(startDate, endDate, 10000, 0)
	if err != nil {
		return errors.Newf("failed to load yearly species data from database: %w", err).
			Component("new-species-tracker").
			Category(errors.CategoryDatabase).
			Context("operation", "load_yearly_data").
			Context("year_start", startDate).
			Context("year_end", endDate).
			Context("current_year", t.currentYear).
			Build()
	}

	// Only clear existing data if we have new data to replace it with
	// This prevents data loss if database returns empty results due to errors
	switch {
	case len(yearlyData) > 0:
		// Clear and populate yearly tracking map with new data
		t.speciesThisYear = make(map[string]time.Time, len(yearlyData))
		for _, species := range yearlyData {
			if species.FirstSeenDate != "" {
				firstSeen, err := time.Parse("2006-01-02", species.FirstSeenDate)
				if err == nil {
					t.speciesThisYear[species.ScientificName] = firstSeen
				}
			}
		}
		logger.Debug("Loaded yearly species data from database",
			"species_count", len(yearlyData),
			"year", t.currentYear)
	case len(t.speciesThisYear) == 0:
		// No data from database and no existing data - initialize empty map
		t.speciesThisYear = make(map[string]time.Time, initialSpeciesCapacity)
		logger.Debug("No yearly species data from database, initialized empty tracking",
			"year", t.currentYear)
	default:
		// Database returned empty data but we have existing data - keep it
		logger.Debug("Database returned empty yearly data, preserving existing tracking data",
			"existing_yearly_species_count", len(t.speciesThisYear),
			"year", t.currentYear)
	}

	return nil
}

// loadSeasonalDataFromDatabase loads first detection data for each season in the current year
func (t *NewSpeciesTracker) loadSeasonalDataFromDatabase(now time.Time) error {
	// Preserve existing seasonal maps if we have them
	existingSeasonData := t.speciesBySeason
	hasExistingData := len(existingSeasonData) > 0
	
	// Initialize seasonal maps
	t.speciesBySeason = make(map[string]map[string]time.Time)

	logger.Debug("Loading seasonal data from database",
		"total_seasons", len(t.seasons),
		"has_existing_data", hasExistingData)

	for seasonName := range t.seasons {
		startDate, endDate := t.getSeasonDateRange(seasonName, now)

		logger.Debug("Loading data for season",
			"season", seasonName,
			"start_date", startDate,
			"end_date", endDate)

		// Get first detection of each species within this season period
		seasonalData, err := t.ds.GetSpeciesFirstDetectionInPeriod(startDate, endDate, 10000, 0)
		if err != nil {
			return errors.Newf("failed to load seasonal species data from database for %s: %w", seasonName, err).
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

	// Check if all seasons returned empty data
	allEmpty := true
	for _, seasonMap := range t.speciesBySeason {
		if len(seasonMap) > 0 {
			allEmpty = false
			break
		}
	}

	// If all seasons returned empty and we had existing data, restore it
	if allEmpty && hasExistingData {
		logger.Debug("All seasons returned empty data, restoring existing seasonal tracking data")
		t.speciesBySeason = existingSeasonData
	}

	return nil
}

// getYearDateRange calculates the start and end dates for yearly tracking
func (t *NewSpeciesTracker) getYearDateRange(now time.Time) (startDate, endDate string) {
	// Use t.currentYear if explicitly set for testing, otherwise use the provided time's year
	currentYear := now.Year()
	if t.currentYear != 0 && t.currentYear != time.Now().Year() {
		// Only use t.currentYear if it was explicitly set for testing (not the default from constructor)
		currentYear = t.currentYear
	}

	// Determine the tracking year based on reset date
	// If current time is before this year's reset date, we're still in the previous tracking year
	currentYearResetDate := time.Date(currentYear, time.Month(t.resetMonth), t.resetDay, 0, 0, 0, 0, now.Location())
	
	var trackingYear int
	if now.Before(currentYearResetDate) {
		// We haven't reached this year's reset date yet, so we're still in the previous tracking year
		trackingYear = currentYear - 1
	} else {
		// We've passed this year's reset date, so we're in the current tracking year
		trackingYear = currentYear
	}

	// Calculate the tracking period: from reset date of trackingYear to day before reset date of next year
	yearStart := time.Date(trackingYear, time.Month(t.resetMonth), t.resetDay, 0, 0, 0, 0, now.Location())
	nextYearReset := time.Date(trackingYear+1, time.Month(t.resetMonth), t.resetDay, 0, 0, 0, 0, now.Location())
	yearEnd := nextYearReset.AddDate(0, 0, -1)

	startDate = yearStart.Format("2006-01-02")
	endDate = yearEnd.Format("2006-01-02")

	return startDate, endDate
}

// getSeasonDateRange calculates the start and end dates for a specific season
func (t *NewSpeciesTracker) getSeasonDateRange(seasonName string, now time.Time) (startDate, endDate string) {
	season, exists := t.seasons[seasonName]
	if !exists || season.month <= 0 || season.day <= 0 {
		// Return empty strings for unknown or invalid season
		return "", ""
	}

	// Use test year override if set, otherwise use now's year
	currentYear := now.Year()
	if t.currentYear != 0 && t.currentYear != time.Now().Year() {
		currentYear = t.currentYear
	}

	// Calculate season start date
	seasonStart := time.Date(currentYear, time.Month(season.month), season.day, 0, 0, 0, 0, now.Location())

	// Handle winter season that might start in previous year
	if t.shouldAdjustWinter(now, time.Month(season.month)) {
		seasonStart = time.Date(currentYear-1, time.Month(season.month), season.day, 0, 0, 0, 0, now.Location())
	}

	// Calculate season end date - seasons last 3 months
	// Add 3 months, then subtract 1 day to get the last day of the 3rd month
	seasonEnd := seasonStart.AddDate(0, 3, 0).AddDate(0, 0, -1)

	startDate = seasonStart.Format("2006-01-02")
	endDate = seasonEnd.Format("2006-01-02")

	return startDate, endDate
}

// isWithinCurrentYear checks if a detection time falls within the current tracking year
func (t *NewSpeciesTracker) isWithinCurrentYear(detectionTime time.Time) bool {
	// For mid-year resets, we need to calculate year range based on the detection time itself
	// to determine which tracking year the detection belongs to
	referenceTime := detectionTime
	if t.currentYear != 0 {
		// For testing: use the detection time but in the test year to maintain timezone
		referenceTime = time.Date(t.currentYear, detectionTime.Month(), detectionTime.Day(), 
			detectionTime.Hour(), detectionTime.Minute(), detectionTime.Second(), 
			detectionTime.Nanosecond(), detectionTime.Location())
	}
	startDate, endDate := t.getYearDateRange(referenceTime)
	
	yearStart, err := time.Parse("2006-01-02", startDate)
	if err != nil {
		return false
	}
	
	yearEnd, err := time.Parse("2006-01-02", endDate)
	if err != nil {
		return false
	}
	// Add 23:59:59 to end date to include the entire last day
	yearEnd = yearEnd.Add(23*time.Hour + 59*time.Minute + 59*time.Second)

	// Check if the detection falls within this year's tracking period (inclusive of both bounds)
	return (detectionTime.After(yearStart) || detectionTime.Equal(yearStart)) && 
		   (detectionTime.Before(yearEnd) || detectionTime.Equal(yearEnd))
}

// GetSpeciesStatus returns the tracking status for a species with caching for performance
// This method implements cache-first lookup with TTL validation to minimize expensive computations
func (t *NewSpeciesTracker) GetSpeciesStatus(scientificName string, currentTime time.Time) SpeciesStatus {
	t.mu.Lock()
	defer t.mu.Unlock()

	// Check cache first - if valid entry exists within TTL and same year, return it
	if cached, exists := t.statusCache[scientificName]; exists {
		// Check if cache is still valid (within TTL and same year)
		cacheValid := currentTime.Sub(cached.timestamp) < t.cacheTTL
		sameYear := currentTime.Year() == cached.timestamp.Year()
		
		if cacheValid && sameYear {
			// Cache hit - return cached result directly
			return cached.status
		}
		// Cache expired or year changed - will recompute and update cache below
	}

	// Perform periodic cache cleanup to prevent unbounded growth
	// The cleanup function will check if enough time has passed
	t.cleanupExpiredCache(currentTime)

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
		daysSince := int(currentTime.Sub(firstSeen).Hours() / hoursPerDay)
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
			daysThisYear := int(currentTime.Sub(*firstThisYear).Hours() / hoursPerDay)
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
			daysThisSeason := int(currentTime.Sub(*firstThisSeason).Hours() / hoursPerDay)
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
// cleanupExpiredCache removes expired entries and enforces size limits with LRU eviction
func (t *NewSpeciesTracker) cleanupExpiredCache(currentTime time.Time) {
	t.cleanupExpiredCacheWithForce(currentTime, false)
}

// cleanupExpiredCacheWithForce allows forcing cleanup even if recently performed (for testing)
func (t *NewSpeciesTracker) cleanupExpiredCacheWithForce(currentTime time.Time, force bool) {
	const maxStatusCacheSize = 1000 // Maximum number of species to cache
	const targetCacheSize = 800     // Target size after cleanup (80% of max)

	// Skip if recently performed (unless forced)
	if !force && currentTime.Sub(t.lastCacheCleanup) <= t.cacheTTL*10 {
		return // Skip cleanup entirely if done recently
	}

	// First pass: remove expired entries
	for scientificName := range t.statusCache {
		if currentTime.Sub(t.statusCache[scientificName].timestamp) >= t.cacheTTL {
			delete(t.statusCache, scientificName)
		}
	}

	// Second pass: if still over limit, remove oldest entries (LRU)
	if len(t.statusCache) > targetCacheSize {
		// Create a slice of entries for sorting
		type cacheEntry struct {
			name      string
			timestamp time.Time
		}
		entries := make([]cacheEntry, 0, len(t.statusCache))
		for name := range t.statusCache {
			entries = append(entries, cacheEntry{name: name, timestamp: t.statusCache[name].timestamp})
		}

		// Sort by timestamp (oldest first)
		// Note: We could optimize this with a proper LRU implementation if needed
		for i := 0; i < len(entries)-1; i++ {
			for j := i + 1; j < len(entries); j++ {
				if entries[i].timestamp.After(entries[j].timestamp) {
					entries[i], entries[j] = entries[j], entries[i]
				}
			}
		}

		// Remove oldest entries until we're at target size
		entriesToRemove := len(t.statusCache) - targetCacheSize
		for i := 0; i < entriesToRemove && i < len(entries); i++ {
			delete(t.statusCache, entries[i].name)
		}

		logger.Debug("Cache cleanup completed",
			"removed_count", entriesToRemove,
			"remaining_count", len(t.statusCache))
	}

	// Update cleanup timestamp
	t.lastCacheCleanup = currentTime
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
		FirstSeenTime:   firstSeen,
		IsNew:           false,
		DaysSinceFirst:  -1,
		LastUpdatedTime: currentTime,
		FirstThisYear:   firstThisYear,
		FirstThisSeason: firstThisSeason,
		CurrentSeason:   currentSeason,
		IsNewThisYear:   false,
		IsNewThisSeason: false,
		DaysThisYear:    -1,
		DaysThisSeason:  -1,
	}

	// Calculate lifetime status
	if exists {
		daysSince := int(currentTime.Sub(firstSeen).Hours() / hoursPerDay)
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
			daysThisYear := int(currentTime.Sub(*firstThisYear).Hours() / hoursPerDay)
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
			daysThisSeason := int(currentTime.Sub(*firstThisSeason).Hours() / hoursPerDay)
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

	daysSince := int(time.Since(firstSeen).Hours() / hoursPerDay)
	return daysSince <= t.windowDays
}

// SyncIfNeeded checks if a database sync is needed and performs it
// This helps keep the tracker updated with any database changes
func (t *NewSpeciesTracker) SyncIfNeeded() error {
	t.mu.RLock()
	elapsed := time.Since(t.lastSyncTime)
	interval := t.syncIntervalMins
	t.mu.RUnlock()

	// Compare durations directly; min interval is in minutes
	if elapsed < time.Duration(interval)*time.Minute {
		return nil // No sync needed yet
	}

	// Store count of existing data before sync
	t.mu.RLock()
	existingLifetimeCount := len(t.speciesFirstSeen)
	existingYearlyCount := len(t.speciesThisYear)
	existingSeasonalCount := 0
	for _, seasonMap := range t.speciesBySeason {
		existingSeasonalCount += len(seasonMap)
	}
	t.mu.RUnlock()

	// Log sync attempt
	logger.Debug("Starting database sync",
		"existing_lifetime_species", existingLifetimeCount,
		"existing_yearly_species", existingYearlyCount,
		"existing_seasonal_species", existingSeasonalCount)

	// Perform database sync
	if err := t.InitFromDatabase(); err != nil {
		logger.Error("Database sync failed, preserving existing data",
			"error", err,
			"existing_species", existingLifetimeCount)
		// Don't propagate error if we have existing data - continue with cached data
		if existingLifetimeCount > 0 {
			return nil
		}
		return err
	}

	// Check if sync suspiciously cleared all data
	t.mu.RLock()
	newLifetimeCount := len(t.speciesFirstSeen)
	t.mu.RUnlock()

	if existingLifetimeCount > 0 && newLifetimeCount == 0 {
		logger.Warn("Database sync returned no data but had existing data - possible database issue",
			"previous_count", existingLifetimeCount,
			"new_count", newLifetimeCount)
	}

	// Also perform periodic cleanup of old records (both species and notification records)
	pruned := t.PruneOldEntries()
	if pruned > 0 {
		logger.Debug("Pruned old entries during sync",
			"count", pruned)
	}

	return nil
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

// PruneOldEntries removes species entries older than 2x their respective window periods
// This prevents unbounded memory growth over time using period-specific cutoff times
func (t *NewSpeciesTracker) PruneOldEntries() int {
	t.mu.Lock()
	defer t.mu.Unlock()

	now := time.Now()
	pruned := 0

	// Calculate separate cutoff times for each tracking period
	lifetimeCutoff := now.AddDate(0, 0, -t.windowDays*2)

	// Prune lifetime tracking map
	for scientificName, firstSeen := range t.speciesFirstSeen {
		if firstSeen.Before(lifetimeCutoff) {
			delete(t.speciesFirstSeen, scientificName)
			pruned++
		}
	}

	// Prune yearly tracking map if enabled
	if t.yearlyEnabled {
		yearlyCutoff := now.AddDate(0, 0, -t.yearlyWindowDays*2)
		for scientificName, firstSeen := range t.speciesThisYear {
			if firstSeen.Before(yearlyCutoff) {
				delete(t.speciesThisYear, scientificName)
				pruned++
			}
		}
	}

	// Prune seasonal tracking maps if enabled
	if t.seasonalEnabled {
		seasonalCutoff := now.AddDate(0, 0, -t.seasonalWindowDays*2)
		for season, speciesMap := range t.speciesBySeason {
			for scientificName, firstSeen := range speciesMap {
				if firstSeen.Before(seasonalCutoff) {
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

	// Also cleanup old notification records (only if suppression is enabled)
	if t.notificationSuppressionWindow > 0 {
		cleaned := t.cleanupOldNotificationRecordsLocked(now)
		pruned += cleaned
	}

	return pruned
}

// cleanupOldNotificationRecordsLocked is an internal version that assumes lock is already held
func (t *NewSpeciesTracker) cleanupOldNotificationRecordsLocked(currentTime time.Time) int {
	if t.notificationLastSent == nil || t.notificationSuppressionWindow <= 0 {
		return 0
	}

	cleaned := 0
	// Compute cutoff = currentTime - suppressionWindow to remove records no longer needed
	// Once the suppression window has passed, we can notify again, so no need to keep the record
	cutoffTime := currentTime.Add(-t.notificationSuppressionWindow)

	for species, sentTime := range t.notificationLastSent {
		if sentTime.Before(cutoffTime) {
			delete(t.notificationLastSent, species)
			cleaned++
		}
	}

	return cleaned
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
		// Update if this detection is earlier than recorded
		if detectionTime.Before(firstSeen) {
			t.speciesFirstSeen[scientificName] = detectionTime
			// This is now the earliest detection
			daysSinceFirstSeen = 0
			isNew = true // New detection is always "new" when it's the earliest
		} else {
			// Calculate days since first seen
			daysSince := int(detectionTime.Sub(firstSeen).Hours() / hoursPerDay)
			daysSinceFirstSeen = daysSince
			isNew = daysSince <= t.windowDays
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

// IsSeasonMapInitialized checks if the season map is properly initialized for the given season.
// This method provides safe access to internal state for testing purposes.
func (t *NewSpeciesTracker) IsSeasonMapInitialized(season string) bool {
	t.mu.RLock()
	defer t.mu.RUnlock()

	if !t.seasonalEnabled {
		return false
	}

	return t.speciesBySeason != nil && t.speciesBySeason[season] != nil
}

// GetSeasonMapCount returns the number of species tracked for the given season.
// This method provides safe access to internal state for testing purposes.
func (t *NewSpeciesTracker) GetSeasonMapCount(season string) int {
	t.mu.RLock()
	defer t.mu.RUnlock()

	if !t.seasonalEnabled || t.speciesBySeason == nil || t.speciesBySeason[season] == nil {
		return 0
	}

	return len(t.speciesBySeason[season])
}

// ExpireCacheForTesting forces cache expiration for the given species for testing purposes.
// This method should only be used in tests to simulate cache expiration without
// manipulating internal state directly.
func (t *NewSpeciesTracker) ExpireCacheForTesting(scientificName string) {
	t.mu.Lock()
	defer t.mu.Unlock()

	if cached, exists := t.statusCache[scientificName]; exists {
		// Set timestamp to expired (1 hour ago)
		cached.timestamp = time.Now().Add(defaultCacheExpiredAge)
		t.statusCache[scientificName] = cached
	}
}

// ClearCacheForTesting clears the entire status cache for testing purposes.
// This method should only be used in tests.
func (t *NewSpeciesTracker) ClearCacheForTesting() {
	t.mu.Lock()
	defer t.mu.Unlock()

	t.statusCache = make(map[string]cachedSpeciesStatus)
}

// ShouldSuppressNotification checks if a notification for this species should be suppressed
// based on when the last notification was sent for this species.
// Returns true if notification should be suppressed, false if it should be sent.
func (t *NewSpeciesTracker) ShouldSuppressNotification(scientificName string, currentTime time.Time) bool {
	t.mu.RLock()
	lastSent, exists := t.notificationLastSent[scientificName]
	window := t.notificationSuppressionWindow
	t.mu.RUnlock()

	if !exists {
		return false // Never sent, don't suppress
	}
	if window <= 0 {
		return false // Suppression disabled
	}

	suppressUntil := lastSent.Add(window)
	shouldSuppress := currentTime.Before(suppressUntil)

	if shouldSuppress {
		logger.Debug("Suppressing duplicate notification",
			"species", scientificName,
			"suppress_until", suppressUntil,
			"suppression_window", window)
	}
	return shouldSuppress
}

// RecordNotificationSent records that a notification was sent for a species.
// This is used to prevent duplicate notifications within the suppression window.
func (t *NewSpeciesTracker) RecordNotificationSent(scientificName string, sentTime time.Time) {
	// Early return when suppression is disabled to avoid unnecessary operations
	if t.notificationSuppressionWindow <= 0 {
		return
	}

	t.mu.Lock()
	// Initialize map if needed
	if t.notificationLastSent == nil {
		t.notificationLastSent = make(map[string]time.Time, initialSpeciesCapacity)
	}

	// Record the notification time
	t.notificationLastSent[scientificName] = sentTime
	t.mu.Unlock()

	// Log outside the critical section to reduce lock contention
	logger.Debug("Recorded notification sent",
		"species", scientificName,
		"sent_time", sentTime.Format("2006-01-02 15:04:05"))
}

// CleanupOldNotificationRecords removes notification records older than 2x the suppression window
// to prevent unbounded memory growth.
func (t *NewSpeciesTracker) CleanupOldNotificationRecords(currentTime time.Time) int {
	// Early return if suppression is disabled (0 window)
	if t.notificationSuppressionWindow <= 0 {
		return 0
	}

	t.mu.Lock()
	cleaned := t.cleanupOldNotificationRecordsLocked(currentTime)
	t.mu.Unlock()

	if cleaned > 0 {
		cutoffTime := currentTime.Add(-2 * t.notificationSuppressionWindow)
		logger.Debug("Cleaned up old notification records",
			"removed_count", cleaned,
			"cutoff_time", cutoffTime.Format("2006-01-02 15:04:05"))
	}

	return cleaned
}

// Close releases resources associated with the species tracker, including the logger.
// This should be called during application shutdown or when the tracker is no longer needed.
func (t *NewSpeciesTracker) Close() error {
	// Close the shared logger used by all tracker instances
	// Note: This is a package-level resource shared across all tracker instances
	if err := Close(); err != nil {
		return errors.New(err).
			Component("new-species-tracker").
			Category(errors.CategoryResource).
			Context("operation", "close_logger").
			Build()
	}
	return nil
}
