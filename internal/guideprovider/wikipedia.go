package guideprovider

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"sync"
	"time"

	"github.com/tphakala/birdnet-go/internal/errors"
	"github.com/tphakala/birdnet-go/internal/logger"
	"golang.org/x/time/rate"
)

const (
	wikipediaRESTBaseURL = "https://en.wikipedia.org/api/rest_v1/page/summary"

	// User-Agent following Wikimedia policy
	wikiUserAgent = "BirdNETGo/1.0 (https://github.com/tphakala/birdnet-go) Go-HTTP-Client"

	// Circuit breaker durations
	cbRateLimitDuration  = 60 * time.Second
	cbBlockedDuration    = 5 * time.Minute
	cbUnavailDuration    = 30 * time.Second
	cbNetworkDuration    = 2 * time.Minute

	// HTTP configuration
	wikiHTTPTimeout     = 30 * time.Second
	wikiIdleConnTimeout = 90 * time.Second

	// Rate limiting
	wikiRateLimitPerSec = 1

	// Response limits
	wikiMaxResponseBody = 512 * 1024 // 512KB max response body
)

// wikipediaSummaryResponse represents the Wikipedia REST API summary response.
type wikipediaSummaryResponse struct {
	Type        string `json:"type"`        // "standard", "disambiguation", "no-extract", etc.
	Title       string `json:"title"`
	DisplayName string `json:"displaytitle"`
	Extract     string `json:"extract"`     // Plain text summary
	Description string `json:"description"` // Short description
	ContentURLs struct {
		Desktop struct {
			Page string `json:"page"`
		} `json:"desktop"`
	} `json:"content_urls"`
}

// WikipediaGuideProvider fetches species guide text from the Wikipedia REST API.
type WikipediaGuideProvider struct {
	httpClient *http.Client
	limiter    *rate.Limiter

	// Circuit breaker
	circuitMu        sync.RWMutex
	circuitOpenUntil time.Time
	circuitFailures  int    // Number of consecutive failures
	circuitLastError string // Last error message for logging

	// testBaseURL overrides the Wikipedia API base URL for testing.
	testBaseURL string
}

// NewWikipediaGuideProvider creates a new WikipediaGuideProvider.
func NewWikipediaGuideProvider() *WikipediaGuideProvider {
	transport := &http.Transport{
		MaxIdleConns:       10,
		IdleConnTimeout:    wikiIdleConnTimeout,
		DisableCompression: false,
	}

	return &WikipediaGuideProvider{
		httpClient: &http.Client{
			Timeout:   wikiHTTPTimeout,
			Transport: transport,
		},
		limiter: rate.NewLimiter(rate.Limit(wikiRateLimitPerSec), 1),
	}
}

// Fetch retrieves species guide information from Wikipedia.
func (p *WikipediaGuideProvider) Fetch(ctx context.Context, scientificName string) (SpeciesGuide, error) {
	log := GetLogger()

	// Check circuit breaker
	if open, reason := p.isCircuitOpen(); open {
		log.Debug("Wikipedia guide circuit breaker open",
			logger.String("reason", reason),
			logger.String("species", scientificName))
		return SpeciesGuide{}, ErrAllProvidersUnavailable
	}

	// Rate limit
	if err := p.limiter.Wait(ctx); err != nil {
		return SpeciesGuide{}, errors.Newf("rate limiter: %w", err).
			Component("guideprovider").
			Category(errors.CategoryNetwork).
			Build()
	}

	// Try scientific name first
	guide, err := p.fetchSummary(ctx, scientificName)
	if err == nil {
		return guide, nil
	}

	// If not found or disambiguation, the species article might be under a different title
	log.Debug("Wikipedia scientific name lookup failed, no common name fallback available",
		logger.String("species", scientificName),
		logger.Any("error", err))

	return SpeciesGuide{}, ErrGuideNotFound
}

