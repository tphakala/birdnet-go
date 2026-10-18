package security

import (
	"net"
	"net/http"
	"net/netip"
	"strings"

	"github.com/gorilla/sessions"
	"github.com/labstack/echo/v4"

	"github.com/tphakala/birdnet-go/internal/conf"
	"github.com/tphakala/birdnet-go/internal/security/proxytrust"
)

// Proxy request headers that carry no client IP, so proxytrust does not list
// them, and that Echo does not define.
const (
	headerXForwardedHost   = "X-Forwarded-Host"
	headerXForwardedPort   = "X-Forwarded-Port"
	headerXForwardedPrefix = "X-Forwarded-Prefix"
	headerXIngressPath     = "X-Ingress-Path" // Home Assistant ingress
	headerVia              = "Via"            // RFC 9110
)

// proxyOnlyHeaders are request headers set by reverse proxies on top of the
// client-IP headers that proxytrust.HasClientIPHeader checks, in canonical form
// so they can be looked up in http.Header directly.
var proxyOnlyHeaders = []string{
	http.CanonicalHeaderKey(echo.HeaderXForwardedProto),
	http.CanonicalHeaderKey(echo.HeaderXForwardedProtocol),
	http.CanonicalHeaderKey(echo.HeaderXForwardedSsl),
	http.CanonicalHeaderKey(echo.HeaderXUrlScheme),
	http.CanonicalHeaderKey(headerXForwardedHost),
	http.CanonicalHeaderKey(headerXForwardedPort),
	http.CanonicalHeaderKey(headerXForwardedPrefix),
	http.CanonicalHeaderKey(headerXIngressPath),
	http.CanonicalHeaderKey(headerVia),
}

// hasProxyHeader reports whether h carries any reverse-proxy header, even with
// an empty value: a request carrying one did not come straight from the client.
// Client-IP headers are checked by proxytrust so the auth path and the cookie
// path agree on what counts as proxied.
func hasProxyHeader(h http.Header) bool {
	if proxytrust.HasClientIPHeader(h) {
		return true
	}
	for _, key := range proxyOnlyHeaders {
		if len(h[key]) > 0 {
			return true
		}
	}
	return false
}

// plainHTTPClientSessionStore wraps the session store so that a session loaded
// for a client listed in Security.PlainHTTPSessionClients, connecting directly
// over plain HTTP, does not carry the Secure attribute: such a client (for
// example another container calling http://birdnet-go:8080 while browsers come
// in through an HTTPS reverse proxy) would otherwise never send the cookie
// back. Every other request keeps the store-wide Secure setting.
//
// The adjustment is made when the session is loaded in Get and New, because
// Session.Save writes the cookie through the wrapped store using the session's
// own copy of the options.
type plainHTTPClientSessionStore struct {
	sessions.Store
	// settings is the fallback for conf.CurrentOrFallback; the client list is
	// read from the live snapshot on every request so edits hot-reload.
	settings *conf.Settings
}

// Get returns the named session for r with Secure adjusted to how r arrived.
func (s plainHTTPClientSessionStore) Get(r *http.Request, name string) (*sessions.Session, error) {
	session, err := s.Store.Get(r, name)
	s.clearSecureForPlainHTTPClient(r, session)
	return session, err
}

// New returns a new session for r with Secure adjusted to how r arrived.
func (s plainHTTPClientSessionStore) New(r *http.Request, name string) (*sessions.Session, error) {
	session, err := s.Store.New(r, name)
	s.clearSecureForPlainHTTPClient(r, session)
	return session, err
}

// clearSecureForPlainHTTPClient drops the Secure attribute from session when r
// comes from a configured plain-HTTP session client. Each session holds its own
// copy of the store options, so other requests are not affected.
func (s plainHTTPClientSessionStore) clearSecureForPlainHTTPClient(r *http.Request, session *sessions.Session) {
	if session == nil || session.Options == nil || !session.Options.Secure {
		return
	}
	settings := conf.CurrentOrFallback(s.settings)
	if settings == nil || !isPlainHTTPSessionClient(r, settings.Security.PlainHTTPSessionClients) {
		return
	}
	session.Options.Secure = false
}

// isPlainHTTPSessionClient reports whether r arrived over a plain-HTTP
// connection, without reverse-proxy headers, from a peer address equal to one
// of clients (IP addresses). The peer address comes from the connection, never
// from request headers, so a client cannot claim to be on the list. A request
// with proxy headers keeps Secure even from a listed address, since it is
// relaying traffic for someone else.
func isPlainHTTPSessionClient(r *http.Request, clients []string) bool {
	if len(clients) == 0 || r == nil || r.TLS != nil {
		return false
	}
	if hasProxyHeader(r.Header) {
		return false
	}
	peer, ok := connectionPeerAddr(r)
	if !ok {
		return false
	}
	for _, entry := range clients {
		if client, ok := parseClientAddr(strings.TrimSpace(entry)); ok && client == peer {
			return true
		}
	}
	return false
}

// connectionPeerAddr returns the IP address of the connection peer from
// r.RemoteAddr, with any zone dropped and IPv4-mapped IPv6 unmapped. It reports
// false for addresses that are not IP:port, such as unix-socket peers.
func connectionPeerAddr(r *http.Request) (netip.Addr, bool) {
	host, _, err := net.SplitHostPort(r.RemoteAddr)
	if err != nil {
		return netip.Addr{}, false
	}
	return parseClientAddr(host)
}

// parseClientAddr parses s as an IP address with any zone dropped and
// IPv4-mapped IPv6 unmapped, so a peer and a list entry for the same host
// compare equal whether or not either is written in IPv4-mapped form. Peer
// addresses may carry a zone; list entries cannot, since config validation
// (net.ParseIP) rejects zoned entries, as it rejects ranges. It reports false
// for anything that is not a single IP address.
func parseClientAddr(s string) (netip.Addr, bool) {
	addr, err := netip.ParseAddr(s)
	if err != nil {
		return netip.Addr{}, false
	}
	return addr.WithZone("").Unmap(), true
}

// unwrapSessionStore returns the store wrapped by plainHTTPClientSessionStore,
// or store itself when it is not wrapped.
func unwrapSessionStore(store sessions.Store) sessions.Store {
	if wrapped, ok := store.(plainHTTPClientSessionStore); ok {
		return wrapped.Store
	}
	return store
}
