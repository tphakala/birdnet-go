// Package wikipedia provides a client for fetching species summaries
// from the Wikipedia REST API.
package wikipedia

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/patrickmn/go-cache"
	"github.com/tphakala/birdnet-go/internal/errors"
	"github.com/tphakala/birdnet-go/internal/logger"
)

// DefaultCacheTTL is the default time-to-live for cached Wikipedia summaries.
const DefaultCacheTTL = 24 * time.Hour

// DefaultCacheCleanup is the interval for removing expired cache entries.
const DefaultCacheCleanup = 1 * time.Hour

// DefaultTimeout is the HTTP request timeout for Wikipedia API calls.
const DefaultTimeout = 10 * time.Second

// UserAgent identifies this application to Wikipedia per their API policy.
// Wikipedia's REST API requires a user agent that looks like a real client.
// Per https://meta.wikimedia.org/wiki/User-Agent_policy, bots must include
// contact information. We include both a browser-compatible prefix and our
// application identifier.
const UserAgent = "Mozilla/5.0 (compatible; BirdNET-Go/1.0; +https://github.com/tphakala/birdnet-go)"

// Summary represents a Wikipedia page summary from the REST API.
type Summary struct {
	Title       string      `json:"title"`
	Extract     string      `json:"extract"`
	Description string      `json:"description,omitempty"`
	Thumbnail   *Thumbnail  `json:"thumbnail,omitempty"`
	ContentURLs *ContentURL `json:"content_urls,omitempty"`
}

// Thumbnail represents a Wikipedia page thumbnail image.
type Thumbnail struct {
	Source string `json:"source"`
	Width  int    `json:"width"`
	Height int    `json:"height"`
}

// ContentURL contains links to the Wikipedia article.
type ContentURL struct {
	Desktop *PageURL `json:"desktop,omitempty"`
	Mobile  *PageURL `json:"mobile,omitempty"`
}

// PageURL holds the full article URL.
type PageURL struct {
	Page string `json:"page,omitempty"`
}

// ArticleURL returns the best available article URL, preferring mobile.
func (s *Summary) ArticleURL() string {
	if s.ContentURLs != nil {
		if s.ContentURLs.Mobile != nil && s.ContentURLs.Mobile.Page != "" {
			return s.ContentURLs.Mobile.Page
		}
		if s.ContentURLs.Desktop != nil && s.ContentURLs.Desktop.Page != "" {
			return s.ContentURLs.Desktop.Page
		}
	}
	return ""
}

// Client fetches and caches species summaries from Wikipedia.
type Client struct {
	httpClient *http.Client
	cache      *cache.Cache
	lang       string
}

// GetLogger returns the package logger for the wikipedia module.
func GetLogger() logger.Logger {
	return logger.Global().Module("wikipedia")
}

// NewClient creates a new Wikipedia client with default settings.
func NewClient() *Client {
	return &Client{
		httpClient: &http.Client{Timeout: DefaultTimeout},
		cache:      cache.New(DefaultCacheTTL, DefaultCacheCleanup),
		lang:       "en",
	}
}

// GetSummary fetches a species summary, trying commonName first then scientificName.
// Results are cached for 24 hours.
func (c *Client) GetSummary(ctx context.Context, commonName, scientificName string) (*Summary, error) {
	cacheKey := strings.ToLower(commonName)

	// Check cache first
	if cached, found := c.cache.Get(cacheKey); found {
		if summary, ok := cached.(*Summary); ok {
			return summary, nil
		}
	}

	// Try common name first (Wikipedia articles are usually titled by common name)
	summary, err := c.fetchSummary(ctx, commonName)
	if err != nil {
		// Fall back to scientific name
		summary, err = c.fetchSummary(ctx, scientificName)
		if err != nil {
			return nil, errors.Newf("wikipedia summary not found for '%s' or '%s'", commonName, scientificName).
				Category(errors.CategoryNotFound).
				Component("wikipedia").
				Build()
		}
	}

	c.cache.Set(cacheKey, summary, cache.DefaultExpiration)
	return summary, nil
}

// fetchSummary fetches a page summary from the Wikipedia REST API.
func (c *Client) fetchSummary(ctx context.Context, title string) (*Summary, error) {
	encoded := url.PathEscape(strings.ReplaceAll(title, " ", "_"))
	apiURL := fmt.Sprintf("https://%s.wikipedia.org/api/rest_v1/page/summary/%s", c.lang, encoded)

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, apiURL, nil)
	if err != nil {
		return nil, errors.New(err).
			Category(errors.CategorySystem).
			Component("wikipedia").
			Build()
	}
	req.Header.Set("User-Agent", UserAgent)
	req.Header.Set("Api-User-Agent", UserAgent)
	req.Header.Set("Accept", "application/json")

	resp, err := c.httpClient.Do(req)
	if err != nil {
		return nil, errors.New(err).
			Category(errors.CategoryNetwork).
			Component("wikipedia").
			Build()
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		// Drain body to allow connection reuse
		_, _ = io.Copy(io.Discard, resp.Body)
		return nil, errors.Newf("wikipedia returned status %d for '%s'", resp.StatusCode, title).
			Category(errors.CategoryNotFound).
			Component("wikipedia").
			Build()
	}

	var summary Summary
	if err := json.NewDecoder(resp.Body).Decode(&summary); err != nil {
		return nil, errors.New(err).
			Category(errors.CategoryProcessing).
			Component("wikipedia").
			Build()
	}

	return &summary, nil
}
