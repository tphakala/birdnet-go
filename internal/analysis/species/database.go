// database.go - Database loading operations for species tracking

package species

import (
	"context"
	"sort"
	"time"

	"github.com/tphakala/birdnet-go/internal/errors"
	"github.com/tphakala/birdnet-go/internal/logger"
)

// InitFromDatabase populates the tracker from historical data.
// This should be called once during initialization. It uses a background
// context (no cancellation); the startup path uses InitFromDatabaseAsync, which
// supplies a cancellable context via initFromDatabaseContext.
func (t *SpeciesTracker) InitFromDatabase() error {
	return t.initFromDatabaseContext(context.Background())
}

// initFromDatabaseContext performs the historical load under the given context,
// so a shutdown during a background warm-up can abort the in-flight DB scan.
func (t *SpeciesTracker) initFromDatabaseContext(ctx context.Context) error {
	if t.ds == nil {
		return errors.Newf("datastore is nil").
			Component("new-species-tracker").
			Category(errors.CategoryConfiguration).
			Build()
	}

	now := time.Now()

	getLog().Debug("Initializing species tracker from database",
		logger.String("current_time", now.Format(time.DateTime)),
		logger.Bool("yearly_enabled", t.yearlyEnabled),
		logger.Bool("seasonal_enabled", t.seasonalEnabled))

	t.mu.Lock()
	defer t.mu.Unlock()

	// Step 1: Load lifetime tracking data (existing logic)
	if err := t.loadLifetimeDataFromDatabase(ctx, now); err != nil {
		return errors.New(err).
			Component("new-species-tracker").
			Category(errors.CategoryDatabase).
			Context("operation", "load_lifetime_data").
			Context("sync_time", now.Format(time.DateTime)).
			Build()
	}

	if err := t.loadNoveltyEpisodesFromDatabase(ctx, now); err != nil {
		getLog().Error("Failed to restore novelty episodes from database",
			logger.Error(err),
			logger.String("operation", "load_novelty_episodes"),
			logger.String("impact", "Novelty episode metadata may restart from the next detection"))
	}

	// Step 2: Load yearly tracking data if enabled
	if t.yearlyEnabled {
		if err := t.loadYearlyDataFromDatabase(ctx, now); err != nil {
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
		if err := t.loadSeasonalDataFromDatabase(ctx, now); err != nil {
			return errors.New(err).
				Component("new-species-tracker").
				Category(errors.CategoryDatabase).
				Context("operation", "load_seasonal_data").
				Context("current_season", t.currentSeason).
				Build()
		}
	}

	// Step 4: Load notification history (BG-17 fix)
	// Design decision: Don't fail initialization if notification history load fails
	// Rationale:
	// 1. Notification suppression is a "nice-to-have" feature, not critical for core functionality
	// 2. First-run scenarios: On fresh installs, the table will be empty (not an error)
	// 3. Graceful degradation: New notifications will still be suppressed (just not historical ones)
	// 4. Self-healing: As new notifications are sent, the suppression state rebuilds automatically
	// 5. The table will be created by GORM AutoMigrate on first run
	if err := t.loadNotificationHistoryFromDatabase(ctx, now); err != nil {
		getLog().Error("Failed to load notification history from database",
			logger.Error(err),
			logger.String("operation", "load_notification_history"),
			logger.String("impact", "May send duplicate notifications for species detected recently"))
		// Continue initialization - the feature will work for new notifications going forward
	}

	t.lastSyncTime = now

	// Drop any cached status computed against the pre-load maps. Status entries
	// cached during a background warm-up (or before a runtime reconfigure reload)
	// would otherwise mask the freshly loaded history for the cache TTL.
	if len(t.statusCache) > 0 {
		t.statusCache = make(map[string]cachedSpeciesStatus, initialSpeciesCapacity)
	}

	getLog().Debug("Database initialization complete",
		logger.Int("lifetime_species", len(t.speciesFirstSeen)),
		logger.Int("novelty_episodes", len(t.noveltyEpisodes)),
		logger.Int("yearly_species", len(t.speciesThisYear)),
		logger.Int("total_seasons", len(t.speciesBySeason)),
		logger.Int("notification_history_loaded", len(t.notificationLastSent)))

	return nil
}

// loadLifetimeDataFromDatabase loads all-time first detection data
func (t *SpeciesTracker) loadLifetimeDataFromDatabase(ctx context.Context, now time.Time) error {
	endDate := now.Format(time.DateOnly)
	startDate := "1900-01-01" // Load from beginning of time to get all historical data

	// TODO(context-timeout): Add a timeout to ctx (e.g., 60s) for the load operations
	// TODO(telemetry): Report initialization timeouts/failures to internal/telemetry for monitoring
	newSpeciesData, err := t.ds.GetNewSpeciesDetections(ctx, startDate, endDate, defaultDBQueryLimit, 0)
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
		t.speciesLastSeen = make(map[string]time.Time, len(newSpeciesData))
		for _, species := range newSpeciesData {
			if species.FirstSeenDate != "" {
				firstSeen, err := time.Parse(time.DateOnly, species.FirstSeenDate)
				if err != nil {
					getLog().Debug("Failed to parse first seen date",
						logger.String("species", species.ScientificName),
						logger.String("date", species.FirstSeenDate),
						logger.Error(err))
					continue
				}
				t.speciesFirstSeen[species.ScientificName] = firstSeen
				t.speciesLastSeen[species.ScientificName] = firstSeen
			}
			if species.LastSeenDate != "" {
				lastSeen, err := time.Parse(time.DateOnly, species.LastSeenDate)
				if err != nil {
					getLog().Debug("Failed to parse last seen date",
						logger.String("species", species.ScientificName),
						logger.String("date", species.LastSeenDate),
						logger.Error(err))
					continue
				}
				t.speciesLastSeen[species.ScientificName] = lastSeen
			}
		}
		getLog().Debug("Loaded species data from database",
			logger.Int("species_count", len(newSpeciesData)))
	case len(t.speciesFirstSeen) == 0:
		// No data from database and no existing data - initialize empty map
		t.speciesFirstSeen = make(map[string]time.Time, initialSpeciesCapacity)
		t.speciesLastSeen = make(map[string]time.Time, initialSpeciesCapacity)
		getLog().Debug("No species data from database, initialized empty tracking")
	default:
		// Database returned empty data but we have existing data - keep it
		getLog().Debug("Database returned empty species data, preserving existing tracking data",
			logger.Int("existing_species_count", len(t.speciesFirstSeen)))
	}

	return nil
}

