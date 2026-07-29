package guideprovider

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/url"
	"regexp"
	"strings"
	"sync"
	"time"

	"golang.org/x/time/rate"

	"github.com/tphakala/birdnet-go/internal/errors"
	"github.com/tphakala/birdnet-go/internal/httpclient"
	"github.com/tphakala/birdnet-go/internal/logger"
	"github.com/tphakala/birdnet-go/internal/openfauna"
	"github.com/tphakala/birdnet-go/internal/useragent"
)

const (
	// wikipediaLicense and wikipediaLicenseURL describe the license of article text.
	wikipediaLicense    = "CC BY-SA 4.0"
	wikipediaLicenseURL = "https://creativecommons.org/licenses/by-sa/4.0/"

	// wikipediaTimeout bounds a single Wikipedia HTTP request.
	wikipediaTimeout = 15 * time.Second
	// wikipediaMaxResponseBytes caps the response body read so a hostile or
	// malfunctioning upstream cannot exhaust memory. Real TextExtracts responses
	// for a single article are a few KB; 2 MiB is a generous ceiling.
	wikipediaMaxResponseBytes = 2 << 20
	// wikipediaRateLimit is the steady-state request rate (requests/second).
	wikipediaRateLimit = 5
	// wikipediaRateBurst is the rate-limiter burst allowance.
	wikipediaRateBurst = 10

	// httpStatusServerErrorMin is the lowest 5xx status (transient territory).
	httpStatusServerErrorMin = 500

	// wikipediaRefusalCooldown is how long the provider stops issuing requests after
	// the upstream refuses us outright (see isPermanentRefusal). A refusal is a
	// property of this client, not of the species: retrying it per request would
	// re-issue a guaranteed-failing call for every uncached species and again for
	// every entry on each refresh sweep, because neither a transient nor a plain
	// error is persisted by fetchAndStore (only a definitive not-found is). The
	// cooldown is the backoff that classification alone cannot provide.
	wikipediaRefusalCooldown = 30 * time.Minute
)

// ErrWikipediaRefused indicates the Wikipedia API refused this client outright
// (auth/UA policy/legal block) rather than failing transiently or lacking the
// species. It is deliberately NOT a transient error: transient errors signal
// "retry soon", which is exactly wrong for a refusal that will persist until the
// client or the policy changes.
var ErrWikipediaRefused = errors.Newf("wikipedia refused the request").
	Component("guideprovider").
	Category(errors.CategoryHTTP).
	Build()

// isPermanentRefusal reports whether a status means "we will keep being refused":
// 401/403 (credentials or User-Agent policy, e.g. phab T400119), 451 (legal
// block). These do not resolve by retrying, unlike 429/408/5xx.
func isPermanentRefusal(status int) bool {
	switch status {
	case http.StatusUnauthorized, http.StatusForbidden, http.StatusUnavailableForLegalReasons:
		return true
	default:
		return false
	}
}

// sectionHeadingRegex matches a top-level MediaWiki section header line
// (== Heading ==) produced by TextExtracts with exsectionformat=wiki.
var sectionHeadingRegex = regexp.MustCompile(`^==\s*(.+?)\s*==$`)

// subSectionHeadingRegex matches deeper MediaWiki headers (=== ... ===).
var subSectionHeadingRegex = regexp.MustCompile(`^={3,}\s*(.+?)\s*={3,}$`)

// WikipediaGuideProvider fetches guide data from the Wikipedia REST/action API.
type WikipediaGuideProvider struct {
	client  *httpclient.Client
	limiter *rate.Limiter

	// refusalMu guards refusedUntil, which suppresses requests for
	// wikipediaRefusalCooldown after an outright refusal. Fetch is called from
	// many goroutines (per-request Tier-3 fetches, background refresh, pre-fetch
	// warming), so the check-and-set must be serialized.
	refusalMu    sync.Mutex
	refusedUntil time.Time
}

// refusalActive reports whether a prior refusal is still within its cooldown, in
// which case the caller must not issue another request.
func (p *WikipediaGuideProvider) refusalActive() bool {
	p.refusalMu.Lock()
	defer p.refusalMu.Unlock()
	return time.Now().Before(p.refusedUntil)
}

// noteRefusal starts (or extends) the refusal cooldown.
func (p *WikipediaGuideProvider) noteRefusal() {
	p.refusalMu.Lock()
	defer p.refusalMu.Unlock()
	p.refusedUntil = time.Now().Add(wikipediaRefusalCooldown)
}

