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

const testDisplayNameSnowball = "Blue Snowball"

func setupAudioSourceTestDB(t *testing.T) *gorm.DB {
	t.Helper()
	dbPath := filepath.Join(t.TempDir(), "test.db")
	db, err := gorm.Open(sqlite.Open(dbPath), &gorm.Config{
		Logger: gorm_logger.Default.LogMode(gorm_logger.Silent),
	})
	require.NoError(t, err, "failed to open test database")

	sqlDB, err := db.DB()
	require.NoError(t, err, "failed to get sql.DB")
	t.Cleanup(func() { _ = sqlDB.Close() })

	err = db.AutoMigrate(&entities.AudioSource{})
	require.NoError(t, err, "failed to migrate audio_sources table")
	return db
}

func TestGetOrCreate_CreatesNewSource(t *testing.T) {
	t.Parallel()
	db := setupAudioSourceTestDB(t)
	repo := NewAudioSourceRepository(db, nil, false, false)
	ctx := t.Context()

	name := testDisplayNameSnowball
	source, err := repo.GetOrCreate(ctx, "hw:0,0", "node1", &name, entities.SourceTypeALSA)
	require.NoError(t, err)
	assert.Equal(t, "hw:0,0", source.SourceURI)
	assert.Equal(t, "node1", source.NodeName)
	require.NotNil(t, source.DisplayName)
	assert.Equal(t, testDisplayNameSnowball, *source.DisplayName)
	assert.Equal(t, entities.SourceTypeALSA, source.SourceType)
}

func TestGetOrCreate_ReturnsSameSourceForSameURI(t *testing.T) {
	t.Parallel()
	db := setupAudioSourceTestDB(t)
	repo := NewAudioSourceRepository(db, nil, false, false)
	ctx := t.Context()

	name := "Mic One"
	first, err := repo.GetOrCreate(ctx, "hw:1,0", "node1", &name, entities.SourceTypeALSA)
	require.NoError(t, err)

	second, err := repo.GetOrCreate(ctx, "hw:1,0", "node1", &name, entities.SourceTypeALSA)
	require.NoError(t, err)
	assert.Equal(t, first.ID, second.ID)
}

func TestGetOrCreate_UpdatesDisplayNameOnDeviceSwap(t *testing.T) {
	t.Parallel()
	db := setupAudioSourceTestDB(t)
	repo := NewAudioSourceRepository(db, nil, false, false)
	ctx := t.Context()

	oldName := testDisplayNameSnowball
	source, err := repo.GetOrCreate(ctx, "hw:0,0", "node1", &oldName, entities.SourceTypeALSA)
	require.NoError(t, err)
	originalID := source.ID
	require.NotNil(t, source.DisplayName)
	assert.Equal(t, testDisplayNameSnowball, *source.DisplayName)

	// Same ALSA path, different mic plugged in
	newName := "RODE AI-Micro"
	source, err = repo.GetOrCreate(ctx, "hw:0,0", "node1", &newName, entities.SourceTypeALSA)
	require.NoError(t, err)
	assert.Equal(t, originalID, source.ID, "should return the same row, not create a new one")
	require.NotNil(t, source.DisplayName)
	assert.Equal(t, "RODE AI-Micro", *source.DisplayName, "display name should be updated")

	// Verify the update was persisted to the database
	fetched, err := repo.GetByID(ctx, originalID)
	require.NoError(t, err)
	require.NotNil(t, fetched.DisplayName)
	assert.Equal(t, "RODE AI-Micro", *fetched.DisplayName, "updated name should be persisted in DB")
}

