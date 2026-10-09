package security

import (
	"net/http"

	"github.com/gorilla/sessions"
	"github.com/labstack/echo/v4"
)

// Proxy request headers not defined by Echo.
const (
	headerForwarded      = "Forwarded" // RFC 7239
	headerCFConnectingIP = "CF-Connecting-IP"
)

// proxyHeaders are request headers set by reverse proxies. A request carrying
// any of them did not come straight from the client.
var proxyHeaders = []string{
	headerForwarded,
	echo.HeaderXForwardedFor,
	echo.HeaderXForwardedProto,
	echo.HeaderXForwardedProtocol,
	echo.HeaderXForwardedSsl,
	echo.HeaderXUrlScheme,
	echo.HeaderXRealIP,
	headerCFConnectingIP,
}

// requestSchemeSessionStore wraps the session store so that a session loaded
// for a direct plain-HTTP request does not carry the Secure attribute: such a
// client (for example another container calling http://birdnet-go:8080 while
// browsers come in through an HTTPS reverse proxy) would otherwise never send
// the cookie back. The store-wide options decide Secure for every other
// request, including everything that arrives through a proxy.
//
// The cookie is written by Session.Save, which goes straight to the wrapped
// store with the session's own copy of the options, so the adjustment is made
// when the session is loaded in Get and New.
type requestSchemeSessionStore struct {
	sessions.Store
}

// Get returns the named session for r with Secure adjusted to how r arrived.
func (s requestSchemeSessionStore) Get(r *http.Request, name string) (*sessions.Session, error) {
	session, err := s.Store.Get(r, name)
	clearSecureForDirectPlainHTTP(r, session)
	return session, err
}

// New returns a new session for r with Secure adjusted to how r arrived.
func (s requestSchemeSessionStore) New(r *http.Request, name string) (*sessions.Session, error) {
	session, err := s.Store.New(r, name)
	clearSecureForDirectPlainHTTP(r, session)
	return session, err
}

// Save writes session for r with Secure adjusted to how r arrived.
func (s requestSchemeSessionStore) Save(r *http.Request, w http.ResponseWriter, session *sessions.Session) error {
	clearSecureForDirectPlainHTTP(r, session)
	return s.Store.Save(r, w, session)
}

// clearSecureForDirectPlainHTTP drops the Secure attribute from session when r
// reached the app over plain HTTP without passing through a proxy. Each session
// holds its own copy of the store options, so other requests are not affected.
func clearSecureForDirectPlainHTTP(r *http.Request, session *sessions.Session) {
	if session == nil || session.Options == nil || !session.Options.Secure {
		return
	}
	if !isDirectPlainHTTPRequest(r) {
		return
	}
	session.Options.Secure = false
}

// isDirectPlainHTTPRequest reports whether r arrived over a plain-HTTP
// connection and carries no reverse-proxy headers.
func isDirectPlainHTTPRequest(r *http.Request) bool {
	if r == nil || r.TLS != nil {
		return false
	}
	for _, header := range proxyHeaders {
		if r.Header.Get(header) != "" {
			return false
		}
	}
	return true
}

// unwrapSessionStore returns the store wrapped by requestSchemeSessionStore, or
// store itself when it is not wrapped.
func unwrapSessionStore(store sessions.Store) sessions.Store {
	if wrapped, ok := store.(requestSchemeSessionStore); ok {
		return wrapped.Store
	}
	return store
}
