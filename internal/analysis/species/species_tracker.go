// species_tracker.go

// Package species provides functionality for tracking and analyzing
// bird species detections over various time periods.
package species

import (
	"context"
	"sync"
	"sync/atomic"
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

	// speciesTrackerInitTimeout bounds the background warm-up load so a stalled
	// datastore query cannot keep the tracker suppressed forever or block
	// shutdown indefinitely. It is a generous safety net, NOT a tuning knob: the
	// normal load takes seconds, so this only fires for a genuinely stuck query.
	// Keep it large; too short would abort a legitimately slow load on a big
	// database or slow storage and briefly flag every species as new.
	speciesTrackerInitTimeout = 5 * time.Minute

	// notificationPersistTimeout bounds the fire-and-forget notification-history
	// writes (save/cleanup) so a stuck SQLite write cannot leak a goroutine
	// indefinitely. These are small local upsert/delete operations.
	notificationPersistTimeout = 30 * time.Second

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
	notificationTypeLifer                = "lifer"         // Notification type for lifer (not on the user's imported life list) alerts

	// liferNotificationSuppressionWindow is a fixed, short re-alert interval for
	// lifer notifications — intentionally NOT the user-configurable
	// NotificationSuppressionHours used for new-species alerts. A lifer is only
	// resolved by the user going out, confirming the species, and adding it to
	// their life list; until then the species stays "not on the list" on every
	// detection. A multi-day suppression window (the new-species default) would
	// mean the user might only be reminded once every several days, easy to
	// forget; a short window keeps reminding them across a session without
	// spamming on every single detection cycle.
	liferNotificationSuppressionWindow = 5 * time.Minute

	// Novelty episode tracking
	firstEverNoveltyEpisodeDays = 36500 // Treat first-ever detections as a very large absence (~100 years)
	inactiveNoveltyValue        = -1    // Sentinel used when no novelty episode is active

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
	GetActiveNotificationHistory(ctx context.Context, after time.Time) ([]datastore.NotificationHistory, error)
	// GetActiveNotificationHistoryByType loads suppression state for a single
	// notification type on restart. Used for "lifer" so it warms up
	// independently of the "new_species" notifications loaded via
	// GetActiveNotificationHistory (see liferNotificationLastSent).
	GetActiveNotificationHistoryByType(ctx context.Context, notificationType string, after time.Time) ([]datastore.NotificationHistory, error)
	SaveNotificationHistory(ctx context.Context, history *datastore.NotificationHistory) error
	DeleteExpiredNotificationHistory(ctx context.Context, before time.Time) (int64, error)
}

type speciesDetectionHistoryDatastore interface {
	GetSpeciesDetectionDatesInPeriod(ctx context.Context, startDate, endDate string, limit, offset int) ([]datastore.SpeciesDetectionDate, error)
	GetSpeciesLastDetectionDateBefore(ctx context.Context, scientificName, beforeDate string) (string, error)
}

