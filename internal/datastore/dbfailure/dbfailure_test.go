package dbfailure

import (
	"database/sql/driver"
	"fmt"
	"io/fs"
	"net"
	"os"
	"syscall"
	"testing"

	"github.com/go-sql-driver/mysql"
	"github.com/mattn/go-sqlite3"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/tphakala/birdnet-go/internal/errors"
)

const (
	testUserName = "secret_user"
	testAddress  = "192.0.2.7"
)

// errTestSentinel stands in for the startup package sentinel that wraps driver errors.
var errTestSentinel = errors.NewStd("sentinel")

// mysqlServerError builds a server error with a message that carries the user name
// and the client address, as the real server does.
func mysqlServerError(number uint16) *mysql.MySQLError {
	return &mysql.MySQLError{
		Number:   number,
		SQLState: [5]byte{'2', '8', '0', '0', '0'},
		Message:  fmt.Sprintf("Access denied for user '%s'@'%s' (using password: YES)", testUserName, testAddress),
	}
}

func refusedDial() error {
	return &net.OpError{
		Op: "dial", Net: "tcp",
		Addr: &net.TCPAddr{IP: net.ParseIP(testAddress), Port: 3306},
		Err:  &os.SyscallError{Syscall: "connect", Err: syscall.ECONNREFUSED},
	}
}

func TestClassify(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name       string
		err        error
		wantKind   Kind
		wantNumber uint16
		wantSet    []string
		backend    Backend
	}{
		{"access denied wrapped like the state check", fmt.Errorf("%w: %w", errTestSentinel, mysqlServerError(1045)),
			KindMySQLAccessDenied, 1045, []string{SettingMySQLUsername, SettingMySQLPassword}, BackendMySQL},
		{"database access denied in the Open shape", errors.New(mysqlServerError(1044)).Component("datastore").Category(errors.CategoryDatabase).Build(),
			KindMySQLDatabaseAccessDenied, 1044, []string{SettingMySQLDatabase, SettingMySQLUsername}, BackendMySQL},
		{"unknown database", mysqlServerError(1049), KindMySQLUnknownDatabase, 1049, []string{SettingMySQLDatabase}, BackendMySQL},
		{"secure transport required", mysqlServerError(3159), KindMySQLSecureTransportRequired, 3159, []string{SettingMySQLHost}, BackendMySQL},
		{"other server error is not guessed", mysqlServerError(1142), KindUnknown, 0, nil, ""},
		{"dns failure arrives as a dial error and wins over it",
			&net.OpError{Op: "dial", Net: "tcp", Err: &net.DNSError{Err: "no such host", Name: "x.invalid", IsNotFound: true}},
			KindMySQLHostNotFound, 0, []string{SettingMySQLHost}, BackendMySQL},
		{"refused dial", refusedDial(), KindMySQLUnreachable, 0, []string{SettingMySQLHost, SettingMySQLPort}, BackendMySQL},
		{"dial timeout", &net.OpError{Op: "dial", Net: "tcp", Err: os.ErrDeadlineExceeded},
			KindMySQLTimeout, 0, []string{SettingMySQLHost, SettingMySQLPort}, BackendMySQL},
		{"read error is not a dial failure", &net.OpError{Op: "read", Net: "tcp", Err: syscall.ECONNRESET}, KindUnknown, 0, nil, ""},
		{"driver invalid connection", mysql.ErrInvalidConn, KindMySQLUnreachable, 0, []string{SettingMySQLHost, SettingMySQLPort}, BackendMySQL},
		{"sql bad connection", driver.ErrBadConn, KindMySQLUnreachable, 0, []string{SettingMySQLHost, SettingMySQLPort}, BackendMySQL},
		{"sqlite cannot open", sqlite3.Error{Code: sqlite3.ErrCantOpen}, KindSQLitePathUnusable, 0, []string{SettingSQLitePath}, BackendSQLite},
		{"sqlite full", sqlite3.Error{Code: sqlite3.ErrFull}, KindSQLiteDiskSpace, 0, []string{SettingSQLitePath}, BackendSQLite},
		{"startup disk check", fmt.Errorf("%w: 512MB available", ErrInsufficientDiskSpace), KindSQLiteDiskSpace, 0, []string{SettingSQLitePath}, BackendSQLite},
		{"permission denied creating the directory", &fs.PathError{Op: "mkdir", Path: "/x", Err: syscall.EACCES},
			KindSQLitePathUnusable, 0, []string{SettingSQLitePath}, BackendSQLite},
		{"parent is not a directory", syscall.ENOTDIR, KindSQLitePathUnusable, 0, []string{SettingSQLitePath}, BackendSQLite},
		{"sqlite permission", sqlite3.Error{Code: sqlite3.ErrPerm}, KindSQLitePathUnusable, 0, []string{SettingSQLitePath}, BackendSQLite},
		{"sqlite read only", sqlite3.Error{Code: sqlite3.ErrReadonly}, KindSQLitePathUnusable, 0, []string{SettingSQLitePath}, BackendSQLite},
		{"directory does not exist", &fs.PathError{Op: "open", Path: "/x", Err: syscall.ENOENT},
			KindSQLitePathUnusable, 0, []string{SettingSQLitePath}, BackendSQLite},
		{"read only file system", syscall.EROFS, KindSQLitePathUnusable, 0, []string{SettingSQLitePath}, BackendSQLite},
		{"no space left on device", &fs.PathError{Op: "write", Path: "/x", Err: syscall.ENOSPC},
			KindSQLiteDiskSpace, 0, []string{SettingSQLitePath}, BackendSQLite},
		{"nil", nil, KindUnknown, 0, nil, ""},
		{"other", errors.NewStd("other"), KindUnknown, 0, nil, ""},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			f := Classify(tt.err)
			assert.Equal(t, tt.wantKind, f.Kind)
			assert.Equal(t, tt.backend, f.Backend)
			assert.Equal(t, tt.wantNumber, f.MySQLErrorNumber)
			assert.Equal(t, tt.wantSet, f.Settings)
			for _, key := range f.Settings {
				assert.Contains(t, f.Hint, key, "the hint must name every setting it lists")
			}
		})
	}
}

