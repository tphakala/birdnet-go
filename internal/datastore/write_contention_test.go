// write_contention_test.go: Integration tests for concurrent SQLite write contention.
//
// These tests verify that the retry logic and connection pool configuration
// (MaxOpenConns=1, DSN pragmas) prevent silent data loss under concurrent
// write pressure. The original problem (silent data loss from unretried lock
// errors) was only caught in production.
//
// All tests use real SQLite databases (not mocks) to exercise actual
// concurrency behavior.
package datastore

import (
	"database/sql"
	"fmt"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"gorm.io/driver/sqlite"
	"gorm.io/gorm"
	gormlogger "gorm.io/gorm/logger"
)

// TestConcurrentSaveDailyEvents spawns N goroutines all calling SaveDailyEvents
// with different dates simultaneously against a real SQLite database. Verifies
// all records are persisted (no silent drops).
func TestConcurrentSaveDailyEvents(t *testing.T) {
	t.Parallel()

	settings := createTestSettings(t)
	ds := createDatabase(t, settings)

	const numGoroutines = 20
	errs := make(chan error, numGoroutines)
	var wg sync.WaitGroup

	for i := range numGoroutines {
		wg.Go(func() {
			event := &DailyEvents{
				Date:             fmt.Sprintf("2024-01-%02d", i+1),
				Sunrise:          int64(6*3600 + i*60),
				Sunset:           int64(18*3600 + i*60),
				Country:          "FI",
				CityName:         fmt.Sprintf("City-%d", i),
				MoonPhase:        float64(i) * 0.5,
				MoonIllumination: float64(i) * 5.0,
			}

			if err := ds.SaveDailyEvents(event); err != nil {
				errs <- fmt.Errorf("goroutine %d: %w", i, err)
			}
		})
	}

	wg.Wait()
	close(errs)

	// Collect and check errors
	allErrors := make([]error, 0, numGoroutines)
	for err := range errs {
		allErrors = append(allErrors, err)
	}
	require.Empty(t, allErrors, "concurrent SaveDailyEvents should not fail: %v", allErrors)

	// Verify all records were persisted
	sqliteStore, ok := ds.(*SQLiteStore)
	require.True(t, ok)

	var count int64
	err := sqliteStore.DB.Model(&DailyEvents{}).Count(&count).Error
	require.NoError(t, err)
	assert.Equal(t, int64(numGoroutines), count,
		"all %d DailyEvents should be persisted, got %d (silent data loss?)", numGoroutines, count)

	// Verify each date is present
	for i := range numGoroutines {
		date := fmt.Sprintf("2024-01-%02d", i+1)
		events, err := ds.GetDailyEvents(date)
		require.NoError(t, err, "GetDailyEvents(%s) failed", date)
		assert.Equal(t, date, events.Date, "date mismatch for record %d", i)
		assert.Equal(t, fmt.Sprintf("City-%d", i), events.CityName,
			"CityName mismatch for record %d — wrong record persisted?", i)
	}
}

