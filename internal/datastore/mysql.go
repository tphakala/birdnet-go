package datastore

import (
	"context"
	"fmt"
	"regexp"
	"strings"
	"time"

	"github.com/tphakala/birdnet-go/internal/conf"
	"github.com/tphakala/birdnet-go/internal/errors"
	"github.com/tphakala/birdnet-go/internal/logger"

	"gorm.io/driver/mysql"
	"gorm.io/gorm"
	gormlogger "gorm.io/gorm/logger"
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
	GetLogger().Info("Opening MySQL database connection",
		logger.String("dsn", sanitizedDSN))

	// Configure GORM logger with metrics if available
	var gormLogger gormlogger.Interface
	if store.Settings.Debug {
		// Use debug log level with lower slow threshold
		gormLogger = NewGormLogger(500*time.Millisecond, gormlogger.Info, store.metrics)
	} else {
		// Use default settings with metrics
		gormLogger = NewGormLogger(500*time.Millisecond, gormlogger.Warn, store.metrics)
	}

	// Open the MySQL database
	db, err := gorm.Open(mysql.Open(dsn), &gorm.Config{Logger: gormLogger})
	if err != nil {
		GetLogger().Error("Failed to open MySQL database", logger.Error(err))
		return fmt.Errorf("failed to open MySQL database: %w", err)
	}

	store.DB = db

	// Log successful connection
	GetLogger().Info("MySQL database opened successfully",
		logger.String("host", store.Settings.Output.MySQL.Host),
		logger.String("port", store.Settings.Output.MySQL.Port),
		logger.String("database", store.Settings.Output.MySQL.Database))

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
	GetLogger().Info("Closing MySQL database connection",
		logger.String("host", store.Settings.Output.MySQL.Host),
		logger.String("database", store.Settings.Output.MySQL.Database))

	// Retrieve the generic database object from the GORM DB object
	sqlDB, err := store.DB.DB()
	if err != nil {
		GetLogger().Error("Failed to retrieve generic DB object",
			logger.Error(err))
		return err
	}

	// Close the generic database object, which closes the underlying SQL database connection
	if err := sqlDB.Close(); err != nil {
		GetLogger().Error("Failed to close MySQL database",
			logger.String("host", store.Settings.Output.MySQL.Host),
			logger.String("database", store.Settings.Output.MySQL.Database),
			logger.Error(err))
		return err
	}

	// Log successful closure
	GetLogger().Info("MySQL database closed successfully",
		logger.String("host", store.Settings.Output.MySQL.Host),
		logger.String("database", store.Settings.Output.MySQL.Database))

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
	optimizeLogger := GetLogger().With(logger.String("operation", "optimize"), logger.String("db_type", "MySQL"))

	optimizeLogger.Info("Starting database optimization")

	// Get list of tables in the database
	var tables []string
	if err := store.DB.Raw("SHOW TABLES").Pluck("Tables_in_"+store.Settings.Output.MySQL.Database, &tables).Error; err != nil {
		enhancedErr := errors.New(err).
			Component("datastore").
			Category(errors.CategoryDatabase).
			Context("operation", "show_tables").
			Build()
		optimizeLogger.Error("Failed to get table list", logger.Error(enhancedErr))
		return enhancedErr
	}

	optimizeLogger.Info("Found tables to optimize", logger.Int("table_count", len(tables)))

	// Optimize each table
	optimizedCount := 0
	for _, table := range tables {
		// Validate table name to prevent SQL injection
		if !isValidTableName(table) {
			optimizeLogger.Error("Invalid table name detected, skipping",
				logger.String("table", table),
				logger.String("reason", "contains unsafe characters or exceeds length limit"))
			continue
		}

		tableStart := time.Now()
		optimizeLogger.Debug("Optimizing table", logger.String("table", table))

		// Run OPTIMIZE TABLE
		if err := store.DB.Exec(fmt.Sprintf("OPTIMIZE TABLE `%s`", table)).Error; err != nil {
			// MySQL may return a note/warning for InnoDB tables, which is not an error
			if !strings.Contains(err.Error(), "Table does not support optimize") {
				optimizeLogger.Warn("Failed to optimize table",
					logger.String("table", table),
					logger.Error(err),
					logger.String("note", "This is often normal for InnoDB tables"))
			}
		} else {
			optimizedCount++
		}

		// Run ANALYZE TABLE to update statistics
		if err := store.DB.Exec(fmt.Sprintf("ANALYZE TABLE `%s`", table)).Error; err != nil {
			optimizeLogger.Warn("Failed to analyze table",
				logger.String("table", table),
				logger.Error(err))
		} else {
			optimizeLogger.Info("Table optimization completed",
				logger.String("table", table),
				logger.Duration("duration", time.Since(tableStart)))
		}
	}

	optimizeLogger.Info("Database optimization completed",
		logger.Duration("total_duration", time.Since(optimizeStart)),
		logger.Int("tables_processed", len(tables)),
		logger.Int("tables_optimized", optimizedCount))

	return nil
}

// UpdateNote updates specific fields of a note in MySQL
func (m *MySQLStore) UpdateNote(id string, updates map[string]any) error {
	return m.DB.Model(&Note{}).Where("id = ?", id).Updates(updates).Error
}

// GetDatabaseStats returns basic runtime statistics about the MySQL database.
// Returns partial stats with ErrDBNotConnected if the database is unreachable.
// The Connected field in the returned stats indicates if the DB is reachable.
func (m *MySQLStore) GetDatabaseStats() (*DatabaseStats, error) {
	// Defensive guard for nil Settings (e.g., in custom test setups)
	location := ""
	if m.Settings != nil {
		location = fmt.Sprintf("%s:%s/%s",
			m.Settings.Output.MySQL.Host,
			m.Settings.Output.MySQL.Port,
			m.Settings.Output.MySQL.Database)
	}

	stats := &DatabaseStats{
		Type:      DialectMySQL,
		Connected: false,
		Location:  location,
	}

	// Check connection - return partial stats with error if unavailable
	if m.DB == nil {
		return stats, ErrDBNotConnected
	}

	sqlDB, err := m.DB.DB()
	if err != nil {
		return stats, ErrDBNotConnected
	}

	if err := sqlDB.Ping(); err != nil {
		return stats, ErrDBNotConnected
	}
	stats.Connected = true

	// Get database size (ignore errors, size stays 0)
	if size, sizeErr := m.getDatabaseSize(); sizeErr == nil {
		stats.SizeBytes = size
	}

	// Get total detections (ignore errors, count stays 0)
	if count, countErr := m.getTableRowCount("notes"); countErr == nil {
		stats.TotalDetections = count
	}

	return stats, nil
}
