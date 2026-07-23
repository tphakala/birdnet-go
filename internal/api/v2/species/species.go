// Package species is the api/v2 species domain handler. It owns the
// /api/v2/species/* and /api/v2/taxonomy/* endpoints (species info, rarity, the
// all-species picker list, the species dictionary, thumbnails, and genus/family/
// tree taxonomy lookups). The Handler embeds *apicore.Core by pointer so the
// shared dependencies and helpers (Processor, TaxonomyDB, EBirdClient,
// BirdImageCache, CurrentLocale, HandleError, the logging helpers) promote onto
// it; the facade constructs one Handler and calls RegisterRoutes to wire the
// routes in their existing order.
//
// Two dependencies the species handlers need are owned by other parts of the
// monolith that have not been extracted yet, so the facade injects them as
// function values (the tls-domain facade-dependency-injection precedent):
//   - commonNameMap: a read accessor over the shared scientific-to-common name
//     map. The name-map plumbing (UpdateCommonNameMap/SetNameResolver) stays in
//     the facade package because control_monitor drives it and several domains
//     share it; species only needs read access.
//   - serveImageProxy: the media domain's bird-image proxy handler, which the
//     species thumbnail endpoint delegates to. When media is extracted this can
//     point at the media handler instead.
package species

import (
	"context"
	"net/http"
	"sort"
	"strings"
	"time"

	"github.com/labstack/echo/v4"
	"github.com/tphakala/birdnet-go/internal/api/v2/apicore"
	"github.com/tphakala/birdnet-go/internal/api/v2/dto"
	"github.com/tphakala/birdnet-go/internal/classifier"
	"github.com/tphakala/birdnet-go/internal/conf"
	"github.com/tphakala/birdnet-go/internal/detection"
	"github.com/tphakala/birdnet-go/internal/ebird"
	"github.com/tphakala/birdnet-go/internal/errors"
	"github.com/tphakala/birdnet-go/internal/logger"
	"github.com/tphakala/birdnet-go/internal/openfauna"
)

// Handler serves the species domain endpoints. It embeds *apicore.Core BY
// POINTER so the shared Core members promote onto it without re-wiring; Core
// carries atomic/lock-bearing fields and must never be copied by value.
type Handler struct {
	*apicore.Core

	// commonNameMap returns the current scientific-to-common lookup map, owned by
	// the facade's name-map plumbing and injected so species stays read-only over
	// it. Always returns a non-nil map.
	commonNameMap func() map[string]string

	// serveImageProxy is the media domain's species-image proxy handler. The
	// thumbnail endpoint resolves a species code to a scientific name and then
	// delegates to it.
	serveImageProxy echo.HandlerFunc
}

// New builds a species Handler around the shared core and the two facade-owned
// dependencies the species handlers delegate to (the common-name map accessor
// and the media image-proxy handler).
func New(core *apicore.Core, commonNameMap func() map[string]string, serveImageProxy echo.HandlerFunc) *Handler {
	return &Handler{Core: core, commonNameMap: commonNameMap, serveImageProxy: serveImageProxy}
}

// RegisterRoutes registers all species-related API endpoints on the supplied API
// v2 group, preserving the exact routes and order the facade used before the
// species domain was extracted.
func (c *Handler) RegisterRoutes(g *echo.Group) {
	// Public endpoints for species information
	g.GET("/species", c.GetSpeciesInfo)
	g.GET("/species/all", c.GetAllSpecies)
	g.GET("/species/taxonomy", c.GetSpeciesTaxonomy)
	g.GET("/species/dictionary/:locale", c.ServeSpeciesDictionary)

	// RESTful thumbnail endpoint - uses species code from path
	g.GET("/species/:code/thumbnail", c.GetSpeciesThumbnail)

	// New taxonomy endpoints using local database
	g.GET("/taxonomy/genus/:genus", c.GetGenusSpecies)
	g.GET("/taxonomy/family/:family", c.GetFamilySpecies)
	g.GET("/taxonomy/tree/:scientific_name", c.GetSpeciesTree)
}

// RarityStatus represents the rarity classification of a species
type RarityStatus string

const (
	RarityVeryCommon RarityStatus = "very_common"
	RarityCommon     RarityStatus = "common"
	RarityUncommon   RarityStatus = "uncommon"
	RarityRare       RarityStatus = "rare"
	RarityVeryRare   RarityStatus = "very_rare"
	RarityUnknown    RarityStatus = "unknown"
)

// Rarity threshold constants for score-based classification
const (
	RarityThresholdVeryCommon = 0.8
	RarityThresholdCommon     = 0.5
	RarityThresholdUncommon   = 0.2
	RarityThresholdRare       = 0.05
)

