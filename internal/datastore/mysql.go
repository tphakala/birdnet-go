package datastore

import (
	"context"
	"fmt"
	"log"
	"log/slog"
	"regexp"
	"strings"
	"time"

	"github.com/tphakala/birdnet-go/internal/conf"
	"github.com/tphakala/birdnet-go/internal/errors"

	"gorm.io/driver/mysql"
	"gorm.io/gorm"
	"gorm.io/gorm/logger"
)

// validTableNameRegex matches valid table names: alphanumeric, underscores, and dashes only
var validTableNameRegex = regexp.MustCompile(`^[a-zA-Z0-9_-]+$`)

// isValidTableName validates that a table name contains only safe characters
func isValidTableName(tableName string) bool {
	if tableName == "" || len(tableName) > 64 { // MySQL max table name length
		return false
	}
	return validTableNameRegex.MatchString(tableName)
}

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

	// Log database opening (with sanitized DSN)
	sanitizedDSN := fmt.Sprintf("%s:***@tcp(%s:%s)/%s?charset=utf8mb4&parseTime=True&loc=Local",
		store.Settings.Output.MySQL.Username,
		store.Settings.Output.MySQL.Host, store.Settings.Output.MySQL.Port,
		store.Settings.Output.MySQL.Database)
	getLogger().Info("Opening MySQL database connection",
		"dsn", sanitizedDSN)

	// Configure GORM logger with metrics if available
	var gormLogger logger.Interface
	if store.Settings.Debug {
		// Use debug log level with lower slow threshold
		gormLogger = NewGormLogger(100*time.Millisecond, logger.Info, store.metrics)
		datastoreLevelVar.Set(slog.LevelDebug)
	} else {
		// Use default settings with metrics
		gormLogger = NewGormLogger(200*time.Millisecond, logger.Warn, store.metrics)
	}

	// Open the MySQL database
	db, err := gorm.Open(mysql.Open(dsn), &gorm.Config{Logger: gormLogger})
	if err != nil {
		log.Printf("Failed to open MySQL database: %v\n", err)
		return fmt.Errorf("failed to open MySQL database: %w", err)
	}

	store.DB = db

	// Log successful connection
	getLogger().Info("MySQL database opened successfully",
		"host", store.Settings.Output.MySQL.Host,
		"port", store.Settings.Output.MySQL.Port,
		"database", store.Settings.Output.MySQL.Database)

	if err := performAutoMigration(db, store.Settings.Debug, "MySQL", dsn); err != nil {
		return err
	}

	// Start monitoring if metrics are available
	if store.metrics != nil {
		// Monitoring intervals:
		// - 30s for connection pool: Provides timely visibility into connection usage patterns
		//   and potential exhaustion without overwhelming the metrics system
		// - 5m for database stats: Table sizes and row counts change less frequently,
		//   so a longer interval reduces overhead while still capturing growth trends
		store.StartMonitoring(30*time.Second, 5*time.Minute)
	}

	return nil
}

// Close MySQL database connections
func (store *MySQLStore) Close() error {
	// Ensure that the store's DB field is not nil to avoid a panic
	if store.DB == nil {
		return errors.Newf("database connection is not initialized").
			Component("datastore").
			Category(errors.CategoryValidation).
			Context("operation", "close").
			Build()
	}

	// Stop monitoring before closing database
	store.StopMonitoring()

	// Log database closing
	getLogger().Info("Closing MySQL database connection",
		"host", store.Settings.Output.MySQL.Host,
		"database", store.Settings.Output.MySQL.Database)

	// Retrieve the generic database object from the GORM DB object
	sqlDB, err := store.DB.DB()
	if err != nil {
		getLogger().Error("Failed to retrieve generic DB object",
			"error", err)
		return err
	}

	// Close the generic database object, which closes the underlying SQL database connection
	if err := sqlDB.Close(); err != nil {
		getLogger().Error("Failed to close MySQL database",
			"host", store.Settings.Output.MySQL.Host,
			"database", store.Settings.Output.MySQL.Database,
			"error", err)
		return err
	}

	// Log successful closure
	getLogger().Info("MySQL database closed successfully",
		"host", store.Settings.Output.MySQL.Host,
		"database", store.Settings.Output.MySQL.Database)

	return nil
}

// Optimize performs database optimization operations for MySQL
func (store *MySQLStore) Optimize(ctx context.Context) error {
	if store.DB == nil {
		return errors.Newf("database connection is not initialized").
			Component("datastore").
			Category(errors.CategoryValidation).
			Context("operation", "optimize").
			Build()
	}

	optimizeStart := time.Now()
	optimizeLogger := getLogger().With("operation", "optimize", "db_type", "MySQL")

	optimizeLogger.Info("Starting database optimization")

	// Get list of tables in the database
	var tables []string
	if err := store.DB.Raw("SHOW TABLES").Pluck("Tables_in_"+store.Settings.Output.MySQL.Database, &tables).Error; err != nil {
		enhancedErr := errors.New(err).
			Component("datastore").
			Category(errors.CategoryDatabase).
			Context("operation", "show_tables").
			Build()
		optimizeLogger.Error("Failed to get table list", "error", enhancedErr)
		return enhancedErr
	}

	optimizeLogger.Info("Found tables to optimize", "table_count", len(tables))

	// Optimize each table
	optimizedCount := 0
	for _, table := range tables {
		// Validate table name to prevent SQL injection
		if !isValidTableName(table) {
			optimizeLogger.Error("Invalid table name detected, skipping",
				"table", table,
				"reason", "contains unsafe characters or exceeds length limit")
			continue
		}

		tableStart := time.Now()
		optimizeLogger.Debug("Optimizing table", "table", table)

		// Run OPTIMIZE TABLE
		if err := store.DB.Exec(fmt.Sprintf("OPTIMIZE TABLE `%s`", table)).Error; err != nil {
			// MySQL may return a note/warning for InnoDB tables, which is not an error
			if !strings.Contains(err.Error(), "Table does not support optimize") {
				optimizeLogger.Warn("Failed to optimize table",
					"table", table,
					"error", err,
					"note", "This is often normal for InnoDB tables")
			}
		} else {
			optimizedCount++
		}

		// Run ANALYZE TABLE to update statistics
		if err := store.DB.Exec(fmt.Sprintf("ANALYZE TABLE `%s`", table)).Error; err != nil {
			optimizeLogger.Warn("Failed to analyze table",
				"table", table,
				"error", err)
		} else {
			optimizeLogger.Info("Table optimization completed",
				"table", table,
				"duration", time.Since(tableStart))
		}
	}

	optimizeLogger.Info("Database optimization completed",
		"total_duration", time.Since(optimizeStart),
		"tables_processed", len(tables),
		"tables_optimized", optimizedCount)

	return nil
}

// UpdateNote updates specific fields of a note in MySQL
func (m *MySQLStore) UpdateNote(id string, updates map[string]any) error {
	return m.DB.Model(&Note{}).Where("id = ?", id).Updates(updates).Error
}

// Save stores a note and its associated results as a single transaction in the database.
