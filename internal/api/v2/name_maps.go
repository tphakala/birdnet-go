// internal/api/v2/name_maps.go
//
// Facade-owned BirdNET name-map plumbing. The cached scientific<->common lookup
// maps and the authoritative name resolver live on the *Controller (the field
// names in api.go, a *speciesindex.Service). They are shared infrastructure: the
// analytics domain, the detections search resolver, the species image handler,
// and the settings exclude-list canonicalization all read them through the
// accessors below (analytics, detections, and species receive the accessors as
// injected bound-method values; settings.go calls canonicalizeExcludeList
// directly). UpdateCommonNameMap and SetNameResolver are part of the external
// surface: internal/analysis drives them through *apiv2.Controller. Keeping the
// plumbing here (rather than in a domain package) avoids any domain->domain or
// domain->facade dependency. The maps themselves are built and owned by the
// shared internal/speciesindex leaf package, so the api/v2 and datastore name
// maps stay byte-identical.
package api

import (
	"github.com/tphakala/birdnet-go/internal/api/v2/apicore"
	"github.com/tphakala/birdnet-go/internal/datastore"
	"github.com/tphakala/birdnet-go/internal/speciesindex"
)

// loadNameMaps returns the current species-name snapshot. Always returns a
// non-nil snapshot with non-nil inner maps (Empty() before the first rebuild, or
// when the service is unset on a bare-struct test), so callers index without
// guards.
func (c *Controller) loadNameMaps() *speciesindex.Snapshot {
	if c.names == nil {
		return speciesindex.Empty()
	}
	return c.names.Snapshot()
}

// loadCommonToScientificMap returns the current common-to-scientific lookup map.
// Always returns a non-nil map.
func (c *Controller) loadCommonToScientificMap() map[string]string {
	return c.loadNameMaps().CommonToSci
}

// loadCommonNameMap returns the current scientific-to-common lookup map.
// Always returns a non-nil map.
func (c *Controller) loadCommonNameMap() map[string]string {
	return c.loadNameMaps().SciToCommon
}

// loadFoldedCommonNameMap returns the current scientific-to-normalized-common
// lookup map used by substring search. Always returns a non-nil map.
func (c *Controller) loadFoldedCommonNameMap() map[string]string {
	return c.loadNameMaps().SciToCommonFolded
}

// canonicalizeExcludeList canonicalizes the species exclude list (resolve each
// entry to its scientific name, drop blanks, de-duplicate case-insensitively). It
// is a thin facade wrapper over apicore.CanonicalizeExcludeList so the settings
// save flow and the detection ignore/review handlers keep the stored list in a
// single canonical form. Returns nil for an empty/all-blank input.
func (c *Controller) canonicalizeExcludeList(exclude []string) []string {
	return apicore.CanonicalizeExcludeList(c.loadCommonToScientificMap(), exclude)
}

// UpdateCommonNameMap rebuilds the cached name maps from updated BirdNET labels,
// using the locale from the current settings (empty when settings are nil).
// Called after locale or model changes to keep insights and search endpoints
// current.
func (c *Controller) UpdateCommonNameMap(labels []string) {
	locale := ""
	if s := c.ControllerSettings(); s != nil {
		locale = s.BirdNET.Locale
	}
	c.names.Rebuild(labels, locale)
}

// SetNameResolver installs the authoritative localized name resolver, shared with
// the classifier orchestrator. A nil resolver is ignored.
func (c *Controller) SetNameResolver(r datastore.SpeciesNameResolver) {
	c.names.SetResolver(r)
}

// initInsightsRoutes seeds the facade-owned name maps and registers the analytics
// domain's insights endpoints (/insights/* and /dashboard/kpis). The insights
// repository and the route registration are owned by the analytics handler; the
// name-map seeding stays here because the name maps are facade-owned and feed the
// detections, species, and settings code paths as well as insights. It is gated on
// the enhanced (v2) manager to preserve the original behavior: without it neither
// the maps are seeded here nor the routes registered (the analysis pipeline still
// seeds the maps via UpdateCommonNameMap on locale/model changes).
func (c *Controller) initInsightsRoutes() {
	if c.V2Manager == nil {
		return
	}
	// Build both cached name maps once from the current labels.
	if s := c.ControllerSettings(); s != nil {
		c.UpdateCommonNameMap(s.BirdNET.Labels)
	}
	c.analytics.RegisterInsightsRoutes(c.Group)
}