// loadNoveltyEpisodesFromDatabase reconstructs active novelty episodes from detection dates.
func (t *SpeciesTracker) loadNoveltyEpisodesFromDatabase(ctx context.Context, now time.Time) error {
	history, ok := t.ds.(speciesDetectionHistoryDatastore)
	if !ok {
		return nil
	}

	endDate := trackerDateOnly(now)
	startDate := endDate.AddDate(0, 0, -(t.windowDays + 1))

	detections, err := history.GetSpeciesDetectionDatesInPeriod(ctx, startDate.Format(time.DateOnly), endDate.Format(time.DateOnly), defaultDBQueryLimit, 0)
	if err != nil {
		return errors.Newf("failed to load novelty detection dates from database: %w", err).
			Component("new-species-tracker").
			Category(errors.CategoryDatabase).
			Context("operation", "load_novelty_detection_dates").
			Context("start_date", startDate.Format(time.DateOnly)).
			Context("end_date", endDate.Format(time.DateOnly)).
			Build()
	}

	datesBySpecies := make(map[string][]time.Time, len(detections))
	for _, detection := range detections {
		if detection.ScientificName == "" || detection.Date == "" {
			continue
		}
		detectionDate, parseErr := time.Parse(time.DateOnly, detection.Date)
		if parseErr != nil {
			getLog().Debug("Failed to parse novelty detection date",
				logger.String("species", detection.ScientificName),
				logger.String("date", detection.Date),
				logger.Error(parseErr))
			continue
		}
		datesBySpecies[detection.ScientificName] = append(datesBySpecies[detection.ScientificName], detectionDate)
	}

	episodes := make(map[string]NoveltyStatus, len(datesBySpecies))
	for scientificName, dates := range datesBySpecies {
		dates = normalizeNoveltyDetectionDates(dates)
		if len(dates) == 0 {
			continue
		}

		runStart := findContiguousNoveltyRunStart(dates)
		if calculateDaysSince(now, runStart) > t.windowDays {
			continue
		}

		episodeDays, restored, restoreErr := t.restoredNoveltyEpisodeDays(ctx, history, scientificName, runStart)
		if restoreErr != nil {
			return restoreErr
		}
		if !restored {
			continue
		}

		// DaysSinceLastSeen is the absence gap that triggered the episode, matching
		// the live path's episode-creation snapshot. restoredNoveltyEpisodeDays
		// returns the firstEverNoveltyEpisodeDays sentinel for first-ever species;
		// those have no prior sighting, so fall back to the inactive sentinel
		// instead of reporting a spurious multi-decade gap.
		daysSinceLastSeen := episodeDays
		if episodeDays == firstEverNoveltyEpisodeDays {
			daysSinceLastSeen = inactiveNoveltyValue
		}

		episodes[scientificName] = NoveltyStatus{
			DaysSinceLastSeen:    daysSinceLastSeen,
			NoveltyEpisodeDays:   episodeDays,
			NoveltyEpisodeStart:  runStart,
			NoveltyEpisodeActive: true,
		}
	}

	t.noveltyEpisodes = episodes
	getLog().Debug("Restored active novelty episodes from database",
		logger.Int("episodes", len(episodes)))

	return nil
}

