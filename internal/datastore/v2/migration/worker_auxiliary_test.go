package migration

import (
	"bytes"
	"context"
	"path/filepath"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/require"
	"github.com/tphakala/birdnet-go/internal/datastore"
	"github.com/tphakala/birdnet-go/internal/datastore/mocks"
	datastoreV2 "github.com/tphakala/birdnet-go/internal/datastore/v2"
	"github.com/tphakala/birdnet-go/internal/datastore/v2/entities"
	"github.com/tphakala/birdnet-go/internal/datastore/v2/repository"
	"github.com/tphakala/birdnet-go/internal/detection"
	"github.com/tphakala/birdnet-go/internal/errors"
	"github.com/tphakala/birdnet-go/internal/logger"
	"gorm.io/gorm"
)

// capturingLogger returns a logger that writes to a buffer for assertion.
func capturingLogger() (logger.Logger, *bytes.Buffer) {
	var buf bytes.Buffer
	return logger.NewSlogLogger(&buf, logger.LogLevelTrace, time.UTC), &buf
}

func TestAuxiliaryMigrationResult_LogErrors(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name        string
		setup       func(r *AuxiliaryMigrationResult)
		wantPhrases []string // substrings expected in output
		wantAbsent  []string // substrings that must NOT appear
	}{
		{
			name:  "no errors produces no warnings",
			setup: func(_ *AuxiliaryMigrationResult) {},
			wantAbsent: []string{
				"migration had errors",
			},
		},
		{
			name: "single section error logs only that section",
			setup: func(r *AuxiliaryMigrationResult) {
				r.Thresholds.Total = 10
				r.Thresholds.Migrated = 7
				r.Thresholds.Error = errors.NewStd("threshold fetch failed")
			},
			wantPhrases: []string{
				"threshold migration had errors",
				"threshold fetch failed",
			},
			wantAbsent: []string{
				"image cache migration had errors",
				"weather migration had errors",
			},
		},
		{
			name: "all sections with errors logs every section",
			setup: func(r *AuxiliaryMigrationResult) {
				r.ImageCaches.Error = errors.NewStd("img err")
				r.Thresholds.Error = errors.NewStd("thresh err")
				r.ThresholdEvents.Error = errors.NewStd("events err")
				r.Notifications.Error = errors.NewStd("notif err")
				r.Weather.Error = errors.NewStd("weather err")
			},
			wantPhrases: []string{
				"image cache migration had errors",
				"threshold migration had errors",
				"threshold events migration had errors",
				"notifications migration had errors",
				"weather migration had errors",
			},
		},
		{
			name: "weather error includes daily and hourly counts",
			setup: func(r *AuxiliaryMigrationResult) {
				r.Weather.DailyEventsTotal = 30
				r.Weather.DailyEventsMigrated = 28
				r.Weather.HourlyWeatherTotal = 720
				r.Weather.HourlyWeatherMigrated = 700
				r.Weather.Error = errors.NewStd("partial weather failure")
			},
			wantPhrases: []string{
				"weather migration had errors",
				"partial weather failure",
			},
			wantAbsent: []string{
				"image cache migration had errors",
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			result := &AuxiliaryMigrationResult{}
			tt.setup(result)

			log, buf := capturingLogger()
			result.LogErrors(log)

			output := buf.String()
			for _, phrase := range tt.wantPhrases {
				assert.Contains(t, output, phrase, "expected log output to contain %q", phrase)
			}
			for _, phrase := range tt.wantAbsent {
				assert.NotContains(t, output, phrase, "expected log output NOT to contain %q", phrase)
			}
		})
	}
}

func TestAuxiliaryMigrationResult_Sections(t *testing.T) {
	t.Parallel()

	r := &AuxiliaryMigrationResult{}
	r.ImageCaches.Total = 1
	r.Thresholds.Total = 2
	r.ThresholdEvents.Total = 3
	r.Notifications.Total = 4

	sections := r.sections()
	assert.Len(t, sections, 4, "expected 4 standard sections")

	expectedNames := []string{"image cache", "threshold", "threshold events", "notifications"}
	for i, s := range sections {
		assert.Equal(t, expectedNames[i], s.name)
		assert.Equal(t, i+1, s.total)
	}
}

