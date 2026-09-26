// control_monitor_telemetry_test.go guards the Prometheus telemetry endpoint's
// bind-and-serve behavior. In production the realtime telemetry endpoint
// (observability.NewEndpoint) is started from ControlMonitor.Start via
// initializeTelemetryIfEnabled, which AudioPipelineService.Start invokes with a
// non-nil *observability.Metrics; a QA defect reported the endpoint as never
// starting even though this wiring is present.
//
// These tests pin the leaf that does the work, initializeTelemetryIfEnabled:
// given telemetry enabled and non-nil metrics it must construct the endpoint
// and actually bind and serve /metrics; given telemetry disabled, or metrics
// nil, it must not start an endpoint. They exercise initializeTelemetryIfEnabled
// directly rather than the full AudioPipelineService.Start -> NewControlMonitor
// -> Start chain, so they do not by themselves pin that Start still calls the
// initializer (that call is a single line, verified structurally) nor that
// AudioPipelineService passes non-nil metrics; the end-to-end path is covered
// empirically by running the server.
package analysis

import (
	"net"
	"net/http"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/tphakala/birdnet-go/internal/conf"
	"github.com/tphakala/birdnet-go/internal/conf/conftest"
	"github.com/tphakala/birdnet-go/internal/observability"
)

// freeLoopbackAddr returns a currently-free 127.0.0.1 host:port. It binds an
// ephemeral port, records the address, and releases it so the telemetry
// endpoint can claim it. The small reuse window is acceptable for a test.
func freeLoopbackAddr(t *testing.T) string {
	t.Helper()
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	require.NoError(t, err, "reserve a free loopback port")
	addr := ln.Addr().String()
	require.NoError(t, ln.Close())
	return addr
}

// TestControlMonitor_InitializeTelemetryStartsEndpoint verifies that, with
// realtime telemetry enabled and non-nil metrics, ControlMonitor initializes
// and starts the Prometheus endpoint so /metrics is reachable. This is the wire
// path AudioPipelineService.Start drives via NewControlMonitor(..., metrics,
// ...) then ctrlMonitor.Start().
func TestControlMonitor_InitializeTelemetryStartsEndpoint(t *testing.T) {
	// Not parallel: conftest.SetTestSettings mutates package-global settings and
	// initializeTelemetryIfEnabled reads them via conf.Setting().
	prev := conf.CloneSettings(conf.GetSettings())
	t.Cleanup(func() { conftest.SetTestSettings(prev) })

	metrics, err := observability.NewMetrics()
	require.NoError(t, err, "metrics must initialize")

	addr := freeLoopbackAddr(t)
	settings := &conf.Settings{}
	settings.Realtime.Telemetry.Enabled = true
	settings.Realtime.Telemetry.Listen = addr
	conftest.SetTestSettings(settings)

	cm := &ControlMonitor{metrics: metrics}
	cm.initializeTelemetryIfEnabled()

	// The endpoint must have been constructed and started.
	require.NotNil(t, cm.telemetryEndpoint, "telemetry endpoint should be initialized when enabled")

	// Ensure the endpoint goroutine is torn down even if an assertion below
	// fails. ControlMonitor.Stop closes the quit channel and waits on the
	// endpoint WaitGroup under telemetryEndpointMutex, and is a no-op for the
	// components this test never started. Registered after Start so it runs
	// before the settings restore above (t.Cleanup is LIFO).
	t.Cleanup(cm.Stop)

	// The server binds asynchronously, so poll /metrics until it answers. A
	// successful scrape proves the endpoint is actually listening, not merely
	// constructed.
	url := "http://" + addr + "/metrics"
	client := &http.Client{Timeout: time.Second}
	require.Eventually(t, func() bool {
		resp, getErr := client.Get(url)
		if getErr != nil {
			return false
		}
		defer func() { assert.NoError(t, resp.Body.Close()) }()
		return resp.StatusCode == http.StatusOK
	}, 5*time.Second, 20*time.Millisecond, "telemetry /metrics endpoint should serve requests")
}

// TestControlMonitor_InitializeTelemetryDisabledNoEndpoint verifies the endpoint
// is not started when telemetry is disabled, so the loopback listener is never
// bound. This pins the enabled-gate behavior alongside the enabled case above.
func TestControlMonitor_InitializeTelemetryDisabledNoEndpoint(t *testing.T) {
	prev := conf.CloneSettings(conf.GetSettings())
	t.Cleanup(func() { conftest.SetTestSettings(prev) })

	metrics, err := observability.NewMetrics()
	require.NoError(t, err, "metrics must initialize")

	settings := &conf.Settings{}
	settings.Realtime.Telemetry.Enabled = false
	settings.Realtime.Telemetry.Listen = freeLoopbackAddr(t)
	conftest.SetTestSettings(settings)

	cm := &ControlMonitor{metrics: metrics}
	cm.initializeTelemetryIfEnabled()

	assert.Nil(t, cm.telemetryEndpoint, "telemetry endpoint must not start when disabled")
}

// TestControlMonitor_InitializeTelemetryNilMetricsNoEndpoint verifies that, even
// with telemetry enabled, no endpoint is started when metrics is nil:
// initializeTelemetryIfEnabled must take its metrics-nil early return rather than
// hand a nil *observability.Metrics to NewEndpoint. This pins the "drops the
// metrics argument" regression the package comment names.
func TestControlMonitor_InitializeTelemetryNilMetricsNoEndpoint(t *testing.T) {
	prev := conf.CloneSettings(conf.GetSettings())
	t.Cleanup(func() { conftest.SetTestSettings(prev) })

	settings := &conf.Settings{}
	settings.Realtime.Telemetry.Enabled = true
	settings.Realtime.Telemetry.Listen = freeLoopbackAddr(t)
	conftest.SetTestSettings(settings)

	cm := &ControlMonitor{metrics: nil}
	cm.initializeTelemetryIfEnabled()

	assert.Nil(t, cm.telemetryEndpoint, "telemetry endpoint must not start when metrics is nil")
}