// fetchSummary fetches the Wikipedia REST API summary for a given title.
func (p *WikipediaGuideProvider) fetchSummary(ctx context.Context, title string) (SpeciesGuide, error) {
	// Build URL: replace spaces with underscores, URL-encode
	baseURL := wikipediaRESTBaseURL
	if p.testBaseURL != "" {
		baseURL = p.testBaseURL
	}
	encodedTitle := url.PathEscape(strings.ReplaceAll(title, " ", "_"))
	apiURL := fmt.Sprintf("%s/%s", baseURL, encodedTitle)

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, apiURL, nil)
	if err != nil {
		return SpeciesGuide{}, errors.Newf("creating request: %w", err).
			Component("guideprovider").
			Category(errors.CategoryNetwork).
			Build()
	}
	req.Header.Set("User-Agent", wikiUserAgent)
	req.Header.Set("Accept", "application/json")

	resp, err := p.httpClient.Do(req)
	if err != nil {
		p.tripCircuitBreaker(cbNetworkDuration, "network error: "+err.Error())
		return SpeciesGuide{}, errors.Newf("HTTP request failed: %w", err).
			Component("guideprovider").
			Category(errors.CategoryNetwork).
			Build()
	}
	defer resp.Body.Close()

	// Handle HTTP errors
	switch {
	case resp.StatusCode == http.StatusNotFound:
		return SpeciesGuide{}, ErrGuideNotFound
	case resp.StatusCode == http.StatusTooManyRequests:
		p.tripCircuitBreaker(cbRateLimitDuration, "rate limited")
		return SpeciesGuide{}, errors.Newf("Wikipedia rate limited").
			Component("guideprovider").
			Category(errors.CategoryNetwork).
			Build()
	case resp.StatusCode == http.StatusForbidden:
		p.tripCircuitBreaker(cbBlockedDuration, "access blocked")
		return SpeciesGuide{}, errors.Newf("Wikipedia access blocked").
			Component("guideprovider").
			Category(errors.CategoryNetwork).
			Build()
	case resp.StatusCode == http.StatusServiceUnavailable:
		p.tripCircuitBreaker(cbUnavailDuration, "service unavailable")
		return SpeciesGuide{}, errors.Newf("Wikipedia service unavailable").
			Component("guideprovider").
			Category(errors.CategoryNetwork).
			Build()
	case resp.StatusCode != http.StatusOK:
		return SpeciesGuide{}, errors.Newf("Wikipedia returned status %d", resp.StatusCode).
			Component("guideprovider").
			Category(errors.CategoryNetwork).
			Build()
	}

	// Reset circuit breaker on successful response
	p.resetCircuit()

	// Read response with size limit
	body, err := io.ReadAll(io.LimitReader(resp.Body, wikiMaxResponseBody))
	if err != nil {
		return SpeciesGuide{}, errors.Newf("reading response: %w", err).
			Component("guideprovider").
			Category(errors.CategoryNetwork).
			Build()
	}

	var summary wikipediaSummaryResponse
	if err := json.Unmarshal(body, &summary); err != nil {
		return SpeciesGuide{}, errors.Newf("parsing response: %w", err).
			Component("guideprovider").
			Category(errors.CategoryProcessing).
			Build()
	}

	// Handle disambiguation pages
	if summary.Type == "disambiguation" {
		return SpeciesGuide{}, ErrGuideNotFound
	}

	// Check for empty extract
	if summary.Extract == "" {
		return SpeciesGuide{}, ErrGuideNotFound
	}

	// Truncate description if too long
	description := summary.Extract
	if len(description) > maxDescriptionLength {
		description = description[:maxDescriptionLength] + "..."
	}

	guide := SpeciesGuide{
		ScientificName: title,
		CommonName:     summary.Title,
		Description:    description,
		SourceProvider: WikipediaProviderName,
		SourceURL:      summary.ContentURLs.Desktop.Page,
		LicenseName:    "CC BY-SA 4.0",
		LicenseURL:     "https://creativecommons.org/licenses/by-sa/4.0/",
		CachedAt:       time.Now(),
		Partial:        true, // Wikipedia REST summary only provides description
	}

	return guide, nil
}

// isCircuitOpen checks if the circuit breaker is blocking requests.
func (p *WikipediaGuideProvider) isCircuitOpen() (bool, string) {
	p.circuitMu.RLock()
	defer p.circuitMu.RUnlock()

	if time.Now().Before(p.circuitOpenUntil) {
		return true, p.circuitLastError
	}
	return false, ""
}

// tripCircuitBreaker opens the circuit breaker for the specified duration.
func (p *WikipediaGuideProvider) tripCircuitBreaker(duration time.Duration, reason string) {
	p.circuitMu.Lock()
	defer p.circuitMu.Unlock()

	p.circuitOpenUntil = time.Now().Add(duration)
	p.circuitFailures++
	p.circuitLastError = reason

	GetLogger().Error("Opening Wikipedia guide circuit breaker",
		logger.String("reason", reason),
		logger.Duration("duration", duration),
		logger.Int("consecutive_failures", p.circuitFailures))
}

// resetCircuit resets the circuit breaker on successful request.
func (p *WikipediaGuideProvider) resetCircuit() {
	p.circuitMu.Lock()
	defer p.circuitMu.Unlock()

	if p.circuitFailures > 0 {
		GetLogger().Info("Resetting Wikipedia guide circuit breaker after successful request",
			logger.Int("previous_failures", p.circuitFailures))
	}

	p.circuitOpenUntil = time.Time{}
	p.circuitFailures = 0
	p.circuitLastError = ""
}