// auxV2Env is a real v2 SQLite database with the repositories the auxiliary
// migrator writes to.
type auxV2Env struct {
	t                *testing.T
	db               *gorm.DB
	labelRepo        repository.LabelRepository
	weatherRepo      repository.WeatherRepository
	imageCacheRepo   repository.ImageCacheRepository
	thresholdRepo    repository.DynamicThresholdRepository
	notificationRepo repository.NotificationHistoryRepository
	modelID          uint
	labelTypeID      uint
	avesClassID      *uint
}

// newAuxV2Env creates a v2 database in t.TempDir() with real repositories.
func newAuxV2Env(t *testing.T) *auxV2Env {
	t.Helper()

	mgr, err := datastoreV2.NewSQLiteManager(datastoreV2.Config{ConfiguredPath: filepath.Join(t.TempDir(), "birdnet.db")})
	require.NoError(t, err)
	require.NoError(t, mgr.Initialize())
	t.Cleanup(func() { _ = mgr.Close() })

	db := mgr.DB()
	var labelType entities.LabelType
	require.NoError(t, db.FirstOrCreate(&labelType, entities.LabelType{Name: "species"}).Error)
	var aves entities.TaxonomicClass
	require.NoError(t, db.FirstOrCreate(&aves, entities.TaxonomicClass{Name: "Aves"}).Error)
	var model entities.AIModel
	require.NoError(t, db.FirstOrCreate(&model, entities.AIModel{
		Name:      detection.DefaultModelName,
		Version:   detection.DefaultModelVersion,
		Variant:   detection.DefaultModelVariant,
		ModelType: entities.ModelTypeBird,
	}).Error)

	labelRepo := repository.NewLabelRepository(db, nil, false, false)
	return &auxV2Env{
		t:                t,
		db:               db,
		labelRepo:        labelRepo,
		weatherRepo:      repository.NewWeatherRepository(db, nil, false, false),
		imageCacheRepo:   repository.NewImageCacheRepository(db, nil, labelRepo, false, false),
		thresholdRepo:    repository.NewDynamicThresholdRepository(db, nil, false, false),
		notificationRepo: repository.NewNotificationHistoryRepository(db, nil, labelRepo, false, false),
		modelID:          model.ID,
		labelTypeID:      labelType.ID,
		avesClassID:      &aves.ID,
	}
}

// config returns a migrator config that uses the environment's repositories.
func (e *auxV2Env) config(store datastore.Interface) *AuxiliaryMigratorConfig {
	return &AuxiliaryMigratorConfig{
		LegacyStore:        store,
		LabelRepo:          e.labelRepo,
		WeatherRepo:        e.weatherRepo,
		ImageCacheRepo:     e.imageCacheRepo,
		ThresholdRepo:      e.thresholdRepo,
		NotificationRepo:   e.notificationRepo,
		Logger:             testLogger(),
		DefaultModelID:     e.modelID,
		SpeciesLabelTypeID: e.labelTypeID,
		AvesClassID:        e.avesClassID,
	}
}

// dropTable removes a v2 table so that the next write to it fails.
func (e *auxV2Env) dropTable(model any) {
	e.t.Helper()
	require.NoError(e.t, e.db.Migrator().DropTable(model))
}

// labelMissDecorator wraps a label repository and omits one name from the
// BatchGetOrCreate result, as if the label had not been resolved.
type labelMissDecorator struct {
	repository.LabelRepository
	omit string
}

func (d labelMissDecorator) BatchGetOrCreate(ctx context.Context, names []string, modelID, labelTypeID uint, classID *uint) (map[string]*entities.Label, error) {
	labels, err := d.LabelRepository.BatchGetOrCreate(ctx, names, modelID, labelTypeID, classID)
	delete(labels, d.omit)
	return labels, err
}

const (
	auxTestSpeciesImage        = "Parus major"
	auxTestSpeciesNotification = "Corvus corax"
	auxTestSpeciesThreshold    = "great tit"
)

