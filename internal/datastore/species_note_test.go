// species_note_test.go: unit tests for species guide note operations.
package datastore

import (
	"math"
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
	// Pin to a single connection: a bare `:memory:` DSN gives each pooled
	// connection its own independent database, so a write on one connection is
	// invisible to a read on another, making CRUD round-trips flaky.
	sqlDB, err := db.DB()
	require.NoError(t, err)
	sqlDB.SetMaxOpenConns(1)
	t.Cleanup(func() { _ = sqlDB.Close() })
	require.NoError(t, db.AutoMigrate(&SpeciesNote{}), "Failed to migrate schema")
	return &DataStore{DB: db}
}

func TestSpeciesNote_SaveGetUpdateDelete(t *testing.T) {
	t.Parallel()
	ds := setupSpeciesNoteTestDB(t)
	ctx := t.Context()

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
	ctx := t.Context()

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
	ctx := t.Context()

	long := strings.Repeat("x", SpeciesNoteMaxLength+1)
	err := ds.SaveSpeciesNote(ctx, &SpeciesNote{ScientificName: "Turdus merula", Entry: long})
	require.Error(t, err)
	assert.True(t, errors.IsCategory(err, errors.CategoryValidation))
}

func TestSpeciesNote_EmptyEntryRejected(t *testing.T) {
	t.Parallel()
	ds := setupSpeciesNoteTestDB(t)
	ctx := t.Context()

	err := ds.SaveSpeciesNote(ctx, &SpeciesNote{ScientificName: "Turdus merula", Entry: "   "})
	require.Error(t, err)
}

func TestSpeciesNote_UpdateDeleteNotFound(t *testing.T) {
	t.Parallel()
	ds := setupSpeciesNoteTestDB(t)
	ctx := t.Context()

	assert.True(t, errors.Is(ds.UpdateSpeciesNote(ctx, "999", "x"), ErrSpeciesNoteNotFound))
	assert.True(t, errors.Is(ds.DeleteSpeciesNote(ctx, "999"), ErrSpeciesNoteNotFound))
}

func TestSpeciesNote_InvalidID(t *testing.T) {
	t.Parallel()
	ds := setupSpeciesNoteTestDB(t)
	ctx := t.Context()

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

func TestParseSpeciesNoteID(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name    string
		input   string
		want    uint
		wantErr bool
	}{
		{name: "valid", input: "42", want: 42},
		{name: "leading/trailing space", input: "  7 ", want: 7},
		{name: "zero rejected", input: "0", wantErr: true},
		{name: "non-numeric rejected", input: "abc", wantErr: true},
		{name: "empty rejected", input: "", wantErr: true},
		{name: "negative rejected", input: "-1", wantErr: true},
		// Above math.MaxUint64 fails ParseUint outright.
		{name: "overflow uint64 rejected", input: "18446744073709551616", wantErr: true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			got, err := parseSpeciesNoteID(tt.input)
			if tt.wantErr {
				require.Error(t, err)
				return
			}
			require.NoError(t, err)
			assert.Equal(t, tt.want, got)
		})
	}

	// A value above math.MaxUint32 must never wrap: on 32-bit builds (uint is
	// 32-bit) it is rejected; on 64-bit it parses to its exact value rather than
	// truncating. Build the input from math.MaxUint32 so there is no uint literal
	// that would overflow at compile time on 32-bit.
	t.Run("above uint32 does not wrap", func(t *testing.T) {
		t.Parallel()
		want := uint64(math.MaxUint32) + 1
		got, err := parseSpeciesNoteID(strconv.FormatUint(want, 10))
		if math.MaxUint < math.MaxUint64 { // 32-bit platform
			require.Error(t, err)
			return
		}
		require.NoError(t, err)
		assert.Equal(t, want, uint64(got))
	})
}

func idString(id uint) string {
	return strconv.FormatUint(uint64(id), 10)
}
