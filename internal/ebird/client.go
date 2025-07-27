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

	"github.com/patrickmn/go-cache"
	"github.com/tphakala/birdnet-go/internal/errors"
)

// Client provides methods for interacting with the eBird API
type Client struct {
	config      Config
	httpClient  *http.Client
	cache       *cache.Cache
	rateLimiter *time.Ticker
	mu          sync.RWMutex
	lastRequest time.Time
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

	client := &Client{
		config: config,
		httpClient: &http.Client{
			Timeout: config.Timeout,
		},
		cache:       cache.New(config.CacheTTL, config.CacheTTL*2),
		rateLimiter: time.NewTicker(time.Duration(config.RateLimitMS) * time.Millisecond),
	}

	return client, nil
}

// Close cleans up client resources
func (c *Client) Close() {
	c.rateLimiter.Stop()
}

// GetTaxonomy retrieves the complete eBird taxonomy, optionally filtered by locale
func (c *Client) GetTaxonomy(ctx context.Context, locale string) ([]TaxonomyEntry, error) {
	cacheKey := fmt.Sprintf("taxonomy:%s", locale)
	
	// Check cache first
	if cached, found := c.cache.Get(cacheKey); found {
		if taxonomy, ok := cached.([]TaxonomyEntry); ok {
			return taxonomy, nil
		}
	}

	// Build URL
	url := fmt.Sprintf("%s/ref/taxonomy/ebird", c.config.BaseURL)
	if locale != "" {
		url = fmt.Sprintf("%s?locale=%s", url, locale)
	}

	// Make API request
	var taxonomy []TaxonomyEntry
	err := c.doRequest(ctx, "GET", url, nil, &taxonomy)
	if err != nil {
		// doRequest already returns enhanced errors, just return them
		return nil, err
	}

	// Cache the result
	c.cache.Set(cacheKey, taxonomy, cache.DefaultExpiration)

	return taxonomy, nil
}

// GetSpeciesTaxonomy retrieves taxonomy information for a specific species
func (c *Client) GetSpeciesTaxonomy(ctx context.Context, speciesCode, locale string) (*TaxonomyEntry, error) {
	cacheKey := fmt.Sprintf("species:%s:%s", speciesCode, locale)
	
	// Check cache first
	if cached, found := c.cache.Get(cacheKey); found {
		if entry, ok := cached.(*TaxonomyEntry); ok {
			return entry, nil
		}
	}

	// Build URL
	url := fmt.Sprintf("%s/ref/taxonomy/ebird/%s", c.config.BaseURL, speciesCode)
	if locale != "" {
		url = fmt.Sprintf("%s?locale=%s", url, locale)
	}

	// Make API request
	var entries []TaxonomyEntry
	err := c.doRequest(ctx, "GET", url, nil, &entries)
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
	
	// Check cache first
	if cached, found := c.cache.Get(cacheKey); found {
		if tree, ok := cached.(*TaxonomyTree); ok {
			return tree, nil
		}
	}

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

	// Create request
	req, err := http.NewRequestWithContext(ctx, method, url, body)
	if err != nil {
		return err
	}

	// Add authentication header
	req.Header.Set("X-eBirdApiToken", c.config.APIKey)
	req.Header.Set("Accept", "application/json")
	
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}

	// Execute request
	resp, err := c.httpClient.Do(req)
	if err != nil {
		return err
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
		return err
	}

	// Check for errors
	if resp.StatusCode >= 400 {
		var apiErr Error
		if err := json.Unmarshal(bodyBytes, &apiErr); err != nil {
			// If we can't parse error response, create a generic one
			return errors.Newf("eBird API error (status %d): %s", resp.StatusCode, string(bodyBytes)).
				Category(getErrorCategory(resp.StatusCode)).
				Context("status_code", resp.StatusCode).
				Context("url", url).
				Component("ebird").
				Build()
		}
		apiErr.Status = resp.StatusCode
		
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
			return fmt.Errorf("failed to parse response: %w", err)
		}
	}

	return nil
}

// ClearCache clears all cached data
func (c *Client) ClearCache() {
	c.cache.Flush()
}

// GetCacheStats returns cache statistics
func (c *Client) GetCacheStats() (itemCount int, size int64) {
	itemCount = c.cache.ItemCount()
	// Note: go-cache doesn't provide size info directly
	return itemCount, 0
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