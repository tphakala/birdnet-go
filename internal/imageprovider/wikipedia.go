// wikipedia.go: Package imageprovider provides functionality for fetching and caching bird images.
package imageprovider

import (
	"bytes"
	"context"
	"log/slog"
	"strings"
	"time"

	"cgt.name/pkg/go-mwclient"
	"github.com/antonholmquist/jason"
	"github.com/google/uuid"
	"github.com/k3a/html2text"
	"github.com/tphakala/birdnet-go/internal/conf"
	"github.com/tphakala/birdnet-go/internal/errors"
	"golang.org/x/net/html"
	"golang.org/x/time/rate"
)

const wikiProviderName = "wikipedia"

// wikiMediaProvider implements the ImageProvider interface for Wikipedia.
type wikiMediaProvider struct {
	client            *mwclient.Client
	debug             bool
	limiter           *rate.Limiter // For user-initiated requests
	backgroundLimiter *rate.Limiter // For background refresh operations
	maxRetries        int
}

// wikiMediaAuthor represents the author information for a Wikipedia image.
type wikiMediaAuthor struct {
	name        string
	URL         string
	licenseName string
	licenseURL  string
}

// NewWikiMediaProvider creates a new Wikipedia media provider.
// It initializes a new mwclient for interacting with the Wikipedia API.
func NewWikiMediaProvider() (*wikiMediaProvider, error) {
	// Use the shared imageProviderLogger
	logger := imageProviderLogger.With("provider", wikiProviderName)
	logger.Info("Initializing WikiMedia provider")
	settings := conf.Setting()
	client, err := mwclient.New("https://wikipedia.org/w/api.php", "BirdNET-Go")
	if err != nil {
		enhancedErr := errors.New(err).
			Component("imageprovider").
			Category(errors.CategoryNetwork).
			Context("provider", wikiProviderName).
			Context("operation", "create_mwclient").
			Context("api_url", "https://wikipedia.org/w/api.php").
			Build()
		logger.Error("Failed to create mwclient for Wikipedia API", "error", enhancedErr)
		return nil, enhancedErr
	}

	// Rate limiting is only applied to background cache refresh operations
	// User requests are not rate limited to ensure UI responsiveness
	// Background operations: 2 requests per second to respect Wikipedia's rate limits
	limiter := rate.NewLimiter(rate.Limit(10), 10) // Kept for backward compatibility but not used
	backgroundLimiter := rate.NewLimiter(rate.Limit(2), 2)
	logger.Info("WikiMedia provider initialized", "user_rate_limit", "none", "background_rate_limit_rps", 2)
	return &wikiMediaProvider{
		client:            client,
		debug:             settings.Realtime.Dashboard.Thumbnails.Debug,
		limiter:           limiter,
		backgroundLimiter: backgroundLimiter,
		maxRetries:        3,
	}, nil
}

// queryWithRetryAndLimiter performs a query with retry logic using the specified rate limiter.
func (l *wikiMediaProvider) queryWithRetryAndLimiter(reqID string, params map[string]string, limiter *rate.Limiter) (*jason.Object, error) {
	logger := imageProviderLogger.With("provider", wikiProviderName, "request_id", reqID, "api_action", params["action"])
	var lastErr error
	for attempt := 0; attempt < l.maxRetries; attempt++ {
		attemptLogger := logger.With("attempt", attempt+1, "max_attempts", l.maxRetries)
		attemptLogger.Debug("Attempting Wikipedia API request")
		// if l.debug {
		// 	log.Printf("[%s] Debug: API request attempt %d", reqID, attempt+1)
		// }
		// Wait for rate limiter if one is provided (only for background operations)
		if limiter != nil {
			attemptLogger.Debug("Waiting for rate limiter")
			err := limiter.Wait(context.Background()) // Using Background context for limiter wait
			if err != nil {
				enhancedErr := errors.New(err).
					Component("imageprovider").
					Category(errors.CategoryNetwork).
					Context("provider", wikiProviderName).
					Context("request_id", reqID).
					Context("operation", "rate_limiter_wait").
					Build()
				attemptLogger.Error("Rate limiter error", "error", enhancedErr)
				// Don't retry on limiter error, return immediately
				return nil, enhancedErr
			}
		} else {
			attemptLogger.Debug("No rate limiting applied (user request)")
		}

		attemptLogger.Debug("Sending GET request to Wikipedia API")
		resp, err := l.client.Get(params)
		if err == nil {
			attemptLogger.Debug("API request successful")
			return resp, nil // Success
		}

		lastErr = err
		attemptLogger.Warn("API request failed", "error", err)
		// if l.debug {
		// 	log.Printf("Debug: API request attempt %d failed: %v", attempt+1, err)
		// }

		// Wait before retry (exponential backoff)
		waitDuration := time.Second * time.Duration(1<<attempt)
		attemptLogger.Debug("Waiting before retry", "duration", waitDuration)
		time.Sleep(waitDuration)
	}

	logger.Error("API request failed after all retries", "last_error", lastErr)
	enhancedErr := errors.New(lastErr).
		Component("imageprovider").
		Category(errors.CategoryNetwork).
		Context("provider", wikiProviderName).
		Context("request_id", reqID).
		Context("max_retries", l.maxRetries).
		Context("operation", "query_with_retry").
		Build()
	return nil, enhancedErr
}

