package ebird

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"log/slog"
	"net/http"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/patrickmn/go-cache"
	"github.com/tphakala/birdnet-go/internal/conf"
	"github.com/tphakala/birdnet-go/internal/errors"
	"github.com/tphakala/birdnet-go/internal/logging"
)

// Package-level logger specific to ebird service
var (
	logger          *slog.Logger
	serviceLevelVar = new(slog.LevelVar) // Dynamic level control
	closeLogger     func() error
)

func init() {
	var err error
	// Define log file path relative to working directory
	logFilePath := filepath.Join("logs", "ebird.log")
	initialLevel := slog.LevelDebug // Set desired initial level
	serviceLevelVar.Set(initialLevel)

	// Initialize the service-specific file logger
	logger, closeLogger, err = logging.NewFileLogger(logFilePath, "ebird", serviceLevelVar)
	if err != nil {
		// Fallback: Log error to standard log and potentially disable service logging
		log.Printf("FATAL: Failed to initialize ebird file logger at %s: %v. Service logging disabled.", logFilePath, err)
		// Set logger to a disabled handler to prevent nil panics, but respects level var
		fbHandler := slog.NewJSONHandler(io.Discard, &slog.HandlerOptions{Level: serviceLevelVar})
		logger = slog.New(fbHandler).With("service", "ebird")
		closeLogger = func() error { return nil } // No-op closer
	}
}

// Client provides methods for interacting with the eBird API
type Client struct {
	config          Config
	httpClient      *http.Client
	cache           *cache.Cache
	rateLimiter     *time.Ticker
	mu              sync.RWMutex
	lastRequest     time.Time
	debug           bool // Enable debug logging
	firstCallMade   bool // Track if first successful API call has been made
	firstCallMu     sync.Once
	
	// Metrics
	metrics struct {
		apiCalls      int64
		cacheHits     int64
		cacheMisses   int64
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
		cache:       cache.New(config.CacheTTL, config.CacheTTL*2),
		rateLimiter: time.NewTicker(time.Duration(config.RateLimitMS) * time.Millisecond),
		debug:       debug,
	}

	// Log successful initialization
	logger.Info("eBird client initialized",
		"base_url", config.BaseURL,
		"cache_ttl", config.CacheTTL,
		"rate_limit_ms", config.RateLimitMS,
		"debug", debug,
		"api_key_configured", config.APIKey != "")

	return client, nil
}

// Close cleans up client resources
func (c *Client) Close() {
	c.rateLimiter.Stop()
	logger.Info("Closing eBird client")
	
	// Close the logger if it was successfully initialized
	if closeLogger != nil {
		logger.Debug("Closing eBird service log file")
		if err := closeLogger(); err != nil {
			// Use standard log since our logger might be closing
			log.Printf("Error closing eBird logger: %v", err)
		}
	}
}

// GetTaxonomy retrieves the complete eBird taxonomy, optionally filtered by locale
func (c *Client) GetTaxonomy(ctx context.Context, locale string) ([]TaxonomyEntry, error) {
	cacheKey := fmt.Sprintf("taxonomy:%s", locale)
	
	// Check cache first
	if cached, found := c.cache.Get(cacheKey); found {
		if taxonomy, ok := cached.([]TaxonomyEntry); ok {
			c.metrics.mu.Lock()
			c.metrics.cacheHits++
			c.metrics.mu.Unlock()
			
			logger.Debug("eBird taxonomy cache hit",
				"cache_key", cacheKey,
				"entries", len(taxonomy))
			return taxonomy, nil
		}
	}
	
	// Cache miss
	c.metrics.mu.Lock()
	c.metrics.cacheMisses++
	c.metrics.mu.Unlock()

	// Apply timeout to API request
	reqCtx, cancel := context.WithTimeout(ctx, c.config.Timeout)
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

	// Cache the result
	c.cache.Set(cacheKey, taxonomy, cache.DefaultExpiration)

	logger.Debug("eBird taxonomy cached",
		"cache_key", cacheKey,
		"entries", len(taxonomy),
		"locale", locale)

	return taxonomy, nil
}

