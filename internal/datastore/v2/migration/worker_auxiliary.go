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

// Auxiliary section names, shared by the result sections and the outcome summary.
const (
	auxSectionImageCache      = "image cache"
	auxSectionThreshold       = "threshold"
	auxSectionThresholdEvents = "threshold events"
	auxSectionNotifications   = "notifications"
	auxSectionWeather         = "weather"
)

// Sentinel errors recorded in a section's Error when rows that should have been
// copied were not.
var (
	// ErrAuxLabelNotResolved means a species label was missing after batch creation.
	ErrAuxLabelNotResolved = errors.NewStd("species label not resolved after batch creation")
	// ErrAuxDailyEventNotInV2 means a legacy daily event date is missing in v2.
	ErrAuxDailyEventNotInV2 = errors.NewStd("legacy daily event date not found in v2")
	// ErrAuxDependencyNotConfigured means the auxiliary migrator lacks a dependency.
	ErrAuxDependencyNotConfigured = errors.NewStd("auxiliary migrator dependency not configured")
)

// AuxiliaryMigrationResult tracks what was migrated, skipped and failed.
//
// Skipped counts legacy rows that cannot exist in v2. Failed counts rows that
// should have been copied and were not. Error is the first error of any kind for
// the section: it is set whenever a failed count rises or a fetch, label or read
// fails, so a nil Error means the section is complete.
type AuxiliaryMigrationResult struct {
	ImageCaches struct {
		Total    int
		Migrated int
		Skipped  int
		Failed   int
		Error    error
	}
	Thresholds struct {
		Total    int
		Migrated int
		Skipped  int
		Failed   int
		Error    error
	}
	ThresholdEvents struct {
		Total    int
		Migrated int
		Skipped  int
		Failed   int
		Error    error // A failed fetch leaves the event count unknown, so Failed stays 0
	}
	Notifications struct {
		Total    int
		Migrated int
		Skipped  int
		Failed   int
		Error    error
	}
	Weather struct {
		DailyEventsTotal      int
		DailyEventsMigrated   int
		DailyEventsFailed     int
		HourlyWeatherTotal    int
		HourlyWeatherMigrated int
		HourlyWeatherFailed   int
		Skipped               int
		Error                 error
	}
}

// auxSection pairs a section name with its migration stats for iteration.
type auxSection struct {
	name     string
	total    int
	migrated int
	skipped  int
	failed   int
	err      error
}

// sections returns the standard (non-weather) auxiliary migration sections.
func (r *AuxiliaryMigrationResult) sections() []auxSection {
	return []auxSection{
		{auxSectionImageCache, r.ImageCaches.Total, r.ImageCaches.Migrated, r.ImageCaches.Skipped, r.ImageCaches.Failed, r.ImageCaches.Error},
		{auxSectionThreshold, r.Thresholds.Total, r.Thresholds.Migrated, r.Thresholds.Skipped, r.Thresholds.Failed, r.Thresholds.Error},
		{auxSectionThresholdEvents, r.ThresholdEvents.Total, r.ThresholdEvents.Migrated, r.ThresholdEvents.Skipped, r.ThresholdEvents.Failed, r.ThresholdEvents.Error},
		{auxSectionNotifications, r.Notifications.Total, r.Notifications.Migrated, r.Notifications.Skipped, r.Notifications.Failed, r.Notifications.Error},
	}
}

// firstAuxError keeps the first error recorded for a section.
func firstAuxError(current, err error) error {
	if current != nil {
		return current
	}
	return err
}

// LogErrors logs a warning for each auxiliary section that encountered errors.
// Weather is handled separately because it tracks daily and hourly counts.
func (r *AuxiliaryMigrationResult) LogErrors(log logger.Logger) {
	if log == nil {
		return
	}
	for _, s := range r.sections() {
		if s.err != nil {
			log.Warn(s.name+" migration had errors",
				logger.Int("total", s.total),
				logger.Int("migrated", s.migrated),
				logger.Error(s.err))
		}
	}
	if r.Weather.Error != nil {
		log.Warn("weather migration had errors",
			logger.Int("daily_total", r.Weather.DailyEventsTotal),
			logger.Int("daily_migrated", r.Weather.DailyEventsMigrated),
			logger.Int("hourly_total", r.Weather.HourlyWeatherTotal),
			logger.Int("hourly_migrated", r.Weather.HourlyWeatherMigrated),
			logger.Error(r.Weather.Error))
	}
}

