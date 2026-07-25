// Package observability provides tools for monitoring and debugging the BirdNET-Go application.
package observability

import (
	"net/http"
)

// debugPath is the base URL path that used to serve the pprof debugging
// endpoints on this listener.
const debugPath = "/debug/pprof/"

// movedNotice is the body returned at the old pprof location. It names the new
// path and the setting that opens it, so an existing profiling workflow gets an
// answer instead of a bare status code.
const movedNotice = `The pprof endpoints have moved off the telemetry listener.

They are now served by the main web server at /debug/pprof/, gated by the
diagnostics.profiling.enabled setting and placed behind the web server's
authentication. When no authentication provider is configured, pass the
generated diagnostics.profiling.token as a query parameter:

    go tool pprof "http://<host>:<webserver-port>/debug/pprof/heap?token=<token>"

The telemetry listener serves Prometheus metrics only.
`

// RegisterMovedDebugHandler registers a 410 Gone breadcrumb at the old pprof
// location on the telemetry mux.
//
// The pprof handlers used to be registered here on a bare ServeMux with no
// middleware of any kind, so enabling Prometheus metrics also published
// profiling on every interface with no authentication. They now live on the
// authenticated web server instead. Removing them outright would turn every
// existing profiling workflow into a connection error with nothing to go on;
// this handler costs nothing, discloses nothing, and explains itself.
func RegisterMovedDebugHandler(mux *http.ServeMux) {
	mux.HandleFunc(debugPath, func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "text/plain; charset=utf-8")
		w.Header().Set("X-Content-Type-Options", "nosniff")
		w.WriteHeader(http.StatusGone)
		_, _ = w.Write([]byte(movedNotice))
	})
}
