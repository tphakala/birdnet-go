package errors

import (
	"context"
	"database/sql"
	"database/sql/driver"
	"fmt"
	"net"
	"os"
	"syscall"
	"testing"
	"time"

	"github.com/go-sql-driver/mysql"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// refusedDialError builds the error the net package returns when a TCP
// connect is refused, without touching the network.
func refusedDialError(errno syscall.Errno) error {
	return &net.OpError{Op: "dial", Net: "tcp", Err: os.NewSyscallError("connect", errno)}
}

// resetConnectivityWindow clears the connectivity rate limit window now and
// when the test ends. Tests that use it must not run in parallel.
func resetConnectivityWindow(t *testing.T) {
	t.Helper()
	connectivityLimiter.last.Store(0)
	t.Cleanup(func() { connectivityLimiter.last.Store(0) })
}

// flatten returns an error with the message prefix + ": " + err, and no chain.
func flatten(prefix string, err error) error {
	return fmt.Errorf("%s: %s", prefix, err.Error())
}

// gormJoin builds the shape of gorm's AddError: the first error flattened to
// text, the second kept as the wrapped cause.
func gormJoin(first, second error) error {
	return fmt.Errorf("%s; %w", first.Error(), second)
}

func TestIsConnectivityError(t *testing.T) {
	t.Parallel()

	refused := refusedDialError(syscall.ECONNREFUSED)
	server1364 := &mysql.MySQLError{Number: 1364, SQLState: [5]byte{'H', 'Y', '0', '0', '0'}, Message: "Field 'label_id' doesn't have a default value"}
	dialAddrErr := &net.OpError{Op: "dial", Net: "tcp", Err: &net.AddrError{Err: "too many colons in address", Addr: "::1:3306"}}

	tests := []struct {
		name string
		err  error
		want bool
	}{
		{"connection refused", refused, true},
		{"host unreachable", refusedDialError(syscall.EHOSTUNREACH), true},
		{"dial timeout that is also a context deadline", &net.OpError{Op: "dial", Net: "tcp", Err: context.DeadlineExceeded}, true},
		{"dial timeout", &net.OpError{Op: "dial", Net: "tcp", Err: os.ErrDeadlineExceeded}, true},
		{"dns not found", &net.OpError{Op: "dial", Err: &net.DNSError{Err: "no such host", Name: "db", IsNotFound: true}}, true},
		{"bare dns timeout", &net.DNSError{Err: "i/o timeout", Name: "db", IsTimeout: true}, true},
		{"wrapped bad conn", fmt.Errorf("x: %w", driver.ErrBadConn), true},
		{"bare invalid connection", mysql.ErrInvalidConn, true},
		{"wrapped invalid connection", fmt.Errorf("query: %w", mysql.ErrInvalidConn), true},
		{"invalid connection in enhanced error", New(mysql.ErrInvalidConn).Build(), true},
		{"invalid connection in nested enhanced error", New(New(mysql.ErrInvalidConn).Build()).Build(), true},
		{"invalid connection in joined error", Join(NewStd("other"), fmt.Errorf("x: %w", mysql.ErrInvalidConn)), true},
		{"unix socket missing", &net.OpError{Op: "dial", Net: "unix", Err: os.NewSyscallError("connect", syscall.ENOENT)}, true},
		{"flattened refused", flatten("x", refused), true},
		{"flattened unix socket refused", NewStd("dial unix /run/mysqld.sock: connect: connection refused"), true},

		{"nil", nil, false},
		{"wrapper hiding a nil-cause enhanced error", hidingWrapperError{&EnhancedError{}}, false},
		{"enhanced error without cause", &EnhancedError{}, false},
		{"unknown database", &mysql.MySQLError{Number: 1049, Message: "Unknown database 'birdnet'"}, false},
		{"missing default", server1364, false},
		{"not null column", NewStd("Cannot add a NOT NULL column with default value NULL"), false},
		{"database closed", NewStd("sql: database is closed"), false},
		{"malformed address", dialAddrErr, false},
		{"fd exhaustion", &net.OpError{Op: "dial", Net: "tcp", Err: os.NewSyscallError("socket", syscall.EMFILE)}, false},
		{"listener error", &net.OpError{Op: "listen", Net: "tcp", Err: os.NewSyscallError("bind", syscall.EADDRINUSE)}, false},
		{"syscall error outside the dial error", Join(dialAddrErr, os.NewSyscallError("connect", syscall.EINVAL)), false},
		{"gorm join, defect first", gormJoin(server1364, mysql.ErrInvalidConn), false},
		{"gorm join, dial first", gormJoin(refused, &mysql.MySQLError{Number: 1054, Message: "Unknown column"}), false},
		{"words only", NewStd("invalid connection string"), false},
		{"sentinel text in a non-leaf", fmt.Errorf("%w; %s", sql.ErrTxDone, mysql.ErrInvalidConn.Error()), false},
		{"flattened missing port", NewStd("dial tcp: address h: missing port in address"), false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			assert.Equal(t, tt.want, IsConnectivityError(tt.err))
		})
	}
}

