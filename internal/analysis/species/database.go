// database.go - Database loading operations for species tracking

package species

import (
	"context"
	"time"

	"github.com/tphakala/birdnet-go/internal/errors"
	"github.com/tphakala/birdnet-go/internal/logger"
)

// InitFromDatabase populates the tracker from historical data
// This should be called once during initialization
func (t *SpeciesTracker) InitFromDatabase() error {
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
	if err := t.loadLifetimeDataFromDatabase(now); err != nil {
		return errors.New(err).
			Component("new-species-tracker").
			Category(errors.CategoryDatabase).
			Context("operation", "load_lifetime_data").
			Context("sync_time", now.Format(time.DateTime)).
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

	// Step 4: Load notification history (BG-17 fix)
	// Design decision: Don't fail initialization if notification history load fails
	// Rationale:
	// 1. Notification suppression is a "nice-to-have" feature, not critical for core functionality
	// 2. First-run scenarios: On fresh installs, the table will be empty (not an error)
	// 3. Graceful degradation: New notifications will still be suppressed (just not historical ones)
	// 4. Self-healing: As new notifications are sent, the suppression state rebuilds automatically
	// 5. The table will be created by GORM AutoMigrate on first run
	if err := t.loadNotificationHistoryFromDatabase(now); err != nil {
		getLog().Error("Failed to load notification history from database",
			logger.Error(err),
			logger.String("operation", "load_notification_history"),
			logger.String("impact", "May send duplicate notifications for species detected recently"))
		// Continue initialization - the feature will work for new notifications going forward
	}

	t.lastSyncTime = now

	getLog().Debug("Database initialization complete",
		logger.Int("lifetime_species", len(t.speciesFirstSeen)),
		logger.Int("yearly_species", len(t.speciesThisYear)),
		logger.Int("total_seasons", len(t.speciesBySeason)),
		logger.Int("notification_history_loaded", len(t.notificationLastSent)))

	return nil
}

// loadLifetimeDataFromDatabase loads all-time first detection data
func (t *SpeciesTracker) loadLifetimeDataFromDatabase(now time.Time) error {
	endDate := now.Format(time.DateOnly)
	startDate := "1900-01-01" // Load from beginning of time to get all historical data

	// TODO(graceful-shutdown): Accept context parameter to enable graceful cancellation during shutdown
	// TODO(context-timeout): Add timeout context (e.g., 60s) for database initialization operations
	// TODO(telemetry): Report initialization timeouts/failures to internal/telemetry for monitoring
	newSpeciesData, err := t.ds.GetNewSpeciesDetections(context.Background(), startDate, endDate, defaultDBQueryLimit, 0)
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
				firstSeen, err := time.Parse(time.DateOnly, species.FirstSeenDate)
				if err != nil {
					getLog().Debug("Failed to parse first seen date",
						logger.String("species", species.ScientificName),
						logger.String("date", species.FirstSeenDate),
						logger.Error(err))
					continue
				}
				t.speciesFirstSeen[species.ScientificName] = firstSeen
			}
		}
		getLog().Debug("Loaded species data from database",
			logger.Int("species_count", len(newSpeciesData)))
	case len(t.speciesFirstSeen) == 0:
		// No data from database and no existing data - initialize empty map
		t.speciesFirstSeen = make(map[string]time.Time, initialSpeciesCapacity)
		getLog().Debug("No species data from database, initialized empty tracking")
	default:
		// Database returned empty data but we have existing data - keep it
		getLog().Debug("Database returned empty species data, preserving existing tracking data",
			logger.Int("existing_species_count", len(t.speciesFirstSeen)))
	}

	return nil
}

// loadYearlyDataFromDatabase loads first detection data for the current year
func (t *SpeciesTracker) loadYearlyDataFromDatabase(now time.Time) error {
	startDate, endDate := t.getYearDateRange(now)

	// Use GetSpeciesFirstDetectionInPeriod for yearly tracking
	// TODO(graceful-shutdown): Accept context parameter for graceful cancellation
	// TODO(telemetry): Report database load failures to internal/telemetry
	yearlyData, err := t.ds.GetSpeciesFirstDetectionInPeriod(context.Background(), startDate, endDate, defaultDBQueryLimit, 0)
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
func (t *SpeciesTracker) loadSingleSeasonData(seasonName string, now time.Time) (map[string]time.Time, error) {
	startDate, endDate := t.getSeasonDateRange(seasonName, now)

	getLog().Debug("Loading data for season",
		logger.String("season", seasonName),
		logger.String("start_date", startDate),
		logger.String("end_date", endDate))

	// Get first detection of each species within this season period
	// TODO(graceful-shutdown): Accept context parameter for cancellation during shutdown
	// TODO(telemetry): Report seasonal data load failures to internal/telemetry
	seasonalData, err := t.ds.GetSpeciesFirstDetectionInPeriod(context.Background(), startDate, endDate, defaultDBQueryLimit, 0)
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
func (t *SpeciesTracker) loadSeasonalDataFromDatabase(now time.Time) error {
	// Preserve existing seasonal maps if we have them
	existingSeasonData := t.speciesBySeason
	hasExistingData := len(existingSeasonData) > 0

	// Initialize seasonal maps
	t.speciesBySeason = make(map[string]map[string]time.Time)

	getLog().Debug("Loading seasonal data from database",
		logger.Int("total_seasons", len(t.seasons)),
		logger.Bool("has_existing_data", hasExistingData))

	for seasonName := range t.seasons {
		seasonMap, err := t.loadSingleSeasonData(seasonName, now)
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
func (t *SpeciesTracker) loadNotificationHistoryFromDatabase(now time.Time) error {
	// Only load if notification suppression is enabled
	if t.notificationSuppressionWindow <= 0 {
		getLog().Debug("Notification suppression disabled, skipping history load")
		return nil
	}

	// Load notifications from past 2x suppression window to ensure coverage
	// This handles cases where notifications were sent just before the suppression window
	lookbackTime := now.Add(-2 * t.notificationSuppressionWindow)

	getLog().Debug("Loading notification history from database",
		logger.String("lookback_time", lookbackTime.Format(time.DateTime)),
		logger.Duration("suppression_window", t.notificationSuppressionWindow))

	// Get notification history from database
	histories, err := t.ds.GetActiveNotificationHistory(lookbackTime)
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
