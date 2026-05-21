package repository

import (
	"path/filepath"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/tphakala/birdnet-go/internal/datastore/v2/entities"
	"gorm.io/driver/sqlite"
	"gorm.io/gorm"
	gorm_logger "gorm.io/gorm/logger"
)

func setupAppEventTestDB(t *testing.T) *gorm.DB {
	t.Helper()
	dbPath := filepath.Join(t.TempDir(), "test.db")
	db, err := gorm.Open(sqlite.Open(dbPath), &gorm.Config{
		Logger: gorm_logger.Default.LogMode(gorm_logger.Silent),
	})
	require.NoError(t, err)

	sqlDB, err := db.DB()
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, sqlDB.Close()) })

	err = db.AutoMigrate(&entities.AppEvent{})
	require.NoError(t, err)
	return db
}

func TestAppEventRepository_Save(t *testing.T) {
	t.Parallel()
	db := setupAppEventTestDB(t)
	repo := NewAppEventRepository(db, nil, false, false)
	ctx := t.Context()

	event := &entities.AppEvent{
		Timestamp: time.Now(),
		Category:  "system",
		EventType: "startup",
		Message:   "Application started",
		Metadata:  `{"version":"1.0.0"}`,
	}
	err := repo.Save(ctx, event)
	require.NoError(t, err)
	assert.NotZero(t, event.ID, "Save should populate ID")
}

func TestAppEventRepository_SaveNilReturnsError(t *testing.T) {
	t.Parallel()
	db := setupAppEventTestDB(t)
	repo := NewAppEventRepository(db, nil, false, false)
	ctx := t.Context()

	err := repo.Save(ctx, nil)
	require.Error(t, err)
}

func TestAppEventRepository_GetRecent(t *testing.T) {
	t.Parallel()
	db := setupAppEventTestDB(t)
	repo := NewAppEventRepository(db, nil, false, false)
	ctx := t.Context()

	now := time.Now()
	for i := range 5 {
		event := &entities.AppEvent{
			Timestamp: now.Add(time.Duration(i) * time.Minute),
			Category:  "system",
			EventType: "test",
			Message:   "event",
		}
		require.NoError(t, repo.Save(ctx, event))
	}

	events, err := repo.GetRecent(ctx, 3)
	require.NoError(t, err)
	assert.Len(t, events, 3)
	assert.True(t, events[0].Timestamp.After(events[1].Timestamp), "should be ordered newest first")
}

func TestAppEventRepository_GetByCategory(t *testing.T) {
	t.Parallel()
	db := setupAppEventTestDB(t)
	repo := NewAppEventRepository(db, nil, false, false)
	ctx := t.Context()

	now := time.Now()
	categories := []string{"system", "settings", "system", "notification", "system"}
	for i, cat := range categories {
		event := &entities.AppEvent{
			Timestamp: now.Add(time.Duration(i) * time.Minute),
			Category:  cat,
			EventType: "test",
			Message:   "event",
		}
		require.NoError(t, repo.Save(ctx, event))
	}

	events, err := repo.GetByCategory(ctx, "system", 10)
	require.NoError(t, err)
	assert.Len(t, events, 3)
	for _, e := range events {
		assert.Equal(t, "system", e.Category)
	}
}

func TestAppEventRepository_GetSince(t *testing.T) {
	t.Parallel()
	db := setupAppEventTestDB(t)
	repo := NewAppEventRepository(db, nil, false, false)
	ctx := t.Context()

	now := time.Now().Truncate(time.Second)
	for i := range 5 {
		event := &entities.AppEvent{
			Timestamp: now.Add(time.Duration(i) * time.Hour),
			Category:  "system",
			EventType: "test",
			Message:   "event",
		}
		require.NoError(t, repo.Save(ctx, event))
	}

	since := now.Add(2 * time.Hour)
	events, err := repo.GetSince(ctx, since, 10)
	require.NoError(t, err)
	assert.Len(t, events, 3, "should return events at 2h, 3h, and 4h")
}

func TestAppEventRepository_DeleteBefore(t *testing.T) {
	t.Parallel()
	db := setupAppEventTestDB(t)
	repo := NewAppEventRepository(db, nil, false, false)
	ctx := t.Context()

	now := time.Now().Truncate(time.Second)
	for i := range 5 {
		event := &entities.AppEvent{
			Timestamp: now.Add(time.Duration(i) * time.Hour),
			Category:  "system",
			EventType: "test",
			Message:   "event",
		}
		require.NoError(t, repo.Save(ctx, event))
	}

	cutoff := now.Add(3 * time.Hour)
	deleted, err := repo.DeleteBefore(ctx, cutoff)
	require.NoError(t, err)
	assert.Equal(t, int64(3), deleted, "should delete events at 0h, 1h, and 2h")

	count, err := repo.Count(ctx)
	require.NoError(t, err)
	assert.Equal(t, int64(2), count)
}

func TestAppEventRepository_Count(t *testing.T) {
	t.Parallel()
	db := setupAppEventTestDB(t)
	repo := NewAppEventRepository(db, nil, false, false)
	ctx := t.Context()

	count, err := repo.Count(ctx)
	require.NoError(t, err)
	assert.Equal(t, int64(0), count)

	for range 3 {
		event := &entities.AppEvent{
			Timestamp: time.Now(),
			Category:  "system",
			EventType: "test",
			Message:   "event",
		}
		require.NoError(t, repo.Save(ctx, event))
	}

	count, err = repo.Count(ctx)
	require.NoError(t, err)
	assert.Equal(t, int64(3), count)
}

func TestAppEventRepository_V2Prefix(t *testing.T) {
	t.Parallel()
	dbPath := filepath.Join(t.TempDir(), "test.db")
	db, err := gorm.Open(sqlite.Open(dbPath), &gorm.Config{
		Logger: gorm_logger.Default.LogMode(gorm_logger.Silent),
	})
	require.NoError(t, err)

	sqlDB, err := db.DB()
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, sqlDB.Close()) })

	err = db.Exec("CREATE TABLE v2_app_events (id INTEGER PRIMARY KEY AUTOINCREMENT, timestamp DATETIME, category VARCHAR(50), event_type VARCHAR(100), message TEXT, metadata TEXT, created_at DATETIME)").Error
	require.NoError(t, err)

	repo := NewAppEventRepository(db, nil, true, false)
	ctx := t.Context()

	event := &entities.AppEvent{
		Timestamp: time.Now(),
		Category:  "system",
		EventType: "startup",
		Message:   "test",
	}
	err = repo.Save(ctx, event)
	require.NoError(t, err)

	count, err := repo.Count(ctx)
	require.NoError(t, err)
	assert.Equal(t, int64(1), count)
}