// queryAndGetFirstPageWithLimiter queries Wikipedia with given parameters using the specified rate limiter.
func (l *wikiMediaProvider) queryAndGetFirstPageWithLimiter(reqID string, params map[string]string, limiter *rate.Limiter) (*jason.Object, error) {
	logger := imageProviderLogger.With("provider", wikiProviderName, "request_id", reqID, "api_action", params["action"], "titles", params["titles"])
	logger.Info("Querying Wikipedia API")
	// if l.debug {
	// 	log.Printf("[%s] Debug: Querying Wikipedia API with params: %v", reqID, params)
	// }

	resp, err := l.queryWithRetryAndLimiter(reqID, params, limiter)
	if err != nil {
		// Error already logged in queryWithRetry
		// if l.debug {
		// 	log.Printf("Debug: Wikipedia API query failed after retries: %v", err)
		// }
		// Error already enhanced in queryWithRetry
		return nil, err
	}

	// Optionally log raw response at Debug level
	if logger.Enabled(context.Background(), slog.LevelDebug) { // Check if Debug level is enabled
		if respObj, errJson := resp.Object(); errJson == nil {
			logger.Debug("Raw Wikipedia API response", "response", respObj.String())
		} else {
			logger.Debug("Failed to format raw response for logging", "error", errJson)
		}
	}
	// if l.debug {
	// 	if obj, err := resp.Object(); err == nil {
	// 		log.Printf("[%s] Debug: Raw Wikipedia API response: %v", reqID, obj)
	// 	}
	// }

	logger.Debug("Parsing pages from API response")

	// First check if the response has a query field at all
	query, err := resp.GetObject("query")
	if err != nil {
		// No query field usually means the API returned an error or unexpected structure
		logger.Debug("No 'query' field in Wikipedia response", "error", err)

		// Check if there's an error field in the response
		if errorObj, errCheck := resp.GetObject("error"); errCheck == nil {
			if errorCode, errCode := errorObj.GetString("code"); errCode == nil {
				if errorInfo, errInfo := errorObj.GetString("info"); errInfo == nil {
					logger.Warn("Wikipedia API returned error", "error_code", errorCode, "error_info", errorInfo)
				}
			}
		}

		// This is likely a "not found" scenario, not an error worth reporting to telemetry
		return nil, ErrImageNotFound
	}

	// Try to get pages array from the query object
	pages, err := query.GetObjectArray("pages")
	if err != nil {
		logger.Debug("No 'pages' field in query response", "error", err)

		// Check for alternative structures that might indicate page issues
		// Check for redirects
		if redirects, redirectErr := query.GetObjectArray("redirects"); redirectErr == nil && len(redirects) > 0 {
			logger.Debug("Wikipedia response contains redirects but no pages")
		}

		// Check for normalized titles
		if normalized, normalErr := query.GetObjectArray("normalized"); normalErr == nil && len(normalized) > 0 {
			logger.Debug("Wikipedia response contains normalized titles but no pages")
		}

		// This is a common scenario for species without Wikipedia pages
		// Don't create telemetry noise - treat as "not found"
		logger.Debug("Wikipedia page structure indicates no available page for species")
		return nil, ErrImageNotFound
	}

	if len(pages) == 0 {
		logger.Warn("No pages found in Wikipedia response")
		// Log full response if debug enabled
		if logger.Enabled(context.Background(), slog.LevelDebug) {
			if respObj, errJson := resp.Object(); errJson == nil {
				logger.Debug("Full response structure (no pages found)", "response", respObj.String())
			}
		}
		// if l.debug {
		// 	log.Printf("Debug: No pages found in Wikipedia response for params: %v", params)
		// 	if obj, err := resp.Object(); err == nil {
		// 		log.Printf("Debug: Full response structure: %v", obj)
		// 	}
		// }
		// Return specific error indicating page not found
		return nil, ErrImageNotFound // Use the package-level error
	}

	// Optionally log first page content at Debug level
	if logger.Enabled(context.Background(), slog.LevelDebug) {
		if firstPageObj, errJson := pages[0].Object(); errJson == nil {
			logger.Debug("First page content from API response", "page_content", firstPageObj.String())
		}
	}
	// if l.debug {
	// 	if firstPage, err := pages[0].Object(); err == nil {
	// 		log.Printf("[%s] Debug: First page content: %v", reqID, firstPage)
	// 		log.Printf("[%s] Debug: Successfully retrieved Wikipedia page", reqID)
	// 	}
	// }

	logger.Debug("Successfully retrieved page data from Wikipedia")
	return pages[0], nil
}

