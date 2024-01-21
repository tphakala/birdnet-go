package model

import (
	"fmt"

	"github.com/tphakala/birdnet-go/internal/config"
	"github.com/tphakala/birdnet-go/internal/logger"
	"gorm.io/driver/mysql"
	"gorm.io/gorm"
)

// MySQLStore implements DataStore for MySQL
type MySQLStore struct {
	DataStore
}

func validateMySQLConfig(ctx *config.Context) error {
	// Add validation logic for MySQL configuration
	// Return an error if the configuration is invalid
	return nil
}

// InitializeDatabase sets up the MySQL database connection
func (store *MySQLStore) Open(ctx *config.Context) error {
	if err := validateMySQLConfig(ctx); err != nil {
		return err // validateMySQLConfig returns a properly formatted error
	}

	dsn := fmt.Sprintf("%s:%s@tcp(%s:%s)/%s?charset=utf8mb4&parseTime=True&loc=Local",
		ctx.Settings.Output.MySQL.Username, ctx.Settings.Output.MySQL.Password, ctx.Settings.Output.MySQL.Host, ctx.Settings.Output.MySQL.Port, ctx.Settings.Output.MySQL.Database)

	db, err := gorm.Open(mysql.Open(dsn), &gorm.Config{})
	if err != nil {
		return fmt.Errorf("failed to open MySQL database: %v", err)
	}

	store.DB = db
	return performAutoMigration(db, ctx.Settings.Debug, "MySQL", dsn)
}

// SaveToDatabase inserts a new Note record into the SQLite database
func (store *MySQLStore) Save(ctx *config.Context, note Note) error {
	if store.DB == nil {
		return fmt.Errorf("database connection is not initialized")
	}

	if err := store.DB.Create(&note).Error; err != nil {
		logger.Error("main", "Failed to save note: %v\n", err)
		return err
	}

	logger.Debug("main", "Saved note: %v\n", note)

	return nil
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
		logger.Error("main", "Failed to retrieve generic DB object: %v", err)
		return err
	}

	// Close the generic database object, which closes the underlying SQL database connection
	if err := sqlDB.Close(); err != nil {
		logger.Error("main", "Failed to close MySQL database: %v", err)
		return err
	}

	logger.Info("main", "MySQL database successfully closed")
	return nil
}
