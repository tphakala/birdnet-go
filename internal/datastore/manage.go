package datastore

import (
	"fmt"
	"log"
	"log/slog"
	"net/url"
	"slices"
	"strings"
	"time"

	"github.com/tphakala/birdnet-go/internal/errors"
	"gorm.io/gorm"
	"gorm.io/gorm/logger"
)

const (
	// DefaultSlowQueryThreshold defines the duration after which a query is considered slow.
	// 200ms is chosen based on common industry practices and to catch queries that may
	// impact user experience. This threshold helps identify queries that need optimization
	// while avoiding false positives from typical database operations.
	DefaultSlowQueryThreshold = 200 * time.Millisecond
	
	// MaxColumnsForDetailedDisplay defines the maximum number of columns to display
	// in detailed logs. When more columns are present, only the count is shown to
	// keep log output concise and readable.
	MaxColumnsForDetailedDisplay = 5
)

// createGormLogger configures and returns a new GORM logger instance.
func createGormLogger() logger.Interface {
	// Use our custom GORM logger with metrics support
	return NewGormLogger(DefaultSlowQueryThreshold, logger.Warn, nil)
}

// getSQLiteIndexInfo executes PRAGMA index_info for a given SQLite index name,
// handling necessary string formatting and escaping.
func getSQLiteIndexInfo(db *gorm.DB, indexName string, debug bool) ([]struct {
	Name string `gorm:"column:name"`
}, error) {
	var info []struct {
		Name string `gorm:"column:name"`
	}
	// Escape single quotes in the index name to prevent SQL injection,
	// although index names from PRAGMA index_list are generally safe.
	escapedIndexName := strings.ReplaceAll(indexName, "'", "''")
	query := fmt.Sprintf("PRAGMA index_info('%s')", escapedIndexName)
	if err := db.Raw(query).Scan(&info).Error; err != nil {
		// Log the warning here as the caller might just continue
		log.Printf("WARN: Failed to get info for index '%s' using query [%s]: %v", indexName, query, err)
		return nil, err // Return the error to indicate failure
	}
	return info, nil
}

// getSQLiteIndexColumns retrieves the column names for a given SQLite index.
// It reuses getSQLiteIndexInfo and simplifies the result.
func getSQLiteIndexColumns(db *gorm.DB, indexName string, debug bool) ([]string, error) {
	info, err := getSQLiteIndexInfo(db, indexName, debug) // Existing helper logs errors
	if err != nil {
		return nil, err // Propagate error
	}
	cols := make([]string, len(info))
	for i, colInfo := range info {
		cols[i] = colInfo.Name
	}
	return cols, nil
}

// hasCorrectImageCacheIndexSQLite checks if the SQLite database has the correct
// composite unique index on the image_caches table, and specifically checks
// against a known incorrect single-column unique index.
func hasCorrectImageCacheIndexSQLite(db *gorm.DB, debug bool) (bool, error) {
	var indexes []struct {
		Name   string `gorm:"column:name"`
		Unique int    `gorm:"column:unique"` // 1 == unique
	}

	// 1. Check if the table exists
	if !db.Migrator().HasTable(&ImageCache{}) {
		if debug {
			log.Println("DEBUG: SQLite 'image_caches' table does not exist.")
		}
		return false, nil // Table doesn't exist, schema is implicitly incorrect for this check
	}

	// 2. Query index list for the table
	if err := db.Raw("PRAGMA index_list('image_caches')").Scan(&indexes).Error; err != nil {
		return false, fmt.Errorf("failed to query index list for image_caches: %w", err)
	}

	correctIndexFound := false
	incorrectIndexFound := false
	targetIndexName := "idx_imagecache_provider_species"

	// 3. Analyze each index
	for _, idx := range indexes {
		if debug {
			log.Printf("DEBUG: SQLite Analyzing index: Name=%s, Unique=%d", idx.Name, idx.Unique)
		}

		// Both the correct and the known incorrect index must be unique
		if idx.Unique != 1 {
			continue
		}

		// Get the columns for this unique index
		columns, err := getSQLiteIndexColumns(db, idx.Name, debug)
		if err != nil {
			// Error fetching columns, cannot determine state, log is in getSQLiteIndexInfo
			continue // Skip this index
		}

		// Check if it's the target correct index
		if idx.Name == targetIndexName {
			// Expected: unique, 2 columns: provider_name, scientific_name (order doesn't matter for existence check)
			if len(columns) == 2 && slices.Contains(columns, "provider_name") && slices.Contains(columns, "scientific_name") {
				correctIndexFound = true
				if debug {
					log.Printf("DEBUG: SQLite Found correct composite unique index: %s", idx.Name)
				}
			} else if debug {
				// Log if the named index doesn't have the expected structure
				log.Printf("DEBUG: SQLite Index '%s' found, but columns %v or uniqueness mismatch.", idx.Name, columns)
			}
		} else {
			// Check if it's the known incorrect index (unique, 1 column: scientific_name)
			// This check is only performed if the index name is NOT the target name.
			if len(columns) == 1 && columns[0] == "scientific_name" {
				incorrectIndexFound = true
				if debug {
					log.Printf("DEBUG: SQLite Found incorrect single-column unique index on scientific_name: %s", idx.Name)
				}
			}
		}

		// Optimization: If we've found both states, we can stop.
		if correctIndexFound && incorrectIndexFound {
			break
		}
	}

	if debug {
		log.Printf("DEBUG: SQLite Schema Check Result: correctIndexFound=%v, incorrectIndexFound=%v", correctIndexFound, incorrectIndexFound)
	}

	// Schema is considered correct only if the specific target index exists AND the specific incorrect index does NOT exist.
	return correctIndexFound && !incorrectIndexFound, nil
}

