// species_guide.go implements the species guide, notes, and similar-species API
// endpoints backed by the guideprovider cache and the species-notes datastore.
package api

import (
	"context"
	"net/http"
	"net/url"
	"strings"
	"sync"
	"time"

	"github.com/labstack/echo/v4"
	"github.com/labstack/echo/v4/middleware"

	"github.com/tphakala/birdnet-go/internal/classifier"
	"github.com/tphakala/birdnet-go/internal/conf"
	"github.com/tphakala/birdnet-go/internal/datastore"
	"github.com/tphakala/birdnet-go/internal/detection"
	"github.com/tphakala/birdnet-go/internal/errors"
	"github.com/tphakala/birdnet-go/internal/guideprovider"
)

// Guide quality classifications.
const (
	guideQualityFull      = "full"
	guideQualityIntroOnly = "intro_only"
	guideQualityStub      = "stub"
)

// Expectedness classifications.
const (
	expectednessExpected   = "expected"
	expectednessUncommon   = "uncommon"
	expectednessRare       = "rare"
	expectednessUnexpected = "unexpected"
)

// Similar-species relationship labels.
const (
	relationshipSameGenus  = "same_genus"
	relationshipSameFamily = "same_family"
)

const (
	// guideRateLimitPerMinute bounds calls to the external-API-backed endpoints.
	guideRateLimitPerMinute = 30
	// maxSimilarSpecies caps the number of similar-species candidates resolved.
	maxSimilarSpecies = 8
	// similarSpeciesResolveTimeout bounds the worst-case latency of the
	// similar-species panel: candidates fan out concurrent guide lookups that may
	// trigger live (external) fetches on a cold cache, so cap how long the whole
	// resolution may block. Candidates that don't resolve in time return
	// name-only (best-effort enrichment); the cache still persists any fetch that
	// completes after the deadline for the next request.
	similarSpeciesResolveTimeout = 8 * time.Second
	// guideSummaryMaxLength caps the per-candidate guide summary length (bytes).
	guideSummaryMaxLength = 280
	// guideDescriptionShortThreshold is the byte length under which a guide with
	// no sections is treated as a stub rather than intro-only.
	guideDescriptionShortThreshold = 80
)

// errGuideCacheUnavailable is returned by the accessor when no cache is wired.
var errGuideCacheUnavailable = errors.Newf("species guide cache unavailable").
	Component("api-species-guide").
	Category(errors.CategorySystem).
	Build()

// --- Response shapes (authoritative; mirrored by frontend types in species.ts) ---

// GuideFeatureFlags reports which guide sections the configuration enables.
type GuideFeatureFlags struct {
	Notes          bool `json:"notes"`
	Enrichments    bool `json:"enrichments"`
	SimilarSpecies bool `json:"similar_species"`
}

// GuideSource describes the data source and license of a guide.
type GuideSource struct {
	Provider   string `json:"provider"`
	URL        string `json:"url"`
	License    string `json:"license"`
	LicenseURL string `json:"license_url"`
}

// GuideExternalLink is a labeled external resource link.
type GuideExternalLink struct {
	Name string `json:"name"`
	URL  string `json:"url"`
}

// SpeciesGuideData is the response body for GET /species/:name/guide.
type SpeciesGuideData struct {
	ScientificName string              `json:"scientific_name"`
	CommonName     string              `json:"common_name"`
	Description    string              `json:"description"`
	Quality        string              `json:"quality"`
	Expectedness   string              `json:"expectedness,omitempty"`
	CurrentSeason  string              `json:"current_season,omitempty"`
	ExternalLinks  []GuideExternalLink `json:"external_links,omitempty"`
	Features       GuideFeatureFlags   `json:"features"`
	Source         GuideSource         `json:"source"`
	Partial        bool                `json:"partial"`
	CachedAt       string              `json:"cached_at"`
}

// SimilarSpeciesSections holds parsed guide sections for a similar species.
type SimilarSpeciesSections struct {
	Description    string   `json:"description,omitempty"`
	SongsAndCalls  string   `json:"songs_and_calls,omitempty"`
	SimilarSpecies []string `json:"similar_species,omitempty"`
}

// SimilarSpeciesEntry is one related species in the comparison panel.
type SimilarSpeciesEntry struct {
	ScientificName string                  `json:"scientific_name"`
	CommonName     string                  `json:"common_name"`
	Relationship   string                  `json:"relationship"`
	GuideSummary   string                  `json:"guide_summary,omitempty"`
	Sections       *SimilarSpeciesSections `json:"sections,omitempty"`
}

