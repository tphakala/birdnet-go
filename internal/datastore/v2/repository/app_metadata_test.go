package repository

import (
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/tphakala/birdnet-go/internal/datastore/v2/entities"
	"gorm.io/driver/sqlite"
	"gorm.io/gorm"
	gorm_logger "gorm.io/gorm/logger"
)

// setupAppMetadataTestDB creates an in-memory SQLite database for app metadata tests.
// Each call returns an isolated database using a temp file so parallel tests don't collide.
func setupAppMetadataTestDB(t *testing.T) *gorm.DB {
	t.Helper()
	dbPath := filepath.Join(t.TempDir(), "test.db")
	db, err := gorm.Open(sqlite.Open(dbPath), &gorm.Config{
		Logger: gorm_logger.Default.LogMode(gorm_logger.Silent),
	})
	require.NoError(t, err, "failed to open test database")

	sqlDB, err := db.DB()
	require.NoError(t, err, "failed to get sql.DB")
	t.Cleanup(func() { _ = sqlDB.Close() })

	err = db.AutoMigrate(&entities.AppMetadata{})
	require.NoError(t, err, "failed to migrate app_metadata table")
	return db
}

func TestAppMetadataRepository_GetEmpty(t *testing.T) {
	t.Parallel()
	db := setupAppMetadataTestDB(t)
	repo := NewAppMetadataRepository(db, false, false)
	ctx := t.Context()

	value, err := repo.Get(ctx, "nonexistent_key")
	require.NoError(t, err)
	assert.Empty(t, value, "Get on empty table should return empty string")
}

func TestAppMetadataRepository_SetThenGet(t *testing.T) {
	t.Parallel()
	db := setupAppMetadataTestDB(t)
	repo := NewAppMetadataRepository(db, false, false)
	ctx := t.Context()

	err := repo.Set(ctx, "my_key", "my_value")
	require.NoError(t, err)

	value, err := repo.Get(ctx, "my_key")
	require.NoError(t, err)
	assert.Equal(t, "my_value", value)
}

func TestAppMetadataRepository_SetOverwrites(t *testing.T) {
	t.Parallel()
	db := setupAppMetadataTestDB(t)
	repo := NewAppMetadataRepository(db, false, false)
	ctx := t.Context()

	err := repo.Set(ctx, "version", "v1.0.0")
	require.NoError(t, err)

	err = repo.Set(ctx, "version", "v2.0.0")
	require.NoError(t, err)

	value, err := repo.Get(ctx, "version")
	require.NoError(t, err)
	assert.Equal(t, "v2.0.0", value, "Set should upsert, overwriting the previous value")
}

func TestAppMetadataRepository_GetDifferentKeys(t *testing.T) {
	t.Parallel()
	db := setupAppMetadataTestDB(t)
	repo := NewAppMetadataRepository(db, false, false)
	ctx := t.Context()

	err := repo.Set(ctx, "key_a", "value_a")
	require.NoError(t, err)
	err = repo.Set(ctx, "key_b", "value_b")
	require.NoError(t, err)

	valueA, err := repo.Get(ctx, "key_a")
	require.NoError(t, err)
	assert.Equal(t, "value_a", valueA)

	valueB, err := repo.Get(ctx, "key_b")
	require.NoError(t, err)
	assert.Equal(t, "value_b", valueB)

	// Non-existent key should still return empty
	valueC, err := repo.Get(ctx, "key_c")
	require.NoError(t, err)
	assert.Empty(t, valueC)
}

func TestAppMetadataRepository_SetEmptyValue(t *testing.T) {
	t.Parallel()
	db := setupAppMetadataTestDB(t)
	repo := NewAppMetadataRepository(db, false, false)
	ctx := t.Context()

	err := repo.Set(ctx, "empty_key", "")
	require.NoError(t, err)

	value, err := repo.Get(ctx, "empty_key")
	require.NoError(t, err)
	assert.Empty(t, value, "Empty string value should be retrievable")
}
