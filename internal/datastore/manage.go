package datastore

import (
	"fmt"
	"log"
	"os"
	"time"

	"github.com/tphakala/birdnet-go/internal/logger"
	"gorm.io/gorm"
	gormlogger "gorm.io/gorm/logger"
)

// createGormLogger configures and returns a new GORM logger instance.
// If a custom logger is provided, it will use that for logging.
func createGormLogger(customLogger *logger.Logger) gormlogger.Interface {
	// If we have a custom logger, we could implement a custom GORM logger that uses it.
	// For now, we'll keep using the default GORM logger since it requires significant
	// adapter code to make it use our custom logger
	return gormlogger.New(
		log.New(os.Stdout, "\r\n", log.LstdFlags),
		gormlogger.Config{
			SlowThreshold: 200 * time.Millisecond,
			LogLevel:      gormlogger.Warn,
			Colorful:      true,
		},
	)
}

// performAutoMigration automates database migrations with error handling.
func performAutoMigration(db *gorm.DB, debug bool, dbType, connectionInfo string, customLogger *logger.Logger) error {
	migrationLogger := getMigrationLogger(customLogger)

	if err := db.AutoMigrate(&Note{}, &Results{}, &NoteReview{}, &NoteComment{}, &DailyEvents{}, &HourlyWeather{}, &NoteLock{}, &ImageCache{}); err != nil {
		if migrationLogger != nil {
			migrationLogger.Error("Failed to auto-migrate database",
				"db_type", dbType,
				"error", err)
		}
		return fmt.Errorf("failed to auto-migrate %s database: %w", dbType, err)
	}

	if debug {
		if migrationLogger != nil {
			migrationLogger.Info("Database initialized successfully",
				"db_type", dbType)
		} else {
			log.Printf("%s database initialized successfully", dbType)
		}
	}

	return nil
}

// getMigrationLogger returns a component-specific logger for migration operations
func getMigrationLogger(customLogger *logger.Logger) *logger.Logger {
	if customLogger == nil {
		return nil
	}

	return customLogger.Named("db.migration")
}
