package support

import (
	"context"
	"fmt"
	"os"
	"strings"
	"time"

	"github.com/tphakala/birdnet-go/internal/datastore"
	"github.com/tphakala/birdnet-go/internal/datastore/v2/entities"
	"github.com/tphakala/birdnet-go/internal/logger"
	"github.com/tphakala/birdnet-go/internal/privacy"
	"gorm.io/gorm"
)

// Database collection timeouts
const (
	countTimeout     = 2 * time.Second
	integrityTimeout = 5 * time.Second
	fkCheckTimeout   = 10 * time.Second
	totalDBTimeout   = 30 * time.Second
)

// GormDatabaseInfoCollector implements DatabaseInfoProvider using a GORM connection.
type GormDatabaseInfoCollector struct {
	db            *gorm.DB
	dialect       string
	dbPath        string
	schemaVersion string
	tablePrefix   string
}

// NewGormDatabaseInfoCollector creates a collector for database diagnostics.
// Returns nil if db is nil (caller must nil-check before use).
func NewGormDatabaseInfoCollector(db *gorm.DB, dialect, dbPath, schemaVersion, tablePrefix string) *GormDatabaseInfoCollector {
	if db == nil {
		return nil
	}
	return &GormDatabaseInfoCollector{
		db:            db,
		dialect:       dialect,
		dbPath:        dbPath,
		schemaVersion: schemaVersion,
		tablePrefix:   tablePrefix,
	}
}

// quoteIdentifier wraps a SQL identifier in double quotes, escaping internal quotes per SQL standard.
func quoteIdentifier(name string) string {
	escaped := strings.ReplaceAll(name, `"`, `""`)
	return `"` + escaped + `"`
}

// CollectDatabaseInfo gathers database schema, health, and metadata.
// Each sub-collection is independent; failures are recorded in error fields, not propagated.
func (c *GormDatabaseInfoCollector) CollectDatabaseInfo(ctx context.Context) (*DatabaseInfo, error) {
	ctx, cancel := context.WithTimeout(ctx, totalDBTimeout)
	defer cancel()

	log := logger.Global().Module("support")

	info := &DatabaseInfo{
		Dialect:       c.dialect,
		SchemaVersion: c.schemaVersion,
		DatabasePath:  privacy.AnonymizePath(c.dbPath),
		Tables:        []TableSchema{},
		FKViolations:  []ForeignKeyViolation{},
	}

	var collectionErrors []string

	switch c.dialect {
	case datastore.DialectSQLite:
		c.collectSQLiteInfo(ctx, info, &collectionErrors, log)
	case datastore.DialectMySQL:
		c.collectMySQLInfo(ctx, info, &collectionErrors, log)
	default:
		collectionErrors = append(collectionErrors, fmt.Sprintf("unsupported dialect: %s", c.dialect))
	}

	c.collectMigrationState(ctx, info, &collectionErrors, log)
	c.collectAppMetadata(ctx, info, &collectionErrors, log)

	if len(collectionErrors) > 0 {
		info.CollectionError = strings.Join(collectionErrors, "; ")
	}

	return info, nil
}

// collectSQLiteInfo gathers SQLite-specific diagnostics.
func (c *GormDatabaseInfoCollector) collectSQLiteInfo(ctx context.Context, info *DatabaseInfo, errs *[]string, log logger.Logger) {
	db := c.db.WithContext(ctx)

	// Engine version
	var version string
	if err := db.Raw("SELECT sqlite_version()").Row().Scan(&version); err != nil {
		*errs = append(*errs, fmt.Sprintf("sqlite_version: %v", err))
	} else {
		info.EngineVersion = version
	}

	// File sizes
	c.collectSQLiteFileSizes(info)

	// SQLite PRAGMAs for diagnostics
	c.collectSQLitePragmas(ctx, info, errs, log)

	// Table schemas
	c.collectSQLiteTables(ctx, info, errs, log)

	// Integrity check
	c.collectSQLiteIntegrityCheck(ctx, info, errs, log)

	// Foreign key violations
	c.collectSQLiteFKViolations(ctx, info, errs, log)
}

// collectSQLiteFileSizes reads the database, WAL, and SHM file sizes.
func (c *GormDatabaseInfoCollector) collectSQLiteFileSizes(info *DatabaseInfo) {
	if stat, err := os.Stat(c.dbPath); err == nil {
		info.DatabaseSizeBytes = stat.Size()
	}
	if stat, err := os.Stat(c.dbPath + "-wal"); err == nil {
		info.WALSizeBytes = stat.Size()
	}
	if stat, err := os.Stat(c.dbPath + "-shm"); err == nil {
		info.SHMSizeBytes = stat.Size()
	}
}

