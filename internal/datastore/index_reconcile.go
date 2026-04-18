// Package datastore: stale unique-index reconciler.
//
// GORM's AutoMigrate only adds new indexes; it never drops indexes that are
// no longer declared on the entity. When the DynamicThreshold schema evolved
// from UNIQUE(species_name) to a composite UNIQUE(species_name, model_name),
// existing MySQL/SQLite DBs retained the old single-column index. The stale
// constraint then blocks legitimate composite-valid inserts and, on MySQL,
// causes AutoMigrate itself to fail on restart with Error 1062. This
// reconciler detects DB unique indexes whose column set matches no
// entity-declared unique index and drops them before AutoMigrate runs.
//
// Scope: legacy entities in internal/datastore/model.go. The v2 schema has
// its own migration path and is out of scope.
//
// Symptoms addressed: MySQL Error 1062 "Duplicate entry for key
// idx_dt_species_model" on every service start, and SQLite "UNIQUE
// constraint failed: dynamic_thresholds.species_name" on legitimate inserts
// once the schema grew the composite (species_name, model_name) index.
package datastore

import (
	"fmt"
	"slices"
	"strings"

	"github.com/tphakala/birdnet-go/internal/errors"
	"github.com/tphakala/birdnet-go/internal/logger"
	"gorm.io/gorm"
)

// dbUniqueIndex describes a unique index discovered on the live database.
// Columns are ordered by the index's column sequence (SEQ_IN_INDEX in MySQL,
// seqno in SQLite PRAGMA index_info).
type dbUniqueIndex struct {
	Name    string
	Table   string
	Columns []string
}

// entityUniqueIndex describes a unique index declared on a GORM entity.
// Columns are the physical (DB) column names in declaration order.
type entityUniqueIndex struct {
	Name    string
	Columns []string
}

// declaredUniqueIndexes parses a GORM entity and returns its declared unique
// indexes along with its physical table name. Declared indexes come from
// `gorm:"uniqueIndex:..."` tags and single-column `gorm:"uniqueIndex"` tags.
// The GORM schema parser (stmt.Parse + Schema.ParseIndexes) is the source of
// truth here; it matches the index names GORM would emit via AutoMigrate.
func declaredUniqueIndexes(db *gorm.DB, entity any) (tableName string, indexes []entityUniqueIndex, err error) {
	stmt := &gorm.Statement{DB: db}
	if err := stmt.Parse(entity); err != nil {
		return "", nil, fmt.Errorf("parse entity %T: %w", entity, err)
	}

	parsedIndexes := stmt.Schema.ParseIndexes()
	indexes = make([]entityUniqueIndex, 0, len(parsedIndexes))
	for _, idx := range parsedIndexes {
		if !strings.EqualFold(idx.Class, "UNIQUE") {
			continue
		}
		cols := make([]string, 0, len(idx.Fields))
		for _, f := range idx.Fields {
			if f.Field != nil { //nolint:staticcheck // QF1008: Field is embedded *schema.Field; nil check is required before accessing promoted fields
				cols = append(cols, f.DBName)
			}
		}
		if len(cols) == 0 {
			continue
		}
		indexes = append(indexes, entityUniqueIndex{
			Name:    idx.Name,
			Columns: cols,
		})
	}

	return stmt.Schema.Table, indexes, nil
}

// columnSetsEqual reports whether two column lists describe the same set of
// columns, ignoring order. Index identity for uniqueness purposes is the
// unordered column set; GORM emits indexes in declaration order but legacy
// DDL could have stored them in any order.
func columnSetsEqual(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	aSorted := slices.Clone(a)
	bSorted := slices.Clone(b)
	slices.Sort(aSorted)
	slices.Sort(bSorted)
	return slices.Equal(aSorted, bSorted)
}

// indexIsDeclared reports whether the given live DB unique index matches any
// entity-declared unique index by column set.
func indexIsDeclared(live dbUniqueIndex, declared []entityUniqueIndex) bool {
	for _, d := range declared {
		if columnSetsEqual(live.Columns, d.Columns) {
			return true
		}
	}
	return false
}

