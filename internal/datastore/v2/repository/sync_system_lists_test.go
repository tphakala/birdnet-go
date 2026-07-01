package repository

import (
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/tphakala/birdnet-go/internal/conf"
	"github.com/tphakala/birdnet-go/internal/datastore/v2/entities"
	"gorm.io/driver/sqlite"
	"gorm.io/gorm"
	gorm_logger "gorm.io/gorm/logger"
)

func setupSpeciesListsTestDB(t *testing.T) *gorm.DB {
	t.Helper()
	dbPath := filepath.Join(t.TempDir(), "test.db")
	db, err := gorm.Open(sqlite.Open(dbPath), &gorm.Config{
		Logger: gorm_logger.Default.LogMode(gorm_logger.Silent),
	})
	require.NoError(t, err, "failed to open test database")

	sqlDB, err := db.DB()
	require.NoError(t, err, "failed to get sql.DB")
	t.Cleanup(func() { require.NoError(t, sqlDB.Close(), "failed to close test database") })

	err = db.AutoMigrate(&entities.SpeciesList{}, &entities.SpeciesListMember{}, &entities.AlertRule{}, &entities.AlertCondition{})
	require.NoError(t, err, "failed to migrate species lists tables")
	return db
}

func TestSyncSystemLists(t *testing.T) {
	db := setupSpeciesListsTestDB(t)
	ctx := t.Context()

	settings := &conf.Settings{}
	settings.Realtime.ExtendedCapture.Species = []string{"Turdus merula", "cyanistes caeruleus", "  "}
	settings.Realtime.DogBarkFilter.Species = []string{"Canis lupus"}
	settings.Realtime.DaylightFilter.Species = []string{"Passer domesticus", "turdus merula"}

	// Sync initially
	err := SyncSystemLists(ctx, db, settings)
	require.NoError(t, err)

	// Verify lists exist
	var lists []entities.SpeciesList
	err = db.Preload("Members").Find(&lists).Error
	require.NoError(t, err)
	assert.Len(t, lists, 3)

	listMap := make(map[string]entities.SpeciesList)
	for _, l := range lists {
		listMap[l.Name] = l
		assert.True(t, l.IsSystem)
	}

	// Verify Extended Capture list
	extCapList, ok := listMap["YAML: Extended Capture"]
	require.True(t, ok)
	assert.Len(t, extCapList.Members, 2)
	assert.Equal(t, "turdus merula", extCapList.Members[0].ScientificName)
	assert.Equal(t, "cyanistes caeruleus", extCapList.Members[1].ScientificName)

	// Verify Dog Bark list
	dogBarkList, ok := listMap["YAML: Dog Bark Filter"]
	require.True(t, ok)
	assert.Len(t, dogBarkList.Members, 1)
	assert.Equal(t, "canis lupus", dogBarkList.Members[0].ScientificName)

	// Update list entries and synchronize again
	settings.Realtime.ExtendedCapture.Species = []string{"Cyanistes caeruleus", "Passer domesticus"}
	err = SyncSystemLists(ctx, db, settings)
	require.NoError(t, err)

	// Reload lists
	err = db.Preload("Members").Find(&lists).Error
	require.NoError(t, err)
	listMap = make(map[string]entities.SpeciesList)
	for _, l := range lists {
		listMap[l.Name] = l
	}

	extCapList = listMap["YAML: Extended Capture"]
	assert.Len(t, extCapList.Members, 2)
	assert.Equal(t, "cyanistes caeruleus", extCapList.Members[0].ScientificName)
	assert.Equal(t, "passer domesticus", extCapList.Members[1].ScientificName)
}
