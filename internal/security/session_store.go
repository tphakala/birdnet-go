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

// proxyHeaders are request headers set by reverse proxies, in canonical form so
// they can be looked up in http.Header directly. A request carrying any of them
// did not come straight from the client.
var proxyHeaders = []string{
	http.CanonicalHeaderKey(headerForwarded),
	http.CanonicalHeaderKey(echo.HeaderXForwardedFor),
	http.CanonicalHeaderKey(echo.HeaderXForwardedProto),
	http.CanonicalHeaderKey(echo.HeaderXForwardedProtocol),
	http.CanonicalHeaderKey(echo.HeaderXForwardedSsl),
	http.CanonicalHeaderKey(echo.HeaderXUrlScheme),
	http.CanonicalHeaderKey(echo.HeaderXRealIP),
	http.CanonicalHeaderKey(headerCFConnectingIP),
}

// requestSchemeSessionStore wraps the session store so that a session loaded
// for a direct plain-HTTP request does not carry the Secure attribute: such a
// client (for example another container calling http://birdnet-go:8080 while
// browsers come in through an HTTPS reverse proxy) would otherwise never send
// the cookie back. The store-wide options decide Secure for every other
// request, including everything that arrives through a proxy.
//
// The adjustment is made when the session is loaded in Get and New, because
// Session.Save writes the cookie through the wrapped store using the session's
// own copy of the options.
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
// connection and carries no reverse-proxy headers. Unlike the scheme checks in
// the API layer, it does not ask which peers are trusted to report the scheme:
// any proxy header keeps the configured Secure attribute, so a proxy on an
// untrusted address cannot cause Secure to be dropped for browsers behind it.
func isDirectPlainHTTPRequest(r *http.Request) bool {
	if r == nil || r.TLS != nil {
		return false
	}
	for _, header := range proxyHeaders {
		if values := r.Header[header]; len(values) > 0 && values[0] != "" {
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
