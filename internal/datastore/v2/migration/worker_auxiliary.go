package migration

import (
	"context"
	"fmt"
	"maps"
	"slices"
	"strings"
	"time"

	"github.com/tphakala/birdnet-go/internal/datastore"
	"github.com/tphakala/birdnet-go/internal/datastore/v2/entities"
	"github.com/tphakala/birdnet-go/internal/datastore/v2/repository"
	"github.com/tphakala/birdnet-go/internal/errors"
	"github.com/tphakala/birdnet-go/internal/logger"
)

// AuxiliaryMigrationResult tracks what was migrated and what was skipped.
type AuxiliaryMigrationResult struct {
	ImageCaches struct {
		Total    int
		Migrated int
		Skipped  int
		Error    error // Non-nil if fetch from legacy failed
	}
	Thresholds struct {
		Total    int
		Migrated int
		Skipped  int
		Error    error
	}
	ThresholdEvents struct {
		Total    int
		Migrated int
		Skipped  int
		Error    error // Non-nil if fetch from legacy failed
	}
	Notifications struct {
		Total    int
		Migrated int
		Skipped  int
		Error    error
	}
	Weather struct {
		DailyEventsTotal      int
		DailyEventsMigrated   int
		HourlyWeatherTotal    int
		HourlyWeatherMigrated int
		Skipped               int
		Error                 error
	}
}

// HasErrors returns true if any migration step encountered errors fetching legacy data.
func (r *AuxiliaryMigrationResult) HasErrors() bool {
	return r.ImageCaches.Error != nil ||
		r.Thresholds.Error != nil ||
		r.ThresholdEvents.Error != nil ||
		r.Notifications.Error != nil ||
		r.Weather.Error != nil
}

// Summary returns a human-readable summary of the migration.
func (r *AuxiliaryMigrationResult) Summary() string {
	var b strings.Builder
	b.WriteString("Auxiliary Migration Summary:\n")

	fmt.Fprintf(&b, "  Image Caches: %d/%d migrated", r.ImageCaches.Migrated, r.ImageCaches.Total)
	if r.ImageCaches.Error != nil {
		fmt.Fprintf(&b, " (fetch error: %v)", r.ImageCaches.Error)
	}
	b.WriteString("\n")

	fmt.Fprintf(&b, "  Thresholds: %d/%d migrated", r.Thresholds.Migrated, r.Thresholds.Total)
	if r.Thresholds.Error != nil {
		fmt.Fprintf(&b, " (fetch error: %v)", r.Thresholds.Error)
	}
	b.WriteString("\n")

	fmt.Fprintf(&b, "  Threshold Events: %d/%d migrated", r.ThresholdEvents.Migrated, r.ThresholdEvents.Total)
	if r.ThresholdEvents.Error != nil {
		fmt.Fprintf(&b, " (fetch error: %v)", r.ThresholdEvents.Error)
	}
	b.WriteString("\n")

	fmt.Fprintf(&b, "  Notifications: %d/%d migrated", r.Notifications.Migrated, r.Notifications.Total)
	if r.Notifications.Error != nil {
		fmt.Fprintf(&b, " (fetch error: %v)", r.Notifications.Error)
	}
	b.WriteString("\n")

	fmt.Fprintf(&b, "  Daily Events: %d/%d migrated", r.Weather.DailyEventsMigrated, r.Weather.DailyEventsTotal)
	fmt.Fprintf(&b, ", Hourly Weather: %d/%d migrated", r.Weather.HourlyWeatherMigrated, r.Weather.HourlyWeatherTotal)
	if r.Weather.Error != nil {
		fmt.Fprintf(&b, " (fetch error: %v)", r.Weather.Error)
	}
	b.WriteString("\n")

	return b.String()
}

