// Package datastore provides logging infrastructure for database operations
package datastore

import (
	"context"
	"fmt"
	"log/slog"
	"sync"
	"time"

	"github.com/tphakala/birdnet-go/internal/errors"
	"github.com/tphakala/birdnet-go/internal/logging"
	"gorm.io/gorm"
	"gorm.io/gorm/logger"
)

// Package-level logger for datastore operations
var (
	datastoreLogger   *slog.Logger
	datastoreLevelVar = new(slog.LevelVar) // Dynamic level control
	loggerCloseFunc   func() error         // Function to close the logger
	loggerOnce        sync.Once            // Ensures logger is initialized only once
	loggerMu          sync.RWMutex         // Protects logger access
	
	// defaultLogPath follows the project-wide convention of using a "logs/" directory
	// for all log files. This is consistent across all components (API, weather,
	// imageprovider, etc.) and centralizes logs in a single location for easier
	// management, rotation, and debugging. The directory is created automatically
	// if it doesn't exist when the logger is initialized.
	defaultLogPath    = "logs/datastore.log"
)

// InitializeLogger initializes the datastore logger with the specified log file path
// This function is safe to call multiple times - initialization happens only once
func InitializeLogger(logFilePath string) error {
	var initErr error
	
	loggerOnce.Do(func() {
		if logFilePath == "" {
			logFilePath = defaultLogPath
		}
		
		// Set initial log level
		initialLevel := slog.LevelInfo
		datastoreLevelVar.Set(initialLevel)
		
		// Attempt to create file logger
		var err error
		datastoreLogger, loggerCloseFunc, err = logging.NewFileLogger(logFilePath, "datastore", datastoreLevelVar)
		if err != nil {
			// Create fallback no-op logger instead of failing
			datastoreLogger = slog.New(slog.NewTextHandler(nil, nil))
			loggerCloseFunc = func() error { return nil }
			
			// Return the error but don't fail completely
			initErr = errors.Newf("datastore: failed to initialize file logger: %v", err).
				Component("datastore").
				Category(errors.CategoryFileIO).
				Context("log_file", logFilePath).
				Context("operation", "logger_initialization").
				Build()
		}
	})
	
	return initErr
}

// getLogger returns the logger, initializing it with default path if needed
func getLogger() *slog.Logger {
	loggerMu.RLock()
	if datastoreLogger != nil {
		defer loggerMu.RUnlock()
		return datastoreLogger
	}
	loggerMu.RUnlock()
	
	// Initialize with default path if not already initialized
	_ = InitializeLogger(defaultLogPath)
	
	loggerMu.RLock()
	defer loggerMu.RUnlock()
	return datastoreLogger
}

// CloseLogger closes the datastore logger
func CloseLogger() error {
	if loggerCloseFunc != nil {
		return loggerCloseFunc()
	}
	return nil
}

// SetLogLevel sets the log level for the datastore logger
func SetLogLevel(level slog.Level) {
	datastoreLevelVar.Set(level)
}

// GormLogger implements GORM's logger interface with structured logging and metrics
type GormLogger struct {
	SlowThreshold time.Duration
	LogLevel      logger.LogLevel
	metrics       *Metrics
}

// NewGormLogger creates a new GORM logger instance
func NewGormLogger(slowThreshold time.Duration, logLevel logger.LogLevel, metrics *Metrics) *GormLogger {
	return &GormLogger{
		SlowThreshold: slowThreshold,
		LogLevel:      logLevel,
		metrics:       metrics,
	}
}

// LogMode implements logger.Interface
func (l *GormLogger) LogMode(level logger.LogLevel) logger.Interface {
	newLogger := *l
	newLogger.LogLevel = level
	return &newLogger
}

// Info implements logger.Interface
func (l *GormLogger) Info(ctx context.Context, msg string, data ...interface{}) {
	if l.LogLevel >= logger.Info {
		getLogger().InfoContext(ctx, fmt.Sprintf(msg, data...))
	}
}

// Warn implements logger.Interface
func (l *GormLogger) Warn(ctx context.Context, msg string, data ...interface{}) {
	if l.LogLevel >= logger.Warn {
		getLogger().WarnContext(ctx, fmt.Sprintf(msg, data...))
	}
}

// Error implements logger.Interface
func (l *GormLogger) Error(ctx context.Context, msg string, data ...interface{}) {
	if l.LogLevel >= logger.Error {
		getLogger().ErrorContext(ctx, "GORM error", 
			"msg", fmt.Sprintf(msg, data...))
		
		// Record error metric if available
		if l.metrics != nil {
			l.metrics.RecordDbOperationError("gorm_internal", "unknown", "gorm_error")
		}
	}
}

// Trace implements logger.Interface
func (l *GormLogger) Trace(ctx context.Context, begin time.Time, fc func() (sql string, rowsAffected int64), err error) {
	if l.LogLevel <= logger.Silent {
		return
	}

	elapsed := time.Since(begin)
	sql, rows := fc()
	
	// Extract operation and table from SQL
	operation, table := parseSQLOperation(sql)
	
	// Record metrics if available
	if l.metrics != nil {
		l.metrics.RecordDbOperationDuration(operation, table, elapsed.Seconds())
		l.metrics.RecordQueryResultSize(operation, table, int(rows))
	}
	
	switch {
	case err != nil && !errors.Is(err, gorm.ErrRecordNotFound):
		// Log and create enhanced error
		enhancedErr := errors.New(err).
			Component("datastore").
			Category(errors.CategoryDatabase).
			Context("operation", "sql_query").
			Context("sql", sql).
			Context("duration_ms", elapsed.Milliseconds()).
			Context("original_error_type", fmt.Sprintf("%T", err)).
			Build()
		
		getLogger().ErrorContext(ctx, "Database query failed",
			"error", enhancedErr,
			"sql", sql,
			"duration", elapsed,
			"rows_affected", rows)
		
		// Record error metric
		if l.metrics != nil {
			l.metrics.RecordDbOperation(operation, table, "error")
			l.metrics.RecordDbOperationError(operation, table, categorizeError(err))
		}
			
	case elapsed > l.SlowThreshold && l.SlowThreshold != 0:
		// Log slow query with warning
		getLogger().WarnContext(ctx, "Slow query detected",
			"sql", sql,
			"duration", elapsed,
			"rows_affected", rows,
			"threshold", l.SlowThreshold)
		
		// Record as successful but slow
		if l.metrics != nil {
			l.metrics.RecordDbOperation(operation, table, "success")
		}
			
	case l.LogLevel >= logger.Info:
		// Log normal queries at debug level
		getLogger().DebugContext(ctx, "Query executed",
			"sql", sql,
			"duration", elapsed,
			"rows_affected", rows)
		
		// Record success metric
		if l.metrics != nil {
			l.metrics.RecordDbOperation(operation, table, "success")
		}
	}
}