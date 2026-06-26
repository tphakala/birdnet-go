// api_datastore_guard_test.go: coverage for the nil-datastore route contract.
//
// NewWithOptions permits a nil datastore ("datastore disabled" mode) but the route
// layer used to register the detection and media route groups unconditionally and
// their handlers dereferenced c.DS with no guard, so hitting one with a nil datastore
// panicked despite the constructor advertising the mode. The fix honors the mode:
// initRoutes skips registering the DS-dependent route groups when c.DS == nil, and the
// affected handlers return 503 instead of panicking (defense in depth).
package api

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/labstack/echo/v4"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/tphakala/birdnet-go/internal/api/v2/apicore"
	"github.com/tphakala/birdnet-go/internal/api/v2/apitest"
)

// TestGetSpectrogramStatusReturns503WhenDatastoreDisabled pins that a DS-dependent
// handler returns 503 Service Unavailable instead of panicking when the controller was
// constructed without a datastore.
func TestGetSpectrogramStatusReturns503WhenDatastoreDisabled(t *testing.T) {
	// Build a fully-formed controller (metrics, logger, telemetry) then disable its
	// datastore, so the test exercises the nil-DS guard rather than tripping over an
	// otherwise-unrelated nil dependency in the 5xx error path.
	e, _, controller := setupTestEnvironment(t)
	controller.DS = nil // datastore disabled

	req := httptest.NewRequest(http.MethodGet, "/api/v2/spectrogram/123/status", http.NoBody)
	rec := httptest.NewRecorder()
	ctx := e.NewContext(req, rec)
	ctx.SetParamNames("id")
	ctx.SetParamValues("123")

	var err error
	require.NotPanics(t, func() { err = controller.GetSpectrogramStatus(ctx) })
	require.ErrorIs(t, err, apicore.ErrDatastoreUnavailable,
		"DS-dependent handler must short-circuit with the datastore-unavailable sentinel, not panic")
	assert.Equal(t, http.StatusServiceUnavailable, rec.Code,
		"DS-dependent handler must return 503 when the datastore is disabled, not panic")
}

// TestInitMediaRoutesSkippedWhenDatastoreDisabled pins that the ID-based (datastore
// dependent) media routes are not registered when the controller has no datastore, while
// the datastore-independent media routes (filename serve, species images) still register.
func TestInitMediaRoutesSkippedWhenDatastoreDisabled(t *testing.T) {
	e := echo.New()
	controller := &Controller{Core: &apicore.Core{Echo: e, Group: e.Group("/api/v2"), DS: nil}}

	controller.initMediaRoutes()

	var hasFilenameSpectrogram, hasSpeciesImage bool
	for _, r := range e.Routes() {
		// Every datastore-dependent media handler is registered under an :id parameter.
		assert.NotContains(t, r.Path, ":id",
			"ID-based media routes must not register when the datastore is disabled: %s %s", r.Method, r.Path)
		// The query-ID audio endpoint is also datastore-dependent.
		assert.NotEqual(t, "/api/v2/media/audio", r.Path,
			"the datastore-dependent query-ID audio route must not register: %s %s", r.Method, r.Path)
		switch r.Path {
		case "/api/v2/media/spectrogram/:filename":
			hasFilenameSpectrogram = true
		case "/api/v2/media/species-image":
			hasSpeciesImage = true
		}
	}
	assert.True(t, hasFilenameSpectrogram,
		"datastore-independent filename spectrogram route must still register when the datastore is disabled")
	assert.True(t, hasSpeciesImage,
		"datastore-independent species-image route must still register when the datastore is disabled")
}

// TestInitDetectionRoutesSkippedWhenDatastoreDisabled pins that the detection route group
// is not registered when the controller has no datastore.
func TestInitDetectionRoutesSkippedWhenDatastoreDisabled(t *testing.T) {
	e := echo.New()
	controller := &Controller{Core: &apicore.Core{Echo: e, Group: e.Group("/api/v2"), DS: nil}}

	controller.initDetectionRoutes()

	for _, r := range e.Routes() {
		assert.NotContains(t, r.Path, "/detections",
			"detection routes must not register when the datastore is disabled: %s %s", r.Method, r.Path)
	}
}

// TestInitDebugRoutesReadsSettingsNilSafely pins that initDebugRoutes reads the debug
// flag via the nil-safe controllerSettings() snapshot (matching requireDebugMode) instead
// of dereferencing a possibly-nil c.Settings on a standalone/test controller.
func TestInitDebugRoutesReadsSettingsNilSafely(t *testing.T) {
	withRestoredGlobalSettings(t)

	e := echo.New()
	settings := apitest.NewValidTestSettings()
	settings.Debug = false // debug mode off -> initDebugRoutes takes the skip path

	controller := &Controller{Core: &apicore.Core{Echo: e, Group: e.Group("/api/v2")}}
	// initDebugRoutes must read settings through the nil-safe controllerSettings()
	// accessor (an atomic Load), not assume a non-nil snapshot.
	controller.Settings.Store(settings)

	require.NotPanics(t, func() { controller.initDebugRoutes() },
		"initDebugRoutes must not dereference a nil settings snapshot")
	for _, r := range e.Routes() {
		assert.NotContains(t, r.Path, "/debug",
			"debug routes must not register when debug mode is off: %s %s", r.Method, r.Path)
	}
}
