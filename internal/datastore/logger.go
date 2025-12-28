// Package datastore provides logging infrastructure for database operations
package datastore

import (
	"context"
	"fmt"
	"regexp"
	"strings"
	"time"

	"github.com/tphakala/birdnet-go/internal/errors"
	"github.com/tphakala/birdnet-go/internal/logger"
	"gorm.io/gorm"
	gormlogger "gorm.io/gorm/logger"
)

// whitespaceRegex matches sequences of whitespace (spaces, tabs, newlines)
var whitespaceRegex = regexp.MustCompile(`\s+`)

// sanitizeSQL normalizes SQL strings for logging by collapsing
// multiple whitespace characters (tabs, newlines, spaces) into single spaces.
func sanitizeSQL(sql string) string {
	return strings.TrimSpace(whitespaceRegex.ReplaceAllString(sql, " "))
}

// GetLogger returns the datastore package logger scoped to the datastore module.
// The logger is fetched from the global logger each time to ensure it uses
// the current centralized logger (which may be set after package init).
func GetLogger() logger.Logger {
	return logger.Global().Module("datastore")
}

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
		GetLogger().Info(fmt.Sprintf(msg, data...))
	}
}

// Warn implements gormlogger.Interface
func (l *GormLogger) Warn(ctx context.Context, msg string, data ...any) {
	if l.LogLevel >= gormlogger.Warn {
		GetLogger().Warn(fmt.Sprintf(msg, data...))
	}
}

// Error implements gormlogger.Interface
func (l *GormLogger) Error(ctx context.Context, msg string, data ...any) {
	if l.LogLevel >= gormlogger.Error {
		GetLogger().Error("GORM error",
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
		// Sanitize SQL for logging (collapse whitespace)
		sanitized := sanitizeSQL(sql)

		// Log and create enhanced error
		enhancedErr := errors.New(err).
			Component("datastore").
			Category(errors.CategoryDatabase).
			Context("operation", "sql_query").
			Context("sql", sanitized).
			Context("duration_ms", elapsed.Milliseconds()).
			Context("original_error_type", fmt.Sprintf("%T", err)).
			Build()

		GetLogger().Error("Database query failed",
			logger.Error(enhancedErr),
			logger.String("sql", sanitized),
			logger.Duration("duration", elapsed),
			logger.Int64("rows_affected", rows))

		// Record error metric
		if l.metrics != nil {
			l.metrics.RecordDbOperation(operation, table, "error")
			l.metrics.RecordDbOperationError(operation, table, categorizeError(err))
		}

	case elapsed > l.SlowThreshold && l.SlowThreshold != 0:
		// Log slow query with warning (sanitize SQL for readability)
		GetLogger().Warn("Slow query detected",
			logger.String("sql", sanitizeSQL(sql)),
			logger.Duration("duration", elapsed),
			logger.Int64("rows_affected", rows),
			logger.Duration("threshold", l.SlowThreshold))

		// Record as successful but slow
		if l.metrics != nil {
			l.metrics.RecordDbOperation(operation, table, "success")
		}

	case l.LogLevel >= gormlogger.Info:
		// Log normal queries at debug level (sanitize SQL for readability)
		GetLogger().Debug("Query executed",
			logger.String("sql", sanitizeSQL(sql)),
			logger.Duration("duration", elapsed),
			logger.Int64("rows_affected", rows))

		// Record success metric
		if l.metrics != nil {
			l.metrics.RecordDbOperation(operation, table, "success")
		}
	}
}