// TestConcurrentMixedWrites combines SaveDailyEvents, SaveHourlyWeather,
// SaveImageCache, and Save calls in parallel to simulate realistic production
// load where different write paths compete for the database.
func TestConcurrentMixedWrites(t *testing.T) {
	t.Parallel()

	settings := createTestSettings(t)
	ds := createDatabase(t, settings)

	const numPerType = 10
	totalOps := numPerType * 4
	errs := make(chan error, totalOps)
	var wg sync.WaitGroup

	// SaveDailyEvents goroutines
	for i := range numPerType {
		wg.Go(func() {
			event := &DailyEvents{
				Date:     fmt.Sprintf("2024-02-%02d", i+1),
				Sunrise:  int64(6*3600 + i*60),
				Sunset:   int64(18*3600 + i*60),
				Country:  "FI",
				CityName: fmt.Sprintf("Helsinki-%d", i),
			}
			if err := ds.SaveDailyEvents(event); err != nil {
				errs <- fmt.Errorf("SaveDailyEvents goroutine %d: %w", i, err)
			}
		})
	}

	// SaveHourlyWeather goroutines
	for i := range numPerType {
		wg.Go(func() {
			weather := &HourlyWeather{
				Time:        time.Date(2024, 2, 1, i, 0, 0, 0, time.UTC),
				Temperature: 20.0 + float64(i),
				Humidity:    50 + i,
				Pressure:    1013,
				WindSpeed:   5.0,
				WeatherMain: "Clear",
			}
			if err := ds.SaveHourlyWeather(weather); err != nil {
				errs <- fmt.Errorf("SaveHourlyWeather goroutine %d: %w", i, err)
			}
		})
	}

	// SaveImageCache goroutines
	for i := range numPerType {
		wg.Go(func() {
			cache := &ImageCache{
				ProviderName:   "wikimedia",
				ScientificName: fmt.Sprintf("Species testus %d", i),
				URL:            fmt.Sprintf("https://example.com/bird_%d.jpg", i),
				LicenseName:    "CC BY-SA 4.0",
				AuthorName:     fmt.Sprintf("Author %d", i),
				CachedAt:       time.Now(),
			}
			if err := ds.SaveImageCache(cache); err != nil {
				errs <- fmt.Errorf("SaveImageCache goroutine %d: %w", i, err)
			}
		})
	}

	// Save (detection) goroutines
	for i := range numPerType {
		wg.Go(func() {
			note := Note{
				SourceNode:     fmt.Sprintf("node-%d", i),
				Date:           "2024-02-15",
				Time:           fmt.Sprintf("10:%02d:00", i),
				ScientificName: fmt.Sprintf("Testus species%d", i),
				CommonName:     fmt.Sprintf("Test Bird %d", i),
				Confidence:     0.90,
				ClipName:       fmt.Sprintf("clip_%03d.wav", i),
			}
			results := []Results{
				{Species: fmt.Sprintf("Testus species%d_Test Bird %d", i, i), Confidence: 0.90},
			}
			if err := ds.Save(&note, results); err != nil {
				errs <- fmt.Errorf("Save goroutine %d: %w", i, err)
			}
		})
	}

	wg.Wait()
	close(errs)

	// Collect and check errors
	allErrors := make([]error, 0, totalOps)
	for err := range errs {
		allErrors = append(allErrors, err)
	}
	require.Empty(t, allErrors, "concurrent mixed writes should not fail: %v", allErrors)

	// Verify record counts
	sqliteStore, ok := ds.(*SQLiteStore)
	require.True(t, ok)

	var dailyCount int64
	require.NoError(t, sqliteStore.DB.Model(&DailyEvents{}).Count(&dailyCount).Error)
	assert.Equal(t, int64(numPerType), dailyCount, "DailyEvents count mismatch")

	var weatherCount int64
	require.NoError(t, sqliteStore.DB.Model(&HourlyWeather{}).Count(&weatherCount).Error)
	assert.Equal(t, int64(numPerType), weatherCount, "HourlyWeather count mismatch")

	var cacheCount int64
	require.NoError(t, sqliteStore.DB.Model(&ImageCache{}).Count(&cacheCount).Error)
	assert.Equal(t, int64(numPerType), cacheCount, "ImageCache count mismatch")

	var noteCount int64
	require.NoError(t, sqliteStore.DB.Model(&Note{}).Count(&noteCount).Error)
	assert.Equal(t, int64(numPerType), noteCount, "Note count mismatch")

	// Verify Results rows were also persisted (each Save call included 1 Result)
	var resultsCount int64
	require.NoError(t, sqliteStore.DB.Model(&Results{}).Count(&resultsCount).Error)
	assert.Equal(t, int64(numPerType), resultsCount, "Results count mismatch — Save() dropped Results?")
}