// AuxiliaryMigrator handles migration of auxiliary tables (weather, image cache, thresholds, etc.)
// These tables are independent of detections and can be migrated in bulk.
type AuxiliaryMigrator struct {
	legacyStore      datastore.Interface
	labelRepo        repository.LabelRepository
	weatherRepo      repository.WeatherRepository
	imageCacheRepo   repository.ImageCacheRepository
	thresholdRepo    repository.DynamicThresholdRepository
	notificationRepo repository.NotificationHistoryRepository
	logger           logger.Logger

	// Cached lookup table IDs for label creation
	defaultModelID     uint  // Model ID to use for migrated labels
	speciesLabelTypeID uint  // "species" label type ID
	avesClassID        *uint // "Aves" taxonomic class ID (optional)
}

// AuxiliaryMigratorConfig configures the auxiliary migrator.
type AuxiliaryMigratorConfig struct {
	LegacyStore      datastore.Interface
	LabelRepo        repository.LabelRepository
	WeatherRepo      repository.WeatherRepository
	ImageCacheRepo   repository.ImageCacheRepository
	ThresholdRepo    repository.DynamicThresholdRepository
	NotificationRepo repository.NotificationHistoryRepository
	Logger           logger.Logger

	// Required: Cached lookup table IDs
	DefaultModelID     uint  // Model ID to use for migrated labels (typically default BirdNET)
	SpeciesLabelTypeID uint  // "species" label type ID
	AvesClassID        *uint // "Aves" taxonomic class ID (optional)
}

// NewAuxiliaryMigrator creates a new auxiliary migrator.
func NewAuxiliaryMigrator(cfg *AuxiliaryMigratorConfig) *AuxiliaryMigrator {
	return &AuxiliaryMigrator{
		legacyStore:        cfg.LegacyStore,
		labelRepo:          cfg.LabelRepo,
		weatherRepo:        cfg.WeatherRepo,
		imageCacheRepo:     cfg.ImageCacheRepo,
		thresholdRepo:      cfg.ThresholdRepo,
		notificationRepo:   cfg.NotificationRepo,
		logger:             cfg.Logger,
		defaultModelID:     cfg.DefaultModelID,
		speciesLabelTypeID: cfg.SpeciesLabelTypeID,
		avesClassID:        cfg.AvesClassID,
	}
}

// MigrateAll migrates all auxiliary tables from legacy to v2.
// This should be called once during the migration process.
// Returns a result struct with detailed migration statistics.
func (m *AuxiliaryMigrator) MigrateAll(ctx context.Context) (*AuxiliaryMigrationResult, error) {
	result := &AuxiliaryMigrationResult{}

	if m.legacyStore == nil {
		m.logger.Debug("no legacy store provided, skipping auxiliary migration")
		return result, nil
	}

	// Fail fast if label repo is not configured - it's required for migration
	if m.labelRepo == nil {
		return result, errors.NewStd("label repository not configured")
	}

	m.logger.Info("starting auxiliary table migration")

	// Migrate each table, collecting results
	m.migrateImageCaches(ctx, result)
	m.migrateDynamicThresholds(ctx, result)
	m.migrateNotificationHistory(ctx, result)
	m.migrateWeatherData(ctx, result)

	// Log comprehensive summary with structured fields
	m.logger.Info("auxiliary migration completed",
		logger.Int("image_caches_total", result.ImageCaches.Total),
		logger.Int("image_caches_migrated", result.ImageCaches.Migrated),
		logger.Int("thresholds_total", result.Thresholds.Total),
		logger.Int("thresholds_migrated", result.Thresholds.Migrated),
		logger.Int("threshold_events_total", result.ThresholdEvents.Total),
		logger.Int("threshold_events_migrated", result.ThresholdEvents.Migrated),
		logger.Int("notifications_total", result.Notifications.Total),
		logger.Int("notifications_migrated", result.Notifications.Migrated),
		logger.Int("daily_events_total", result.Weather.DailyEventsTotal),
		logger.Int("daily_events_migrated", result.Weather.DailyEventsMigrated),
		logger.Int("hourly_weather_total", result.Weather.HourlyWeatherTotal),
		logger.Int("hourly_weather_migrated", result.Weather.HourlyWeatherMigrated))

	// Caller can inspect result.HasErrors() to decide if this is acceptable
	return result, nil
}

