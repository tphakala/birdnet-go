package ebird

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"sync"
	"time"

	"github.com/tphakala/birdnet-go/internal/conf"
	"github.com/tphakala/birdnet-go/internal/errors"
	"github.com/tphakala/birdnet-go/internal/logger"
	"github.com/tphakala/birdnet-go/internal/ttlcache"
)

// GetLogger returns the package logger for the ebird module
func GetLogger() logger.Logger {
	return logger.Global().Module("ebird")
}

// speciesKey identifies one cached species taxonomy lookup.
type speciesKey struct {
	code, locale string
}

// Client provides methods for interacting with the eBird API
type Client struct {
	config        Config
	httpClient    *http.Client
	taxonomy      *ttlcache.Cache[string, []TaxonomyEntry]    // keyed by locale
	species       *ttlcache.Cache[speciesKey, *TaxonomyEntry] // keyed by species code and locale
	familyTrees   *ttlcache.Cache[string, *TaxonomyTree]      // keyed by scientific name as passed
	observations  *ttlcache.Cache[observationKey, []Observation]
	rateLimiter   *time.Ticker
	mu            sync.RWMutex
	lastRequest   time.Time
	debug         bool // Enable debug logging
	firstCallMade bool // Track if first successful API call has been made
	firstCallMu   sync.Once

	// Metrics
	metrics struct {
		apiCalls      int64
		apiErrors     int64
		totalDuration time.Duration
		mu            sync.RWMutex
	}
}

// NewClient creates a new eBird API client
func NewClient(config Config) (*Client, error) {
	if config.APIKey == "" {
		return nil, errors.Newf("eBird API key is required").
			Category(errors.CategoryConfiguration).
			Component("ebird").
			Build()
	}

	// Use defaults for missing config values
	if config.BaseURL == "" {
		config.BaseURL = DefaultConfig().BaseURL
	}
	if config.Timeout == 0 {
		config.Timeout = DefaultConfig().Timeout
	}
	if config.CacheTTL == 0 {
		config.CacheTTL = DefaultConfig().CacheTTL
	}
	if config.RateLimitMS == 0 {
		config.RateLimitMS = DefaultConfig().RateLimitMS
	}

	// Get global debug setting
	settings := conf.GetSettings()
	debug := settings != nil && settings.Debug

	client := &Client{
		config: config,
		httpClient: &http.Client{
			Timeout: config.Timeout,
		},
		taxonomy:     ttlcache.New[string, []TaxonomyEntry](config.CacheTTL),
		species:      ttlcache.New[speciesKey, *TaxonomyEntry](config.CacheTTL),
		familyTrees:  ttlcache.New[string, *TaxonomyTree](config.CacheTTL),
		observations: ttlcache.New[observationKey, []Observation](config.CacheTTL),
		rateLimiter:  time.NewTicker(time.Duration(config.RateLimitMS) * time.Millisecond),
		debug:        debug,
	}

	// Log successful initialization
	GetLogger().Info("eBird client initialized",
		logger.String("base_url", config.BaseURL),
		logger.String("cache_ttl", config.CacheTTL.String()),
		logger.Int("rate_limit_ms", config.RateLimitMS),
		logger.Bool("debug", debug),
		logger.Bool("api_key_configured", config.APIKey != ""))

	return client, nil
}

// Close cleans up client resources
func (c *Client) Close() {
	c.rateLimiter.Stop()
	GetLogger().Info("Closing eBird client")
}

// GetTaxonomy retrieves the complete eBird taxonomy, optionally filtered by locale.
// Concurrent calls for the same locale share one download.
func (c *Client) GetTaxonomy(ctx context.Context, locale string) ([]TaxonomyEntry, error) {
	return c.taxonomy.GetOrLoad(ctx, locale, func(loadCtx context.Context) ([]TaxonomyEntry, error) {
		// Apply timeout to API request
		reqCtx, cancel := context.WithTimeout(loadCtx, c.config.Timeout)
		defer cancel()

		// Build URL - eBird API defaults to CSV, we need to specify fmt=json
		url := fmt.Sprintf("%s/ref/taxonomy/ebird?fmt=json", c.config.BaseURL)
		if locale != "" {
			url = fmt.Sprintf("%s&locale=%s", url, locale)
		}

		// Make API request with retry for transient failures
		var taxonomy []TaxonomyEntry
		err := c.doRequestWithRetry(reqCtx, "GET", url, nil, &taxonomy)
		if err != nil {
			// doRequest already returns enhanced errors, just return them
			return nil, err
		}

		GetLogger().Debug("eBird taxonomy fetched",
			logger.Int("entries", len(taxonomy)),
			logger.String("locale", locale))

		return taxonomy, nil
	})
}

