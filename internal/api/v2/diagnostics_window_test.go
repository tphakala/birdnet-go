package api

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/labstack/echo/v4"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/tphakala/birdnet-go/internal/health"
	"github.com/tphakala/birdnet-go/internal/health/checks"
	"github.com/tphakala/birdnet-go/internal/observability"
)

// setupDiagnosticsTest creates a test Controller wired for diagnostics
// tests with the given health metrics store and optional event buffer.
func setupDiagnosticsTest(t *testing.T, store *observability.HealthMetricsStore, eventBuf *observability.HealthEventBuffer) (*echo.Echo, *Controller) {
	t.Helper()
	e, _, controller := setupTestEnvironment(t)
	controller.healthMetricsStore = store
	if eventBuf != nil {
		controller.healthEvents = eventBuf
	} else {
		controller.healthEvents = observability.NewHealthEventBuffer(100)
	}
	controller.healthReports = health.NewReportStore(10)
	controller.healthRegistry = health.NewRegistry()
	return e, controller
}

func TestParseWindow(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name    string
		input   string
		want    time.Duration
		wantErr bool
	}{
		{name: "empty defaults to 1h", input: "", want: time.Hour},
		{name: "15m", input: "15m", want: 15 * time.Minute},
		{name: "30m", input: "30m", want: 30 * time.Minute},
		{name: "1h", input: "1h", want: time.Hour},
		{name: "6h", input: "6h", want: 6 * time.Hour},
		{name: "24h", input: "24h", want: 24 * time.Hour},
		{name: "7d", input: "7d", want: 7 * 24 * time.Hour},
		{name: "invalid value", input: "2h", wantErr: true},
		{name: "garbage", input: "abc", wantErr: true},
		{name: "numeric only", input: "60", wantErr: true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			got, err := parseWindow(tt.input)
			if tt.wantErr {
				assert.Error(t, err)
				return
			}
			require.NoError(t, err)
			assert.Equal(t, tt.want, got)
		})
	}
}

func TestRunDiagnostics_InvalidWindow(t *testing.T) {
	t.Parallel()

	e, controller := setupDiagnosticsTest(t, observability.NewHealthMetricsStore(), nil)

	req := httptest.NewRequest(http.MethodPost, "/api/v2/system/diagnostics/run?window=invalid", http.NoBody)
	rec := httptest.NewRecorder()
	c := e.NewContext(req, rec)

	err := controller.RunDiagnostics(c)
	require.NoError(t, err)
	assert.Equal(t, http.StatusBadRequest, rec.Code)
}

func TestRunDiagnostics_WindowedChecksIncludeDetails(t *testing.T) {
	t.Parallel()

	store := observability.NewHealthMetricsStore()
	eventBuf := observability.NewHealthEventBuffer(100)

	now := time.Now()
	store.RecordAt("audio.drops.src1", 15, now)
	eventBuf.Add(observability.HealthEvent{
		Time: now, Source: "src1", Delta: 15, Metric: "drops",
	})

	e, controller := setupDiagnosticsTest(t, store, eventBuf)
	controller.healthRegistry.RegisterAll(
		checks.NewBufferDropsCheck(store, eventBuf.Recent),
		checks.NewBufferOverrunCheck(store, eventBuf.Recent),
		checks.NewStreamErrorRateCheck(store, eventBuf.Recent),
	)

	req := httptest.NewRequest(http.MethodPost, "/api/v2/system/diagnostics/run?window=1h", http.NoBody)
	rec := httptest.NewRecorder()
	c := e.NewContext(req, rec)

	err := controller.RunDiagnostics(c)
	require.NoError(t, err)
	assert.Equal(t, http.StatusOK, rec.Code)

	var report health.DiagnosticsReport
	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &report))

	assert.NotEmpty(t, report.ID)
	assert.NotZero(t, report.StartedAt)
	require.Len(t, report.Results, 3)

	dropsResult := findResult(t, report.Results, "buffer_drops")
	require.NotNil(t, dropsResult.Details)
	assert.Equal(t, "1h", dropsResult.Details["window"])
	assert.NotNil(t, dropsResult.Details["sparkline"], "sparkline missing from buffer_drops")
	assert.NotNil(t, dropsResult.Details["recent_events"], "recent_events missing from buffer_drops")
	assert.Contains(t, dropsResult.Details, "active_hours")
	assert.Contains(t, dropsResult.Details, "velocity")
	assert.Contains(t, dropsResult.Details, "pattern")
	assert.Contains(t, dropsResult.Details, "lifetime_total")
	assert.Contains(t, dropsResult.Details, "per_source")
}