// migrateImageCaches migrates all image cache entries.
// Resolves scientific names to label IDs for the normalized schema.
func (m *AuxiliaryMigrator) migrateImageCaches(ctx context.Context, result *AuxiliaryMigrationResult) {
	if m.imageCacheRepo == nil {
		m.logger.Debug("image cache repo not configured, skipping")
		return
	}

	// Get all image caches from legacy (wikimedia provider is default)
	legacyCaches, err := m.legacyStore.GetAllImageCaches("wikimedia")
	if err != nil {
		m.logger.Warn("failed to get legacy image caches", logger.Error(err))
		result.ImageCaches.Error = err
		return
	}

	result.ImageCaches.Total = len(legacyCaches)
	if len(legacyCaches) == 0 {
		m.logger.Debug("no image caches to migrate")
		return
	}

	// Batch resolve all labels to avoid N+1 queries
	speciesSet := make(map[string]struct{})
	for i := range legacyCaches {
		speciesSet[legacyCaches[i].ScientificName] = struct{}{}
	}
	speciesNames := slices.Collect(maps.Keys(speciesSet))

	labelMap, err := m.labelRepo.BatchGetOrCreate(ctx, speciesNames, m.defaultModelID, m.speciesLabelTypeID, m.avesClassID)
	if err != nil {
		m.logger.Warn("failed to batch resolve labels for image caches", logger.Error(err))
		result.ImageCaches.Error = err
		return
	}

	for i := range legacyCaches {
		cache := &legacyCaches[i]

		// Look up label from pre-resolved map
		label, ok := labelMap[cache.ScientificName]
		if !ok {
			m.logger.Warn("label not found after batch creation",
				logger.String("species", cache.ScientificName))
			result.ImageCaches.Skipped++
			continue
		}

		v2Cache := &entities.ImageCache{
			ProviderName:   cache.ProviderName,
			LabelID:        label.ID,
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
			result.ImageCaches.Skipped++
			continue
		}
		result.ImageCaches.Migrated++
	}

	m.logger.Info("image cache migration completed",
		logger.Int("total", result.ImageCaches.Total),
		logger.Int("migrated", result.ImageCaches.Migrated),
		logger.Int("skipped", result.ImageCaches.Skipped))
}

// migrateDynamicThresholds migrates all dynamic thresholds and their events.
// Resolves scientific names to label IDs for the normalized schema.
func (m *AuxiliaryMigrator) migrateDynamicThresholds(ctx context.Context, result *AuxiliaryMigrationResult) {
	if m.thresholdRepo == nil {
		m.logger.Debug("threshold repo not configured, skipping")
		return
	}

	// Get all thresholds from legacy
	legacyThresholds, err := m.legacyStore.GetAllDynamicThresholds()
	if err != nil {
		m.logger.Warn("failed to get legacy thresholds", logger.Error(err))
		result.Thresholds.Error = err
		return
	}

	result.Thresholds.Total = len(legacyThresholds)
	if len(legacyThresholds) == 0 {
		m.logger.Debug("no dynamic thresholds to migrate")
		return
	}

	// Batch resolve all labels to avoid N+1 queries
	speciesSet := make(map[string]struct{})
	for i := range legacyThresholds {
		speciesSet[legacyThresholds[i].ScientificName] = struct{}{}
	}
	speciesNames := slices.Collect(maps.Keys(speciesSet))

	labelMap, err := m.labelRepo.BatchGetOrCreate(ctx, speciesNames, m.defaultModelID, m.speciesLabelTypeID, m.avesClassID)
	if err != nil {
		m.logger.Warn("failed to batch resolve labels for thresholds", logger.Error(err))
		result.Thresholds.Error = err
		return
	}

	for i := range legacyThresholds {
		threshold := &legacyThresholds[i]

		// Look up label from pre-resolved map
		label, ok := labelMap[threshold.ScientificName]
		if !ok {
			m.logger.Warn("label not found after batch creation",
				logger.String("species", threshold.SpeciesName),
				logger.String("scientific_name", threshold.ScientificName))
			result.Thresholds.Skipped++
			continue
		}

		v2Threshold := entities.DynamicThreshold{
			LabelID:       label.ID,
			Level:         threshold.Level,
			CurrentValue:  threshold.CurrentValue,
			BaseThreshold: threshold.BaseThreshold,
			HighConfCount: threshold.HighConfCount,
			ValidHours:    threshold.ValidHours,
			ExpiresAt:     threshold.ExpiresAt,
			LastTriggered: threshold.LastTriggered,
			FirstCreated:  threshold.FirstCreated,
			TriggerCount:  threshold.TriggerCount,
		}
		if err := m.thresholdRepo.SaveDynamicThreshold(ctx, &v2Threshold); err != nil {
			m.logger.Warn("failed to migrate threshold",
				logger.String("species", threshold.SpeciesName),
				logger.Error(err))
			result.Thresholds.Skipped++
			continue
		}
		result.Thresholds.Migrated++

		// Migrate threshold events for this species using the resolved label ID
		m.migrateThresholdEvents(ctx, threshold.SpeciesName, label.ID, result)
	}

	m.logger.Info("dynamic threshold migration completed",
		logger.Int("total", result.Thresholds.Total),
		logger.Int("migrated", result.Thresholds.Migrated),
		logger.Int("skipped", result.Thresholds.Skipped))
}