// GetSpeciesTaxonomy retrieves taxonomy information for a specific species
func (c *Client) GetSpeciesTaxonomy(ctx context.Context, speciesCode, locale string) (*TaxonomyEntry, error) {
	key := speciesKey{code: speciesCode, locale: locale}
	return c.species.GetOrLoad(ctx, key, func(loadCtx context.Context) (*TaxonomyEntry, error) {
		// Apply timeout to API request
		reqCtx, cancel := context.WithTimeout(loadCtx, c.config.Timeout)
		defer cancel()

		// Build URL - eBird API defaults to CSV, we need to specify fmt=json
		url := fmt.Sprintf("%s/ref/taxonomy/ebird/%s?fmt=json", c.config.BaseURL, speciesCode)
		if locale != "" {
			url = fmt.Sprintf("%s&locale=%s", url, locale)
		}

		// Make API request with retry for transient failures
		var entries []TaxonomyEntry
		err := c.doRequestWithRetry(reqCtx, "GET", url, nil, &entries)
		if err != nil {
			// doRequest already returns enhanced errors, just return them
			return nil, err
		}

		if len(entries) == 0 {
			return nil, errors.Newf("species not found: %s", speciesCode).
				Category(errors.CategoryNotFound).
				Context("species_code", speciesCode).
				Component("ebird").
				Build()
		}

		return &entries[0], nil
	})
}

// BuildFamilyTree builds a complete taxonomic tree for a species
func (c *Client) BuildFamilyTree(ctx context.Context, scientificName string) (*TaxonomyTree, error) {
	GetLogger().Debug("Building family tree",
		logger.String("scientific_name", scientificName))

	return c.familyTrees.GetOrLoad(ctx, scientificName, func(loadCtx context.Context) (*TaxonomyTree, error) {
		// Get full taxonomy to search for the species
		taxonomy, err := c.GetTaxonomy(loadCtx, "")
		if err != nil {
			return nil, err
		}

		// Find the species in taxonomy
		var speciesEntry *TaxonomyEntry
		for i := range taxonomy {
			if strings.EqualFold(taxonomy[i].ScientificName, scientificName) {
				speciesEntry = &taxonomy[i]
				break
			}
		}

		if speciesEntry == nil {
			return nil, errors.Newf("species not found in eBird taxonomy: %s", scientificName).
				Category(errors.CategoryNotFound).
				Context("scientific_name", scientificName).
				Component("ebird").
				Build()
		}

		// Parse genus from scientific name (first part before space)
		parts := strings.Split(speciesEntry.ScientificName, " ")
		genus := ""
		if len(parts) > 0 {
			genus = parts[0]
		}

		// Build the family tree
		tree := &TaxonomyTree{
			Kingdom:       "Animalia", // All birds are in kingdom Animalia
			Phylum:        "Chordata", // All birds are in phylum Chordata
			Class:         "Aves",     // All entries are birds
			Order:         speciesEntry.Order,
			Family:        speciesEntry.FamilySciName,
			FamilyCommon:  speciesEntry.FamilyComName,
			Genus:         genus,
			Species:       speciesEntry.ScientificName,
			SpeciesCommon: speciesEntry.CommonName,
			UpdatedAt:     time.Now(),
		}

		// Find subspecies if this is a species entry
		if speciesEntry.Category == "species" {
			tree.Subspecies = c.findSubspecies(taxonomy, speciesEntry.SpeciesCode)
		}

		GetLogger().Info("eBird family tree built",
			logger.String("scientific_name", scientificName),
			logger.String("order", tree.Order),
			logger.String("family", tree.Family),
			logger.Int("subspecies_count", len(tree.Subspecies)))

		return tree, nil
	})
}