func TestRunDiagnostics_DefaultWindowIs1h(t *testing.T) {
	t.Parallel()

	store := observability.NewHealthMetricsStore()
	store.RecordAt("audio.drops.src1", 5, time.Now())

	e, controller := setupDiagnosticsTest(t, store, nil)
	controller.healthRegistry.Register(checks.NewBufferDropsCheck(store, nil))

	req := httptest.NewRequest(http.MethodPost, "/api/v2/system/diagnostics/run", http.NoBody)
	rec := httptest.NewRecorder()
	c := e.NewContext(req, rec)

	err := controller.RunDiagnostics(c)
	require.NoError(t, err)
	assert.Equal(t, http.StatusOK, rec.Code)

	var report health.DiagnosticsReport
	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &report))

	dropsResult := findResult(t, report.Results, "buffer_drops")
	require.NotNil(t, dropsResult.Details)
	assert.Equal(t, "1h", dropsResult.Details["window"])
}

func TestRunDiagnostics_WindowAffectsEvaluation(t *testing.T) {
	t.Parallel()

	store := observability.NewHealthMetricsStore()
	store.RecordAt("audio.drops.src1", 20, time.Now().Add(-3*time.Hour))
	store.RecordAt("audio.drops.src1", 0, time.Now())

	e, controller := setupDiagnosticsTest(t, store, nil)
	controller.healthRegistry.Register(checks.NewBufferDropsCheck(store, nil))

	runWithWindow := func(window string) health.DiagnosticsReport {
		path := "/api/v2/system/diagnostics/run"
		if window != "" {
			path += "?window=" + window
		}
		req := httptest.NewRequest(http.MethodPost, path, http.NoBody)
		rec := httptest.NewRecorder()
		c := e.NewContext(req, rec)
		require.NoError(t, controller.RunDiagnostics(c))
		require.Equal(t, http.StatusOK, rec.Code)
		var report health.DiagnosticsReport
		require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &report))
		return report
	}

	narrow := runWithWindow("1h")
	dropsNarrow := findResult(t, narrow.Results, "buffer_drops")
	assert.Equal(t, health.StatusHealthy, dropsNarrow.Status,
		"1h window should be healthy (drops were 3h ago)")

	wide := runWithWindow("6h")
	dropsWide := findResult(t, wide.Results, "buffer_drops")
	assert.Equal(t, health.StatusWarning, dropsWide.Status,
		"6h window should be warning (captures the 20 drops from 3h ago)")
	assert.Equal(t, "6h", dropsWide.Details["window"])
}

func TestRunDiagnostics_NonCounterChecksUnchanged(t *testing.T) {
	t.Parallel()

	store := observability.NewHealthMetricsStore()
	e, controller := setupDiagnosticsTest(t, store, nil)
	controller.healthRegistry.RegisterAll(
		checks.NewMemoryCheck(),
		checks.NewBufferDropsCheck(store, nil),
	)

	req := httptest.NewRequest(http.MethodPost, "/api/v2/system/diagnostics/run?window=6h", http.NoBody)
	rec := httptest.NewRecorder()
	c := e.NewContext(req, rec)

	require.NoError(t, controller.RunDiagnostics(c))
	assert.Equal(t, http.StatusOK, rec.Code)

	var report health.DiagnosticsReport
	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &report))

	memResult := findResult(t, report.Results, "memory")
	assert.NotEqual(t, health.StatusUnknown, memResult.Status)
	assert.Nil(t, memResult.Details["sparkline"], "non-counter check should not have sparkline")
	assert.Nil(t, memResult.Details["window"], "non-counter check should not have window field")
}

