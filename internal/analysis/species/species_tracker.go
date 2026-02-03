// species_tracker.go

// Package species provides functionality for tracking and analyzing
// bird species detections over various time periods.
package species

import (
	"context"
	"sync"
	"time"

	"github.com/tphakala/birdnet-go/internal/conf"
	"github.com/tphakala/birdnet-go/internal/datastore"
	"github.com/tphakala/birdnet-go/internal/logger"
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
	hoursPerDay          = 24
	seasonBufferDays     = 7 // Days buffer for season comparison
	seasonBufferDuration = seasonBufferDays * hoursPerDay * time.Hour

	// Season calculations
	// For any season starting in late year months (Oct, Nov, Dec), adjustment happens if current month is early in year
	yearCrossingCutoffMonth time.Month = time.June // Cutoff for determining year-crossing season adjustment

	// Month constants for season configuration
	monthFebruary  = 2  // February (special case for leap years)
	monthMarch     = 3  // March - Spring start (Northern Hemisphere)
	monthJune      = 6  // June - Summer start (Northern Hemisphere)
	monthSeptember = 9  // September - Fall start (Northern Hemisphere)
	monthDecember  = 12 // December - Winter start (Northern Hemisphere)

	// Default season start days (Northern Hemisphere astronomical dates)
	daySpringEquinox  = 20 // March 20 - Vernal equinox
	daySummerSolstice = 21 // June 21 - Summer solstice
	dayFallEquinox    = 22 // September 22 - Autumnal equinox
	dayWinterSolstice = 21 // December 21 - Winter solstice

	// Season duration and year calculations
	monthsPerYear   = 12 // Number of months in a year
	monthsPerSeason = 3  // Duration of each season in months

	// Database query limits
	defaultDBQueryLimit = 10000 // Maximum records per database query

	// Notification suppression
	defaultNotificationSuppressionWindow = 168 * time.Hour // Default suppression window (7 days)
	notificationTypeNewSpecies           = "new_species"   // Notification type for new species alerts

	// Cache management
	maxStatusCacheSize           = 1000 // Maximum number of species to cache
	targetCacheSize              = 800  // Target size after cleanup (80% of max)
	cacheCleanupIntervalMultiple = 10   // Cleanup runs every cacheTTL * this multiplier

	// Retention periods
	lifetimeRetentionYears = 10 // How long to keep lifetime data
)

// Package-level logger for species tracking
// daysInMonth contains the number of days in each month (non-leap year, Jan=index 0)
var daysInMonth = [12]int{31, 28, 31, 30, 31, 30, 31, 31, 30, 31, 30, 31}

// getLog returns the species tracker logger.
// Fetched dynamically to ensure it uses the current centralized logger.
func getLog() logger.Logger {
	return logger.Global().Module("analysis.species")
}

// SpeciesDatastore defines the minimal interface needed by SpeciesTracker
type SpeciesDatastore interface {
	GetNewSpeciesDetections(ctx context.Context, startDate, endDate string, limit, offset int) ([]datastore.NewSpeciesData, error)
	GetSpeciesFirstDetectionInPeriod(ctx context.Context, startDate, endDate string, limit, offset int) ([]datastore.NewSpeciesData, error)
	// Notification history methods for BG-17 fix
	GetActiveNotificationHistory(after time.Time) ([]datastore.NotificationHistory, error)
	SaveNotificationHistory(history *datastore.NotificationHistory) error
	DeleteExpiredNotificationHistory(before time.Time) (int64, error)
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

// SpeciesTracker tracks species detections and identifies new species
// within a configurable time window. Designed for minimal memory allocations.
type SpeciesTracker struct {
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

	// Cached season order for performance optimization (built once at initialization)
	// This avoids rebuilding the season order on every computeCurrentSeason() call
	cachedSeasonOrder []string

	// Goroutine lifecycle management for graceful shutdown
	// Tracks in-flight async database operations (notification persistence/cleanup)
	asyncOpsWg sync.WaitGroup
}

// seasonDates represents the start date for a season
type seasonDates struct {
	month int
	day   int
}

// NewTrackerFromSettings creates a tracker from configuration settings
// Note: All time calculations use the system's local timezone via time.Now()
func NewTrackerFromSettings(ds SpeciesDatastore, settings *conf.SpeciesTrackingSettings) *SpeciesTracker {
	now := time.Now() // Uses system local timezone

	// Log initialization
	getLog().Debug("Creating new species tracker",
		logger.Bool("enabled", settings.Enabled),
		logger.Int("window_days", settings.NewSpeciesWindowDays),
		logger.Bool("yearly_enabled", settings.YearlyTracking.Enabled),
		logger.Bool("seasonal_enabled", settings.SeasonalTracking.Enabled),
		logger.String("current_time", now.Format(time.DateTime)))

	tracker := &SpeciesTracker{
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
			// Validate season date
			if err := validateSeasonDate(season.StartMonth, season.StartDay); err != nil {
				getLog().Error("Invalid season date, skipping",
					logger.String("season", name),
					logger.Int("month", season.StartMonth),
					logger.Int("day", season.StartDay),
					logger.Error(err))
				continue
			}
			tracker.seasons[name] = seasonDates{
				month: season.StartMonth,
				day:   season.StartDay,
			}
			getLog().Debug("Configured season",
				logger.String("name", name),
				logger.Int("start_month", season.StartMonth),
				logger.Int("start_day", season.StartDay))
		}
	} else {
		tracker.initializeDefaultSeasons()
	}

	// Build cached season order once at initialization
	if tracker.seasonalEnabled {
		tracker.initializeSeasonOrder()
	}

	tracker.currentSeason = tracker.getCurrentSeason(now)

	getLog().Debug("Species tracker initialized",
		logger.String("current_season", tracker.currentSeason),
		logger.Int("current_year", tracker.currentYear),
		logger.Int("total_seasons", len(tracker.seasons)))

	// Set notification suppression window from configuration
	// 0 is valid (disabled), negative values get default
	if settings.NotificationSuppressionHours < 0 {
		tracker.notificationSuppressionWindow = defaultNotificationSuppressionWindow
	} else {
		tracker.notificationSuppressionWindow = time.Duration(settings.NotificationSuppressionHours) * time.Hour
	}

	return tracker
}

// GetWindowDays returns the configured window for new species
func (t *SpeciesTracker) GetWindowDays() int {
	return t.windowDays
}

// GetSpeciesCount returns the number of tracked species
func (t *SpeciesTracker) GetSpeciesCount() int {
	t.mu.RLock()
	defer t.mu.RUnlock()
	return len(t.speciesFirstSeen)
}