func (t *SpeciesTracker) restoredNoveltyEpisodeDays(ctx context.Context, history speciesDetectionHistoryDatastore, scientificName string, runStart time.Time) (episodeDays int, restored bool, err error) {
	if firstSeen, exists := t.speciesFirstSeen[scientificName]; exists && sameTrackerDate(firstSeen, runStart) {
		return firstEverNoveltyEpisodeDays, true, nil
	}

	previousDate, err := history.GetSpeciesLastDetectionDateBefore(ctx, scientificName, runStart.Format(time.DateOnly))
	if err != nil {
		return 0, false, errors.Newf("failed to load previous novelty detection date from database: %w", err).
			Component("new-species-tracker").
			Category(errors.CategoryDatabase).
			Context("operation", "load_previous_novelty_detection_date").
			Context("scientific_name", scientificName).
			Context("before_date", runStart.Format(time.DateOnly)).
			Build()
	}
	if previousDate == "" {
		return 0, false, nil
	}

	previous, parseErr := time.Parse(time.DateOnly, previousDate)
	if parseErr != nil {
		getLog().Debug("Failed to parse previous novelty detection date",
			logger.String("species", scientificName),
			logger.String("date", previousDate),
			logger.Error(parseErr))
		return 0, false, nil
	}

	absenceDays := calculateDaysSince(runStart, previous)
	if absenceDays <= 0 {
		return 0, false, nil
	}

	return absenceDays, true, nil
}

func findContiguousNoveltyRunStart(dates []time.Time) time.Time {
	runStart := dates[len(dates)-1]
	for i := len(dates) - 2; i >= 0; i-- {
		if calculateDaysSince(runStart, dates[i]) != 1 {
			break
		}
		runStart = dates[i]
	}
	return runStart
}

func normalizeNoveltyDetectionDates(dates []time.Time) []time.Time {
	sort.Slice(dates, func(i, j int) bool {
		return dates[i].Before(dates[j])
	})

	unique := dates[:0]
	var lastDate string
	for _, date := range dates {
		dateKey := date.Format(time.DateOnly)
		if dateKey == lastDate {
			continue
		}
		unique = append(unique, date)
		lastDate = dateKey
	}
	return unique
}