// SimilarSpeciesResponse is the response body for GET /species/:name/similar.
type SimilarSpeciesResponse struct {
	ScientificName string                `json:"scientific_name"`
	Genus          string                `json:"genus"`
	Similar        []SimilarSpeciesEntry `json:"similar"`
}

// SpeciesNoteData is the response body for a single note.
type SpeciesNoteData struct {
	ID        uint   `json:"id"`
	Entry     string `json:"entry"`
	CreatedAt string `json:"created_at"`
	UpdatedAt string `json:"updated_at"`
}

// createSpeciesNoteRequest is the body for creating/updating a note.
type createSpeciesNoteRequest struct {
	Entry string `json:"entry"`
}

// --- Controller cache plumbing (hot-reload safe) ---

// WithGuideCache snapshots the guide cache pointer under a read lock and runs fn
// outside the lock, so hot-reload swaps never block readers. Returns
// errGuideCacheUnavailable when no cache is configured.
func (c *Controller) WithGuideCache(fn func(*guideprovider.GuideCache) error) error {
	c.guideCacheMu.RLock()
	gc := c.guideCache
	c.guideCacheMu.RUnlock()
	if gc == nil {
		return errGuideCacheUnavailable
	}
	return fn(gc)
}

// SetGuideCache atomically swaps in a new guide cache and closes the previous one
// OUTSIDE the lock so it never blocks concurrent WithGuideCache readers.
func (c *Controller) SetGuideCache(gc *guideprovider.GuideCache) {
	c.guideCacheMu.Lock()
	old := c.guideCache
	c.guideCache = gc
	c.guideCacheMu.Unlock()
	if old != nil && old != gc {
		old.Close()
	}
}

// --- Routes ---

// initSpeciesGuideRoutes registers the species guide, similar-species, and notes
// endpoints. Called from initSpeciesRoutes.
func (c *Controller) initSpeciesGuideRoutes() {
	guideRateLimiter := c.newGuideRateLimiter()

	// Guide + similar are rate-limited (external API calls).
	c.Group.GET("/species/:scientific_name/guide", c.GetSpeciesGuide, guideRateLimiter)
	c.Group.GET("/species/:scientific_name/similar", c.GetSimilarSpecies, guideRateLimiter)

	// Notes — reads public, writes auth-gated.
	c.Group.GET("/species/:scientific_name/notes", c.GetSpeciesNotes)
	c.Group.POST("/species/:scientific_name/notes", c.CreateSpeciesNote, c.authMiddleware)
	c.Group.PUT("/species/notes/:id", c.UpdateSpeciesNote, c.authMiddleware)
	c.Group.DELETE("/species/notes/:id", c.DeleteSpeciesNote, c.authMiddleware)
}

// newGuideRateLimiter builds the shared rate limiter for guide/similar endpoints.
func (c *Controller) newGuideRateLimiter() echo.MiddlewareFunc {
	return middleware.RateLimiterWithConfig(middleware.RateLimiterConfig{
		Store: middleware.NewRateLimiterMemoryStoreWithConfig(
			middleware.RateLimiterMemoryStoreConfig{
				Rate:      guideRateLimitPerMinute,
				ExpiresIn: 1 * time.Minute,
			},
		),
		IdentifierExtractor: middleware.DefaultRateLimiterConfig.IdentifierExtractor,
		ErrorHandler: func(ctx echo.Context, _ error) error {
			return ctx.JSON(http.StatusTooManyRequests, map[string]string{
				"error": "Rate limit exceeded for species guide requests",
			})
		},
		DenyHandler: func(ctx echo.Context, _ string, _ error) error {
			return ctx.JSON(http.StatusTooManyRequests, map[string]string{
				"error": "Too many species guide requests, please wait before trying again",
			})
		},
	})
}

// parseScientificNameParam extracts and validates the :scientific_name path param.
// Echo decodes percent-encoding, so spaces arrive intact.
func parseScientificNameParam(ctx echo.Context) (string, error) {
	name := strings.TrimSpace(ctx.Param("scientific_name"))
	if name == "" {
		// Fall back to the first positional param in case of param-name drift.
		if vals := ctx.ParamValues(); len(vals) > 0 {
			name = strings.TrimSpace(vals[0])
		}
	}
	if name == "" {
		return "", errors.Newf("scientific_name parameter is required").
			Category(errors.CategoryValidation).
			Component("api-species-guide").
			Build()
	}
	return name, nil
}