// FetchWithContext retrieves the bird image for a given scientific name using a context.
// If the context indicates a background operation, it uses the background rate limiter.
// User requests are not rate limited.
func (l *wikiMediaProvider) FetchWithContext(ctx context.Context, scientificName string) (BirdImage, error) {
	// Check if this is a background operation
	isBackground := false
	if ctx != nil {
		if bg, ok := ctx.Value(backgroundOperationKey).(bool); ok && bg {
			isBackground = true
		}
	}

	// Only use rate limiter for background operations
	var limiter *rate.Limiter
	if isBackground {
		limiter = l.backgroundLimiter
	}
	// For user requests, limiter remains nil (no rate limiting)

	return l.fetchWithLimiter(scientificName, limiter)
}

// Fetch retrieves the bird image for a given scientific name.
// It queries for the thumbnail and author information, then constructs a BirdImage.
// User requests through this method are not rate limited.
func (l *wikiMediaProvider) Fetch(scientificName string) (BirdImage, error) {
	return l.fetchWithLimiter(scientificName, nil) // No rate limiting for user requests
}

// fetchWithLimiter retrieves the bird image using the specified rate limiter.
func (l *wikiMediaProvider) fetchWithLimiter(scientificName string, limiter *rate.Limiter) (BirdImage, error) {
	reqID := uuid.New().String()[:8] // Using first 8 chars for brevity
	logger := imageProviderLogger.With("provider", wikiProviderName, "scientific_name", scientificName, "request_id", reqID)
	logger.Info("Fetching image from Wikipedia")
	// if l.debug {
	// 	log.Printf("[%s] Debug: Starting Wikipedia fetch for species: %s", reqID, scientificName)
	// }

	thumbnailURL, thumbnailSourceFile, err := l.queryThumbnail(reqID, scientificName, limiter)
	if err != nil {
		// Error already logged in queryThumbnail
		// if l.debug {
		// 	log.Printf("[%s] Debug: Failed to fetch thumbnail for %s: %v", reqID, scientificName, err)
		// }
		return BirdImage{}, err // Pass through the user-friendly error from queryThumbnail
	}
	logger = logger.With("thumbnail_url", thumbnailURL, "source_file", thumbnailSourceFile)
	logger.Info("Thumbnail retrieved successfully")
	// if l.debug {
	// 	log.Printf("[%s] Debug: Successfully retrieved thumbnail - URL: %s, File: %s", reqID, thumbnailURL, thumbnailSourceFile)
	// 	log.Printf("[%s] Debug: Thumbnail source file: %s", reqID, thumbnailSourceFile)
	// }

	authorInfo, err := l.queryAuthorInfo(reqID, thumbnailSourceFile, limiter)
	if err != nil {
		// If it's just a "not found" error, continue with default author info
		// Only fail for actual errors (network issues, parsing failures)
		if errors.Is(err, ErrImageNotFound) {
			logger.Debug("Author info not available, using defaults")
			// Use default author info rather than failing
			authorInfo = &wikiMediaAuthor{
				name:        "Unknown",
				URL:         "",
				licenseName: "Unknown",
				licenseURL:  "",
			}
		} else {
			// This is a real error (network, API issues), so we should report it
			logger.Error("Failed to fetch author info", "error", err)
			enhancedErr := errors.Newf("unable to retrieve image attribution for species: %s", scientificName).
				Component("imageprovider").
				Category(errors.CategoryImageFetch).
				Context("provider", wikiProviderName).
				Context("request_id", reqID).
				Context("scientific_name", scientificName).
				Context("thumbnail_source_file", thumbnailSourceFile).
				Context("operation", "fetch_author_info").
				Build()
			return BirdImage{}, enhancedErr
		}
	}
	logger = logger.With("author", authorInfo.name, "license", authorInfo.licenseName)
	logger.Info("Author info retrieved successfully")
	// if l.debug {
	// 	log.Printf("[%s] Debug: Successfully retrieved author info for %s - Author: %s", reqID, scientificName, authorInfo.name)
	// }

	result := BirdImage{
		URL:         thumbnailURL,
		AuthorName:  authorInfo.name,
		AuthorURL:   authorInfo.URL,
		LicenseName: authorInfo.licenseName,
		LicenseURL:  authorInfo.licenseURL,
	}
	logger.Info("Successfully fetched image and metadata from Wikipedia")
	return result, nil
}