// TestMaxOpenConnsEffectiveness validates that SetMaxOpenConns(1) prevents
// database lock errors. Opens a database with MaxOpenConns > 1 and runs
// concurrent writes to demonstrate lock errors can occur, then opens another
// with MaxOpenConns = 1 (the production setting) and verifies no lock errors.
func TestMaxOpenConnsEffectiveness(t *testing.T) {
	t.Parallel()

	const numGoroutines = 20

	// --- Phase 1: MaxOpenConns > 1 should produce lock contention ---
	// We open a raw GORM connection with MaxOpenConns=10 and no busy_timeout
	// to maximize the chance of immediate lock errors.
	unboundedDB := openRawSQLiteDB(t, "unbounded",
		"_journal_mode=WAL&_busy_timeout=0&_foreign_keys=ON")
	setMaxOpenConns(t, unboundedDB, numGoroutines)

	require.NoError(t, unboundedDB.AutoMigrate(&DailyEvents{}))

	unboundedErrs := make(chan error, numGoroutines)
	var wgUnbounded sync.WaitGroup

	for i := range numGoroutines {
		wgUnbounded.Go(func() {
			event := DailyEvents{
				Date:     fmt.Sprintf("2024-03-%02d", i+1),
				Sunrise:  int64(6*3600 + i*60),
				Sunset:   int64(18*3600 + i*60),
				CityName: fmt.Sprintf("Unbounded-%d", i),
			}
			// Direct GORM write, bypassing retry logic to expose raw lock errors
			if err := unboundedDB.Create(&event).Error; err != nil {
				unboundedErrs <- err
			}
		})
	}
	wgUnbounded.Wait()
	close(unboundedErrs)

	unboundedLockErrors := 0
	for err := range unboundedErrs {
		if isDatabaseLocked(err) {
			unboundedLockErrors++
		}
	}
	// We expect at least some lock errors with multiple connections and no busy_timeout.
	// This is probabilistic — if it doesn't trigger, the test still passes but
	// logs a note. The important assertion is in phase 2.
	t.Logf("Phase 1 (MaxOpenConns=%d, busy_timeout=0): %d lock errors out of %d writes",
		numGoroutines, unboundedLockErrors, numGoroutines)

	// --- Phase 2: MaxOpenConns=1 (production config) should have zero errors ---
	settings := createTestSettings(t)
	ds := createDatabase(t, settings) // Uses MaxOpenConns(1) + busy_timeout=30s

	serializedErrs := make(chan error, numGoroutines)
	var wgSerialized sync.WaitGroup

	for i := range numGoroutines {
		wgSerialized.Go(func() {
			event := &DailyEvents{
				Date:     fmt.Sprintf("2024-04-%02d", i+1),
				Sunrise:  int64(6*3600 + i*60),
				Sunset:   int64(18*3600 + i*60),
				CityName: fmt.Sprintf("Serialized-%d", i),
			}
			if err := ds.SaveDailyEvents(event); err != nil {
				serializedErrs <- err
			}
		})
	}
	wgSerialized.Wait()
	close(serializedErrs)

	allErrors := make([]error, 0, numGoroutines)
	for err := range serializedErrs {
		allErrors = append(allErrors, err)
	}
	require.Empty(t, allErrors,
		"MaxOpenConns=1 should prevent all lock errors, got %d: %v", len(allErrors), allErrors)

	// Verify all records persisted
	sqliteStore, ok := ds.(*SQLiteStore)
	require.True(t, ok)

	var count int64
	require.NoError(t, sqliteStore.DB.Model(&DailyEvents{}).Count(&count).Error)
	assert.Equal(t, int64(numGoroutines), count,
		"all %d records should persist with MaxOpenConns=1", numGoroutines)
}

// TestRetryExhaustion verifies that when lock contention exceeds the retry
// budget, the error is surfaced (not swallowed). Uses RetryOnLock directly
// with a function that always returns a lock error.
func TestRetryExhaustion(t *testing.T) {
	t.Parallel()

	callCount := 0
	lockErr := fmt.Errorf("database is locked")

	err := RetryOnLock("test_exhaustion", func() error {
		callCount++
		return lockErr
	}, nil)

	require.Error(t, err, "RetryOnLock should return an error after exhausting retries")
	assert.Contains(t, err.Error(), "database is locked",
		"the returned error should be the original lock error")
	assert.Equal(t, retryMaxAttempts, callCount,
		"RetryOnLock should have attempted exactly %d times", retryMaxAttempts)
}