// handleScientificNameError maps a scientific-name parse error to a 400 response.
func (c *Controller) handleScientificNameError(ctx echo.Context, err error) error {
	return c.HandleError(ctx, err, "Invalid scientific name", http.StatusBadRequest)
}

// guideLocale resolves the request locale: query param, then dashboard locale, then "en".
func guideLocale(ctx echo.Context, settings *conf.Settings) string {
	if l := strings.TrimSpace(ctx.QueryParam("locale")); l != "" {
		return l
	}
	if settings != nil {
		if l := strings.TrimSpace(settings.Realtime.Dashboard.Locale); l != "" {
			return l
		}
	}
	return "en"
}

// --- Handlers ---

// GetSpeciesGuide returns the species guide for a detected species.
// GET /api/v2/species/:scientific_name/guide
func (c *Controller) GetSpeciesGuide(ctx echo.Context) error {
	name, err := parseScientificNameParam(ctx)
	if err != nil {
		return c.handleScientificNameError(ctx, err)
	}

	settings := c.currentSettings()
	cfg := settings.Realtime.Dashboard.SpeciesGuide
	if !cfg.Enabled {
		return c.HandleError(ctx, errors.Newf("species guide is disabled").
			Category(errors.CategoryConfiguration).
			Component("api-species-guide").
			Build(), "Species guide is not enabled", http.StatusNotFound)
	}

	locale := guideLocale(ctx, settings)

	var guide *guideprovider.SpeciesGuide
	err = c.WithGuideCache(func(gc *guideprovider.GuideCache) error {
		g, gErr := gc.Get(ctx.Request().Context(), name, guideprovider.FetchOptions{Locale: locale})
		if gErr != nil {
			return gErr
		}
		guide = g
		return nil
	})
	if err != nil {
		if errors.Is(err, errGuideCacheUnavailable) {
			return c.HandleError(ctx, err, "Species guide is temporarily unavailable", http.StatusServiceUnavailable)
		}
		if errors.Is(err, guideprovider.ErrGuideNotFound) {
			return c.HandleError(ctx, err, "No guide found for species", http.StatusNotFound)
		}
		return c.HandleError(ctx, err, "Failed to fetch species guide", http.StatusBadGateway)
	}
	if guide == nil || guide.IsNegativeEntry() {
		return c.HandleError(ctx, errors.Newf("no guide content").
			Category(errors.CategoryNotFound).
			Component("api-species-guide").
			Build(), "No guide found for species", http.StatusNotFound)
	}

	data := SpeciesGuideData{
		ScientificName: guide.ScientificName,
		CommonName:     guide.CommonName,
		Description:    guide.Description,
		Quality:        classifyGuideQuality(guide.Description, guide.Partial),
		CurrentSeason:  computeCurrentSeason(settings.BirdNET.Latitude, time.Now()),
		ExternalLinks:  buildExternalLinks(guide.CommonName, guide.ScientificName),
		Features: GuideFeatureFlags{
			Notes:          cfg.IsShowNotes(),
			Enrichments:    cfg.IsShowEnrichments(),
			SimilarSpecies: cfg.IsShowSimilarSpecies(),
		},
		Source: GuideSource{
			Provider:   guide.SourceProvider,
			URL:        guide.SourceURL,
			License:    guide.License,
			LicenseURL: guide.LicenseURL,
		},
		Partial:  guide.Partial,
		CachedAt: guide.CachedAt.Format(time.RFC3339),
	}
	if exp := c.guideExpectedness(name); exp != "" {
		data.Expectedness = exp
	}

	return ctx.JSON(http.StatusOK, data)
}