// queryThumbnail queries Wikipedia for the thumbnail image of the given scientific name.
// It returns the URL and file name of the thumbnail.
func (l *wikiMediaProvider) queryThumbnail(reqID, scientificName string, limiter *rate.Limiter) (url, fileName string, err error) {
	logger := imageProviderLogger.With("provider", wikiProviderName, "scientific_name", scientificName, "request_id", reqID)
	logger.Debug("Querying thumbnail")
	// if l.debug {
	// 	log.Printf("[%s] Debug: Querying thumbnail for species: %s", reqID, scientificName)
	// }

	params := map[string]string{
		"action":      "query",
		"prop":        "pageimages",
		"piprop":      "thumbnail|name",
		"pilicense":   "free",
		"titles":      scientificName,
		"pithumbsize": "400",
		"redirects":   "",
	}

	page, err := l.queryAndGetFirstPageWithLimiter(reqID, params, limiter)
	if err != nil {
		// Log based on error type
		if errors.Is(err, ErrImageNotFound) {
			logger.Warn("No Wikipedia page found for species")
		} else {
			logger.Error("Failed to query thumbnail page", "error", err)
		}
		// if l.debug {
		// 	log.Printf("Debug: Failed to query thumbnail page: %v", err)
		// }
		// Return a consistent user-facing error
		// Check if it's already an enhanced error from queryAndGetFirstPage
		var enhancedErr *errors.EnhancedError
		if !errors.As(err, &enhancedErr) {
			enhancedErr = errors.Newf("no Wikipedia page found for species: %s", scientificName).
				Component("imageprovider").
				Category(errors.CategoryImageFetch).
				Context("provider", wikiProviderName).
				Context("request_id", reqID).
				Context("scientific_name", scientificName).
				Context("operation", "query_thumbnail").
				Build()
		}
		return "", "", enhancedErr
	}

	url, err = page.GetString("thumbnail", "source")
	if err != nil {
		logger.Debug("No thumbnail URL found in page data", "error", err)
		// This is common for pages without images or with non-free images
		// Don't create telemetry noise - treat as "not found"
		return "", "", ErrImageNotFound
	}

	fileName, err = page.GetString("pageimage")
	if err != nil {
		logger.Debug("No pageimage filename found in page data", "error", err)
		// This is common for pages without proper image metadata
		// Don't create telemetry noise - treat as "not found"
		return "", "", ErrImageNotFound
	}

	logger.Debug("Successfully retrieved thumbnail URL and filename", "url", url, "filename", fileName)
	// if l.debug {
	// 	log.Printf("[%s] Debug: Successfully retrieved thumbnail - URL: %s, File: %s", reqID, url, fileName)
	// 	log.Printf("[%s] Debug: Successfully retrieved thumbnail URL: %s", reqID, url)
	// 	log.Printf("[%s] Debug: Thumbnail source file: %s", reqID, fileName)
	// }

	return url, fileName, nil
}

