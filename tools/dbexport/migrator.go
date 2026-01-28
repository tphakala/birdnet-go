package main

import (
	"fmt"
	"time"

	"github.com/tphakala/birdnet-go/internal/datastore"
	"gorm.io/driver/mysql"
	"gorm.io/driver/sqlite"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"
	"gorm.io/gorm/logger"
)

// Migrator handles the migration of data from SQLite to MySQL.
type Migrator struct {
	cfg      Config
	sourceDB *gorm.DB
	targetDB *gorm.DB
}

// MigrationStats tracks migration statistics.
type MigrationStats struct {
	StartTime time.Time
	EndTime   time.Time
	Tables    []TableStats
}

// TableStats tracks per-table migration statistics.
type TableStats struct {
	Name      string
	Migrated  int64
	Skipped   int64
	Errors    int64
	Duration  time.Duration
	BatchSize int
}

// Print outputs the migration statistics.
func (s *MigrationStats) Print() {
	fmt.Println("\n=== Migration Summary ===")
	fmt.Printf("Duration: %s\n\n", s.EndTime.Sub(s.StartTime).Round(time.Millisecond))

	fmt.Printf("%-25s %10s %10s %10s %12s\n", "Table", "Migrated", "Skipped", "Errors", "Duration")
	fmt.Println(string(make([]byte, 70)))

	var totalMigrated, totalSkipped, totalErrors int64
	for _, t := range s.Tables {
		fmt.Printf("%-25s %10d %10d %10d %12s\n",
			t.Name, t.Migrated, t.Skipped, t.Errors, t.Duration.Round(time.Millisecond))
		totalMigrated += t.Migrated
		totalSkipped += t.Skipped
		totalErrors += t.Errors
	}

	fmt.Println(string(make([]byte, 70)))
	fmt.Printf("%-25s %10d %10d %10d\n", "TOTAL", totalMigrated, totalSkipped, totalErrors)
}

// NewMigrator creates a new Migrator with database connections.
func NewMigrator(cfg *Config) (*Migrator, error) {
	m := &Migrator{cfg: *cfg}

	// Configure GORM logger
	logLevel := logger.Silent
	if cfg.Verbose {
		logLevel = logger.Info
	}
	gormConfig := &gorm.Config{
		Logger: logger.Default.LogMode(logLevel),
	}

	// Open source SQLite database
	sourceDB, err := gorm.Open(sqlite.Open(cfg.SQLitePath), gormConfig)
	if err != nil {
		return nil, fmt.Errorf("failed to open SQLite database: %w", err)
	}
	m.sourceDB = sourceDB

	// Open target MySQL database
	targetDB, err := gorm.Open(mysql.Open(cfg.GetMySQLDSN()), gormConfig)
	if err != nil {
		return nil, fmt.Errorf("failed to open MySQL database: %w", err)
	}
	m.targetDB = targetDB

	// Test connections
	sqlDB, err := sourceDB.DB()
	if err != nil {
		return nil, fmt.Errorf("failed to get SQLite connection: %w", err)
	}
	if err := sqlDB.Ping(); err != nil {
		return nil, fmt.Errorf("failed to ping SQLite database: %w", err)
	}

	sqlDB, err = targetDB.DB()
	if err != nil {
		return nil, fmt.Errorf("failed to get MySQL connection: %w", err)
	}
	if err := sqlDB.Ping(); err != nil {
		return nil, fmt.Errorf("failed to ping MySQL database: %w", err)
	}

	fmt.Println("Database connections established successfully")

	return m, nil
}

// Close closes both database connections.
func (m *Migrator) Close() {
	if m.sourceDB != nil {
		if db, err := m.sourceDB.DB(); err == nil {
			_ = db.Close()
		}
	}
	if m.targetDB != nil {
		if db, err := m.targetDB.DB(); err == nil {
			_ = db.Close()
		}
	}
}

