package testutil

import (
	"context"
	"database/sql"
	"io"
	"path/filepath"
	"testing"
	"time"

	_ "github.com/mattn/go-sqlite3"
	"github.com/stretchr/testify/require"
	"github.com/tphakala/birdnet-go/internal/datastore"
	datastoreV2 "github.com/tphakala/birdnet-go/internal/datastore/v2"
	"github.com/tphakala/birdnet-go/internal/datastore/v2/entities"
	"github.com/tphakala/birdnet-go/internal/datastore/v2/migration"
	"github.com/tphakala/birdnet-go/internal/datastore/v2/repository"
	"github.com/tphakala/birdnet-go/internal/detection"
	"github.com/tphakala/birdnet-go/internal/logger"
	"gorm.io/driver/sqlite"
	"gorm.io/gorm"
	gormlogger "gorm.io/gorm/logger"
)

// TestContext contains all dependencies needed for migration integration tests.
type TestContext struct {
	// Temporary directory for test databases
	TempDir string

	// Legacy database components
	LegacyDB   *sql.DB         // Raw SQL connection for seeding
	LegacyGorm *gorm.DB        // GORM connection for queries
	Seeder     *LegacySeeder   // Seeder for legacy data

	// V2 database components
	V2Manager    *datastoreV2.SQLiteManager // V2 SQLite manager
	StateManager *datastoreV2.StateManager  // Migration state manager

	// V2 Repositories
	DetectionRepo    repository.DetectionRepository
	LabelRepo        repository.LabelRepository
	ModelRepo        repository.ModelRepository
	SourceRepo       repository.AudioSourceRepository
	WeatherRepo      repository.WeatherRepository
	ImageCacheRepo   repository.ImageCacheRepository
	ThresholdRepo    repository.DynamicThresholdRepository
	NotificationRepo repository.NotificationHistoryRepository

	// Migration components
	Worker            *migration.Worker
	AuxiliaryMigrator *migration.AuxiliaryMigrator

	// Adapters for migration interfaces
	legacyDetectionRepo datastore.DetectionRepository
	legacyInterface     datastore.Interface

	// Logger
	Logger logger.Logger

	// Testing
	t       *testing.T
	cleanup func()
}

// SetupIntegrationTest creates a complete test environment for migration integration tests.
// Call this at the start of each test. The cleanup function is registered with t.Cleanup().
func SetupIntegrationTest(t *testing.T) *TestContext {
	t.Helper()

	tmpDir := t.TempDir()

	// Create test logger (silent for tests)
	log := logger.NewSlogLogger(io.Discard, logger.LogLevelError, time.UTC)

	ctx := &TestContext{
		TempDir: tmpDir,
		Logger:  log,
		t:       t,
	}

	// Set up legacy database
	ctx.setupLegacyDB(t, tmpDir)

	// Set up V2 database
	ctx.setupV2DB(t, tmpDir)

	// Create migration worker
	ctx.createWorker(t)

	// Create auxiliary migrator
	ctx.createAuxiliaryMigrator(t)

	// Register cleanup
	t.Cleanup(func() {
		if ctx.cleanup != nil {
			ctx.cleanup()
		}
	})

	return ctx
}