// HasErrors returns true if any section failed to copy rows that should have been
// copied, or failed to fetch, resolve or read the data it needs. Every such failure
// sets the section's Error.
func (r *AuxiliaryMigrationResult) HasErrors() bool {
	return r.ImageCaches.Error != nil ||
		r.Thresholds.Error != nil ||
		r.ThresholdEvents.Error != nil ||
		r.Notifications.Error != nil ||
		r.Weather.Error != nil
}

// Outcome returns the status to persist for this result and, when it failed, a
// summary with one part per failed section joined by "; ". Each part holds counts
// only, never error text.
func (r *AuxiliaryMigrationResult) Outcome() (status entities.AuxiliaryDataStatus, summary string) {
	if !r.HasErrors() {
		return entities.AuxiliaryDataStatusSucceeded, ""
	}
	var parts []string
	for _, sec := range r.sections() {
		if sec.err == nil {
			continue
		}
		parts = append(parts, auxSectionSummary(sec.name,
			fmt.Sprintf("%d of %d migrated", sec.migrated, sec.total), sec.failed))
	}
	if r.Weather.Error != nil {
		w := &r.Weather
		counts := fmt.Sprintf("daily events %d of %d migrated%s, hourly weather %d of %d migrated%s",
			w.DailyEventsMigrated, w.DailyEventsTotal, auxFailedSuffix(w.DailyEventsFailed),
			w.HourlyWeatherMigrated, w.HourlyWeatherTotal, auxFailedSuffix(w.HourlyWeatherFailed))
		parts = append(parts, fmt.Sprintf("%s: %s", auxSectionWeather, counts))
	}
	return entities.AuxiliaryDataStatusFailed, strings.Join(parts, "; ")
}

// auxFailedSuffix formats a failed count for the summary, omitting a zero count.
func auxFailedSuffix(failed int) string {
	if failed == 0 {
		return ""
	}
	return fmt.Sprintf(" (%d failed)", failed)
}

// auxSectionSummary formats one failed section of the outcome summary. It carries
// counts only: the summary is shown by the status API and the support dump, and a
// section error can name paths, hosts or users. The errors themselves are logged by
// LogErrors.
func auxSectionSummary(name, counts string, failed int) string {
	if failed == 0 {
		return fmt.Sprintf("%s: %s", name, counts)
	}
	return fmt.Sprintf("%s: %s, %d failed", name, counts, failed)
}