// findSubspecies finds all subspecies for a given species code
func (c *Client) findSubspecies(taxonomy []TaxonomyEntry, speciesCode string) []string {
	var subspecies []string

	for i := range taxonomy {
		// Check if this entry reports as our species and is a subspecies category
		if taxonomy[i].ReportAs == speciesCode &&
			(taxonomy[i].Category == "issf" || taxonomy[i].Category == "form") {
			subspecies = append(subspecies, taxonomy[i].ScientificName)
		}
	}

	return subspecies
}

// doRequest performs an HTTP request with rate limiting and auth
func (c *Client) doRequest(ctx context.Context, method, url string, body io.Reader, result any) error {
	log := GetLogger()

	// Rate limiting
	c.mu.Lock()
	<-c.rateLimiter.C
	c.lastRequest = time.Now()
	c.mu.Unlock()

	start := time.Now()

	// Track API call
	c.metrics.mu.Lock()
	c.metrics.apiCalls++
	c.metrics.mu.Unlock()

	// Create request
	req, err := http.NewRequestWithContext(ctx, method, url, body)
	if err != nil {
		c.metrics.mu.Lock()
		c.metrics.apiErrors++
		c.metrics.mu.Unlock()
		return errors.Newf("failed to create HTTP request: %w", err).
			Category(errors.CategoryNetwork).
			Context("method", method).
			Context("url", url).
			Component("ebird").
			Build()
	}

	// Add authentication header
	req.Header.Set("X-eBirdApiToken", c.config.APIKey)
	req.Header.Set("Accept", "application/json")

	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}

	// Log request if debug enabled
	if c.debug {
		log.Debug("eBird API request",
			logger.String("method", method),
			logger.String("url", url),
			logger.Bool("has_api_key", c.config.APIKey != ""))
	}

	// Execute request
	resp, err := c.httpClient.Do(req)
	if err != nil {
		c.metrics.mu.Lock()
		c.metrics.apiErrors++
		c.metrics.mu.Unlock()

		log.Error("eBird API request failed",
			logger.Error(err),
			logger.String("method", method),
			logger.String("url", url))
		return errors.Newf("HTTP request failed: %w", err).
			Category(errors.CategoryNetwork).
			Context("method", method).
			Context("url", url).
			Component("ebird").
			Build()
	}
	defer func() {
		if err := resp.Body.Close(); err != nil {
			log.Warn("Failed to close response body",
				logger.Error(err),
				logger.String("url", url))
		}
	}()

	// Read response body
	bodyBytes, err := io.ReadAll(resp.Body)
	if err != nil {
		log.Error("Failed to read response body",
			logger.Error(err),
			logger.String("url", url),
			logger.Int("status_code", resp.StatusCode))
		return errors.Newf("failed to read response body: %w", err).
			Category(errors.CategoryNetwork).
			Context("url", url).
			Context("status_code", resp.StatusCode).
			Component("ebird").
			Build()
	}

	// Check content type for non-error responses
	contentType := resp.Header.Get("Content-Type")
	if resp.StatusCode == 200 && !strings.Contains(strings.ToLower(contentType), "application/json") {
		// Log error for non-JSON responses
		responsePreview := string(bodyBytes)
		if len(responsePreview) > 500 {
			responsePreview = responsePreview[:500] + "..."
		}

		log.Error("eBird API returned non-JSON response",
			logger.Int("status_code", resp.StatusCode),
			logger.String("content_type", contentType),
			logger.String("url", url),
			logger.String("response_preview", responsePreview))

		return errors.Newf("eBird API returned non-JSON response (Content-Type: %s)", contentType).
			Category(errors.CategoryNetwork).
			Context("status_code", resp.StatusCode).
			Context("content_type", contentType).
			Context("url", url).
			Component("ebird").
			Build()
	}

	// Check for errors
	if resp.StatusCode >= 400 {
		// Track API error
		c.metrics.mu.Lock()
		c.metrics.apiErrors++
		c.metrics.mu.Unlock()

		var apiErr Error
		if err := json.Unmarshal(bodyBytes, &apiErr); err != nil {
			// Log authentication failures specially
			if resp.StatusCode == 401 || resp.StatusCode == 403 {
				log.Error("eBird API authentication failed",
					logger.Int("status_code", resp.StatusCode),
					logger.String("url", url),
					logger.String("response_body", string(bodyBytes)),
					logger.Bool("has_api_key", c.config.APIKey != ""),
					logger.String("message", "Check your eBird API key in the configuration"))
			} else {
				log.Error("eBird API error",
					logger.Int("status_code", resp.StatusCode),
					logger.String("url", url),
					logger.String("response_body", string(bodyBytes)))
			}

			// If we can't parse error response, create a generic one
			return errors.Newf("eBird API error (status %d): %s", resp.StatusCode, string(bodyBytes)).
				Category(getErrorCategory(resp.StatusCode)).
				Context("status_code", resp.StatusCode).
				Context("url", url).
				Component("ebird").
				Build()
		}
		apiErr.Status = resp.StatusCode

		// Log authentication failures specially
		if resp.StatusCode == 401 || resp.StatusCode == 403 {
			log.Error("eBird API authentication failed",
				logger.Int("status_code", resp.StatusCode),
				logger.String("error_title", apiErr.Title),
				logger.String("error_detail", apiErr.Detail),
				logger.String("url", url),
				logger.Bool("has_api_key", c.config.APIKey != ""),
				logger.String("message", "Check your eBird API key in the configuration"))
		} else {
			log.Warn("eBird API error response",
				logger.Int("status_code", resp.StatusCode),
				logger.String("error_title", apiErr.Title),
				logger.String("error_detail", apiErr.Detail),
				logger.String("url", url))
		}

		// Wrap API error with enhanced error for proper notification
		return errors.Newf("eBird API error: %s", apiErr.Detail).
			Category(getErrorCategory(resp.StatusCode)).
			Context("status_code", resp.StatusCode).
			Context("error_title", apiErr.Title).
			Context("url", url).
			Component("ebird").
			Build()
	}

	// Parse successful response
	if result != nil {
		if err := json.Unmarshal(bodyBytes, result); err != nil {
			// Log first 500 chars of response to debug parsing issues
			responsePreview := string(bodyBytes)
			if len(responsePreview) > 500 {
				responsePreview = responsePreview[:500] + "..."
			}

			log.Error("Failed to parse eBird API response",
				logger.Error(err),
				logger.String("url", url),
				logger.Int("response_size", len(bodyBytes)),
				logger.String("response_preview", responsePreview),
				logger.String("content_type", resp.Header.Get("Content-Type")))
			return errors.Newf("failed to parse response: %w", err).
				Category(errors.CategoryFileParsing).
				Context("url", url).
				Context("response_size", len(bodyBytes)).
				Component("ebird").
				Build()
		}
	}

	duration := time.Since(start)

	// Log successful requests
	if resp.StatusCode == 200 {
		// Log first successful API call to confirm authentication
		c.firstCallMu.Do(func() {
			log.Info("eBird API authentication successful",
				logger.String("first_successful_request", url),
				logger.String("message", "eBird API key is valid and working"))
		})

		if c.debug {
			log.Debug("eBird API response",
				logger.Int("status_code", resp.StatusCode),
				logger.String("url", url),
				logger.Int64("duration_ms", duration.Milliseconds()),
				logger.Int("response_size", len(bodyBytes)))

			// Log detailed response body for debugging if it's not too large
			if len(bodyBytes) < 10000 { // Only log if less than 10KB
				log.Debug("eBird API response body",
					logger.String("url", url),
					logger.String("response", string(bodyBytes)))
			}
		} else {
			log.Info("eBird API request successful",
				logger.String("url", url),
				logger.Int64("duration_ms", duration.Milliseconds()))
		}
	}

	// Track successful API call duration
	c.metrics.mu.Lock()
	c.metrics.totalDuration += duration
	c.metrics.mu.Unlock()

	return nil
}

