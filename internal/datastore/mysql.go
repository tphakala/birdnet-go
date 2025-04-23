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
		return fmt.Errorf("failed to open MySQL database: %w", err)
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

// UpdateNote updates specific fields of a note in MySQL
func (m *MySQLStore) UpdateNote(id string, updates map[string]interface{}) error {
	return m.DB.Model(&Note{}).Where("id = ?", id).Updates(updates).Error
}

// Save stores a note and its associated results as a single transaction in the database.

// GetAllLockedNotes returns all notes that have an entry in the note_locks table
func (ms *MySQLStore) GetAllLockedNotes() ([]Note, error) {
	var notes []Note
	
	// Use a join between notes and note_locks tables to find all locked notes
	err := ms.DB.Joins("JOIN note_locks ON notes.id = note_locks.note_id").Find(&notes).Error
	if err != nil {
		return nil, fmt.Errorf("error retrieving locked notes: %w", err)
	}
	
	return notes, nil
}