// queryAuthorInfo queries Wikipedia for the author information of the given thumbnail URL.
// It returns a wikiMediaAuthor struct containing the author and license information.
func (l *wikiMediaProvider) queryAuthorInfo(reqID, thumbnailFileName string, limiter *rate.Limiter) (*wikiMediaAuthor, error) {
	logger := imageProviderLogger.With("provider", wikiProviderName, "request_id", reqID, "filename", thumbnailFileName)
	logger.Debug("Querying author info for file")
	// if l.debug {
	// 	log.Printf("[%s] Debug: Querying author info for thumbnail: %s", reqID, thumbnailURL)
	// }

	params := map[string]string{
		"action":    "query",
		"prop":      "imageinfo",
		"iiprop":    "extmetadata",
		"titles":    "File:" + thumbnailFileName, // Use filename here
		"redirects": "",
	}

	page, err := l.queryAndGetFirstPageWithLimiter(reqID, params, limiter)
	if err != nil {
		// Log based on error type
		if errors.Is(err, ErrImageNotFound) {
			logger.Warn("No Wikipedia file page found for image filename")
		} else {
			logger.Error("Failed to query author info page", "error", err)
		}
		// if l.debug {
		// 	log.Printf("Debug: Failed to query author info page: %v", err)
		// }
		// Return internal error, fetch will wrap it
		// Check if it's already an enhanced error from queryAndGetFirstPage
		var enhancedErr *errors.EnhancedError
		if !errors.As(err, &enhancedErr) {
			enhancedErr = errors.New(err).
				Component("imageprovider").
				Category(errors.CategoryImageFetch).
				Context("provider", wikiProviderName).
				Context("request_id", reqID).
				Context("thumbnail_filename", thumbnailFileName).
				Context("operation", "query_author_info").
				Build()
		}
		return nil, enhancedErr
	}

	// Extract metadata
	logger.Debug("Extracting metadata from imageinfo response")
	imgInfo, err := page.GetObjectArray("imageinfo")
	if err != nil || len(imgInfo) == 0 {
		logger.Debug("No imageinfo found in file page", "error", err, "array_len", len(imgInfo))
		// This is common for files without metadata or processing issues
		// Don't create telemetry noise - treat as "not found"
		return nil, ErrImageNotFound
	}

	extMetadata, err := imgInfo[0].GetObject("extmetadata")
	if err != nil {
		logger.Debug("No extmetadata found in imageinfo", "error", err)
		// This is common for files without extended metadata
		// Don't create telemetry noise - treat as "not found"
		return nil, ErrImageNotFound
	}

	// Extract specific fields (Artist, LicenseShortName, LicenseUrl)
	artistHTML, _ := extMetadata.GetString("Artist", "value")
	licenseName, _ := extMetadata.GetString("LicenseShortName", "value")
	licenseURL, _ := extMetadata.GetString("LicenseUrl", "value")

	logger.Debug("Extracted raw metadata fields", "artist_html_len", len(artistHTML), "license_name", licenseName, "license_url", licenseURL)

	// Parse artist HTML to get name and URL
	authorName, authorURL := "", ""
	if artistHTML != "" {
		authorURL, authorName, err = extractArtistInfo(artistHTML)
		if err != nil {
			// Log error but continue, attribution might just be text
			logger.Warn("Failed to parse artist HTML, using plain text if available", "html", artistHTML, "error", err)
			// Fallback to plain text version if parsing failed
			authorName = html2text.HTML2Text(artistHTML)
		} else {
			logger.Debug("Parsed artist info from HTML", "name", authorName, "url", authorURL)
		}
	}

	// Handle cases where author might still be empty
	if authorName == "" {
		logger.Warn("Author name could not be extracted")
		authorName = "Unknown"
	}
	if licenseName == "" {
		logger.Warn("License name could not be extracted")
		licenseName = "Unknown"
	}

	logger.Debug("Final extracted author and license info", "author_name", authorName, "author_url", authorURL, "license_name", licenseName, "license_url", licenseURL)
	return &wikiMediaAuthor{
		name:        authorName,
		URL:         authorURL,
		licenseName: licenseName,
		licenseURL:  licenseURL,
	}, nil
}

