package errors

import (
	"context"
	"database/sql/driver"
	"net"
	"os"
	"regexp"
	"slices"
	"strings"
	"sync/atomic"
	"time"
)

const (
	// connectivityReportCooldown is the minimum time between two connectivity
	// events sent to Sentry from one process, shared across error classes and
	// categories so one outage yields a bounded number of events.
	connectivityReportCooldown = time.Hour

	// mysqlInvalidConnMessage is the text of mysql.ErrInvalidConn in
	// go-sql-driver/mysql v1.10.1. The driver returns the sentinel bare, without
	// a driver.ErrBadConn wrapper, so it is matched as a chain leaf by message
	// instead of importing the driver into this package.
	mysqlInvalidConnMessage = "invalid connection"

	// dialOp is the net.OpError operation of an outgoing connection attempt.
	dialOp = "dial"

	// flattenedDialPrefix is the cheap pre-check for flattened dial errors.
	flattenedDialPrefix = "dial "

	// connectivityFingerprintPrefix leads the Sentry fingerprint of every
	// connectivity event so they group apart from code defects.
	connectivityFingerprintPrefix = "connectivity"

	// errorOriginEnvironment is the error_origin tag of connectivity events.
	errorOriginEnvironment = "environment"
)

var (
	// connectivityLimiter holds the window of the last connectivity event
	// allowed through to Sentry.
	connectivityLimiter = reportCooldown{window: connectivityReportCooldown}

	// mysqlServerErrorRegex matches the "Error 1364 (HY000): ..." shape of
	// mysql.MySQLError. GORM joins a second error into the first as
	// "%v; %w", so a server-side defect can sit in front of a connectivity
	// leaf, or behind a flattened dial error; such an error must stay
	// reportable.
	mysqlServerErrorRegex = regexp.MustCompile(`(?i)\berror \d{4}\b`)

	// connectSyscalls are the syscall names net reports for a failed connect.
	connectSyscalls = []string{"connect", "connectex"}

	// flattenedDialAnchors mark a dial error whose chain was flattened into text.
	flattenedDialAnchors = []string{"dial tcp", "dial unix"}

	// flattenedDialTokens are the failure texts accepted together with a dial anchor.
	flattenedDialTokens = []string{
		"connection refused",
		"no route to host",
		"network is unreachable",
		"i/o timeout",
		"no such host",
		"server misbehaving",
	}
)

// IsConnectivityError reports whether err means the database host could not
// be reached: a failed dial (connect refused, host or network unreachable,
// dial timeout, DNS lookup failure), driver.ErrBadConn, or the MySQL driver's
// "invalid connection" sentinel. Chains flattened with %v are recognised by a
// narrow dial-plus-token text match. Errors carrying a MySQL server error
// marker, malformed addresses, descriptor exhaustion and listener errors are
// not connectivity errors. The check is fail-open: anything unrecognised
// returns false and is reported as before.
func IsConnectivityError(err error) bool {
	if err == nil {
		return false
	}
	if ee, ok := err.(*EnhancedError); ok && ee.Err == nil { //nolint:errorlint // the nil-cause guard needs the outermost value only
		return false
	}
	msg := err.Error()
	// The MySQL server error veto only runs for a positive match, so
	// unrecognised errors never reach the regexp.
	return hasConnectivityShape(err, msg) && !mysqlServerErrorRegex.MatchString(msg)
}

// hasConnectivityShape reports whether err, whose message is msg, has one of
// the connectivity shapes, before the server error veto.
func hasConnectivityShape(err error, msg string) bool {
	if _, ok := AsType[*net.DNSError](err); ok {
		return true
	}
	if opErr, ok := AsType[*net.OpError](err); ok && opErr.Op == dialOp {
		if opErr.Timeout() {
			return true
		}
		if sysErr, ok := AsType[*os.SyscallError](opErr.Err); ok && isConnectSyscall(sysErr.Syscall) {
			return true
		}
	}
	if Is(err, driver.ErrBadConn) {
		return true
	}
	if isLeafMessage(err, mysqlInvalidConnMessage) {
		return true
	}
	return isFlattenedDialFailure(msg)
}

