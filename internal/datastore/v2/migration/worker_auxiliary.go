package migration

import (
	"context"
	"fmt"
	"time"

	"github.com/tphakala/birdnet-go/internal/datastore"
	"github.com/tphakala/birdnet-go/internal/datastore/v2/entities"
	"github.com/tphakala/birdnet-go/internal/datastore/v2/repository"
	"github.com/tphakala/birdnet-go/internal/logger"
)

// AuxiliaryMigrator handles migration of auxiliary tables (weather, image cache, thresholds, etc.)
// These tables are independent of detections and can be migrated in bulk.
type AuxiliaryMigrator struct {
	legacyStore   datastore.Interface
	weatherRepo   repository.WeatherRepository
	imageCacheRepo repository.ImageCacheRepository
	thresholdRepo repository.DynamicThresholdRepository
	notificationRepo repository.NotificationHistoryRepository
	logger        logger.Logger
}

// AuxiliaryMigratorConfig configures the auxiliary migrator.
type AuxiliaryMigratorConfig struct {
	LegacyStore      datastore.Interface
	WeatherRepo      repository.WeatherRepository
	ImageCacheRepo   repository.ImageCacheRepository
	ThresholdRepo    repository.DynamicThresholdRepository
	NotificationRepo repository.NotificationHistoryRepository
	Logger           logger.Logger
}

// NewAuxiliaryMigrator creates a new auxiliary migrator.
func NewAuxiliaryMigrator(cfg *AuxiliaryMigratorConfig) *AuxiliaryMigrator {
	return &AuxiliaryMigrator{
		legacyStore:      cfg.LegacyStore,
		weatherRepo:      cfg.WeatherRepo,
		imageCacheRepo:   cfg.ImageCacheRepo,
		thresholdRepo:    cfg.ThresholdRepo,
		notificationRepo: cfg.NotificationRepo,
		logger:           cfg.Logger,
	}
}

// MigrateAll migrates all auxiliary tables from legacy to v2.
// This should be called once during the migration process.
func (m *AuxiliaryMigrator) MigrateAll(ctx context.Context) error {
	if m.legacyStore == nil {
		m.logger.Debug("no legacy store provided, skipping auxiliary migration")
		return nil
	}

	m.logger.Info("starting auxiliary table migration")

	// Migrate each auxiliary table type
	if err := m.migrateImageCaches(ctx); err != nil {
		return fmt.Errorf("image cache migration failed: %w", err)
	}

	if err := m.migrateDynamicThresholds(ctx); err != nil {
		return fmt.Errorf("dynamic threshold migration failed: %w", err)
	}

	if err := m.migrateNotificationHistory(ctx); err != nil {
		return fmt.Errorf("notification history migration failed: %w", err)
	}

	if err := m.migrateWeatherData(ctx); err != nil {
		return fmt.Errorf("weather data migration failed: %w", err)
	}

	m.logger.Info("auxiliary table migration completed")
	return nil
}

// migrateImageCaches migrates all image cache entries.
func (m *AuxiliaryMigrator) migrateImageCaches(ctx context.Context) error {
	if m.imageCacheRepo == nil {
		m.logger.Debug("image cache repo not configured, skipping")
		return nil
	}

	// Get all image caches from legacy (wikimedia provider is default)
	legacyCaches, err := m.legacyStore.GetAllImageCaches("wikimedia")
	if err != nil {
		m.logger.Warn("failed to get legacy image caches", logger.Error(err))
		return nil // Non-fatal: image cache is not critical
	}

	if len(legacyCaches) == 0 {
		m.logger.Debug("no image caches to migrate")
		return nil
	}

	var migrated int
	for i := range legacyCaches {
		cache := &legacyCaches[i]
		v2Cache := &entities.ImageCache{
			ProviderName:   cache.ProviderName,
			ScientificName: cache.ScientificName,
			SourceProvider: cache.SourceProvider,
			URL:            cache.URL,
			LicenseName:    cache.LicenseName,
			LicenseURL:     cache.LicenseURL,
			AuthorName:     cache.AuthorName,
			AuthorURL:      cache.AuthorURL,
			CachedAt:       cache.CachedAt,
		}
		if err := m.imageCacheRepo.SaveImageCache(ctx, v2Cache); err != nil {
			m.logger.Warn("failed to migrate image cache",
				logger.String("species", cache.ScientificName),
				logger.Error(err))
			continue
		}
		migrated++
	}

	m.logger.Info("image cache migration completed",
		logger.Int("total", len(legacyCaches)),
		logger.Int("migrated", migrated))
	return nil
}