func trackerDateOnly(ts time.Time) time.Time {
	year, month, day := ts.Date()
	return time.Date(year, month, day, 0, 0, 0, 0, ts.Location())
}

func sameTrackerDate(a, b time.Time) bool {
	ay, am, ad := a.Date()
	by, bm, bd := b.Date()
	return ay == by && am == bm && ad == bd
}

// loadYearlyDataFromDatabase loads first detection data for the current year
func (t *SpeciesTracker) loadYearlyDataFromDatabase(ctx context.Context, now time.Time) error {
	startDate, endDate := t.getYearDateRange(now)

	// Use GetSpeciesFirstDetectionInPeriod for yearly tracking
	// TODO(telemetry): Report database load failures to internal/telemetry
	yearlyData, err := t.ds.GetSpeciesFirstDetectionInPeriod(ctx, startDate, endDate, defaultDBQueryLimit, 0)
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
				firstSeen, err := time.Parse(time.DateOnly, species.FirstSeenDate)
				if err != nil {
					getLog().Debug("Failed to parse yearly first seen date",
						logger.String("species", species.ScientificName),
						logger.String("date", species.FirstSeenDate),
						logger.Error(err))
					continue
				}
				t.speciesThisYear[species.ScientificName] = firstSeen
			}
		}
		getLog().Debug("Loaded yearly species data from database",
			logger.Int("species_count", len(yearlyData)),
			logger.Int("year", t.currentYear))
	case len(t.speciesThisYear) == 0:
		// No data from database and no existing data - initialize empty map
		t.speciesThisYear = make(map[string]time.Time, initialSpeciesCapacity)
		getLog().Debug("No yearly species data from database, initialized empty tracking",
			logger.Int("year", t.currentYear))
	default:
		// Database returned empty data but we have existing data - keep it
		getLog().Debug("Database returned empty yearly data, preserving existing tracking data",
			logger.Int("existing_yearly_species_count", len(t.speciesThisYear)),
			logger.Int("year", t.currentYear))
	}

	return nil
}

// loadSingleSeasonData loads data for a single season from the database.
func (t *SpeciesTracker) loadSingleSeasonData(ctx context.Context, seasonName string, now time.Time) (map[string]time.Time, error) {
	startDate, endDate := t.getSeasonDateRange(seasonName, now)

	getLog().Debug("Loading data for season",
		logger.String("season", seasonName),
		logger.String("start_date", startDate),
		logger.String("end_date", endDate))

	// Get first detection of each species within this season period
	// TODO(telemetry): Report seasonal data load failures to internal/telemetry
	seasonalData, err := t.ds.GetSpeciesFirstDetectionInPeriod(ctx, startDate, endDate, defaultDBQueryLimit, 0)
	if err != nil {
		return nil, errors.Newf("failed to load seasonal species data from database for %s: %w", seasonName, err).
			Component("new-species-tracker").
			Category(errors.CategoryDatabase).
			Context("operation", "load_seasonal_data").
			Context("season", seasonName).
			Context("season_start", startDate).
			Context("season_end", endDate).
			Build()
	}

	// Parse species data into map
	seasonMap := make(map[string]time.Time, len(seasonalData))
	for _, species := range seasonalData {
		if species.FirstSeenDate == "" {
			continue
		}
		firstSeen, parseErr := time.Parse(time.DateOnly, species.FirstSeenDate)
		if parseErr != nil {
			getLog().Debug("Failed to parse seasonal first seen date",
				logger.String("species", species.ScientificName),
				logger.String("season", seasonName),
				logger.String("date", species.FirstSeenDate),
				logger.Error(parseErr))
			continue
		}
		seasonMap[species.ScientificName] = firstSeen
	}

	getLog().Debug("Season loading complete",
		logger.String("season", seasonName),
		logger.Int("total_retrieved", len(seasonalData)),
		logger.Int("species_loaded", len(seasonMap)))

	return seasonMap, nil
}