func TestClassify_SettingsAreACopy(t *testing.T) {
	t.Parallel()
	first := Classify(mysqlServerError(1045))
	first.Settings[0] = "mutated"
	assert.Equal(t, SettingMySQLUsername, Classify(mysqlServerError(1045)).Settings[0])
}

func TestFailure_ServerRejected(t *testing.T) {
	t.Parallel()
	for _, n := range []uint16{1044, 1045, 1049, 3159} {
		f := Classify(mysqlServerError(n))
		assert.True(t, f.ServerRejected(), "error %d", n)
	}
	for _, err := range []error{refusedDial(), sqlite3.Error{Code: sqlite3.ErrCantOpen}, nil} {
		f := Classify(err)
		assert.False(t, f.ServerRejected())
	}
}

func TestStartError_MessageCarriesHintWithoutDriverText(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name             string
		cause            error
		wantConnectivity bool
		wantTarget       func(error) bool
	}{
		{"access denied", fmt.Errorf("%w: %w", errTestSentinel, mysqlServerError(1045)), false,
			func(err error) bool { _, ok := errors.AsType[*mysql.MySQLError](err); return ok }},
		{"refused dial", refusedDial(), true,
			func(err error) bool { _, ok := errors.AsType[*net.OpError](err); return ok }},
		{"sqlite cannot open", sqlite3.Error{Code: sqlite3.ErrCantOpen}, false,
			func(err error) bool { _, ok := errors.AsType[sqlite3.Error](err); return ok }},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			f := Classify(tt.cause)
			require.NotEqual(t, KindUnknown, f.Kind)
			var err error = NewStartError("refusing to start", &f, tt.cause)
			msg := err.Error()
			assert.Contains(t, msg, "refusing to start")
			assert.Contains(t, msg, "then restart BirdNET-Go")
			for _, key := range f.Settings {
				assert.Contains(t, msg, key)
			}
			for _, leak := range []string{testUserName, testAddress, "Access denied", "connection refused", "unable to open"} {
				assert.NotContains(t, msg, leak)
			}
			assert.True(t, tt.wantTarget(err), "the typed cause must stay reachable")
			assert.Equal(t, tt.wantConnectivity, errors.IsConnectivityError(err))
		})
	}
}