// GetSpeciesTaxonomy retrieves taxonomy information for a specific species
func (c *Client) GetSpeciesTaxonomy(ctx context.Context, speciesCode, locale string) (*TaxonomyEntry, error) {
	cacheKey := fmt.Sprintf("species:%s:%s", speciesCode, locale)
	
	// Check cache first
	if cached, found := c.cache.Get(cacheKey); found {
		if entry, ok := cached.(*TaxonomyEntry); ok {
			c.metrics.mu.Lock()
			c.metrics.cacheHits++
			c.metrics.mu.Unlock()
			
			logger.Debug("eBird species cache hit",
				"cache_key", cacheKey,
				"species_code", speciesCode)
			return entry, nil
		}
	}
	
	// Cache miss
	c.metrics.mu.Lock()
	c.metrics.cacheMisses++
	c.metrics.mu.Unlock()

	// Apply timeout to API request
	reqCtx, cancel := context.WithTimeout(ctx, c.config.Timeout)
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

	entry := &entries[0]
	
	// Cache the result
	c.cache.Set(cacheKey, entry, cache.DefaultExpiration)

	return entry, nil
}

// BuildFamilyTree builds a complete taxonomic tree for a species
func (c *Client) BuildFamilyTree(ctx context.Context, scientificName string) (*TaxonomyTree, error) {
	cacheKey := fmt.Sprintf("family_tree:%s", scientificName)
	
	logger.Debug("Building family tree",
		"scientific_name", scientificName)
	
	// Check cache first
	if cached, found := c.cache.Get(cacheKey); found {
		if tree, ok := cached.(*TaxonomyTree); ok {
			c.metrics.mu.Lock()
			c.metrics.cacheHits++
			c.metrics.mu.Unlock()
			
			logger.Debug("eBird family tree cache hit",
				"cache_key", cacheKey,
				"scientific_name", scientificName)
			return tree, nil
		}
	}
	
	// Cache miss
	c.metrics.mu.Lock()
	c.metrics.cacheMisses++
	c.metrics.mu.Unlock()

	// Get full taxonomy to search for the species
	taxonomy, err := c.GetTaxonomy(ctx, "")
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
		subspecies := c.findSubspecies(taxonomy, speciesEntry.SpeciesCode)
		tree.Subspecies = subspecies
	}

	// Cache the result
	c.cache.Set(cacheKey, tree, cache.DefaultExpiration)

	logger.Info("eBird family tree built",
		"scientific_name", scientificName,
		"order", tree.Order,
		"family", tree.Family,
		"subspecies_count", len(tree.Subspecies))

	return tree, nil
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
func (c *Client) doRequest(ctx context.Context, method, url string, body io.Reader, result interface{}) error {
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
		logger.Debug("eBird API request",
			"method", method,
			"url", url,
			"has_api_key", c.config.APIKey != "")
	}

	// Execute request
	resp, err := c.httpClient.Do(req)
	if err != nil {
		c.metrics.mu.Lock()
		c.metrics.apiErrors++
		c.metrics.mu.Unlock()
		
		logger.Error("eBird API request failed",
			"error", err,
			"method", method,
			"url", url)
		return errors.Newf("HTTP request failed: %w", err).
			Category(errors.CategoryNetwork).
			Context("method", method).
			Context("url", url).
			Component("ebird").
			Build()
	}
	defer func() {
		if err := resp.Body.Close(); err != nil {
			// Log error but don't propagate it
			_ = err
		}
	}()

	// Read response body
	bodyBytes, err := io.ReadAll(resp.Body)
	if err != nil {
		logger.Error("Failed to read response body",
			"error", err,
			"url", url,
			"status_code", resp.StatusCode)
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
		
		logger.Error("eBird API returned non-JSON response",
			"status_code", resp.StatusCode,
			"content_type", contentType,
			"url", url,
			"response_preview", responsePreview)
		
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
				logger.Error("eBird API authentication failed",
					"status_code", resp.StatusCode,
					"url", url,
					"response_body", string(bodyBytes),
					"has_api_key", c.config.APIKey != "",
					"message", "Check your eBird API key in the configuration")
			} else {
				logger.Error("eBird API error",
					"status_code", resp.StatusCode,
					"url", url,
					"response_body", string(bodyBytes))
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
			logger.Error("eBird API authentication failed",
				"status_code", resp.StatusCode,
				"error_title", apiErr.Title,
				"error_detail", apiErr.Detail,
				"url", url,
				"has_api_key", c.config.APIKey != "",
				"message", "Check your eBird API key in the configuration")
		} else {
			logger.Warn("eBird API error response",
				"status_code", resp.StatusCode,
				"error_title", apiErr.Title,
				"error_detail", apiErr.Detail,
				"url", url)
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
			
			logger.Error("Failed to parse eBird API response",
				"error", err,
				"url", url,
				"response_size", len(bodyBytes),
				"response_preview", responsePreview,
				"content_type", resp.Header.Get("Content-Type"))
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
			logger.Info("eBird API authentication successful",
				"first_successful_request", url,
				"message", "eBird API key is valid and working")
		})
		
		if c.debug {
			logger.Debug("eBird API response",
				"status_code", resp.StatusCode,
				"url", url,
				"duration_ms", duration.Milliseconds(),
				"response_size", len(bodyBytes))
			
			// Log detailed response body for debugging if it's not too large
			if len(bodyBytes) < 10000 { // Only log if less than 10KB
				logger.Debug("eBird API response body",
					"url", url,
					"response", string(bodyBytes))
			}
		} else {
			logger.Info("eBird API request successful",
				"url", url,
				"duration_ms", duration.Milliseconds())
		}
	}
	
	// Track successful API call duration
	c.metrics.mu.Lock()
	c.metrics.totalDuration += duration
	c.metrics.mu.Unlock()

	return nil
}