// migrateThresholdEvents migrates threshold events for a species.
// Uses the pre-resolved labelID to avoid repeated label lookups.
func (m *AuxiliaryMigrator) migrateThresholdEvents(ctx context.Context, speciesName string, labelID uint, result *AuxiliaryMigrationResult) {
	// Get events from legacy (limit to 100 most recent)
	legacyEvents, err := m.legacyStore.GetThresholdEvents(speciesName, 100)
	if err != nil {
		m.logger.Warn("failed to get threshold events",
			logger.String("species", speciesName),
			logger.Error(err))
		// Record the first fetch error encountered
		if result.ThresholdEvents.Error == nil {
			result.ThresholdEvents.Error = err
		}
		return
	}

	result.ThresholdEvents.Total += len(legacyEvents)
	for i := range legacyEvents {
		event := &legacyEvents[i]
		v2Event := &entities.ThresholdEvent{
			LabelID:       labelID,
			PreviousLevel: event.PreviousLevel,
			NewLevel:      event.NewLevel,
			PreviousValue: event.PreviousValue,
			NewValue:      event.NewValue,
			ChangeReason:  event.ChangeReason,
			Confidence:    event.Confidence,
			CreatedAt:     event.CreatedAt,
		}
		if err := m.thresholdRepo.SaveThresholdEvent(ctx, v2Event); err != nil {
			m.logger.Warn("failed to migrate threshold event",
				logger.String("species", speciesName),
				logger.Error(err))
			result.ThresholdEvents.Skipped++
			continue
		}
		result.ThresholdEvents.Migrated++
	}
}

// migrateNotificationHistory migrates notification history.
// Resolves scientific names to label IDs for the normalized schema.
func (m *AuxiliaryMigrator) migrateNotificationHistory(ctx context.Context, result *AuxiliaryMigrationResult) {
	if m.notificationRepo == nil {
		m.logger.Debug("notification repo not configured, skipping")
		return
	}

	// Get active notification history (not expired)
	legacyHistory, err := m.legacyStore.GetActiveNotificationHistory(time.Now())
	if err != nil {
		m.logger.Warn("failed to get legacy notification history", logger.Error(err))
		result.Notifications.Error = err
		return
	}

	result.Notifications.Total = len(legacyHistory)
	if len(legacyHistory) == 0 {
		m.logger.Debug("no notification history to migrate")
		return
	}

	// Batch resolve all labels to avoid N+1 queries
	speciesSet := make(map[string]struct{})
	for i := range legacyHistory {
		speciesSet[legacyHistory[i].ScientificName] = struct{}{}
	}
	speciesNames := slices.Collect(maps.Keys(speciesSet))

	labelMap, err := m.labelRepo.BatchGetOrCreate(ctx, speciesNames, m.defaultModelID, m.speciesLabelTypeID, m.avesClassID)
	if err != nil {
		m.logger.Warn("failed to batch resolve labels for notification history", logger.Error(err))
		result.Notifications.Error = err
		return
	}

	for i := range legacyHistory {
		history := &legacyHistory[i]

		// Look up label from pre-resolved map
		label, ok := labelMap[history.ScientificName]
		if !ok {
			m.logger.Warn("label not found after batch creation",
				logger.String("species", history.ScientificName))
			result.Notifications.Skipped++
			continue
		}

		v2History := &entities.NotificationHistory{
			LabelID:          label.ID,
			NotificationType: history.NotificationType,
			LastSent:         history.LastSent,
			ExpiresAt:        history.ExpiresAt,
		}
		if err := m.notificationRepo.SaveNotificationHistory(ctx, v2History); err != nil {
			m.logger.Warn("failed to migrate notification history",
				logger.String("species", history.ScientificName),
				logger.Error(err))
			result.Notifications.Skipped++
			continue
		}
		result.Notifications.Migrated++
	}

	m.logger.Info("notification history migration completed",
		logger.Int("total", result.Notifications.Total),
		logger.Int("migrated", result.Notifications.Migrated),
		logger.Int("skipped", result.Notifications.Skipped))
}