// setupLegacyDB initializes the legacy SQLite database with schema.
func (ctx *TestContext) setupLegacyDB(t *testing.T, tmpDir string) {
	t.Helper()

	legacyDBPath := filepath.Join(tmpDir, "birdnet.db")

	// Create legacy database with GORM to set up schema
	gormDB, err := gorm.Open(sqlite.Open(legacyDBPath), &gorm.Config{
		Logger: gormlogger.Default.LogMode(gormlogger.Silent),
	})
	require.NoError(t, err, "failed to create legacy GORM DB")

	// Auto-migrate the legacy schema
	err = gormDB.AutoMigrate(
		&datastore.Note{},
		&datastore.Results{},
		&datastore.NoteReview{},
		&datastore.NoteComment{},
		&datastore.NoteLock{},
		&datastore.DailyEvents{},
		&datastore.HourlyWeather{},
		&datastore.DynamicThreshold{},
		&datastore.ThresholdEvent{},
		&datastore.ImageCache{},
		&datastore.NotificationHistory{},
	)
	require.NoError(t, err, "failed to migrate legacy schema")

	// Get the underlying sql.DB for the seeder
	sqlDB, err := gormDB.DB()
	require.NoError(t, err, "failed to get sql.DB from GORM")

	// Also open a direct SQL connection for the seeder (since GORM's sql.DB may have pooling issues)
	directSqlDB, err := sql.Open("sqlite3", legacyDBPath)
	require.NoError(t, err, "failed to open direct SQL connection")

	ctx.LegacyDB = directSqlDB
	ctx.LegacyGorm = gormDB
	ctx.Seeder = NewLegacySeeder(directSqlDB)

	// Create adapters for migration interfaces
	ctx.legacyDetectionRepo = newTestDetectionRepo(gormDB)
	ctx.legacyInterface = newTestLegacyInterface(gormDB)

	// Store sql.DB for cleanup
	oldCleanup := ctx.cleanup
	ctx.cleanup = func() {
		if oldCleanup != nil {
			oldCleanup()
		}
		_ = directSqlDB.Close()
		_ = sqlDB.Close()
	}
}

// setupV2DB initializes the V2 SQLite database with schema and repositories.
func (ctx *TestContext) setupV2DB(t *testing.T, tmpDir string) {
	t.Helper()

	// Create V2 manager
	mgr, err := datastoreV2.NewSQLiteManager(datastoreV2.Config{
		DataDir: tmpDir,
		Debug:   false,
		Logger:  ctx.Logger,
	})
	require.NoError(t, err, "failed to create V2 manager")

	// Initialize schema
	err = mgr.Initialize()
	require.NoError(t, err, "failed to initialize V2 schema")

	ctx.V2Manager = mgr

	// Create state manager
	ctx.StateManager = datastoreV2.NewStateManager(mgr.DB())

	// Create repositories (useV2Prefix = false for direct table access in tests, isMySQL = false for SQLite)
	db := mgr.DB()
	ctx.DetectionRepo = repository.NewDetectionRepository(db, false, false)
	ctx.LabelRepo = repository.NewLabelRepository(db, false, false)
	ctx.ModelRepo = repository.NewModelRepository(db, false, false)
	ctx.SourceRepo = repository.NewAudioSourceRepository(db, false, false)
	ctx.WeatherRepo = repository.NewWeatherRepository(db, false, false)
	ctx.ImageCacheRepo = repository.NewImageCacheRepository(db, false, false)
	ctx.ThresholdRepo = repository.NewDynamicThresholdRepository(db, false, false)
	ctx.NotificationRepo = repository.NewNotificationHistoryRepository(db, false, false)

	// Add to cleanup
	oldCleanup := ctx.cleanup
	ctx.cleanup = func() {
		if oldCleanup != nil {
			oldCleanup()
		}
		_ = mgr.Close()
	}
}

// createWorker creates the migration worker with all dependencies.
func (ctx *TestContext) createWorker(t *testing.T) {
	t.Helper()

	// Batch size for test migrations - smaller than production for faster tests
	const testMigrationBatchSize = 100

	// Create related data migrator for reviews, comments, locks, predictions
	// Use small batch size for faster testing
	relatedMigrator := migration.NewRelatedDataMigrator(&migration.RelatedDataMigratorConfig{
		LegacyStore:   ctx.legacyInterface,
		DetectionRepo: ctx.DetectionRepo,
		LabelRepo:     ctx.LabelRepo,
		Logger:        ctx.Logger,
		BatchSize:     testMigrationBatchSize,
	})

	// Create worker with test configuration
	worker, err := migration.NewWorker(&migration.WorkerConfig{
		Legacy:          ctx.legacyDetectionRepo,
		V2Detection:     ctx.DetectionRepo,
		LabelRepo:       ctx.LabelRepo,
		ModelRepo:       ctx.ModelRepo,
		SourceRepo:      ctx.SourceRepo,
		StateManager:    ctx.StateManager,
		RelatedMigrator: relatedMigrator,
		Logger:          ctx.Logger,
		BatchSize:       testMigrationBatchSize,
		Timezone:        time.UTC,
		MaxConsecErrors: 5,
	})
	require.NoError(t, err, "failed to create migration worker")

	ctx.Worker = worker
}