// GetSimilarSpecies returns same-genus / same-family / similar species with summaries.
// GET /api/v2/species/:scientific_name/similar
func (c *Controller) GetSimilarSpecies(ctx echo.Context) error {
	name, err := parseScientificNameParam(ctx)
	if err != nil {
		return c.handleScientificNameError(ctx, err)
	}

	settings := c.currentSettings()
	if !settings.Realtime.Dashboard.SpeciesGuide.Enabled {
		return c.HandleError(ctx, errors.Newf("species guide is disabled").
			Category(errors.CategoryConfiguration).
			Component("api-species-guide").
			Build(), "Species guide is not enabled", http.StatusNotFound)
	}
	if c.TaxonomyDB == nil {
		return c.HandleError(ctx, errors.Newf("taxonomy database not available").
			Category(errors.CategorySystem).
			Component("api-species-guide").
			Build(), "Taxonomy database not available", http.StatusServiceUnavailable)
	}

	locale := guideLocale(ctx, settings)
	genus, candidates := c.similarSpeciesCandidates(name)

	entries := c.resolveSimilarSpecies(ctx.Request().Context(), candidates, locale)

	return ctx.JSON(http.StatusOK, SimilarSpeciesResponse{
		ScientificName: name,
		Genus:          genus,
		Similar:        entries,
	})
}

// similarCandidate is an intermediate candidate before guide resolution.
type similarCandidate struct {
	scientificName string
	relationship   string
}

// similarSpeciesCandidates resolves same-genus then same-family candidates,
// excluding the focal species and capped at maxSimilarSpecies.
func (c *Controller) similarSpeciesCandidates(focal string) (genus string, candidates []similarCandidate) {
	genusName, meta, err := c.TaxonomyDB.GetGenusByScientificName(focal)
	if err != nil || genusName == "" {
		// Fall back to the first token of the scientific name. meta stays nil,
		// so the same-family branch below is skipped (it has no family to use).
		genusName, _, _ = strings.Cut(focal, " ")
	}

	seen := map[string]struct{}{strings.ToLower(focal): {}}
	add := func(names []string, relationship string) {
		for _, n := range names {
			if len(candidates) >= maxSimilarSpecies {
				return
			}
			key := strings.ToLower(strings.TrimSpace(n))
			if key == "" {
				continue
			}
			if _, dup := seen[key]; dup {
				continue
			}
			seen[key] = struct{}{}
			candidates = append(candidates, similarCandidate{scientificName: n, relationship: relationship})
		}
	}

	if genusName != "" {
		add(c.TaxonomyDB.LookupAllSpeciesInGenus(genusName), relationshipSameGenus)
	}
	// Reuse the family metadata resolved above rather than looking it up again.
	if len(candidates) < maxSimilarSpecies && meta != nil {
		add(c.TaxonomyDB.LookupAllSpeciesInFamily(meta.Family), relationshipSameFamily)
	}
	return genusName, candidates
}

// resolveSimilarSpecies fetches each candidate's guide in parallel and builds the
// response entries, preserving candidate order.
func (c *Controller) resolveSimilarSpecies(ctx context.Context, candidates []similarCandidate, locale string) []SimilarSpeciesEntry {
	// Bound the whole fan-out so a cold cache (live external fetches) cannot
	// block the request indefinitely; unresolved candidates fall back to
	// name-only below.
	resolveCtx, cancel := context.WithTimeout(ctx, similarSpeciesResolveTimeout)
	defer cancel()

	entries := make([]SimilarSpeciesEntry, len(candidates))
	var wg sync.WaitGroup
	for i := range candidates {
		idx := i // capture per-iteration index for the goroutine
		wg.Go(func() {
			cand := candidates[idx]
			entry := SimilarSpeciesEntry{
				ScientificName: cand.scientificName,
				Relationship:   cand.relationship,
			}
			_ = c.WithGuideCache(func(gc *guideprovider.GuideCache) error {
				g, err := gc.Get(resolveCtx, cand.scientificName, guideprovider.FetchOptions{Locale: locale})
				if err != nil || g == nil || g.IsNegativeEntry() {
					return nil //nolint:nilerr // best-effort enrichment; missing guide is fine
				}
				entry.CommonName = g.CommonName
				entry.GuideSummary = summarizeDescription(g.Description)
				entry.Sections = extractSections(g.Description, namesOf(g.SimilarSpecies))
				return nil
			})
			entries[idx] = entry
		})
	}
	wg.Wait()
	return entries
}

// GetSpeciesNotes returns all notes for a species (public).
// GET /api/v2/species/:scientific_name/notes
func (c *Controller) GetSpeciesNotes(ctx echo.Context) error {
	name, err := parseScientificNameParam(ctx)
	if err != nil {
		return c.handleScientificNameError(ctx, err)
	}
	if dsErr := c.requireDatastore(ctx); dsErr != nil {
		return dsErr
	}

	notes, err := c.DS.GetSpeciesNotes(ctx.Request().Context(), name)
	if err != nil {
		return c.HandleError(ctx, err, "Failed to fetch notes", http.StatusInternalServerError)
	}

	data := make([]SpeciesNoteData, 0, len(notes))
	for i := range notes {
		data = append(data, toSpeciesNoteData(&notes[i]))
	}
	return ctx.JSON(http.StatusOK, data)
}

