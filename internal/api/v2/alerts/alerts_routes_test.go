// alerts_routes_test.go: regression tests for two functional-correctness fixes in
// the alerts domain:
//
//  1. With the enhanced (v2) database unavailable, the alert routes must REGISTER
//     and answer 409 Conflict (not 404). Before the fix RegisterRoutes returned
//     early when V2Manager was nil, so legacy deployments 404'd instead of 409'd.
//  2. When alerting.Initialize fails, engine-dependent handlers must surface 503
//     Service Unavailable instead of a silent no-op / false "test fired" success.
package alerts

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/labstack/echo/v4"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/require"

	"github.com/tphakala/birdnet-go/internal/alerting"
	"github.com/tphakala/birdnet-go/internal/api/v2/apitest"
	"github.com/tphakala/birdnet-go/internal/datastore/v2/entities"
	"github.com/tphakala/birdnet-go/internal/datastore/v2/repository/mocks"
	"github.com/tphakala/birdnet-go/internal/logger"
	"github.com/tphakala/birdnet-go/internal/notification"
)

// passthroughMiddleware allows all requests through. The protected alert subgroup
// is created with c.AuthMiddleware, so behavioral tests that route HTTP requests
// through it need a non-nil middleware (a nil one panics when invoked at request
// time).
func passthroughMiddleware() echo.MiddlewareFunc {
	return func(next echo.HandlerFunc) echo.HandlerFunc {
		return func(c echo.Context) error { return next(c) }
	}
}

// newContextForRouteParam builds an echo.Context for a direct handler call with a
// single route parameter set, plus the recorder backing the response.
func newContextForRouteParam(t *testing.T, method, target, paramName, paramValue string) (echo.Context, *httptest.ResponseRecorder) {
	t.Helper()
	e := echo.New()
	req := httptest.NewRequest(method, target, http.NoBody)
	rec := httptest.NewRecorder()
	ctx := e.NewContext(req, rec)
	ctx.SetParamNames(paramName)
	ctx.SetParamValues(paramValue)
	return ctx, rec
}

// decodeErrorKey extracts the i18n error_key field from a JSON error response body.
func decodeErrorKey(t *testing.T, body []byte) string {
	t.Helper()
	var resp struct {
		ErrorKey string `json:"error_key"`
	}
	require.NoError(t, json.Unmarshal(body, &resp), "error response should be valid JSON")
	return resp.ErrorKey
}

// --- Bug 1: missing v2 DB must register routes and return 409, not 404 ---

// TestAlertRoutesRegisteredWithoutV2 verifies the alert routes register even when
// the enhanced v2 database is unavailable (V2Manager nil), so requests reach the
// requireV2 middleware (409) instead of echo's route-not-found stub (404).
func TestAlertRoutesRegisteredWithoutV2(t *testing.T) {
	// NOT parallel: apitest.NewCore publishes to the process-global settings snapshot.
	e := echo.New()
	core := apitest.NewCore(t, apitest.WithEcho(e))
	core.AuthMiddleware = passthroughMiddleware()
	// V2Manager stays nil and enhanced mode stays off, so alertsAvailable() is false.
	require.Nil(t, core.V2Manager, "precondition: v2 manager must be absent")

	New(core).RegisterRoutes(core.Group)

	apitest.AssertRoutesRegistered(t, e, []string{
		"GET /api/v2/alerts/schema",
		"GET /api/v2/alerts/rules",
		"GET /api/v2/alerts/rules/:id",
		"GET /api/v2/alerts/history",
		"GET /api/v2/alerts/rules/export",
		"POST /api/v2/alerts/rules",
		"PUT /api/v2/alerts/rules/:id",
		"PATCH /api/v2/alerts/rules/:id/toggle",
		"DELETE /api/v2/alerts/rules/:id",
		"POST /api/v2/alerts/rules/:id/test",
		"POST /api/v2/alerts/rules/reset-defaults",
		"POST /api/v2/alerts/rules/import",
		"DELETE /api/v2/alerts/history",
	})
}

// TestAlertRoutesReturn409WithoutV2 drives real HTTP requests and asserts every
// representative alert endpoint answers 409 (not 404) when v2 is unavailable,
// covering a public read endpoint and a protected mutating endpoint.
func TestAlertRoutesReturn409WithoutV2(t *testing.T) {
	// NOT parallel: apitest.NewCore publishes to the process-global settings snapshot.
	e := echo.New()
	core := apitest.NewCore(t, apitest.WithEcho(e))
	core.AuthMiddleware = passthroughMiddleware()
	New(core).RegisterRoutes(core.Group)

	server := httptest.NewServer(e)
	t.Cleanup(server.Close)
	client := apitest.NewTestHTTPClient(apitest.TestResponseHeaderTimeout)

	cases := []struct {
		name   string
		method string
		path   string
	}{
		{"read endpoint", http.MethodGet, "/api/v2/alerts/rules"},
		{"mutating endpoint", http.MethodPost, "/api/v2/alerts/rules"},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			// Not parallel: shared server.
			req, err := http.NewRequestWithContext(t.Context(), tc.method, server.URL+tc.path, http.NoBody)
			require.NoError(t, err)
			resp, err := client.Do(req)
			require.NoError(t, err)
			defer resp.Body.Close() //nolint:errcheck // test cleanup

			assert.Equal(t, http.StatusConflict, resp.StatusCode,
				"v2-unavailable alert routes must return 409 Conflict, not 404")

			body, err := io.ReadAll(resp.Body)
			require.NoError(t, err)
			assert.Equal(t, notification.MsgErrAlertV2Required, decodeErrorKey(t, body),
				"409 body should carry the v2-required error key")
		})
	}
}