// doRequestWithRetry wraps doRequest with retry logic for transient failures
func (c *Client) doRequestWithRetry(ctx context.Context, method, url string, body io.Reader, result any) error {
	log := GetLogger()
	const maxRetries = 3
	var lastErr error

	for attempt := range maxRetries {
		// For retries after the first attempt, create a new body reader if needed
		var reqBody io.Reader
		if body != nil && attempt > 0 {
			// Body was already consumed, we can't retry with body
			// This is a limitation - callers should use bytes.Buffer if retry is needed
			log.Debug("Retry attempted but request body cannot be re-read",
				logger.Int("attempt", attempt+1),
				logger.String("url", url))
			return lastErr
		}
		reqBody = body

		err := c.doRequest(ctx, method, url, reqBody, result)
		if err == nil {
			return nil
		}

		// Check if error is retryable
		if enhancedErr, ok := errors.AsType[*errors.EnhancedError](err); ok {
			// Don't retry authentication errors or not found errors
			if enhancedErr.Category == errors.CategoryConfiguration ||
				enhancedErr.Category == errors.CategoryNotFound ||
				enhancedErr.Category == errors.CategoryValidation {
				return err
			}

			// Check for specific status codes
			if statusCode, ok := enhancedErr.Context["status_code"].(int); ok {
				// Don't retry client errors (except 429 which is handled by rate limiter)
				if statusCode >= 400 && statusCode < 500 && statusCode != 429 {
					return err
				}
			}
		}

		lastErr = err

		// Don't retry if context is cancelled
		if ctx.Err() != nil {
			return lastErr
		}

		// Calculate backoff delay
		delay := time.Duration(attempt+1) * 500 * time.Millisecond
		if attempt < maxRetries-1 {
			log.Warn("eBird API request failed, retrying",
				logger.Int("attempt", attempt+1),
				logger.Int("max_retries", maxRetries),
				logger.Int64("delay_ms", delay.Milliseconds()),
				logger.String("url", url),
				logger.Error(err))

			select {
			case <-time.After(delay):
				// Continue to next retry
			case <-ctx.Done():
				return ctx.Err()
			}
		}
	}

	return lastErr
}