// NewWikipediaGuideProviderWithMetrics constructs a Wikipedia provider. The
// metrics sink is recorded by the cache around Fetch, so it is accepted for
// signature compatibility but not retained here.
func NewWikipediaGuideProviderWithMetrics(_ GuideCacheMetrics) *WikipediaGuideProvider {
	// The shared client rather than a bare &http.Client: it owns a tuned,
	// pooled transport and the per-request timeout/UA plumbing, and it is what
	// the rest of the project's outbound HTTP goes through. The User-Agent is
	// still set per request (httpclient only fills one in when absent) so the
	// header self-heals when the version is published after startup.
	return &WikipediaGuideProvider{
		client: httpclient.New(&httpclient.Config{
			DefaultTimeout: wikipediaTimeout,
			UserAgent:      wikipediaUserAgent(),
		}),
		limiter: rate.NewLimiter(rate.Limit(wikipediaRateLimit), wikipediaRateBurst),
	}
}

// Name returns the provider's registration name.
func (p *WikipediaGuideProvider) Name() string { return WikipediaProviderName }

// Close releases the provider's pooled connections. GuideCache.Close probes each
// registered provider for io.Closer; without this the Wikipedia provider's idle
// connections outlived the cache that owned it.
func (p *WikipediaGuideProvider) Close() error {
	p.client.Close()
	return nil
}

// wikiQueryResponse models the action=query TextExtracts response shape.
type wikiQueryResponse struct {
	// Error is populated when the API rejects the request: MediaWiki returns
	// these with a 200 OK status and an error object instead of a query result.
	Error *struct {
		Code string `json:"code"`
		Info string `json:"info"`
	} `json:"error"`
	Query struct {
		Pages map[string]struct {
			PageID  int       `json:"pageid"`
			Title   string    `json:"title"`
			Extract string    `json:"extract"`
			FullURL string    `json:"fullurl"`
			Missing *struct{} `json:"missing"`
		} `json:"pages"`
	} `json:"query"`
}

