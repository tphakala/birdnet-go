package datastore

import (
	"fmt"
	"log"

	"github.com/tphakala/birdnet-go/internal/conf"

	"gorm.io/driver/mysql"
	"gorm.io/gorm"
)

// MySQLStore implements DataStore for MySQL
type MySQLStore struct {
	DataStore
	Settings *conf.Settings
}

func validateMySQLConfig() error {
	// Add validation logic for MySQL configuration
	// Return an error if the configuration is invalid
	return nil
}

// InitializeDatabase sets up the MySQL database connection
func (store *MySQLStore) Open() error {
	if err := validateMySQLConfig(); err != nil {
		return err // validateMySQLConfig returns a properly formatted error
	}

	// Get a component-specific logger
	mysqlLogger := store.getDbLogger("mysql")

	dsn := fmt.Sprintf("%s:%s@tcp(%s:%s)/%s?charset=utf8mb4&parseTime=True&loc=Local",
		store.Settings.Output.MySQL.Username, store.Settings.Output.MySQL.Password,
		store.Settings.Output.MySQL.Host, store.Settings.Output.MySQL.Port,
		store.Settings.Output.MySQL.Database)

	// Create a new GORM logger
	newLogger := createGormLogger(store.Logger)

	// Open the MySQL database
	db, err := gorm.Open(mysql.Open(dsn), &gorm.Config{Logger: newLogger})
	if err != nil {
		if mysqlLogger != nil {
			mysqlLogger.Error("Failed to open MySQL database",
				"host", store.Settings.Output.MySQL.Host,
				"port", store.Settings.Output.MySQL.Port,
				"database", store.Settings.Output.MySQL.Database,
				"error", err)
		} else {
			log.Printf("Failed to open MySQL database: %v\n", err)
		}
		return fmt.Errorf("failed to open MySQL database: %w", err)
	}

	store.DB = db
	return performAutoMigration(db, store.Settings.Debug, "MySQL", dsn, store.Logger)
}

// Close MySQL database connections
func (store *MySQLStore) Close() error {
	// Get a component-specific logger
	mysqlLogger := store.getDbLogger("mysql")

	// Ensure that the store's DB field is not nil to avoid a panic
	if store.DB == nil {
		if mysqlLogger != nil {
			mysqlLogger.Error("Database connection is not initialized")
		}
		return fmt.Errorf("database connection is not initialized")
	}

	// Retrieve the generic database object from the GORM DB object
	sqlDB, err := store.DB.DB()
	if err != nil {
		if mysqlLogger != nil {
			mysqlLogger.Error("Failed to retrieve generic DB object", "error", err)
		} else {
			log.Printf("Failed to retrieve generic DB object: %v\n", err)
		}
		return err
	}

	// Close the generic database object, which closes the underlying SQL database connection
	if err := sqlDB.Close(); err != nil {
		if mysqlLogger != nil {
			mysqlLogger.Error("Failed to close MySQL database", "error", err)
		} else {
			log.Printf("Failed to close MySQL database: %v\n", err)
		}
		return err
	}

	if mysqlLogger != nil && store.Settings.Debug {
		mysqlLogger.Debug("MySQL database connection closed successfully")
	}
	return nil
}

// UpdateNote updates specific fields of a note in MySQL
func (store *MySQLStore) UpdateNote(id uint, fields map[string]interface{}) error {
	if err := store.DB.Model(&Note{}).Where("id = ?", id).Updates(fields).Error; err != nil {
		return fmt.Errorf("failed to update note: %w", err)
	}
	return nil
}

// Save stores a note and its associated results as a single transaction in the database.
