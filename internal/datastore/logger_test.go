package datastore

import (
	"context"
	"fmt"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/tphakala/birdnet-go/internal/logger/logtest"
	gormlogger "gorm.io/gorm/logger"
)

// TestGormLogger_Trace_ContextCancellation verifies that context cancellation
// and deadline-exceeded query errors are logged at debug level ("Query canceled
// or timed out") and never as an error ("Database query failed"), so normal
// client disconnects, graceful shutdowns, and request timeouts do not produce
// misleading datastore error logs. The user-facing false-positive notification
// these errors used to raise is suppressed separately in the notification
// worker; this test only covers the log path.
func TestGormLogger_Trace_ContextCancellation(t *testing.T) {
	tests := []struct {
		name string
		err  error
	}{
		{name: "context_canceled", err: context.Canceled},
		{name: "deadline_exceeded", err: context.DeadlineExceeded},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			// Not parallel: logtest.CaptureBuffer swaps the process-global logger.
			buf := logtest.CaptureBuffer(t)

			gLogger := NewGormLogger(200*time.Millisecond, gormlogger.Info, nil, "sqlite")

			// Trace selects its branch from the err argument, not the context.
			fc := func() (sql string, rowsAffected int64) {
				return "SELECT * FROM test", 0
			}

			gLogger.Trace(t.Context(), time.Now(), fc, tt.err)

			output := buf.String()
			assert.NotContains(t, output, "level=ERROR", "%s must not be logged at error level", tt.name)
			assert.NotContains(t, output, "Database query failed", "%s must not be reported as a query failure", tt.name)
			assert.Contains(t, output, "level=DEBUG", "%s must be logged at debug level", tt.name)
			assert.Contains(t, output, "Query canceled or timed out", "%s should be logged as a debug message", tt.name)
		})
	}
}

// TestGormLogger_Trace_ErrorContext verifies that a genuine (non-cancellation,
// non-record-not-found) query failure is logged as "Database query failed" with
// the dialect and the parsed SQL operation/table attached, so a dialect-specific
// fault (GitHub #3833) is attributable to its engine and query from the logs or
// a support dump. Also verifies the dialect field is omitted for a
// dialect-agnostic logger.
func TestGormLogger_Trace_ErrorContext(t *testing.T) {
	tests := []struct {
		name          string
		dialect       string
		wantDialect   bool
		dialectSubstr string
	}{
		{name: "mysql dialect is logged", dialect: "mysql", wantDialect: true, dialectSubstr: "dialect=mysql"},
		{name: "empty dialect is omitted", dialect: "", wantDialect: false, dialectSubstr: "dialect="},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			// Not parallel: logtest.Capture swaps the process-global logger.
			gLogger := NewGormLogger(200*time.Millisecond, gormlogger.Info, nil, tt.dialect)

			fc := func() (sql string, rowsAffected int64) {
				return "SELECT * FROM notes WHERE id = ?", 0
			}
			// A plain syntax-style error, not a cancellation or ErrRecordNotFound,
			// takes the "Database query failed" branch.
			queryErr := fmt.Errorf("Error 1064 (42000): syntax error near ')'")
			out := logtest.Capture(t, func() {
				gLogger.Trace(t.Context(), time.Now(), fc, queryErr)
			})
			assert.Contains(t, out, "Database query failed", "a real query fault must log at error level")
			assert.Contains(t, out, "sql_operation=select", "the parsed SQL operation must be attached")
			assert.Contains(t, out, "sql_table=notes", "the parsed SQL table must be attached")
			if tt.wantDialect {
				assert.Contains(t, out, tt.dialectSubstr, "the dialect must be logged when known")
			} else {
				assert.NotContains(t, out, tt.dialectSubstr, "the dialect field must be omitted when unknown")
			}
		})
	}
}