// SpeciesInfo represents extended information about a bird species
type SpeciesInfo struct {
	ScientificName string             `json:"scientific_name"`
	CommonName     string             `json:"common_name"`
	Rarity         SpeciesRarityInfo  `json:"rarity,omitzero"`
	Taxonomy       ebird.TaxonomyTree `json:"taxonomy,omitzero"`
	Metadata       map[string]any     `json:"metadata,omitempty"`
}

// SpeciesRarityInfo contains rarity information for a species
type SpeciesRarityInfo struct {
	Status           RarityStatus `json:"status"`
	Score            float64      `json:"score"`
	LocationBased    bool         `json:"location_based"`
	Latitude         float64      `json:"latitude,omitempty"`
	Longitude        float64      `json:"longitude,omitempty"`
	Date             string       `json:"date"`
	ThresholdApplied float64      `json:"threshold_applied"`
}

// taxonomyLookupResult holds the result of a taxonomy lookup with source info.
type taxonomyLookupResult struct {
	tree   *ebird.TaxonomyTree
	source string
}

// lookupTaxonomyEitherName looks the species up under both of the names it may be known
// by, returning the first hit. The embedded taxonomy database is a frozen snapshot that
// holds some species under their legacy name and others under the current one, and
// neither the request nor the matched model label is reliably the indexed form: of the
// 236 alias pairs BirdNET v2.4 ships under the legacy name, 153 are in the database
// under both, 56 only under the current name and 27 only under the legacy one. Trying
// one name alone therefore drops the taxonomy block for a knowable species.
func (c *Handler) lookupTaxonomyEitherName(ctx context.Context, primary, secondary string) *taxonomyLookupResult {
	if result := c.lookupTaxonomyTree(ctx, primary); result != nil {
		return result
	}
	if strings.EqualFold(primary, secondary) {
		return nil
	}
	return c.lookupTaxonomyTree(ctx, secondary)
}

// resolveEitherName localizes a common name under whichever of the two scientific names
// the resolver's working set is keyed on, for the reason lookupTaxonomyEitherName
// documents.
func (c *Handler) resolveEitherName(bn *classifier.Orchestrator, primary, secondary string) string {
	if resolved := bn.ResolveName(primary, c.CurrentLocale()); resolved != "" {
		return resolved
	}
	if strings.EqualFold(primary, secondary) {
		return ""
	}
	return bn.ResolveName(secondary, c.CurrentLocale())
}

// lookupTaxonomyTree attempts to find taxonomy for a species, trying local DB first then eBird.
// Returns nil result (not error) if taxonomy is unavailable from both sources.
func (c *Handler) lookupTaxonomyTree(ctx context.Context, scientificName string) *taxonomyLookupResult {
	// Try local taxonomy database first (fast, no network)
	if c.TaxonomyDB != nil {
		tree, err := c.TaxonomyDB.BuildFamilyTree(scientificName)
		if err == nil {
			c.Debug("Retrieved taxonomy for %s from local database", scientificName)
			return &taxonomyLookupResult{tree: tree, source: "local"}
		}
		c.Debug("Local taxonomy lookup failed for %s: %v, falling back to eBird API", scientificName, err)
	}

	// Fall back to eBird API
	if c.EBirdClient != nil {
		tree, err := c.EBirdClient.BuildFamilyTree(ctx, scientificName)
		if err != nil {
			c.Debug("Failed to get taxonomy info from eBird for species %s: %v", scientificName, err)
			return nil
		}
		return &taxonomyLookupResult{tree: tree, source: "ebird"}
	}

	return nil
}

// AllSpeciesResponse represents the response for the all species endpoint
type AllSpeciesResponse struct {
	Species []dto.RangeFilterSpecies `json:"species"`
	Count   int                      `json:"count"`
}

// GetAllSpecies returns all known BirdNET species labels regardless of location or range filter.
// This is used for species include/exclude search where users need to find any species,
// not just those matching the current location's range filter.
// @Summary Get all BirdNET species
// @Description Returns all species from the loaded BirdNET labels, independent of range filter
// @Tags species
// @Produce json
// @Success 200 {object} AllSpeciesResponse
// @Failure 500 {object} ErrorResponse
// @Router /api/v2/species/all [get]
func (c *Handler) GetAllSpecies(ctx echo.Context) error {
	ip := ctx.RealIP()
	path := ctx.Request().URL.Path
	c.LogDebugIfEnabled("Retrieving all BirdNET species labels",
		logger.String("ip", ip),
		logger.String("path", path),
	)

	speciesList := buildAllSpeciesList(c.commonNameMap(), c.allModelLabels())

	c.LogInfoIfEnabled("All species labels retrieved successfully",
		logger.Int("count", len(speciesList)),
		logger.String("ip", ip),
		logger.String("path", path),
	)

	return ctx.JSON(http.StatusOK, AllSpeciesResponse{
		Species: speciesList,
		Count:   len(speciesList),
	})
}

