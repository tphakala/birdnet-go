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
	// Note: For URL paths, strings.Contains is still appropriate because filepath.IsLocal
	// cleans paths internally (e.g., "path/../etc" → "etc" → valid).
	m.Match(
		`strings.Contains($path, "..")`,
	).
		Report("consider using filepath.IsLocal($path) for file path validation (Go 1.20+); for URL paths, strings.Contains is appropriate")
}

// DeprecatedReverseProxyDirector detects usage of httputil.ReverseProxy's
// deprecated Director field and suggests using Rewrite instead.
//
// Deprecated pattern:
//
//	proxy := &httputil.ReverseProxy{
//	    Director: func(req *http.Request) {
//	        req.URL.Scheme = "https"
//	        req.URL.Host = "backend:8080"
//	    },
//	}
//
// New pattern (Go 1.26+):
//
//	proxy := &httputil.ReverseProxy{
//	    Rewrite: func(r *httputil.ProxyRequest) {
//	        r.SetURL(targetURL)
//	        r.SetXForwarded()
//	    },
//	}
//
// Security issue: When using Director, a malicious client can send a request
// that designates security headers (e.g., X-Forwarded-For) as hop-by-hop
// headers via the Connection header. The proxy strips hop-by-hop headers
// AFTER Director runs, effectively removing headers that Director set.
// Rewrite does not have this vulnerability because it operates on a copy
// of the request where hop-by-hop headers have already been removed.
//
// See: https://pkg.go.dev/net/http/httputil#ReverseProxy
// See: https://pkg.go.dev/net/http/httputil#ProxyRequest
func DeprecatedReverseProxyDirector(m dsl.Matcher) {
	m.Match(
		`httputil.ReverseProxy{$*_, Director: $_, $*_}`,
	).
		Report("httputil.ReverseProxy.Director is deprecated in Go 1.26: Director is vulnerable to hop-by-hop header abuse; use Rewrite instead for safe header handling")

	m.Match(
		`$proxy.Director = $_`,
	).
		Where(m["proxy"].Type.Is("*httputil.ReverseProxy")).
		Report("httputil.ReverseProxy.Director is deprecated in Go 1.26: Director is vulnerable to hop-by-hop header abuse; use Rewrite instead for safe header handling")
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