// Summary returns a human-readable summary of the migration.
func (r *AuxiliaryMigrationResult) Summary() string {
	var b strings.Builder
	b.WriteString("Auxiliary Migration Summary:\n")

	fmt.Fprintf(&b, "  Image Caches: %d/%d migrated, %d skipped, %d failed", r.ImageCaches.Migrated, r.ImageCaches.Total, r.ImageCaches.Skipped, r.ImageCaches.Failed)
	if r.ImageCaches.Error != nil {
		fmt.Fprintf(&b, " (error: %v)", r.ImageCaches.Error)
	}
	b.WriteString("\n")

	fmt.Fprintf(&b, "  Thresholds: %d/%d migrated, %d skipped, %d failed", r.Thresholds.Migrated, r.Thresholds.Total, r.Thresholds.Skipped, r.Thresholds.Failed)
	if r.Thresholds.Error != nil {
		fmt.Fprintf(&b, " (error: %v)", r.Thresholds.Error)
	}
	b.WriteString("\n")

	fmt.Fprintf(&b, "  Threshold Events: %d/%d migrated, %d skipped, %d failed", r.ThresholdEvents.Migrated, r.ThresholdEvents.Total, r.ThresholdEvents.Skipped, r.ThresholdEvents.Failed)
	if r.ThresholdEvents.Error != nil {
		fmt.Fprintf(&b, " (error: %v)", r.ThresholdEvents.Error)
	}
	b.WriteString("\n")

	fmt.Fprintf(&b, "  Notifications: %d/%d migrated, %d skipped, %d failed", r.Notifications.Migrated, r.Notifications.Total, r.Notifications.Skipped, r.Notifications.Failed)
	if r.Notifications.Error != nil {
		fmt.Fprintf(&b, " (error: %v)", r.Notifications.Error)
	}
	b.WriteString("\n")

	fmt.Fprintf(&b, "  Daily Events: %d/%d migrated, %d failed", r.Weather.DailyEventsMigrated, r.Weather.DailyEventsTotal, r.Weather.DailyEventsFailed)
	fmt.Fprintf(&b, ", Hourly Weather: %d/%d migrated, %d skipped, %d failed", r.Weather.HourlyWeatherMigrated, r.Weather.HourlyWeatherTotal, r.Weather.Skipped, r.Weather.HourlyWeatherFailed)
	if r.Weather.Error != nil {
		fmt.Fprintf(&b, " (error: %v)", r.Weather.Error)
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
// Returns a result struct with detailed migration statistics; its Outcome is what
// callers record. A missing dependency returns an error wrapping
// ErrAuxDependencyNotConfigured instead of silently skipping rows.
func (m *AuxiliaryMigrator) MigrateAll(ctx context.Context) (*AuxiliaryMigrationResult, error) {
	result := &AuxiliaryMigrationResult{}

	// Fail fast on a missing dependency: skipping would leave rows uncopied while
	// the caller records a clean outcome.
	for _, dep := range []struct {
		name       string
		configured bool
	}{
		{"legacy store", m.legacyStore != nil},
		{"label repository", m.labelRepo != nil},
		{"image cache repository", m.imageCacheRepo != nil},
		{"threshold repository", m.thresholdRepo != nil},
		{"notification repository", m.notificationRepo != nil},
		{"weather repository", m.weatherRepo != nil},
	} {
		if !dep.configured {
			return result, fmt.Errorf("%w: %s", ErrAuxDependencyNotConfigured, dep.name)
		}
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

	// Caller records result.Outcome()
	return result, nil
}

// migrateImageCaches migrates all image cache entries.
// Resolves scientific names (trimmed, as the label repository keys them) to label
// IDs for the normalized schema. Rows without a name are skipped; label and save
// failures are counted as failed.
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
	// Labels are keyed by the trimmed name and an empty name has no label.
	speciesSet := make(map[string]struct{})
	emptyNameRows := 0
	for i := range legacyCaches {
		if name := strings.TrimSpace(legacyCaches[i].ScientificName); name != "" {
			speciesSet[name] = struct{}{}
		} else {
			emptyNameRows++
		}
	}
	speciesNames := slices.Collect(maps.Keys(speciesSet))

	labelMap, err := m.labelRepo.BatchGetOrCreate(ctx, speciesNames, m.defaultModelID, m.speciesLabelTypeID, m.avesClassID)
	if err != nil {
		m.logger.Warn("failed to batch resolve labels for image caches", logger.Error(err))
		// The per-row loop below skips rows without a name; count them the same
		// way here, so only the other rows are failures.
		result.ImageCaches.Skipped += emptyNameRows
		result.ImageCaches.Failed += len(legacyCaches) - emptyNameRows
		result.ImageCaches.Error = firstAuxError(result.ImageCaches.Error, err)
		return
	}

	for i := range legacyCaches {
		cache := &legacyCaches[i]

		// A legacy row without a name cannot exist in v2: labels need a name.
		scientificName := strings.TrimSpace(cache.ScientificName)
		if scientificName == "" {
			m.logger.Warn("skipping legacy image cache with empty scientific name")
			result.ImageCaches.Skipped++
			continue
		}

		// Look up label from pre-resolved map
		label, ok := labelMap[scientificName]
		if !ok {
			m.logger.Warn("label not found after batch creation",
				logger.String("species", scientificName))
			result.ImageCaches.Failed++
			result.ImageCaches.Error = firstAuxError(result.ImageCaches.Error, ErrAuxLabelNotResolved)
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
				logger.String("species", scientificName),
				logger.Error(err))
			result.ImageCaches.Failed++
			result.ImageCaches.Error = firstAuxError(result.ImageCaches.Error, err)
			continue
		}
		result.ImageCaches.Migrated++
	}

	m.logger.Info("image cache migration completed",
		logger.Int("total", result.ImageCaches.Total),
		logger.Int("migrated", result.ImageCaches.Migrated),
		logger.Int("skipped", result.ImageCaches.Skipped),
		logger.Int("failed", result.ImageCaches.Failed))
}

// migrateDynamicThresholds migrates all dynamic thresholds and their events.
// Thresholds are keyed per species (lowercase common name) and model-independent
// (#4195), so this is a direct 1:1 copy from the legacy store; no label resolution.
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

	for i := range legacyThresholds {
		threshold := &legacyThresholds[i]
		speciesName := strings.ToLower(threshold.SpeciesName)
		if speciesName == "" {
			m.logger.Warn("skipping legacy threshold with empty species name")
			result.Thresholds.Skipped++
			continue
		}

		v2Threshold := entities.DynamicThreshold{
			SpeciesName:    speciesName,
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
				logger.String("species", speciesName),
				logger.Error(err))
			result.Thresholds.Failed++
			result.Thresholds.Error = firstAuxError(result.Thresholds.Error, err)
			continue
		}
		result.Thresholds.Migrated++

		// Migrate threshold events for this species
		m.migrateThresholdEvents(ctx, threshold.SpeciesName, result)
	}

	m.logger.Info("dynamic threshold migration completed",
		logger.Int("total", result.Thresholds.Total),
		logger.Int("migrated", result.Thresholds.Migrated),
		logger.Int("skipped", result.Thresholds.Skipped),
		logger.Int("failed", result.Thresholds.Failed))
}

