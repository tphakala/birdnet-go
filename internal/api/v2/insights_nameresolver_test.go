package api

import (
	"testing"

	"github.com/labstack/echo/v4"
	"github.com/stretchr/testify/assert"
	"github.com/tphakala/birdnet-go/internal/api/v2/apicore"
	"github.com/tphakala/birdnet-go/internal/datastore"
	"github.com/tphakala/birdnet-go/internal/speciesindex"
)

// newNameMapController builds a bare Controller with an initialized species-name
// index, for exercising the facade name-map accessors without spinning up the full
// facade. The underlying builder itself is golden-tested in internal/speciesindex.
func newNameMapController(t *testing.T) *Controller {
	t.Helper()
	e := echo.New()
	c := &Controller{Core: &apicore.Core{Group: e.Group("/api/v2")}}
	c.names = speciesindex.New(nil)
	c.ownsNames = true
	return c
}

// seedNames drives the facade's own name service directly, standing in for the
// removed UpdateCommonNameMap/SetNameResolver: it installs the resolver (a nil or
// typed-nil resolver is ignored, matching production) and rebuilds the maps from
// labels at the current-settings locale. Used by facade tests that exercise the
// name-map accessors without the orchestrator that normally owns the service.
func seedNames(t *testing.T, c *Controller, r datastore.SpeciesNameResolver, labels []string) {
	t.Helper()
	if !datastore.IsNilResolver(r) {
		c.names.SetResolver(r)
	}
	locale := ""
	if s := c.ControllerSettings(); s != nil {
		locale = s.BirdNET.Locale
	}
	c.names.Rebuild(labels, locale)
}

// TestWithSpeciesIndex_SharesInjectedService verifies the WithSpeciesIndex option
// makes the facade read the orchestrator-owned shared service (ownsNames false),
// and that the facade never rebuilds a service it does not own.
func TestWithSpeciesIndex_SharesInjectedService(t *testing.T) {
	t.Parallel()

	shared := speciesindex.New(nil)
	shared.Rebuild([]string{"Turdus merula_Blackbird"}, "")

	c := newNameMapController(t) // starts owning its own empty service
	WithSpeciesIndex(shared)(c)

	assert.Same(t, shared, c.names, "the injected service must replace the facade's own")
	assert.False(t, c.ownsNames, "the facade must not own an injected service")
	assert.Equal(t, "Blackbird", c.loadCommonNameMap()["Turdus merula"], "accessors read the injected snapshot")

	// seedFallbackNames is a no-op on a non-owned service: the orchestrator is its
	// only writer, so the published snapshot pointer must not change.
	before := c.names.Snapshot()
	c.seedFallbackNames([]string{"Parus major_Great Tit"})
	assert.Same(t, before, c.names.Snapshot(), "seedFallbackNames must not rebuild an injected service")

	// A nil injection leaves the facade's own service and ownership in place.
	own := newNameMapController(t)
	ownService := own.names
	WithSpeciesIndex(nil)(own)
	assert.Same(t, ownService, own.names)
	assert.True(t, own.ownsNames)
}

// TestSeedFallbackNames_OwnedServiceRebuilds exercises the active (owned-service)
// branch of seedFallbackNames directly, the path initInsightsRoutes uses to seed a
// facade that was never handed the orchestrator's shared index.
func TestSeedFallbackNames_OwnedServiceRebuilds(t *testing.T) {
	t.Parallel()

	c := newNameMapController(t) // ownsNames == true
	c.seedFallbackNames([]string{"Turdus merula_Common Blackbird"})
	assert.Equal(t, "Common Blackbird", c.loadCommonNameMap()["Turdus merula"],
		"seedFallbackNames must rebuild a facade-owned index from labels")
}
