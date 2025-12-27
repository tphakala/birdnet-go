// Package datastore provides logging infrastructure for database operations
package datastore

import (
	"context"
	"fmt"
	"time"

	"github.com/tphakala/birdnet-go/internal/errors"
	"github.com/tphakala/birdnet-go/internal/logger"
	"gorm.io/gorm"
	gormlogger "gorm.io/gorm/logger"
)

// Package-level cached logger instance for efficiency
var log = logger.Global().Module("datastore")

// GormLogger implements GORM's logger interface with structured logging and metrics
type GormLogger struct {
	SlowThreshold time.Duration
	LogLevel      gormlogger.LogLevel
	metrics       *Metrics
}

// NewGormLogger creates a new GORM logger instance
func NewGormLogger(slowThreshold time.Duration, logLevel gormlogger.LogLevel, metrics *Metrics) *GormLogger {
	return &GormLogger{
		SlowThreshold: slowThreshold,
		LogLevel:      logLevel,
		metrics:       metrics,
	}
}

// LogMode implements gormlogger.Interface
func (l *GormLogger) LogMode(level gormlogger.LogLevel) gormlogger.Interface {
	newLogger := *l
	newLogger.LogLevel = level
	return &newLogger
}

// Info implements gormlogger.Interface
func (l *GormLogger) Info(ctx context.Context, msg string, data ...any) {
	if l.LogLevel >= gormlogger.Info {
		log.Info(fmt.Sprintf(msg, data...))
	}
}

// Warn implements gormlogger.Interface
func (l *GormLogger) Warn(ctx context.Context, msg string, data ...any) {
	if l.LogLevel >= gormlogger.Warn {
		log.Warn(fmt.Sprintf(msg, data...))
	}
}

// Error implements gormlogger.Interface
func (l *GormLogger) Error(ctx context.Context, msg string, data ...any) {
	if l.LogLevel >= gormlogger.Error {
		log.Error("GORM error",
			logger.String("msg", fmt.Sprintf(msg, data...)))

		// Record error metric if available
		if l.metrics != nil {
			l.metrics.RecordDbOperationError("gorm_internal", "unknown", "gorm_error")
		}
	}
}

// Trace implements gormlogger.Interface
func (l *GormLogger) Trace(ctx context.Context, begin time.Time, fc func() (sql string, rowsAffected int64), err error) {
	if l.LogLevel <= gormlogger.Silent {
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

		log.Error("Database query failed",
			logger.Error(enhancedErr),
			logger.String("sql", sql),
			logger.Duration("duration", elapsed),
			logger.Int64("rows_affected", rows))

		// Record error metric
		if l.metrics != nil {
			l.metrics.RecordDbOperation(operation, table, "error")
			l.metrics.RecordDbOperationError(operation, table, categorizeError(err))
		}

	case elapsed > l.SlowThreshold && l.SlowThreshold != 0:
		// Log slow query with warning
		log.Warn("Slow query detected",
			logger.String("sql", sql),
			logger.Duration("duration", elapsed),
			logger.Int64("rows_affected", rows),
			logger.Duration("threshold", l.SlowThreshold))

		// Record as successful but slow
		if l.metrics != nil {
			l.metrics.RecordDbOperation(operation, table, "success")
		}

	case l.LogLevel >= gormlogger.Info:
		// Log normal queries at debug level
		log.Debug("Query executed",
			logger.String("sql", sql),
			logger.Duration("duration", elapsed),
			logger.Int64("rows_affected", rows))

		// Record success metric
		if l.metrics != nil {
			l.metrics.RecordDbOperation(operation, table, "success")
		}
	}
}
