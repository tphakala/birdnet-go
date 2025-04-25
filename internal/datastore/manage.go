package datastore

import (
	"fmt"
	"log"
	"os"
	"strings"
	"time"

	"gorm.io/gorm"
	"gorm.io/gorm/logger"
)

// createGormLogger configures and returns a new GORM logger instance.
func createGormLogger() logger.Interface {
	return logger.New(
		log.New(os.Stdout, "\r\n", log.LstdFlags),
		logger.Config{
			SlowThreshold: 200 * time.Millisecond,
			LogLevel:      logger.Warn,
			Colorful:      true,
		},
	)
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

// hasCorrectImageCacheIndexSQLite checks if the SQLite database has the correct
// composite unique index on the image_caches table.
func hasCorrectImageCacheIndexSQLite(db *gorm.DB, debug bool) (bool, error) {
	var indexes []struct {
		Name   string `gorm:"column:name"`
		Unique int    `gorm:"column:unique"` // 1 == unique
	}

	// Check if the table exists first
	if !db.Migrator().HasTable(&ImageCache{}) {
		return false, nil // Table doesn't exist, so index can't be correct (will be created by AutoMigrate)
	}

	// Check index list for the table
	if err := db.Raw("PRAGMA index_list('image_caches')").Scan(&indexes).Error; err != nil {
		return false, fmt.Errorf("failed to query index list for image_caches: %w", err)
	}

	correctIndexFound := false
	incorrectIndexFound := false
	targetIndexName := "idx_imagecache_provider_species" // Expected index name from GORM tags

	for _, idx := range indexes {
		if debug {
			log.Printf("DEBUG: SQLite Found index: Name=%s, Unique=%d", idx.Name, idx.Unique)
		}

		// Check if the current index is the target index and if it's correct
		if idx.Name == targetIndexName {
			// Use helper function to get index info
			info, err := getSQLiteIndexInfo(db, idx.Name, debug)
			if err != nil {
				// Error already logged by helper, just continue to next index
				continue
			}

			if len(info) == 2 {
				hasProvider := false
				hasScientific := false
				for _, col := range info {
					if col.Name == "provider_name" {
						hasProvider = true
					}
					if col.Name == "scientific_name" {
						hasScientific = true
					}
				}
				if hasProvider && hasScientific && idx.Unique == 1 {
					correctIndexFound = true
					if debug {
						log.Printf("DEBUG: SQLite Found correct composite unique index: %s", idx.Name)
					}
					// Do not break here yet, we need to check for incorrect indexes too.
				}
			}
		}

		// Separately, check if the current index is the known incorrect single-column unique index
		// Check using the Unique flag from index_list and index_info results
		if idx.Unique == 1 { // Check if the index itself is marked unique
			// Use helper function to get index info again
			info, err := getSQLiteIndexInfo(db, idx.Name, debug)
			// If error getting info, we can't determine if it's the incorrect index, so skip
			if err == nil {
				// Now check if this unique index is ONLY on scientific_name
				if len(info) == 1 && info[0].Name == "scientific_name" {
					incorrectIndexFound = true
					if debug {
						log.Printf("DEBUG: SQLite Found incorrect single-column unique index on scientific_name: %s", idx.Name)
					}
				}
			}
		}

		// Optimization: If we've found both the correct index and an incorrect one,
		// we know the state and can stop searching early.
		if correctIndexFound && incorrectIndexFound {
			break
		}
	}

	// If we found the correct index AND did not find the specific incorrect one, the schema is okay.
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
	migrator := db.Migrator()
	var schemaCorrect bool // Declare without initial assignment
	var err error

	switch dbType {
	case "sqlite":
		schemaCorrect, err = hasCorrectImageCacheIndexSQLite(db, debug)
		if err != nil {
			return fmt.Errorf("failed to check SQLite schema for image_caches: %w", err)
		}
	case "mysql":
		// Need to extract dbName from connectionInfo for MySQL check
		dbName := extractDBNameFromMySQLInfo(connectionInfo) // Helper function needed
		if dbName == "" {
			log.Printf("WARN: Could not determine database name from connection info for MySQL schema check. Assuming schema is correct.")
			schemaCorrect = true // Avoid dropping if we can't check
		} else {
			schemaCorrect, err = hasCorrectImageCacheIndexMySQL(db, dbName, debug)
			if err != nil {
				return fmt.Errorf("failed to check MySQL schema for image_caches: %w", err)
			}
		}
	default:
		log.Printf("WARN: Unsupported database type '%s' for image_caches schema check. Assuming schema is correct.", dbType)
		schemaCorrect = true // Avoid dropping for unsupported types
	}

	if !schemaCorrect {
		if migrator.HasTable(&ImageCache{}) {
			if debug {
				log.Printf("Debug: Incorrect schema detected for 'image_caches'. Dropping table.")
			}
			if err := migrator.DropTable(&ImageCache{}); err != nil {
				return fmt.Errorf("failed to drop existing 'image_caches' table with incorrect schema: %w", err)
			}
		} else if debug {
			log.Printf("Debug: 'image_caches' table does not exist. AutoMigrate will create it.")
		}
	} else {
		if debug {
			log.Printf("Debug: Schema for 'image_caches' appears correct. Skipping drop.")
		}
	}

	// Perform the auto-migration for all necessary tables.
	if err := db.AutoMigrate(&Note{}, &Results{}, &NoteReview{}, &NoteComment{}, &DailyEvents{}, &HourlyWeather{}, &NoteLock{}, &ImageCache{}); err != nil {
		return fmt.Errorf("failed to auto-migrate %s database: %w", dbType, err)
	}

	if debug {
		log.Printf("%s database initialized successfully", dbType)
	}

	return nil
}

// extractDBNameFromMySQLInfo parses the database name from a MySQL DSN string.
// Example DSN: user:pass@tcp(127.0.0.1:3306)/dbname?charset=utf8mb4&parseTime=True&loc=Local
func extractDBNameFromMySQLInfo(connectionInfo string) string {
	// Find the last '/' which usually precedes the database name
	lastSlash := strings.LastIndex(connectionInfo, "/")
	if lastSlash == -1 {
		return "" // '/' not found
	}

	// Extract the part after '/'
	dbAndParams := connectionInfo[lastSlash+1:]

	// Find the '?' which separates dbname from parameters
	qMark := strings.Index(dbAndParams, "?")
	if qMark == -1 {
		return dbAndParams // No parameters, the whole part is the dbname
	}

	// Return the part before '?'
	return dbAndParams[:qMark]
}