func TestGetOrCreate_DoesNotUpdateWhenNameUnchanged(t *testing.T) {
	t.Parallel()
	db := setupAudioSourceTestDB(t)
	repo := NewAudioSourceRepository(db, nil, false, false)
	ctx := t.Context()

	name := "My Mic"
	source, err := repo.GetOrCreate(ctx, "hw:2,0", "node1", &name, entities.SourceTypeALSA)
	require.NoError(t, err)
	require.NotNil(t, source.DisplayName)
	assert.Equal(t, "My Mic", *source.DisplayName)

	// Call again with the same name; should be a no-op
	sameName := "My Mic"
	source, err = repo.GetOrCreate(ctx, "hw:2,0", "node1", &sameName, entities.SourceTypeALSA)
	require.NoError(t, err)
	require.NotNil(t, source.DisplayName)
	assert.Equal(t, "My Mic", *source.DisplayName)
}

func TestGetOrCreate_SkipsUpdateForEmptyDisplayName(t *testing.T) {
	t.Parallel()
	db := setupAudioSourceTestDB(t)
	repo := NewAudioSourceRepository(db, nil, false, false)
	ctx := t.Context()

	original := testDisplayNameSnowball
	source, err := repo.GetOrCreate(ctx, "hw:3,0", "node1", &original, entities.SourceTypeALSA)
	require.NoError(t, err)
	require.NotNil(t, source.DisplayName)
	assert.Equal(t, testDisplayNameSnowball, *source.DisplayName)

	// Empty display name should not overwrite the existing one
	empty := ""
	source, err = repo.GetOrCreate(ctx, "hw:3,0", "node1", &empty, entities.SourceTypeALSA)
	require.NoError(t, err)
	require.NotNil(t, source.DisplayName)
	assert.Equal(t, testDisplayNameSnowball, *source.DisplayName, "empty name should not overwrite existing")
}

func TestGetOrCreate_SkipsUpdateForNilDisplayName(t *testing.T) {
	t.Parallel()
	db := setupAudioSourceTestDB(t)
	repo := NewAudioSourceRepository(db, nil, false, false)
	ctx := t.Context()

	original := testDisplayNameSnowball
	source, err := repo.GetOrCreate(ctx, "hw:4,0", "node1", &original, entities.SourceTypeALSA)
	require.NoError(t, err)
	require.NotNil(t, source.DisplayName)
	assert.Equal(t, testDisplayNameSnowball, *source.DisplayName)

	// Nil display name should not overwrite the existing one
	source, err = repo.GetOrCreate(ctx, "hw:4,0", "node1", nil, entities.SourceTypeALSA)
	require.NoError(t, err)
	require.NotNil(t, source.DisplayName)
	assert.Equal(t, testDisplayNameSnowball, *source.DisplayName, "nil name should not overwrite existing")
}

func TestGetOrCreate_SetsDisplayNameWhenInitiallyNil(t *testing.T) {
	t.Parallel()
	db := setupAudioSourceTestDB(t)
	repo := NewAudioSourceRepository(db, nil, false, false)
	ctx := t.Context()

	source, err := repo.GetOrCreate(ctx, "hw:5,0", "node1", nil, entities.SourceTypeALSA)
	require.NoError(t, err)
	assert.Nil(t, source.DisplayName)

	name := "New Mic"
	source, err = repo.GetOrCreate(ctx, "hw:5,0", "node1", &name, entities.SourceTypeALSA)
	require.NoError(t, err)
	require.NotNil(t, source.DisplayName)
	assert.Equal(t, "New Mic", *source.DisplayName)

	fetched, err := repo.GetByID(ctx, source.ID)
	require.NoError(t, err)
	require.NotNil(t, fetched.DisplayName)
	assert.Equal(t, "New Mic", *fetched.DisplayName)
}

func TestGetOrCreate_AutoDetectsSourceType(t *testing.T) {
	t.Parallel()
	db := setupAudioSourceTestDB(t)
	repo := NewAudioSourceRepository(db, nil, false, false)
	ctx := t.Context()

	name := "Camera"
	source, err := repo.GetOrCreate(ctx, "rtsp://192.168.1.10/stream", "node1", &name, "")
	require.NoError(t, err)
	assert.Equal(t, entities.SourceTypeRTSP, source.SourceType)
}