// createAuxiliaryMigrator creates the auxiliary migrator with all dependencies.
func (ctx *TestContext) createAuxiliaryMigrator(t *testing.T) {
	t.Helper()

	ctx.AuxiliaryMigrator = migration.NewAuxiliaryMigrator(&migration.AuxiliaryMigratorConfig{
		LegacyStore:      ctx.legacyInterface,
		WeatherRepo:      ctx.WeatherRepo,
		ImageCacheRepo:   ctx.ImageCacheRepo,
		ThresholdRepo:    ctx.ThresholdRepo,
		NotificationRepo: ctx.NotificationRepo,
		Logger:           ctx.Logger,
	})
}

// RecreateWorker creates a new worker instance (useful for crash recovery tests).
func (ctx *TestContext) RecreateWorker(t *testing.T) {
	t.Helper()
	ctx.createWorker(t)
}

// InitMigrationState initializes the migration state for a test.
func (ctx *TestContext) InitMigrationState(t *testing.T, totalRecords int) {
	t.Helper()

	err := ctx.StateManager.StartMigration(int64(totalRecords))
	require.NoError(t, err, "failed to start migration")
}

// TransitionToDualWrite transitions the migration state to dual-write mode.
func (ctx *TestContext) TransitionToDualWrite(t *testing.T) {
	t.Helper()

	state, err := ctx.StateManager.GetState()
	require.NoError(t, err, "failed to get state")

	if state.State == entities.MigrationStatusInitializing {
		err = ctx.StateManager.TransitionToDualWrite()
		require.NoError(t, err, "failed to transition to dual-write")
	}
}

// StartMigration runs the full migration process.
func (ctx *TestContext) StartMigration(t *testing.T, totalRecords int) {
	t.Helper()

	c := context.Background()

	// Initialize state
	ctx.InitMigrationState(t, totalRecords)

	// Transition to dual-write
	ctx.TransitionToDualWrite(t)

	// Run auxiliary migration (weather, thresholds, etc.)
	err := ctx.AuxiliaryMigrator.MigrateAll(c)
	require.NoError(t, err, "auxiliary migration failed")

	// Start worker
	err = ctx.Worker.Start(c)
	require.NoError(t, err, "worker start failed")
}

// WaitForCompletion waits for the migration to complete.
func (ctx *TestContext) WaitForCompletion(t *testing.T, timeout time.Duration) {
	t.Helper()

	deadline := time.Now().Add(timeout)

	for time.Now().Before(deadline) {
		state, err := ctx.StateManager.GetState()
		require.NoError(t, err, "failed to get state")

		if state.State == entities.MigrationStatusCompleted {
			return
		}

		if state.State == entities.MigrationStatusPaused {
			t.Fatal("migration unexpectedly paused")
		}

		// Check for error in state
		if state.ErrorMessage != "" {
			t.Fatalf("migration error: %s", state.ErrorMessage)
		}

		time.Sleep(50 * time.Millisecond)
	}

	t.Fatalf("migration did not complete within %v", timeout)
}

// GetMigrationProgress returns current migration progress.
func (ctx *TestContext) GetMigrationProgress(t *testing.T) (migrated, total int64) {
	t.Helper()

	state, err := ctx.StateManager.GetState()
	require.NoError(t, err, "failed to get state")

	return state.MigratedRecords, state.TotalRecords
}

// GetV2DetectionCount returns the number of detections in V2 database.
func (ctx *TestContext) GetV2DetectionCount(t *testing.T) int64 {
	t.Helper()

	c := context.Background()
	count, err := ctx.DetectionRepo.CountAll(c)
	require.NoError(t, err, "failed to get detection count")

	return count
}

// GetLegacyNoteCount returns the number of notes in the legacy database.
func (ctx *TestContext) GetLegacyNoteCount(t *testing.T) int {
	t.Helper()

	var count int
	err := ctx.LegacyDB.QueryRow("SELECT COUNT(*) FROM notes").Scan(&count)
	require.NoError(t, err, "failed to get legacy note count")

	return count
}