// migrateThresholdEvents migrates threshold events for a species (1:1 copy, #4195).
func (m *AuxiliaryMigrator) migrateThresholdEvents(ctx context.Context, speciesName string, result *AuxiliaryMigrationResult) {
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

	lower := strings.ToLower(speciesName)
	if lower == "" {
		return
	}

	result.ThresholdEvents.Total += len(legacyEvents)
	for i := range legacyEvents {
		event := &legacyEvents[i]
		v2Event := &entities.ThresholdEvent{
			SpeciesName:    lower,
			ScientificName: event.ScientificName,
			PreviousLevel:  event.PreviousLevel,
			NewLevel:       event.NewLevel,
			PreviousValue:  event.PreviousValue,
			NewValue:       event.NewValue,
			ChangeReason:   event.ChangeReason,
			Confidence:     event.Confidence,
			CreatedAt:      event.CreatedAt,
		}
		if err := m.thresholdRepo.SaveThresholdEvent(ctx, v2Event); err != nil {
			m.logger.Warn("failed to migrate threshold event",
				logger.String("species", speciesName),
				logger.Error(err))
			result.ThresholdEvents.Failed++
			result.ThresholdEvents.Error = firstAuxError(result.ThresholdEvents.Error, err)
			continue
		}
		result.ThresholdEvents.Migrated++
	}
}