// SpeciesStatus represents the tracking status of a species across multiple periods
type SpeciesStatus struct {
	// Existing lifetime tracking
	FirstSeenTime   time.Time
	IsNew           bool
	DaysSinceFirst  int
	LastUpdatedTime time.Time // For cache management

	// Absence-return tracking
	DaysSinceLastSeen int // Days since the previous detection before a return episode; -1 if first-ever / unknown

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

// NoveltyStatus describes the current absence-return episode for a species.
// A novelty episode starts when a species is first detected, or when it returns
// after at least one calendar day without detections. The episode remains active
// for the same window used by new-species tracking.
type NoveltyStatus struct {
	DaysSinceLastSeen    int
	NoveltyEpisodeDays   int
	NoveltyEpisodeStart  time.Time
	NoveltyEpisodeActive bool
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
	speciesLastSeen  map[string]time.Time // scientificName -> most recent detection time
	windowDays       int                  // Days to consider a species "new"

	// Novelty episode tracking
	noveltyEpisodes map[string]NoveltyStatus // scientificName -> active novelty episode

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

	// Lifer notification suppression: a structural sibling of
	// notificationLastSent, not the same map. A "new_species" notification
	// and a "lifer" notification for the same scientific name are
	// independent events (a species can be new to this install but not a
	// lifer, or vice versa once a life list is uploaded), so they must not
	// share one suppression timer. Uses the fixed liferNotificationSuppressionWindow,
	// not the user-configurable notificationSuppressionWindow — see that
	// constant's doc comment for why.
	liferNotificationLastSent map[string]time.Time

	// Cached season order for performance optimization (built once at initialization)
	// This avoids rebuilding the season order on every computeCurrentSeason() call
	cachedSeasonOrder []string

	// Goroutine lifecycle management for graceful shutdown
	// Tracks in-flight async database operations (notification persistence/cleanup)
	asyncOpsWg sync.WaitGroup

	// warming is true while the background historical load started by
	// InitFromDatabaseAsync is in flight. While warming, the public hot-path
	// methods short-circuit (returning "not new") BEFORE acquiring t.mu, so the
	// HTTP server stays responsive and no spurious new-species notifications
	// fire from empty maps. It is an atomic so those guards never block on the
	// mutex the background load holds. Default false: a directly-constructed
	// tracker (synchronous InitFromDatabase, or none) behaves normally.
	//
	// Guarded methods (suppress while warming): GetSpeciesStatus,
	// GetBatchSpeciesStatus, UpdateSpecies, IsNewSpecies, CheckAndUpdateSpecies,
	// CheckAndUpdateSpeciesWithNovelty, GetSpeciesCount. The notification
	// helpers (ShouldSuppressNotification, RecordNotificationSent) are
	// intentionally NOT guarded: they are only reached from the new-species
	// notification branch, which the suppressed isNew already prevents during
	// warm-up, so they cannot run mid-load.
	warming atomic.Bool

	// initCancel cancels the in-flight background load started by
	// InitFromDatabaseAsync. Close calls it (lock-free) so a shutdown landing in
	// the warm-up window aborts the DB scan instead of blocking on it. nil when
	// no async load was started.
	initCancel atomic.Pointer[context.CancelFunc]
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
		speciesLastSeen:  make(map[string]time.Time, initialSpeciesCapacity),
		windowDays:       settings.NewSpeciesWindowDays,

		// Novelty episode tracking
		noveltyEpisodes: make(map[string]NoveltyStatus, initialSpeciesCapacity),

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
		notificationLastSent:      make(map[string]time.Time, initialSpeciesCapacity),
		liferNotificationLastSent: make(map[string]time.Time, initialSpeciesCapacity),
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
	// During the background warm-up load the maps are still being populated and
	// the background goroutine holds t.mu; report 0 without blocking.
	if t.warming.Load() {
		return 0
	}
	t.mu.RLock()
	defer t.mu.RUnlock()
	return len(t.speciesFirstSeen)
}

// IsWarming reports whether the background historical load started by
// InitFromDatabaseAsync is still in flight. While warming, new-species status is
// suppressed so the tracker never reports a spurious "new species" from an
// not-yet-populated database load. Intended for tests and diagnostics.
func (t *SpeciesTracker) IsWarming() bool {
	return t.warming.Load()
}

// InitFromDatabaseAsync runs the historical load in the background so it does not
// block startup (notably the HTTP server). The tracker enters the warming state
// immediately and leaves it once the load finishes, succeed or fail; on failure
// it still goes live and self-heals from new detections. The load is registered
// with asyncOpsWg so Close waits for it, and runs under a cancellable,
// timeout-bounded context so Close can abort it during shutdown and a stalled
// query cannot keep the tracker warming forever.
//
// Use this on the startup path only. Runtime reconfiguration keeps calling the
// synchronous InitFromDatabase, which is a rare, user-initiated action; a
// concurrent SyncIfNeeded reload is harmless (it serializes on t.mu).
func (t *SpeciesTracker) InitFromDatabaseAsync() {
	t.warming.Store(true)

	ctx, cancel := context.WithTimeout(context.Background(), speciesTrackerInitTimeout)
	t.initCancel.Store(&cancel)

	// Stamp the sync time now, before any detection can flow, so an early
	// SyncIfNeeded during the warm-up window does not kick off a redundant
	// second full load on top of this one (lastSyncTime would otherwise be zero).
	t.mu.Lock()
	t.lastSyncTime = time.Now()
	t.mu.Unlock()

	t.asyncOpsWg.Go(func() {
		defer t.warming.Store(false)
		defer cancel()
		if err := t.initFromDatabaseContext(ctx); err != nil {
			getLog().Error("species tracker background initialization failed; continuing with live detections",
				logger.Error(err),
				logger.String("operation", "species_tracker_async_init"),
				logger.String("impact", "new-species history unavailable until the next successful sync"))
		}
	})
}