// hasCorrectImageCacheIndexMySQL checks if the MySQL database has the correct
// composite unique index on the image_caches table.
func hasCorrectImageCacheIndexMySQL(db *gorm.DB, dbName string, debug bool) (bool, error) {
	type IndexInfo struct {
		IndexName  string `gorm:"column:INDEX_NAME"`
		ColumnName string `gorm:"column:COLUMN_NAME"`
		SeqInIndex int    `gorm:"column:SEQ_IN_INDEX"`
		NonUnique  int    `gorm:"column:NON_UNIQUE"` // 0 means unique
	}

	var stats []IndexInfo
	targetIndexName := "idx_imagecache_provider_species"
	targetTableName := "image_caches"

	// Check if the table exists first
	if !db.Migrator().HasTable(&ImageCache{}) {
		return false, nil // Table doesn't exist, index can't be correct (will be created by AutoMigrate)
	}

	// Query the information schema for index details
	query := `SELECT INDEX_NAME, COLUMN_NAME, SEQ_IN_INDEX, NON_UNIQUE
	          FROM information_schema.STATISTICS
	          WHERE TABLE_SCHEMA = ? AND TABLE_NAME = ?`

	if err := db.Raw(query, dbName, targetTableName).Scan(&stats).Error; err != nil {
		// Handle case where information_schema might not be accessible or table doesn't exist yet
		if strings.Contains(err.Error(), "doesn't exist") {
			return false, nil // Treat as schema incorrect/incomplete if table/schema info missing
		}
		return false, fmt.Errorf("failed to query index info from information_schema for %s.%s: %w", dbName, targetTableName, err)
	}

	// Analyze the results by first mapping index names to their columns and uniqueness
	indexDetails := make(map[string]struct {
		Columns   []string
		IsUnique  bool
		SeqInCols map[string]int // Map column name to its sequence position
	})

	for _, stat := range stats {
		if debug {
			log.Printf("DEBUG: MySQL Processing index stat: Index=%s, Column=%s, Seq=%d, NonUnique=%d",
				stat.IndexName, stat.ColumnName, stat.SeqInIndex, stat.NonUnique)
		}
		detail, exists := indexDetails[stat.IndexName]
		if !exists {
			detail.Columns = []string{}
			detail.IsUnique = stat.NonUnique == 0
			detail.SeqInCols = make(map[string]int)
		}
		// Only add column if sequence is valid (greater than 0)
		if stat.SeqInIndex > 0 {
			detail.Columns = append(detail.Columns, stat.ColumnName)
			detail.SeqInCols[stat.ColumnName] = stat.SeqInIndex
		}
		// Update uniqueness; if any part is non-unique, the whole index is considered non-unique for our check
		if stat.NonUnique != 0 {
			detail.IsUnique = false
		}
		indexDetails[stat.IndexName] = detail
	}

	foundCorrectIndex := false
	foundIncorrectIndex := false

	// Now iterate through the processed index details
	for indexName, detail := range indexDetails {
		// Check for the target composite unique index
		if indexName == targetIndexName {
			if detail.IsUnique && len(detail.Columns) == 2 {
				providerSeq, providerOk := detail.SeqInCols["provider_name"]
				scientificSeq, scientificOk := detail.SeqInCols["scientific_name"]
				// Check if both columns exist and are in the correct order (provider=1, scientific=2)
				if providerOk && scientificOk && providerSeq == 1 && scientificSeq == 2 {
					foundCorrectIndex = true
					if debug {
						log.Printf("DEBUG: MySQL Found correct composite unique index: %s", indexName)
					}
				}
			}
		} else if detail.IsUnique && len(detail.Columns) == 1 && detail.Columns[0] == "scientific_name" {
			// Check for the incorrect single-column unique index on scientific_name
			foundIncorrectIndex = true
			if debug {
				log.Printf("DEBUG: MySQL Found incorrect single-column unique index on scientific_name: %s", indexName)
			}
		}

		// Optimization: Early exit if both conditions are met
		if foundCorrectIndex && foundIncorrectIndex {
			break
		}
	}

	if debug {
		log.Printf("DEBUG: MySQL Schema Check Result: foundCorrectIndex=%v, foundIncorrectIndex=%v", foundCorrectIndex, foundIncorrectIndex)
	}

	// Schema is correct if the target composite index exists and the incorrect single-column one doesn't
	return foundCorrectIndex && !foundIncorrectIndex, nil
}

