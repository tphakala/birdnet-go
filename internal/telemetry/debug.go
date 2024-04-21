// debug.go: pprof debug routes and helpers for telemetry package
package telemetry

import (
	"net/http"
	"net/http/pprof"
)

const debugPath = "/debug/pprof/"

// RegisterDebugHandlers adds pprof debugging routes to the provided mux
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