// collectSQLitePragmas reads diagnostic PRAGMAs (journal_mode, auto_vacuum, page_size, etc.).
func (c *GormDatabaseInfoCollector) collectSQLitePragmas(ctx context.Context, info *DatabaseInfo, errs *[]string, _ logger.Logger) {
	db := c.db.WithContext(ctx)

	pragmas := []struct {
		name   string
		target any
	}{
		{"journal_mode", &info.JournalMode},
		{"page_size", &info.PageSize},
		{"page_count", &info.PageCount},
		{"freelist_count", &info.FreelistCount},
	}
	for _, p := range pragmas {
		if err := db.Raw(fmt.Sprintf("PRAGMA %s", p.name)).Row().Scan(p.target); err != nil {
			*errs = append(*errs, fmt.Sprintf("PRAGMA %s: %v", p.name, err))
		}
	}

	// auto_vacuum returns an integer; map to human-readable string
	var autoVacuumVal int
	if err := db.Raw("PRAGMA auto_vacuum").Row().Scan(&autoVacuumVal); err != nil {
		*errs = append(*errs, fmt.Sprintf("PRAGMA auto_vacuum: %v", err))
	} else {
		switch autoVacuumVal {
		case 0:
			info.AutoVacuum = "none"
		case 1:
			info.AutoVacuum = "full"
		case 2:
			info.AutoVacuum = "incremental"
		default:
			info.AutoVacuum = fmt.Sprintf("unknown(%d)", autoVacuumVal)
		}
	}
}

// collectSQLiteTables enumerates tables and their schemas.
func (c *GormDatabaseInfoCollector) collectSQLiteTables(ctx context.Context, info *DatabaseInfo, errs *[]string, log logger.Logger) {
	db := c.db.WithContext(ctx)

	var tableNames []string
	if err := db.Raw("SELECT name FROM sqlite_master WHERE type='table' AND name NOT LIKE 'sqlite_%'").Scan(&tableNames).Error; err != nil {
		*errs = append(*errs, fmt.Sprintf("list tables: %v", err))
		return
	}

	tables := make([]TableSchema, 0, len(tableNames))
	for _, name := range tableNames {
		ts := TableSchema{Name: name}
		c.collectSQLiteTableColumns(ctx, &ts, errs)
		c.collectSQLiteTableIndexes(ctx, &ts, errs)
		c.collectTableRowCount(ctx, &ts, errs, log)
		tables = append(tables, ts)
	}
	info.Tables = tables
}

// collectSQLiteTableColumns reads column definitions via PRAGMA table_info.
func (c *GormDatabaseInfoCollector) collectSQLiteTableColumns(ctx context.Context, ts *TableSchema, errs *[]string) {
	db := c.db.WithContext(ctx)

	rows, err := db.Raw(fmt.Sprintf("PRAGMA table_info(%s)", quoteIdentifier(ts.Name))).Rows()
	if err != nil {
		*errs = append(*errs, fmt.Sprintf("table_info(%s): %v", ts.Name, err))
		return
	}
	defer func() { _ = rows.Close() }()

	var columns []ColumnInfo
	for rows.Next() {
		var cid int
		var name, colType string
		var notNull, pk int
		var dfltValue *string
		if err := rows.Scan(&cid, &name, &colType, &notNull, &dfltValue, &pk); err != nil {
			*errs = append(*errs, fmt.Sprintf("scan table_info(%s): %v", ts.Name, err))
			continue
		}
		columns = append(columns, ColumnInfo{
			Name:         name,
			Type:         colType,
			Nullable:     notNull == 0,
			PrimaryKey:   pk > 0,
			DefaultValue: dfltValue,
		})
	}
	if err := rows.Err(); err != nil {
		*errs = append(*errs, fmt.Sprintf("iterate table_info(%s): %v", ts.Name, err))
	}
	ts.Columns = columns
}

