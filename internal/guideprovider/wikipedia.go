package guideprovider

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/url"
	"regexp"
	"strings"
	"time"

	"golang.org/x/time/rate"

	"github.com/tphakala/birdnet-go/internal/errors"
)

const (
	// wikipediaLicense and wikipediaLicenseURL describe the license of article text.
	wikipediaLicense    = "CC BY-SA 4.0"
	wikipediaLicenseURL = "https://creativecommons.org/licenses/by-sa/4.0/"

	// wikipediaUserAgent identifies the client to the Wikimedia API. Wikimedia's
	// UA-policy enforcement (phab T400119) rejects bare "App/1.0 (url)" agents
	// with HTTP 403, so we use the standard polite-bot "Mozilla/5.0 (compatible;
	// ...)" form that includes the app name and a contact URL.
	wikipediaUserAgent = "Mozilla/5.0 (compatible; BirdNET-Go/1.0; +https://github.com/tphakala/birdnet-go)"

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
)

// sectionHeadingRegex matches a top-level MediaWiki section header line
// (== Heading ==) produced by TextExtracts with exsectionformat=wiki.
var sectionHeadingRegex = regexp.MustCompile(`^==\s*(.+?)\s*==$`)

// subSectionHeadingRegex matches deeper MediaWiki headers (=== ... ===).
var subSectionHeadingRegex = regexp.MustCompile(`^={3,}\s*(.+?)\s*={3,}$`)

// WikipediaGuideProvider fetches guide data from the Wikipedia REST/action API.
type WikipediaGuideProvider struct {
	client  *http.Client
	limiter *rate.Limiter
}

// NewWikipediaGuideProviderWithMetrics constructs a Wikipedia provider. The
// metrics sink is recorded by the cache around Fetch, so it is accepted for
// signature compatibility but not retained here.
func NewWikipediaGuideProviderWithMetrics(_ GuideCacheMetrics) *WikipediaGuideProvider {
	return &WikipediaGuideProvider{
		client:  &http.Client{Timeout: wikipediaTimeout},
		limiter: rate.NewLimiter(rate.Limit(wikipediaRateLimit), wikipediaRateBurst),
	}
}

// Name returns the provider's registration name.
func (p *WikipediaGuideProvider) Name() string { return WikipediaProviderName }

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
	req.Header.Set("User-Agent", wikipediaUserAgent)

	resp, err := p.client.Do(req)
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
	return "https://" + url.PathEscape(locale) + ".wikipedia.org/w/api.php?" + q.Encode()
}

// convertWikiSections rewrites MediaWiki section headers in a plain-text extract
// into the "## Heading" markdown the frontend's parseGuideDescription expects.
// Top-level (== H ==) headers become "## H"; deeper headers are flattened to a
// bare heading line so they don't create spurious top-level splits.
func convertWikiSections(extract string) string {
	lines := strings.Split(extract, "\n")
	for i, line := range lines {
		trimmed := strings.TrimSpace(line)
		// Check deeper (===+) headers first so they are flattened, not promoted
		// to top-level "## " splits by the level-2 matcher.
		if m := subSectionHeadingRegex.FindStringSubmatch(trimmed); m != nil {
			lines[i] = m[1]
			continue
		}
		if m := sectionHeadingRegex.FindStringSubmatch(trimmed); m != nil {
			lines[i] = "## " + m[1]
		}
	}
	return strings.TrimSpace(strings.Join(lines, "\n"))
}