// performAutoMigration automates database migrations with error handling.
// It checks the schema of the image_caches table and drops/recreates it if incorrect.
func performAutoMigration(db *gorm.DB, debug bool, dbType, connectionInfo string) error {
	migrationStart := time.Now()
	migrationLogger := getLogger().With("db_type", dbType)
	
	migrationLogger.Info("Starting database migration")
	
	// Validate and fix schema if needed
	if err := validateAndFixSchema(db, dbType, connectionInfo, debug, migrationLogger); err != nil {
		return err
	}

	// Perform table migrations
	successCount, err := migrateTables(db, dbType, migrationLogger)
	if err != nil {
		return err
	}
	
	// Log successful migration completion
	migrationLogger.Info("Database migration completed successfully",
		"db_type", dbType,
		"total_duration", time.Since(migrationStart),
		"tables_migrated", successCount)

	return nil
}

// extractDBNameFromMySQLInfo parses the database name from a MySQL DSN string.
// Example DSNs:
//
//	user:pass@tcp(127.0.0.1:3306)/dbname?charset=utf8mb4
//	user:pass@unix(/path/to/socket)/dbname
//	user:pass@/dbname?charset=utf8mb4 (no host/protocol)
//	/dbname (only path)
func extractDBNameFromMySQLInfo(connectionInfo string) string {
	// The go-sql-driver/mysql doesn't strictly require a scheme,
	// but net/url.Parse needs one for correct parsing. Add a dummy scheme if missing.
	// We need to handle cases where the DSN might *only* be the path, e.g., "/dbname".
	// Also handle cases like "user:pass@/dbname"
	parseInput := connectionInfo
	if !strings.Contains(parseInput, "://") && !strings.HasPrefix(parseInput, "/") {
		// If no scheme and not starting with '/', add dummy scheme for parsing.
		parseInput = "dummy://" + parseInput
	} else if strings.HasPrefix(parseInput, "/") {
		// Case like "/dbname?params=value"
		parseInput = "dummy://dummyhost" + parseInput // Add dummy scheme and host
	}

	u, err := url.Parse(parseInput)
	if err != nil {
		sanitizedConnectionInfo := redactSensitiveInfo(connectionInfo)
		log.Printf("WARN: Failed to parse MySQL connection info '%s' as URL: %v. Cannot extract DB name.", sanitizedConnectionInfo, err)
		return "" // Return empty on parse error
	}

	// The database name is the path component, stripping the leading '/' if present.
	dbName := u.Path
	dbName = strings.TrimPrefix(dbName, "/") // Use TrimPrefix directly

	// The go-sql-driver/mysql can handle DSNs without a path/dbname
	// (e.g., connecting to the default database). Return empty string in that case.
	if dbName == "" {
		return ""
	}

	// The Path might still contain parameters if the original DSN was just `/dbname?param=val`
	// and we added a dummy host. Check for '?' again.
	if qMark := strings.Index(dbName, "?"); qMark != -1 {
		dbName = dbName[:qMark]
	}

	return dbName
}