// buildAllSpeciesList builds the /api/v2/species/all payload for the species
// include/exclude picker. When the cached, resolver-localized scientific-to-common
// map (sciToCommon) is populated, the list is built from it: control_monitor seeds
// that map from the orchestrator's multi-model AllLabels union at startup and on
// hot-reload, so it already covers every loaded model's species (including
// secondary-model bats/Perch), localized to the configured BirdNET.Locale and
// deduplicated (scientific keys are unique). Output is sorted by scientific name for
// a deterministic response.
//
// When sciToCommon is empty (the brief startup window before control_monitor seeds
// the maps), it falls back to parsing fallbackLabels. The caller passes the
// orchestrator's AllLabels union there (not just the primary BirdNET labels), so the
// picker still includes secondary-model species during that window; the fallback
// preserves input order and the original label string.
func buildAllSpeciesList(sciToCommon map[string]string, fallbackLabels []string) []dto.RangeFilterSpecies {
	if len(sciToCommon) > 0 {
		speciesList := make([]dto.RangeFilterSpecies, 0, len(sciToCommon))
		for sci, common := range sciToCommon {
			speciesList = append(speciesList, dto.RangeFilterSpecies{
				Label:          sci + "_" + common,
				ScientificName: sci,
				CommonName:     common,
			})
		}
		sort.Slice(speciesList, func(i, j int) bool {
			return speciesList[i].ScientificName < speciesList[j].ScientificName
		})
		return speciesList
	}

	speciesList := make([]dto.RangeFilterSpecies, 0, len(fallbackLabels))
	seen := make(map[string]struct{}, len(fallbackLabels))
	for _, label := range fallbackLabels {
		sp := detection.ParseSpeciesString(label)
		// AllLabels unions models that may emit the same species in different label
		// forms ("Scientific_Common" vs scientific-only), so dedup by scientific name
		// to avoid duplicate rows; input order and the original label are preserved.
		key := strings.ToLower(sp.ScientificName)
		if _, dup := seen[key]; dup {
			continue
		}
		seen[key] = struct{}{}
		speciesList = append(speciesList, dto.RangeFilterSpecies{
			Label:          label,
			ScientificName: sp.ScientificName,
			CommonName:     sp.CommonName,
		})
	}
	return speciesList
}

// allModelLabels returns the union of every loaded model's labels (primary plus
// secondary models such as the bat and Perch classifiers) from the orchestrator,
// falling back to the primary BirdNET labels from settings when the orchestrator is
// not yet available (e.g. early startup before the audio pipeline builds it).
func (c *Handler) allModelLabels() []string {
	if proc := c.Processor; proc != nil {
		if bn := proc.GetBirdNET(); bn != nil {
			if labels := bn.AllLabels(); len(labels) > 0 {
				return labels
			}
		}
	}
	if s := c.ControllerSettings(); s != nil {
		return s.BirdNET.Labels
	}
	return nil
}

// resolveSpeciesLabel finds the "Scientific_Common" label denoting targetSci in a label
// set, returning the label and its common name, or empty strings when no label denotes
// the species.
//
// An exact scientific-name match is tried across the whole set before any alias match,
// so a species that exists under its own name is never shadowed by a synonym. That
// ordering is load-bearing: OpenFauna's alias map merges pairs the classifier ships as
// separate species (BirdNET v2.4 carries both Dicrurus adsimilis and D. divaricatus,
// and both Mirafra javanica and M. cantillans), so an alias-first match would answer a
// request for one of them with the other's label and common name.
//
// This keeps the pair distinct whenever both are in the set. It cannot when only one is:
// the alias fallback then answers a request for the absent member with the present one's
// label, which is the same behaviour any alias resolution has and is still better than
// reporting nothing, but it is not a guarantee that the two never cross.
func resolveSpeciesLabel(targetSci string, allLabels []string) (matchedLabel, commonName string) {
	for _, label := range allLabels {
		if strings.EqualFold(detection.ExtractScientificName(label), targetSci) {
			return label, detection.ParseSpeciesString(label).CommonName
		}
	}
	// No label carries this exact name, so fall back to the taxonomic alias and let a
	// request naming a species by a legacy synonym resolve to its current name.
	canonicalTarget := openfauna.CanonicalName(targetSci)
	for _, label := range allLabels {
		if labelMatchesSpecies(label, canonicalTarget) {
			return label, detection.ParseSpeciesString(label).CommonName
		}
	}
	return "", ""
}

