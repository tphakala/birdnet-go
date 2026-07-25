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
func TestTelemetryMuxServesNoProfiles(t *testing.T) {
	t.Parallel()

	mux := http.NewServeMux()
	RegisterMovedDebugHandler(mux)

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
		t.Run(path, func(t *testing.T) {
			t.Parallel()

			rec := httptest.NewRecorder()
			mux.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, path, http.NoBody))

			assert.Equal(t, http.StatusGone, rec.Code,
				"the telemetry listener must not serve profiling data")
			assert.NotContains(t, rec.Body.String(), "runtime.",
				"a 410 body must not carry profile content")
		})
	}
}

// TestMovedDebugHandlerExplainsItself checks the breadcrumb actually earns its
// place. A bare 410 would be no better than a connection error for someone whose
// profiling workflow just stopped working, so the body has to name both the new
// location and the setting that opens it.
func TestMovedDebugHandlerExplainsItself(t *testing.T) {
	t.Parallel()

	mux := http.NewServeMux()
	RegisterMovedDebugHandler(mux)

	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, debugPath, http.NoBody))

	require.Equal(t, http.StatusGone, rec.Code)

	body := rec.Body.String()
	assert.Contains(t, body, "diagnostics.profiling.enabled", "the body must name the setting")
	assert.Contains(t, body, debugPath, "the body must name the new path")
	assert.Contains(t, body, "diagnostics.profiling.token", "the body must explain the no-auth case")
	assert.True(t, strings.HasPrefix(rec.Header().Get("Content-Type"), "text/plain"),
		"the notice is plain text, not something a browser should render as markup")
	assert.Equal(t, "nosniff", rec.Header().Get("X-Content-Type-Options"))
}

// TestTelemetryMuxStillServesMetrics confirms removing the profiling handlers
// left the listener's actual job intact.
func TestTelemetryMuxStillServesMetrics(t *testing.T) {
	t.Parallel()

	metrics, err := NewMetrics()
	require.NoError(t, err)

	mux := http.NewServeMux()
	metrics.RegisterHandlers(mux)
	RegisterMovedDebugHandler(mux)

	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/metrics", http.NoBody))

	assert.Equal(t, http.StatusOK, rec.Code)
	assert.NotEmpty(t, rec.Body.Bytes())
}
