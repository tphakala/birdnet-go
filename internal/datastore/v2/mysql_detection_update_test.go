//go:build integration

package v2

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/tphakala/birdnet-go/internal/datastore/v2/repository"
	"gorm.io/gorm"
)

// TestMySQL_DetectionUpdate_OutcomeByRowState runs detectionRepository.Update
// through the production MySQL DSN against every server the suite is pointed
// at. An UPDATE that leaves the stored values as they are must still count as
// a match, and the not-found and locked outcomes must stay distinguishable.
func TestMySQL_DetectionUpdate_OutcomeByRowState(t *testing.T) {
	dropAllTables(t, coexTestDB, coexTestDBName)

	mgr := newTestMySQLManager(t, true)
	require.NoError(t, mgr.Initialize())
	t.Cleanup(func() { _ = mgr.Close() })

	repo := repository.NewDetectionRepository(mgr.DB(), nil, true, true)

	const (
		openID    = uint(1)
		lockedID  = uint(2)
		missingID = uint(99)
		clipName  = "clip.wav"
	)

	// Explicit ids with foreign key checks off, so no lookup rows are needed.
	require.NoError(t, withForeignKeyChecksOff(coexTestDB, func(tx *gorm.DB) error {
		for _, id := range []uint{openID, lockedID} {
			if err := tx.Exec(
				"INSERT INTO v2_detections (id, model_id, label_id, detected_at, confidence, clip_name) VALUES (?, 1, 1, ?, 0.9, ?)",
				id, 1000+id, clipName).Error; err != nil {
				return err
			}
		}
		return nil
	}))
	require.NoError(t, repo.Lock(t.Context(), lockedID))

	storedClip := func(id uint) *string {
		det, err := repo.Get(t.Context(), id)
		require.NoError(t, err)
		return det.ClipName
	}

	tests := []struct {
		name    string
		id      uint
		updates map[string]any
		wantErr error
		check   func(t *testing.T)
	}{
		{
			name:    "values equal to the stored ones succeed",
			id:      openID,
			updates: map[string]any{"clip_name": clipName, "confidence": 0.9},
		},
		{
			name:    "clearing a clip name succeeds",
			id:      openID,
			updates: map[string]any{"clip_name": nil},
		},
		{
			name:    "clearing an already cleared clip name succeeds",
			id:      openID,
			updates: map[string]any{"clip_name": nil},
			check:   func(t *testing.T) { assert.Nil(t, storedClip(openID)) },
		},
		{
			name:    "a changed value on a locked detection is refused",
			id:      lockedID,
			updates: map[string]any{"clip_name": "other.wav"},
			wantErr: repository.ErrDetectionLocked,
			check: func(t *testing.T) {
				got := storedClip(lockedID)
				require.NotNil(t, got)
				assert.Equal(t, clipName, *got)
			},
		},
		{
			name:    "an unchanged value on a locked detection is still refused",
			id:      lockedID,
			updates: map[string]any{"clip_name": clipName},
			wantErr: repository.ErrDetectionLocked,
		},
		{
			name:    "a missing detection is not found",
			id:      missingID,
			updates: map[string]any{"clip_name": nil},
			wantErr: repository.ErrDetectionNotFound,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := repo.Update(t.Context(), tt.id, tt.updates)
			if tt.wantErr != nil {
				require.ErrorIs(t, err, tt.wantErr)
			} else {
				require.NoError(t, err)
			}
			if tt.check != nil {
				tt.check(t)
			}
		})
	}
}
