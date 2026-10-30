//go:build integration

package v2

import (
	"context"
	"testing"

	"github.com/stretchr/testify/require"
	"gorm.io/gorm"
)

// Fixture values for TestVerifyMySQLLegacyMigrated. The watermark is the last id
// the migration recorded; ids above it are the tail.
const (
	proofWatermark      = uint(10)
	proofTailID         = proofWatermark + 1
	proofPrefix         = "v2_"
	proofDirtyInsertSQL = "INSERT INTO v2_migration_dirty_ids (detection_id, created_at) VALUES (?, NOW())"
	proofNotesDDL       = "CREATE TABLE `notes` (id INT PRIMARY KEY)"
	proofNotesInsertSQL = "INSERT INTO `notes` (id) VALUES (?)"
	proofChildDDL       = "CREATE TABLE `note_comments` (id INT PRIMARY KEY AUTO_INCREMENT, note_id INT NOT NULL)"
	proofChildInsertSQL = "INSERT INTO `note_comments` (note_id) VALUES (?)"
	proofBadChildDDL    = "CREATE TABLE `results` (id INT PRIMARY KEY AUTO_INCREMENT)"
)

// proofFixture prepares a prefixed v2 schema beside plain legacy tables and
// returns the manager's handle. Legacy tables carry only the columns the proof
// reads, with no foreign keys, so a case can drop notes without ceremony.
func proofFixture(t *testing.T) *gorm.DB {
	t.Helper()
	dropAllTables(t, coexTestDB, coexTestDBName)
	mgr := newTestMySQLManager(t, true)
	require.NoError(t, mgr.Initialize())
	t.Cleanup(func() { _ = mgr.Close() })
	return mgr.DB()
}

// proofSeed seeds one case of TestVerifyMySQLLegacyMigrated.
type proofSeed struct {
	t  *testing.T
	db *gorm.DB
}

// notes creates the legacy notes table with the given ids.
func (s proofSeed) notes(ids ...uint) {
	s.t.Helper()
	require.NoError(s.t, s.db.Exec(proofNotesDDL).Error)
	for _, id := range ids {
		require.NoError(s.t, s.db.Exec(proofNotesInsertSQL, id).Error)
	}
}

// child creates the legacy note_comments table with one row per note id.
func (s proofSeed) child(noteIDs ...uint) {
	s.t.Helper()
	require.NoError(s.t, s.db.Exec(proofChildDDL).Error)
	for _, id := range noteIDs {
		require.NoError(s.t, s.db.Exec(proofChildInsertSQL, id).Error)
	}
}

// inV2 adds detections with the given ids to v2_detections.
func (s proofSeed) inV2(ids ...uint) {
	s.t.Helper()
	for _, id := range ids {
		seedDetection(s.t, id, nil)
	}
}

// TestVerifyMySQLLegacyMigrated pins the MySQL drop-time tail proof for the
// notes-present and notes-absent branches and for read failures.
func TestVerifyMySQLLegacyMigrated(t *testing.T) {
	tests := []struct {
		name   string
		prefix string
		setup  func(s proofSeed)
		// cancel runs the proof with an already cancelled context.
		cancel  bool
		wantErr error
	}{
		{
			name:   "notes ids at or below the watermark",
			prefix: proofPrefix,
			setup:  func(s proofSeed) { s.notes(1, proofWatermark) },
		},
		{
			name:   "tail id present in v2_detections",
			prefix: proofPrefix,
			setup: func(s proofSeed) {
				s.notes(1, proofTailID)
				s.inV2(proofTailID)
			},
		},
		{
			name:    "tail id missing from v2",
			prefix:  proofPrefix,
			setup:   func(s proofSeed) { s.notes(1, proofTailID) },
			wantErr: ErrLegacyRecordsUnmigrated,
		},
		{
			name:   "pending dirty id",
			prefix: proofPrefix,
			setup: func(s proofSeed) {
				s.notes(1)
				require.NoError(s.t, s.db.Exec(proofDirtyInsertSQL, 1).Error)
			},
			wantErr: ErrLegacyRecordsUnmigrated,
		},
		{
			name:   "notes dropped, pending dirty id",
			prefix: proofPrefix,
			setup: func(s proofSeed) {
				s.child(1)
				require.NoError(s.t, s.db.Exec(proofDirtyInsertSQL, 1).Error)
			},
			wantErr: ErrLegacyRecordsUnmigrated,
		},
		{
			name:   "notes dropped, child rows at or below the watermark",
			prefix: proofPrefix,
			setup:  func(s proofSeed) { s.child(1, proofWatermark) },
		},
		{
			name:    "notes dropped, child row above the watermark missing from v2",
			prefix:  proofPrefix,
			setup:   func(s proofSeed) { s.child(1, proofTailID) },
			wantErr: ErrLegacyRecordsUnmigrated,
		},
		{
			name:   "notes dropped, child row above the watermark present in v2",
			prefix: proofPrefix,
			setup: func(s proofSeed) {
				s.child(proofTailID)
				s.inV2(proofTailID)
			},
		},
		{
			name:   "notes dropped, same-named table without note_id",
			prefix: proofPrefix,
			setup: func(s proofSeed) {
				require.NoError(s.t, s.db.Exec(proofBadChildDDL).Error)
			},
			wantErr: ErrLegacyTailUnverified,
		},
		{
			name:    "cancelled context",
			prefix:  proofPrefix,
			setup:   func(s proofSeed) { s.notes(1) },
			cancel:  true,
			wantErr: ErrLegacyTailUnverified,
		},
		{
			name:    "prefix that is not a plain identifier",
			prefix:  "v2`_",
			setup:   func(s proofSeed) { s.notes(1) },
			wantErr: ErrLegacyTailUnverified,
		},
		{
			name:    "empty prefix",
			prefix:  "",
			setup:   func(s proofSeed) { s.notes(1) },
			wantErr: ErrNotLegacyDatabase,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			db := proofFixture(t)
			tt.setup(proofSeed{t: t, db: db})

			ctx := t.Context()
			if tt.cancel {
				var cancel context.CancelFunc
				ctx, cancel = context.WithCancel(ctx)
				cancel()
			}

			err := VerifyMySQLLegacyMigrated(ctx, db, tt.prefix, proofWatermark, testStartupLogger())
			if tt.wantErr == nil {
				require.NoError(t, err)
				return
			}
			require.ErrorIs(t, err, tt.wantErr)
		})
	}
}
