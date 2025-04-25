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

// hasCorrectImageCacheIndexSQLite checks if the SQLite database has the correct
// composite unique index on the image_caches table.
func hasCorrectImageCacheIndexSQLite(db *gorm.DB) (bool, error) {
	var indexes []struct {
		Name   string `gorm:"column:name"`
		SQL    string `gorm:"column:sql"`
		Unique int    `gorm:"column:origin"` // For PRAGMA index_list, 'u' means unique index created by UNIQUE constraint
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
		log.Printf("DEBUG: SQLite Found index: Name=%s, SQL=%s, Unique=%d", idx.Name, idx.SQL, idx.Unique)

		// Check if the current index is the target index and if it's correct
		if idx.Name == targetIndexName {
			var info []struct {
				Name string `gorm:"column:name"`
			}
			if err := db.Raw("PRAGMA index_info(?)", idx.Name).Scan(&info).Error; err != nil {
				log.Printf("WARN: Failed to get info for index %s: %v", idx.Name, err)
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
				if hasProvider && hasScientific && strings.Contains(strings.ToUpper(idx.SQL), "UNIQUE INDEX") {
					correctIndexFound = true
					log.Printf("DEBUG: SQLite Found correct composite unique index: %s", idx.Name)
					// Do not break here yet, we need to check for incorrect indexes too.
				}
			}
		}

		// Separately, check if the current index is the known incorrect single-column unique index
		if strings.Contains(strings.ToUpper(idx.SQL), "UNIQUE") {
			var info []struct {
				Name string `gorm:"column:name"`
			}
			if err := db.Raw("PRAGMA index_info(?)", idx.Name).Scan(&info).Error; err == nil {
				if len(info) == 1 && info[0].Name == "scientific_name" {
					incorrectIndexFound = true
					log.Printf("DEBUG: SQLite Found incorrect single-column unique index on scientific_name: %s", idx.Name)
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
func hasCorrectImageCacheIndexMySQL(db *gorm.DB, dbName string) (bool, error) {
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

	// Analyze the results
	correctIndexColumns := make(map[string]bool)
	foundCorrectIndex := false
	foundIncorrectIndex := false

	for _, stat := range stats {
		log.Printf("DEBUG: MySQL Found index stat: Index=%s, Column=%s, Seq=%d, NonUnique=%d",
			stat.IndexName, stat.ColumnName, stat.SeqInIndex, stat.NonUnique)

		// Check if this row belongs to our target composite index
		if stat.IndexName == targetIndexName {
			if stat.NonUnique == 0 { // Check if it's unique
				if stat.ColumnName == "provider_name" && stat.SeqInIndex == 1 {
					correctIndexColumns["provider_name"] = true
				}
				if stat.ColumnName == "scientific_name" && stat.SeqInIndex == 2 {
					correctIndexColumns["scientific_name"] = true
				}
			}
		} else if stat.NonUnique == 0 { // Check other unique indexes for the incorrect one
			// See if this unique index is ONLY on scientific_name
			if stat.ColumnName == "scientific_name" && stat.SeqInIndex == 1 {
				// To confirm it's ONLY on scientific_name, check if other rows exist for this index
				isSingleColumn := true
				for _, otherStat := range stats {
					if otherStat.IndexName == stat.IndexName && otherStat.SeqInIndex > 1 {
						isSingleColumn = false
						break
					}
				}
				if isSingleColumn {
					foundIncorrectIndex = true
					log.Printf("DEBUG: MySQL Found incorrect single-column unique index on scientific_name: %s", stat.IndexName)
				}
			}
		}
	}

	// Check if we found both required columns in the target index
	if correctIndexColumns["provider_name"] && correctIndexColumns["scientific_name"] {
		foundCorrectIndex = true
		log.Printf("DEBUG: MySQL Found correct composite unique index: %s", targetIndexName)
	}

	log.Printf("DEBUG: MySQL Schema Check Result: foundCorrectIndex=%v, foundIncorrectIndex=%v", foundCorrectIndex, foundIncorrectIndex)

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
		schemaCorrect, err = hasCorrectImageCacheIndexSQLite(db)
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
			schemaCorrect, err = hasCorrectImageCacheIndexMySQL(db, dbName)
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
