package v2

import (
	"fmt"
	"time"

	"github.com/tphakala/birdnet-go/internal/datastore/v2/entities"
	"gorm.io/driver/mysql"
	"gorm.io/gorm"
	"gorm.io/gorm/logger"
	"gorm.io/gorm/schema"
)

// MySQLConfig holds MySQL-specific configuration for the v2 manager.
type MySQLConfig struct {
	Host     string
	Port     string
	Username string
	Password string
	Database string
	Debug    bool
}

// MySQLManager handles the v2 normalized database for MySQL.
// Unlike SQLite which uses a separate file, MySQL uses table prefixes
// to coexist with legacy tables in the same database.
type MySQLManager struct {
	db       *gorm.DB
	config   MySQLConfig
	location string // host:port/database for display
}

// v2TablePrefix is the prefix used for all v2 tables in MySQL.
const v2TablePrefix = "v2_"

// NewMySQLManager creates a new v2 MySQL database manager.
// V2 tables will be created with the "v2_" prefix in the same database as legacy tables.
func NewMySQLManager(cfg *MySQLConfig) (*MySQLManager, error) {
	dsn := fmt.Sprintf("%s:%s@tcp(%s:%s)/%s?charset=utf8mb4&parseTime=True&loc=Local",
		cfg.Username, cfg.Password, cfg.Host, cfg.Port, cfg.Database)

	logLevel := logger.Silent
	if cfg.Debug {
		logLevel = logger.Info
	}

	db, err := gorm.Open(mysql.Open(dsn), &gorm.Config{
		Logger: logger.Default.LogMode(logLevel),
		NamingStrategy: schema.NamingStrategy{
			TablePrefix: v2TablePrefix, // Add prefix to all v2 tables
		},
	})
	if err != nil {
		return nil, fmt.Errorf("failed to open MySQL database for v2: %w", err)
	}

	// Configure connection pool
	sqlDB, err := db.DB()
	if err != nil {
		return nil, fmt.Errorf("failed to get underlying database: %w", err)
	}
	sqlDB.SetMaxIdleConns(10)
	sqlDB.SetMaxOpenConns(100)
	sqlDB.SetConnMaxLifetime(time.Hour)

	return &MySQLManager{
		db:       db,
		config:   *cfg,
		location: fmt.Sprintf("%s:%s/%s", cfg.Host, cfg.Port, cfg.Database),
	}, nil
}

// Initialize creates the v2 schema tables with the v2_ prefix.
func (m *MySQLManager) Initialize() error {
	// Run GORM auto-migrations for all entities
	// Tables will be created with v2_ prefix due to NamingStrategy
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

	// Initialize migration state singleton using FirstOrCreate to handle race conditions
	state := entities.MigrationState{ID: 1, State: entities.MigrationStatusIdle}
	if err := m.db.FirstOrCreate(&state, entities.MigrationState{ID: 1}).Error; err != nil {
		return fmt.Errorf("failed to initialize migration state: %w", err)
	}

	// Seed default AI model (BirdNET)
	return m.seedDefaultModel()
}

// seedDefaultModel ensures the BirdNET model exists in the registry.
func (m *MySQLManager) seedDefaultModel() error {
	model := entities.AIModel{
		Name:      "BirdNET",
		Version:   "2.4",
		ModelType: entities.ModelTypeBird,
	}
	// Use FirstOrCreate to avoid duplicates
	result := m.db.Where("name = ? AND version = ?", model.Name, model.Version).FirstOrCreate(&model)
	if result.Error != nil {
		return fmt.Errorf("failed to seed BirdNET model: %w", result.Error)
	}
	return nil
}

// DB returns the underlying GORM database.
func (m *MySQLManager) DB() *gorm.DB {
	return m.db
}

// Path returns the database location (host:port/database).
func (m *MySQLManager) Path() string {
	return m.location
}

// Close closes the database connection.
func (m *MySQLManager) Close() error {
	sqlDB, err := m.db.DB()
	if err != nil {
		return fmt.Errorf("failed to get underlying database: %w", err)
	}
	return sqlDB.Close()
}

// Delete removes all v2 tables from the MySQL database.
// This should only be called during rollback or cleanup.
func (m *MySQLManager) Delete() error {
	// Drop tables in reverse dependency order
	tables := []string{
		v2TablePrefix + "detection_locks",
		v2TablePrefix + "detection_comments",
		v2TablePrefix + "detection_reviews",
		v2TablePrefix + "detection_predictions",
		v2TablePrefix + "detections",
		v2TablePrefix + "audio_sources",
		v2TablePrefix + "model_labels",
		v2TablePrefix + "ai_models",
		v2TablePrefix + "labels",
		v2TablePrefix + "migration_state",
	}

	for _, table := range tables {
		// Use raw SQL to drop tables to avoid GORM's safety checks
		if err := m.db.Exec("DROP TABLE IF EXISTS " + table).Error; err != nil {
			return fmt.Errorf("failed to drop table %s: %w", table, err)
		}
	}

	return nil
}

// Exists checks if v2 tables exist in the MySQL database.
func (m *MySQLManager) Exists() bool {
	// Check if the v2_migration_state table exists as an indicator
	return m.db.Migrator().HasTable(v2TablePrefix + "migration_state")
}

// IsMySQL returns true for MySQL manager.
func (m *MySQLManager) IsMySQL() bool {
	return true
}

// TablePrefix returns the table prefix for v2 tables.
func (m *MySQLManager) TablePrefix() string {
	return v2TablePrefix
}

// V2TableName returns the full table name with v2 prefix for explicit queries.
// Use this when you need to do raw SQL or explicit Table() calls.
func V2TableName(baseName string) string {
	return v2TablePrefix + baseName
}

// RenameTablesAfterMigration renames v2_ prefixed tables to their final names
// and legacy tables to legacy_ prefix. This is called during cutover.
func (m *MySQLManager) RenameTablesAfterMigration() error {
	// This will be implemented in the cutover phase
	// For now, just return nil as a placeholder
	return nil
}
