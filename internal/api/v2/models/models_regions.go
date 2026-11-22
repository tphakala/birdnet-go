package models

import (
	"cmp"
	"maps"
	"net/http"
	"slices"

	"github.com/labstack/echo/v4"

	"github.com/tphakala/birdnet-go/internal/api/v2/apicore"
	"github.com/tphakala/birdnet-go/internal/classifier"
	"github.com/tphakala/birdnet-go/internal/classifier/region"
	"github.com/tphakala/birdnet-go/internal/logger"
)

const (
	// svgContentType is the media type served for region coverage maps.
	svgContentType = "image/svg+xml"
	// coverageMapCacheControl lets a browser reuse an embedded coverage map for
	// an hour before revalidating. Combined with the per-map ETag, a revalidation
	// after that window returns 304 unless a binary upgrade changed the bytes.
	coverageMapCacheControl = "public, max-age=3600"
)

// RegionOption is one selectable region in the gallery dropdown.
type RegionOption struct {
	Slug         string           `json:"slug"`
	Name         string           `json:"name"`
	Group        string           `json:"group"`        // continental bucket slug, for grouping in the UI
	GroupDisplay string           `json:"groupDisplay"` // continental bucket display name
	Tier         int              `json:"tier"`
	Countries    region.Countries `json:"countries"` // ISO 3166-1 alpha-2 codes the UI localizes client-side
}

// RegionResolution is the coordinate-resolution outcome the UI renders as the
// "detected region" why-line. Slug is empty when the global model applies.
type RegionResolution struct {
	Slug      string `json:"slug"`
	Source    string `json:"source"`
	Ambiguous bool   `json:"ambiguous"`
	RunnerUp  string `json:"runnerUp,omitempty"`
}

// RegionFamily reports how one installed-or-available model family resolves under
// the configured coordinates, so the UI can surface a per-family difference (the
// D8 divergence case). Today all families share geometry and resolve identically.
type RegionFamily struct {
	CatalogID              string           `json:"catalogId"`
	Repo                   string           `json:"repo"`
	Installed              bool             `json:"installed"`
	InstalledVariantRegion string           `json:"installedVariantRegion"` // region of the installed variant, "" for a global/hardware variant
	Resolved               RegionResolution `json:"resolved"`
}

// ModelRegionsResponse is the payload of GET /api/v2/models/regions. It never
// echoes the raw coordinates back; only the resolved region slug leaves the
// server, so the endpoint reveals no more location precision than the region
// tiles themselves.
type ModelRegionsResponse struct {
	ModelRegion        string           `json:"modelRegion"`        // the saved BirdNET.ModelRegion setting
	LocationConfigured bool             `json:"locationConfigured"` // whether the station location is set
	Resolved           RegionResolution `json:"resolved"`           // what "auto" resolves to from the coordinates
	Regions            []RegionOption   `json:"regions"`            // dropdown options, union across families
	Families           []RegionFamily   `json:"families"`           // per-family resolution
}

// GetModelRegions returns the region table for the gallery region selector: the
// selectable regions, what the configured coordinates resolve to under "auto"
// (regardless of the saved mode, so the UI can preview an unsaved choice), and
// the per-family resolution. It is auth-gated because the resolved region and
// per-family fields are derived from the user's coordinates (approximate
// location); on an auth-disabled install the middleware passes through. The raw
// coordinates are never echoed back.
func (c *Handler) GetModelRegions(ctx echo.Context) error {
	s := c.CurrentSettings()

	tables, err := region.Tables()
	if err != nil {
		return c.HandleError(ctx, err, "region data unavailable", http.StatusInternalServerError)
	}

	// Coordinate resolution is only meaningful once the user has set a station
	// location. Until then, auto resolves to the global model. Gating on
	// LocationConfigured (rather than trusting that the 0,0 default falls outside
	// every tile) keeps an unconfigured station on the global model even if a
	// future tile ever covers the null-island origin.
	families := c.buildRegionFamilies(tables, s.BirdNET.LocationConfigured, s.BirdNET.Latitude, s.BirdNET.Longitude)

	resolved := RegionResolution{Source: string(region.SourceGlobal)}
	if s.BirdNET.LocationConfigured {
		// Families may ship different tile sets, so the detected region the UI
		// shows comes from the representative table (the most specific tile set),
		// not from whichever family happens to be listed first. A family without
		// that tile reports its own resolution in Families.
		if rep := representativeTable(tables); rep != nil {
			sel := region.Select(rep, region.ModeAuto, s.BirdNET.Latitude, s.BirdNET.Longitude)
			resolved = toRegionResolution(&sel)
		}
	}

	return ctx.JSON(http.StatusOK, ModelRegionsResponse{
		ModelRegion:        s.BirdNET.ModelRegion,
		LocationConfigured: s.BirdNET.LocationConfigured,
		Resolved:           resolved,
		Regions:            buildRegionOptions(tables),
		Families:           families,
	})
}

