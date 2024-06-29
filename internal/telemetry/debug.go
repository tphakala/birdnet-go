// Package telemetry provides tools for monitoring and debugging the BirdNET-Go application.
package telemetry

import (
	"net/http"
	"net/http/pprof"
)

// debugPath is the base URL path for pprof debugging endpoints.
const debugPath = "/debug/pprof/"

// RegisterDebugHandlers adds pprof debugging routes to the provided http.ServeMux.
//
// This function registers various pprof handlers for profiling and debugging purposes.
// It should only be used in development or controlled environments, as it can expose
// sensitive information about the application's internals.
//
// The following endpoints are registered:
//   - /debug/pprof/
//   - /debug/pprof/cmdline
//   - /debug/pprof/profile
//   - /debug/pprof/symbol
//   - /debug/pprof/trace
//   - /debug/pprof/allocs
//   - /debug/pprof/goroutine
//   - /debug/pprof/heap
//   - /debug/pprof/threadcreate
//   - /debug/pprof/block
//   - /debug/pprof/mutex
//
// Parameters:
//   - mux: The http.ServeMux to which the debug handlers will be added.
func RegisterDebugHandlers(mux *http.ServeMux) {
	mux.HandleFunc(debugPath, pprof.Index)
	mux.HandleFunc(debugPath+"cmdline", pprof.Cmdline)
	mux.HandleFunc(debugPath+"profile", pprof.Profile)
	mux.HandleFunc(debugPath+"symbol", pprof.Symbol)
	mux.HandleFunc(debugPath+"trace", pprof.Trace)
	mux.Handle(debugPath+"allocs", pprof.Handler("allocs"))
	mux.Handle(debugPath+"goroutine", pprof.Handler("goroutine"))
	mux.Handle(debugPath+"heap", pprof.Handler("heap"))
	mux.Handle(debugPath+"threadcreate", pprof.Handler("threadcreate"))
	mux.Handle(debugPath+"block", pprof.Handler("block"))
	mux.Handle(debugPath+"mutex", pprof.Handler("mutex"))
}
