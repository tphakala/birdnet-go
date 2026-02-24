package dbstats

import (
	"context"
	"strings"
	"time"

	"gorm.io/gorm"
)

// startTimeKey is the context key used to store the query start time.
type startTimeKey struct{}

// RegisterCallbacks registers Before/After GORM callbacks that time every
// database operation and record the duration in the provided Counters.
//
// All callback names use the "dbstats:" prefix to avoid conflicts with
// existing GORM plugins (e.g., gormlogger).
func RegisterCallbacks(db *gorm.DB, counters *Counters) {
	if db == nil || counters == nil {
		return
	}

	// beforeFn stores the current time in the statement context.
	beforeFn := func(db *gorm.DB) {
		db.Statement.Context = context.WithValue(db.Statement.Context, startTimeKey{}, time.Now())
	}

	// afterFn computes the duration and calls the provided recorder.
	afterFn := func(recorder func(int64)) func(*gorm.DB) {
		return func(db *gorm.DB) {
			start, ok := db.Statement.Context.Value(startTimeKey{}).(time.Time)
			if !ok {
				return
			}
			durationUs := time.Since(start).Microseconds()
			recorder(durationUs)

			// Track SQLITE_BUSY errors
			if db.Error != nil && isBusyError(db.Error) {
				counters.RecordBusyTimeout()
			}
		}
	}

	// Query (SELECT) → RecordRead
	_ = db.Callback().Query().Before("*").Register("dbstats:before_query", beforeFn)
	_ = db.Callback().Query().After("*").Register("dbstats:after_query", afterFn(counters.RecordRead))

	// Create (INSERT) → RecordWrite
	_ = db.Callback().Create().Before("*").Register("dbstats:before_create", beforeFn)
	_ = db.Callback().Create().After("*").Register("dbstats:after_create", afterFn(counters.RecordWrite))

	// Update → RecordWrite
	_ = db.Callback().Update().Before("*").Register("dbstats:before_update", beforeFn)
	_ = db.Callback().Update().After("*").Register("dbstats:after_update", afterFn(counters.RecordWrite))

	// Delete → RecordWrite
	_ = db.Callback().Delete().Before("*").Register("dbstats:before_delete", beforeFn)
	_ = db.Callback().Delete().After("*").Register("dbstats:after_delete", afterFn(counters.RecordWrite))

	// Raw (db.Exec) → RecordWrite
	_ = db.Callback().Raw().Before("*").Register("dbstats:before_raw", beforeFn)
	_ = db.Callback().Raw().After("*").Register("dbstats:after_raw", afterFn(counters.RecordWrite))

	// Row (db.Raw().Row()) → RecordRead
	_ = db.Callback().Row().Before("*").Register("dbstats:before_row", beforeFn)
	_ = db.Callback().Row().After("*").Register("dbstats:after_row", afterFn(counters.RecordRead))
}

// isBusyError checks if a GORM error is a SQLite SQLITE_BUSY error.
func isBusyError(err error) bool {
	if err == nil {
		return false
	}
	errStr := err.Error()
	return strings.Contains(errStr, "database is locked") || strings.Contains(errStr, "SQLITE_BUSY")
}
