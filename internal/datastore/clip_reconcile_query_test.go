package datastore

import (
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestGetNoteClipReferences verifies the keyset-paginated read used by the clip
// reconcile crawler: it returns only rows with a non-empty clip_name, ordered by
// ID ascending, honors afterID and limit, and maps EndTime to CompletionTime.
func TestGetNoteClipReferences(t *testing.T) {
	ds := setupTestDB(t)

	end1 := time.Date(2024, 1, 15, 8, 31, 0, 0, time.UTC)
	end3 := time.Date(2024, 1, 16, 10, 1, 0, 0, time.UTC)
	notes := []Note{
		{ID: 1, Date: "2024-01-15", Time: "08:30:00", ClipName: "2024/01/a.wav", EndTime: end1},
		{ID: 2, Date: "2024-01-15", Time: "09:15:00", ClipName: ""}, // no clip -> excluded
		{ID: 3, Date: "2024-01-16", Time: "10:00:00", ClipName: "2024/01/c.wav", EndTime: end3},
		{ID: 4, Date: "2024-01-16", Time: "11:00:00", ClipName: "2024/01/d.wav"},
	}
	require.NoError(t, ds.DB.Create(&notes).Error)

	t.Run("filters empty clip_name and orders by id", func(t *testing.T) {
		refs, err := ds.GetNoteClipReferences(0, 10)
		require.NoError(t, err)
		require.Len(t, refs, 3, "row with empty clip_name must be excluded")

		assert.Equal(t, uint(1), refs[0].ID)
		assert.Equal(t, "2024/01/a.wav", refs[0].ClipName)
		assert.True(t, refs[0].CompletionTime.Equal(end1), "CompletionTime must be EndTime")
		assert.Equal(t, uint(3), refs[1].ID)
		assert.Equal(t, uint(4), refs[2].ID)
	})

	t.Run("honors afterID for keyset pagination", func(t *testing.T) {
		refs, err := ds.GetNoteClipReferences(1, 10)
		require.NoError(t, err)
		require.Len(t, refs, 2)
		assert.Equal(t, uint(3), refs[0].ID, "must return rows with id > afterID")
		assert.Equal(t, uint(4), refs[1].ID)
	})

	t.Run("honors limit", func(t *testing.T) {
		refs, err := ds.GetNoteClipReferences(0, 1)
		require.NoError(t, err)
		require.Len(t, refs, 1)
		assert.Equal(t, uint(1), refs[0].ID)
	})

	t.Run("rejects non-positive limit", func(t *testing.T) {
		_, err := ds.GetNoteClipReferences(0, 0)
		assert.Error(t, err)
	})
}