// GetSpeciesInfo retrieves extended information about a bird species
func (c *Handler) GetSpeciesInfo(ctx echo.Context) error {
	// Get scientific name from query parameter
	scientificName := ctx.QueryParam("scientific_name")
	if scientificName == "" {
		return c.HandleError(ctx, errors.Newf("scientific_name parameter is required").
			Category(errors.CategoryValidation).
			Component("api-species").
			Build(), "Missing required parameter", http.StatusBadRequest)
	}

	// Validate the scientific name format (basic validation)
	scientificName = strings.TrimSpace(scientificName)
	if len(scientificName) < 3 || !strings.Contains(scientificName, " ") {
		return c.HandleError(ctx, errors.Newf("invalid scientific name format").
			Category(errors.CategoryValidation).
			Context("scientific_name", scientificName).
			Component("api-species").
			Build(), "Invalid scientific name format", http.StatusBadRequest)
	}

	// Get species info
	speciesInfo, err := c.getSpeciesInfo(ctx.Request().Context(), scientificName)
	if err != nil {
		return c.HandleErrorWithNotFound(ctx, err, "Species not found", "Failed to get species information")
	}

	return ctx.JSON(http.StatusOK, speciesInfo)
}

// getSpeciesInfo retrieves species information including rarity status
func (c *Handler) getSpeciesInfo(ctx context.Context, scientificName string) (*SpeciesInfo, error) {
	// Snapshot to avoid TOCTOU race on c.Processor
	proc := c.Processor
	if proc == nil || proc.Bn == nil {
		return nil, errors.Newf("BirdNET processor not available").
			Category(errors.CategorySystem).
			Component("api-species").
			Build()
	}

	bn := proc.Bn

	// Search the full multi-model label union (primary plus secondary models such
	// as the bat/Perch classifiers) so a secondary-model scientific name resolves
	// instead of 404ing.
	matchedLabel, commonName := resolveSpeciesLabel(scientificName, bn.AllLabels())

	// If species not found in any loaded model's labels, return error
	if matchedLabel == "" {
		return nil, errors.Newf("species '%s' not found in loaded model labels", scientificName).
			Category(errors.CategoryNotFound).
			Context("scientific_name", scientificName).
			Component("api-species").
			Build()
	}

	// The request name and the matched label's name differ whenever one of them is a
	// taxonomic synonym of the other, and neither is reliably the one a downstream
	// backend is keyed on: detections are canonicalized at ingestion so the request
	// usually carries the current name, while BirdNET v2.4's labels carry the legacy
	// one, and the embedded taxonomy database is a frozen snapshot holding some species
	// under each. Downstream lookups therefore try both rather than picking one.
	matchedSci := detection.ExtractScientificName(matchedLabel)

	// Secondary-model labels (bats, Perch) are scientific-only, so ParseSpeciesString
	// reports CommonName == ScientificName for them. Treat that (and an empty common
	// name) as "needs localizing" and resolve through the orchestrator's
	// OpenFauna-authoritative resolver, passing the configured locale explicitly.
	if commonName == "" || strings.EqualFold(commonName, matchedSci) {
		if resolved := c.resolveEitherName(bn, matchedSci, scientificName); resolved != "" {
			commonName = resolved
		}
	}

	// Report the name the caller asked for. Echoing the matched label's name instead
	// would hand back the legacy synonym for a request made with the current name, which
	// is the form the rest of the API uses since detections are canonicalized on write.
	info := &SpeciesInfo{
		ScientificName: scientificName,
		CommonName:     commonName,
		Metadata:       make(map[string]any),
	}

	// Get rarity information
	rarityInfo, err := c.getSpeciesRarityInfo(bn, matchedLabel)
	if err != nil {
		// Log error but don't fail the request
		c.Debug("Failed to get rarity info for species %s: %v", scientificName, err)
		// Continue without rarity info
	} else {
		info.Rarity = rarityInfo
	}

	// Get taxonomy/family tree information using fallback pattern. Both backends return
	// a non-nil tree whenever they report no error, so the tree guard is defensive: the
	// field is a value, so a backend that ever returned (nil, nil) would panic the
	// handler rather than yield an empty tree.
	if result := c.lookupTaxonomyEitherName(ctx, matchedSci, scientificName); result != nil {
		if result.tree != nil {
			info.Taxonomy = *result.tree
		}
		info.Metadata["source"] = result.source
	}

	return info, nil
}

// labelMatchesSpecies reports whether a "Scientific_Common" label denotes the same
// taxon as canonicalTarget, which the caller must have already passed through
// openfauna.CanonicalName. Both sides are canonicalized so a legacy synonym in the
// label (or in the request) matches the current name, and compared case-insensitively
// because CanonicalName preserves the input's case for names it has no alias for.
func labelMatchesSpecies(label, canonicalTarget string) bool {
	return strings.EqualFold(openfauna.CanonicalName(detection.ExtractScientificName(label)), canonicalTarget)
}

