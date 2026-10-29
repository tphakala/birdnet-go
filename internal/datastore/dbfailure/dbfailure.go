// Package dbfailure classifies why the database could not be used at startup
// (MySQL or MariaDB server rejections, unreachable servers, unusable SQLite
// paths, low disk space) into a typed Failure that names the settings to check.
//
// It is a leaf package: it imports only the standard library, internal/errors
// and the two database drivers, so the startup code and the API layer can both
// use it. The Error text of a StartError is built from constants only and never
// carries driver text, which holds the user name, the client address and the
// database name.
package dbfailure

import (
	"database/sql/driver"
	"fmt"
	"io/fs"
	"net"
	"slices"
	"syscall"

	"github.com/go-sql-driver/mysql"
	"github.com/mattn/go-sqlite3"
	"github.com/tphakala/birdnet-go/internal/errors"
)

// Backend names the database backend a failure belongs to.
type Backend string

// Database backends a Failure can belong to.
const (
	// BackendMySQL is a MySQL or MariaDB server.
	BackendMySQL Backend = "mysql"
	// BackendSQLite is a SQLite database file.
	BackendSQLite Backend = "sqlite"
)

// Kind names the cause of a failed database start. The values are stable strings.
type Kind string

// Kinds of failed database start.
const (
	// KindUnknown is any error this package does not recognise.
	KindUnknown Kind = "unknown"
	// KindMySQLAccessDenied is server error 1045: wrong user name or password.
	KindMySQLAccessDenied Kind = "mysql_access_denied"
	// KindMySQLDatabaseAccessDenied is server error 1044: no access to the database, or it does not exist.
	KindMySQLDatabaseAccessDenied Kind = "mysql_database_access_denied"
	// KindMySQLUnknownDatabase is server error 1049: the database does not exist.
	KindMySQLUnknownDatabase Kind = "mysql_unknown_database"
	// KindMySQLSecureTransportRequired is server error 3159: the server accepts encrypted connections only.
	KindMySQLSecureTransportRequired Kind = "mysql_secure_transport_required"
	// KindMySQLHostNotFound is a host name that does not resolve.
	KindMySQLHostNotFound Kind = "mysql_host_not_found"
	// KindMySQLUnreachable is a refused or broken connection to the server.
	KindMySQLUnreachable Kind = "mysql_unreachable"
	// KindMySQLTimeout is a connection attempt that got no answer in time.
	KindMySQLTimeout Kind = "mysql_timeout"
	// KindSQLitePathUnusable is a SQLite file or directory that cannot be created or opened.
	KindSQLitePathUnusable Kind = "sqlite_path_unusable"
	// KindSQLiteDiskSpace is a SQLite disk without enough free space.
	KindSQLiteDiskSpace Kind = "sqlite_disk_space"
)

// Setting keys named in hints, as config.yaml paths.
const (
	// SettingMySQLHost is the MySQL host setting.
	SettingMySQLHost = "output.mysql.host"
	// SettingMySQLPort is the MySQL port setting.
	SettingMySQLPort = "output.mysql.port"
	// SettingMySQLUsername is the MySQL user name setting.
	SettingMySQLUsername = "output.mysql.username"
	// SettingMySQLPassword is the MySQL password setting.
	SettingMySQLPassword = "output.mysql.password"
	// SettingMySQLDatabase is the MySQL database name setting.
	SettingMySQLDatabase = "output.mysql.database"
	// SettingSQLitePath is the SQLite database path setting.
	SettingSQLitePath = "output.sqlite.path"
)

// MySQL and MariaDB server error numbers. 3159 has the same number on both servers.
const (
	mysqlErrDBAccessDenied          uint16 = 1044 // ER_DBACCESS_DENIED_ERROR
	mysqlErrAccessDenied            uint16 = 1045 // ER_ACCESS_DENIED_ERROR
	mysqlErrBadDB                   uint16 = 1049 // ER_BAD_DB_ERROR
	mysqlErrSecureTransportRequired uint16 = 3159 // ER_SECURE_TRANSPORT_REQUIRED
)

const (
	// configFileName is how hints refer to the configuration file.
	configFileName = "config.yaml"
	// restartSuffix ends every StartError text.
	restartSuffix = "then restart BirdNET-Go"
	// dialOp is the net.OpError operation of a failed connection attempt.
	dialOp = "dial"
)