// Run executes the full migration.
func (m *Migrator) Run() (*MigrationStats, error) {
	stats := &MigrationStats{
		StartTime: time.Now(),
	}

	// Drop tables if requested (fresh start)
	if m.cfg.DropTables {
		if err := m.dropTables(); err != nil {
			return nil, fmt.Errorf("failed to drop tables: %w", err)
		}
	}

	// Auto-migrate tables if requested
	if m.cfg.AutoMigrate {
		if err := m.autoMigrateTables(); err != nil {
			return nil, fmt.Errorf("failed to auto-migrate tables: %w", err)
		}
	}

	// Disable foreign key checks on MySQL
	if err := m.targetDB.Exec("SET FOREIGN_KEY_CHECKS=0").Error; err != nil {
		return nil, fmt.Errorf("failed to disable foreign key checks: %w", err)
	}
	defer m.targetDB.Exec("SET FOREIGN_KEY_CHECKS=1")

	fmt.Println("Foreign key checks disabled")

	// Clean tables if requested
	if m.cfg.Clean {
		if err := m.cleanTables(); err != nil {
			return nil, fmt.Errorf("failed to clean tables: %w", err)
		}
	}

	// Migrate tables in dependency order
	tables := []struct {
		name      string
		batchSize int
		migrate   func(int) (*TableStats, error)
	}{
		{"daily_events", 5000, m.migrateDailyEvents},
		{"hourly_weathers", 5000, m.migrateHourlyWeathers},
		{"notes", 1000, m.migrateNotes},
		{"results", 2000, m.migrateResults},
		{"note_reviews", 2000, m.migrateNoteReviews},
		{"note_comments", 2000, m.migrateNoteComments},
		{"note_locks", 2000, m.migrateNoteLocks},
		{"image_caches", 2000, m.migrateImageCaches},
		{"dynamic_thresholds", 5000, m.migrateDynamicThresholds},
		{"threshold_events", 5000, m.migrateThresholdEvents},
		{"notification_histories", 5000, m.migrateNotificationHistories},
	}

	for _, t := range tables {
		batchSize := t.batchSize
		if m.cfg.BatchSize > 0 && m.cfg.BatchSize < t.batchSize {
			batchSize = m.cfg.BatchSize
		}

		tableStats, err := t.migrate(batchSize)
		if err != nil {
			return stats, fmt.Errorf("failed to migrate %s: %w", t.name, err)
		}
		stats.Tables = append(stats.Tables, *tableStats)
	}

	stats.EndTime = time.Now()

	// Note: FK checks re-enabled by defer on line 147

	return stats, nil
}

// dropTables drops all tables from the target database for a fresh start.
func (m *Migrator) dropTables() error {
	fmt.Println("Dropping all tables from target database...")

	// Disable FK checks first to allow dropping in any order
	if err := m.targetDB.Exec("SET FOREIGN_KEY_CHECKS=0").Error; err != nil {
		return fmt.Errorf("failed to disable foreign key checks: %w", err)
	}

	tables := []string{
		"notification_histories",
		"threshold_events",
		"dynamic_thresholds",
		"image_caches",
		"note_locks",
		"note_comments",
		"note_reviews",
		"results",
		"notes",
		"hourly_weathers",
		"daily_events",
	}

	for _, table := range tables {
		if err := m.targetDB.Exec(fmt.Sprintf("DROP TABLE IF EXISTS %s", table)).Error; err != nil {
			fmt.Printf("Warning: could not drop table %s: %v\n", table, err)
		} else if m.cfg.Verbose {
			fmt.Printf("  Dropped: %s\n", table)
		}
	}

	// Re-enable FK checks
	if err := m.targetDB.Exec("SET FOREIGN_KEY_CHECKS=1").Error; err != nil {
		return fmt.Errorf("failed to re-enable foreign key checks: %w", err)
	}

	fmt.Println("Tables dropped successfully")
	return nil
}

// autoMigrateTables creates all tables in the target database using GORM AutoMigrate.
func (m *Migrator) autoMigrateTables() error {
	fmt.Println("Creating tables in target database...")

	models := []any{
		&datastore.DailyEvents{},
		&datastore.HourlyWeather{},
		&datastore.Note{},
		&datastore.Results{},
		&datastore.NoteReview{},
		&datastore.NoteComment{},
		&datastore.NoteLock{},
		&datastore.ImageCache{},
		&datastore.DynamicThreshold{},
		&datastore.ThresholdEvent{},
		&datastore.NotificationHistory{},
	}

	for _, model := range models {
		if err := m.targetDB.AutoMigrate(model); err != nil {
			return fmt.Errorf("failed to migrate %T: %w", model, err)
		}
	}

	fmt.Println("Tables created successfully")
	return nil
}

// cleanTables truncates all target tables.
func (m *Migrator) cleanTables() error {
	tables := []string{
		"notification_histories",
		"threshold_events",
		"dynamic_thresholds",
		"image_caches",
		"note_locks",
		"note_comments",
		"note_reviews",
		"results",
		"notes",
		"hourly_weathers",
		"daily_events",
	}

	fmt.Println("Cleaning target tables...")
	for _, table := range tables {
		if err := m.targetDB.Exec(fmt.Sprintf("TRUNCATE TABLE %s", table)).Error; err != nil {
			// Table might not exist, try DELETE instead
			if err := m.targetDB.Exec(fmt.Sprintf("DELETE FROM %s", table)).Error; err != nil {
				fmt.Printf("Warning: could not clean table %s: %v\n", table, err)
			}
		}
		if m.cfg.Verbose {
			fmt.Printf("  Cleaned: %s\n", table)
		}
	}
	fmt.Println("Tables cleaned")

	return nil
}