// ============================================================================
// Test Adapter: DetectionRepository for Worker
// ============================================================================

// testDetectionRepo implements datastore.DetectionRepository for testing.
// It provides the minimal methods needed by the migration Worker.
type testDetectionRepo struct {
	db *gorm.DB
}

func newTestDetectionRepo(db *gorm.DB) *testDetectionRepo {
	return &testDetectionRepo{db: db}
}

// CountAll returns the total count of all detections.
func (r *testDetectionRepo) CountAll(_ context.Context) (int64, error) {
	var count int64
	err := r.db.Model(&datastore.Note{}).Count(&count).Error
	return count, err
}

// Search finds detections matching the given filters.
func (r *testDetectionRepo) Search(_ context.Context, filters *datastore.DetectionFilters) ([]*detection.Result, int64, error) {
	var notes []datastore.Note
	query := r.db.Model(&datastore.Note{})

	// Apply basic filters
	if filters != nil {
		if filters.Limit > 0 {
			query = query.Limit(filters.Limit)
		}
		if filters.Offset > 0 {
			query = query.Offset(filters.Offset)
		}
		if filters.MinID > 0 {
			query = query.Where("id > ?", filters.MinID)
		}
	}

	query = query.Order("id ASC")

	if err := query.Find(&notes).Error; err != nil {
		return nil, 0, err
	}

	// Count total
	var total int64
	countQuery := r.db.Model(&datastore.Note{})
	if filters != nil && filters.MinID > 0 {
		countQuery = countQuery.Where("id > ?", filters.MinID)
	}
	if err := countQuery.Count(&total).Error; err != nil {
		return nil, 0, err
	}

	// Convert to detection.Result
	results := make([]*detection.Result, len(notes))
	for i := range notes {
		// Load related data
		var additionalResults []datastore.Results
		r.db.Where("note_id = ?", notes[i].ID).Find(&additionalResults)

		var review datastore.NoteReview
		r.db.Where("note_id = ?", notes[i].ID).First(&review)

		var comments []datastore.NoteComment
		r.db.Where("note_id = ?", notes[i].ID).Find(&comments)

		var lock datastore.NoteLock
		r.db.Where("note_id = ?", notes[i].ID).First(&lock)

		results[i] = convertNoteToResult(&notes[i], additionalResults, &review, comments, &lock)
	}

	return results, total, nil
}

// convertNoteToResult converts a legacy Note to a detection.Result.
func convertNoteToResult(note *datastore.Note, _ []datastore.Results, review *datastore.NoteReview, comments []datastore.NoteComment, lock *datastore.NoteLock) *detection.Result {
	// Parse date and time strings to timestamp
	timestamp, _ := time.Parse("2006-01-02 15:04:05", note.Date+" "+note.Time)

	result := &detection.Result{
		ID:        note.ID,
		Timestamp: timestamp,
		Species: detection.Species{
			ScientificName: note.ScientificName,
			CommonName:     note.CommonName,
			Code:           note.SpeciesCode,
		},
		Confidence:     note.Confidence,
		Latitude:       note.Latitude,
		Longitude:      note.Longitude,
		Threshold:      note.Threshold,
		Sensitivity:    note.Sensitivity,
		ClipName:       note.ClipName,
		ProcessingTime: note.ProcessingTime,
		BeginTime:      note.BeginTime,
		EndTime:        note.EndTime,
		Model:          detection.DefaultModelInfo(),
		AudioSource:    detection.AudioSource{},
	}

	// Add review status
	if review != nil && review.ID != 0 {
		result.Verified = review.Verified
	}

	// Add comments
	for _, c := range comments {
		result.Comments = append(result.Comments, detection.Comment{
			ID:        c.ID,
			Entry:     c.Entry,
			CreatedAt: c.CreatedAt,
			UpdatedAt: c.UpdatedAt,
		})
	}

	// Add lock status
	if lock != nil && lock.ID != 0 {
		result.Locked = true
	}

	return result
}

// Stub implementations for DetectionRepository interface methods not used by Worker.
// These are required by the interface but not called during migration.
//
//nolint:nilnil // All stub methods intentionally return nil, nil as they are never called.