// ErrInsufficientDiskSpace marks a start refused for lack of free disk space.
// Its text is the message prefix the startup disk check has always used.
var ErrInsufficientDiskSpace = errors.NewStd("insufficient disk space to start application")

// Failure is the classification of a failed database start.
type Failure struct {
	// Kind is the cause.
	Kind Kind
	// Backend is the database backend, empty for KindUnknown.
	Backend Backend
	// MySQLErrorNumber is the server error number, 0 when the server did not answer with one.
	MySQLErrorNumber uint16
	// Settings are the setting keys the user should check, in hint order; nil for KindUnknown.
	Settings []string
	// Summary says what failed, with no values from the configuration.
	Summary string
	// Hint says what to do and names every key in Settings.
	Hint string
}

// ServerRejected reports whether the MySQL or MariaDB server answered and refused
// the connection (1044, 1045, 1049, 3159): retrying cannot help and no database
// state was read.
func (f *Failure) ServerRejected() bool {
	switch f.Kind {
	case KindMySQLAccessDenied, KindMySQLDatabaseAccessDenied, KindMySQLUnknownDatabase, KindMySQLSecureTransportRequired:
		return true
	default:
		return false
	}
}

// kindSpec is the constant part of a Failure.
type kindSpec struct {
	backend  Backend
	settings []string
	summary  string
	hint     string
}

// specs holds the summary and hint of every Kind except KindUnknown.
var specs = map[Kind]kindSpec{
	KindMySQLAccessDenied: {
		backend:  BackendMySQL,
		settings: []string{SettingMySQLUsername, SettingMySQLPassword},
		summary:  "the MySQL or MariaDB server rejected the user name or password",
		hint: "check " + SettingMySQLUsername + " and " + SettingMySQLPassword + " in " + configFileName +
			", and that this user may connect from the BirdNET-Go host",
	},
	KindMySQLDatabaseAccessDenied: {
		backend:  BackendMySQL,
		settings: []string{SettingMySQLDatabase, SettingMySQLUsername},
		summary:  "the MySQL or MariaDB user has no access to the configured database, or the database does not exist",
		hint: "check " + SettingMySQLDatabase + " and " + SettingMySQLUsername + " in " + configFileName +
			"; create the database on the server or grant this user privileges on it",
	},
	KindMySQLUnknownDatabase: {
		backend:  BackendMySQL,
		settings: []string{SettingMySQLDatabase},
		summary:  "the configured MySQL or MariaDB database does not exist",
		hint:     "create the database on the server or correct " + SettingMySQLDatabase + " in " + configFileName,
	},
	KindMySQLSecureTransportRequired: {
		backend:  BackendMySQL,
		settings: []string{SettingMySQLHost},
		summary:  "the MySQL or MariaDB server accepts only encrypted connections",
		hint: "BirdNET-Go cannot connect to a server that requires encrypted connections; check the require_secure_transport setting of the server behind " +
			SettingMySQLHost + " in " + configFileName,
	},
	KindMySQLHostNotFound: {
		backend:  BackendMySQL,
		settings: []string{SettingMySQLHost},
		summary:  "the MySQL or MariaDB host name could not be resolved",
		hint:     "check " + SettingMySQLHost + " in " + configFileName + " and that the BirdNET-Go host can resolve it",
	},
	KindMySQLUnreachable: {
		backend:  BackendMySQL,
		settings: []string{SettingMySQLHost, SettingMySQLPort},
		summary:  "the MySQL or MariaDB server could not be reached",
		hint: "make sure the server is running and reachable at " + SettingMySQLHost + " and " + SettingMySQLPort +
			" in " + configFileName,
	},
	KindMySQLTimeout: {
		backend:  BackendMySQL,
		settings: []string{SettingMySQLHost, SettingMySQLPort},
		summary:  "the MySQL or MariaDB server did not answer in time",
		hint: "check " + SettingMySQLHost + " and " + SettingMySQLPort + " in " + configFileName +
			" and that no firewall drops the connection",
	},
	KindSQLitePathUnusable: {
		backend:  BackendSQLite,
		settings: []string{SettingSQLitePath},
		summary:  "the SQLite database file cannot be created or opened",
		hint: "make sure the directory in " + SettingSQLitePath +
			" exists and is writable by the user BirdNET-Go runs as",
	},
	KindSQLiteDiskSpace: {
		backend:  BackendSQLite,
		settings: []string{SettingSQLitePath},
		summary:  "there is not enough free disk space for the SQLite database",
		hint: "free disk space on the disk that holds " + SettingSQLitePath + ", or move " + SettingSQLitePath +
			" to a larger disk",
	},
}