// collectSQLiteTableIndexes reads indexes via PRAGMA index_list and index_info.
func (c *GormDatabaseInfoCollector) collectSQLiteTableIndexes(ctx context.Context, ts *TableSchema, errs *[]string) {
	db := c.db.WithContext(ctx)

	type indexListRow struct {
		Seq     int
		Name    string
		Unique  int
		Origin  string
		Partial int
	}

	rows, err := db.Raw(fmt.Sprintf("PRAGMA index_list(%s)", quoteIdentifier(ts.Name))).Rows()
	if err != nil {
		*errs = append(*errs, fmt.Sprintf("index_list(%s): %v", ts.Name, err))
		return
	}
	defer func() { _ = rows.Close() }()

	var indexes []IndexInfo
	for rows.Next() {
		var ilr indexListRow
		if err := rows.Scan(&ilr.Seq, &ilr.Name, &ilr.Unique, &ilr.Origin, &ilr.Partial); err != nil {
			*errs = append(*errs, fmt.Sprintf("scan index_list(%s): %v", ts.Name, err))
			continue
		}

		indexColumns := c.collectIndexColumns(db, ilr.Name, errs)
		indexes = append(indexes, IndexInfo{
			Name:    ilr.Name,
			Columns: indexColumns,
			Unique:  ilr.Unique != 0,
		})
	}
	if err := rows.Err(); err != nil {
		*errs = append(*errs, fmt.Sprintf("iterate index_list(%s): %v", ts.Name, err))
	}
	ts.Indexes = indexes
}

// collectIndexColumns reads columns for a single index via PRAGMA index_info.
func (c *GormDatabaseInfoCollector) collectIndexColumns(db *gorm.DB, indexName string, errs *[]string) []string {
	infoRows, err := db.Raw(fmt.Sprintf("PRAGMA index_info(%s)", quoteIdentifier(indexName))).Rows()
	if err != nil {
		*errs = append(*errs, fmt.Sprintf("index_info(%s): %v", indexName, err))
		return nil
	}
	defer func() { _ = infoRows.Close() }()

	var columns []string
	for infoRows.Next() {
		var seqno, cid int
		var colName string
		if err := infoRows.Scan(&seqno, &cid, &colName); err != nil {
			*errs = append(*errs, fmt.Sprintf("scan index_info(%s): %v", indexName, err))
			continue
		}
		columns = append(columns, colName)
	}
	if err := infoRows.Err(); err != nil {
		*errs = append(*errs, fmt.Sprintf("iterate index_info(%s): %v", indexName, err))
	}
	return columns
}

// collectTableRowCount counts rows with a per-table timeout.
func (c *GormDatabaseInfoCollector) collectTableRowCount(ctx context.Context, ts *TableSchema, errs *[]string, log logger.Logger) {
	countCtx, cancel := context.WithTimeout(ctx, countTimeout)
	defer cancel()

	var count int64
	if err := c.db.WithContext(countCtx).Raw("SELECT COUNT(*) FROM " + quoteIdentifier(ts.Name)).Row().Scan(&count); err != nil { //nolint:gosec // table names from sqlite_master/INFORMATION_SCHEMA
		log.Warn("support: row count timed out or failed", logger.String("table", ts.Name), logger.Error(err))
		ts.RowCount = -1
	} else {
		ts.RowCount = count
	}
}

// collectSQLiteIntegrityCheck runs PRAGMA quick_check with a timeout.
// Reads all result rows (quick_check returns multiple rows when issues are found).
func (c *GormDatabaseInfoCollector) collectSQLiteIntegrityCheck(ctx context.Context, info *DatabaseInfo, errs *[]string, _ logger.Logger) {
	integrityCtx, cancel := context.WithTimeout(ctx, integrityTimeout)
	defer cancel()

	var results []string
	if err := c.db.WithContext(integrityCtx).Raw("PRAGMA quick_check").Scan(&results).Error; err != nil {
		if integrityCtx.Err() != nil {
			info.IntegrityCheck = "timeout_exceeded"
			*errs = append(*errs, "integrity check timed out")
		} else {
			info.IntegrityCheck = fmt.Sprintf("error: %v", err)
			*errs = append(*errs, fmt.Sprintf("quick_check: %v", err))
		}
		return
	}
	result := strings.Join(results, "; ")
	if result == "" {
		result = "ok"
	}
	info.IntegrityCheck = result
}