func (r *testDetectionRepo) Save(_ context.Context, _ *detection.Result, _ []detection.AdditionalResult) error {
	return nil
}

func (r *testDetectionRepo) Get(_ context.Context, _ string) (*detection.Result, error) {
	return nil, nil //nolint:nilnil // Stub implementation
}

func (r *testDetectionRepo) Delete(_ context.Context, _ string) error {
	return nil
}

func (r *testDetectionRepo) GetRecent(_ context.Context, _ int) ([]*detection.Result, error) {
	return nil, nil
}

func (r *testDetectionRepo) GetBySpecies(_ context.Context, _ string, _ *datastore.DetectionFilters) ([]*detection.Result, int64, error) {
	return nil, 0, nil
}

func (r *testDetectionRepo) GetByDateRange(_ context.Context, _, _ string, _, _ int) ([]*detection.Result, int64, error) {
	return nil, 0, nil
}

func (r *testDetectionRepo) GetHourly(_ context.Context, _, _ string, _, _, _ int) ([]*detection.Result, int64, error) {
	return nil, 0, nil
}

func (r *testDetectionRepo) Lock(_ context.Context, _ string) error   { return nil }
func (r *testDetectionRepo) Unlock(_ context.Context, _ string) error { return nil }
func (r *testDetectionRepo) IsLocked(_ context.Context, _ string) (bool, error) {
	return false, nil
}

func (r *testDetectionRepo) SetReview(_ context.Context, _, _ string) error { return nil }
func (r *testDetectionRepo) GetReview(_ context.Context, _ string) (string, error) {
	return "", nil
}

func (r *testDetectionRepo) AddComment(_ context.Context, _, _ string) error { return nil }
func (r *testDetectionRepo) GetComments(_ context.Context, _ string) ([]detection.Comment, error) {
	return nil, nil
}
func (r *testDetectionRepo) UpdateComment(_ context.Context, _ uint, _ string) error { return nil }
func (r *testDetectionRepo) DeleteComment(_ context.Context, _ uint) error           { return nil }
func (r *testDetectionRepo) GetClipPath(_ context.Context, _ string) (string, error) {
	return "", nil
}
func (r *testDetectionRepo) GetAdditionalResults(_ context.Context, _ string) ([]detection.AdditionalResult, error) {
	return nil, nil
}

// ============================================================================
// Test Adapter: Interface for AuxiliaryMigrator
// ============================================================================

// testLegacyInterface implements datastore.Interface for testing.
// It provides the methods needed by the AuxiliaryMigrator.
type testLegacyInterface struct {
	db *gorm.DB
}

func newTestLegacyInterface(db *gorm.DB) *testLegacyInterface {
	return &testLegacyInterface{db: db}
}

// GetAllImageCaches implements datastore.Interface.
func (s *testLegacyInterface) GetAllImageCaches(provider string) ([]datastore.ImageCache, error) {
	var caches []datastore.ImageCache
	err := s.db.Where("provider_name = ?", provider).Find(&caches).Error
	return caches, err
}

// GetAllDynamicThresholds implements datastore.Interface.
func (s *testLegacyInterface) GetAllDynamicThresholds(_ ...int) ([]datastore.DynamicThreshold, error) {
	var thresholds []datastore.DynamicThreshold
	err := s.db.Find(&thresholds).Error
	return thresholds, err
}

// GetThresholdEvents implements datastore.Interface.
func (s *testLegacyInterface) GetThresholdEvents(speciesName string, limit int) ([]datastore.ThresholdEvent, error) {
	var events []datastore.ThresholdEvent
	err := s.db.Where("species_name = ?", speciesName).Order("created_at DESC").Limit(limit).Find(&events).Error
	return events, err
}

// GetActiveNotificationHistory implements datastore.Interface.
func (s *testLegacyInterface) GetActiveNotificationHistory(after time.Time) ([]datastore.NotificationHistory, error) {
	var history []datastore.NotificationHistory
	err := s.db.Where("expires_at > ?", after).Find(&history).Error
	return history, err
}