// migrateWeatherData migrates DailyEvents and HourlyWeather from legacy to v2.
// Handles ID remapping since V2 may have different IDs for the same dates.
func (m *AuxiliaryMigrator) migrateWeatherData(ctx context.Context, result *AuxiliaryMigrationResult) {
	if m.weatherRepo == nil {
		m.logger.Debug("weather repo not configured, skipping")
		return
	}

	// Step 1: Get legacy data
	legacyEvents, err := m.legacyStore.GetAllDailyEvents()
	if err != nil {
		m.logger.Warn("failed to get legacy daily events", logger.Error(err))
		result.Weather.Error = err
		return
	}

	legacyWeather, err := m.legacyStore.GetAllHourlyWeather()
	if err != nil {
		m.logger.Warn("failed to get legacy hourly weather", logger.Error(err))
		result.Weather.Error = err
		return
	}

	result.Weather.DailyEventsTotal = len(legacyEvents)
	result.Weather.HourlyWeatherTotal = len(legacyWeather)

	if len(legacyEvents) == 0 {
		m.logger.Debug("no weather data to migrate")
		return
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
		// Record first save error for HasErrors() to report
		if result.Weather.Error == nil {
			result.Weather.Error = err
		}
	}
	result.Weather.DailyEventsMigrated = migrated
	m.logger.Info("daily events migration completed",
		logger.Int("total", result.Weather.DailyEventsTotal),
		logger.Int("migrated", result.Weather.DailyEventsMigrated))

	// Step 4: Build Date -> V2 ID mapping (after migration)
	v2EventsAll, err := m.weatherRepo.GetAllDailyEvents(ctx)
	if err != nil {
		m.logger.Warn("failed to get v2 daily events for ID mapping", logger.Error(err))
		return
	}

	dateToV2ID := make(map[string]uint, len(v2EventsAll))
	for _, event := range v2EventsAll {
		dateToV2ID[event.Date] = event.ID
	}

	// Step 5: Migrate HourlyWeather with ID remapping
	if len(legacyWeather) == 0 {
		m.logger.Debug("no hourly weather to migrate")
		return
	}

	v2Weather := make([]entities.HourlyWeather, 0, len(legacyWeather))
	for i := range legacyWeather {
		w := &legacyWeather[i]
		// Lookup: LegacyID -> Date -> V2ID
		date, ok := legacyIDToDate[w.DailyEventsID]
		if !ok {
			result.Weather.Skipped++
			continue // Orphan record, skip
		}
		v2ID, ok := dateToV2ID[date]
		if !ok {
			result.Weather.Skipped++
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
		// Record first save error for HasErrors() to report
		if result.Weather.Error == nil {
			result.Weather.Error = err
		}
	}
	result.Weather.HourlyWeatherMigrated = migratedWeather

	m.logger.Info("hourly weather migration completed",
		logger.Int("total", result.Weather.HourlyWeatherTotal),
		logger.Int("migrated", result.Weather.HourlyWeatherMigrated),
		logger.Int("skipped", result.Weather.Skipped))
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
