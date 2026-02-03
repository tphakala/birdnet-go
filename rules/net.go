//go:build ruleguard

package gorules

import "github.com/quasilyte/go-ruleguard/dsl"

// JoinHostPort detects fmt.Sprintf patterns for host:port and suggests net.JoinHostPort.
//
// The old pattern:
//
//	addr := fmt.Sprintf("%s:%d", host, port)
//
// Should be:
//
//	addr := net.JoinHostPort(host, strconv.Itoa(port))
//
// net.JoinHostPort properly handles IPv6 addresses by wrapping them in brackets,
// which fmt.Sprintf does not. This is critical for network code correctness.
//
// Note: This rule only flags patterns with integer ports to reduce false positives.
// String concatenation patterns like "host + : + port" are too common for non-network
// use cases (cache keys, identifiers, etc.) and are not flagged.
//
// See: https://pkg.go.dev/net#JoinHostPort
func JoinHostPort(m dsl.Matcher) {
	// Only flag fmt.Sprintf with integer port - this is a strong signal for network addresses
	// String ports could be cache keys, identifiers, etc.
	m.Match(
		`fmt.Sprintf("%s:%d", $host, $port)`,
		`fmt.Sprintf("%v:%d", $host, $port)`,
	).
		Report("use net.JoinHostPort($host, strconv.Itoa($port)) instead of fmt.Sprintf for host:port (handles IPv6 correctly)")
}

// FilepathIsLocal detects simple path traversal checks that could use filepath.IsLocal.
//
// Old pattern (manual path traversal check):
//
//	if strings.Contains(userPath, "..") {
//	    return errors.New("invalid path")
//	}
//	// Still vulnerable to other attacks
//
// New pattern (Go 1.20+):
//
//	if !filepath.IsLocal(userPath) {
//	    return errors.New("invalid path")
//	}
//	f, _ := os.Open(userPath)
//
// filepath.IsLocal reports whether path is:
//   - Not absolute
//   - Not empty
//   - Does not contain ".." elements
//   - Does not start with "/"
//   - On Windows: does not contain ":" or start with "\"
//
// Benefits:
//   - Comprehensive path validation
//   - Handles OS-specific path separators
//   - Prevents directory traversal attacks
//
// See: https://pkg.go.dev/path/filepath#IsLocal
func FilepathIsLocal(m dsl.Matcher) {
	// Detect simple .. check that might be replaced by IsLocal
	m.Match(
		`strings.Contains($path, "..")`,
	).
		Report("consider using filepath.IsLocal($path) for comprehensive path validation (Go 1.20+)")
}

// ErrorBeforeUse detects potential nil pointer dereference before error check.
//
// Go 1.25 fixed a compiler bug (Go 1.21-1.24) where nil checks were incorrectly delayed.
// Code that worked before may now correctly panic. This rule catches common patterns.
//
// Broken pattern:
//
//	f, err := os.Open(path)
//	name := f.Name()  // PANICS if err != nil
//	if err != nil { ... }
//
// Correct pattern:
//
//	f, err := os.Open(path)
//	if err != nil { ... }
//	name := f.Name()
//
// See: https://go.dev/doc/go1.25#compiler (nil check reordering fix)
func ErrorBeforeUse(m dsl.Matcher) {
	// os.Open/Create followed by method call before error check
	m.Match(
		`$f, $err := os.Open($path); $_ := $f.$method($*_); if $err != nil { $*_ }`,
		`$f, $err := os.Create($path); $_ := $f.$method($*_); if $err != nil { $*_ }`,
		`$f, $err := os.OpenFile($*_); $_ := $f.$method($*_); if $err != nil { $*_ }`,
	).
		Report("potential nil pointer: $f may be nil if $err != nil; check error before using $f.$method()")
}
