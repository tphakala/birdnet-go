// Package speciesguide implements the species guide, notes, and similar-species
// API endpoints backed by the guideprovider cache and the species-notes
// datastore. It follows the standard domain-handler pattern: Handler embeds
// *apicore.Core by pointer and registers its routes via RegisterRoutes.
package speciesguide

import (
	"context"
	"fmt"
	"net/http"
	"net/url"
	"strings"
	"sync"
	"sync/atomic"
	"time"
	"unicode"

	"github.com/labstack/echo/v4"
	"github.com/labstack/echo/v4/middleware"
	"golang.org/x/time/rate"

	"github.com/tphakala/birdnet-go/internal/api/v2/apicore"
	"github.com/tphakala/birdnet-go/internal/classifier"
	"github.com/tphakala/birdnet-go/internal/conf"
	"github.com/tphakala/birdnet-go/internal/datastore"
	"github.com/tphakala/birdnet-go/internal/detection"
	"github.com/tphakala/birdnet-go/internal/errors"
	"github.com/tphakala/birdnet-go/internal/guideprovider"
	"github.com/tphakala/birdnet-go/internal/logger"
	"github.com/tphakala/birdnet-go/internal/notification"
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

// Season tokens. These cross the API boundary into the frontend's season labels, so
// they are named rather than inlined at each switch arm: a typo in a bare literal
// would silently produce an untranslatable token that no compiler catches.
const (
	seasonSpring = "spring"
	seasonSummer = "summer"
	seasonAutumn = "autumn"
	seasonWinter = "winter"
	// Equatorial (bimodal) seasons: two wet and two dry periods per year.
	seasonWet1 = "wet1"
	seasonDry1 = "dry1"
	seasonWet2 = "wet2"
	seasonDry2 = "dry2"
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

// secondsPerMinute converts the per-minute rate-limit budget to Echo's
// requests-per-second limiter rate.
const secondsPerMinute = 60

const (
	// guideRateLimitPerMinute bounds calls to the external-API-backed endpoints.
	// The limiter runs as middleware (before the handler), so it cannot tell a
	// warm cache hit from a live external fetch and counts both. Echo's limiter
	// Rate is in requests-per-second, so this per-minute value is divided by
	// secondsPerMinute at construction; the cache's singleflight + stale-refresh
	// dedup already bounds the actual number of external provider calls.
	guideRateLimitPerMinute = 60
	// guideRateLimitBurst is the limiter burst allowance. The species comparison
	// panel issues two requests per open (guide + similar), so a generous burst
	// keeps a user clicking through detections from being throttled prematurely
	// while the sustained per-second rate still bounds abuse.
	guideRateLimitBurst = 30
	// maxSimilarSpecies caps the number of similar-species candidates resolved.
	maxSimilarSpecies = 8
	// maxConcurrentSimilarFetches bounds how many of a single request's similar-species
	// candidates hold a live guide fetch at once. singleflight does NOT collapse the
	// fan-out (each candidate is a distinct species), so an unbounded fan-out could put
	// maxSimilarSpecies live upstream fetches in flight per request; capping it here
	// smooths the burst without changing the result (cache hits still return
	// immediately, and a candidate that can't get a slot before the deadline falls back
	// to links-only, identical to a fetch that times out).
	maxConcurrentSimilarFetches = 4
	// similarSpeciesResolveTimeout bounds the worst-case latency of the
	// similar-species panel: candidates fan out concurrent guide lookups that may
	// trigger live (external) fetches on a cold cache, so cap how long the whole
	// resolution may block. Candidates that don't resolve in time return
	// name-only (best-effort enrichment); the cache still persists any fetch that
	// completes after the deadline for the next request.
	similarSpeciesResolveTimeout = 8 * time.Second
	// guideSummaryMaxLength caps the per-candidate guide summary length (bytes).
	guideSummaryMaxLength = 280
)

// componentSpeciesGuide is the telemetry component for this domain. It must stay in
// sync with the RegisterComponent call in internal/errors: an unregistered component
// is tagged incorrectly, and because lookupComponent matches the registered "api"
// pattern against this package's path, the explicitly-tagged errors and the ones
// that fall through to inference used to land in two different Sentry buckets.
const componentSpeciesGuide = "api-species-guide"

// errGuideCacheUnavailable is returned by the accessor when no cache is wired.
var errGuideCacheUnavailable = errors.Newf("species guide cache unavailable").
	Component(componentSpeciesGuide).
	Category(errors.CategorySystem).
	Build()

// Handler serves the species guide domain: the guide and similar-species
// endpoints (rate-limited; backed by the guideprovider cache) and the
// auth-gated species-notes CRUD endpoints. Beyond the shared *apicore.Core it
// owns the guide cache slot (hot-reload swaps a new cache in via SetGuideCache;
// the handler is the canonical owner and closes it on Shutdown) and the
// guide-rarity memoization for the expectedness badge.
type Handler struct {
	*apicore.Core

	// guideCache is the species guide cache, guarded by guideCacheMu. May be
	// nil when the feature is disabled.
	guideCache   *guideprovider.GuideCache
	guideCacheMu sync.RWMutex
	// guideShutdown latches once Shutdown has run, so a reconfigure that lost the
	// race with teardown cannot install a fresh cache nothing will ever close.
	guideShutdown bool

	// guideRarity* memoize the daily probable-species score map (normalized
	// scientific name -> score) for the guide expectedness badge, so a burst of
	// guide requests doesn't re-run the geomodel prediction per call. Keyed on the
	// configured location (guideRarityLocKey) and the TTL so a location change
	// invalidates it immediately.
	guideRarityMu     sync.RWMutex
	guideRarityExpiry time.Time
	guideRarityScores map[string]float64
	guideRarityLocKey string

	// geomodelCoverage* memoize the set of scientific names the primary geomodel can
	// classify (lowercased), so the expectedness badge does not linear-scan the full
	// label set on every guide request for a species absent from today's probable
	// list. The label set is immutable for a loaded model, so the set is built once
	// and keyed on the classifier pointer; a model reload (new pointer) rebuilds it.
	geomodelCoverageMu  sync.RWMutex
	geomodelCoverageBn  labelSource
	geomodelCoverageSet map[string]struct{}
}

// New creates the species guide domain handler around the shared core.
func New(core *apicore.Core) *Handler {
	return &Handler{Core: core}
}

// --- Response shapes (authoritative; mirrored by frontend types in species.ts) ---

// GuideFeatureFlags reports which guide sections the configuration enables, so the
// client gates on a server-authoritative value rather than only on its own (possibly
// stale) settings copy.
//
// Taxonomy is reported for that consistency but, unlike the others, is NOT additionally
// enforced by a server-side gate. Its data comes from GET /species/taxonomy, which is a
// public endpoint in the species domain with a non-guide consumer (the detection detail
// view renders the same lineage with the guide disabled), so gating it on a species-guide
// flag would break that caller. The enforcement argument is also weak here: the response
// is served from the embedded offline dataset, so a hidden section costs no upstream
// fetch — the thing the other flags exist to prevent.
type GuideFeatureFlags struct {
	Notes          bool `json:"notes"`
	Enrichments    bool `json:"enrichments"`
	SimilarSpecies bool `json:"similar_species"`
	Taxonomy       bool `json:"taxonomy"`
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
	// GuideUnavailable reports that the guide cache could not be consulted, so the
	// entries carry no common names or summaries. It distinguishes a degraded
	// response from one where the species genuinely have no guides — without it the
	// endpoint answered a plain 200 for both, while /guide answered 503 for the
	// same state, and the UI had no way to tell the two apart.
	GuideUnavailable bool `json:"guide_unavailable,omitempty"`
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

// --- Guide cache plumbing (hot-reload safe) ---

// WithGuideCache snapshots the guide cache pointer under a read lock and runs fn
// outside the lock, so hot-reload swaps never block readers. Returns
// errGuideCacheUnavailable when no cache is configured.
func (c *Handler) WithGuideCache(fn func(*guideprovider.GuideCache) error) error {
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
//
// After Shutdown it installs nothing and closes the incoming cache instead. A
// reconfigure signal can still be sitting on the control-monitor channel when
// teardown begins; without this the monitor would drain it afterwards, build a
// fresh cache (spawning a refresh loop and a detached warm on a non-cancellable
// context) and install it into a handler nobody will shut down again, leaving those
// goroutines running past process teardown.
//
// It reports whether gc was installed. A false return means the incoming cache was
// refused and closed, so callers must not go on to wire callbacks into it (see the
// hot-reload handler in internal/analysis).
func (c *Handler) SetGuideCache(gc *guideprovider.GuideCache) bool {
	c.guideCacheMu.Lock()
	if c.guideShutdown {
		c.guideCacheMu.Unlock()
		if gc != nil {
			gc.Close()
		}
		return false
	}
	old := c.guideCache
	c.guideCache = gc
	c.guideCacheMu.Unlock()
	if old != nil && old != gc {
		old.Close()
	}
	return true
}

// Shutdown closes and nils the guide cache (the handler is its canonical owner).
// Snapshot under the lock, then close outside it. Called by the facade during
// controller teardown. It is one-way: see SetGuideCache for why a later swap must
// not resurrect a cache.
func (c *Handler) Shutdown() {
	c.guideCacheMu.Lock()
	gc := c.guideCache
	c.guideCache = nil
	c.guideShutdown = true
	c.guideCacheMu.Unlock()
	if gc != nil {
		gc.Close()
	}
}

// --- Routes ---

// RegisterRoutes registers the species guide, similar-species, and notes
// endpoints on the /api/v2 group. Called from the facade's initRoutes.
func (c *Handler) RegisterRoutes(g *echo.Group) {
	guideRateLimiter := c.newGuideRateLimiter()

	// Guide + similar are rate-limited (external API calls).
	g.GET("/species/:scientific_name/guide", c.GetSpeciesGuide, guideRateLimiter)
	g.GET("/species/:scientific_name/similar", c.GetSimilarSpecies, guideRateLimiter)

	// Notes are auth-gated for both reads and writes: they are user-authored and
	// may contain sensitive content (e.g. precise locations of rare species), so
	// they must not be world-readable on a publicly exposed instance.
	//
	// Authorization is deliberately single-tier: BirdNET-Go has one admin identity,
	// so any authenticated principal may read every species' notes and update/delete
	// any note by :id. There is intentionally NO per-user ownership scoping (it would
	// be meaningless in the single-admin model). If multi-user auth is ever added, the
	// update/delete-by-id handlers must gain an ownership check to avoid IDOR.
	g.GET("/species/:scientific_name/notes", c.GetSpeciesNotes, c.GetAuthMiddleware())
	g.POST("/species/:scientific_name/notes", c.CreateSpeciesNote, c.GetAuthMiddleware())
	g.PUT("/species/notes/:id", c.UpdateSpeciesNote, c.GetAuthMiddleware())
	g.DELETE("/species/notes/:id", c.DeleteSpeciesNote, c.GetAuthMiddleware())
}

// newGuideRateLimiter builds the shared rate limiter for guide/similar endpoints.
func (c *Handler) newGuideRateLimiter() echo.MiddlewareFunc {
	return middleware.RateLimiterWithConfig(middleware.RateLimiterConfig{
		Store: middleware.NewRateLimiterMemoryStoreWithConfig(
			middleware.RateLimiterMemoryStoreConfig{
				Rate:      rate.Limit(float64(guideRateLimitPerMinute) / float64(secondsPerMinute)),
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
		// Both paths go through HandleErrorWithKey rather than a bare ctx.JSON so the
		// response carries the standard ErrorResponse shape and correlation ID, the
		// failure reaches telemetry, and the message is a translatable key instead of
		// raw English. The cause is passed through rather than discarded so it lands
		// in that telemetry.
		ErrorHandler: func(ctx echo.Context, err error) error {
			return c.rateLimited(ctx, err)
		},
		DenyHandler: func(ctx echo.Context, identifier string, err error) error {
			c.LogWarnIfEnabled("species guide request denied by rate limit",
				logger.String("identifier", identifier),
				logger.String("path", ctx.Request().URL.Path))
			return c.rateLimited(ctx, err)
		},
	})
}

// rateLimited writes the 429 for both rate-limiter paths. err is the limiter's
// own cause (nil on the deny path with some Echo versions), which HandleError
// tolerates.
func (c *Handler) rateLimited(ctx echo.Context, err error) error {
	return c.HandleErrorWithKey(ctx, err,
		"Too many species guide requests, please wait before trying again",
		http.StatusTooManyRequests, notification.MsgErrSpeciesGuideRateLimit, nil)
}

// parseScientificNameParam extracts and validates the :scientific_name path param.
// Echo decodes percent-encoding, so spaces arrive intact.
func parseScientificNameParam(ctx echo.Context) (string, error) {
	name := strings.TrimSpace(ctx.Param("scientific_name"))
	if name == "" {
		// Fall back to the sole positional param in case of param-name drift, but ONLY
		// when the route has exactly one path param. On a multi-param route grabbing
		// vals[0] could silently bind the wrong value, so prefer a clear 400 there.
		if vals := ctx.ParamValues(); len(vals) == 1 {
			name = strings.TrimSpace(vals[0])
		}
	}
	if name == "" {
		return "", errors.Newf("scientific_name parameter is required").
			Category(errors.CategoryValidation).
			Component(componentSpeciesGuide).
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
			Component(componentSpeciesGuide).
			Build()
	}
	if !isPlausibleScientificName(name) {
		return "", errors.Newf("scientific_name contains invalid characters").
			Category(errors.CategoryValidation).
			Component(componentSpeciesGuide).
			Build()
	}
	return name, nil
}

// isPlausibleScientificName reports whether s looks like a scientific name: only
// Latin-script letters, spaces, hyphens, apostrophes, and periods. Scientific
// binomials (and BirdNET's binomial, hyphenated, and "x"-hybrid labels) are Latin
// script, and the embedded OpenFauna dataset is keyed by Latin binomials, so a
// non-Latin string (CJK, Cyrillic, Arabic, ...) can never resolve to a guide. Rejecting
// non-Latin scripts here — rather than accepting any unicode letter — keeps them out of
// the Wikipedia API and the per-value dataset memo instead of accumulating one scan
// result per distinct non-Latin garbage value. It is a cheap input filter, not a
// taxonomic check.
func isPlausibleScientificName(s string) bool {
	if s == "" {
		return false // an empty name is never plausible (callers also guard, but be explicit)
	}
	for _, r := range s {
		switch {
		case unicode.Is(unicode.Latin, r):
		case r == ' ' || r == '-' || r == '\'' || r == '.':
		default:
			return false
		}
	}
	return true
}

// handleScientificNameError maps a scientific-name parse error to a 400 response.
func (c *Handler) handleScientificNameError(ctx echo.Context, err error) error {
	return c.HandleError(ctx, err, "Invalid scientific name", http.StatusBadRequest)
}

// requireGuideFeature returns a 404 response when the species guide is disabled, or
// when the specific sub-feature this endpoint serves is switched off. Settings are
// read per request (c.CurrentSettings) so a toggle takes effect without a restart.
//
// Every endpoint in the domain goes through this: the Show* flags are presented in
// the settings UI as feature switches, so an "off" flag must actually stop serving
// the data rather than only stopping the frontend from rendering it — otherwise a
// disabled panel still costs the upstream fetches it was disabled to avoid.
// subFeature selects the sub-feature flag; pass nil for endpoints gated only by the
// top-level Enabled switch.
//
// ShowTaxonomy is the one deliberate exception, because this domain serves no taxonomy
// endpoint: that data comes from the species domain's public GET /species/taxonomy,
// which has a non-guide consumer and costs no upstream fetch. See GuideFeatureFlags.
//
// The GATE IS THE SETTINGS POINTER, not the error: HandleError writes the response and
// returns nil, so a caller branching on the error would sail past a denied request and
// dereference nil settings. Callers must therefore write:
//
//	settings, resp := c.requireGuideFeature(ctx, nil)
//	if settings == nil {
//		return resp
//	}
//
// The returned error is whatever HandleError produced, so the handler still propagates
// a genuine response-write failure.
func (c *Handler) requireGuideFeature(ctx echo.Context, subFeature func(*conf.SpeciesGuideConfig) bool) (*conf.Settings, error) {
	settings := c.CurrentSettings()
	cfg := settings.Realtime.Dashboard.SpeciesGuide
	if !cfg.Enabled || (subFeature != nil && !subFeature(&cfg)) {
		return nil, c.HandleError(ctx, errors.Newf("species guide is disabled").
			Category(errors.CategoryConfiguration).
			Component(componentSpeciesGuide).
			Build(), "Species guide is not enabled", http.StatusNotFound)
	}
	return settings, nil
}

// notesEnabled is the sub-feature predicate for the four notes endpoints.
func notesEnabled(cfg *conf.SpeciesGuideConfig) bool { return cfg.ShowNotes }

// parseNoteIDParam returns the trimmed :id path param, or a validation error when absent.
func parseNoteIDParam(ctx echo.Context) (string, error) {
	id := strings.TrimSpace(ctx.Param("id"))
	if id == "" {
		return "", errors.Newf("note id is required").
			Category(errors.CategoryValidation).Component(componentSpeciesGuide).Build()
	}
	return id, nil
}

// guideLocale resolves the request locale: query param, then dashboard locale, then "en".
// The returned value is intentionally NOT sanitized here: the guide cache normalizes it
// downstream (guideprovider.normalizeLocale bounds the cache-key space and the Wikipedia
// subdomain it can select), and the external-link path re-derives its base subtag via
// baseLanguage. Validation lives in those consumers rather than being duplicated at this
// boundary, so there is a single locale-validation authority per consumer.
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
func (c *Handler) GetSpeciesGuide(ctx echo.Context) error {
	name, err := parseScientificNameParam(ctx)
	if err != nil {
		return c.handleScientificNameError(ctx, err)
	}

	settings, resp := c.requireGuideFeature(ctx, nil)
	if settings == nil {
		return resp
	}
	cfg := settings.Realtime.Dashboard.SpeciesGuide

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
			c.LogInfoIfEnabled("species guide request canceled by client",
				logger.String("species", name), logger.Error(err))
			return c.HandleError(ctx, err, "Request canceled by client", apicore.StatusClientClosedRequest)
		case errors.Is(err, context.DeadlineExceeded):
			return c.HandleError(ctx, err, "Species guide request timed out", http.StatusRequestTimeout)
		case errors.Is(err, errGuideCacheUnavailable), errors.Is(err, guideprovider.ErrCacheUnavailable):
			// Both sentinels mean the same thing to a client: no cache is currently
			// able to answer. The provider-side one (no registered providers) used to
			// fall through to the default 502, which the frontend renders as a red
			// error box with a raw server message instead of its softer "guide
			// unavailable" notice — the 503 branch is what selects that.
			return c.HandleError(ctx, err, "Species guide is temporarily unavailable", http.StatusServiceUnavailable)
		case errors.Is(err, guideprovider.ErrGuideNotFound):
			return c.HandleError(ctx, err, "No guide found for species", http.StatusNotFound)
		default:
			return c.HandleError(ctx, err, "Failed to fetch species guide", http.StatusBadGateway)
		}
	}
	if guide == nil || guide.IsNegativeEntry() {
		// No prose — but external links are computed offline from the embedded
		// dataset and need no provider, so a species with no article can still be
		// given somewhere to go. The similar-species endpoint already attaches links
		// in exactly this case ("REGARDLESS of why there is no prose"), so 404ing
		// here made the same species answer differently depending on which panel
		// rendered it, and the panel the user opened deliberately was the one that
		// showed nothing.
		//
		// Still a 404 when there is genuinely nothing to offer, so "this species has
		// no guide" remains distinguishable from "here are some resources".
		var links []GuideExternalLink
		if cfg.ShowEnrichments {
			links = externalLinksForGuide(
				name,
				c.ebirdSpeciesCode(name),
				locale,
				cfg.EnableSupplementaryLinks,
			)
		}
		if len(links) == 0 {
			return c.HandleError(ctx, errors.Newf("no guide content").
				Category(errors.CategoryNotFound).
				Component(componentSpeciesGuide).
				Build(), "No guide found for species", http.StatusNotFound)
		}
		return ctx.JSON(http.StatusOK, SpeciesGuideData{
			ScientificName: name,
			Quality:        guideQualityStub,
			Features: GuideFeatureFlags{
				Notes:          cfg.ShowNotes,
				Enrichments:    cfg.ShowEnrichments,
				SimilarSpecies: cfg.ShowSimilarSpecies,
				Taxonomy:       cfg.ShowTaxonomy,
			},
			ExternalLinks: links,
		})
	}

	data := SpeciesGuideData{
		ScientificName: guide.ScientificName,
		CommonName:     guide.CommonName,
		Description:    guide.Description,
		Quality:        classifyGuideQuality(guide.Description, guide.Partial),
		Features: GuideFeatureFlags{
			Notes:          cfg.ShowNotes,
			Enrichments:    cfg.ShowEnrichments,
			SimilarSpecies: cfg.ShowSimilarSpecies,
			Taxonomy:       cfg.ShowTaxonomy,
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
	if cfg.ShowEnrichments {
		// Season is only meaningful once the observer location is configured. An unset
		// location defaults to (0,0), which falls inside the equatorial band and would
		// otherwise emit a wet/dry-season token for every un-configured instance
		// (misleading for temperate users); treat (0,0) as "unset" and omit the field.
		if settings.BirdNET.Latitude != 0 || settings.BirdNET.Longitude != 0 {
			data.CurrentSeason = computeCurrentSeason(settings.BirdNET.Latitude, time.Now())
		}
		data.ExternalLinks = externalLinksForGuide(
			guide.ScientificName,
			c.ebirdSpeciesCode(guide.ScientificName),
			locale,
			cfg.EnableSupplementaryLinks,
		)
		// Expectedness keys off the REQUEST name, not guide.ScientificName (which the
		// links/eBird calls above use): the geomodel's vocabulary is BirdNET-label-
		// derived — the same origin as the detection's request name — so the request
		// name is the value guaranteed to match the geomodel label set. A
		// provider-normalized guide.ScientificName could fall outside that vocabulary
		// and silently miss.
		if exp := c.guideExpectedness(name, settings); exp != "" {
			data.Expectedness = exp
		}
	}

	return ctx.JSON(http.StatusOK, data)
}

// GetSimilarSpecies returns same-genus / same-family / similar species with summaries.
// GET /api/v2/species/:scientific_name/similar
func (c *Handler) GetSimilarSpecies(ctx echo.Context) error {
	name, err := parseScientificNameParam(ctx)
	if err != nil {
		return c.handleScientificNameError(ctx, err)
	}

	settings, resp := c.requireGuideFeature(ctx,
		func(cfg *conf.SpeciesGuideConfig) bool { return cfg.ShowSimilarSpecies })
	if settings == nil {
		return resp
	}
	if c.TaxonomyDB == nil {
		return c.HandleError(ctx, errors.Newf("taxonomy database not available").
			Category(errors.CategorySystem).
			Component(componentSpeciesGuide).
			Build(), "Taxonomy database not available", http.StatusServiceUnavailable)
	}

	locale := guideLocale(ctx, settings)
	genus, candidates := c.similarSpeciesCandidates(name)

	// Links are enrichment data: only attach them to description-less entries when
	// enrichments are enabled, mirroring the main guide modal.
	withLinks := settings.Realtime.Dashboard.SpeciesGuide.ShowEnrichments
	supplementary := settings.Realtime.Dashboard.SpeciesGuide.EnableSupplementaryLinks
	entries, cacheUnavailable := c.resolveSimilarSpecies(ctx.Request().Context(), candidates, locale, withLinks, supplementary)

	return ctx.JSON(http.StatusOK, SimilarSpeciesResponse{
		ScientificName:   name,
		Genus:            genus,
		Similar:          entries,
		GuideUnavailable: cacheUnavailable,
	})
}

// similarCandidate is an intermediate candidate before guide resolution.
type similarCandidate struct {
	scientificName string
	relationship   string
}

// similarSpeciesCandidates resolves same-genus then same-family candidates,
// excluding the focal species and capped at maxSimilarSpecies.
func (c *Handler) similarSpeciesCandidates(focal string) (genus string, candidates []similarCandidate) {
	genusName, meta, err := c.TaxonomyDB.GetGenusByScientificName(focal)
	if err != nil || genusName == "" {
		// Fall back to the first token of the scientific name, and DISCARD meta:
		// the lookup that produced it failed, so its family is not trustworthy.
		// Only genusName was being reassigned here, so a partially-resolved entry
		// could leave meta non-nil and drive the same-family branch below off a
		// family the lookup had already reported as unreliable — filling the
		// comparison panel with unrelated species.
		genusName, _, _ = strings.Cut(focal, " ")
		meta = nil
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
//
// The second return value reports that the guide cache was unavailable for at least
// one candidate. Discarding that used to make an unavailable cache indistinguishable
// from "these species genuinely have no guide": the endpoint answered 200 with every
// common_name blank while /guide answered 503 for the identical state, so the UI
// rendered an "unavailable" guide panel beside a populated-looking rail of bare
// scientific names with no hint the data was degraded.
func (c *Handler) resolveSimilarSpecies(ctx context.Context, candidates []similarCandidate, locale string, withLinks, supplementary bool) (entries []SimilarSpeciesEntry, cacheUnavailable bool) {
	// Resolve every candidate against the embedded datasets in ONE pass per blob
	// before fanning out. Each candidate's gc.Get otherwise drives its own
	// openfauna.LookupCommonName + LookupMeta, and a memo miss there decompresses
	// and scans an embedded blob — up to maxSimilarSpecies scans of the ~20 MB
	// translations data per request, on the Pi-class hardware this targets, inside
	// similarSpeciesResolveTimeout.
	//
	// No batch-size guard is needed: PrimeCaches excludes names it has already
	// memoized and skips a pass whose work set is empty, so the common warm-cache
	// request pays map lookups only. Priming before the deadline starts (rather than
	// inside it) keeps the whole timeout available to the fetches it is meant to
	// bound; the scans themselves are CPU-bound and cannot be cancelled anyway.
	primeNames := make([]string, 0, len(candidates))
	for i := range candidates {
		primeNames = append(primeNames, candidates[i].scientificName)
	}
	openfauna.PrimeCaches(primeNames, locale)

	// Bound the whole fan-out so a cold cache (live external fetches) cannot
	// block the request indefinitely; unresolved candidates fall back to
	// links-only (or name-only when enrichments are off) below.
	resolveCtx, cancel := context.WithTimeout(ctx, similarSpeciesResolveTimeout)
	defer cancel()

	entries = make([]SimilarSpeciesEntry, len(candidates))
	// Set when any candidate found no usable cache, so the caller can tell the
	// client the rail is degraded rather than genuinely empty.
	var unavailable atomic.Bool
	// Per-request semaphore bounding concurrent live guide fetches (see
	// maxConcurrentSimilarFetches). Acquired around gc.Get and released after.
	sem := make(chan struct{}, maxConcurrentSimilarFetches)
	var wg sync.WaitGroup
	for idx := range candidates {
		wg.Go(func() {
			cand := candidates[idx]
			entry := SimilarSpeciesEntry{
				ScientificName: cand.scientificName,
				Relationship:   cand.relationship,
			}
			if cacheErr := c.WithGuideCache(func(gc *guideprovider.GuideCache) error {
				// Wait for a fetch slot, but never past the resolve deadline; a candidate
				// that can't get a slot in time falls back to links-only below.
				select {
				case sem <- struct{}{}:
					defer func() { <-sem }()
				case <-resolveCtx.Done():
					return nil
				}
				g, err := gc.Get(resolveCtx, cand.scientificName, guideprovider.FetchOptions{Locale: locale})
				if err != nil || g == nil || g.IsNegativeEntry() {
					return nil //nolint:nilerr // best-effort enrichment; missing guide is fine
				}
				entry.CommonName = g.CommonName
				if strings.TrimSpace(g.Description) != "" {
					entry.GuideSummary = summarizeDescription(g.Description)
					entry.HasGuide = true
				}
				return nil
			}); errors.Is(cacheErr, errGuideCacheUnavailable) {
				unavailable.Store(true)
			}
			// Prose-less entries carry external resource links (when enrichments
			// are on) REGARDLESS of why there is no prose: an OpenFauna stub, a
			// negative cache entry, an unavailable cache, or a resolve timeout.
			// Links are computed from embedded data and need no fetch, so this
			// honors the SimilarSpeciesEntry contract ("links whenever there is
			// no prose to compare") on the failure paths too.
			if withLinks && !entry.HasGuide {
				entry.ExternalLinks = externalLinksForGuide(
					cand.scientificName,
					c.ebirdSpeciesCode(cand.scientificName),
					locale,
					supplementary,
				)
			}
			entries[idx] = entry
		})
	}
	wg.Wait()
	return entries, unavailable.Load()
}

// GetSpeciesNotes returns all notes for a species (authenticated; notes are
// user-authored and may contain sensitive content, see RegisterRoutes).
// GET /api/v2/species/:scientific_name/notes
func (c *Handler) GetSpeciesNotes(ctx echo.Context) error {
	name, err := parseScientificNameParam(ctx)
	if err != nil {
		return c.handleScientificNameError(ctx, err)
	}
	if settings, resp := c.requireGuideFeature(ctx, notesEnabled); settings == nil {
		return resp
	}
	if dsErr := c.RequireDatastore(ctx); dsErr != nil {
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
func (c *Handler) CreateSpeciesNote(ctx echo.Context) error {
	name, err := parseScientificNameParam(ctx)
	if err != nil {
		return c.handleScientificNameError(ctx, err)
	}
	if settings, resp := c.requireGuideFeature(ctx, notesEnabled); settings == nil {
		return resp
	}
	if dsErr := c.RequireDatastore(ctx); dsErr != nil {
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
func (c *Handler) UpdateSpeciesNote(ctx echo.Context) error {
	id, err := parseNoteIDParam(ctx)
	if err != nil {
		return c.HandleError(ctx, err, "Missing note id", http.StatusBadRequest)
	}
	if settings, resp := c.requireGuideFeature(ctx, notesEnabled); settings == nil {
		return resp
	}
	if dsErr := c.RequireDatastore(ctx); dsErr != nil {
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
func (c *Handler) DeleteSpeciesNote(ctx echo.Context) error {
	id, err := parseNoteIDParam(ctx)
	if err != nil {
		return c.HandleError(ctx, err, "Missing note id", http.StatusBadRequest)
	}
	if settings, resp := c.requireGuideFeature(ctx, notesEnabled); settings == nil {
		return resp
	}
	if dsErr := c.RequireDatastore(ctx); dsErr != nil {
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
func (c *Handler) handleSpeciesNoteWriteError(ctx echo.Context, err error, message string) error {
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

// classifyGuideQuality classifies a guide's completeness for the UI badge. It
// delegates to guideprovider.ClassifyQuality so the badge and the cache-quality
// metric are derived from one set of thresholds; the guideQuality* constants below
// document the label strings this API serves.
func classifyGuideQuality(description string, partial bool) string {
	return guideprovider.ClassifyQuality(description, partial)
}

// guideRarityTTL bounds how long the cached probable-species score map is reused.
// The geomodel prediction behind it is the per-request cost we're avoiding; 60s
// of staleness after a location/settings change is acceptable for a badge.
const guideRarityTTL = 60 * time.Second

// guideExpectedness returns the expectedness classification for a species, or ""
// when the rarity model is unavailable or has no coverage for the species. It
// uses a short-lived cache of the probable-species scores so a rate-limited
// burst of guide requests doesn't re-run the geomodel prediction per call.
func (c *Handler) guideExpectedness(scientificName string, settings *conf.Settings) string {
	proc := c.Processor
	if proc == nil || proc.Bn == nil {
		return ""
	}
	scores := c.probableSpeciesScores(proc.Bn, guideRarityLocationKey(settings))
	if scores == nil {
		return ""
	}
	key := strings.ToLower(detection.ExtractScientificName(scientificName))
	if score, ok := scores[key]; ok {
		return scoreToExpectedness(score)
	}
	// Not in today's probable list. If the geomodel covers the species at all, its
	// occurrence probability for today is effectively zero, so classify it from a
	// zero score (which lands on "unexpected" — the bottom band). If the geomodel
	// does not cover it there is no probability to reason about, so omit the badge
	// rather than implying the species is unexpected here.
	if c.speciesHasGeomodelCoverage(proc.Bn, scientificName) {
		return scoreToExpectedness(0)
	}
	return ""
}

// guideRarityLocationKey returns a stable key for an observer location. The
// memoized probable-species map is invalidated when this changes so a location
// update is reflected immediately rather than after the TTL window.
//
// It takes the caller's settings snapshot rather than re-reading CurrentSettings():
// the handler already holds one two frames up, and a second read could key the memo
// to a location the rest of the response was not built from if a hot-reload lands
// between the two.
func guideRarityLocationKey(settings *conf.Settings) string {
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
// geomodel occurrence score, rebuilding it when the TTL expires or locKey (the
// caller's location key, from guideRarityLocationKey) differs from the one the
// memo was built for. Returns nil when the prediction is unavailable (caller omits
// expectedness).
func (c *Handler) probableSpeciesScores(bn probableSpeciesPredictor, locKey string) map[string]float64 {
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

// Rarity score thresholds for the guide expectedness badge. These mirror the
// geomodel occurrence-probability bands the species rarity domain uses
// (internal/api/v2/species); they are defined locally so the two domains stay
// decoupled (domains depend only on apicore and dto, never on each other).
const (
	rarityThresholdCommon   = 0.5
	rarityThresholdUncommon = 0.2
	rarityThresholdRare     = 0.05
)

// labelSource is the minimal view of the geomodel that coverage detection needs: the
// primary model's classifiable label set. *classifier.Orchestrator satisfies it; tests
// supply a fake so the memoization logic is exercisable without a loaded tflite model.
type labelSource interface {
	Labels() []string
}

// speciesHasGeomodelCoverage reports whether the scientific name is in the primary
// model's label set (the geomodel's classifiable vocabulary). Secondary-model-only
// species (e.g. bats) are absent from it and so have no geomodel occurrence
// probability to base a rarity on. It consults a memoized name set (see
// geomodelCoverageNames) rather than re-scanning bn.Labels() on every call.
func (c *Handler) speciesHasGeomodelCoverage(bn labelSource, scientificName string) bool {
	set := c.geomodelCoverageNames(bn)
	_, ok := set[strings.ToLower(detection.ExtractScientificName(scientificName))]
	return ok
}

// geomodelCoverageNames returns the lowercased set of scientific names the primary
// model can classify, building it once per loaded model. The label set is immutable
// for a given classifier, so the result is memoized under geomodelCoverageMu and
// keyed on the classifier pointer; a model reload swaps the pointer and rebuilds.
// Lowercasing both sides (here and at lookup) is equivalent to the previous EqualFold
// comparison for the ASCII-Latin scientific names in the label set and matches how
// probableSpeciesScores keys its map.
func (c *Handler) geomodelCoverageNames(bn labelSource) map[string]struct{} {
	c.geomodelCoverageMu.RLock()
	if c.geomodelCoverageBn == bn && c.geomodelCoverageSet != nil {
		set := c.geomodelCoverageSet
		c.geomodelCoverageMu.RUnlock()
		return set
	}
	c.geomodelCoverageMu.RUnlock()

	c.geomodelCoverageMu.Lock()
	defer c.geomodelCoverageMu.Unlock()
	// Re-check under the write lock: another goroutine may have built the set while
	// we waited, so only one scan runs per loaded model.
	if c.geomodelCoverageBn == bn && c.geomodelCoverageSet != nil {
		return c.geomodelCoverageSet
	}
	labels := bn.Labels()
	set := make(map[string]struct{}, len(labels))
	for _, label := range labels {
		set[strings.ToLower(detection.ExtractScientificName(label))] = struct{}{}
	}
	// Do not memoize an empty set: an empty label slice means the model's vocabulary
	// is not yet available, and caching it (keyed on this bn pointer) would pin an
	// empty result for the model's lifetime. Return it uncached so a later call —
	// once labels are populated — rebuilds. On the real path this is unreachable
	// (expectedness only runs after a successful GetProbableSpecies, which implies a
	// loaded label set), but the guard keeps the memo self-correcting regardless.
	if len(set) == 0 {
		return set
	}
	c.geomodelCoverageSet = set
	c.geomodelCoverageBn = bn
	return set
}

// scoreToExpectedness maps a geomodel occurrence score to an expectedness label.
func scoreToExpectedness(score float64) string {
	switch {
	case score > rarityThresholdCommon:
		return expectednessExpected
	case score > rarityThresholdUncommon:
		return expectednessUncommon
	case score > rarityThresholdRare:
		return expectednessRare
	default:
		return expectednessUnexpected
	}
}

// seasonTokenByConfName maps the season names conf.GetDefaultSeasons returns onto
// this package's season tokens. The two vocabularies agree everywhere except one
// name — conf says "fall", the API and the frontend's i18n keys say "autumn" — so
// the mapping is explicit rather than a pass-through that would emit a token no
// locale can translate. TestSeasonTokenByConfName_CoversEveryDefaultSeason pins
// that it stays exhaustive if conf's names ever change.
var seasonTokenByConfName = map[string]string{
	"spring": seasonSpring,
	"summer": seasonSummer,
	"fall":   seasonAutumn,
	"winter": seasonWinter,
	"wet1":   seasonWet1,
	"dry1":   seasonDry1,
	"wet2":   seasonWet2,
	"dry2":   seasonDry2,
}

// computeCurrentSeason returns a hemisphere-aware season token for the given
// latitude and time. Near the equator it returns wet/dry-season tokens.
//
// The season table comes from conf.GetDefaultSeasons, which is what the species
// tracker uses (internal/analysis/species/season.go). This used to bucket by whole
// months — Mar/Apr/May = spring — while the tracker used the solstice/equinox dates
// conf defines (Mar 20, Jun 21, Sep 22, Dec 21), so the guide badge and the tracker
// disagreed for roughly three weeks each quarter. Reading one table is what stops
// them drifting again. The hemisphere and equatorial-band classification arrives the
// same way (GetDefaultSeasons calls conf.DetectHemisphere), replacing a second copy
// of the ±10° band that lived here.
func computeCurrentSeason(latitude float64, now time.Time) string {
	var token string
	var start time.Time
	for name, season := range conf.GetDefaultSeasons(latitude) {
		tok, known := seasonTokenByConfName[name]
		if !known {
			continue
		}
		// The most recent start on or before now. A season whose date this year is
		// still ahead of us last began a year ago — that is what makes December's
		// winter the answer in January, without a special case for the wrap.
		began := time.Date(now.Year(), time.Month(season.StartMonth), season.StartDay, 0, 0, 0, 0, now.Location())
		if began.After(now) {
			began = began.AddDate(-1, 0, 0)
		}
		if token == "" || began.After(start) {
			token, start = tok, began
		}
	}
	return token
}

// ebirdSpeciesCode resolves the eBird species code for a scientific name from
// the loaded BirdNET taxonomy. Returns "" when no real code is available — the
// resolver returns a generated placeholder in that case, which is not a valid
// eBird URL slug, so callers must treat the missing-code case as "no eBird link".
func (c *Handler) ebirdSpeciesCode(scientificName string) string {
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
// lang for {lang} substitution is the plain base-language subtag (e.g. "nb" for
// Norwegian Bokmål). Any Wikipedia-specific remapping (nb/nn -> the "no" project)
// is applied per-source inside the OpenFauna resolver via each source's lang_map,
// so it affects only Wikipedia links; other services (e.g. iNaturalist's ?locale=)
// correctly receive the base subtag rather than the Wikipedia-mapped value.
func externalLinksForGuide(scientificName, ebirdCode, locale string, includeSupplementary bool) []GuideExternalLink {
	if scientificName == "" {
		return nil
	}
	lang := baseLanguage(locale)
	resolved := openfauna.ExternalLinks(scientificName, lang, includeSupplementary)
	out := make([]GuideExternalLink, 0, len(resolved)+1)
	for _, l := range resolved {
		out = append(out, GuideExternalLink{Name: l.Name, URL: l.URL, Icon: l.Icon})
	}
	// Appended after openfauna.ExternalLinks returns, so eBird sits OUTSIDE that
	// resolver's icon-keyed supplementary de-dup. Safe while no registry ships an
	// "ebird" icon; if eBird data ever enters a registry, drop this append or
	// extend the de-dup to cover it, or the link will be emitted twice.
	if ebirdCode != "" {
		out = append(out, GuideExternalLink{
			Name: linkNameEBird,
			URL:  "https://ebird.org/species/" + url.PathEscape(ebirdCode),
			Icon: linkIconEBird,
		})
	}
	return out
}

// baseLanguage extracts the lowercase base-language subtag from a UI locale (e.g.
// "pt-br"/"pt_pt" -> "pt", "zh-cn" -> "zh"), validating it as a 2-3 letter code.
// Anything else falls back to defaultWikiLang ("en"). The locale always originates
// from the app's UI locale set, so the result is a valid base-language subtag safe
// to feed the link resolver (which applies any per-source language remapping, e.g.
// Wikipedia's nb/nn -> no, itself).
func baseLanguage(locale string) string {
	// Delegates to the leaf package's single implementation. This used to be a
	// character-for-character copy of the tail of guideprovider.wikipediaSubdomain,
	// so widening what counts as a valid subtag in one (as the locale pattern was
	// widened for zh-classical and friends) silently left the other collapsing those
	// locales to English — the guide description and its external links could then
	// disagree about the language.
	return guideprovider.BaseLanguage(locale)
}

// summarizeDescription returns a short, single-paragraph summary of a description.
func summarizeDescription(description string) string {
	intro := description
	if before, _, ok := strings.Cut(description, guideprovider.SectionMarker); ok {
		intro = before
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
