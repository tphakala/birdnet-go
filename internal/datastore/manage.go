package datastore

import (
	"fmt"
	"log"
	"os"
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

// performAutoMigration automates database migrations with error handling.
func performAutoMigration(db *gorm.DB, debug bool, dbType, connectionInfo string) error {
	if err := db.AutoMigrate(&Note{}, &Results{}, &NoteReview{}, &NoteComment{}, &DailyEvents{}, &HourlyWeather{}, &NoteLock{}, &ImageCache{}); err != nil {
		return fmt.Errorf("failed to auto-migrate %s database: %w", dbType, err)
	}

	if debug {
		log.Printf("%s database initialized successfully", dbType)
	}

	return nil
}