// redactSensitiveInfo redacts sensitive information (e.g., password) from a MySQL DSN string.
func redactSensitiveInfo(dsn string) string {
	// Parse the DSN to extract components. Add a dummy scheme if needed for parsing,
	// similar to how extractDBNameFromMySQLInfo does, but focus just on enabling parsing.
	parseInput := dsn
	needsDummyScheme := false
	if !strings.Contains(parseInput, "://") {
		// Add dummy scheme if it's likely a DSN needing one (contains '@' or starts without '/')
		if strings.Contains(parseInput, "@") || (!strings.HasPrefix(parseInput, "/") && strings.Contains(parseInput, "(")) {
			parseInput = "dummy://" + parseInput
			needsDummyScheme = true
		} else if strings.HasPrefix(parseInput, "/") {
			// Handle path-only or path-with-params DSNs like "/dbname?..."
			parseInput = "dummy://dummyhost" + parseInput
			needsDummyScheme = true
		}
		// Note: Plain "dbname" without scheme/user/host/params might fail parsing, which is acceptable.
	}

	u, err := url.Parse(parseInput)
	if err != nil {
		// If parsing fails even with added scheme, return a generic redacted string
		// as we cannot reliably locate the password. Avoid logging the raw DSN.
		log.Printf("DEBUG: Failed to parse DSN for redaction: %v. Returning generic redaction.", err)
		return "[REDACTED DSN]"
	}

	// Redact the password if present in the UserInfo
	if u.User != nil {
		_, hasPassword := u.User.Password()
		if hasPassword {
			u.User = url.UserPassword(u.User.Username(), "[REDACTED]")
		}
	}

	// Reconstruct the string. If we added a dummy scheme/host, remove it.
	sanitized := u.String()
	if needsDummyScheme {
		if strings.HasPrefix(sanitized, "dummy://dummyhost") {
			sanitized = strings.TrimPrefix(sanitized, "dummy://dummyhost")
		} else if strings.HasPrefix(sanitized, "dummy://") {
			sanitized = strings.TrimPrefix(sanitized, "dummy://")
		}
	}

	return sanitized
}

// validateAndFixSchema checks and fixes the database schema if needed
func validateAndFixSchema(db *gorm.DB, dbType, connectionInfo string, debug bool, lgr *slog.Logger) error {
	migrator := db.Migrator()
	var schemaCorrect bool
	var err error

	switch dbType {
	case "sqlite":
		schemaCorrect, err = hasCorrectImageCacheIndexSQLite(db, debug)
		if err != nil {
			enhancedErr := errors.New(err).
				Component("datastore").
				Category(errors.CategoryDatabase).
				Context("operation", "schema_validation").
				Context("db_type", dbType).
				Context("table", "image_caches").
				Build()
			
			lgr.Error("Schema validation failed", "error", enhancedErr)
			return enhancedErr
		}
	case "mysql":
		// Need to extract dbName from connectionInfo for MySQL check
		dbName := extractDBNameFromMySQLInfo(connectionInfo)
		if dbName == "" {
			lgr.Warn("Could not determine database name from connection info for MySQL schema check. Assuming schema is correct.")
			schemaCorrect = true // Avoid dropping if we can't check
		} else {
			schemaCorrect, err = hasCorrectImageCacheIndexMySQL(db, dbName, debug)
			if err != nil {
				enhancedErr := errors.New(err).
					Component("datastore").
					Category(errors.CategoryDatabase).
					Context("operation", "schema_validation").
					Context("db_type", dbType).
					Context("table", "image_caches").
					Context("database", dbName).
					Build()
				
				lgr.Error("Schema validation failed", "error", enhancedErr)
				return enhancedErr
			}
		}
	default:
		lgr.Warn("Unsupported database type for image_caches schema check. Assuming schema is correct.",
			"db_type", dbType)
		schemaCorrect = true // Avoid dropping for unsupported types
	}
	
	lgr.Info("Schema validation completed",
		"schema_correct", schemaCorrect)

	if !schemaCorrect {
		if migrator.HasTable(&ImageCache{}) {
			if debug {
				lgr.Debug("Incorrect schema detected for 'image_caches'. Dropping table")
			}
			if err := migrator.DropTable(&ImageCache{}); err != nil {
				return fmt.Errorf("failed to drop existing 'image_caches' table with incorrect schema: %w", err)
			}
		} else if debug {
			lgr.Debug("'image_caches' table does not exist. AutoMigrate will create it")
		}
	} else {
		if debug {
			lgr.Debug("Schema for 'image_caches' appears correct. Skipping drop")
		}
	}
	
	return nil
}