// GetAllDailyEvents implements datastore.Interface.
func (s *testLegacyInterface) GetAllDailyEvents() ([]datastore.DailyEvents, error) {
	var events []datastore.DailyEvents
	err := s.db.Find(&events).Error
	return events, err
}

// GetAllHourlyWeather implements datastore.Interface.
func (s *testLegacyInterface) GetAllHourlyWeather() ([]datastore.HourlyWeather, error) {
	var weather []datastore.HourlyWeather
	err := s.db.Find(&weather).Error
	return weather, err
}

// Stub implementations for unused datastore.Interface methods.
// These are required by the interface but not used in migration tests.

func (s *testLegacyInterface) Open() error                                      { return nil }
func (s *testLegacyInterface) Close() error                                     { return nil }
func (s *testLegacyInterface) Save(_ *datastore.Note, _ []datastore.Results) error { return nil }
func (s *testLegacyInterface) Delete(_ string) error                            { return nil }
func (s *testLegacyInterface) Get(_ string) (datastore.Note, error)             { return datastore.Note{}, nil }
func (s *testLegacyInterface) SetMetrics(_ *datastore.Metrics)                  {}
func (s *testLegacyInterface) SetSunCalcMetrics(_ any)                          {}
func (s *testLegacyInterface) Optimize(_ context.Context) error                 { return nil }
func (s *testLegacyInterface) GetAllNotes() ([]datastore.Note, error)           { return nil, nil }
func (s *testLegacyInterface) GetTopBirdsData(_ string, _ float64) ([]datastore.Note, error) {
	return nil, nil
}
func (s *testLegacyInterface) GetHourlyOccurrences(_, _ string, _ float64) ([24]int, error) {
	return [24]int{}, nil
}
func (s *testLegacyInterface) SpeciesDetections(_, _, _ string, _ int, _ bool, _, _ int) ([]datastore.Note, error) {
	return nil, nil
}
func (s *testLegacyInterface) GetLastDetections(_ int) ([]datastore.Note, error)   { return nil, nil }
func (s *testLegacyInterface) GetAllDetectedSpecies() ([]datastore.Note, error)    { return nil, nil }
func (s *testLegacyInterface) SearchNotes(_ string, _ bool, _, _ int) ([]datastore.Note, error) {
	return nil, nil
}
func (s *testLegacyInterface) SearchNotesAdvanced(_ *datastore.AdvancedSearchFilters) ([]datastore.Note, int64, error) {
	return nil, 0, nil
}
func (s *testLegacyInterface) GetNoteClipPath(_ string) (string, error)    { return "", nil }
func (s *testLegacyInterface) DeleteNoteClipPath(_ string) error           { return nil }
func (s *testLegacyInterface) GetNoteReview(_ string) (*datastore.NoteReview, error) { return nil, nil } //nolint:nilnil // stub
func (s *testLegacyInterface) SaveNoteReview(_ *datastore.NoteReview) error          { return nil }
func (s *testLegacyInterface) GetNoteComments(_ string) ([]datastore.NoteComment, error) {
	return nil, nil
}
func (s *testLegacyInterface) GetNoteResults(_ string) ([]datastore.Results, error) { return nil, nil }
func (s *testLegacyInterface) SaveNoteComment(_ *datastore.NoteComment) error       { return nil }
func (s *testLegacyInterface) UpdateNoteComment(_, _ string) error                  { return nil }
func (s *testLegacyInterface) DeleteNoteComment(_ string) error                     { return nil }
func (s *testLegacyInterface) SaveDailyEvents(_ *datastore.DailyEvents) error       { return nil }
func (s *testLegacyInterface) GetDailyEvents(_ string) (datastore.DailyEvents, error) {
	return datastore.DailyEvents{}, nil
}
func (s *testLegacyInterface) SaveHourlyWeather(_ *datastore.HourlyWeather) error { return nil }
func (s *testLegacyInterface) GetHourlyWeather(_ string) ([]datastore.HourlyWeather, error) {
	return nil, nil
}
func (s *testLegacyInterface) LatestHourlyWeather() (*datastore.HourlyWeather, error) { return nil, nil } //nolint:nilnil // stub
func (s *testLegacyInterface) GetHourlyDetections(_, _ string, _, _, _ int) ([]datastore.Note, error) {
	return nil, nil
}
func (s *testLegacyInterface) CountSpeciesDetections(_, _, _ string, _ int) (int64, error) {
	return 0, nil
}
func (s *testLegacyInterface) CountSearchResults(_ string) (int64, error) { return 0, nil }
func (s *testLegacyInterface) Transaction(_ func(tx *gorm.DB) error) error { return nil }
func (s *testLegacyInterface) LockNote(_ string) error                    { return nil }
func (s *testLegacyInterface) UnlockNote(_ string) error                  { return nil }
func (s *testLegacyInterface) GetNoteLock(_ string) (*datastore.NoteLock, error) { return nil, nil } //nolint:nilnil // stub
func (s *testLegacyInterface) IsNoteLocked(_ string) (bool, error)        { return false, nil }
func (s *testLegacyInterface) GetImageCache(_ datastore.ImageCacheQuery) (*datastore.ImageCache, error) {
	return nil, nil //nolint:nilnil // stub
}
func (s *testLegacyInterface) GetImageCacheBatch(_ string, _ []string) (map[string]*datastore.ImageCache, error) {
	return nil, nil //nolint:nilnil // stub
}
func (s *testLegacyInterface) SaveImageCache(_ *datastore.ImageCache) error { return nil }
func (s *testLegacyInterface) GetLockedNotesClipPaths() ([]string, error)   { return nil, nil }
func (s *testLegacyInterface) CountHourlyDetections(_, _ string, _ int) (int64, error) {
	return 0, nil
}
func (s *testLegacyInterface) GetSpeciesSummaryData(_ context.Context, _, _ string) ([]datastore.SpeciesSummaryData, error) {
	return nil, nil
}
func (s *testLegacyInterface) GetHourlyAnalyticsData(_ context.Context, _, _ string) ([]datastore.HourlyAnalyticsData, error) {
	return nil, nil
}
func (s *testLegacyInterface) GetDailyAnalyticsData(_ context.Context, _, _, _ string) ([]datastore.DailyAnalyticsData, error) {
	return nil, nil
}
func (s *testLegacyInterface) GetDetectionTrends(_ context.Context, _ string, _ int) ([]datastore.DailyAnalyticsData, error) {
	return nil, nil
}
func (s *testLegacyInterface) GetHourlyDistribution(_ context.Context, _, _, _ string) ([]datastore.HourlyDistributionData, error) {
	return nil, nil
}
func (s *testLegacyInterface) GetNewSpeciesDetections(_ context.Context, _, _ string, _, _ int) ([]datastore.NewSpeciesData, error) {
	return nil, nil
}
func (s *testLegacyInterface) GetSpeciesFirstDetectionInPeriod(_ context.Context, _, _ string, _, _ int) ([]datastore.NewSpeciesData, error) {
	return nil, nil
}
func (s *testLegacyInterface) SearchDetections(_ *datastore.SearchFilters) ([]datastore.DetectionRecord, int, error) {
	return nil, 0, nil
}
func (s *testLegacyInterface) SaveDynamicThreshold(_ *datastore.DynamicThreshold) error { return nil }
func (s *testLegacyInterface) GetDynamicThreshold(_ string) (*datastore.DynamicThreshold, error) {
	return nil, nil //nolint:nilnil // stub
}
func (s *testLegacyInterface) DeleteDynamicThreshold(_ string) error                   { return nil }
func (s *testLegacyInterface) DeleteExpiredDynamicThresholds(_ time.Time) (int64, error) {
	return 0, nil
}
func (s *testLegacyInterface) UpdateDynamicThresholdExpiry(_ string, _ time.Time) error { return nil }
func (s *testLegacyInterface) BatchSaveDynamicThresholds(_ []datastore.DynamicThreshold) error {
	return nil
}
func (s *testLegacyInterface) DeleteAllDynamicThresholds() (int64, error) { return 0, nil }
func (s *testLegacyInterface) GetDynamicThresholdStats() (totalCount, activeCount, atMinimumCount int64, levelDistribution map[int]int64, err error) {
	return 0, 0, 0, nil, nil
}
func (s *testLegacyInterface) SaveThresholdEvent(_ *datastore.ThresholdEvent) error { return nil }
func (s *testLegacyInterface) GetRecentThresholdEvents(_ int) ([]datastore.ThresholdEvent, error) {
	return nil, nil
}
func (s *testLegacyInterface) DeleteThresholdEvents(_ string) error       { return nil }
func (s *testLegacyInterface) DeleteAllThresholdEvents() (int64, error)   { return 0, nil }
func (s *testLegacyInterface) SaveNotificationHistory(_ *datastore.NotificationHistory) error {
	return nil
}
func (s *testLegacyInterface) GetNotificationHistory(_, _ string) (*datastore.NotificationHistory, error) {
	return nil, nil //nolint:nilnil // stub
}
func (s *testLegacyInterface) DeleteExpiredNotificationHistory(_ time.Time) (int64, error) {
	return 0, nil
}
func (s *testLegacyInterface) GetDatabaseStats() (*datastore.DatabaseStats, error) { return nil, nil } //nolint:nilnil // stub

