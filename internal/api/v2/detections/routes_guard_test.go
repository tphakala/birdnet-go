package detections

import (
	"testing"

	"github.com/labstack/echo/v4"
	"github.com/stretchr/testify/assert"

	"github.com/tphakala/birdnet-go/internal/api/v2/apicore"
)

// TestRegisterDetectionRoutesSkippedWhenDatastoreDisabled pins the datastore-disabled
// guard: when DS is nil (the "datastore disabled" mode the facade constructor
// permits), RegisterDetectionRoutes registers no /detections routes instead of
// wiring handlers that would dereference a nil datastore. This is the guard the
// facade's old initDetectionRoutes carried; it moved verbatim into the handler.
func TestRegisterDetectionRoutesSkippedWhenDatastoreDisabled(t *testing.T) {
	t.Parallel()

	e := echo.New()
	h := &Handler{Core: &apicore.Core{Echo: e, Group: e.Group("/api/v2"), DS: nil}}

	h.RegisterDetectionRoutes(h.Group)

	for _, r := range e.Routes() {
		assert.NotContains(t, r.Path, "/detections",
			"detection routes must not register when the datastore is disabled: %s %s", r.Method, r.Path)
	}
}

// TestRegisterSearchRoutesSkippedWhenDatastoreDisabled pins the datastore-disabled
// guard for the search domain: when DS is nil (the "datastore disabled" mode the
// facade constructor permits), RegisterSearchRoutes registers no /search route
// instead of wiring HandleSearch, which dereferences a nil datastore via
// SearchDetections and would panic on the first request. This mirrors
// TestRegisterDetectionRoutesSkippedWhenDatastoreDisabled.
func TestRegisterSearchRoutesSkippedWhenDatastoreDisabled(t *testing.T) {
	t.Parallel()

	e := echo.New()
	h := &Handler{Core: &apicore.Core{Echo: e, Group: e.Group("/api/v2"), DS: nil}}

	// Must not panic and must not register any /search route.
	assert.NotPanics(t, func() {
		h.RegisterSearchRoutes(h.Group)
	}, "RegisterSearchRoutes must not panic when the datastore is disabled")

	for _, r := range e.Routes() {
		assert.NotContains(t, r.Path, "/search",
			"search routes must not register when the datastore is disabled: %s %s", r.Method, r.Path)
	}
}