// --- Bug 2: alerting-engine init failure must surface as 503 ---

// TestTestAlertRuleReturns503WhenEngineInitFailed verifies TestAlertRule returns
// 503 (instead of a false 200 "test fired") when alerting.Initialize failed, which
// is recorded as alertEngineErr.
func TestTestAlertRuleReturns503WhenEngineInitFailed(t *testing.T) {
	// NOT parallel: apitest.NewCore publishes to the process-global settings snapshot.
	core := apitest.NewCore(t)
	h := New(core)
	// Simulate an attempted-and-failed engine init: error recorded, engine nil.
	h.alertEngineErr = fmt.Errorf("simulated alerting engine initialization failure")

	ctx, rec := newContextForRouteParam(t, http.MethodPost, "/api/v2/alerts/rules/1/test", "id", "1")
	err := h.TestAlertRule(ctx)

	apitest.AssertControllerError(t, err, rec, http.StatusServiceUnavailable, "Alerting engine is unavailable")
	assert.Equal(t, notification.MsgErrAlertEngineUnavailable, decodeErrorKey(t, rec.Body.Bytes()),
		"503 body should carry the engine-unavailable error key")
}

// TestTestAlertRuleReturns503WhenEngineNilWithoutError covers the defensive case:
// the engine is nil but no init error was recorded (init was skipped, or a direct
// call that bypasses the requireV2 middleware). TestAlertRule must return 503 and
// must NOT nil-deref the engine or falsely report "test fired". No repo is wired,
// so the guard must also fire before any repository access.
func TestTestAlertRuleReturns503WhenEngineNilWithoutError(t *testing.T) {
	// NOT parallel: apitest.NewCore publishes to the process-global settings snapshot.
	core := apitest.NewCore(t)
	h := New(core)
	// Defensive state: engine absent AND no recorded init error, no repo wired.
	require.Nil(t, h.alertEngine)
	require.NoError(t, h.alertEngineErr)

	ctx, rec := newContextForRouteParam(t, http.MethodPost, "/api/v2/alerts/rules/1/test", "id", "1")

	var err error
	require.NotPanics(t, func() { err = h.TestAlertRule(ctx) }, "a nil engine must not panic")

	apitest.AssertControllerError(t, err, rec, http.StatusServiceUnavailable, "Alerting engine is unavailable")
	assert.Equal(t, notification.MsgErrAlertEngineUnavailable, decodeErrorKey(t, rec.Body.Bytes()),
		"503 body should carry the engine-unavailable error key")
}

// TestTestAlertRuleReturns200WithWorkingEngine is the counter-test: with a healthy
// engine (alertEngineErr nil) TestAlertRule fires the rule and returns 200.
func TestTestAlertRuleReturns200WithWorkingEngine(t *testing.T) {
	// NOT parallel: apitest.NewCore publishes to the process-global settings snapshot.
	core := apitest.NewCore(t)
	h := New(core)

	rule := &entities.AlertRule{
		ID:          1,
		Name:        "Test Rule",
		ObjectType:  "detection",
		TriggerType: "event",
		EventName:   "detection.new",
	}

	repo := mocks.NewMockAlertRuleRepository(t)
	repo.EXPECT().GetRule(mock.Anything, uint(1)).Return(rule, nil).Once()
	// TestFireRule persists a history row synchronously via the engine.
	repo.EXPECT().SaveHistory(mock.Anything, mock.Anything).Return(nil).Once()

	// A live engine over the mock repo with a no-op action; alertEngineErr stays nil.
	noopAction := func(_ *entities.AlertRule, _ *alerting.AlertEvent) {}
	h.alertRuleRepo = repo
	h.alertEngine = alerting.NewEngine(repo, noopAction, logger.Global().Module("alerts-test"), alerting.NewAlertingTelemetry())

	ctx, rec := newContextForRouteParam(t, http.MethodPost, "/api/v2/alerts/rules/1/test", "id", "1")
	err := h.TestAlertRule(ctx)

	require.NoError(t, err)
	assert.Equal(t, http.StatusOK, rec.Code)

	var resp map[string]string
	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &resp))
	assert.Equal(t, "test fired", resp["status"])
}

// TestMutatingHandlerReturns503WhenEngineInitFailed verifies an engine-dependent
// mutating handler (CreateAlertRule) also surfaces 503 when init failed, rather
// than persisting an inert rule whose change the dead engine cannot apply.
func TestMutatingHandlerReturns503WhenEngineInitFailed(t *testing.T) {
	// NOT parallel: apitest.NewCore publishes to the process-global settings snapshot.
	core := apitest.NewCore(t)
	h := New(core)
	h.alertEngineErr = fmt.Errorf("simulated alerting engine initialization failure")
	// No repo wired: the 503 guard must fire before any repository access.

	e := echo.New()
	req := httptest.NewRequest(http.MethodPost, "/api/v2/alerts/rules", http.NoBody)
	rec := httptest.NewRecorder()
	ctx := e.NewContext(req, rec)

	err := h.CreateAlertRule(ctx)

	apitest.AssertControllerError(t, err, rec, http.StatusServiceUnavailable, "Alerting engine is unavailable")
	assert.Equal(t, notification.MsgErrAlertEngineUnavailable, decodeErrorKey(t, rec.Body.Bytes()))
}