// migrateDynamicThresholds migrates all dynamic thresholds and their events.
func (m *AuxiliaryMigrator) migrateDynamicThresholds(ctx context.Context) error {
	if m.thresholdRepo == nil {
		m.logger.Debug("threshold repo not configured, skipping")
		return nil
	}

	// Get all thresholds from legacy
	legacyThresholds, err := m.legacyStore.GetAllDynamicThresholds()
	if err != nil {
		m.logger.Warn("failed to get legacy thresholds", logger.Error(err))
		return nil // Non-fatal
	}

	if len(legacyThresholds) == 0 {
		m.logger.Debug("no dynamic thresholds to migrate")
		return nil
	}

	var migrated int
	for i := range legacyThresholds {
		threshold := &legacyThresholds[i]
		v2Threshold := entities.DynamicThreshold{
			SpeciesName:    threshold.SpeciesName,
			ScientificName: threshold.ScientificName,
			Level:          threshold.Level,
			CurrentValue:   threshold.CurrentValue,
			BaseThreshold:  threshold.BaseThreshold,
			HighConfCount:  threshold.HighConfCount,
			ValidHours:     threshold.ValidHours,
			ExpiresAt:      threshold.ExpiresAt,
			LastTriggered:  threshold.LastTriggered,
			FirstCreated:   threshold.FirstCreated,
			TriggerCount:   threshold.TriggerCount,
		}
		if err := m.thresholdRepo.SaveDynamicThreshold(ctx, &v2Threshold); err != nil {
			m.logger.Warn("failed to migrate threshold",
				logger.String("species", threshold.SpeciesName),
				logger.Error(err))
			continue
		}
		migrated++

		// Migrate threshold events for this species
		if err := m.migrateThresholdEvents(ctx, threshold.SpeciesName); err != nil {
			m.logger.Warn("failed to migrate threshold events",
				logger.String("species", threshold.SpeciesName),
				logger.Error(err))
		}
	}

	m.logger.Info("dynamic threshold migration completed",
		logger.Int("total", len(legacyThresholds)),
		logger.Int("migrated", migrated))
	return nil
}

// migrateThresholdEvents migrates threshold events for a species.
func (m *AuxiliaryMigrator) migrateThresholdEvents(ctx context.Context, speciesName string) error {
	// Get events from legacy (limit to 100 most recent)
	legacyEvents, err := m.legacyStore.GetThresholdEvents(speciesName, 100)
	if err != nil {
		return err
	}

	for i := range legacyEvents {
		event := &legacyEvents[i]
		v2Event := &entities.ThresholdEvent{
			SpeciesName:   event.SpeciesName,
			PreviousLevel: event.PreviousLevel,
			NewLevel:      event.NewLevel,
			PreviousValue: event.PreviousValue,
			NewValue:      event.NewValue,
			ChangeReason:  event.ChangeReason,
			Confidence:    event.Confidence,
			CreatedAt:     event.CreatedAt,
		}
		if err := m.thresholdRepo.SaveThresholdEvent(ctx, v2Event); err != nil {
			return err
		}
	}
	return nil
}

// migrateNotificationHistory migrates notification history.
func (m *AuxiliaryMigrator) migrateNotificationHistory(ctx context.Context) error {
	if m.notificationRepo == nil {
		m.logger.Debug("notification repo not configured, skipping")
		return nil
	}

	// Get active notification history (not expired)
	legacyHistory, err := m.legacyStore.GetActiveNotificationHistory(time.Now())
	if err != nil {
		m.logger.Warn("failed to get legacy notification history", logger.Error(err))
		return nil // Non-fatal
	}

	if len(legacyHistory) == 0 {
		m.logger.Debug("no notification history to migrate")
		return nil
	}

	var migrated int
	for i := range legacyHistory {
		history := &legacyHistory[i]
		v2History := &entities.NotificationHistory{
			ScientificName:   history.ScientificName,
			NotificationType: history.NotificationType,
			LastSent:         history.LastSent,
			ExpiresAt:        history.ExpiresAt,
		}
		if err := m.notificationRepo.SaveNotificationHistory(ctx, v2History); err != nil {
			m.logger.Warn("failed to migrate notification history",
				logger.String("species", history.ScientificName),
				logger.Error(err))
			continue
		}
		migrated++
	}

	m.logger.Info("notification history migration completed",
		logger.Int("total", len(legacyHistory)),
		logger.Int("migrated", migrated))
	return nil
}

