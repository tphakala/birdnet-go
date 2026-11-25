package errors

import (
	"database/sql/driver"
	"net"
	"os"
	"regexp"
	"slices"
	"strings"
)

const (
	// mysqlInvalidConnMessage is the text of mysql.ErrInvalidConn in
	// go-sql-driver/mysql v1.10.1. The driver returns the sentinel bare, without
	// a driver.ErrBadConn wrapper, so it is matched as a chain leaf by message
	// instead of importing the driver into this package.
	mysqlInvalidConnMessage = "invalid connection"

	// dialOp is the net.OpError operation of an outgoing connection attempt.
	dialOp = "dial"

	// flattenedDialPrefix is the cheap pre-check for flattened dial errors.
	flattenedDialPrefix = "dial "
)

var (
	// mysqlServerErrorRegex matches the "Error 1364 (HY000): ..." shape of
	// mysql.MySQLError. GORM joins a second error into the first as
	// "%v; %w", so a server-side defect can sit in front of a connectivity
	// leaf, or behind a flattened dial error; such an error must stay
	// reportable.
	mysqlServerErrorRegex = regexp.MustCompile(`(?i)\berror \d{4}\b`)

	// eofTokenRegex matches EOF as a whole word in a lowercased message, the
	// text of a connection closed by the peer (for example an MQTT broker
	// disconnect). A plain substring would also match words such as "geoffroy"
	// and "typeof".
	eofTokenRegex = regexp.MustCompile(`\beof\b`)

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

// IsUnsentError reports whether err means no byte of the failed operation
// reached the database server: a failed dial (connect refused, host or network
// unreachable, dial timeout, DNS lookup failure) or driver.ErrBadConn. The
// database/sql/driver contract returns ErrBadConn only when the driver is sure
// the server did not perform the operation, so a single-transaction write that
// failed this way stored nothing and can be repeated without creating a
// duplicate. Unlike IsConnectivityError it does not accept the MySQL driver's
// "invalid connection" sentinel, which the driver also returns when a COMMIT
// was sent and its reply was lost, a case where the server may have applied the
// write. Errors carrying a MySQL server error marker are not unsent errors.
func IsUnsentError(err error) bool {
	if err == nil {
		return false
	}
	if ee, ok := err.(*EnhancedError); ok && ee.Err == nil { //nolint:errorlint // the nil-cause guard needs the outermost value only
		return false
	}
	msg := err.Error()
	return hasUnsentShape(err, msg) && !mysqlServerErrorRegex.MatchString(msg)
}

// hasConnectivityShape reports whether err, whose message is msg, has one of
// the connectivity shapes, before the server error veto: an unsent shape
// (hasUnsentShape) or the MySQL driver's "invalid connection" sentinel.
func hasConnectivityShape(err error, msg string) bool {
	return hasUnsentShape(err, msg) || isLeafMessage(err, mysqlInvalidConnMessage)
}

// hasUnsentShape reports whether err, whose message is msg, shows that the
// operation never reached the server, before the server error veto.
func hasUnsentShape(err error, msg string) bool {
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

// connectivitySignatures are lowercase transport-failure texts, matched in
// network-category errors only. They indicate user infrastructure issues
// (unreachable hosts, dropped connections, DNS, client-side timeouts), not
// code bugs.
var connectivitySignatures = []string{
	"no route to host",
	"connection refused",
	"connection reset by peer",
	"server misbehaving",    // DNS failure
	"no such host",          // DNS failure
	"dns resolution failed", // DNS failure (BirdWeather wrapper)
	"network is unreachable",
	"i/o timeout",
	"context deadline exceeded", // Upload/request timeout
	"tls handshake timeout",

	// net/http transport and client failures. The http2 text is the transport's
	// "client connection lost"; "client.timeout" ends the "net/http: request
	// canceled (Client.Timeout exceeded while awaiting headers)" and body-read
	// texts of the HTTP client timeout.
	"http2: client connection lost",
	"client.timeout",

	// Connect failures reported by ffmpeg and the kernel: ETIMEDOUT and
	// EHOSTDOWN texts, and the "Network unreachable" PrimaryMessage that the
	// "network is unreachable" entry above does not match.
	"connection timed out",
	"host is down",
	"network unreachable",

	// MQTT client failures: paho's "connection lost before Publish/Subscribe/
	// Unsubscribe completed", the publish wait timeout, and the connect wait
	// failures in internal/mqtt/client.go. The too-short configured timeout
	// message is deliberately absent: it is a configuration error.
	"connection lost before",
	"publish timeout after",
	"mqtt connection failed post-wait",
	"mqtt connection attempt actively timed out",
	"not connected to mqtt broker", // publish attempted while the broker link is down

	// imageprovider Wikipedia rate-limit / circuit-breaker noise.
	// Wikipedia rate-limits the image fetcher (HTTP 429) and the circuit
	// breaker then throttles further calls, so forwarding a Sentry event for
	// each rejected request is pure noise. These are built with CategoryNetwork
	// in internal/imageprovider/wikipedia.go. The patterns are Wikipedia-specific
	// full signatures (not loose substrings like "rate limit exceeded") so
	// network-category errors from other components are never collaterally
	// suppressed, matching the rtspSilenceTimeoutSignature approach.
	// HTTP 429 is purely environmental throttling so every occurrence is
	// suppressed; 403/500/503 still report at least the first event because
	// those can indicate a real problem (policy violation, server fault).
	// Lowercase substrings of the real messages produced there:
	//   - handleHTTPStatusError: "Wikipedia API returned status 429: <body>"
	//   - checkCircuitBreaker:   "Wikipedia API circuit breaker open: <reason>"
	//   - diagnostic path:       "Wikipedia rate limit exceeded: <message>"
	"wikipedia api returned status 429",  // direct HTTP 429 from the API
	"wikipedia api circuit breaker open", // repeated rejection while breaker is open
	"wikipedia rate limit exceeded",      // diagnostic rate-limit error
}

// isConnectivityCategory reports whether errors of c are checked for typed
// connectivity failures. Network categories use the text signatures instead
// (see isNetworkCategory) and CategoryIntegration stays excluded.
func isConnectivityCategory(c ErrorCategory) bool {
	switch c { //nolint:exhaustive // only these four categories are gated
	case CategoryDatabase, CategoryState, CategorySystem, CategoryImageCache:
		return true
	default:
		return false
	}
}

// isGatedConnectivity reports whether ee is a typed connectivity failure in a
// gated category.
func isGatedConnectivity(ee *EnhancedError) bool {
	return isConnectivityCategory(ee.Category) && IsConnectivityError(ee.Err)
}

// isNetworkCategory reports whether c is a category whose errors are checked
// against connectivitySignatures. CategoryIntegration is deliberately
// excluded to avoid suppressing legitimate non-transient integration errors;
// transient network failures in integrations should use CategoryNetwork.
func isNetworkCategory(c ErrorCategory) bool {
	switch c { //nolint:exhaustive // only network-related categories need this filter
	case CategoryNetwork, CategoryMQTTConnection, CategoryMQTTPublish, CategoryRTSP, CategoryHTTP:
		return true
	default:
		return false
	}
}

// isConnectivityNoise reports whether ee is a connectivity failure. These are
// environment problems, not code defects, so telemetry never reports them. The
// typed check (IsConnectivityError: dial, DNS, driver.ErrBadConn and the MySQL
// "invalid connection" sentinel) applies to the database, state, system and
// image-cache categories only: a dial error under another category can be a
// configuration problem such as a permission or missing socket error. The text
// signatures (connectivitySignatures plus a whole-word EOF match) apply only to
// network categories, so a code error that happens to mention a timeout
// elsewhere keeps reporting. lowerMsg is the lowercased message of ee.Err.
func isConnectivityNoise(ee *EnhancedError, lowerMsg string) bool {
	if isGatedConnectivity(ee) {
		return true
	}
	if !isNetworkCategory(ee.Category) {
		return false
	}
	return containsAny(lowerMsg, connectivitySignatures) || eofTokenRegex.MatchString(lowerMsg)
}