// Fetch retrieves a species guide from the locale's Wikipedia.
func (p *WikipediaGuideProvider) Fetch(ctx context.Context, scientificName string, opts FetchOptions) (*SpeciesGuide, error) {
	// Short-circuit while a refusal cooldown is active: no HTTP call, and no seat
	// taken in the rate limiter (a queue of goroutines waiting on the limiter only
	// to be refused is the pile-up this guard exists to prevent).
	if p.refusalActive() {
		return nil, ErrWikipediaRefused
	}
	if err := p.limiter.Wait(ctx); err != nil {
		return nil, NewTransientError(err)
	}

	locale := opts.Locale
	if locale == "" {
		locale = defaultLocale
	}

	endpoint := p.buildURL(locale, scientificName)
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, endpoint, http.NoBody)
	if err != nil {
		return nil, errors.New(err).
			Component("guideprovider").
			Category(errors.CategoryHTTP).
			Context("operation", "wikipedia_request").
			Build()
	}
	req.Header.Set("Accept", "application/json")
	req.Header.Set("User-Agent", wikipediaUserAgent())

	resp, err := p.client.Do(ctx, req)
	if err != nil {
		// Network-level failures are transient.
		return nil, NewTransientError(err)
	}
	defer func() { _ = resp.Body.Close() }()

	switch {
	case resp.StatusCode == http.StatusNotFound:
		return nil, ErrGuideNotFound
	case resp.StatusCode == http.StatusTooManyRequests,
		resp.StatusCode == http.StatusRequestTimeout,
		resp.StatusCode >= httpStatusServerErrorMin:
		// 429 (rate limited), 408 (request timeout) and 5xx are transient. Returning
		// a plain (non-transient) error would make fetchAndStore persist a 30-minute
		// negative entry, suppressing retries for a species that was merely throttled
		// or briefly unavailable.
		return nil, NewTransientError(errors.Newf("wikipedia returned status %d", resp.StatusCode).
			Component("guideprovider").
			Category(errors.CategoryHTTP).
			Context("operation", "wikipedia_status_transient").
			Build())
	case isPermanentRefusal(resp.StatusCode):
		// The upstream is refusing this client, not failing to find the species.
		// Start a cooldown so the next request does not re-issue a call we already
		// know will fail, and return a non-transient error: "retry soon" is the
		// wrong signal for a condition that persists until our client changes.
		p.noteRefusal()
		GetLogger().Warn("Wikipedia refused the request; pausing Wikipedia guide fetches",
			logger.Int("status", resp.StatusCode),
			logger.String("cooldown", wikipediaRefusalCooldown.String()))
		return nil, errors.New(ErrWikipediaRefused).
			Component("guideprovider").
			Category(errors.CategoryHTTP).
			Context("operation", "wikipedia_refused").
			Context("status", resp.StatusCode).
			Build()
	case resp.StatusCode != http.StatusOK:
		// Any other non-OK status is non-definitive: wrap it transient so an
		// unexpected upstream response doesn't get persisted as a 30-minute
		// negative entry that suppresses retries for a valid species.
		return nil, NewTransientError(errors.Newf("wikipedia returned status %d", resp.StatusCode).
			Component("guideprovider").
			Category(errors.CategoryHTTP).
			Context("operation", "wikipedia_status").
			Build())
	}

	body, err := io.ReadAll(io.LimitReader(resp.Body, wikipediaMaxResponseBytes))
	if err != nil {
		return nil, NewTransientError(err)
	}

	var parsed wikiQueryResponse
	if err := json.Unmarshal(body, &parsed); err != nil {
		// A decode failure is a transport/API-shape problem, not "species not
		// found"; keep it out of the negative cache.
		return nil, NewTransientError(errors.New(err).
			Component("guideprovider").
			Category(errors.CategoryHTTP).
			Context("operation", "wikipedia_decode").
			Context("expected_path", "query.pages").
			Context("error_detail", err.Error()).
			Build())
	}

	// MediaWiki signals request-level errors (e.g. maxlag, bad params) with a
	// 200 OK body carrying an error object. Treat these as transient rather than
	// letting an empty Pages map fall through to a cached ErrGuideNotFound.
	if parsed.Error != nil {
		return nil, NewTransientError(errors.Newf("wikipedia api error: %s - %s", parsed.Error.Code, parsed.Error.Info).
			Component("guideprovider").
			Category(errors.CategoryHTTP).
			Context("operation", "wikipedia_api_error").
			Build())
	}

	for _, page := range parsed.Query.Pages {
		if page.Missing != nil || page.PageID <= 0 || strings.TrimSpace(page.Extract) == "" {
			return nil, ErrGuideNotFound
		}
		return &SpeciesGuide{
			CommonName:     page.Title,
			Description:    convertWikiSections(page.Extract),
			SourceProvider: WikipediaProviderName,
			SourceURL:      page.FullURL,
			License:        wikipediaLicense,
			LicenseURL:     wikipediaLicenseURL,
		}, nil
	}

	return nil, ErrGuideNotFound
}

// buildURL constructs the TextExtracts action API URL for a species title.
func (p *WikipediaGuideProvider) buildURL(locale, title string) string {
	q := url.Values{}
	q.Set("action", "query")
	q.Set("format", "json")
	q.Set("prop", "extracts|info")
	q.Set("explaintext", "1")
	q.Set("exsectionformat", "wiki")
	q.Set("inprop", "url")
	q.Set("redirects", "1")
	q.Set("exlimit", "1")
	q.Set("titles", title)
	return "https://" + url.PathEscape(wikipediaSubdomain(locale)) + ".wikipedia.org/w/api.php?" + q.Encode()
}

// Non-standard Wikipedia language editions whose project subdomain itself contains a
// hyphen (e.g. zh-classical.wikipedia.org). Declared as constants so the lookup table
// below and its in-package tests share one source of truth for each subdomain string.
const (
	wpEditionBatSmg      = "bat-smg"
	wpEditionBeTarask    = "be-tarask"
	wpEditionCbkZam      = "cbk-zam"
	wpEditionFiuVro      = "fiu-vro"
	wpEditionMapBms      = "map-bms"
	wpEditionNdsNl       = "nds-nl"
	wpEditionRoaRup      = "roa-rup"
	wpEditionRoaTara     = "roa-tara"
	wpEditionZhClassical = "zh-classical"
	wpEditionZhMinNan    = "zh-min-nan"
	wpEditionZhYue       = "zh-yue"
)

