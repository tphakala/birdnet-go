// internal/api/v2/name_maps.go
//
// Facade-owned BirdNET name-map plumbing. The cached scientific<->common lookup
// maps and the authoritative name resolver live on the *Controller (the fields
// nameMaps and nameResolver in api.go). They are shared infrastructure: the
// analytics domain, the detections search resolver, the species image handler,
// and the settings exclude-list canonicalization all read them through the
// accessors below (analytics, detections, and species receive the accessors as
// injected bound-method values; settings.go calls canonicalizeExcludeList
// directly). UpdateCommonNameMap and SetNameResolver are part of the external
// surface: internal/analysis drives them through *apiv2.Controller. Keeping the
// plumbing here (rather than in a domain package) avoids any domain->domain or
// domain->facade dependency.
package api

import (
	"github.com/tphakala/birdnet-go/internal/api/v2/apicore"
	"github.com/tphakala/birdnet-go/internal/datastore"
)

// nameMaps holds the bidirectional BirdNET label lookup maps. Grouping them
// into one struct lets a single atomic.Value Store swap both maps together,
// avoiding any window where readers could see a partially updated pair.
type nameMaps struct {
	// sciToCommon maps scientific name -> common name.
	sciToCommon map[string]string
	// commonToSci maps NFC-normalised, lowercased common name -> scientific name.
	commonToSci map[string]string
}

// buildNameMaps parses a BirdNET label list ("ScientificName_CommonName")
// and builds both lookup maps in a single pass. If two or more labels share
// the same normalised common name but map to different scientific names,
// the common-name key is removed from commonToSci so that search queries
// matching an ambiguous common name pass through untranslated; resolving
// them to an arbitrary species based on label order would silently hide
// valid matches. sciToCommon is not affected because scientific names are
// unique per label.
// When resolver is non-nil, each label's common name is overridden by the
// resolver (authoritative/localized), so insights display (sciToCommon) and
// search (commonToSci) both reflect the localized name. Labels the resolver does
// not cover keep their embedded common name.
func buildNameMaps(labels []string, resolver datastore.SpeciesNameResolver) *nameMaps {
	nm := &nameMaps{
		sciToCommon: make(map[string]string, len(labels)),
		commonToSci: make(map[string]string, len(labels)),
	}
	ambiguous := make(map[string]struct{})
	for _, sn := range datastore.ResolveLabelNames(labels, resolver) {
		nm.sciToCommon[sn.Scientific] = sn.Common

		key := apicore.NormalizeForLookup(sn.Common)
		if _, seen := ambiguous[key]; seen {
			continue
		}
		if existing, exists := nm.commonToSci[key]; exists && existing != sn.Scientific {
			ambiguous[key] = struct{}{}
			delete(nm.commonToSci, key)
			continue
		}
		nm.commonToSci[key] = sn.Scientific
	}
	return nm
}

// emptyNameMaps is returned by loadNameMaps when the atomic.Value has not been
// populated yet (a narrow startup window before initInsightsRoutes runs). It
// avoids allocating a fresh struct and two empty maps on every cold-path call.
var emptyNameMaps = &nameMaps{
	sciToCommon: map[string]string{},
	commonToSci: map[string]string{},
}

// loadNameMaps returns the current name-maps struct. Always returns a non-nil
// struct with non-nil inner maps so callers can index without guards.
func (c *Controller) loadNameMaps() *nameMaps {
	if nm, ok := c.nameMaps.Load().(*nameMaps); ok && nm != nil {
		return nm
	}
	return emptyNameMaps
}

// loadCommonToScientificMap returns the current common-to-scientific lookup map.
// Always returns a non-nil map.
func (c *Controller) loadCommonToScientificMap() map[string]string {
	return c.loadNameMaps().commonToSci
}

// loadCommonNameMap returns the current scientific-to-common lookup map.
// Always returns a non-nil map.
func (c *Controller) loadCommonNameMap() map[string]string {
	return c.loadNameMaps().sciToCommon
}

// canonicalizeExcludeList canonicalizes the species exclude list (resolve each
// entry to its scientific name, drop blanks, de-duplicate case-insensitively). It
// is a thin facade wrapper over apicore.CanonicalizeExcludeList so the settings
// save flow and the detection ignore/review handlers keep the stored list in a
// single canonical form. Returns nil for an empty/all-blank input.
func (c *Controller) canonicalizeExcludeList(exclude []string) []string {
	return apicore.CanonicalizeExcludeList(c.loadCommonToScientificMap(), exclude)
}

// UpdateCommonNameMap rebuilds both cached name maps from updated BirdNET labels.
// Called after locale or model changes to keep insights and search endpoints current.
func (c *Controller) UpdateCommonNameMap(labels []string) {
	c.nameMaps.Store(buildNameMaps(labels, c.loadNameResolver()))
}

// SetNameResolver installs the authoritative localized name resolver, shared with
// the classifier orchestrator. A nil resolver is ignored.
func (c *Controller) SetNameResolver(r datastore.SpeciesNameResolver) {
	if datastore.IsNilResolver(r) {
		return
	}
	c.nameResolver.Store(&r)
}

// loadNameResolver returns the installed resolver, or nil if none has been set.
func (c *Controller) loadNameResolver() datastore.SpeciesNameResolver {
	if p := c.nameResolver.Load(); p != nil {
		return *p
	}
	return nil
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