func TestShouldReportToSentry_RateLimitsConnectivity(t *testing.T) {
	resetConnectivityWindow(t)

	build := func(cat ErrorCategory, err error) *EnhancedError {
		return New(err).Component("test").Category(cat).Build()
	}
	refused := refusedDialError(syscall.ECONNREFUSED)

	// A bare context error is not a connectivity failure and stays dropped
	// without taking the window, as does a canceled dial.
	assert.False(t, shouldReportToSentry(build(CategoryDatabase, context.DeadlineExceeded)), "bare context deadline stays dropped")
	assert.False(t, shouldReportToSentry(build(CategoryDatabase, &net.OpError{Op: "dial", Net: "tcp", Err: context.Canceled})), "canceled dial stays dropped")
	lowPriority := New(&net.OpError{Op: "dial", Net: "tcp", Err: context.DeadlineExceeded}).
		Component("test").Category(CategorySystem).Priority(PriorityLow).Build()
	assert.False(t, shouldReportToSentry(lowPriority), "low priority system errors stay dropped")

	// A real dial timeout is also a context deadline error. It is a connectivity
	// failure, so it reaches the window instead of being dropped as a context
	// interruption: the first one is sent, the next ones are rate limited.
	dialDeadline := &net.OpError{Op: "dial", Net: "tcp", Err: context.DeadlineExceeded}
	require.True(t, isContextCancellation(dialDeadline), "the real dial timeout is also a context deadline")
	assert.True(t, shouldReportToSentry(build(CategoryDatabase, dialDeadline)), "first dial timeout is sent")
	assert.False(t, shouldReportToSentry(build(CategorySystem, dialDeadline)), "second dial timeout is rate limited")
	connectivityLimiter.last.Store(0)

	assert.True(t, shouldReportToSentry(build(CategoryDatabase, refused)), "first connectivity event is sent")
	assert.False(t, shouldReportToSentry(build(CategoryState, mysql.ErrInvalidConn)), "window is shared across categories and classes")
	assert.False(t, shouldReportToSentry(build(CategorySystem, build(CategoryDatabase, refused))), "wrapped dial error is rate limited")
	assert.True(t, shouldReportToSentry(build(CategoryDatabase, &mysql.MySQLError{Number: 1364, Message: "Field 'x' doesn't have a default value"})), "defects are never rate limited")
	assert.True(t, shouldReportToSentry(build(CategoryIntegration, refused)), "other categories keep today's behaviour")

	connectivityLimiter.last.Store(connectivityLimiter.last.Load() - int64(connectivityReportCooldown/time.Second) - 1)
	assert.True(t, shouldReportToSentry(build(CategoryImageCache, &net.DNSError{Err: "no such host", Name: "x", IsNotFound: true})), "window expires")
}

// hidingWrapperError is an error whose message omits its wrapped child.
type hidingWrapperError struct{ child error }

func (hidingWrapperError) Error() string   { return "wrapper" }
func (w hidingWrapperError) Unwrap() error { return w.child }