// speciesHasGeomodelCoverage reports whether the active range filter can produce an
// occurrence probability for the scientific name. It answers against the geomodel's own
// vocabulary, which for the universal geomodel is much larger than the primary
// classifier's (it spans birds, bats, other mammals and insects), so a species the
// classifier cannot name still gets a real rarity.
//
// geomodelLabels is empty for every backend other than the universal geomodel: the
// TFLite meta model and the plain ONNX range filter are keyed to the classifier's own
// labels, so falling back to classifierLabels is the correct vocabulary for those, not a
// degraded approximation.
//
// It is also empty in two states where the fallback grants nominal coverage to every
// classifier species even though nothing is scoring them, so each reports "very rare" at
// score 0: no location configured, and no range filter loaded. Only the first is visible
// to a client, via SpeciesRarityInfo.LocationBased; a range filter that failed to load
// leaves LocationBased true, so a caller cannot currently distinguish that state from a
// genuine result. That predates this function's signature and is not something callers
// can guard against today.
//
// A species in neither vocabulary (a secondary-model-only species the geomodel does not
// cover) has no occurrence probability to base a rarity on.
func speciesHasGeomodelCoverage(targetSci string, geomodelLabels, classifierLabels []string) bool {
	labels := geomodelLabels
	if len(labels) == 0 {
		labels = classifierLabels
	}
	canonicalTarget := openfauna.CanonicalName(targetSci)
	for _, label := range labels {
		if labelMatchesSpecies(label, canonicalTarget) {
			return true
		}
	}
	return false
}

// findSpeciesScore returns the occurrence score for targetSci from a probable-species
// list. It matches on the exact scientific name across the whole list before trying any
// alias, for the reason resolveSpeciesLabel documents: the alias map merges pairs the
// classifier ships as separate species, and an alias-first match would report one bird's
// occurrence probability for the other. As there, the separation holds only while both
// members are in the list; the probable-species list carries only species above today's
// threshold, so one member being absent is the common case.
func findSpeciesScore(targetSci string, speciesScores []classifier.SpeciesScore) (float64, bool) {
	for _, ss := range speciesScores {
		if strings.EqualFold(detection.ExtractScientificName(ss.Label), targetSci) {
			return ss.Score, true
		}
	}
	canonicalTarget := openfauna.CanonicalName(targetSci)
	for _, ss := range speciesScores {
		if labelMatchesSpecies(ss.Label, canonicalTarget) {
			return ss.Score, true
		}
	}
	return 0, false
}

// computeRarity resolves a species to its occurrence score and rarity status.
//
// Coverage decides first. A species the range filter cannot score has no occurrence
// probability at all, so it is reported as unknown even when it does appear in the
// probable-species list, because PassUnmappedSpecies injects species with no geomodel
// match at score 0.0 purely so they survive the filter. Reading that synthetic zero as a
// rarity reported "very rare" for a species the geomodel has no data on, and made the
// badge depend on whether an unrelated toggle was enabled.
//
// This does NOT cover the other synthetic score. addUserOverrideSpeciesScores injects
// force-included species at 1.0, but resolveOverrideLabels resolves an override against
// the geomodel labels first, so a force-included species the geomodel knows is inside the
// coverage vocabulary and still reads as "very common" off that injected 1.0. Only an
// override for a species outside the geomodel's vocabulary reaches the unknown path here.
// Distinguishing a real score from an injected one needs the range filter to tag
// synthetic entries; membership in a vocabulary cannot express it.
//
// A covered species present in the list is scored directly; one that is covered but
// absent is below today's threshold and therefore genuinely very rare.
func computeRarity(targetSci string, speciesScores []classifier.SpeciesScore, geomodelLabels, classifierLabels []string) (float64, RarityStatus) {
	if !speciesHasGeomodelCoverage(targetSci, geomodelLabels, classifierLabels) {
		return 0.0, RarityUnknown
	}

	if score, found := findSpeciesScore(targetSci, speciesScores); found {
		return score, calculateRarityStatus(score)
	}

	return 0.0, RarityVeryRare
}