// collectSQLiteFKViolations runs PRAGMA foreign_key_check with a timeout.
func (c *GormDatabaseInfoCollector) collectSQLiteFKViolations(ctx context.Context, info *DatabaseInfo, errs *[]string, _ logger.Logger) {
	fkCtx, cancel := context.WithTimeout(ctx, fkCheckTimeout)
	defer cancel()

	rows, err := c.db.WithContext(fkCtx).Raw("PRAGMA foreign_key_check").Rows()
	if err != nil {
		if fkCtx.Err() != nil {
			*errs = append(*errs, "foreign key check timed out")
		} else {
			*errs = append(*errs, fmt.Sprintf("foreign_key_check: %v", err))
		}
		return
	}
	defer func() { _ = rows.Close() }()

	var violations []ForeignKeyViolation
	for rows.Next() {
		var table, refTable string
		var rowID int64
		var fkIdx int
		if err := rows.Scan(&table, &rowID, &refTable, &fkIdx); err != nil {
			*errs = append(*errs, fmt.Sprintf("scan fk_check: %v", err))
			continue
		}
		violations = append(violations, ForeignKeyViolation{
			Table:    table,
			RowID:    rowID,
			RefTable: refTable,
			FKIndex:  fkIdx,
		})
	}
	if err := rows.Err(); err != nil {
		*errs = append(*errs, fmt.Sprintf("iterate fk_check: %v", err))
	}
	if violations != nil {
		info.FKViolations = violations
	}
}

// collectMySQLInfo gathers MySQL-specific diagnostics.
func (c *GormDatabaseInfoCollector) collectMySQLInfo(ctx context.Context, info *DatabaseInfo, errs *[]string, log logger.Logger) {
	db := c.db.WithContext(ctx)

	// Engine version
	var version string
	if err := db.Raw("SELECT VERSION()").Row().Scan(&version); err != nil {
		*errs = append(*errs, fmt.Sprintf("mysql version: %v", err))
	} else {
		info.EngineVersion = version
	}

	// Integrity check not available for MySQL
	info.IntegrityCheck = "not_available"

	// Database size
	var dbSize *int64
	if err := db.Raw("SELECT SUM(data_length + index_length) FROM INFORMATION_SCHEMA.TABLES WHERE TABLE_SCHEMA = DATABASE()").Row().Scan(&dbSize); err != nil {
		*errs = append(*errs, fmt.Sprintf("mysql db size: %v", err))
	} else if dbSize != nil {
		info.DatabaseSizeBytes = *dbSize
	}

	// Tables
	c.collectMySQLTables(ctx, info, errs, log)
}

// collectMySQLTables enumerates MySQL tables, columns, and indexes.
// Row counts are InnoDB estimates from TABLE_ROWS (not exact COUNT(*)) to avoid full table scans.
func (c *GormDatabaseInfoCollector) collectMySQLTables(ctx context.Context, info *DatabaseInfo, errs *[]string, _ logger.Logger) {
	db := c.db.WithContext(ctx)

	type tableRow struct {
		TableName string `gorm:"column:table_name"`
		TableRows int64  `gorm:"column:table_rows"`
	}

	var tableRows []tableRow
	query := "SELECT TABLE_NAME AS table_name, IFNULL(TABLE_ROWS, 0) AS table_rows FROM INFORMATION_SCHEMA.TABLES WHERE TABLE_SCHEMA = DATABASE()"
	rawQuery := db.Raw(query)
	if c.tablePrefix != "" {
		rawQuery = db.Raw(query+" AND TABLE_NAME LIKE ?", c.tablePrefix+"%")
	}
	if err := rawQuery.Scan(&tableRows).Error; err != nil {
		*errs = append(*errs, fmt.Sprintf("list mysql tables: %v", err))
		return
	}

	tables := make([]TableSchema, 0, len(tableRows))
	for _, tr := range tableRows {
		ts := TableSchema{Name: tr.TableName, RowCount: tr.TableRows}
		c.collectMySQLTableColumns(ctx, &ts, errs)
		c.collectMySQLTableIndexes(ctx, &ts, errs)
		tables = append(tables, ts)
	}
	info.Tables = tables
}

// collectMySQLTableColumns reads columns from INFORMATION_SCHEMA.
func (c *GormDatabaseInfoCollector) collectMySQLTableColumns(ctx context.Context, ts *TableSchema, errs *[]string) {
	db := c.db.WithContext(ctx)

	rows, err := db.Raw(
		"SELECT COLUMN_NAME, COLUMN_TYPE, IS_NULLABLE, COLUMN_KEY, COLUMN_DEFAULT "+
			"FROM INFORMATION_SCHEMA.COLUMNS "+
			"WHERE TABLE_SCHEMA = DATABASE() AND TABLE_NAME = ? "+
			"ORDER BY ORDINAL_POSITION", ts.Name).Rows()
	if err != nil {
		*errs = append(*errs, fmt.Sprintf("mysql columns(%s): %v", ts.Name, err))
		return
	}
	defer func() { _ = rows.Close() }()

	var columns []ColumnInfo
	for rows.Next() {
		var name, colType, nullable, columnKey string
		var dfltValue *string
		if err := rows.Scan(&name, &colType, &nullable, &columnKey, &dfltValue); err != nil {
			*errs = append(*errs, fmt.Sprintf("scan mysql columns(%s): %v", ts.Name, err))
			continue
		}
		columns = append(columns, ColumnInfo{
			Name:         name,
			Type:         colType,
			Nullable:     nullable == "YES",
			PrimaryKey:   columnKey == "PRI",
			DefaultValue: dfltValue,
		})
	}
	if err := rows.Err(); err != nil {
		*errs = append(*errs, fmt.Sprintf("iterate mysql columns(%s): %v", ts.Name, err))
	}
	ts.Columns = columns
}