// newFailure builds the Failure of kind from the constant table.
func newFailure(kind Kind) Failure {
	spec := specs[kind]
	return Failure{
		Kind:     kind,
		Backend:  spec.backend,
		Settings: slices.Clone(spec.settings),
		Summary:  spec.summary,
		Hint:     spec.hint,
	}
}

// newServerFailure builds the Failure of a server rejection with its error number.
func newServerFailure(kind Kind, number uint16) Failure {
	f := newFailure(kind)
	f.MySQLErrorNumber = number
	f.Summary = fmt.Sprintf("%s (MySQL error %d)", f.Summary, number)
	return f
}

// Classify maps err to a Failure. Unrecognised errors and nil give KindUnknown.
// It inspects the typed values itself and does not depend on errors.IsConnectivityError.
func Classify(err error) Failure {
	if err == nil {
		return unknownFailure
	}
	if serverErr, ok := errors.AsType[*mysql.MySQLError](err); ok {
		return classifyServerError(serverErr.Number)
	}
	// A DNS failure arrives as a dial error that wraps the DNS error, so it is tested first.
	if _, ok := errors.AsType[*net.DNSError](err); ok {
		return newFailure(KindMySQLHostNotFound)
	}
	if opErr, ok := errors.AsType[*net.OpError](err); ok {
		if opErr.Op != dialOp {
			return unknownFailure
		}
		if opErr.Timeout() {
			return newFailure(KindMySQLTimeout)
		}
		return newFailure(KindMySQLUnreachable)
	}
	if errors.Is(err, mysql.ErrInvalidConn) || errors.Is(err, driver.ErrBadConn) {
		return newFailure(KindMySQLUnreachable)
	}
	return classifyFile(err)
}

// classifyServerError maps a MySQL or MariaDB server error number to a Failure.
// Numbers it does not know give KindUnknown rather than a guess.
func classifyServerError(number uint16) Failure {
	switch number {
	case mysqlErrAccessDenied:
		return newServerFailure(KindMySQLAccessDenied, number)
	case mysqlErrDBAccessDenied:
		return newServerFailure(KindMySQLDatabaseAccessDenied, number)
	case mysqlErrBadDB:
		return newServerFailure(KindMySQLUnknownDatabase, number)
	case mysqlErrSecureTransportRequired:
		return newServerFailure(KindMySQLSecureTransportRequired, number)
	default:
		return unknownFailure
	}
}

// classifyFile maps SQLite and file system errors to a Failure.
func classifyFile(err error) Failure {
	sqliteErr, isSQLite := errors.AsType[sqlite3.Error](err)
	switch {
	case errors.Is(err, ErrInsufficientDiskSpace), errors.Is(err, syscall.ENOSPC),
		isSQLite && sqliteErr.Code == sqlite3.ErrFull:
		return newFailure(KindSQLiteDiskSpace)
	case isSQLite && (sqliteErr.Code == sqlite3.ErrCantOpen || sqliteErr.Code == sqlite3.ErrPerm ||
		sqliteErr.Code == sqlite3.ErrReadonly),
		errors.Is(err, fs.ErrPermission), errors.Is(err, fs.ErrNotExist),
		errors.Is(err, syscall.ENOTDIR), errors.Is(err, syscall.EROFS):
		return newFailure(KindSQLitePathUnusable)
	default:
		return unknownFailure
	}
}

// unknownFailure is the result for an error that is not a recognised start failure.
var unknownFailure = Failure{Kind: KindUnknown}

// StartError is a database start failure with a classification. Its text is built
// from constants only; Unwrap returns the original error so typed causes stay
// reachable through errors.Is and errors.AsType.
type StartError struct {
	// Op names the refused operation and starts the text.
	Op string
	// Failure is the classification of the cause.
	Failure Failure
	cause   error
}

// NewStartError returns a StartError for cause, classified as f. It keeps a copy of f.
func NewStartError(op string, f *Failure, cause error) *StartError {
	return &StartError{Op: op, Failure: *f, cause: cause}
}

// Error returns "<Op>: <Summary>; <Hint>, then restart BirdNET-Go". It never
// includes the text of the cause.
func (e *StartError) Error() string {
	return e.Op + ": " + e.Failure.Summary + "; " + e.Failure.Hint + ", " + restartSuffix
}

// Unwrap returns the original error.
func (e *StartError) Unwrap() error { return e.cause }