// isConnectSyscall reports whether name is a connect syscall name from net.
func isConnectSyscall(name string) bool {
	return slices.Contains(connectSyscalls, name)
}

// isFlattenedDialFailure matches a dial failure whose typed chain was lost.
func isFlattenedDialFailure(msg string) bool {
	// net always writes "dial" in lower case; most messages end here without
	// being lowered.
	if !strings.Contains(msg, flattenedDialPrefix) {
		return false
	}
	lower := strings.ToLower(msg)
	return containsAny(lower, flattenedDialAnchors) && containsAny(lower, flattenedDialTokens)
}

// containsAny reports whether s contains any of the substrings.
func containsAny(s string, substrs []string) bool {
	for _, sub := range substrs {
		if strings.Contains(s, sub) {
			return true
		}
	}
	return false
}

// isLeafMessage reports whether some error in err's tree wraps nothing and has
// exactly the given message. Both Unwrap forms are followed and nil children
// are skipped.
func isLeafMessage(err error, msg string) bool {
	switch e := err.(type) { //nolint:errorlint // the tree walk inspects each node by design
	case *EnhancedError:
		// Reachable through a wrapper whose message omits the child, so
		// the whole-tree Error() call did not already dereference the nil cause.
		if e.Err == nil {
			return false
		}
		return isLeafMessage(e.Err, msg)
	case interface{ Unwrap() []error }:
		for _, child := range e.Unwrap() {
			if child != nil && isLeafMessage(child, msg) {
				return true
			}
		}
		return false
	case interface{ Unwrap() error }:
		child := e.Unwrap()
		if child == nil {
			return err.Error() == msg
		}
		return isLeafMessage(child, msg)
	default:
		return err.Error() == msg
	}
}

// isConnectivityCategory reports whether errors of c are checked for
// connectivity failures. Network categories keep their own noise filter and
// CategoryIntegration stays excluded.
func isConnectivityCategory(c ErrorCategory) bool {
	switch c { //nolint:exhaustive // only these four categories are gated
	case CategoryDatabase, CategoryState, CategorySystem, CategoryImageCache:
		return true
	default:
		return false
	}
}

// isDialDeadlineConnectivity reports whether ee is a connectivity failure whose
// only context signal is the deadline a failed dial carries: the dial timeout
// the net package returns is also context.DeadlineExceeded, so an unreachable
// host would otherwise be dropped as a context interruption and never reach the
// connectivity window. Canceled contexts, bare deadline errors and PriorityLow
// system errors stay suppressed.
func isDialDeadlineConnectivity(ee *EnhancedError, connectivity bool) bool {
	if !connectivity || (ee.Category == CategorySystem && ee.GetPriority() == PriorityLow) {
		return false
	}
	opErr, ok := AsType[*net.OpError](ee.Err)
	return ok && opErr.Op == dialOp && !Is(ee.Err, context.Canceled)
}

// isGatedConnectivity reports whether ee is a connectivity failure in a gated category.
func isGatedConnectivity(ee *EnhancedError) bool {
	return isConnectivityCategory(ee.Category) && IsConnectivityError(ee.Err)
}

// allowConnectivityReport lets one connectivity event through per cooldown window.
func allowConnectivityReport() bool {
	return connectivityLimiter.Allow(time.Now())
}

// reportCooldown lets one event through per window. It is shared by the
// connectivity and disk-full limiters.
type reportCooldown struct {
	last   atomic.Int64 // Unix time of the last allowed event
	window time.Duration
}

// Allow reports whether an event at now may be sent and, if so, starts a new
// window. CompareAndSwap keeps concurrent errors from bypassing the window
// (TOCTOU race): the loser of the swap is suppressed.
func (c *reportCooldown) Allow(now time.Time) bool {
	nowUnix := now.Unix()
	last := c.last.Load()
	if nowUnix-last < int64(c.window/time.Second) {
		return false
	}
	return c.last.CompareAndSwap(last, nowUnix)
}
