package v2only

import (
	"strconv"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/tphakala/birdnet-go/internal/datastore"
	"github.com/tphakala/birdnet-go/internal/errors"
)

// Regression: species guide on v2-only datastore — found by /qa on 2026-06-19.
// In v2-only mode (the default for fresh installs) the guide cache returned 503
// and notes returned 500 because v2only.Datastore neither exposed a GORM handle
// nor backed species notes. These assert the contract that made it work:
//   - v2only.Datastore satisfies GormDBProvider (guide cache needs the handle)
//   - species notes persist with real CRUD, validation, and not-found semantics
// Report: see /qa session on the feature/species-guide branch.

// Compile-time guarantee that the guide cache can obtain a GORM handle.
var _ datastore.GormDBProvider = (*Datastore)(nil)

func TestV2OnlyDatastore_GormDBExposed(t *testing.T) {
	t.Parallel()
	ds, cleanup := setupTestDatastore(t)
	defer cleanup()
	require.NotNil(t, ds.GormDB(), "guide cache store needs a non-nil GORM handle in v2-only mode")
}

func TestV2OnlyDatastore_SpeciesNoteCRUD(t *testing.T) {
	t.Parallel()
	ds, cleanup := setupTestDatastore(t)
	defer cleanup()
	ctx := t.Context()

	const sci = "Turdus merula"

	// Empty to start (proves the species_notes table exists and is queryable).
	notes, err := ds.GetSpeciesNotes(ctx, sci)
	require.NoError(t, err)
	assert.Empty(t, notes)

	// Create — entry is trimmed, scientific name normalized.
	note := &datastore.SpeciesNote{ScientificName: sci, Entry: "  heard one at dawn  "}
	require.NoError(t, ds.SaveSpeciesNote(ctx, note))
	assert.NotZero(t, note.ID)
	assert.Equal(t, "heard one at dawn", note.Entry, "entry should be trimmed on save")

	// Read by species and by ID.
	notes, err = ds.GetSpeciesNotes(ctx, sci)
	require.NoError(t, err)
	require.Len(t, notes, 1)
	assert.Equal(t, "heard one at dawn", notes[0].Entry)

	got, err := ds.GetSpeciesNoteByID(ctx, note.ID)
	require.NoError(t, err)
	assert.Equal(t, note.ID, got.ID)

	// Update.
	idStr := strconv.FormatUint(uint64(note.ID), 10)
	require.NoError(t, ds.UpdateSpeciesNote(ctx, idStr, "pair nesting in hedge"))
	got, err = ds.GetSpeciesNoteByID(ctx, note.ID)
	require.NoError(t, err)
	assert.Equal(t, "pair nesting in hedge", got.Entry)

	// A second note must sort newest-first.
	require.NoError(t, ds.SaveSpeciesNote(ctx, &datastore.SpeciesNote{ScientificName: sci, Entry: "second sighting"}))
	notes, err = ds.GetSpeciesNotes(ctx, sci)
	require.NoError(t, err)
	require.Len(t, notes, 2)
	assert.Equal(t, "second sighting", notes[0].Entry, "newest note first")

	// Delete the first note; it is then not found.
	require.NoError(t, ds.DeleteSpeciesNote(ctx, idStr))
	_, err = ds.GetSpeciesNoteByID(ctx, note.ID)
	assert.True(t, errors.Is(err, datastore.ErrSpeciesNoteNotFound))
}

func TestV2OnlyDatastore_SpeciesNoteValidationAndNotFound(t *testing.T) {
	t.Parallel()
	ds, cleanup := setupTestDatastore(t)
	defer cleanup()
	ctx := t.Context()

	const sci = "Turdus merula"

	// Whitespace-only entry is rejected as a validation error.
	err := ds.SaveSpeciesNote(ctx, &datastore.SpeciesNote{ScientificName: sci, Entry: "   "})
	require.Error(t, err)
	assert.True(t, errors.IsCategory(err, errors.CategoryValidation), "empty entry must be a validation error")

	// Over-length entry is rejected (the UI surfaces notes.tooLong on this).
	err = ds.SaveSpeciesNote(ctx, &datastore.SpeciesNote{
		ScientificName: sci,
		Entry:          strings.Repeat("x", datastore.SpeciesNoteMaxLength+1),
	})
	require.Error(t, err)
	assert.True(t, errors.IsCategory(err, errors.CategoryValidation), "too-long entry must be a validation error")

	// Empty scientific name on read is a validation error.
	_, err = ds.GetSpeciesNotes(ctx, "   ")
	require.Error(t, err)
	assert.True(t, errors.IsCategory(err, errors.CategoryValidation))

	// Update/Delete of an unknown ID returns the not-found sentinel.
	assert.True(t, errors.Is(ds.UpdateSpeciesNote(ctx, "999999", "x"), datastore.ErrSpeciesNoteNotFound))
	assert.True(t, errors.Is(ds.DeleteSpeciesNote(ctx, "999999"), datastore.ErrSpeciesNoteNotFound))

	// A non-numeric ID is a validation error, not a not-found.
	assert.True(t, errors.IsCategory(ds.DeleteSpeciesNote(ctx, "abc"), errors.CategoryValidation))
}