// TestRetryExhaustion_NonTransientBailsImmediately verifies that non-transient
// errors (e.g., constraint violations) are not retried.
func TestRetryExhaustion_NonTransientBailsImmediately(t *testing.T) {
	t.Parallel()

	callCount := 0
	constraintErr := fmt.Errorf("UNIQUE constraint failed: daily_events.date")

	err := RetryOnLock("test_non_transient", func() error {
		callCount++
		return constraintErr
	}, nil)

	require.Error(t, err)
	assert.Contains(t, err.Error(), "UNIQUE constraint failed")
	assert.Equal(t, 1, callCount,
		"non-transient errors should bail after exactly 1 attempt, got %d", callCount)
}

// TestRetryExhaustion_IntegrationWithRealDB creates real lock contention by
// holding a write transaction open while another goroutine tries to write,
// verifying the error surfaces after retries are exhausted.
func TestRetryExhaustion_IntegrationWithRealDB(t *testing.T) {
	t.Parallel()

	// Open a database with multiple connections and no busy_timeout
	// so lock errors happen immediately without waiting.
	db := openRawSQLiteDB(t, "retry_exhaust", "_journal_mode=WAL&_busy_timeout=0&_foreign_keys=ON")
	setMaxOpenConns(t, db, 2)
	require.NoError(t, db.AutoMigrate(&DailyEvents{}))

	ds := &DataStore{DB: db}

	// Hold a write transaction open to block other writers
	tx := db.Begin()
	require.NoError(t, tx.Error)
	t.Cleanup(func() {
		// Rollback is a no-op after Commit, but ensures cleanup if the
		// test fails before reaching the explicit Commit call below.
		_ = tx.Rollback().Error
	})
	require.NoError(t, tx.Create(&DailyEvents{
		Date:     "2024-05-01",
		CityName: "Blocker",
	}).Error)
	// Don't commit yet — keep the transaction open to hold the write lock

	// Try to write from a different goroutine using RetryOnLock.
	// With busy_timeout=0 and the lock held, every attempt should fail.
	done := make(chan error, 1)
	go func() {
		event := &DailyEvents{
			Date:     "2024-05-02",
			CityName: "Blocked",
		}
		done <- RetryOnLock("blocked_write", func() error {
			return ds.DB.Create(event).Error
		}, nil)
	}()

	// Wait for retries to exhaust (with busy_timeout=0, each attempt fails
	// immediately, so retries complete quickly)
	err := <-done
	require.Error(t, err, "write should fail when lock is held and retries exhaust")
	assert.True(t, isDatabaseLocked(err),
		"error should be a database lock error, got: %v", err)

	// Clean up: commit the blocking transaction
	require.NoError(t, tx.Commit().Error)
}

// TestDSNPragmaVerification opens a SQLite database with the production DSN
// parameters and verifies that PRAGMA journal_mode and PRAGMA busy_timeout
// report the expected values (WAL and 30000ms) from the connection pool.
func TestDSNPragmaVerification(t *testing.T) {
	t.Parallel()

	settings := createTestSettings(t)
	ds := createDatabase(t, settings)

	sqliteStore, ok := ds.(*SQLiteStore)
	require.True(t, ok)

	sqlDB, err := sqliteStore.DB.DB()
	require.NoError(t, err)

	// Verify journal_mode is WAL
	var journalMode string
	err = sqlDB.QueryRow("PRAGMA journal_mode").Scan(&journalMode)
	require.NoError(t, err)
	assert.Equal(t, "wal", journalMode,
		"journal_mode should be WAL, got %q", journalMode)

	// Verify busy_timeout is 30000ms
	var busyTimeout int
	err = sqlDB.QueryRow("PRAGMA busy_timeout").Scan(&busyTimeout)
	require.NoError(t, err)
	assert.Equal(t, 30000, busyTimeout,
		"busy_timeout should be 30000ms, got %d", busyTimeout)
}