// auxLegacyRows is the legacy content one auxiliary test row serves.
type auxLegacyRows struct {
	imageCaches   []datastore.ImageCache
	thresholds    []datastore.DynamicThreshold
	events        []datastore.ThresholdEvent
	notifications []datastore.NotificationHistory
}

// defaultAuxLegacyRows returns one valid row per auxiliary section.
func defaultAuxLegacyRows() *auxLegacyRows {
	now := time.Now()
	return &auxLegacyRows{
		imageCaches: []datastore.ImageCache{{ProviderName: "wikimedia", ScientificName: auxTestSpeciesImage, SourceProvider: "wikimedia", CachedAt: now}},
		thresholds: []datastore.DynamicThreshold{{
			SpeciesName: auxTestSpeciesThreshold, ScientificName: auxTestSpeciesImage,
			ExpiresAt: now.Add(time.Hour), LastTriggered: now, FirstCreated: now,
		}},
		events:        []datastore.ThresholdEvent{{SpeciesName: auxTestSpeciesThreshold, ChangeReason: "high_confidence", CreatedAt: now}},
		notifications: []datastore.NotificationHistory{{ScientificName: auxTestSpeciesNotification, NotificationType: "new_species", LastSent: now, ExpiresAt: now.Add(time.Hour)}},
	}
}

// expectLegacy sets the legacy store expectations for rows. Threshold events
// are fetched once per saved threshold, so eventFetches says how many.
func expectLegacy(t *testing.T, rows *auxLegacyRows, eventFetches int) *mocks.MockInterface {
	t.Helper()
	store := mocks.NewMockInterface(t)
	store.EXPECT().GetAllImageCaches("wikimedia").Return(rows.imageCaches, nil)
	store.EXPECT().GetAllDynamicThresholds().Return(rows.thresholds, nil)
	store.EXPECT().GetActiveNotificationHistory(mock.Anything, mock.Anything).Return(rows.notifications, nil)
	store.EXPECT().GetAllDailyEvents().Return(nil, nil)
	store.EXPECT().GetAllHourlyWeather().Return(nil, nil)
	if eventFetches > 0 {
		store.EXPECT().GetThresholdEvents(mock.Anything, 100).Return(rows.events, nil).Times(eventFetches)
	}
	return store
}

func TestAuxiliaryMigrator_MigrateAll_RecordsRowFailures(t *testing.T) {
	t.Parallel()

	// wantMigrated is image caches, thresholds, threshold events, notifications.
	tests := []struct {
		name         string
		prepare      func(env *auxV2Env, cfg *AuxiliaryMigratorConfig)
		eventFetches int
		wantFailed   string // section named in the summary; empty means succeeded
		wantMigrated [4]int
	}{
		{
			name: "image cache save fails",
			prepare: func(env *auxV2Env, _ *AuxiliaryMigratorConfig) {
				env.dropTable(&entities.ImageCache{})
			},
			eventFetches: 1,
			wantFailed:   "image cache",
			wantMigrated: [4]int{0, 1, 1, 1},
		},
		{
			name: "image cache label not resolved",
			prepare: func(env *auxV2Env, cfg *AuxiliaryMigratorConfig) {
				cfg.LabelRepo = labelMissDecorator{LabelRepository: env.labelRepo, omit: auxTestSpeciesImage}
			},
			eventFetches: 1,
			wantFailed:   "image cache",
			wantMigrated: [4]int{0, 1, 1, 1},
		},
		{
			name: "notification label not resolved",
			prepare: func(env *auxV2Env, cfg *AuxiliaryMigratorConfig) {
				cfg.LabelRepo = labelMissDecorator{LabelRepository: env.labelRepo, omit: auxTestSpeciesNotification}
			},
			eventFetches: 1,
			wantFailed:   "notifications",
			wantMigrated: [4]int{1, 1, 1, 0},
		},
		{
			name: "dynamic threshold save fails",
			prepare: func(env *auxV2Env, _ *AuxiliaryMigratorConfig) {
				env.dropTable(&entities.DynamicThreshold{})
			},
			eventFetches: 0,
			wantFailed:   "threshold",
			wantMigrated: [4]int{1, 0, 0, 1},
		},
		{
			name: "threshold event save fails",
			prepare: func(env *auxV2Env, _ *AuxiliaryMigratorConfig) {
				env.dropTable(&entities.ThresholdEvent{})
			},
			eventFetches: 1,
			wantFailed:   "threshold events",
			wantMigrated: [4]int{1, 1, 0, 1},
		},
		{
			name: "notification save fails",
			prepare: func(env *auxV2Env, _ *AuxiliaryMigratorConfig) {
				env.dropTable(&entities.NotificationHistory{})
			},
			eventFetches: 1,
			wantFailed:   "notifications",
			wantMigrated: [4]int{1, 1, 1, 0},
		},
		{
			name:         "all rows migrate",
			prepare:      func(*auxV2Env, *AuxiliaryMigratorConfig) {},
			eventFetches: 1,
			wantMigrated: [4]int{1, 1, 1, 1},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			env := newAuxV2Env(t)
			cfg := env.config(expectLegacy(t, defaultAuxLegacyRows(), tt.eventFetches))
			tt.prepare(env, cfg)

			result, err := NewAuxiliaryMigrator(cfg).MigrateAll(t.Context())
			require.NoError(t, err)

			status, summary := result.Outcome()
			if tt.wantFailed == "" {
				assert.Equal(t, entities.AuxiliaryDataStatusSucceeded, status)
				assert.Empty(t, summary)
			} else {
				assert.Equal(t, entities.AuxiliaryDataStatusFailed, status)
				assert.Contains(t, summary, tt.wantFailed+":")
				assert.True(t, result.HasErrors())
			}
			assert.Equal(t, tt.wantMigrated, [4]int{
				result.ImageCaches.Migrated, result.Thresholds.Migrated,
				result.ThresholdEvents.Migrated, result.Notifications.Migrated,
			})
		})
	}
}