func TestRunDiagnostics_AllValidWindowPresets(t *testing.T) {
	t.Parallel()

	presets := []struct {
		param    string
		expected string
	}{
		{"15m", "15m"},
		{"30m", "30m"},
		{"1h", "1h"},
		{"6h", "6h"},
		{"24h", "1d"},
		{"7d", "7d"},
	}

	for _, preset := range presets {
		t.Run(preset.param, func(t *testing.T) {
			t.Parallel()

			store := observability.NewHealthMetricsStore()
			store.RecordAt("audio.drops.src1", 5, time.Now())

			e, controller := setupDiagnosticsTest(t, store, nil)
			controller.healthRegistry.Register(checks.NewBufferDropsCheck(store, nil))

			req := httptest.NewRequest(http.MethodPost,
				"/api/v2/system/diagnostics/run?window="+preset.param, http.NoBody)
			rec := httptest.NewRecorder()
			c := e.NewContext(req, rec)

			require.NoError(t, controller.RunDiagnostics(c))
			assert.Equal(t, http.StatusOK, rec.Code)

			var report health.DiagnosticsReport
			require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &report))

			dropsResult := findResult(t, report.Results, "buffer_drops")
			require.NotNil(t, dropsResult.Details)
			assert.Equal(t, preset.expected, dropsResult.Details["window"])
		})
	}
}

func TestRunDiagnostics_SparklineStructure(t *testing.T) {
	t.Parallel()

	store := observability.NewHealthMetricsStore()
	now := time.Now()
	store.RecordAt("audio.drops.src1", 10, now.Add(-2*time.Hour))
	store.RecordAt("audio.drops.src1", 5, now)

	e, controller := setupDiagnosticsTest(t, store, nil)
	controller.healthRegistry.Register(checks.NewBufferDropsCheck(store, nil))

	req := httptest.NewRequest(http.MethodPost, "/api/v2/system/diagnostics/run", http.NoBody)
	rec := httptest.NewRecorder()
	c := e.NewContext(req, rec)

	require.NoError(t, controller.RunDiagnostics(c))
	assert.Equal(t, http.StatusOK, rec.Code)

	var report health.DiagnosticsReport
	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &report))

	dropsResult := findResult(t, report.Results, "buffer_drops")
	require.NotNil(t, dropsResult.Details)

	sparklineRaw, ok := dropsResult.Details["sparkline"]
	require.True(t, ok, "sparkline must be present")

	sparkline, ok := sparklineRaw.([]any)
	require.True(t, ok, "sparkline must be an array")
	require.Len(t, sparkline, 24, "sparkline should have 24 hourly buckets")

	bucket, ok := sparkline[0].(map[string]any)
	require.True(t, ok, "each sparkline bucket must be an object")
	assert.Contains(t, bucket, "t", "bucket must have timestamp field 't'")
	assert.Contains(t, bucket, "v", "bucket must have value field 'v'")
}

func TestRunDiagnostics_RecentEventsStructure(t *testing.T) {
	t.Parallel()

	store := observability.NewHealthMetricsStore()
	eventBuf := observability.NewHealthEventBuffer(100)

	now := time.Now()
	store.RecordAt("audio.drops.src1", 10, now)
	eventBuf.Add(observability.HealthEvent{
		Time: now, Source: "src1", Delta: 10, Metric: "drops",
	})

	e, controller := setupDiagnosticsTest(t, store, eventBuf)
	controller.healthRegistry.Register(checks.NewBufferDropsCheck(store, eventBuf.Recent))

	req := httptest.NewRequest(http.MethodPost, "/api/v2/system/diagnostics/run", http.NoBody)
	rec := httptest.NewRecorder()
	c := e.NewContext(req, rec)

	require.NoError(t, controller.RunDiagnostics(c))
	assert.Equal(t, http.StatusOK, rec.Code)

	var report health.DiagnosticsReport
	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &report))

	dropsResult := findResult(t, report.Results, "buffer_drops")
	eventsRaw, ok := dropsResult.Details["recent_events"]
	require.True(t, ok, "recent_events must be present")

	events, ok := eventsRaw.([]any)
	require.True(t, ok, "recent_events must be an array")
	require.NotEmpty(t, events)

	event, ok := events[0].(map[string]any)
	require.True(t, ok, "each event must be an object")
	assert.Contains(t, event, "time")
	assert.Contains(t, event, "source")
	assert.Contains(t, event, "delta")
	assert.Contains(t, event, "metric")
}

// findResult locates a result by check name or fails the test.
func findResult(t *testing.T, results []health.Result, name string) health.Result {
	t.Helper()
	for _, r := range results {
		if r.Name == name {
			return r
		}
	}
	t.Fatalf("result %q not found in %d results", name, len(results))
	return health.Result{}
}