// wikipediaHyphenatedSubdomains lists the Wikipedia language editions whose project
// subdomain itself contains a hyphen (e.g. zh-classical.wikipedia.org). These are the
// non-standard subdomains localePattern (guideprovider.go) was widened to admit;
// wikipediaSubdomain preserves them whole rather than collapsing to a base subtag, so
// those locales fetch their own edition instead of silently falling back to the base
// language. Every OTHER hyphenated locale is a regional variant (pt-br, zh-cn) with no
// dedicated subdomain and is correctly collapsed.
var wikipediaHyphenatedSubdomains = map[string]struct{}{
	wpEditionBatSmg:      {},
	wpEditionBeTarask:    {},
	wpEditionCbkZam:      {},
	wpEditionFiuVro:      {},
	wpEditionMapBms:      {},
	wpEditionNdsNl:       {},
	wpEditionRoaRup:      {},
	wpEditionRoaTara:     {},
	wpEditionZhClassical: {},
	wpEditionZhMinNan:    {},
	wpEditionZhYue:       {},
}

// wikipediaUserAgent is the User-Agent sent to the Wikimedia API: the polite-bot
// form, because UA-policy enforcement (phab T400119) answers 403 to a bare
// "App/1.0 (url)" agent.
//
// Both the version and the contact URL are resolved at call time rather than baked
// into a literal. The literal this replaced pinned the version at "1.0", so no
// install ever reported its real build, and hardcoded upstream's repository URL, so
// a fork rebranded through internal/branding still advertised tphakala's contact
// address — which is exactly what branding.RepoURL() exists to prevent, and what
// Wikimedia's policy relies on to reach the right operator.
//
// internal/useragent owns the construction (shared with internal/imageprovider,
// the other Wikimedia client) and memoizes the result once the running version is
// published, so this no longer re-assembles the string on every request. It stays
// unlatched until then, so a provider built before main.go publishes Version
// starts reporting the real one as soon as it is known.
var wikipediaUserAgent = useragent.PoliteBot

// wikipediaSubdomain converts a UI locale to its Wikipedia language subdomain. It
// drops any regional subtag ("pt-br"/"pt_PT" -> "pt", "zh-cn" -> "zh") and applies
// the nb/nn -> no override, but PRESERVES the non-standard hyphenated subdomains that
// really exist (zh-classical, zh-min-nan, be-tarask, ...). Wikipedia has no regional
// subdomains (there is no pt-br.wikipedia.org), so passing a regional locale straight
// into the host would build an invalid URL and fail every fetch. Anything that isn't a
// recognized hyphenated subdomain or a 2-3 letter base code falls back to the default.
func wikipediaSubdomain(locale string) string {
	l := strings.ToLower(strings.TrimSpace(locale))
	// Normalize the separator so the allowlist matches an underscore form too.
	l = strings.ReplaceAll(l, "_", "-")
	// A hyphenated locale that is itself a real Wikipedia subdomain is kept whole.
	if _, ok := wikipediaHyphenatedSubdomains[l]; ok {
		return l
	}
	// Otherwise reduce to the validated primary subtag, then apply the registry's
	// per-source language remap. Both steps are shared rather than restated here:
	// BaseLanguage is the one extractor (the API layer used to carry an identical
	// copy), and the nb/nn -> no fact lives once in the sources registry, so the
	// host we fetch prose from and the host the external-link badge points at
	// cannot drift apart.
	return openfauna.SourceLang(openfauna.SourceIDWikipedia, BaseLanguage(l))
}

// convertWikiSections rewrites MediaWiki section headers in a plain-text extract
// into the "## Heading" markdown the frontend's parseGuideDescription expects.
// Top-level (== H ==) headers become "## H". Deeper headers (=== H ===) are
// flattened to a bare heading line so they don't create spurious top-level splits —
// EXCEPT when the deeper heading names a canonical comparison section
// (appearance/voice/habitat/behaviour, see isCanonicalHeading), in which case it is
// promoted to a top-level "## H" too. Bird articles routinely nest the voice
// section under "Description"/"Behaviour"; promoting it lets the frontend surface a
// distinct Voice row instead of absorbing the prose into the parent section.
func convertWikiSections(extract string) string {
	lines := strings.Split(extract, "\n")
	for i, line := range lines {
		trimmed := strings.TrimSpace(line)
		// Check deeper (===+) headers first so they aren't matched as top-level
		// "## " splits by the level-2 matcher below.
		if m := subSectionHeadingRegex.FindStringSubmatch(trimmed); m != nil {
			if isCanonicalHeading(m[1]) {
				lines[i] = SectionMarker + m[1] // promote a canonical sub-section to its own row
			} else {
				lines[i] = m[1] // flatten: keep non-canonical prose inline in the parent
			}
			continue
		}
		if m := sectionHeadingRegex.FindStringSubmatch(trimmed); m != nil {
			lines[i] = SectionMarker + m[1]
		}
	}
	return strings.TrimSpace(strings.Join(lines, "\n"))
}