// reconcileLegacyUniqueIndexes drops DB-side unique indexes that the given
// GORM entities no longer declare. It is intended to run before AutoMigrate
// so that stale constraints don't block legitimate schema evolution.
//
// Failures drop through as errors for the caller to log + swallow (same
// non-fatal pattern as cleanupLegacySchemaContamination).
//
// dbType is case-insensitive ("MySQL", "SQLite", ...). dbName is required
// for MySQL (used as TABLE_SCHEMA); pass "" for SQLite.
func reconcileLegacyUniqueIndexes(db *gorm.DB, dbType, dbName string, entities []any) error {
	dialect := strings.ToLower(dbType)
	switch dialect {
	case DialectSQLite:
		// ok, no dbName needed
	case DialectMySQL:
		if dbName == "" {
			// Same defensive posture as validateAndFixSchema: without dbName
			// we can't query information_schema safely. Skip quietly.
			GetLogger().Warn("reconcileLegacyUniqueIndexes: empty dbName for MySQL, skipping")
			return nil
		}
	default:
		// Unknown dialect: skip. Unit tests exercise the known dialects.
		return nil
	}

	for _, entity := range entities {
		tableName, declared, err := declaredUniqueIndexes(db, entity)
		if err != nil {
			return errors.New(err).
				Component("datastore").
				Category(errors.CategoryDatabase).
				Context("operation", "reconcile_parse_entity").
				Build()
		}

		if !db.Migrator().HasTable(entity) {
			continue
		}

		var live []dbUniqueIndex
		switch dialect {
		case DialectSQLite:
			live, err = liveUniqueIndexesSQLite(db, tableName)
		case DialectMySQL:
			live, err = liveUniqueIndexesMySQL(db, dbName, tableName)
		}
		if err != nil {
			return errors.New(err).
				Component("datastore").
				Category(errors.CategoryDatabase).
				Context("operation", "reconcile_read_live_indexes").
				Context("table", tableName).
				Build()
		}

		for _, idx := range live {
			if indexIsDeclared(idx, declared) {
				continue
			}
			GetLogger().Info("Dropping stale unique index not declared by entity",
				logger.String("db_type", dialect),
				logger.String("table", idx.Table),
				logger.String("index", idx.Name),
				logger.Any("columns", idx.Columns))
			if err := dropUniqueIndex(db, dialect, idx); err != nil {
				return errors.New(err).
					Component("datastore").
					Category(errors.CategoryDatabase).
					Context("operation", "reconcile_drop_index").
					Context("table", idx.Table).
					Context("index", idx.Name).
					Build()
			}
		}
	}
	return nil
}

// liveUniqueIndexesSQLite returns the unique indexes currently present on the
// given table in SQLite, excluding the implicit PRIMARY KEY index (sqlite
// auto-generated indexes whose names start with "sqlite_autoindex_" are
// skipped; they cannot be dropped and track PK/UNIQUE constraints that
// belong to the CREATE TABLE statement).
func liveUniqueIndexesSQLite(db *gorm.DB, tableName string) ([]dbUniqueIndex, error) {
	type listRow struct {
		Name   string `gorm:"column:name"`
		Unique int    `gorm:"column:unique"`
	}
	var list []listRow
	escaped := strings.ReplaceAll(tableName, "'", "''")
	if err := db.Raw("PRAGMA index_list('" + escaped + "')").Scan(&list).Error; err != nil {
		return nil, fmt.Errorf("pragma index_list %s: %w", tableName, err)
	}

	result := make([]dbUniqueIndex, 0, len(list))
	for _, r := range list {
		if r.Unique != 1 {
			continue
		}
		if strings.HasPrefix(r.Name, "sqlite_autoindex_") {
			continue
		}
		cols, err := getSQLiteIndexColumns(db, r.Name, false)
		if err != nil {
			// getSQLiteIndexColumns already logs; skip this index.
			continue
		}
		if len(cols) == 0 {
			continue
		}
		result = append(result, dbUniqueIndex{
			Name:    r.Name,
			Table:   tableName,
			Columns: cols,
		})
	}
	return result, nil
}

