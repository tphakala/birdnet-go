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

func validateMySQLConfig(settings *conf.Settings) error {
	// Add validation logic for MySQL configuration
	// Return an error if the configuration is invalid
	return nil
}

// InitializeDatabase sets up the MySQL database connection
func (store *MySQLStore) Open() error {
	if err := validateMySQLConfig(store.Settings); err != nil {
		return err // validateMySQLConfig returns a properly formatted error
	}

	dsn := fmt.Sprintf("%s:%s@tcp(%s:%s)/%s?charset=utf8mb4&parseTime=True&loc=Local",
		store.Settings.Output.MySQL.Username, store.Settings.Output.MySQL.Password,
		store.Settings.Output.MySQL.Host, store.Settings.Output.MySQL.Port,
		store.Settings.Output.MySQL.Database)

	// Create a new GORM logger
	newLogger := createGormLogger()

	// Open the MySQL database
	db, err := gorm.Open(mysql.Open(dsn), &gorm.Config{Logger: newLogger})
	if err != nil {
		log.Printf("Failed to open MySQL database: %v\n", err)
		return fmt.Errorf("failed to open MySQL database: %v", err)
	}

	store.DB = db
	return performAutoMigration(db, store.Settings.Debug, "MySQL", dsn)
}

// Close MySQL database connections
func (store *MySQLStore) Close() error {
	// Ensure that the store's DB field is not nil to avoid a panic
	if store.DB == nil {
		return fmt.Errorf("database connection is not initialized")
	}

	// Retrieve the generic database object from the GORM DB object
	sqlDB, err := store.DB.DB()
	if err != nil {
		log.Printf("Failed to retrieve generic DB object: %v\n", err)
		return err
	}

	// Close the generic database object, which closes the underlying SQL database connection
	if err := sqlDB.Close(); err != nil {
		log.Printf("Failed to close MySQL database: %v\n", err)
		return err
	}

	return nil
}
