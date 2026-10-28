package v2

import (
	"context"

	"github.com/tphakala/birdnet-go/internal/errors"
	"github.com/tphakala/birdnet-go/internal/logger"
	"gorm.io/gorm"
)

// Legacy cleanup proof errors.
var (
	// ErrNotLegacyDatabase indicates the target SQLite database holds no legacy notes table.
	ErrNotLegacyDatabase = errors.NewStd("target is not a legacy database")
	// ErrLegacyRecordsUnmigrated indicates legacy records beyond the migration watermark
	// are missing from the enhanced database (v2), or dirty IDs are still pending sync.
	ErrLegacyRecordsUnmigrated = errors.NewStd("legacy records beyond the migration watermark are missing from v2")
)

// VerifySQLiteLegacyMigrated proves that the SQLite database at legacyPath is a
// legacy database whose records beyond lastMigratedID are all present in v2DB, the
// opened v2 database. It returns nil only when the proof holds, so a caller may
// delete the file on nil and must keep it on any error. It fails closed: a
// database that cannot be opened or read is an error wrapping
// ErrLegacyTailUnverified, a target without a legacy notes table is
// ErrNotLegacyDatabase, and a tail that is not reconciled is
// ErrLegacyRecordsUnmigrated. Errors never carry the path. Unlike the startup
// check, a missing file or table is a refusal, never "nothing to migrate".
func VerifySQLiteLegacyMigrated(ctx context.Context, legacyPath string, v2DB *gorm.DB, lastMigratedID uint, log logger.Logger) error {
	legacyDB, closeLegacy, err := openReadOnlySQLite(legacyPath, tailStepOpenLegacy, tailStepLegacyHandle)
	if err != nil {
		return err
	}
	defer closeLegacy()

	notesTable, err := resolveSQLiteTableName(legacyDB.WithContext(ctx), "notes")
	if err != nil {
		return tailUnverified(tailStepLegacyTable, err)
	}
	if notesTable == "" {
		return ErrNotLegacyDatabase
	}

	dirtyTable, err := resolveSQLiteTableName(v2DB.WithContext(ctx), "migration_dirty_ids", "migration_dirty_id")
	if err != nil {
		return tailUnverified(tailStepDirtyTable, err)
	}
	reconciled, err := legacyTailReconciledInV2(legacyDB.WithContext(ctx), v2DB.WithContext(ctx), lastMigratedID, notesTable, "detections", dirtyTable, log)
	if err != nil {
		return tailUnverified(tailStepVerifyTail, err)
	}
	if !reconciled {
		return ErrLegacyRecordsUnmigrated
	}
	return nil
}
