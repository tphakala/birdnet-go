//go:build integration

package v2

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/tphakala/birdnet-go/internal/datastore/v2/repository"
	"gorm.io/gorm"
)

// Values of the detection rows these tests seed. The equal-values case in
// TestMySQL_DetectionUpdate_OutcomeByRowState sends seededConfidence back, so
// the row and that update cannot drift apart.
const (
	seededModelID      = 1
	seededLabelID      = 1
	seededDetectedAt   = 1000
	seededConfidence   = 0.9
	seededClipName     = "clip.wav"
	insertDetectionSQL = "INSERT INTO v2_detections (id, model_id, label_id, detected_at, confidence, clip_name) VALUES (?, ?, ?, ?, ?, ?)"
)

// seedDetection inserts a v2 detection with an explicit id and foreign key
// checks off, so no lookup rows are needed. A nil clipName stores NULL.
func seedDetection(t *testing.T, id uint, clipName *string) {
	t.Helper()
	require.NoError(t, withForeignKeyChecksOff(coexTestDB, func(tx *gorm.DB) error {
		return tx.Exec(insertDetectionSQL,
			id, seededModelID, seededLabelID, seededDetectedAt, seededConfidence, clipName).Error
	}))
}

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

	// Every case that changes its row owns one, so no case depends on another.
	const (
		equalID   = uint(1)
		clearID   = uint(2)
		nullID    = uint(3)
		lockedID  = uint(4)
		missingID = uint(99)
	)
	clip := seededClipName
	seedDetection(t, equalID, &clip)
	seedDetection(t, clearID, &clip)
	seedDetection(t, nullID, nil)
	seedDetection(t, lockedID, &clip)
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
		// checkClip asserts the stored clip_name afterwards: wantClip, or NULL when empty.
		checkClip bool
		wantClip  string
	}{
		{
			name:      "values equal to the stored ones succeed",
			id:        equalID,
			updates:   map[string]any{"clip_name": seededClipName, "confidence": seededConfidence},
			checkClip: true,
			wantClip:  seededClipName,
		},
		{
			name:      "clearing a clip name succeeds",
			id:        clearID,
			updates:   map[string]any{"clip_name": nil},
			checkClip: true,
		},
		{
			name:      "clearing an already cleared clip name succeeds",
			id:        nullID,
			updates:   map[string]any{"clip_name": nil},
			checkClip: true,
		},
		{
			name:      "a changed value on a locked detection is refused",
			id:        lockedID,
			updates:   map[string]any{"clip_name": "other.wav"},
			wantErr:   repository.ErrDetectionLocked,
			checkClip: true,
			wantClip:  seededClipName,
		},
		{
			name:    "an unchanged value on a locked detection is still refused",
			id:      lockedID,
			updates: map[string]any{"clip_name": seededClipName},
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
			if !tt.checkClip {
				return
			}
			got := storedClip(tt.id)
			if tt.wantClip == "" {
				assert.Nil(t, got)
				return
			}
			require.NotNil(t, got)
			assert.Equal(t, tt.wantClip, *got)
		})
	}
}

// TestMySQL_ClipSampleRate_ReportsOnlyRealChanges pins that, with matched-row
// counting, FillClipSampleRate still reports true only when it filled a NULL
// rate, and that RecordClipSampleRate overwrites a stored rate.
func TestMySQL_ClipSampleRate_ReportsOnlyRealChanges(t *testing.T) {
	dropAllTables(t, coexTestDB, coexTestDBName)

	mgr := newTestMySQLManager(t, true)
	require.NoError(t, mgr.Initialize())
	t.Cleanup(func() { _ = mgr.Close() })

	repo := repository.NewDetectionRepository(mgr.DB(), nil, true, true)

	const (
		detectionID        = uint(1)
		sampleRate         = 48000
		recordedSampleRate = 44100
	)
	clip := seededClipName
	seedDetection(t, detectionID, &clip)

	filled, err := repo.FillClipSampleRate(t.Context(), detectionID, seededClipName, sampleRate)
	require.NoError(t, err)
	assert.True(t, filled, "a NULL rate is filled")

	filled, err = repo.FillClipSampleRate(t.Context(), detectionID, seededClipName, sampleRate)
	require.NoError(t, err)
	assert.False(t, filled, "a stored rate is not filled again")

	require.NoError(t, repo.RecordClipSampleRate(t.Context(), detectionID, seededClipName, recordedSampleRate))
	det, err := repo.Get(t.Context(), detectionID)
	require.NoError(t, err)
	require.NotNil(t, det.ClipSampleRate)
	assert.Equal(t, recordedSampleRate, *det.ClipSampleRate)
}