// collectMySQLTableIndexes reads indexes from INFORMATION_SCHEMA.STATISTICS.
func (c *GormDatabaseInfoCollector) collectMySQLTableIndexes(ctx context.Context, ts *TableSchema, errs *[]string) {
	db := c.db.WithContext(ctx)

	rows, err := db.Raw(
		"SELECT INDEX_NAME, COLUMN_NAME, NON_UNIQUE "+
			"FROM INFORMATION_SCHEMA.STATISTICS "+
			"WHERE TABLE_SCHEMA = DATABASE() AND TABLE_NAME = ? "+
			"ORDER BY INDEX_NAME, SEQ_IN_INDEX", ts.Name).Rows()
	if err != nil {
		*errs = append(*errs, fmt.Sprintf("mysql indexes(%s): %v", ts.Name, err))
		return
	}
	defer func() { _ = rows.Close() }()

	indexMap := make(map[string]*IndexInfo)
	var indexOrder []string
	for rows.Next() {
		var indexName, colName string
		var nonUnique int
		if err := rows.Scan(&indexName, &colName, &nonUnique); err != nil {
			*errs = append(*errs, fmt.Sprintf("scan mysql indexes(%s): %v", ts.Name, err))
			continue
		}
		if existing, ok := indexMap[indexName]; ok {
			existing.Columns = append(existing.Columns, colName)
		} else {
			indexMap[indexName] = &IndexInfo{
				Name:    indexName,
				Columns: []string{colName},
				Unique:  nonUnique == 0,
			}
			indexOrder = append(indexOrder, indexName)
		}
	}

	if err := rows.Err(); err != nil {
		*errs = append(*errs, fmt.Sprintf("iterate mysql indexes(%s): %v", ts.Name, err))
	}

	indexes := make([]IndexInfo, 0, len(indexOrder))
	for _, name := range indexOrder {
		indexes = append(indexes, *indexMap[name])
	}
	ts.Indexes = indexes
}

// collectMigrationState reads the migration_states singleton row.
func (c *GormDatabaseInfoCollector) collectMigrationState(ctx context.Context, info *DatabaseInfo, errs *[]string, _ logger.Logger) {
	db := c.db.WithContext(ctx)

	var state entities.MigrationState
	if err := db.First(&state, 1).Error; err != nil {
		// Table may not exist in legacy mode; this is expected
		return
	}

	msi := &MigrationStateInfo{
		State:            string(state.State),
		CurrentPhase:     string(state.CurrentPhase),
		TotalRecords:     state.TotalRecords,
		MigratedRecords:  state.MigratedRecords,
		ErrorMessage:     state.ErrorMessage,
		RelatedDataError: state.RelatedDataError,
	}
	if state.StartedAt != nil {
		s := state.StartedAt.UTC().Format(time.RFC3339)
		msi.StartedAt = &s
	}
	if state.CompletedAt != nil {
		s := state.CompletedAt.UTC().Format(time.RFC3339)
		msi.CompletedAt = &s
	}

	// Count dirty records if the table exists
	var dirtyCount int64
	if err := db.Table("migration_dirty_ids").Count(&dirtyCount).Error; err == nil {
		msi.DirtyRecordCount = dirtyCount
	}

	info.MigrationState = msi
}

// collectAppMetadata reads the app_metadata key-value store.
func (c *GormDatabaseInfoCollector) collectAppMetadata(ctx context.Context, info *DatabaseInfo, errs *[]string, _ logger.Logger) {
	db := c.db.WithContext(ctx)

	var entries []entities.AppMetadata
	if err := db.Find(&entries).Error; err != nil {
		// Table may not exist in legacy mode
		return
	}

	if len(entries) > 0 {
		metadata := make(map[string]string, len(entries))
		for _, e := range entries {
			metadata[e.Key] = e.Value
		}
		info.AppMetadata = metadata
	}
}
