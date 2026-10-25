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

	"github.com/go-sql-driver/mysql"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// refusedDialError builds the error the net package returns when a TCP
// connect is refused, without touching the network.
func refusedDialError(errno syscall.Errno) error {
	return &net.OpError{Op: "dial", Net: "tcp", Err: os.NewSyscallError("connect", errno)}
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
		{"flattened refused", fmt.Errorf("x: %v", refused), true},
		{"flattened unix socket refused", NewStd("dial unix /run/mysqld.sock: connect: connection refused"), true},

		{"nil", nil, false},
		{"enhanced error without cause", &EnhancedError{}, false},
		{"unknown database", &mysql.MySQLError{Number: 1049, Message: "Unknown database 'birdnet'"}, false},
		{"missing default", server1364, false},
		{"not null column", NewStd("Cannot add a NOT NULL column with default value NULL"), false},
		{"database closed", NewStd("sql: database is closed"), false},
		{"malformed address", dialAddrErr, false},
		{"fd exhaustion", &net.OpError{Op: "dial", Net: "tcp", Err: os.NewSyscallError("socket", syscall.EMFILE)}, false},
		{"listener error", &net.OpError{Op: "listen", Net: "tcp", Err: os.NewSyscallError("bind", syscall.EADDRINUSE)}, false},
		{"syscall error outside the dial error", Join(dialAddrErr, os.NewSyscallError("connect", syscall.EINVAL)), false},
		{"gorm join, defect first", fmt.Errorf("%v; %w", server1364, mysql.ErrInvalidConn), false},
		{"gorm join, dial first", fmt.Errorf("%v; %w", refused, &mysql.MySQLError{Number: 1054, Message: "Unknown column"}), false},
		{"words only", NewStd("invalid connection string"), false},
		{"sentinel text in a non-leaf", fmt.Errorf("%w; %v", sql.ErrTxDone, mysql.ErrInvalidConn), false},
		{"flattened missing port", NewStd("dial tcp: address h: missing port in address"), false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			assert.Equal(t, tt.want, IsConnectivityError(tt.err))
		})
	}
}

// TestIsConnectivityError_RealDialTimeoutIsContextDeadline documents why the
// runtime filter checks context cancellation first: the dial timeout the net
// package returns is also a context deadline error.
func TestIsConnectivityError_RealDialTimeoutIsContextDeadline(t *testing.T) {
	t.Parallel()
	err := &net.OpError{Op: "dial", Net: "tcp", Err: context.DeadlineExceeded}
	require.True(t, IsConnectivityError(err))
	assert.True(t, isContextCancellation(err))
}

func TestShouldReportToSentry_RateLimitsConnectivity(t *testing.T) {
	lastConnectivityReport.Store(0)
	t.Cleanup(func() { lastConnectivityReport.Store(0) })

	build := func(cat ErrorCategory, err error) *EnhancedError {
		return New(err).Component("test").Category(cat).Build()
	}
	refused := refusedDialError(syscall.ECONNREFUSED)

	// A real dial timeout is also a context deadline error; the context filter
	// runs first, so it is dropped without taking the window.
	dialDeadline := &net.OpError{Op: "dial", Net: "tcp", Err: context.DeadlineExceeded}
	assert.False(t, shouldReportToSentry(build(CategoryDatabase, dialDeadline)), "context deadline stays dropped")

	assert.True(t, shouldReportToSentry(build(CategoryDatabase, refused)), "first connectivity event is sent")
	assert.False(t, shouldReportToSentry(build(CategoryState, mysql.ErrInvalidConn)), "window is shared across categories and classes")
	assert.False(t, shouldReportToSentry(build(CategorySystem, build(CategoryDatabase, refused))), "wrapped dial error is rate limited")
	assert.True(t, shouldReportToSentry(build(CategoryDatabase, &mysql.MySQLError{Number: 1364, Message: "Field 'x' doesn't have a default value"})), "defects are never rate limited")
	assert.True(t, shouldReportToSentry(build(CategoryIntegration, refused)), "other categories keep today's behaviour")

	lastConnectivityReport.Store(lastConnectivityReport.Load() - connectivityReportCooldown - 1)
	assert.True(t, shouldReportToSentry(build(CategoryImageCache, &net.DNSError{Err: "no such host", Name: "x", IsNotFound: true})), "window expires")
}
