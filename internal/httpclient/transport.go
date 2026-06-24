package httpclient

import "net/http"

// CloneDefaultTransport returns a clone of http.DefaultTransport for callers
// that need to customize transport settings without losing proxy support, dial
// timeouts, and other production defaults (per golang/go#26013).
//
// Panics if DefaultTransport has been replaced by a non-*http.Transport
// RoundTripper — this indicates a fundamental change to process HTTP setup
// that callers need to handle explicitly rather than silently degrade.
func CloneDefaultTransport() *http.Transport {
	t, ok := http.DefaultTransport.(*http.Transport)
	if !ok {
		panic("httpclient: http.DefaultTransport is not *http.Transport; cannot clone")
	}
	return t.Clone()
}