// migrateNotificationHistory migrates notification history.
// Resolves scientific names (trimmed, as the label repository keys them) to label
// IDs for the normalized schema. Rows without a name are skipped; label and save
// failures are counted as failed.
func (m *AuxiliaryMigrator) migrateNotificationHistory(ctx context.Context, result *AuxiliaryMigrationResult) {
	if m.notificationRepo == nil {
		m.logger.Debug("notification repo not configured, skipping")
		return
	}

	// Get active notification history (not expired)
	legacyHistory, err := m.legacyStore.GetActiveNotificationHistory(ctx, time.Now())
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
	// Labels are keyed by the trimmed name and an empty name has no label.
	speciesSet := make(map[string]struct{})
	emptyNameRows := 0
	for i := range legacyHistory {
		if name := strings.TrimSpace(legacyHistory[i].ScientificName); name != "" {
			speciesSet[name] = struct{}{}
		} else {
			emptyNameRows++
		}
	}
	speciesNames := slices.Collect(maps.Keys(speciesSet))

	labelMap, err := m.labelRepo.BatchGetOrCreate(ctx, speciesNames, m.defaultModelID, m.speciesLabelTypeID, m.avesClassID)
	if err != nil {
		m.logger.Warn("failed to batch resolve labels for notification history", logger.Error(err))
		// The per-row loop below skips rows without a name; count them the same
		// way here, so only the other rows are failures.
		result.Notifications.Skipped += emptyNameRows
		result.Notifications.Failed += len(legacyHistory) - emptyNameRows
		result.Notifications.Error = firstAuxError(result.Notifications.Error, err)
		return
	}

	for i := range legacyHistory {
		history := &legacyHistory[i]

		// A legacy row without a name cannot exist in v2: labels need a name.
		scientificName := strings.TrimSpace(history.ScientificName)
		if scientificName == "" {
			m.logger.Warn("skipping legacy notification history with empty scientific name")
			result.Notifications.Skipped++
			continue
		}

		// Look up label from pre-resolved map
		label, ok := labelMap[scientificName]
		if !ok {
			m.logger.Warn("label not found after batch creation",
				logger.String("species", scientificName))
			result.Notifications.Failed++
			result.Notifications.Error = firstAuxError(result.Notifications.Error, ErrAuxLabelNotResolved)
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
				logger.String("species", scientificName),
				logger.Error(err))
			result.Notifications.Failed++
			result.Notifications.Error = firstAuxError(result.Notifications.Error, err)
			continue
		}
		result.Notifications.Migrated++
	}

	m.logger.Info("notification history migration completed",
		logger.Int("total", result.Notifications.Total),
		logger.Int("migrated", result.Notifications.Migrated),
		logger.Int("skipped", result.Notifications.Skipped),
		logger.Int("failed", result.Notifications.Failed))
}

// migrateWeatherData migrates DailyEvents and HourlyWeather from legacy to v2.
// Handles ID remapping since V2 may have different IDs for the same dates.
// Hourly rows whose legacy parent does not exist are skipped as orphans; a legacy
// date missing in v2 or a failed save or read is counted as failed.
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
		// Without daily events every hourly row is an orphan and cannot exist in v2.
		result.Weather.Skipped = len(legacyWeather)
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
		result.Weather.DailyEventsFailed += max(result.Weather.DailyEventsTotal-migrated, 0)
		result.Weather.Error = firstAuxError(result.Weather.Error, err)
	}
	result.Weather.DailyEventsMigrated = migrated
	m.logger.Info("daily events migration completed",
		logger.Int("total", result.Weather.DailyEventsTotal),
		logger.Int("migrated", result.Weather.DailyEventsMigrated))

	// Step 4: Build Date -> V2 ID mapping (after migration)
	v2EventsAll, err := m.weatherRepo.GetAllDailyEvents(ctx)
	if err != nil {
		m.logger.Warn("failed to get v2 daily events for ID mapping", logger.Error(err))
		// Without the mapping no hourly row can be copied. The per-row loop below
		// skips orphan rows, whose parent is absent from legacy; count them the same
		// way here, so only rows with a known parent are failures.
		for i := range legacyWeather {
			if _, known := legacyIDToDate[legacyWeather[i].DailyEventsID]; known {
				result.Weather.HourlyWeatherFailed++
			} else {
				result.Weather.Skipped++
			}
		}
		result.Weather.Error = firstAuxError(result.Weather.Error,
			fmt.Errorf("read v2 daily events for ID mapping: %w", err))
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
			continue // Orphan record: no such parent in legacy, cannot exist in v2
		}
		v2ID, ok := dateToV2ID[date]
		if !ok {
			// The daily event was saved with ON CONFLICT DO NOTHING, so a legacy
			// date missing in v2 means its save or read failed.
			result.Weather.HourlyWeatherFailed++
			result.Weather.Error = firstAuxError(result.Weather.Error, ErrAuxDailyEventNotInV2)
			continue
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
		result.Weather.HourlyWeatherFailed += max(len(v2Weather)-migratedWeather, 0)
		result.Weather.Error = firstAuxError(result.Weather.Error, err)
	}
	result.Weather.HourlyWeatherMigrated = migratedWeather

	m.logger.Info("hourly weather migration completed",
		logger.Int("total", result.Weather.HourlyWeatherTotal),
		logger.Int("migrated", result.Weather.HourlyWeatherMigrated),
		logger.Int("skipped", result.Weather.Skipped),
		logger.Int("failed", result.Weather.HourlyWeatherFailed))
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
