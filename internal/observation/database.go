package observation

import (
	"errors"
	"fmt"

	"gorm.io/driver/mysql"
	"gorm.io/driver/sqlite"
	"gorm.io/gorm"
)

// db is a package-level variable holding the instance of the GORM database connection.
var db *gorm.DB

// DatabaseConfig holds configuration details for database connection.
// Its fields include support for SQLite and MySQL databases.
type DatabaseConfig struct {
	Type     string // "sqlite" or "mysql"
	Host     string
	Port     string
	User     string
	Password string
	Name     string // Database name
}

// defaultDatabaseType is a constant that defines the default database type.
// This is used when no type is specified in the configuration.
const defaultDatabaseType = "sqlite"

// InitializeDatabase sets up the database connection using the provided configuration.
// It supports SQLite and MySQL databases and performs automatic migration for the Note model.
func InitializeDatabase(config DatabaseConfig) error {
	var err error

	// Set the default database type if none is specified.
	if config.Type == "" {
		config.Type = defaultDatabaseType
	}

	// Initialize the appropriate database driver based on the configuration type.
	switch config.Type {
	case "sqlite":
		db, err = gorm.Open(sqlite.Open("./birdnet.db"), &gorm.Config{})
	case "mysql":
		dsn := fmt.Sprintf("%s:%s@tcp(%s:%s)/%s?charset=utf8mb4&parseTime=True&loc=Local",
			config.User, config.Password, config.Host, config.Port, config.Name)
		db, err = gorm.Open(mysql.Open(dsn), &gorm.Config{})
	default:
		// Return an error when the database type is not supported.
		return errors.New("unsupported database type")
	}

	if err != nil {
		// Return the error encountered while opening the database connection.
		return fmt.Errorf("failed to open database: %v", err)
	}

	// Perform auto-migration to create or update the tables based on the Note model.
	err = db.AutoMigrate(&Note{})
	if err != nil {
		// Return the error encountered during migration.
		return fmt.Errorf("failed to auto-migrate database: %v", err)
	}

	// Return nil if the database was initialized without errors.
	return nil
}

// SaveToDatabase inserts a new Note record into the database.
// It initializes the database connection if not already established.
func SaveToDatabase(note Note) error {
	// Initialize the database if it's not already connected.
	if db == nil {
		// Default to an empty configuration, which uses SQLite by default.
		// This may not be suitable for production - consider passing a valid configuration.
		if err := InitializeDatabase(DatabaseConfig{}); err != nil {
			// Return the error encountered during database initialization.
			return fmt.Errorf("failed to initialize database for saving note: %v", err)
		}
	}

	// Insert the new Note record into the database.
	return db.Create(&note).Error
}
