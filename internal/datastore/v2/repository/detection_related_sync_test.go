package repository

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/tphakala/birdnet-go/internal/datastore/v2/entities"
)

func TestGetLockedDetectionIDsAfter(t *testing.T) {
	db := setupDetectionTestDB(t)
	repo := NewDetectionRepository(db, nil, false, false)
	ctx := t.Context()

	_, ids := createBulkDetections(t, db, 6)
	// Lock out of insertion order to prove the result is sorted by detection id.
	for _, id := range []uint{ids[4], ids[0], ids[2], ids[5]} {
		require.NoError(t, repo.Lock(ctx, id))
	}

	tests := []struct {
		name  string
		after uint
		limit int
		want  []uint
	}{
		{name: "from the start", after: 0, limit: 10, want: []uint{ids[0], ids[2], ids[4], ids[5]}},
		{name: "after is exclusive", after: ids[2], limit: 10, want: []uint{ids[4], ids[5]}},
		{name: "limit applies in ascending order", after: 0, limit: 2, want: []uint{ids[0], ids[2]}},
		{name: "past the last lock", after: ids[5], limit: 10, want: []uint{}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := repo.GetLockedDetectionIDsAfter(ctx, tt.after, tt.limit)
			require.NoError(t, err)
			assert.Equal(t, tt.want, got)
		})
	}
}

func TestSaveCommentsBatch_LegacyIDDedupe(t *testing.T) {
	db := setupDetectionTestDB(t)
	require.NoError(t, db.AutoMigrate(&entities.DetectionComment{}))
	repo := NewDetectionRepository(db, nil, false, false)
	ctx := t.Context()

	_, ids := createBulkDetections(t, db, 1)
	legacyID := uint(42)
	fromLegacy := func() *entities.DetectionComment {
		id := legacyID
		return &entities.DetectionComment{DetectionID: ids[0], Entry: "copied", LegacyID: &id}
	}

	require.NoError(t, repo.SaveCommentsBatch(ctx, []*entities.DetectionComment{fromLegacy()}))
	require.NoError(t, repo.SaveCommentsBatch(ctx, []*entities.DetectionComment{fromLegacy()}),
		"repeating a legacy comment must be skipped, not fail")
	// Comments created in v2 have no legacy id and must all be stored.
	require.NoError(t, repo.SaveCommentsBatch(ctx, []*entities.DetectionComment{
		{DetectionID: ids[0], Entry: "v2 one"},
		{DetectionID: ids[0], Entry: "v2 two"},
	}))

	var count int64
	require.NoError(t, db.Model(&entities.DetectionComment{}).Count(&count).Error)
	assert.Equal(t, int64(3), count, "one legacy comment once, two v2 comments")
}
