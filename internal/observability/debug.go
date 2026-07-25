// Package observability provides tools for monitoring and debugging the BirdNET-Go application.
package observability

import (
	"io"
	"net/http"
)

// debugRootPath is the exact old pprof path; debugPath is its subtree form.
//
// Both are registered. The subtree pattern alone would leave a request for the
// unslashed path answered by ServeMux's own redirect, so a client that does not
// follow redirects (plain curl, a scripted probe) would get a bare 3xx instead
// of the explanation this handler exists to give.
const (
	debugRootPath = "/debug/pprof"
	debugPath     = debugRootPath + "/"
)

// movedNotice is the body returned at the old pprof location.
//
// It is deliberately terse. This listener has no authentication and no rate
// limiting, so the notice points at the configuration section and stops there:
// naming the credential mechanism would hand a scanner both the parameter to
// brute-force and the port shape to aim at, for no benefit to the operator,
// who has the documentation. Pointing at the setting is enough to reconnect
// someone whose profiling workflow just broke, which is the entire job here.
const movedNotice = `The pprof endpoints have moved off the telemetry listener.

They are now served by the main web server and are configured under the
"diagnostics.profiling" section. See the profiling documentation for details.

The telemetry listener serves Prometheus metrics only.
`

// RegisterMovedDebugHandler registers a 410 Gone breadcrumb at the old pprof
// location on the telemetry mux.
//
// The pprof handlers used to be registered here on a bare ServeMux with no
// middleware of any kind, so enabling Prometheus metrics also published
// profiling on every interface with no authentication. They now live on the
// authenticated web server instead. Removing them outright would turn every
// existing profiling workflow into a connection error with nothing to go on.
//
// What it discloses, stated plainly rather than claimed away: any unauthenticated
// caller on this port learns that this is a BirdNET-Go instance and that its
// profiling endpoints moved. It discloses no profile data, no credential, and
// no instance-specific state; the response is a compile-time constant and no
// request data reaches it. That is judged worth the support cost of an
// unexplained break, on a port the operator opted into exposing.
//
// Note this is registered whether or not profiling is enabled, which is a
// weaker stance than the web-server gate's 404. That is deliberate: the
// breadcrumb answers "where did the old endpoint go", a question whose answer
// does not depend on the current setting.
func RegisterMovedDebugHandler(mux *http.ServeMux) {
	handler := func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "text/plain; charset=utf-8")
		w.Header().Set("X-Content-Type-Options", "nosniff")
		w.WriteHeader(http.StatusGone)
		// io.WriteString rather than Write([]byte(const)): this handler is
		// reachable unauthenticated and unthrottled, so the per-request copy of
		// the constant would be attacker-paced for no reason.
		_, _ = io.WriteString(w, movedNotice)
	}
	mux.HandleFunc(debugRootPath, handler)
	mux.HandleFunc(debugPath, handler)
}