// GetRegionCoverageMap serves the embedded SVG coverage map for a region slug at
// GET /api/v2/models/regions/:slug/map. Unlike GetModelRegions it is public and
// not auth-gated: it returns fixed embedded bytes selected by a client-supplied
// public slug and reveals nothing derived from the user's location. It sets a
// strong ETag and honors If-None-Match, so a browser revalidation returns 304.
// An unknown or malformed slug is a 404, which the UI treats as "no map" and
// falls back to its text-only country list.
func (c *Handler) GetRegionCoverageMap(ctx echo.Context) error {
	slug := ctx.Param("slug")
	svg, etag, ok := region.CoverageMap(slug)
	if !ok {
		return c.HandleError(ctx, nil, "coverage map not found", http.StatusNotFound)
	}
	h := ctx.Response().Header()
	h.Set("Cache-Control", coverageMapCacheControl)
	h.Set("ETag", etag)
	if apicore.MatchIfNoneMatch(ctx.Request().Header.Get("If-None-Match"), etag) {
		return ctx.NoContent(http.StatusNotModified)
	}
	return ctx.Blob(http.StatusOK, svgContentType, svg)
}

// representativeTable picks a deterministic table from the embedded set for the
// top-level resolution: the one with the most regions (the most specific tile
// set is the detected region the UI shows), ties broken by the lowest repo id.
// Returns nil when no table is embedded.
func representativeTable(tables map[string]*region.Table) *region.Table {
	var best *region.Table
	for _, repo := range slices.Sorted(maps.Keys(tables)) {
		tbl := tables[repo]
		if tbl == nil {
			continue
		}
		if best == nil || len(tbl.Regions) > len(best.Regions) {
			best = tbl
		}
	}
	return best
}

// resolveRecommendRegions resolves the region slugs the recommender scores
// against: the slug of each entry in its own family's table (families may ship
// different tile sets, so one table cannot stand for all), plus the fallback
// slug from the representative table for an entry without a table. See
// recommendRegions for the rules. This runs per request, so a coordinate or
// ModelRegion change takes effect on the next gallery load without a restart.
// Both results are empty (the global model) when region data is unavailable.
func (c *Handler) resolveRecommendRegions(entries []classifier.CatalogEntry) (fallback string, byEntry map[string]string) {
	s := c.CurrentSettings()
	tables, err := region.Tables()
	if err != nil {
		// Embedded region data always loads, so a failure here is a real anomaly
		// worth a log line even though the fallback (global scoring) is safe. Kept
		// asymmetric with GetModelRegions, which 500s: the recommender must never
		// fail the gallery over region data.
		c.LogDebugIfEnabled("region tables unavailable; recommender falling back to global scoring",
			logger.String("error", err.Error()))
		return "", nil // recommender falls back to global scoring; non-fatal
	}
	return recommendRegions(tables, entries, s.BirdNET.ModelRegion, s.BirdNET.LocationConfigured, s.BirdNET.Latitude, s.BirdNET.Longitude)
}