// migrateTables performs the actual table migrations
func migrateTables(db *gorm.DB, dbType string, lgr *slog.Logger) (int, error) {
	tableMappings := []struct {
		model interface{}
		name  string
	}{
		{&Note{}, "notes"},
		{&Results{}, "results"},
		{&NoteReview{}, "note_reviews"},
		{&NoteComment{}, "note_comments"},
		{&DailyEvents{}, "daily_events"},
		{&HourlyWeather{}, "hourly_weather"},
		{&NoteLock{}, "note_locks"},
		{&ImageCache{}, "image_caches"},
	}
	
	lgr.Info("Starting table migrations",
		"table_count", len(tableMappings))
	
	// Migrate each table individually for better logging
	successCount := 0
	for _, table := range tableMappings {
		if err := migrateTable(db, table.model, table.name, dbType, lgr); err != nil {
			return successCount, err
		}
		successCount++
	}
	
	return successCount, nil
}

// migrateTable migrates a single table with detailed logging
func migrateTable(db *gorm.DB, model interface{}, tableName, dbType string, lgr *slog.Logger) error {
	tableStart := time.Now()
	
	// Check if table exists before migration
	tableExists := db.Migrator().HasTable(model)
	
	lgr.Debug("Migrating table",
		"table", tableName,
		"exists", tableExists)
	
	// Get column information before migration (if table exists)
	columnsBefore := getTableColumns(db, model, tableExists)
	
	if err := db.AutoMigrate(model); err != nil {
		enhancedErr := errors.New(err).
			Component("datastore").
			Category(errors.CategoryDatabase).
			Context("operation", "auto_migrate_table").
			Context("db_type", dbType).
			Context("table", tableName).
			Build()
		
		lgr.Error("Table migration failed",
			"table", tableName,
			"error", enhancedErr)
		return enhancedErr
	}
	
	// Determine what changed
	action, addedColumns := determineTableChanges(db, model, tableExists, columnsBefore)
	
	// Log migration result
	logTableMigration(lgr, tableName, action, addedColumns, time.Since(tableStart))
	
	return nil
}

// getTableColumns retrieves column names for a table
func getTableColumns(db *gorm.DB, model interface{}, tableExists bool) []string {
	var columns []string
	if tableExists {
		if cols, err := db.Migrator().ColumnTypes(model); err == nil {
			for _, col := range cols {
				columns = append(columns, col.Name())
			}
		}
	}
	return columns
}

// determineTableChanges checks what changed after migration
func determineTableChanges(db *gorm.DB, model interface{}, tableExists bool, columnsBefore []string) (action string, addedColumns []string) {
	action = "updated"
	
	if !tableExists {
		action = "created"
		// Get all columns for newly created table
		if cols, err := db.Migrator().ColumnTypes(model); err == nil {
			for _, col := range cols {
				addedColumns = append(addedColumns, col.Name())
			}
		}
	} else {
		// Check for new columns added
		addedColumns = findNewColumns(db, model, columnsBefore)
		if len(addedColumns) == 0 {
			action = "unchanged"
		}
	}
	
	return action, addedColumns
}

// findNewColumns identifies columns added during migration
func findNewColumns(db *gorm.DB, model interface{}, columnsBefore []string) []string {
	var addedColumns []string
	
	if cols, err := db.Migrator().ColumnTypes(model); err == nil {
		for _, col := range cols {
			colName := col.Name()
			if !slices.Contains(columnsBefore, colName) {
				addedColumns = append(addedColumns, colName)
			}
		}
	}
	
	return addedColumns
}

// logTableMigration logs the result of a table migration
func logTableMigration(lgr *slog.Logger, tableName, action string, addedColumns []string, duration time.Duration) {
	logFields := []any{
		"table", tableName,
		"action", action,
		"duration", duration,
	}
	
	if len(addedColumns) > 0 {
		logFields = append(logFields, "columns_added", len(addedColumns))
		if len(addedColumns) <= MaxColumnsForDetailedDisplay {
			logFields = append(logFields, "new_columns", addedColumns)
		}
	}
	
	lgr.Info("Table migration completed", logFields...)
}