// liveUniqueIndexesMySQL returns the unique indexes currently present on the
// given table in MySQL, excluding the PRIMARY key.
func liveUniqueIndexesMySQL(db *gorm.DB, dbName, tableName string) ([]dbUniqueIndex, error) {
	type row struct {
		IndexName  string `gorm:"column:INDEX_NAME"`
		ColumnName string `gorm:"column:COLUMN_NAME"`
		SeqInIndex int    `gorm:"column:SEQ_IN_INDEX"`
		NonUnique  int    `gorm:"column:NON_UNIQUE"`
	}
	var rows []row
	query := `SELECT INDEX_NAME, COLUMN_NAME, SEQ_IN_INDEX, NON_UNIQUE
	          FROM information_schema.STATISTICS
	          WHERE TABLE_SCHEMA = ? AND TABLE_NAME = ?
	          ORDER BY INDEX_NAME, SEQ_IN_INDEX`
	if err := db.Raw(query, dbName, tableName).Scan(&rows).Error; err != nil {
		if isMySQLError(err, mysqlErrNoSuchTable) {
			return nil, nil
		}
		return nil, fmt.Errorf("information_schema.STATISTICS for %s.%s: %w", dbName, tableName, err)
	}

	grouped := make(map[string]*dbUniqueIndex)
	for _, r := range rows {
		if r.NonUnique != 0 {
			continue
		}
		if strings.EqualFold(r.IndexName, "PRIMARY") {
			continue
		}
		idx, ok := grouped[r.IndexName]
		if !ok {
			idx = &dbUniqueIndex{Name: r.IndexName, Table: tableName}
			grouped[r.IndexName] = idx
		}
		idx.Columns = append(idx.Columns, r.ColumnName)
	}

	result := make([]dbUniqueIndex, 0, len(grouped))
	for _, idx := range grouped {
		if len(idx.Columns) == 0 {
			continue
		}
		result = append(result, *idx)
	}
	return result, nil
}

// dropUniqueIndex issues a dialect-appropriate DROP INDEX for the given live
// index. Identifier quoting uses backticks (MySQL) and double quotes
// (SQLite); GORM's quoted identifier helpers are not used here because we
// already hold resolved identifier strings from the introspection queries.
func dropUniqueIndex(db *gorm.DB, dialect string, idx dbUniqueIndex) error {
	switch dialect {
	case DialectMySQL:
		// Validate identifier to prevent SQL injection via crafted index names.
		if !isSafeIdentifier(idx.Name) || !isSafeIdentifier(idx.Table) {
			return fmt.Errorf("unsafe identifier refusing drop: table=%q index=%q", idx.Table, idx.Name)
		}
		return db.Exec(fmt.Sprintf("ALTER TABLE `%s` DROP INDEX `%s`", idx.Table, idx.Name)).Error
	case DialectSQLite:
		if !isSafeIdentifier(idx.Name) {
			return fmt.Errorf("unsafe identifier refusing drop: index=%q", idx.Name)
		}
		return db.Exec(fmt.Sprintf("DROP INDEX IF EXISTS %q", idx.Name)).Error
	default:
		return fmt.Errorf("unsupported dialect: %s", dialect)
	}
}

// isSafeIdentifier rejects identifiers that contain characters outside the
// conservative [a-zA-Z0-9_] set. Index/table names discovered via
// information_schema or PRAGMA should never contain exotic characters in
// this codebase; this is defence-in-depth against a compromised system
// catalog.
func isSafeIdentifier(s string) bool {
	if s == "" || len(s) > 64 {
		return false
	}
	for _, r := range s {
		switch {
		case r >= 'a' && r <= 'z':
		case r >= 'A' && r <= 'Z':
		case r >= '0' && r <= '9':
		case r == '_':
		default:
			return false
		}
	}
	return true
}