// recommendRegions resolves the recommender region per entry. A pinned region
// is an explicit choice independent of coordinates and is honored even before a
// station location is configured, when the entry's family ships that slug (a
// family without it falls back to coordinate resolution in its own table, see
// region.Select); auto resolution needs coordinates and otherwise stays on the
// global model (mirroring GetModelRegions, which keeps an unconfigured station
// off any tile). The map holds an entry per family table, "" meaning the global
// model; fallback is the representative table's resolution, for an entry whose
// repo has no table.
func recommendRegions(tables map[string]*region.Table, entries []classifier.CatalogEntry, modelRegion string, configured bool, lat, lon float64) (fallback string, byEntry map[string]string) {
	pick := func(tbl *region.Table) string {
		if tbl == nil {
			return ""
		}
		// Without a configured location only an explicit pin selects a region.
		// region.Select owns the mode classification, so ask it and honor just the
		// coordinate-independent pinned outcome (its SourcePinned result), avoiding
		// a null-island auto resolution.
		if !configured {
			if sel := region.Select(tbl, modelRegion, 0, 0); sel.Source == region.SourcePinned {
				return sel.Slug
			}
			return ""
		}
		return region.Select(tbl, modelRegion, lat, lon).Slug
	}
	fallback = pick(representativeTable(tables))
	byRepo := make(map[string]string, len(tables)) // a family's slug is the same for each of its entries
	byEntry = make(map[string]string, len(entries))
	for i := range entries {
		repo := entries[i].HuggingFaceRepo
		tbl, ok := tables[repo]
		if !ok {
			continue
		}
		slug, done := byRepo[repo]
		if !done {
			slug = pick(tbl)
			byRepo[repo] = slug
		}
		byEntry[entries[i].ID] = slug
	}
	return fallback, byEntry
}

// buildRegionOptions flattens the embedded tables into a deduplicated, sorted
// list of dropdown options. Tables are visited in sorted repo order so that, for
// a slug shared across families, the display metadata is taken deterministically
// from the lowest-keyed repo (shared slugs agree across families, a region-package
// test enforces it) rather than from a
// random map-iteration winner. Ordering is by group display, then name, then
// slug (a unique final tiebreaker) for a stable UI.
func buildRegionOptions(tables map[string]*region.Table) []RegionOption {
	seen := make(map[string]RegionOption)
	for _, repo := range slices.Sorted(maps.Keys(tables)) {
		tbl := tables[repo]
		for slug := range tbl.Regions {
			if _, ok := seen[slug]; ok {
				continue
			}
			r := tbl.Regions[slug]
			seen[slug] = RegionOption{
				Slug:         slug,
				Name:         r.Name,
				Group:        r.Group,
				GroupDisplay: r.GroupDisplay,
				Tier:         r.Tier,
				Countries:    r.Countries,
			}
		}
	}
	options := slices.Collect(maps.Values(seen))
	slices.SortFunc(options, func(a, b RegionOption) int {
		if c := cmp.Compare(a.GroupDisplay, b.GroupDisplay); c != 0 {
			return c
		}
		if c := cmp.Compare(a.Name, b.Name); c != 0 {
			return c
		}
		return cmp.Compare(a.Slug, b.Slug)
	})
	return options
}

// buildRegionFamilies reports per-family resolution for every visible catalog
// entry whose HuggingFace repo has a region table. When the station location is
// not configured, coordinate resolution is skipped and every family reports the
// global model, matching how auto falls back without coordinates.
func (c *Handler) buildRegionFamilies(tables map[string]*region.Table, locationConfigured bool, lat, lon float64) []RegionFamily {
	visible := classifier.VisibleCatalog()
	families := make([]RegionFamily, 0, len(visible))
	for i := range visible {
		e := &visible[i]
		tbl, ok := tables[e.HuggingFaceRepo]
		if !ok {
			continue
		}
		installed := false
		installedVariantID := ""
		if c.ModelManager != nil {
			if vid, ok := c.ModelManager.InstalledVariantID(e.ID); ok {
				installed = true
				installedVariantID = vid
			}
		}
		resolved := RegionResolution{Source: string(region.SourceGlobal)}
		if locationConfigured {
			sel := region.Select(tbl, region.ModeAuto, lat, lon)
			resolved = toRegionResolution(&sel)
		}
		families = append(families, RegionFamily{
			CatalogID:              e.ID,
			Repo:                   e.HuggingFaceRepo,
			Installed:              installed,
			InstalledVariantRegion: classifier.VariantRegion(e, installedVariantID),
			Resolved:               resolved,
		})
	}
	return families
}

// toRegionResolution maps a resolver Selection to its API form.
func toRegionResolution(sel *region.Selection) RegionResolution {
	return RegionResolution{
		Slug:      sel.Slug,
		Source:    string(sel.Source),
		Ambiguous: sel.Ambiguous,
		RunnerUp:  sel.RunnerUp,
	}
}