// TestAuxiliaryMigrator_MigrateAll_LegitimateSkipsAreNotFailures covers rows
// that cannot exist in v2 (empty names) and a name with surrounding
// whitespace, which must be migrated under its trimmed name.
func TestAuxiliaryMigrator_MigrateAll_LegitimateSkipsAreNotFailures(t *testing.T) {
	t.Parallel()

	env := newAuxV2Env(t)
	rows := defaultAuxLegacyRows()
	now := time.Now()
	rows.imageCaches = []datastore.ImageCache{
		{ProviderName: "wikimedia", ScientificName: "  " + auxTestSpeciesImage + " ", SourceProvider: "wikimedia", CachedAt: now},
		{ProviderName: "wikimedia", ScientificName: "", SourceProvider: "wikimedia", CachedAt: now},
	}
	rows.thresholds = append(rows.thresholds, datastore.DynamicThreshold{ExpiresAt: now, LastTriggered: now, FirstCreated: now})
	cfg := env.config(expectLegacy(t, rows, 1))

	result, err := NewAuxiliaryMigrator(cfg).MigrateAll(t.Context())
	require.NoError(t, err)

	status, summary := result.Outcome()
	assert.Equal(t, entities.AuxiliaryDataStatusSucceeded, status, summary)
	assert.Equal(t, 1, result.ImageCaches.Migrated)
	assert.Equal(t, 1, result.ImageCaches.Skipped)
	assert.Zero(t, result.ImageCaches.Failed)
	assert.Equal(t, 1, result.Thresholds.Migrated)
	assert.Equal(t, 1, result.Thresholds.Skipped)
	assert.Zero(t, result.Thresholds.Failed)
}

func TestAuxiliaryMigrator_MigrateAll_RequiresDependencies(t *testing.T) {
	t.Parallel()

	env := newAuxV2Env(t)
	nilStore := env.config(nil)
	nilThreshold := env.config(mocks.NewMockInterface(t))
	nilThreshold.ThresholdRepo = nil

	for name, cfg := range map[string]*AuxiliaryMigratorConfig{"legacy store": nilStore, "threshold repo": nilThreshold} {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			_, err := NewAuxiliaryMigrator(cfg).MigrateAll(t.Context())
			require.ErrorIs(t, err, ErrAuxDependencyNotConfigured)
		})
	}
}