func (c *Handler) getSpeciesRarityInfo(bn *classifier.Orchestrator, speciesLabel string) (SpeciesRarityInfo, error) {
	// Get current local date
	today := conf.LocalNoon(time.Now())
	settings := bn.CurrentSettings()

	// Rarity is the geomodel occurrence probability, so use the geomodel-backed
	// probable-species list, not the multi-model union: the union assigns synthetic
	// always-active scores (1.0) to secondary-model species (bats, Perch) that have
	// no real occurrence probability, which would misclassify them as "very common".
	speciesScores, geomodelLabels, classifierLabels, err := bn.GetRarityContext(today)
	if err != nil {
		return SpeciesRarityInfo{}, errors.New(err).
			Category(errors.CategoryProcessing).
			Context("species_label", speciesLabel).
			Component("api-species").
			Build()
	}

	// Create rarity info
	rarityInfo := SpeciesRarityInfo{
		Date:             today.Format(time.DateOnly),
		LocationBased:    settings.BirdNET.LocationConfigured,
		ThresholdApplied: float64(settings.BirdNET.RangeFilter.Threshold),
	}

	// Add location if available
	if rarityInfo.LocationBased {
		rarityInfo.Latitude = settings.BirdNET.Latitude
		rarityInfo.Longitude = settings.BirdNET.Longitude
	}

	// Resolve the score and status together; computeRarity documents how an absent
	// species is split between "very rare" and "unknown" by geomodel coverage.
	targetSci := detection.ExtractScientificName(speciesLabel)
	rarityInfo.Score, rarityInfo.Status = computeRarity(targetSci, speciesScores, geomodelLabels, classifierLabels)

	return rarityInfo, nil
}

// calculateRarityStatus determines the rarity status based on the probability score
func calculateRarityStatus(score float64) RarityStatus {
	switch {
	case score > RarityThresholdVeryCommon:
		return RarityVeryCommon
	case score > RarityThresholdCommon:
		return RarityCommon
	case score > RarityThresholdUncommon:
		return RarityUncommon
	case score > RarityThresholdRare:
		return RarityRare
	default:
		return RarityVeryRare
	}
}

// TaxonomyInfo represents detailed taxonomy information for a species
type TaxonomyInfo struct {
	ScientificName     string            `json:"scientific_name"`
	SpeciesCode        string            `json:"species_code,omitempty"`
	Taxonomy           TaxonomyHierarchy `json:"taxonomy,omitzero"`
	Subspecies         []SubspeciesInfo  `json:"subspecies,omitempty"`
	Synonyms           []string          `json:"synonyms,omitempty"`
	ConservationStatus string            `json:"conservation_status,omitempty"`
	NativeRegions      []string          `json:"native_regions,omitempty"`
	Metadata           map[string]any    `json:"metadata,omitempty"`
}

// TaxonomyHierarchy represents the full taxonomic classification
type TaxonomyHierarchy struct {
	Kingdom       string `json:"kingdom"`
	Phylum        string `json:"phylum"`
	Class         string `json:"class"`
	Order         string `json:"order"`
	Family        string `json:"family"`
	FamilyCommon  string `json:"family_common,omitempty"`
	Genus         string `json:"genus"`
	Species       string `json:"species"`
	SpeciesCommon string `json:"species_common,omitempty"`
}

// SubspeciesInfo represents information about a subspecies
type SubspeciesInfo struct {
	ScientificName string `json:"scientific_name"`
	CommonName     string `json:"common_name,omitempty"`
	Region         string `json:"region,omitempty"`
}

// GetSpeciesTaxonomy retrieves detailed taxonomy information for a species
func (c *Handler) GetSpeciesTaxonomy(ctx echo.Context) error {
	// Get parameters from query
	scientificName := ctx.QueryParam("scientific_name")
	if scientificName == "" {
		return c.HandleError(ctx, errors.Newf("scientific_name parameter is required").
			Category(errors.CategoryValidation).
			Component("api-species").
			Build(), "Missing required parameter", http.StatusBadRequest)
	}

	// Validate the scientific name format (basic validation)
	scientificName = strings.TrimSpace(scientificName)
	if len(scientificName) < 3 || !strings.Contains(scientificName, " ") {
		return c.HandleError(ctx, errors.Newf("invalid scientific name format").
			Category(errors.CategoryValidation).
			Context("scientific_name", scientificName).
			Component("api-species").
			Build(), "Invalid scientific name format", http.StatusBadRequest)
	}

	// Get optional parameters
	locale := ctx.QueryParam("locale")
	includeSubspecies := ctx.QueryParam("include_subspecies") != "false" // default true
	includeHierarchy := ctx.QueryParam("include_hierarchy") != "false"   // default true

	// Get taxonomy info
	taxonomyInfo, err := c.getDetailedTaxonomy(ctx.Request().Context(), scientificName, locale, includeSubspecies, includeHierarchy)
	if err != nil {
		return c.HandleErrorWithNotFound(ctx, err, "Species not found", "Failed to get taxonomy information")
	}

	return ctx.JSON(http.StatusOK, taxonomyInfo)
}

