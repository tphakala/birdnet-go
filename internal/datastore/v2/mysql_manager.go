package v2

import (
	"fmt"
	"time"

	"github.com/tphakala/birdnet-go/internal/datastore/v2/entities"
	"github.com/tphakala/birdnet-go/internal/detection"
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
	// UseV2Prefix controls whether tables use the "v2_" prefix.
	// Set to true for migration mode (tables coexist with legacy).
	// Set to false for fresh installs (clean table names).
	// Default (false) means no prefix for fresh installs.
	UseV2Prefix bool
}

// MySQLManager handles the v2 normalized database for MySQL.
// Unlike SQLite which uses a separate file, MySQL uses table prefixes
// to coexist with legacy tables in the same database.
type MySQLManager struct {
	db          *gorm.DB
	config      MySQLConfig
	location    string // host:port/database for display
	tablePrefix string // "" for fresh installs, "v2_" for migration
}

// v2TablePrefix is the prefix used for all v2 tables in MySQL.
const v2TablePrefix = "v2_"

// NewMySQLManager creates a new v2 MySQL database manager.
// If UseV2Prefix is true, tables will be created with the "v2_" prefix (migration mode).
// If UseV2Prefix is false, tables use clean names (fresh install mode).
func NewMySQLManager(cfg *MySQLConfig) (*MySQLManager, error) {
	dsn := fmt.Sprintf("%s:%s@tcp(%s:%s)/%s?charset=utf8mb4&parseTime=True&loc=Local",
		cfg.Username, cfg.Password, cfg.Host, cfg.Port, cfg.Database)

	logLevel := logger.Silent
	if cfg.Debug {
		logLevel = logger.Info
	}

	// Determine table prefix based on configuration
	tablePrefix := ""
	if cfg.UseV2Prefix {
		tablePrefix = v2TablePrefix
	}

	db, err := gorm.Open(mysql.Open(dsn), &gorm.Config{
		Logger: logger.Default.LogMode(logLevel),
		NamingStrategy: schema.NamingStrategy{
			TablePrefix: tablePrefix, // "" for fresh install, "v2_" for migration
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
		db:          db,
		config:      *cfg,
		location:    fmt.Sprintf("%s:%s/%s", cfg.Host, cfg.Port, cfg.Database),
		tablePrefix: tablePrefix,
	}, nil
}

// Initialize creates the v2 schema tables with the v2_ prefix.
func (m *MySQLManager) Initialize() error {
	// Run GORM auto-migrations for all entities
	// Tables will be created with v2_ prefix due to NamingStrategy
	err := m.db.AutoMigrate(
		// Lookup tables (must be created first due to FK constraints)
		&entities.LabelType{},
		&entities.TaxonomicClass{},
		// Core detection entities
		&entities.AIModel{},
		&entities.Label{},
		&entities.AudioSource{},
		&entities.Detection{},
		&entities.DetectionPrediction{},
		&entities.DetectionReview{},
		&entities.DetectionComment{},
		&entities.DetectionLock{},
		&entities.MigrationState{},
		&entities.MigrationDirtyID{},
		// Auxiliary tables
		&entities.DailyEvents{},
		&entities.HourlyWeather{},
		&entities.ImageCache{},
		&entities.DynamicThreshold{},
		&entities.ThresholdEvent{},
		&entities.NotificationHistory{},
		// Alert rules engine
		&entities.AlertRule{},
		&entities.AlertCondition{},
		&entities.AlertAction{},
		&entities.AlertHistory{},
	)
	if err != nil {
		return fmt.Errorf("failed to migrate v2 schema: %w", err)
	}

	// Initialize migration state singleton using FirstOrCreate to handle race conditions
	state := entities.MigrationState{ID: 1, State: entities.MigrationStatusIdle}
	if err := m.db.FirstOrCreate(&state, entities.MigrationState{ID: 1}).Error; err != nil {
		return fmt.Errorf("failed to initialize migration state: %w", err)
	}

	// Seed lookup tables
	if err := m.seedLookupTables(); err != nil {
		return fmt.Errorf("failed to seed lookup tables: %w", err)
	}

	// Seed default AI model (BirdNET)
	return m.seedDefaultModel()
}

// seedLookupTables seeds the label_types and taxonomic_classes tables with default values.
func (m *MySQLManager) seedLookupTables() error {
	// Seed label types
	for _, lt := range entities.DefaultLabelTypes() {
		if err := m.db.Where("name = ?", lt.Name).FirstOrCreate(&lt).Error; err != nil {
			return fmt.Errorf("failed to seed label type %q: %w", lt.Name, err)
		}
	}

	// Seed taxonomic classes
	for _, tc := range entities.DefaultTaxonomicClasses() {
		if err := m.db.Where("name = ?", tc.Name).FirstOrCreate(&tc).Error; err != nil {
			return fmt.Errorf("failed to seed taxonomic class %q: %w", tc.Name, err)
		}
	}

	return nil
}

// seedDefaultModel ensures the default BirdNET model exists in the registry.
// Uses detection.DefaultModelName, detection.DefaultModelVersion, and detection.DefaultModelVariant
// to ensure consistency with the conversion layer's fallback logic.
func (m *MySQLManager) seedDefaultModel() error {
	model := entities.AIModel{
		Name:      detection.DefaultModelName,
		Version:   detection.DefaultModelVersion,
		Variant:   detection.DefaultModelVariant,
		ModelType: entities.ModelTypeBird,
	}
	// Use FirstOrCreate to avoid duplicates
	result := m.db.Where("name = ? AND version = ? AND variant = ?", model.Name, model.Version, model.Variant).FirstOrCreate(&model)
	if result.Error != nil {
		return fmt.Errorf("failed to seed default model: %w", result.Error)
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
	// Use the stored prefix (could be "" for fresh install or "v2_" for migration)
	prefix := m.tablePrefix
	tables := []string{
		// Core detection tables (drop children first)
		prefix + "detection_locks",
		prefix + "detection_comments",
		prefix + "detection_reviews",
		prefix + "detection_predictions",
		prefix + "detections",
		prefix + "audio_sources",
		prefix + "labels",
		prefix + "ai_models",
		prefix + "taxonomic_classes",
		prefix + "label_types",
		prefix + "migration_state",
		prefix + "migration_dirty_ids",
		// Auxiliary tables
		prefix + "hourly_weathers",
		prefix + "daily_events",
		prefix + "image_caches",
		prefix + "threshold_events",
		prefix + "dynamic_thresholds",
		prefix + "notification_histories",
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
	// Check if the migration_state table exists as an indicator.
	// Must account for the v2_ prefix when in migration mode.
	tableName := m.tablePrefix + "migration_state"
	return m.db.Migrator().HasTable(tableName)
}

// IsMySQL returns true for MySQL manager.
func (m *MySQLManager) IsMySQL() bool {
	return true
}

// CheckpointWAL is a no-op for MySQL as it doesn't use Write-Ahead Logging.
// This method exists to satisfy the Manager interface.
func (m *MySQLManager) CheckpointWAL() error {
	return nil
}

// TablePrefix returns the table prefix for v2 tables.
// Returns "" for fresh installs, "v2_" for migration mode.
func (m *MySQLManager) TablePrefix() string {
	return m.tablePrefix
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
