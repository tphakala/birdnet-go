// species_guide.go implements the species guide, notes, and similar-species API
// endpoints backed by the guideprovider cache and the species-notes datastore.
package api

import (
	"context"
	"fmt"
	"net/http"
	"net/url"
	"strings"
	"sync"
	"time"
	"unicode"

	"github.com/labstack/echo/v4"
	"github.com/labstack/echo/v4/middleware"
	"golang.org/x/time/rate"

	"github.com/tphakala/birdnet-go/internal/classifier"
	"github.com/tphakala/birdnet-go/internal/conf"
	"github.com/tphakala/birdnet-go/internal/datastore"
	"github.com/tphakala/birdnet-go/internal/detection"
	"github.com/tphakala/birdnet-go/internal/errors"
	"github.com/tphakala/birdnet-go/internal/guideprovider"
	"github.com/tphakala/birdnet-go/internal/logger"
	"github.com/tphakala/birdnet-go/internal/openfauna"
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

// External-link constants. Only eBird is hardcoded here: it is a runtime special
// case (its data is not in OpenFauna for licensing reasons). All other links come
// from the OpenFauna sources registry and the opt-in supplementary registry.
const (
	linkNameEBird = "eBird"
	linkIconEBird = "ebird"
)

// defaultWikiLang is the Wikipedia language subdomain used when the UI locale has
// no usable base-language subtag.
const defaultWikiLang = "en"

// scientificNameMaxLength bounds the accepted :scientific_name path parameter.
// Real binomial/trinomial names (and BirdNET's non-species labels) are well under
// this; the cap stops an arbitrarily long value from reaching the providers and the
// embedded-dataset lookups. It is a byte cap (cheap, and names are ASCII Latin).
const scientificNameMaxLength = 128

const (
	// guideRateLimitPerMinute bounds calls to the external-API-backed endpoints.
	// The limiter runs as middleware (before the handler), so it cannot tell a
	// warm cache hit from a live external fetch and counts both. Echo's limiter
	// Rate is in requests-per-second, so this per-minute value is divided by
	// SecondsPerMinute at construction; the cache's singleflight + stale-refresh
	// dedup already bounds the actual number of external provider calls.
	guideRateLimitPerMinute = 60
	// guideRateLimitBurst is the limiter burst allowance. The species comparison
	// panel issues two requests per open (guide + similar), so a generous burst
	// keeps a user clicking through detections from being throttled prematurely
	// while the sustained per-second rate still bounds abuse.
	guideRateLimitBurst = 30
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

// GuideExternalLink is a labeled external resource link with an icon hint the
// frontend maps to a bundled glyph (with a generic external-link fallback).
type GuideExternalLink struct {
	Name string `json:"name"`
	URL  string `json:"url"`
	Icon string `json:"icon,omitempty"`
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

// SimilarSpeciesEntry is one related species in the comparison panel.
type SimilarSpeciesEntry struct {
	ScientificName string `json:"scientific_name"`
	CommonName     string `json:"common_name"`
	Relationship   string `json:"relationship"`
	// HasGuide reports whether the candidate resolved to a guide with comparison
	// prose. The panel shows the comparison sections for these; for the rest it
	// shows ExternalLinks instead, so every selection is useful.
	HasGuide     bool   `json:"has_guide"`
	GuideSummary string `json:"guide_summary,omitempty"`
	// ExternalLinks is populated only for description-less entries (and only when
	// enrichments are enabled): the resource links shown when there is no prose to
	// compare. Entries with a description leave this nil and render sections.
	ExternalLinks []GuideExternalLink `json:"external_links,omitempty"`
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
				Rate:      rate.Limit(float64(guideRateLimitPerMinute) / float64(SecondsPerMinute)),
				Burst:     guideRateLimitBurst,
				ExpiresIn: 1 * time.Minute,
			},
		),
		// Per-client identification uses Echo's RealIP, which routes through the
		// controller's global trusted-proxy IP extractor (newTrustedProxyIPExtractor,
		// installed as e.IPExtractor). That extractor honors forwarded client-IP
		// headers (X-Forwarded-For, X-Real-IP, CF-Connecting-IP) ONLY when the TCP
		// peer is a trusted proxy; from any untrusted peer those headers are ignored
		// and the real socket address is used. So the per-IP limit is not spoofable
		// in a default deployment. The one way to weaken it is to misconfigure
		// Security.TrustedProxies to trust an attacker-reachable host — operators
		// behind a real reverse proxy must add ONLY that proxy there. Note this
		// limiter is a fairness layer; outbound provider calls are independently
		// capped by each provider's own rate limiter and by singleflight dedup.
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
	// Reject input that is not name-shaped before it reaches the providers and the
	// embedded-dataset memo. This keeps an arbitrary, attacker-supplied value from
	// being sent to Wikipedia's API as a page title and from accumulating one memoized
	// dataset-scan result per distinct garbage value. A name outside the dataset and
	// absent from Wikipedia would 404 regardless, so rejecting non-name input up front
	// only changes the status for input that could never resolve to a guide.
	if len(name) > scientificNameMaxLength {
		return "", errors.Newf("scientific_name exceeds %d characters", scientificNameMaxLength).
			Category(errors.CategoryValidation).
			Component("api-species-guide").
			Build()
	}
	if !isPlausibleScientificName(name) {
		return "", errors.Newf("scientific_name contains invalid characters").
			Category(errors.CategoryValidation).
			Component("api-species-guide").
			Build()
	}
	return name, nil
}

// isPlausibleScientificName reports whether s looks like a scientific name: only
// letters (any script, to stay locale-safe), spaces, hyphens, apostrophes, and
// periods. Every name in the embedded OpenFauna dataset (and BirdNET's binomial,
// hyphenated, and "x"-hybrid labels) matches this set. It is a cheap input filter,
// not a taxonomic check — it keeps obviously non-name input out of the providers and
// the dataset memo without trying to enumerate valid binomials.
func isPlausibleScientificName(s string) bool {
	for _, r := range s {
		switch {
		case unicode.IsLetter(r):
		case r == ' ' || r == '-' || r == '\'' || r == '.':
		default:
			return false
		}
	}
	return true
}

// handleScientificNameError maps a scientific-name parse error to a 400 response.
func (c *Controller) handleScientificNameError(ctx echo.Context, err error) error {
	return c.HandleError(ctx, err, "Invalid scientific name", http.StatusBadRequest)
}

// parseNoteIDParam returns the trimmed :id path param, or a validation error when absent.
func parseNoteIDParam(ctx echo.Context) (string, error) {
	id := strings.TrimSpace(ctx.Param("id"))
	if id == "" {
		return "", errors.Newf("note id is required").
			Category(errors.CategoryValidation).Component("api-species-guide").Build()
	}
	return id, nil
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
		switch {
		case errors.Is(err, context.Canceled):
			// Client disconnected mid-fetch (navigated away / closed the tab): an
			// expected lifecycle event, not a server or upstream error. Log at info
			// and return the client-closed status instead of a misleading 502.
			c.logInfoIfEnabled("species guide request canceled by client",
				logger.String("species", name), logger.Error(err))
			return c.HandleError(ctx, err, "Request canceled by client", StatusClientClosedRequest)
		case errors.Is(err, context.DeadlineExceeded):
			return c.HandleError(ctx, err, "Species guide request timed out", http.StatusRequestTimeout)
		case errors.Is(err, errGuideCacheUnavailable):
			return c.HandleError(ctx, err, "Species guide is temporarily unavailable", http.StatusServiceUnavailable)
		case errors.Is(err, guideprovider.ErrGuideNotFound):
			return c.HandleError(ctx, err, "No guide found for species", http.StatusNotFound)
		default:
			return c.HandleError(ctx, err, "Failed to fetch species guide", http.StatusBadGateway)
		}
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
	// Enrichments (expectedness, season, external links) are only rendered when
	// the showEnrichments setting is on; skip their cost (the geomodel prediction
	// in particular) when the feature flag is off.
	if cfg.IsShowEnrichments() {
		data.CurrentSeason = computeCurrentSeason(settings.BirdNET.Latitude, time.Now())
		data.ExternalLinks = externalLinksForGuide(
			guide.ScientificName,
			c.ebirdSpeciesCode(guide.ScientificName),
			locale,
			cfg.EnableSupplementaryLinks,
		)
		if exp := c.guideExpectedness(name); exp != "" {
			data.Expectedness = exp
		}
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

	// Links are enrichment data: only attach them to description-less entries when
	// enrichments are enabled, mirroring the main guide modal.
	withLinks := settings.Realtime.Dashboard.SpeciesGuide.IsShowEnrichments()
	supplementary := settings.Realtime.Dashboard.SpeciesGuide.EnableSupplementaryLinks
	entries := c.resolveSimilarSpecies(ctx.Request().Context(), candidates, locale, withLinks, supplementary)

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
func (c *Controller) resolveSimilarSpecies(ctx context.Context, candidates []similarCandidate, locale string, withLinks, supplementary bool) []SimilarSpeciesEntry {
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
				if strings.TrimSpace(g.Description) != "" {
					entry.GuideSummary = summarizeDescription(g.Description)
					entry.HasGuide = true
				} else if withLinks {
					// No prose to compare (e.g. an OpenFauna stub when Wikipedia is
					// disabled or the species was never warmed). Surface external
					// resource links instead so selecting the species is still useful.
					entry.ExternalLinks = externalLinksForGuide(
						cand.scientificName,
						c.ebirdSpeciesCode(cand.scientificName),
						locale,
						supplementary,
					)
				}
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
	id, err := parseNoteIDParam(ctx)
	if err != nil {
		return c.HandleError(ctx, err, "Missing note id", http.StatusBadRequest)
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
	id, err := parseNoteIDParam(ctx)
	if err != nil {
		return c.HandleError(ctx, err, "Missing note id", http.StatusBadRequest)
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

// handleSpeciesNoteWriteError maps note write failures to responses: the
// specific too-long case gets the localized "too long" key, other validation
// failures (empty entry, invalid note ID) get a plain 400 so they are not
// mislabeled as too long, and everything else is a 500.
func (c *Controller) handleSpeciesNoteWriteError(ctx echo.Context, err error, message string) error {
	switch {
	case errors.Is(err, datastore.ErrSpeciesNoteTooLong):
		return c.HandleErrorWithKey(ctx, err, message, http.StatusBadRequest,
			"analytics.species.notes.tooLong", map[string]any{"max": datastore.SpeciesNoteMaxLength})
	case errors.IsCategory(err, errors.CategoryValidation):
		return c.HandleError(ctx, err, message, http.StatusBadRequest)
	default:
		return c.HandleError(ctx, err, message, http.StatusInternalServerError)
	}
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

// guideRarityLocationKey returns a stable key for the configured observer
// location. The memoized probable-species map is invalidated when this changes so
// a location update is reflected immediately rather than after the TTL window.
func (c *Controller) guideRarityLocationKey() string {
	settings := c.currentSettings()
	if settings == nil {
		return ""
	}
	return fmt.Sprintf("%.4f,%.4f", settings.BirdNET.Latitude, settings.BirdNET.Longitude)
}

// probableSpeciesPredictor is the minimal classifier surface
// probableSpeciesScores needs. Declaring it as an interface keeps the memoization
// (double-checked locking + location-keyed invalidation) unit-testable without a
// loaded geomodel; *classifier.Orchestrator satisfies it.
type probableSpeciesPredictor interface {
	GetProbableSpecies(date time.Time, week float32) ([]classifier.SpeciesScore, error)
}

// probableSpeciesScores returns a cached map of normalized scientific name ->
// geomodel occurrence score, rebuilding it when the TTL expires or the configured
// location changes. Returns nil when the prediction is unavailable (caller omits
// expectedness).
func (c *Controller) probableSpeciesScores(bn probableSpeciesPredictor) map[string]float64 {
	locKey := c.guideRarityLocationKey()

	// Fast path: while the memoized map is fresh AND was computed for the current
	// location, a concurrent burst of guide requests shares it under a read lock
	// without serializing on the rebuild.
	c.guideRarityMu.RLock()
	if c.guideRarityScores != nil && c.guideRarityLocKey == locKey && time.Now().Before(c.guideRarityExpiry) {
		scores := c.guideRarityScores
		c.guideRarityMu.RUnlock()
		return scores
	}
	c.guideRarityMu.RUnlock()

	c.guideRarityMu.Lock()
	defer c.guideRarityMu.Unlock()

	// Re-check under the write lock: another goroutine may have rebuilt the map
	// while we waited for the lock, so only one geomodel prediction runs per
	// (location, TTL) window.
	if c.guideRarityScores != nil && c.guideRarityLocKey == locKey && time.Now().Before(c.guideRarityExpiry) {
		return c.guideRarityScores
	}

	// Anchor on local calendar noon rather than time.Now().Truncate(24h):
	// Truncate operates on absolute (UTC) time, so in large positive-offset zones it
	// can land on the wrong local calendar day near midnight. Noon keeps the
	// day-of-year fed to the geomodel correct in every timezone.
	now := time.Now()
	today := time.Date(now.Year(), now.Month(), now.Day(), 12, 0, 0, 0, now.Location())
	speciesScores, err := bn.GetProbableSpecies(today, 0.0)
	if err != nil {
		return nil
	}

	scores := make(map[string]float64, len(speciesScores))
	for _, ss := range speciesScores {
		scores[strings.ToLower(detection.ExtractScientificName(ss.Label))] = ss.Score
	}
	c.guideRarityScores = scores
	c.guideRarityLocKey = locKey
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

// ebirdSpeciesCode resolves the eBird species code for a scientific name from
// the loaded BirdNET taxonomy. Returns "" when no real code is available — the
// resolver returns a generated placeholder in that case, which is not a valid
// eBird URL slug, so callers must treat the missing-code case as "no eBird link".
func (c *Controller) ebirdSpeciesCode(scientificName string) string {
	if c.Processor == nil || c.Processor.Bn == nil {
		return ""
	}
	if code, ok := c.Processor.Bn.GetSpeciesCode(scientificName); ok {
		return code
	}
	return ""
}

// externalLinksForGuide assembles the external resource links for a species:
// OpenFauna-sourced links (Tier 1) plus opt-in computed supplementary links
// (Tier 2: Xeno-canto + Wikipedia gap-fill) from the embedded data, with the eBird
// link appended last when a real eBird species code is available. eBird is a
// deliberate runtime special case: its data cannot live in OpenFauna for
// eBird/Clements licensing reasons, and eBird has no public free-text search
// endpoint, so species pages must be addressed by code as
// https://ebird.org/species/<code>.
//
// lang for {lang} substitution is the Wikipedia project subtag (so the nb/nn -> no
// override is applied). The Wikidata GoToLinkedPage template requires the exact wiki
// project code, so that mapping is mandatory there; the same subtag also feeds
// iNaturalist's ?locale= value. iNaturalist falls back to its default locale for a
// value it does not recognize, so the link is always valid even when not localized.
func externalLinksForGuide(scientificName, ebirdCode, locale string, includeSupplementary bool) []GuideExternalLink {
	if scientificName == "" {
		return nil
	}
	lang := wikipediaSubdomain(baseLanguage(locale))
	resolved := openfauna.ExternalLinks(scientificName, lang, includeSupplementary)
	out := make([]GuideExternalLink, 0, len(resolved)+1)
	for _, l := range resolved {
		out = append(out, GuideExternalLink{Name: l.Name, URL: l.URL, Icon: l.Icon})
	}
	if ebirdCode != "" {
		out = append(out, GuideExternalLink{
			Name: linkNameEBird,
			URL:  "https://ebird.org/species/" + url.PathEscape(ebirdCode),
			Icon: linkIconEBird,
		})
	}
	return out
}

// wikipediaLangOverrides maps a base-language subtag to the Wikipedia language
// subdomain to use when the two differ. Norwegian Bokmål ("nb") and Nynorsk ("nn")
// articles live on the Norwegian Wikipedia ("no"); nb.wikipedia.org only redirects
// there, so we address it canonically instead of relying on the redirect. Subtags
// absent from this map use the base subtag unchanged.
var wikipediaLangOverrides = map[string]string{
	"nb": "no",
	"nn": "no",
}

// wikipediaSubdomain returns the Wikipedia language subdomain for a base-language
// subtag, applying wikipediaLangOverrides for the cases where the article namespace
// differs from the language code. Callers pass a value already produced by
// baseLanguage (validated, lowercase).
func wikipediaSubdomain(lang string) string {
	if sub, ok := wikipediaLangOverrides[lang]; ok {
		return sub
	}
	return lang
}

// baseLanguage extracts the lowercase base-language subtag from a UI locale (e.g.
// "pt-br"/"pt_pt" -> "pt", "zh-cn" -> "zh"), validating it as a 2-3 letter code.
// Anything else falls back to defaultWikiLang ("en"). The locale always originates
// from the app's UI locale set (base-language codes that map to live Wikipedia
// subdomains, modulo wikipediaSubdomain's overrides), so the result is safe to use
// as a Wikipedia language subdomain and as an iNaturalist ?locale= value.
func baseLanguage(locale string) string {
	l := strings.ToLower(strings.TrimSpace(locale))
	// Split on either separator and keep the primary subtag.
	if i := strings.IndexAny(l, "-_"); i >= 0 {
		l = l[:i]
	}
	if len(l) < 2 || len(l) > 3 {
		return defaultWikiLang
	}
	for _, r := range l {
		if r < 'a' || r > 'z' {
			return defaultWikiLang
		}
	}
	return l
}

// summarizeDescription returns a short, single-paragraph summary of a description.
func summarizeDescription(description string) string {
	intro := description
	if idx := strings.Index(description, "## "); idx >= 0 {
		intro = description[:idx]
	}
	intro = strings.TrimSpace(intro)
	if len(intro) > guideSummaryMaxLength {
		// Back the cut off to a rune boundary so multi-byte text (e.g. accented
		// non-English guides) is not split mid-rune, which would otherwise leave a
		// replacement character at the end of the summary.
		intro = strings.TrimSpace(guideprovider.TrimToUTF8Boundary(intro, guideSummaryMaxLength))
	}
	return intro
}
