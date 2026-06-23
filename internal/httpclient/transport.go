package httpclient

import "net/http"

// CloneDefaultTransport returns a clone of http.DefaultTransport for callers
// that need to customize transport settings without losing proxy support, dial
// timeouts, and other production defaults (per golang/go#26013).
//
// If DefaultTransport has been replaced by a non-*http.Transport RoundTripper
// (e.g. by APM or tracing middleware), this falls back to a plain
// &http.Transport{} to avoid a runtime panic.
func CloneDefaultTransport() *http.Transport {
	if t, ok := http.DefaultTransport.(*http.Transport); ok {
		return t.Clone()
	}
	return &http.Transport{}
}