// getDetailedTaxonomy retrieves detailed taxonomy information
// Tries local database first, falls back to eBird API if needed
func (c *Handler) getDetailedTaxonomy(ctx context.Context, scientificName, locale string, includeSubspecies, includeHierarchy bool) (*TaxonomyInfo, error) {
	// Try local taxonomy database first
	if info := c.tryLocalTaxonomy(ctx, scientificName, locale, includeSubspecies, includeHierarchy); info != nil {
		return info, nil
	}

	// Fall back to eBird API
	if c.EBirdClient != nil {
		return c.getEBirdTaxonomy(ctx, scientificName, locale, includeSubspecies)
	}

	// Neither local DB nor eBird API available
	return nil, errors.Newf("taxonomy data not available (no local database or eBird API)").
		Category(errors.CategoryConfiguration).
		Priority(errors.PriorityLow).
		Context("scientific_name", scientificName).
		Component("api-species").
		Build()
}

// tryLocalTaxonomy attempts to retrieve taxonomy from the local database.
// Returns nil if local DB is unavailable or lookup fails.
func (c *Handler) tryLocalTaxonomy(ctx context.Context, scientificName, locale string, includeSubspecies, includeHierarchy bool) *TaxonomyInfo {
	if c.TaxonomyDB == nil {
		return nil
	}

	taxonomyTree, err := c.TaxonomyDB.BuildFamilyTree(scientificName)
	if err != nil {
		c.Debug("Local taxonomy lookup failed for %s: %v, falling back to eBird API", scientificName, err)
		return nil
	}

	info := &TaxonomyInfo{
		ScientificName: scientificName,
		Metadata: map[string]any{
			"source":     "local",
			"updated_at": c.TaxonomyDB.UpdatedAt,
		},
	}

	// Add hierarchy if requested
	if includeHierarchy && taxonomyTree != nil {
		info.Taxonomy = convertToTaxonomyHierarchy(taxonomyTree)
	}

	// Enhance with eBird data if needed
	c.enhanceWithEBirdData(ctx, info, scientificName, locale, includeSubspecies)

	return info
}

// convertToTaxonomyHierarchy converts an ebird.TaxonomyTree to TaxonomyHierarchy.
func convertToTaxonomyHierarchy(tree *ebird.TaxonomyTree) TaxonomyHierarchy {
	return TaxonomyHierarchy{
		Kingdom:       tree.Kingdom,
		Phylum:        tree.Phylum,
		Class:         tree.Class,
		Order:         tree.Order,
		Family:        tree.Family,
		FamilyCommon:  tree.FamilyCommon,
		Genus:         tree.Genus,
		Species:       tree.Species,
		SpeciesCommon: tree.SpeciesCommon,
	}
}

// enhanceWithEBirdData adds subspecies and locale data from eBird API to local taxonomy info.
func (c *Handler) enhanceWithEBirdData(ctx context.Context, info *TaxonomyInfo, scientificName, locale string, includeSubspecies bool) {
	if c.EBirdClient == nil || (!includeSubspecies && locale == "") {
		return
	}

	c.Debug("Enhancing local taxonomy data with eBird API for subspecies/locale")
	ebirdInfo, err := c.getEBirdTaxonomy(ctx, scientificName, locale, includeSubspecies)
	if err != nil {
		return
	}

	if includeSubspecies && len(ebirdInfo.Subspecies) > 0 {
		info.Subspecies = ebirdInfo.Subspecies
	}
	if ebirdInfo.SpeciesCode != "" {
		info.SpeciesCode = ebirdInfo.SpeciesCode
	}
	info.Metadata["source"] = "local+ebird"
	if locale != "" {
		info.Metadata["locale"] = locale
	}
}