// Migration bulk fetch methods - query actual database for integration tests

// GetAllReviews returns all note reviews from legacy database.
func (s *testLegacyInterface) GetAllReviews() ([]datastore.NoteReview, error) {
	var reviews []datastore.NoteReview
	err := s.db.Find(&reviews).Error
	return reviews, err
}

// GetAllComments returns all note comments from legacy database.
func (s *testLegacyInterface) GetAllComments() ([]datastore.NoteComment, error) {
	var comments []datastore.NoteComment
	err := s.db.Find(&comments).Error
	return comments, err
}

// GetAllLocks returns all note locks from legacy database.
func (s *testLegacyInterface) GetAllLocks() ([]datastore.NoteLock, error) {
	var locks []datastore.NoteLock
	err := s.db.Find(&locks).Error
	return locks, err
}

// GetAllResults returns all secondary predictions from legacy database.
func (s *testLegacyInterface) GetAllResults() ([]datastore.Results, error) {
	var results []datastore.Results
	err := s.db.Find(&results).Error
	return results, err
}

// Batched fetch methods for memory-safe migration

// GetReviewsBatch returns a batch of note reviews for memory-safe migration.
func (s *testLegacyInterface) GetReviewsBatch(afterID uint, batchSize int) ([]datastore.NoteReview, error) {
	var reviews []datastore.NoteReview
	err := s.db.Where("id > ?", afterID).Order("id ASC").Limit(batchSize).Find(&reviews).Error
	return reviews, err
}