// CreateSpeciesNote creates a note for a species (auth-gated).
// POST /api/v2/species/:scientific_name/notes
func (c *Controller) CreateSpeciesNote(ctx echo.Context) error {
	name, err := parseScientificNameParam(ctx)
	if err != nil {
		return c.handleScientificNameError(ctx, err)
	}
	if dsErr := c.requireDatastore(ctx); dsErr != nil {
		return dsErr
	}

	var req createSpeciesNoteRequest
	if bindErr := ctx.Bind(&req); bindErr != nil {
		return c.HandleError(ctx, bindErr, "Invalid request body", http.StatusBadRequest)
	}

	note := &datastore.SpeciesNote{ScientificName: name, Entry: req.Entry}
	if saveErr := c.DS.SaveSpeciesNote(ctx.Request().Context(), note); saveErr != nil {
		return c.handleSpeciesNoteWriteError(ctx, saveErr, "Failed to save note")
	}
	return ctx.JSON(http.StatusCreated, toSpeciesNoteData(note))
}

// UpdateSpeciesNote updates a note's entry (auth-gated).
// PUT /api/v2/species/notes/:id
func (c *Controller) UpdateSpeciesNote(ctx echo.Context) error {
	id := strings.TrimSpace(ctx.Param("id"))
	if id == "" {
		return c.HandleError(ctx, errors.Newf("note id is required").
			Category(errors.CategoryValidation).Component("api-species-guide").Build(),
			"Missing note id", http.StatusBadRequest)
	}
	if dsErr := c.requireDatastore(ctx); dsErr != nil {
		return dsErr
	}

	var req createSpeciesNoteRequest
	if bindErr := ctx.Bind(&req); bindErr != nil {
		return c.HandleError(ctx, bindErr, "Invalid request body", http.StatusBadRequest)
	}

	if err := c.DS.UpdateSpeciesNote(ctx.Request().Context(), id, req.Entry); err != nil {
		if errors.Is(err, datastore.ErrSpeciesNoteNotFound) {
			return c.HandleError(ctx, err, "Note not found", http.StatusNotFound)
		}
		return c.handleSpeciesNoteWriteError(ctx, err, "Failed to update note")
	}
	return ctx.NoContent(http.StatusNoContent)
}

// DeleteSpeciesNote deletes a note (auth-gated).
// DELETE /api/v2/species/notes/:id
func (c *Controller) DeleteSpeciesNote(ctx echo.Context) error {
	id := strings.TrimSpace(ctx.Param("id"))
	if id == "" {
		return c.HandleError(ctx, errors.Newf("note id is required").
			Category(errors.CategoryValidation).Component("api-species-guide").Build(),
			"Missing note id", http.StatusBadRequest)
	}
	if dsErr := c.requireDatastore(ctx); dsErr != nil {
		return dsErr
	}

	if err := c.DS.DeleteSpeciesNote(ctx.Request().Context(), id); err != nil {
		if errors.Is(err, datastore.ErrSpeciesNoteNotFound) {
			return c.HandleError(ctx, err, "Note not found", http.StatusNotFound)
		}
		return c.HandleError(ctx, err, "Failed to delete note", http.StatusInternalServerError)
	}
	return ctx.NoContent(http.StatusNoContent)
}

// handleSpeciesNoteWriteError maps validation errors (e.g. too long) to 400 with
// an i18n key the UI can show, and other errors to 500.
func (c *Controller) handleSpeciesNoteWriteError(ctx echo.Context, err error, message string) error {
	if errors.IsCategory(err, errors.CategoryValidation) {
		return c.HandleErrorWithKey(ctx, err, message, http.StatusBadRequest,
			"analytics.species.notes.tooLong", map[string]any{"max": datastore.SpeciesNoteMaxLength})
	}
	return c.HandleError(ctx, err, message, http.StatusInternalServerError)
}

// --- Helpers ---

// toSpeciesNoteData maps a datastore note to its API shape.
func toSpeciesNoteData(n *datastore.SpeciesNote) SpeciesNoteData {
	return SpeciesNoteData{
		ID:        n.ID,
		Entry:     n.Entry,
		CreatedAt: n.CreatedAt.Format(time.RFC3339),
		UpdatedAt: n.UpdatedAt.Format(time.RFC3339),
	}
}

