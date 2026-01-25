package logger

import (
	"context"
	"errors"
	"fmt"
	"time"

	"gorm.io/gorm"
	gorm_logger "gorm.io/gorm/logger"
)

// GormLoggerAdapter adapts logger.Logger to GORM's logger.Interface.
// SQL queries are logged at TRACE level, so they only appear when
// the datastore module is set to "trace" level.
//
// Usage:
//
//	datastoreLogger := centralLogger.Module("datastore")
//	gormLogger := logger.NewGormLoggerAdapter(datastoreLogger, 200*time.Millisecond)
//	db, err := gorm.Open(sqlite.Open(dsn), &gorm.Config{
//	    Logger: gormLogger,
//	})
type GormLoggerAdapter struct {
	logger        Logger
	slowThreshold time.Duration
}

// NewGormLoggerAdapter creates a new GORM logger adapter.
// The slowThreshold parameter sets the duration after which queries are logged
// as slow (at WARN level). Use 0 to disable slow query warnings.
func NewGormLoggerAdapter(logger Logger, slowThreshold time.Duration) *GormLoggerAdapter {
	if logger == nil {
		logger = NewSlogLogger(nil, LogLevelInfo, nil)
	}
	return &GormLoggerAdapter{
		logger:        logger,
		slowThreshold: slowThreshold,
	}
}

// LogMode returns the adapter itself. Log level is managed by the project's
// central logger configuration, not by GORM's log level setting.
func (a *GormLoggerAdapter) LogMode(_ gorm_logger.LogLevel) gorm_logger.Interface {
	return a
}

// Info logs informational messages at DEBUG level.
// GORM's Info level is verbose, so we map it to DEBUG.
func (a *GormLoggerAdapter) Info(_ context.Context, msg string, data ...any) {
	a.logger.Debug(fmt.Sprintf(msg, data...))
}

// Warn logs warning messages at WARN level.
func (a *GormLoggerAdapter) Warn(_ context.Context, msg string, data ...any) {
	a.logger.Warn(fmt.Sprintf(msg, data...))
}

// Error logs error messages at ERROR level.
func (a *GormLoggerAdapter) Error(_ context.Context, msg string, data ...any) {
	a.logger.Error(fmt.Sprintf(msg, data...))
}

// Trace logs SQL queries and their execution details.
// Normal queries are logged at TRACE level.
// Slow queries (exceeding slowThreshold) are logged at WARN level.
// Query errors (except ErrRecordNotFound) are logged at WARN level.
func (a *GormLoggerAdapter) Trace(_ context.Context, begin time.Time, fc func() (sql string, rowsAffected int64), err error) {
	elapsed := time.Since(begin)
	sql, rows := fc()

	switch {
	case err != nil && !errors.Is(err, gorm.ErrRecordNotFound):
		// Query error - log at WARN level
		a.logger.Warn("query error",
			String("sql", sql),
			Int64("rows_affected", rows),
			Int64("duration_ms", elapsed.Milliseconds()),
			Error(err))

	case a.slowThreshold > 0 && elapsed > a.slowThreshold:
		// Slow query - log at WARN level
		a.logger.Warn("slow query",
			String("sql", sql),
			Int64("rows_affected", rows),
			Int64("duration_ms", elapsed.Milliseconds()),
			Duration("threshold", a.slowThreshold))

	default:
		// Normal query - log at TRACE level
		a.logger.Trace("sql query",
			String("sql", sql),
			Int64("rows_affected", rows),
			Int64("duration_ms", elapsed.Milliseconds()))
	}
}