// migrateWeatherData migrates DailyEvents and HourlyWeather from legacy to v2.
// Handles ID remapping since V2 may have different IDs for the same dates.
func (m *AuxiliaryMigrator) migrateWeatherData(ctx context.Context) error {
	if m.weatherRepo == nil {
		m.logger.Debug("weather repo not configured, skipping")
		return nil
	}

	// Step 1: Get legacy data
	legacyEvents, err := m.legacyStore.GetAllDailyEvents()
	if err != nil {
		m.logger.Warn("failed to get legacy daily events", logger.Error(err))
		return nil // Non-fatal
	}

	legacyWeather, err := m.legacyStore.GetAllHourlyWeather()
	if err != nil {
		m.logger.Warn("failed to get legacy hourly weather", logger.Error(err))
		return nil // Non-fatal
	}

	if len(legacyEvents) == 0 {
		m.logger.Debug("no weather data to migrate")
		return nil
	}

	// Step 2: Build Legacy ID -> Date mapping
	legacyIDToDate := make(map[uint]string, len(legacyEvents))
	for _, event := range legacyEvents {
		legacyIDToDate[event.ID] = event.Date
	}

	// Step 3: Migrate DailyEvents to V2
	v2Events := make([]entities.DailyEvents, len(legacyEvents))
	for i, event := range legacyEvents {
		v2Events[i] = entities.DailyEvents{
			Date:     event.Date,
			Sunrise:  event.Sunrise,
			Sunset:   event.Sunset,
			Country:  event.Country,
			CityName: event.CityName,
		}
	}

	migrated, err := m.weatherRepo.SaveAllDailyEvents(ctx, v2Events)
	if err != nil {
		m.logger.Warn("failed to migrate some daily events", logger.Error(err))
	}
	m.logger.Info("daily events migration completed",
		logger.Int("total", len(legacyEvents)),
		logger.Int("migrated", migrated))

	// Step 4: Build Date -> V2 ID mapping (after migration)
	v2EventsAll, err := m.weatherRepo.GetAllDailyEvents(ctx)
	if err != nil {
		m.logger.Warn("failed to get v2 daily events for ID mapping", logger.Error(err))
		return nil
	}

	dateToV2ID := make(map[string]uint, len(v2EventsAll))
	for _, event := range v2EventsAll {
		dateToV2ID[event.Date] = event.ID
	}

	// Step 5: Migrate HourlyWeather with ID remapping
	if len(legacyWeather) == 0 {
		m.logger.Debug("no hourly weather to migrate")
		return nil
	}

	v2Weather := make([]entities.HourlyWeather, 0, len(legacyWeather))
	var skipped int
	for i := range legacyWeather {
		w := &legacyWeather[i]
		// Lookup: LegacyID -> Date -> V2ID
		date, ok := legacyIDToDate[w.DailyEventsID]
		if !ok {
			skipped++
			continue // Orphan record, skip
		}
		v2ID, ok := dateToV2ID[date]
		if !ok {
			skipped++
			continue // Date not in V2, skip
		}

		v2Weather = append(v2Weather, entities.HourlyWeather{
			DailyEventsID: v2ID, // Remapped ID!
			Time:          w.Time,
			Temperature:   w.Temperature,
			FeelsLike:     w.FeelsLike,
			TempMin:       w.TempMin,
			TempMax:       w.TempMax,
			Pressure:      w.Pressure,
			Humidity:      w.Humidity,
			Visibility:    w.Visibility,
			WindSpeed:     w.WindSpeed,
			WindDeg:       w.WindDeg,
			WindGust:      w.WindGust,
			Clouds:        w.Clouds,
			WeatherMain:   w.WeatherMain,
			WeatherDesc:   w.WeatherDesc,
			WeatherIcon:   w.WeatherIcon,
		})
	}

	migratedWeather, err := m.weatherRepo.SaveAllHourlyWeather(ctx, v2Weather)
	if err != nil {
		m.logger.Warn("failed to migrate some hourly weather", logger.Error(err))
	}

	m.logger.Info("hourly weather migration completed",
		logger.Int("total", len(legacyWeather)),
		logger.Int("migrated", migratedWeather),
		logger.Int("skipped", skipped))

	return nil
}

// ValidateAuxiliaryTables validates that auxiliary tables have been migrated.
func (m *AuxiliaryMigrator) ValidateAuxiliaryTables(ctx context.Context) error {
	if m.legacyStore == nil {
		return nil
	}

	m.logger.Info("validating auxiliary table migration")

	// Validate image caches
	if m.imageCacheRepo != nil {
		legacyCaches, _ := m.legacyStore.GetAllImageCaches("wikimedia")
		v2Caches, _ := m.imageCacheRepo.GetAllImageCaches(ctx, "wikimedia")
		m.logger.Info("image cache validation",
			logger.Int("legacy_count", len(legacyCaches)),
			logger.Int("v2_count", len(v2Caches)))
	}

	// Validate thresholds
	if m.thresholdRepo != nil {
		legacyThresholds, _ := m.legacyStore.GetAllDynamicThresholds()
		v2Thresholds, _ := m.thresholdRepo.GetAllDynamicThresholds(ctx)
		m.logger.Info("threshold validation",
			logger.Int("legacy_count", len(legacyThresholds)),
			logger.Int("v2_count", len(v2Thresholds)))
	}

	// Validate weather data
	if m.weatherRepo != nil {
		legacyDailyEvents, _ := m.legacyStore.GetAllDailyEvents()
		v2DailyEvents, _ := m.weatherRepo.GetAllDailyEvents(ctx)
		m.logger.Info("daily events validation",
			logger.Int("legacy_count", len(legacyDailyEvents)),
			logger.Int("v2_count", len(v2DailyEvents)))

		legacyHourlyWeather, _ := m.legacyStore.GetAllHourlyWeather()
		m.logger.Info("hourly weather validation",
			logger.Int("legacy_count", len(legacyHourlyWeather)))
	}

	return nil
}