// migrateTable is a generic function for migrating a table using batched operations.
func migrateTable[T any](m *Migrator, tableName string, batchSize int) (*TableStats, error) {
	start := time.Now()
	stats := &TableStats{
		Name:      tableName,
		BatchSize: batchSize,
	}

	fmt.Printf("Migrating %s...\n", tableName)

	// Count source records
	var sourceCount int64
	if err := m.sourceDB.Model(new(T)).Count(&sourceCount).Error; err != nil {
		return stats, fmt.Errorf("failed to count source records: %w", err)
	}

	if sourceCount == 0 {
		fmt.Printf("  %s: no records to migrate\n", tableName)
		stats.Duration = time.Since(start)
		return stats, nil
	}

	// Process in batches
	var processed int64
	batchNum := 0

	err := m.sourceDB.Model(new(T)).FindInBatches(new([]T), batchSize, func(tx *gorm.DB, batch int) error {
		batchNum++
		records := tx.Statement.Dest.(*[]T)

		// Insert with ON CONFLICT DO NOTHING for idempotency
		result := m.targetDB.Clauses(clause.OnConflict{DoNothing: true}).Create(records)
		if result.Error != nil {
			stats.Errors += int64(len(*records))
			fmt.Printf("  Batch %d error: %v\n", batchNum, result.Error)
			// Continue with next batch - don't fail entire migration on batch error
			return nil //nolint:nilerr // intentional: continue migration despite batch error
		}

		stats.Migrated += result.RowsAffected
		stats.Skipped += int64(len(*records)) - result.RowsAffected
		processed += int64(len(*records))

		if m.cfg.Verbose || batchNum%10 == 0 {
			fmt.Printf("  %s: %d/%d (%.1f%%)\n", tableName, processed, sourceCount,
				float64(processed)/float64(sourceCount)*100)
		}

		return nil
	}).Error

	if err != nil {
		return stats, err
	}

	stats.Duration = time.Since(start)
	fmt.Printf("  %s: completed (%d migrated, %d skipped, %d errors) in %s\n",
		tableName, stats.Migrated, stats.Skipped, stats.Errors, stats.Duration.Round(time.Millisecond))

	return stats, nil
}

// Table-specific migration functions

func (m *Migrator) migrateDailyEvents(batchSize int) (*TableStats, error) {
	return migrateTable[datastore.DailyEvents](m, "daily_events", batchSize)
}

func (m *Migrator) migrateHourlyWeathers(batchSize int) (*TableStats, error) {
	return migrateTable[datastore.HourlyWeather](m, "hourly_weathers", batchSize)
}

func (m *Migrator) migrateNotes(batchSize int) (*TableStats, error) {
	return migrateTable[datastore.Note](m, "notes", batchSize)
}

func (m *Migrator) migrateResults(batchSize int) (*TableStats, error) {
	return migrateTable[datastore.Results](m, "results", batchSize)
}

func (m *Migrator) migrateNoteReviews(batchSize int) (*TableStats, error) {
	return migrateTable[datastore.NoteReview](m, "note_reviews", batchSize)
}

func (m *Migrator) migrateNoteComments(batchSize int) (*TableStats, error) {
	return migrateTable[datastore.NoteComment](m, "note_comments", batchSize)
}

func (m *Migrator) migrateNoteLocks(batchSize int) (*TableStats, error) {
	return migrateTable[datastore.NoteLock](m, "note_locks", batchSize)
}

func (m *Migrator) migrateImageCaches(batchSize int) (*TableStats, error) {
	return migrateTable[datastore.ImageCache](m, "image_caches", batchSize)
}

func (m *Migrator) migrateDynamicThresholds(batchSize int) (*TableStats, error) {
	return migrateTable[datastore.DynamicThreshold](m, "dynamic_thresholds", batchSize)
}

func (m *Migrator) migrateThresholdEvents(batchSize int) (*TableStats, error) {
	return migrateTable[datastore.ThresholdEvent](m, "threshold_events", batchSize)
}

func (m *Migrator) migrateNotificationHistories(batchSize int) (*TableStats, error) {
	return migrateTable[datastore.NotificationHistory](m, "notification_histories", batchSize)
}