// extractArtistInfo extracts the artist's name and URL from the HTML string.
func extractArtistInfo(htmlStr string) (href, text string, err error) {
	logger := imageProviderLogger.With("provider", wikiProviderName)
	logger.Debug("Attempting to extract artist info from HTML", "html_len", len(htmlStr))
	doc, err := html.Parse(strings.NewReader(htmlStr))
	if err != nil {
		logger.Error("Failed to parse artist HTML", "error", err)
		enhancedErr := errors.New(err).
			Component("imageprovider").
			Category(errors.CategoryImageFetch).
			Context("provider", wikiProviderName).
			Context("html_length", len(htmlStr)).
			Context("operation", "parse_artist_html").
			Build()
		return "", "", enhancedErr
	}

	userLinks := findWikipediaUserLinks(findLinks(doc))
	if len(userLinks) > 0 {
		// Prefer the first valid Wikipedia user link
		href = extractHref(userLinks[0])
		text = extractText(userLinks[0])
		logger.Debug("Found Wikipedia user link for artist", "href", href, "text", text)
		return href, text, nil
	}

	// Fallback: Find the first link if no specific user link is found
	allLinks := findLinks(doc)
	if len(allLinks) > 0 {
		href = extractHref(allLinks[0])
		text = extractText(allLinks[0])
		logger.Debug("No user link found, falling back to first available link", "href", href, "text", text)
		return href, text, nil
	}

	// Fallback: No links found, return plain text
	text = html2text.HTML2Text(htmlStr)
	logger.Debug("No links found in artist HTML, returning plain text", "text", text)
	return "", text, nil // No error if no link, just return text
}

// findWikipediaUserLinks traverses the list of nodes and returns only Wikipedia user links.
func findWikipediaUserLinks(nodes []*html.Node) []*html.Node {
	var wikiUserLinks []*html.Node

	for _, node := range nodes {
		for _, attr := range node.Attr {
			if attr.Key == "href" && isWikipediaUserLink(attr.Val) {
				wikiUserLinks = append(wikiUserLinks, node)
				break
			}
		}
	}

	return wikiUserLinks
}

// isWikipediaUserLink checks if the given href is a link to a Wikipedia user page.
func isWikipediaUserLink(href string) bool {
	return strings.Contains(href, "/wiki/User:")
}

// findLinks traverses the HTML document and returns all anchor (<a>) tags.
func findLinks(doc *html.Node) []*html.Node {
	var linkNodes []*html.Node

	var traverse func(*html.Node)
	traverse = func(node *html.Node) {
		if node.Type == html.ElementNode && node.Data == "a" {
			linkNodes = append(linkNodes, node)
		}
		for child := node.FirstChild; child != nil; child = child.NextSibling {
			traverse(child)
		}
	}

	traverse(doc)

	return linkNodes
}

// extractHref extracts the href attribute from an anchor tag.
func extractHref(link *html.Node) string {
	for _, attr := range link.Attr {
		if attr.Key == "href" {
			return attr.Val
		}
	}
	return ""
}

// extractText extracts the inner text from an anchor tag.
func extractText(link *html.Node) string {
	if link.FirstChild != nil {
		var b bytes.Buffer
		err := html.Render(&b, link.FirstChild)
		if err != nil {
			return ""
		}
		return html2text.HTML2Text(b.String())
	}
	return ""
}