// ClearCache clears all cached data
func (c *Client) ClearCache() {
	c.taxonomy.Clear()
	c.species.Clear()
	c.familyTrees.Clear()
	c.observations.Clear()
	GetLogger().Info("eBird cache cleared")
}

// GetCacheStats returns the number of live cached items across all eBird
// caches. The size is not tracked and is always 0.
func (c *Client) GetCacheStats() (itemCount int, size int64) {
	itemCount = c.taxonomy.Len() + c.species.Len() + c.familyTrees.Len() + c.observations.Len()
	return itemCount, 0
}

// Metrics represents eBird client performance metrics
type Metrics struct {
	APICalls      int64         `json:"api_calls"`
	CacheHits     int64         `json:"cache_hits"`
	CacheMisses   int64         `json:"cache_misses"`
	APIErrors     int64         `json:"api_errors"`
	TotalDuration time.Duration `json:"total_duration"`
	AvgDuration   time.Duration `json:"avg_duration"`
}

// GetMetrics returns current client metrics
func (c *Client) GetMetrics() Metrics {
	cacheHits, cacheMisses := c.cacheTotals()

	c.metrics.mu.RLock()
	defer c.metrics.mu.RUnlock()

	metrics := Metrics{
		APICalls:      c.metrics.apiCalls,
		CacheHits:     cacheHits,
		CacheMisses:   cacheMisses,
		APIErrors:     c.metrics.apiErrors,
		TotalDuration: c.metrics.totalDuration,
	}

	if metrics.APICalls > 0 {
		metrics.AvgDuration = time.Duration(int64(metrics.TotalDuration) / metrics.APICalls)
	}

	return metrics
}

// cacheTotals sums the hit and miss counters of all four caches.
func (c *Client) cacheTotals() (hits, misses int64) {
	for _, st := range []ttlcache.Stats{
		c.taxonomy.Stats(), c.species.Stats(), c.familyTrees.Stats(), c.observations.Stats(),
	} {
		hits += int64(st.Hits)
		misses += int64(st.Misses)
	}
	return hits, misses
}

// getErrorCategory determines the appropriate error category based on HTTP status code
func getErrorCategory(statusCode int) errors.ErrorCategory {
	switch statusCode {
	case 401, 403:
		// Authentication/authorization errors - these are critical for user attention
		return errors.CategoryConfiguration
	case 429:
		// Rate limiting
		return errors.CategoryLimit
	case 404:
		return errors.CategoryNotFound
	case 500, 502, 503, 504:
		// Server errors
		return errors.CategoryNetwork
	default:
		return errors.CategoryNetwork
	}
}