// TestDSNPragmaVerification_AllPoolConnections verifies that DSN-embedded
// pragmas apply to ALL connections in the pool, not just the first one.
// This was the root cause of the original bug: pragmas set via Exec() only
// applied to one connection, leaving new pool connections at SQLite defaults.
//
// Uses a barrier pattern: acquires and holds exactly MaxOpenConns connections
// simultaneously, then verifies pragmas on all of them before releasing.
func TestDSNPragmaVerification_AllPoolConnections(t *testing.T) {
	t.Parallel()

	const maxConns = 5

	// Open with multiple pool connections to test pragma propagation
	db := openRawSQLiteDB(t, "pragma_pool",
		"_journal_mode=WAL&_busy_timeout=30000&_foreign_keys=ON&_synchronous=NORMAL&_cache_size=-4000")
	setMaxOpenConns(t, db, maxConns)

	sqlDB, err := db.DB()
	require.NoError(t, err)

	// Acquire and hold all MaxOpenConns connections simultaneously.
	// This guarantees we test every distinct pool connection, not just
	// reuse the same one. If pragmas were set via Exec() instead of DSN,
	// some connections would report SQLite defaults.
	type pragmaResult struct {
		journalMode string
		busyTimeout int
	}

	conns := make([]*sql.Conn, maxConns)
	results := make([]pragmaResult, maxConns)

	// Phase 1: Acquire all connections (barrier — hold them all open)
	for i := range maxConns {
		conn, connErr := sqlDB.Conn(t.Context())
		require.NoError(t, connErr, "failed to acquire pool connection %d", i)
		conns[i] = conn
	}

	// Phase 2: Query pragmas on all held connections
	for i, conn := range conns {
		var jm string
		err := conn.QueryRowContext(t.Context(), "PRAGMA journal_mode").Scan(&jm)
		require.NoError(t, err, "PRAGMA journal_mode failed on connection %d", i)

		var bt int
		err = conn.QueryRowContext(t.Context(), "PRAGMA busy_timeout").Scan(&bt)
		require.NoError(t, err, "PRAGMA busy_timeout failed on connection %d", i)

		results[i] = pragmaResult{journalMode: jm, busyTimeout: bt}
	}

	// Phase 3: Release all connections
	for i, conn := range conns {
		require.NoError(t, conn.Close(), "failed to close connection %d", i)
	}

	// Verify all connections reported correct pragmas
	for i, pr := range results {
		assert.Equal(t, "wal", pr.journalMode,
			"pool connection %d should have journal_mode=WAL", i)
		assert.Equal(t, 30000, pr.busyTimeout,
			"pool connection %d should have busy_timeout=30000", i)
	}
}

// --- Test helpers ---

// openRawSQLiteDB opens a temporary SQLite database with GORM using the given
// pragma string. Returns the GORM DB instance. The database file is created
// in t.TempDir() and cleaned up automatically.
func openRawSQLiteDB(t *testing.T, name, pragmas string) *gorm.DB {
	t.Helper()

	dbPath := fmt.Sprintf("%s/%s.db", t.TempDir(), name)
	dsn := buildSQLiteDSN(dbPath, pragmas)

	db, err := gorm.Open(sqlite.Open(dsn), &gorm.Config{
		Logger: gormlogger.Default.LogMode(gormlogger.Silent),
	})
	require.NoError(t, err, "failed to open SQLite database %s", name)

	t.Cleanup(func() {
		sqlDB, err := db.DB()
		if err == nil {
			_ = sqlDB.Close()
		}
	})

	return db
}

// setMaxOpenConns sets the maximum number of open connections on a GORM DB.
func setMaxOpenConns(t *testing.T, db *gorm.DB, n int) {
	t.Helper()

	sqlDB, err := db.DB()
	require.NoError(t, err)
	sqlDB.SetMaxOpenConns(n)
}
