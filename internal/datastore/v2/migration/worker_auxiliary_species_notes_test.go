package migration

import (
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"gorm.io/driver/sqlite"
	"gorm.io/gorm"

	"github.com/tphakala/birdnet-go/internal/datastore"
	"github.com/tphakala/birdnet-go/internal/datastore/mocks"
)

// legacyStoreWithGorm is a datastore.Interface that also exposes a GORM handle,
// matching the real legacy *DataStore. The generated mock alone does not satisfy
// datastore.GormDBProvider, and without it migrateSpeciesNotes skips.
type legacyStoreWithGorm struct {
	*mocks.MockInterface
	db *gorm.DB
}

func (l *legacyStoreWithGorm) GormDB() *gorm.DB { return l.db }

func newMemoryDB(t *testing.T) *gorm.DB {
	t.Helper()
	db, err := gorm.Open(sqlite.Open(":memory:"), &gorm.Config{})
	require.NoError(t, err, "failed to open in-memory sqlite")
	return db
}

// newSpeciesNotesMigrator wires a migrator with only the pieces species-notes
// migration touches; every other section no-ops on its nil repo.
func newSpeciesNotesMigrator(t *testing.T, legacyDB, v2DB *gorm.DB) *AuxiliaryMigrator {
	t.Helper()
	return NewAuxiliaryMigrator(&AuxiliaryMigratorConfig{
		LegacyStore: &legacyStoreWithGorm{MockInterface: mocks.NewMockInterface(t), db: legacyDB},
		LabelRepo:   nil,
		V2DB:        v2DB,
		Logger:      testLogger(),
	})
}

func TestMigrateSpeciesNotes_CopiesRowsPreservingIDsAndTimestamps(t *testing.T) {
	t.Parallel()

	legacyDB := newMemoryDB(t)
	v2DB := newMemoryDB(t)
	require.NoError(t, legacyDB.AutoMigrate(&datastore.SpeciesNote{}))

	// Distinct, deliberately-old timestamps: the notes list is ordered by
	// created_at, so a migration that re-stamps them would reorder the user's
	// history. Truncated to seconds so the SQLite round-trip compares exactly.
	older := time.Now().Add(-72 * time.Hour).UTC().Truncate(time.Second)
	newer := time.Now().Add(-1 * time.Hour).UTC().Truncate(time.Second)

	seeded := []datastore.SpeciesNote{
		{ID: 1, ScientificName: "Parus major", Entry: "heard at dawn", CreatedAt: older, UpdatedAt: older},
		{ID: 2, ScientificName: "Parus major", Entry: "second note", CreatedAt: newer, UpdatedAt: newer},
		{ID: 3, ScientificName: "Turdus merula", Entry: "nesting in hedge", CreatedAt: newer, UpdatedAt: newer},
	}
	require.NoError(t, legacyDB.Select("ID", "ScientificName", "Entry", "CreatedAt", "UpdatedAt").
		Create(&seeded).Error)

	result := &AuxiliaryMigrationResult{}
	newSpeciesNotesMigrator(t, legacyDB, v2DB).migrateSpeciesNotes(t.Context(), result)

	require.NoError(t, result.SpeciesNotes.Error)
	assert.Equal(t, 3, result.SpeciesNotes.Total, "should count every legacy note")
	assert.Equal(t, 3, result.SpeciesNotes.Migrated, "should migrate every legacy note")

	var got []datastore.SpeciesNote
	require.NoError(t, v2DB.Order("id ASC").Find(&got).Error)
	require.Len(t, got, 3, "all notes must land in v2")

	for i := range seeded {
		assert.Equal(t, seeded[i].ID, got[i].ID, "note ID must survive migration")
		assert.Equal(t, seeded[i].ScientificName, got[i].ScientificName)
		assert.Equal(t, seeded[i].Entry, got[i].Entry)
		assert.Equal(t, seeded[i].CreatedAt.UTC(), got[i].CreatedAt.UTC(),
			"created_at must survive: the notes list is ordered by it")
		assert.Equal(t, seeded[i].UpdatedAt.UTC(), got[i].UpdatedAt.UTC(),
			"updated_at must survive migration")
	}
}

func TestMigrateSpeciesNotes_LegacyTableAbsentIsNotAnError(t *testing.T) {
	t.Parallel()

	// A database written before the species guide existed has no species_notes
	// table. That is the common upgrade path, not a failure.
	legacyDB := newMemoryDB(t)
	v2DB := newMemoryDB(t)

	result := &AuxiliaryMigrationResult{}
	newSpeciesNotesMigrator(t, legacyDB, v2DB).migrateSpeciesNotes(t.Context(), result)

	require.NoError(t, result.SpeciesNotes.Error)
	assert.Equal(t, 0, result.SpeciesNotes.Total)
	assert.Equal(t, 0, result.SpeciesNotes.Migrated)
	assert.False(t, v2DB.Migrator().HasTable(&datastore.SpeciesNote{}),
		"no table should be created when there is nothing to migrate")
}

func TestMigrateSpeciesNotes_EmptyLegacyTableCreatesNothing(t *testing.T) {
	t.Parallel()

	legacyDB := newMemoryDB(t)
	v2DB := newMemoryDB(t)
	require.NoError(t, legacyDB.AutoMigrate(&datastore.SpeciesNote{}))

	result := &AuxiliaryMigrationResult{}
	newSpeciesNotesMigrator(t, legacyDB, v2DB).migrateSpeciesNotes(t.Context(), result)

	require.NoError(t, result.SpeciesNotes.Error)
	assert.Equal(t, 0, result.SpeciesNotes.Total)
}

func TestMigrateSpeciesNotes_SkippedWhenV2HandleMissing(t *testing.T) {
	t.Parallel()

	legacyDB := newMemoryDB(t)
	require.NoError(t, legacyDB.AutoMigrate(&datastore.SpeciesNote{}))
	require.NoError(t, legacyDB.Create(&datastore.SpeciesNote{
		ScientificName: "Parus major", Entry: "note",
	}).Error)

	result := &AuxiliaryMigrationResult{}
	newSpeciesNotesMigrator(t, legacyDB, nil).migrateSpeciesNotes(t.Context(), result)

	require.NoError(t, result.SpeciesNotes.Error, "a missing v2 handle skips, it does not fail")
	assert.Equal(t, 0, result.SpeciesNotes.Total)
}

// TestMigrateSpeciesNotes_SkippedWithoutGormProvider pins the negative branch: a
// legacy store that cannot expose a GORM handle is skipped rather than panicking.
func TestMigrateSpeciesNotes_SkippedWithoutGormProvider(t *testing.T) {
	t.Parallel()

	v2DB := newMemoryDB(t)
	migrator := NewAuxiliaryMigrator(&AuxiliaryMigratorConfig{
		LegacyStore: mocks.NewMockInterface(t),
		V2DB:        v2DB,
		Logger:      testLogger(),
	})

	result := &AuxiliaryMigrationResult{}
	migrator.migrateSpeciesNotes(t.Context(), result)

	require.NoError(t, result.SpeciesNotes.Error)
	assert.Equal(t, 0, result.SpeciesNotes.Total)
}