// classifyGuideQuality classifies a guide's completeness for the UI badge.
func classifyGuideQuality(description string, partial bool) string {
	trimmed := strings.TrimSpace(description)
	switch {
	case len(trimmed) < guideDescriptionShortThreshold:
		return guideQualityStub
	case !strings.Contains(trimmed, "## ") || partial:
		return guideQualityIntroOnly
	default:
		return guideQualityFull
	}
}

// guideRarityTTL bounds how long the cached probable-species score map is reused.
// The geomodel prediction behind it is the per-request cost we're avoiding; 60s
// of staleness after a location/settings change is acceptable for a badge.
const guideRarityTTL = 60 * time.Second

// guideExpectedness returns the expectedness classification for a species, or ""
// when the rarity model is unavailable or has no coverage for the species. It
// uses a short-lived cache of the probable-species scores so a rate-limited
// burst of guide requests doesn't re-run the geomodel prediction per call.
func (c *Controller) guideExpectedness(scientificName string) string {
	proc := c.Processor
	if proc == nil || proc.Bn == nil {
		return ""
	}
	scores := c.probableSpeciesScores(proc.Bn)
	if scores == nil {
		return ""
	}
	key := strings.ToLower(detection.ExtractScientificName(scientificName))
	if score, ok := scores[key]; ok {
		return scoreToExpectedness(score)
	}
	// Not in today's probable list: rare if the geomodel covers it at all,
	// otherwise unknown (omit). Mirrors getSpeciesRarityInfo's semantics.
	if speciesHasGeomodelCoverage(proc.Bn, scientificName) {
		return scoreToExpectedness(0)
	}
	return ""
}

// probableSpeciesScores returns a cached map of normalized scientific name ->
// geomodel occurrence score, rebuilding it when the TTL expires. Returns nil
// when the prediction is unavailable (caller omits expectedness).
func (c *Controller) probableSpeciesScores(bn *classifier.Orchestrator) map[string]float64 {
	// Fast path: while the memoized map is fresh, a concurrent burst of guide
	// requests shares it under a read lock without serializing on the rebuild.
	c.guideRarityMu.RLock()
	if c.guideRarityScores != nil && time.Now().Before(c.guideRarityExpiry) {
		scores := c.guideRarityScores
		c.guideRarityMu.RUnlock()
		return scores
	}
	c.guideRarityMu.RUnlock()

	c.guideRarityMu.Lock()
	defer c.guideRarityMu.Unlock()

	// Re-check under the write lock: another goroutine may have rebuilt the map
	// while we waited for the lock, so only one geomodel prediction runs per TTL.
	if c.guideRarityScores != nil && time.Now().Before(c.guideRarityExpiry) {
		return c.guideRarityScores
	}

	today := time.Now().Truncate(HoursPerDay * time.Hour)
	speciesScores, err := bn.GetProbableSpecies(today, 0.0)
	if err != nil {
		return nil
	}

	scores := make(map[string]float64, len(speciesScores))
	for _, ss := range speciesScores {
		scores[strings.ToLower(detection.ExtractScientificName(ss.Label))] = ss.Score
	}
	c.guideRarityScores = scores
	c.guideRarityExpiry = time.Now().Add(guideRarityTTL)
	return scores
}

// scoreToExpectedness maps a geomodel occurrence score to an expectedness label.
func scoreToExpectedness(score float64) string {
	switch {
	case score > RarityThresholdCommon:
		return expectednessExpected
	case score > RarityThresholdUncommon:
		return expectednessUncommon
	case score > RarityThresholdRare:
		return expectednessRare
	default:
		return expectednessUnexpected
	}
}

// computeCurrentSeason returns a hemisphere-aware season token for the given
// latitude and time. Near the equator it returns wet/dry-season tokens.
func computeCurrentSeason(latitude float64, now time.Time) string {
	month := now.Month()

	// Equatorial band: bimodal wet/dry seasons.
	const equatorialBand = 10.0
	if latitude <= equatorialBand && latitude >= -equatorialBand {
		switch month {
		case time.March, time.April, time.May:
			return "wet1"
		case time.June, time.July, time.August:
			return "dry1"
		case time.September, time.October, time.November:
			return "wet2"
		default: // Dec, Jan, Feb
			return "dry2"
		}
	}

	northern := latitude >= 0
	switch month {
	case time.March, time.April, time.May:
		return seasonForHemisphere(northern, "spring", "autumn")
	case time.June, time.July, time.August:
		return seasonForHemisphere(northern, "summer", "winter")
	case time.September, time.October, time.November:
		return seasonForHemisphere(northern, "autumn", "spring")
	default: // Dec, Jan, Feb
		return seasonForHemisphere(northern, "winter", "summer")
	}
}