// getEBirdTaxonomy retrieves taxonomy information from eBird API
func (c *Handler) getEBirdTaxonomy(ctx context.Context, scientificName, locale string, includeSubspecies bool) (*TaxonomyInfo, error) {
	// Get full taxonomy data with locale if specified
	taxonomyData, err := c.EBirdClient.GetTaxonomy(ctx, locale)
	if err != nil {
		return nil, err
	}

	// Find the species in taxonomy
	var speciesEntry *ebird.TaxonomyEntry
	for i := range taxonomyData {
		if strings.EqualFold(taxonomyData[i].ScientificName, scientificName) {
			speciesEntry = &taxonomyData[i]
			break
		}
	}

	if speciesEntry == nil {
		return nil, errors.Newf("species '%s' not found in eBird taxonomy", scientificName).
			Category(errors.CategoryNotFound).
			Context("scientific_name", scientificName).
			Component("api-species").
			Build()
	}

	// Create taxonomy info
	info := &TaxonomyInfo{
		ScientificName: speciesEntry.ScientificName,
		SpeciesCode:    speciesEntry.SpeciesCode,
		Metadata: map[string]any{
			"source":     "ebird",
			"updated_at": time.Now().Format(time.RFC3339),
			"locale":     locale,
		},
	}

	// Parse genus from scientific name
	parts := strings.Fields(speciesEntry.ScientificName)
	genus := ""
	if len(parts) > 0 {
		genus = parts[0]
	}

	info.Taxonomy = TaxonomyHierarchy{
		Kingdom:       "Animalia", // All birds are in kingdom Animalia
		Phylum:        "Chordata", // All birds are in phylum Chordata
		Class:         "Aves",     // All entries are birds
		Order:         speciesEntry.Order,
		Family:        speciesEntry.FamilySciName,
		FamilyCommon:  speciesEntry.FamilyComName,
		Genus:         genus,
		Species:       speciesEntry.ScientificName,
		SpeciesCommon: speciesEntry.CommonName,
	}

	// Add subspecies if requested and it's a species entry
	if includeSubspecies && speciesEntry.Category == "species" {
		subspecies := c.findDetailedSubspecies(taxonomyData, speciesEntry.SpeciesCode)
		info.Subspecies = subspecies
	}

	// TODO: Add conservation status and native regions when available from eBird API

	return info, nil
}

// findDetailedSubspecies finds all subspecies with detailed information
func (c *Handler) findDetailedSubspecies(taxonomy []ebird.TaxonomyEntry, speciesCode string) []SubspeciesInfo {
	var subspecies []SubspeciesInfo

	for i := range taxonomy {
		// Check if this entry reports as our species and is a subspecies category
		if taxonomy[i].ReportAs == speciesCode &&
			(taxonomy[i].Category == "issf" || taxonomy[i].Category == "form") {

			// Extract region from common name if present (often in parentheses)
			region := ""
			commonName := taxonomy[i].CommonName
			if start := strings.Index(commonName, "("); start != -1 {
				if end := strings.Index(commonName[start:], ")"); end != -1 {
					region = strings.TrimSpace(commonName[start+1 : start+end])
				}
			}

			subspecies = append(subspecies, SubspeciesInfo{
				ScientificName: taxonomy[i].ScientificName,
				CommonName:     taxonomy[i].CommonName,
				Region:         region,
			})
		}
	}

	return subspecies
}

// GetSpeciesThumbnail retrieves a bird thumbnail image by species code
// GET /api/v2/species/:code/thumbnail
func (c *Handler) GetSpeciesThumbnail(ctx echo.Context) error {
	speciesCode := ctx.Param("code")
	if speciesCode == "" {
		return c.HandleError(ctx, errors.Newf("species code parameter is required").
			Category(errors.CategoryValidation).
			Component("api-species").
			Build(), "Missing species code", http.StatusBadRequest)
	}

	// Log the request if API logger is available
	c.LogDebugIfEnabled("Retrieving thumbnail for species code",
		logger.String("species_code", speciesCode),
		logger.String("ip", ctx.RealIP()),
		logger.String("path", ctx.Request().URL.Path),
	)

	// Snapshot to avoid TOCTOU race on c.Processor
	proc := c.Processor
	if proc == nil || proc.Bn == nil {
		return c.HandleError(ctx, errors.Newf("BirdNET processor not available").
			Category(errors.CategorySystem).
			Component("api-species").
			Build(), "BirdNET service unavailable", http.StatusServiceUnavailable)
	}

	cache := c.BirdImageCache
	if cache == nil {
		return c.HandleError(ctx, errors.Newf("image service unavailable").
			Category(errors.CategorySystem).
			Component("api-species").
			Build(), "Image service unavailable", http.StatusServiceUnavailable)
	}

	// Get species name from the taxonomy map using the species code
	bn := proc.Bn
	speciesName, exists := bn.GetSpeciesNameFromCode(speciesCode)

	if !exists {
		return c.HandleError(ctx, errors.Newf("species code '%s' not found in taxonomy", speciesCode).
			Category(errors.CategoryNotFound).
			Context("species_code", speciesCode).
			Component("api-species").
			Build(), "Species not found", http.StatusNotFound)
	}

	// Split the species name to get scientific name
	scientificName, _ := classifier.SplitSpeciesName(speciesName)

	if scientificName == "" {
		return c.HandleError(ctx, errors.Newf("invalid species name format for code '%s'", speciesCode).
			Category(errors.CategoryValidation).
			Context("species_code", speciesCode).
			Context("species_name", speciesName).
			Component("api-species").
			Build(), "Invalid species data", http.StatusInternalServerError)
	}

	// Delegate to the image proxy handler
	ctx.SetParamNames("scientific_name")
	ctx.SetParamValues(scientificName)
	return c.serveImageProxy(ctx)
}
