// Package datastore provides logging infrastructure for database operations
package datastore

import (
	"context"
	"fmt"
	"log/slog"
	"time"

	"github.com/tphakala/birdnet-go/internal/errors"
	"github.com/tphakala/birdnet-go/internal/logging"
	"gorm.io/gorm/logger"
)

// Package-level logger for datastore operations
var (
	datastoreLogger   *slog.Logger
	datastoreLevelVar = new(slog.LevelVar) // Dynamic level control
	loggerCloseFunc   func() error         // Function to close the logger
)

func init() {
	initialLevel := slog.LevelInfo
	datastoreLevelVar.Set(initialLevel)

	var err error
	datastoreLogger, loggerCloseFunc, err = logging.NewFileLogger("logs/datastore.log", "datastore", datastoreLevelVar)
	if err != nil {
		// Fallback to disabled logger
		descriptiveErr := errors.Newf("datastore: failed to initialize file logger: %v", err).
			Component("datastore").
			Category(errors.CategoryFileIO).
			Context("log_file", "logs/datastore.log").
			Context("operation", "logger_initialization").
			Build()
		logging.Error("Failed to initialize datastore file logger", "error", descriptiveErr)
		
		// Create a no-op logger
		datastoreLogger = slog.New(slog.NewTextHandler(nil, nil))
		loggerCloseFunc = func() error { return nil }
	}
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
	metrics       *DatastoreMetrics
}

// NewGormLogger creates a new GORM logger instance
func NewGormLogger(slowThreshold time.Duration, logLevel logger.LogLevel, metrics *DatastoreMetrics) *GormLogger {
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
		datastoreLogger.InfoContext(ctx, fmt.Sprintf(msg, data...))
	}
}

// Warn implements logger.Interface
func (l *GormLogger) Warn(ctx context.Context, msg string, data ...interface{}) {
	if l.LogLevel >= logger.Warn {
		datastoreLogger.WarnContext(ctx, fmt.Sprintf(msg, data...))
	}
}

// Error implements logger.Interface
func (l *GormLogger) Error(ctx context.Context, msg string, data ...interface{}) {
	if l.LogLevel >= logger.Error {
		datastoreLogger.ErrorContext(ctx, "GORM error", 
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
	case err != nil && !errors.Is(err, logger.ErrRecordNotFound):
		// Log and create enhanced error
		enhancedErr := errors.New(err).
			Component("datastore").
			Category(errors.CategoryDatabase).
			Context("operation", "sql_query").
			Context("sql", sql).
			Context("duration_ms", elapsed.Milliseconds()).
			Build()
		
		datastoreLogger.ErrorContext(ctx, "Database query failed",
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
		datastoreLogger.WarnContext(ctx, "Slow query detected",
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
		datastoreLogger.DebugContext(ctx, "Query executed",
			"sql", sql,
			"duration", elapsed,
			"rows_affected", rows)
		
		// Record success metric
		if l.metrics != nil {
			l.metrics.RecordDbOperation(operation, table, "success")
		}
	}
}