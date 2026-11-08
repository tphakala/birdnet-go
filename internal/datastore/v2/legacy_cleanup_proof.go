package v2

import (
	"context"
	"fmt"
	"regexp"

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

// safeTablePrefix is the shape of a table prefix that may be spliced into the
// backtick-quoted identifiers of the proof queries.
var safeTablePrefix = regexp.MustCompile(`^[A-Za-z0-9_]+$`)

// errUnsafeTablePrefix is the fixed cause when the table prefix is not a plain identifier fragment.
var errUnsafeTablePrefix = errors.NewStd("table prefix is not a plain identifier")

// errNoCurrentDatabase is the fixed cause when the server reports no selected database.
var errNoCurrentDatabase = errors.NewStd("no database selected")

// legacyChildTablesQuery finds one child row whose note_id is above the watermark
// and has no detection with that id in v2. The two %s are the child table and the
// prefixed detections table, both backtick-quoted fixed names.
const legacyChildTablesQuery = "SELECT c.note_id FROM `%s` c LEFT JOIN `%s` d ON d.id = c.note_id " +
	"WHERE c.note_id > ? AND d.id IS NULL LIMIT 1"

// VerifyMySQLLegacyMigrated proves that the legacy tables beside the prefixed v2
// schema in the MySQL database db holds are fully migrated, for the detections
// beyond lastMigratedID: every legacy detection id above the watermark has a row in
// <tablePrefix>detections and no dirty id is pending. The proof covers detection
// ids only; it does not compare related rows (reviews, comments, locks and
// secondary predictions) of tail detections with v2. db is the manager's handle,
// which holds both schemas. A caller may drop the legacy tables on nil and must
// keep them on any error.
//
// When the legacy notes table exists, every notes.id above the watermark must be in
// v2. When it is absent (a cleanup that stopped part way), each remaining legacy
// child table (note_locks, note_comments, note_reviews, results) must hold no
// note_id above the watermark that is missing from v2; a child table without a
// note_id column fails that query and so refuses. Nothing left to check is nil.
//
// It fails closed: an empty tablePrefix is ErrNotLegacyDatabase (a bare schema has
// no legacy companion), a tail that is not reconciled is ErrLegacyRecordsUnmigrated,
// and any lookup or query failure, including a cancelled context, is an error
// wrapping ErrLegacyTailUnverified. Unlike the boot check, a missing or unreadable
// table never reads as "nothing to migrate". Errors may carry server text, so
// callers log them and never return them to clients.
func VerifyMySQLLegacyMigrated(ctx context.Context, db *gorm.DB, tablePrefix string, lastMigratedID uint, log logger.Logger) error {
	if tablePrefix == "" {
		return ErrNotLegacyDatabase
	}
	if !safeTablePrefix.MatchString(tablePrefix) {
		return tailUnverified(tailStepLegacyTable, errUnsafeTablePrefix)
	}
	db = db.WithContext(ctx)

	var database string
	if err := db.Raw("SELECT DATABASE()").Scan(&database).Error; err != nil {
		return tailUnverified(tailStepCurrentDatabase, err)
	}
	if database == "" {
		return tailUnverified(tailStepCurrentDatabase, errNoCurrentDatabase)
	}

	notesTable, err := resolveMySQLTableName(db, database, mixedSchemaNotesTable)
	if err != nil {
		return tailUnverified(tailStepLegacyTable, err)
	}
	dirtyTable, err := resolveMySQLTableName(db, database, tablePrefix+"migration_dirty_ids", tablePrefix+"migration_dirty_id")
	if err != nil {
		return tailUnverified(tailStepDirtyTable, err)
	}
	detectionsTable := tablePrefix + "detections"

	if notesTable != "" {
		reconciled, err := legacyTailReconciledInV2(db, db, lastMigratedID, notesTable, detectionsTable, dirtyTable, log)
		if err != nil {
			return tailUnverified(tailStepVerifyTail, err)
		}
		if !reconciled {
			return ErrLegacyRecordsUnmigrated
		}
		return nil
	}

	pending, err := pendingDirtyIDs(db, dirtyTable, lastMigratedID, log)
	if err != nil {
		return tailUnverified(tailStepVerifyTail, err)
	}
	if pending {
		return ErrLegacyRecordsUnmigrated
	}
	return verifyLegacyChildrenInV2(db, database, detectionsTable, lastMigratedID, log)
}

// verifyLegacyChildrenInV2 checks the legacy child tables that still exist when
// the notes table is gone: none may reference a detection id above the watermark
// that v2 lacks.
func verifyLegacyChildrenInV2(db *gorm.DB, database, detectionsTable string, lastMigratedID uint, log logger.Logger) error {
	for _, name := range legacyDetectionTables {
		if name == mixedSchemaNotesTable {
			continue
		}
		child, err := resolveMySQLTableName(db, database, name)
		if err != nil {
			return tailUnverified(tailStepLegacyTable, err)
		}
		if child == "" {
			continue
		}
		var orphans []uint
		if err := db.Raw(fmt.Sprintf(legacyChildTablesQuery, child, detectionsTable), lastMigratedID).Scan(&orphans).Error; err != nil {
			return tailUnverified(tailStepVerifyChildren, err)
		}
		if len(orphans) > 0 {
			log.Warn("legacy tail not reconciled: child rows reference records absent from v2",
				logger.String("table", child),
				logger.Uint64("last_migrated_id", uint64(lastMigratedID)))
			return ErrLegacyRecordsUnmigrated
		}
	}
	return nil
}
