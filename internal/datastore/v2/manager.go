// Package v2 provides the v2 normalized database implementation.
package v2

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/tphakala/birdnet-go/internal/datastore/v2/entities"
	"github.com/tphakala/birdnet-go/internal/detection"
	"gorm.io/driver/sqlite"
	"gorm.io/gorm"
	"gorm.io/gorm/logger"
)

// Manager defines the interface for v2 database operations.
type Manager interface {
	// Initialize creates the schema and seeds initial data.
	Initialize() error
	// DB returns the underlying GORM database.
	DB() *gorm.DB
	// Path returns the database location (file path for SQLite, connection string info for MySQL).
	Path() string
	// Close closes the database connection.
	Close() error
	// Delete removes the v2 database (file for SQLite, tables for MySQL).
	Delete() error
	// Exists checks if the v2 database exists.
	Exists() bool
	// IsMySQL returns true if this is a MySQL manager.
	IsMySQL() bool
}

// Config holds database configuration for the v2 manager.
type Config struct {
	// DataDir is the directory containing database files (SQLite).
	DataDir string
	// Debug enables verbose logging.
	Debug bool
}

// SQLiteManager handles the v2 normalized database for SQLite.
type SQLiteManager struct {
	db     *gorm.DB
	dbPath string
}

// NewSQLiteManager creates a new v2 SQLite database manager.
// The database file will be created at DataDir/birdnet_v2.db.
func NewSQLiteManager(cfg Config) (*SQLiteManager, error) {
	dbPath := filepath.Join(cfg.DataDir, "birdnet_v2.db")

	logLevel := logger.Silent
	if cfg.Debug {
		logLevel = logger.Info
	}

	// Build DSN with recommended SQLite pragmas
	dsn := fmt.Sprintf("%s?_journal_mode=WAL&_busy_timeout=5000&_foreign_keys=ON", dbPath)

	db, err := gorm.Open(sqlite.Open(dsn), &gorm.Config{
		Logger: logger.Default.LogMode(logLevel),
	})
	if err != nil {
		return nil, fmt.Errorf("failed to open v2 database: %w", err)
	}

	return &SQLiteManager{
		db:     db,
		dbPath: dbPath,
	}, nil
}

// Initialize creates the schema and seeds initial data.
func (m *SQLiteManager) Initialize() error {
	// Run GORM auto-migrations for all entities
	err := m.db.AutoMigrate(
		&entities.Label{},
		&entities.AIModel{},
		&entities.ModelLabel{},
		&entities.AudioSource{},
		&entities.Detection{},
		&entities.DetectionPrediction{},
		&entities.DetectionReview{},
		&entities.DetectionComment{},
		&entities.DetectionLock{},
		&entities.MigrationState{},
	)
	if err != nil {
		return fmt.Errorf("failed to migrate v2 schema: %w", err)
	}

	// Fix SQLite foreign key constraints that GORM's AutoMigrate may not handle correctly.
	// SQLite requires ON DELETE clauses to be defined at table creation time.
	if err := m.fixSQLiteForeignKeys(); err != nil {
		return fmt.Errorf("failed to fix foreign key constraints: %w", err)
	}

	// Initialize migration state singleton using FirstOrCreate to handle race conditions
	state := entities.MigrationState{ID: 1, State: entities.MigrationStatusIdle}
	if err := m.db.FirstOrCreate(&state, entities.MigrationState{ID: 1}).Error; err != nil {
		return fmt.Errorf("failed to initialize migration state: %w", err)
	}

	// Seed default AI model (BirdNET)
	return m.seedDefaultModel()
}

// fixSQLiteForeignKeys ensures ON DELETE SET NULL behavior for SQLite.
// GORM's AutoMigrate doesn't always correctly apply ON DELETE clauses for SQLite.
// We use a trigger-based approach which is idempotent and doesn't conflict with GORM.
func (m *SQLiteManager) fixSQLiteForeignKeys() error {
	// Create a trigger to implement ON DELETE SET NULL for source_id.
	// This is more reliable than table recreation and works with GORM's AutoMigrate.
	// The trigger fires BEFORE DELETE and sets source_id to NULL for affected detections.
	triggerSQL := `
		CREATE TRIGGER IF NOT EXISTS trg_audio_source_delete_set_null
		BEFORE DELETE ON audio_sources
		FOR EACH ROW
		BEGIN
			UPDATE detections SET source_id = NULL WHERE source_id = OLD.id;
		END
	`
	return m.db.Exec(triggerSQL).Error
}

// seedDefaultModel ensures the default BirdNET model exists in the registry.
// Uses detection.DefaultModelName and detection.DefaultModelVersion to ensure
// consistency with the conversion layer's fallback logic.
func (m *SQLiteManager) seedDefaultModel() error {
	model := entities.AIModel{
		Name:      detection.DefaultModelName,
		Version:   detection.DefaultModelVersion,
		ModelType: entities.ModelTypeBird,
	}
	// Use FirstOrCreate to avoid duplicates
	result := m.db.Where("name = ? AND version = ?", model.Name, model.Version).FirstOrCreate(&model)
	if result.Error != nil {
		return fmt.Errorf("failed to seed default model: %w", result.Error)
	}
	return nil
}

// DB returns the underlying GORM database.
func (m *SQLiteManager) DB() *gorm.DB {
	return m.db
}

// Path returns the database file path.
func (m *SQLiteManager) Path() string {
	return m.dbPath
}

// Close closes the database connection.
func (m *SQLiteManager) Close() error {
	sqlDB, err := m.db.DB()
	if err != nil {
		return fmt.Errorf("failed to get underlying database: %w", err)
	}
	return sqlDB.Close()
}

// Delete removes the v2 database file.
// This should only be called during rollback or cleanup.
func (m *SQLiteManager) Delete() error {
	if err := m.Close(); err != nil {
		return fmt.Errorf("failed to close database before deletion: %w", err)
	}

	// Remove main database file
	if err := os.Remove(m.dbPath); err != nil && !os.IsNotExist(err) {
		return fmt.Errorf("failed to delete database file: %w", err)
	}

	// Also remove WAL and SHM files if they exist.
	// Errors are ignored since these files may not exist and cleanup
	// failures for auxiliary files shouldn't block database deletion.
	for _, suffix := range []string{"-wal", "-shm"} {
		walPath := m.dbPath + suffix
		_ = os.Remove(walPath)
	}

	return nil
}

// Exists checks if the v2 database file exists.
func (m *SQLiteManager) Exists() bool {
	_, err := os.Stat(m.dbPath)
	return err == nil
}

// IsMySQL returns false for SQLite manager.
func (m *SQLiteManager) IsMySQL() bool {
	return false
}

// TablePrefix returns the table prefix for v2 tables.
// For SQLite, this is empty since we use a separate database file.
// For MySQL, this would return "v2_".
func (m *SQLiteManager) TablePrefix() string {
	return ""
}

// ExistsFromPath checks if a v2 database exists at the given data directory.
// This is a helper function for detecting database state before creating a manager.
func ExistsFromPath(dataDir string) bool {
	dbPath := filepath.Join(dataDir, "birdnet_v2.db")
	_, err := os.Stat(dbPath)
	return err == nil
}

// GetDataDirFromLegacyPath extracts the data directory from a legacy database path.
// For example, "/data/birdnet.db" -> "/data"
func GetDataDirFromLegacyPath(legacyPath string) string {
	// Handle in-memory database for testing
	if legacyPath == ":memory:" || strings.HasPrefix(legacyPath, "file::memory:") {
		return ""
	}
	return filepath.Dir(legacyPath)
}