// doRequestWithRetry wraps doRequest with retry logic for transient failures
func (c *Client) doRequestWithRetry(ctx context.Context, method, url string, body io.Reader, result interface{}) error {
	const maxRetries = 3
	var lastErr error
	
	for attempt := 0; attempt < maxRetries; attempt++ {
		// For retries after the first attempt, create a new body reader if needed
		var reqBody io.Reader
		if body != nil && attempt > 0 {
			// Body was already consumed, we can't retry with body
			// This is a limitation - callers should use bytes.Buffer if retry is needed
			logger.Debug("Retry attempted but request body cannot be re-read",
				"attempt", attempt+1,
				"url", url)
			return lastErr
		}
		reqBody = body
		
		err := c.doRequest(ctx, method, url, reqBody, result)
		if err == nil {
			return nil
		}
		
		// Check if error is retryable
		var enhancedErr *errors.EnhancedError
		if errors.As(err, &enhancedErr) {
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
			logger.Warn("eBird API request failed, retrying",
				"attempt", attempt+1,
				"max_retries", maxRetries,
				"delay_ms", delay.Milliseconds(),
				"url", url,
				"error", err.Error())
			
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
	c.cache.Flush()
	logger.Info("eBird cache cleared")
}

// GetCacheStats returns cache statistics
func (c *Client) GetCacheStats() (itemCount int, size int64) {
	itemCount = c.cache.ItemCount()
	// Note: go-cache doesn't provide size info directly
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
	c.metrics.mu.RLock()
	defer c.metrics.mu.RUnlock()
	
	metrics := Metrics{
		APICalls:      c.metrics.apiCalls,
		CacheHits:     c.metrics.cacheHits,
		CacheMisses:   c.metrics.cacheMisses,
		APIErrors:     c.metrics.apiErrors,
		TotalDuration: c.metrics.totalDuration,
	}
	
	if metrics.APICalls > 0 {
		metrics.AvgDuration = time.Duration(int64(metrics.TotalDuration) / metrics.APICalls)
	}
	
	return metrics
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