// GetCommentsBatch returns a batch of note comments for memory-safe migration.
func (s *testLegacyInterface) GetCommentsBatch(afterID uint, batchSize int) ([]datastore.NoteComment, error) {
	var comments []datastore.NoteComment
	err := s.db.Where("id > ?", afterID).Order("id ASC").Limit(batchSize).Find(&comments).Error
	return comments, err
}

// GetLocksBatch returns a batch of note locks for memory-safe migration.
func (s *testLegacyInterface) GetLocksBatch(afterID uint, batchSize int) ([]datastore.NoteLock, error) {
	var locks []datastore.NoteLock
	err := s.db.Where("note_id > ?", afterID).Order("note_id ASC").Limit(batchSize).Find(&locks).Error
	return locks, err
}

// GetResultsBatch returns a batch of secondary predictions for memory-safe migration.
// Uses keyset pagination: returns results where (note_id > afterNoteID) OR (note_id = afterNoteID AND id > afterResultID).
func (s *testLegacyInterface) GetResultsBatch(afterNoteID, afterResultID uint, batchSize int) ([]datastore.Results, error) {
	var results []datastore.Results
	err := s.db.Where(
		"(note_id > ?) OR (note_id = ? AND id > ?)",
		afterNoteID, afterNoteID, afterResultID,
	).Order("note_id ASC, id ASC").Limit(batchSize).Find(&results).Error
	return results, err
}

// CountResults returns the total number of secondary predictions.
func (s *testLegacyInterface) CountResults() (int64, error) {
	var count int64
	err := s.db.Model(&datastore.Results{}).Count(&count).Error
	return count, err
}