// allSeasonsEmpty checks if all season maps are empty.
func (t *SpeciesTracker) allSeasonsEmpty() bool {
	for _, seasonMap := range t.speciesBySeason {
		if len(seasonMap) > 0 {
			return false
		}
	}
	return true
}

// loadSeasonalDataFromDatabase loads first detection data for each season in the current year
func (t *SpeciesTracker) loadSeasonalDataFromDatabase(ctx context.Context, now time.Time) error {
	// Preserve existing seasonal maps if we have them
	existingSeasonData := t.speciesBySeason
	hasExistingData := len(existingSeasonData) > 0

	// Initialize seasonal maps
	t.speciesBySeason = make(map[string]map[string]time.Time)

	getLog().Debug("Loading seasonal data from database",
		logger.Int("total_seasons", len(t.seasons)),
		logger.Bool("has_existing_data", hasExistingData))

	for seasonName := range t.seasons {
		seasonMap, err := t.loadSingleSeasonData(ctx, seasonName, now)
		if err != nil {
			return err
		}
		t.speciesBySeason[seasonName] = seasonMap
	}

	// If all seasons returned empty and we had existing data, restore it
	if t.allSeasonsEmpty() && hasExistingData {
		getLog().Debug("All seasons returned empty data, restoring existing seasonal tracking data")
		t.speciesBySeason = existingSeasonData
	}

	return nil
}

// loadNotificationHistoryFromDatabase loads recent notification history from database
// This prevents duplicate "new species" notifications after restart (BG-17 fix)
func (t *SpeciesTracker) loadNotificationHistoryFromDatabase(ctx context.Context, now time.Time) error {
	// Only load if notification suppression is enabled
	if t.notificationSuppressionWindow <= 0 {
		getLog().Debug("Notification suppression disabled, skipping history load")
		return nil
	}

	// This is the last load step. GetActiveNotificationHistory honors ctx, but
	// short-circuit here on an already-cancelled load (shutdown during warm-up) to
	// skip issuing one more query. Treated as a clean skip, not an error.
	select {
	case <-ctx.Done():
		getLog().Debug("Notification history load skipped: initialization cancelled")
		return nil
	default:
	}

	// Load notifications from past 2x suppression window to ensure coverage
	// This handles cases where notifications were sent just before the suppression window
	lookbackTime := now.Add(-2 * t.notificationSuppressionWindow)

	getLog().Debug("Loading notification history from database",
		logger.String("lookback_time", lookbackTime.Format(time.DateTime)),
		logger.Duration("suppression_window", t.notificationSuppressionWindow))

	// Get notification history from database
	histories, err := t.ds.GetActiveNotificationHistory(ctx, lookbackTime)
	if err != nil {
		return errors.Newf("failed to load notification history from database: %w", err).
			Component("new-species-tracker").
			Category(errors.CategoryDatabase).
			Context("operation", "load_notification_history").
			Context("lookback_time", lookbackTime.Format(time.DateTime)).
			Build()
	}

	// Populate in-memory notification map
	// Initialize map if needed
	if t.notificationLastSent == nil {
		t.notificationLastSent = make(map[string]time.Time, len(histories))
	}

	// Load notification history into memory
	for i := range histories {
		// Filter by notification type to prevent future types from overwriting new_species entries
		// This is future-proofing for when we add yearly/seasonal notification tracking
		if histories[i].NotificationType != notificationTypeNewSpecies {
			continue
		}

		// Store the most recent notification time for each species
		t.notificationLastSent[histories[i].ScientificName] = histories[i].LastSent

		getLog().Debug("Loaded notification history",
			logger.String("species", histories[i].ScientificName),
			logger.String("last_sent", histories[i].LastSent.Format(time.DateTime)),
			logger.String("notification_type", histories[i].NotificationType))
	}

	getLog().Debug("Notification history loaded successfully",
		logger.Int("notifications_loaded", len(histories)),
		logger.Int("map_size", len(t.notificationLastSent)))

	return nil
}
