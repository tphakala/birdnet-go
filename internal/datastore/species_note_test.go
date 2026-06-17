// species_note_test.go: unit tests for species guide note operations.
package datastore

import (
	"context"
	"strconv"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"gorm.io/driver/sqlite"
	"gorm.io/gorm"

	"github.com/tphakala/birdnet-go/internal/errors"
)

func setupSpeciesNoteTestDB(t *testing.T) *DataStore {
	t.Helper()
	db, err := gorm.Open(sqlite.Open(":memory:"), &gorm.Config{})
	require.NoError(t, err, "Failed to create test database")
	require.NoError(t, db.AutoMigrate(&SpeciesNote{}), "Failed to migrate schema")
	return &DataStore{DB: db}
}

func TestSpeciesNote_SaveGetUpdateDelete(t *testing.T) {
	t.Parallel()
	ds := setupSpeciesNoteTestDB(t)
	ctx := context.Background()

	note := &SpeciesNote{ScientificName: "Turdus merula", Entry: "Heard singing at dawn."}
	require.NoError(t, ds.SaveSpeciesNote(ctx, note))
	require.NotZero(t, note.ID)

	notes, err := ds.GetSpeciesNotes(ctx, "Turdus merula")
	require.NoError(t, err)
	require.Len(t, notes, 1)
	assert.Equal(t, "Heard singing at dawn.", notes[0].Entry)

	got, err := ds.GetSpeciesNoteByID(ctx, note.ID)
	require.NoError(t, err)
	assert.Equal(t, "Heard singing at dawn.", got.Entry)

	require.NoError(t, ds.UpdateSpeciesNote(ctx, idString(note.ID), "Updated text."))
	got, err = ds.GetSpeciesNoteByID(ctx, note.ID)
	require.NoError(t, err)
	assert.Equal(t, "Updated text.", got.Entry)

	require.NoError(t, ds.DeleteSpeciesNote(ctx, idString(note.ID)))
	_, err = ds.GetSpeciesNoteByID(ctx, note.ID)
	assert.True(t, errors.Is(err, ErrSpeciesNoteNotFound))
}

func TestSpeciesNote_ScientificNameNormalization(t *testing.T) {
	t.Parallel()
	ds := setupSpeciesNoteTestDB(t)
	ctx := context.Background()

	require.NoError(t, ds.SaveSpeciesNote(ctx, &SpeciesNote{
		ScientificName: "  Turdus merula  ", Entry: "note",
	}))

	// Lookup with the un-padded name matches the trimmed stored value.
	notes, err := ds.GetSpeciesNotes(ctx, "Turdus merula")
	require.NoError(t, err)
	assert.Len(t, notes, 1)
}

func TestSpeciesNote_TooLongRejected(t *testing.T) {
	t.Parallel()
	ds := setupSpeciesNoteTestDB(t)
	ctx := context.Background()

	long := strings.Repeat("x", SpeciesNoteMaxLength+1)
	err := ds.SaveSpeciesNote(ctx, &SpeciesNote{ScientificName: "Turdus merula", Entry: long})
	require.Error(t, err)
	assert.True(t, errors.IsCategory(err, errors.CategoryValidation))
}

func TestSpeciesNote_EmptyEntryRejected(t *testing.T) {
	t.Parallel()
	ds := setupSpeciesNoteTestDB(t)
	ctx := context.Background()

	err := ds.SaveSpeciesNote(ctx, &SpeciesNote{ScientificName: "Turdus merula", Entry: "   "})
	require.Error(t, err)
}

func TestSpeciesNote_UpdateDeleteNotFound(t *testing.T) {
	t.Parallel()
	ds := setupSpeciesNoteTestDB(t)
	ctx := context.Background()

	assert.True(t, errors.Is(ds.UpdateSpeciesNote(ctx, "999", "x"), ErrSpeciesNoteNotFound))
	assert.True(t, errors.Is(ds.DeleteSpeciesNote(ctx, "999"), ErrSpeciesNoteNotFound))
}

func TestSpeciesNote_InvalidID(t *testing.T) {
	t.Parallel()
	ds := setupSpeciesNoteTestDB(t)
	ctx := context.Background()

	require.Error(t, ds.UpdateSpeciesNote(ctx, "abc", "x"))
	require.Error(t, ds.DeleteSpeciesNote(ctx, "0"))
}

func TestNormalizeSpeciesNoteEntry(t *testing.T) {
	t.Parallel()

	got, err := NormalizeSpeciesNoteEntry("  hello  ")
	require.NoError(t, err)
	assert.Equal(t, "hello", got)

	_, err = NormalizeSpeciesNoteEntry(strings.Repeat("x", SpeciesNoteMaxLength+1))
	require.Error(t, err)
}

func idString(id uint) string {
	return strconv.FormatUint(uint64(id), 10)
}
