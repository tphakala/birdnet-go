package observability

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestTelemetryMuxServesNoProfiles is the regression test for the exposure this
// change closes: the telemetry listener runs a bare ServeMux with no middleware
// of any kind, so every pprof handler registered on it was reachable, without
// authentication, on all interfaces the moment Prometheus metrics were enabled.
//
// The named profiles below are the ones that used to be registered here.
// newTelemetryMux builds the mux the telemetry listener actually serves.
//
// The tests deliberately go through Endpoint.buildMux rather than assembling an
// equivalent mux themselves. Assembling one locally is what made the earlier
// version of this file unable to fail: re-adding a single
// mux.Handle("/debug/pprof/heap", pprof.Handler("heap")) line to the listener
// republishes a real heap profile unauthenticated on every interface, and Go
// 1.22+ pattern precedence gives that exact registration priority over the
// breadcrumb's subtree pattern, so a test holding its own mux stayed green
// through the exact regression it existed to catch.
func newTelemetryMux(t *testing.T) *http.ServeMux {
	t.Helper()

	metrics, err := NewMetrics()
	require.NoError(t, err)

	return (&Endpoint{metrics: metrics}).buildMux()
}

func TestTelemetryMuxServesNoProfiles(t *testing.T) {
	t.Parallel()

	mux := newTelemetryMux(t)

	profiles := []string{
		debugPath + "cmdline",
		debugPath + "profile",
		debugPath + "symbol",
		debugPath + "trace",
		debugPath + "allocs",
		debugPath + "goroutine",
		debugPath + "heap",
		debugPath + "threadcreate",
		debugPath + "block",
		debugPath + "mutex",
	}

	for _, path := range profiles {
		t.Run(strings.TrimPrefix(path, debugPath), func(t *testing.T) {
			t.Parallel()

			rec := httptest.NewRecorder()
			mux.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, path, http.NoBody))

			assert.Equal(t, http.StatusGone, rec.Code,
				"the telemetry listener must not serve profiling data")
			// Byte-equality with the constant, not a substring probe: it pins
			// both that no profile was served AND that the handler reflects
			// nothing from the request into the body.
			assert.Equal(t, movedNotice, rec.Body.String(),
				"the only thing this path may return is the fixed notice")
		})
	}
}

// TestMovedDebugHandlerExplainsItself checks the breadcrumb actually earns its
// place. A bare 410 would be no better than a connection error for someone whose
// profiling workflow just stopped working, so the body has to name both the new
// location and the setting that opens it.
func TestMovedDebugHandlerExplainsItself(t *testing.T) {
	t.Parallel()

	mux := newTelemetryMux(t)

	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, debugPath, http.NoBody))

	require.Equal(t, http.StatusGone, rec.Code)

	body := rec.Body.String()
	assert.Contains(t, body, "diagnostics.profiling",
		"the body must name the configuration section so a broken workflow can reconnect")
	assert.True(t, strings.HasPrefix(rec.Header().Get("Content-Type"), "text/plain"),
		"the notice is plain text, not something a browser should render as markup")
	assert.Equal(t, "nosniff", rec.Header().Get("X-Content-Type-Options"))

	// This listener is unauthenticated, so the notice must stay a signpost and
	// not become a briefing. Naming the credential mechanism would hand a
	// scanner the parameter to brute-force for no operator benefit.
	assert.NotContains(t, body, "token",
		"the breadcrumb must not name the credential mechanism")
}

// TestTelemetryMuxStillServesMetrics confirms removing the profiling handlers
// left the listener's actual job intact. It goes through buildMux, so dropping
// either registration from the listener fails here.
func TestTelemetryMuxStillServesMetrics(t *testing.T) {
	t.Parallel()

	mux := newTelemetryMux(t)

	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/metrics", http.NoBody))

	assert.Equal(t, http.StatusOK, rec.Code)
	assert.NotEmpty(t, rec.Body.Bytes())
}

// TestTelemetryMuxRejectsReAddedProfileHandler is the direct guard on the
// regression this whole change exists to prevent.
//
// It does not test production code; it tests that the OTHER tests in this file
// would notice. A profile handler re-added to buildMux wins over the
// breadcrumb's subtree pattern under Go 1.22+ precedence, so this asserts that
// precedence explicitly: if a future Go release or a mux change made the
// subtree pattern win instead, the shape of the risk would have changed and
// this test says so.
func TestTelemetryMuxRejectsReAddedProfileHandler(t *testing.T) {
	t.Parallel()

	mux := newTelemetryMux(t)
	// Simulate the regression: a specific pattern alongside the subtree one.
	mux.HandleFunc(debugPath+"heap", func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte("runtime.mallocgc"))
	})

	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, debugPath+"heap", http.NoBody))

	require.Equal(t, http.StatusOK, rec.Code,
		"a specific pattern must still beat the subtree breadcrumb; if this "+
			"ever fails, the assumption behind the other tests in this file changed")
}
