// api_datastore_guard_test.go: coverage for nil-safe route initialization on a
// standalone/test controller.
//
// initDebugRoutes reads the debug flag via the nil-safe controllerSettings()
// snapshot (matching requireDebugMode) instead of dereferencing a possibly-nil
// c.Settings on a standalone/test controller. The media domain's equivalent
// nil-datastore route contract is covered in internal/api/v2/media.
package api

import (
	"testing"

	"github.com/labstack/echo/v4"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/tphakala/birdnet-go/internal/api/v2/apicore"
	"github.com/tphakala/birdnet-go/internal/api/v2/apitest"
)

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