func seasonForHemisphere(northern bool, northSeason, southSeason string) string {
	if northern {
		return northSeason
	}
	return southSeason
}

// buildExternalLinks builds external resource links for a species.
func buildExternalLinks(commonName, scientificName string) []GuideExternalLink {
	links := make([]GuideExternalLink, 0, 3)
	if scientificName != "" {
		wikiTitle := strings.ReplaceAll(scientificName, " ", "_")
		links = append(links,
			GuideExternalLink{
				Name: "Wikipedia",
				URL:  "https://en.wikipedia.org/wiki/" + url.PathEscape(wikiTitle),
			},
			GuideExternalLink{
				Name: "eBird",
				URL:  "https://ebird.org/search?q=" + url.QueryEscape(scientificName),
			},
			GuideExternalLink{
				Name: "Xeno-canto",
				URL:  "https://xeno-canto.org/explore?query=" + url.QueryEscape(scientificName),
			},
		)
	}
	return links
}

// summarizeDescription returns a short, single-paragraph summary of a description.
func summarizeDescription(description string) string {
	intro := description
	if idx := strings.Index(description, "## "); idx >= 0 {
		intro = description[:idx]
	}
	intro = strings.TrimSpace(intro)
	if len(intro) > guideSummaryMaxLength {
		intro = strings.TrimSpace(intro[:guideSummaryMaxLength])
	}
	return intro
}

// songsHeadingTokens are localized fragments identifying a "songs and calls"
// section heading (lowercased).
var songsHeadingTokens = []string{
	"song", "call", "voice", "stimme", "voix", "chant", "voz", "canto", "głos",
	"ääntely", "läte",
}

// extractSections parses a guide description into structured sections for the
// similar-species panel. Heading matching is language-agnostic via
// songsHeadingTokens, so no locale is needed.
func extractSections(description string, similar []string) *SimilarSpeciesSections {
	trimmed := strings.TrimSpace(description)
	if trimmed == "" && len(similar) == 0 {
		return nil
	}
	secs := &SimilarSpeciesSections{SimilarSpecies: similar}
	for _, sec := range splitGuideSections(trimmed) {
		heading := strings.ToLower(strings.TrimSpace(sec.heading))
		switch {
		case heading == "":
			secs.Description = sec.body
		case headingMatchesSongs(heading):
			secs.SongsAndCalls = sec.body
		}
	}
	return secs
}

// guideSection is a heading/body pair parsed from a description.
type guideSection struct {
	heading string
	body    string
}

// splitGuideSections splits a description on "## " headers, mirroring the
// frontend parseGuideDescription contract.
func splitGuideSections(description string) []guideSection {
	startsWithHeader := strings.HasPrefix(strings.TrimSpace(description), "## ")
	parts := strings.Split(description, "## ")
	sections := make([]guideSection, 0, len(parts))
	for i, part := range parts {
		trimmed := strings.TrimSpace(part)
		if trimmed == "" {
			continue
		}
		// The leading chunk (before any header) has no heading.
		if i == 0 && !startsWithHeader {
			sections = append(sections, guideSection{heading: "", body: trimmed})
			continue
		}
		heading, body, found := strings.Cut(trimmed, "\n")
		if !found {
			sections = append(sections, guideSection{heading: strings.TrimSpace(heading), body: ""})
			continue
		}
		sections = append(sections, guideSection{
			heading: strings.TrimSpace(heading),
			body:    strings.TrimSpace(body),
		})
	}
	return sections
}

func headingMatchesSongs(lowerHeading string) bool {
	for _, token := range songsHeadingTokens {
		if strings.Contains(lowerHeading, token) {
			return true
		}
	}
	return false
}

// namesOf extracts scientific names from a similar-species list.
func namesOf(list []guideprovider.SimilarSpecies) []string {
	if len(list) == 0 {
		return nil
	}
	out := make([]string, 0, len(list))
	for _, s := range list {
		out = append(out, s.ScientificName)
	}
	return out
}
