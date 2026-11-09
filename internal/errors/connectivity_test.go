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

func TestIsUnsentError(t *testing.T) {
	t.Parallel()

	refused := refusedDialError(syscall.ECONNREFUSED)
	server1045 := &mysql.MySQLError{Number: 1045, SQLState: [5]byte{'2', '8', '0', '0', '0'}, Message: "Access denied for user 'dial'@'host' (connection refused)"}

	tests := []struct {
		name string
		err  error
		want bool
	}{
		{"connection refused", refused, true},
		{"wrapped connection refused", fmt.Errorf("get/create model: %w", refused), true},
		{"dns error", &net.DNSError{Err: "no such host", Name: "db", IsNotFound: true}, true},
		{"dial timeout", &net.OpError{Op: "dial", Net: "tcp", Err: os.ErrDeadlineExceeded}, true},
		{"wrapped bad conn", fmt.Errorf("x: %w", driver.ErrBadConn), true},
		{"flattened refused", flatten("x", refused), true},

		{"nil", nil, false},
		// The row that matters: the driver returns this for a COMMIT whose
		// outcome is unknown, so it must not count as "nothing was sent".
		{"wrapped invalid connection", fmt.Errorf("commit: %w", mysql.ErrInvalidConn), false},
		{"bare invalid connection", mysql.ErrInvalidConn, false},
		{"mysql server error mentioning dial", NewStd(server1045.Error()), false},
		{"mysql server error type", server1045, false},
		{"listener error", &net.OpError{Op: "listen", Net: "tcp", Err: os.NewSyscallError("bind", syscall.EADDRINUSE)}, false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			assert.Equal(t, tt.want, IsUnsentError(tt.err))
		})
	}
}

func TestShouldReportToSentry_DropsDatabaseConnectivity(t *testing.T) {
	t.Parallel()

	build := func(cat ErrorCategory, err error) *EnhancedError {
		return New(err).Component("test").Category(cat).Build()
	}
	refused := refusedDialError(syscall.ECONNREFUSED)
	dialDeadline := &net.OpError{Op: "dial", Net: "tcp", Err: context.DeadlineExceeded}
	require.True(t, isContextCancellation(dialDeadline), "the real dial timeout is also a context deadline")
	// The udp network keeps the message from matching the flattened tcp dial
	// text, so only the typed timeout check can recognise it.
	udpDialTimeout := &net.OpError{Op: "dial", Net: "udp", Err: os.ErrDeadlineExceeded}
	server1364 := &mysql.MySQLError{Number: 1364, Message: "Field 'x' doesn't have a default value"}

	tests := []struct {
		name       string
		ee         *EnhancedError
		wantReport bool
	}{
		{"dial timeout in database", build(CategoryDatabase, dialDeadline), false},
		{"udp dial timeout in state", build(CategoryState, udpDialTimeout), false},
		{"udp dial timeout in image cache", build(CategoryImageCache, udpDialTimeout), false},
		{"refused dial in database", build(CategoryDatabase, refused), false},
		{"invalid connection in state", build(CategoryState, mysql.ErrInvalidConn), false},
		{"wrapped dial in system", build(CategorySystem, build(CategoryDatabase, refused)), false},
		{"dns error in image cache", build(CategoryImageCache, &net.DNSError{Err: "no such host", Name: "x", IsNotFound: true}), false},
		{"canceled dial", build(CategoryDatabase, &net.OpError{Op: "dial", Net: "tcp", Err: context.Canceled}), false},
		{"bare context deadline", build(CategoryDatabase, context.DeadlineExceeded), false},
		{"mysql server error joined with a connection error", build(CategoryDatabase, gormJoin(refused, server1364)), true},
		{"mysql server error", build(CategoryDatabase, server1364), true},
		{"refused dial in an ungated category", build(CategoryIntegration, refused), true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			// Evaluated twice: the verdict is deterministic and no event is
			// let through first as it used to be under the hourly window.
			assert.Equal(t, tt.wantReport, shouldReportToSentry(tt.ee), "first evaluation")
			assert.Equal(t, tt.wantReport, shouldReportToSentry(tt.ee), "second evaluation")
		})
	}
}

// hidingWrapperError is an error whose message omits its wrapped child.
type hidingWrapperError struct{ child error }

func (hidingWrapperError) Error() string   { return "wrapper" }
func (w hidingWrapperError) Unwrap() error { return w.child }
