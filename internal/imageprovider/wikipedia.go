// wikipedia.go: Package imageprovider provides functionality for fetching and caching bird images.
package imageprovider

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"log/slog"
	"strings"
	"time"

	"cgt.name/pkg/go-mwclient"
	"github.com/antonholmquist/jason"
	"github.com/google/uuid"
	"github.com/k3a/html2text"
	"github.com/tphakala/birdnet-go/internal/conf"
	"golang.org/x/net/html"
	"golang.org/x/time/rate"
)

const wikiProviderName = "wikipedia"

// wikiMediaProvider implements the ImageProvider interface for Wikipedia.
type wikiMediaProvider struct {
	client     *mwclient.Client
	debug      bool
	limiter    *rate.Limiter
	maxRetries int
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
		logger.Error("Failed to create mwclient for Wikipedia API", "error", err)
		return nil, fmt.Errorf("failed to create mwclient: %w", err)
	}

	// Rate limit: 10 requests per second with burst of 10
	limiter := rate.NewLimiter(rate.Limit(10), 10)
	logger.Info("WikiMedia provider initialized", "rate_limit_rps", 10, "rate_limit_burst", 10)
	return &wikiMediaProvider{
		client:     client,
		debug:      settings.Realtime.Dashboard.Thumbnails.Debug,
		limiter:    limiter,
		maxRetries: 3,
	}, nil
}

// queryWithRetry performs a query with retry logic.
// It waits for rate limiter, retries on error, and waits before retrying.
func (l *wikiMediaProvider) queryWithRetry(reqID string, params map[string]string) (*jason.Object, error) {
	logger := imageProviderLogger.With("provider", wikiProviderName, "request_id", reqID, "api_action", params["action"])
	var lastErr error
	for attempt := 0; attempt < l.maxRetries; attempt++ {
		attemptLogger := logger.With("attempt", attempt+1, "max_attempts", l.maxRetries)
		attemptLogger.Debug("Attempting Wikipedia API request")
		// if l.debug {
		// 	log.Printf("[%s] Debug: API request attempt %d", reqID, attempt+1)
		// }
		// Wait for rate limiter
		attemptLogger.Debug("Waiting for rate limiter")
		err := l.limiter.Wait(context.Background()) // Using Background context for limiter wait
		if err != nil {
			attemptLogger.Error("Rate limiter error", "error", err)
			// Don't retry on limiter error, return immediately
			return nil, fmt.Errorf("rate limiter error: %w", err)
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
	return nil, fmt.Errorf("all %d attempts failed, last error: %w", l.maxRetries, lastErr)
}

// queryAndGetFirstPage queries Wikipedia with given parameters and returns the first page hit.
// It handles the API request and response parsing.
func (l *wikiMediaProvider) queryAndGetFirstPage(reqID string, params map[string]string) (*jason.Object, error) {
	logger := imageProviderLogger.With("provider", wikiProviderName, "request_id", reqID, "api_action", params["action"], "titles", params["titles"])
	logger.Info("Querying Wikipedia API")
	// if l.debug {
	// 	log.Printf("[%s] Debug: Querying Wikipedia API with params: %v", reqID, params)
	// }

	resp, err := l.queryWithRetry(reqID, params)
	if err != nil {
		// Error already logged in queryWithRetry
		// if l.debug {
		// 	log.Printf("Debug: Wikipedia API query failed after retries: %v", err)
		// }
		return nil, fmt.Errorf("failed to query Wikipedia: %w", err)
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
	pages, err := resp.GetObjectArray("query", "pages")
	if err != nil {
		logger.Error("Failed to parse pages from Wikipedia response", "error", err)
		// if l.debug {
		// 	log.Printf("[%s] Debug: Failed to parse Wikipedia response pages: %v", reqID, err)
		// 	if obj, err := resp.Object(); err == nil {
		// 		log.Printf("[%s] Debug: Response structure: %v", reqID, obj)
		// 	}
		// }
		return nil, fmt.Errorf("failed to get pages from response: %w", err)
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

// fetch retrieves the bird image for a given scientific name.
// It queries for the thumbnail and author information, then constructs a BirdImage.
func (l *wikiMediaProvider) Fetch(scientificName string) (BirdImage, error) {
	reqID := uuid.New().String()[:8] // Using first 8 chars for brevity
	logger := imageProviderLogger.With("provider", wikiProviderName, "scientific_name", scientificName, "request_id", reqID)
	logger.Info("Fetching image from Wikipedia")
	// if l.debug {
	// 	log.Printf("[%s] Debug: Starting Wikipedia fetch for species: %s", reqID, scientificName)
	// }

	thumbnailURL, thumbnailSourceFile, err := l.queryThumbnail(reqID, scientificName)
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

	authorInfo, err := l.queryAuthorInfo(reqID, thumbnailSourceFile)
	if err != nil {
		// Error logged in queryAuthorInfo
		// if l.debug {
		// 	log.Printf("[%s] Debug: Failed to fetch author info for %s: %v", reqID, scientificName, err)
		// }
		// Don't expose internal error to user, use a generic message
		// Keep existing error message which is reasonable
		return BirdImage{}, fmt.Errorf("unable to retrieve image attribution for species: %s", scientificName)
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
func (l *wikiMediaProvider) queryThumbnail(reqID, scientificName string) (url, fileName string, err error) {
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

	page, err := l.queryAndGetFirstPage(reqID, params)
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
		return "", "", fmt.Errorf("no Wikipedia page found for species: %s", scientificName)
	}

	url, err = page.GetString("thumbnail", "source")
	if err != nil {
		logger.Warn("Failed to extract thumbnail URL from page data", "error", err)
		// if l.debug {
		// 	log.Printf("Debug: Failed to extract thumbnail URL: %v", err)
		// }
		// Return a consistent user-facing error
		return "", "", fmt.Errorf("no free-license image available for species: %s", scientificName)
	}

	fileName, err = page.GetString("pageimage")
	if err != nil {
		logger.Warn("Failed to extract thumbnail filename (pageimage) from page data", "error", err)
		// if l.debug {
		// 	log.Printf("Debug: Failed to extract thumbnail filename: %v", err)
		// }
		// Return a consistent user-facing error
		return "", "", fmt.Errorf("image metadata not available for species: %s", scientificName)
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
func (l *wikiMediaProvider) queryAuthorInfo(reqID, thumbnailFileName string) (*wikiMediaAuthor, error) {
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

	page, err := l.queryAndGetFirstPage(reqID, params)
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
		return nil, fmt.Errorf("failed to query image info for %s: %w", thumbnailFileName, err)
	}

	// Extract metadata
	logger.Debug("Extracting metadata from imageinfo response")
	imgInfo, err := page.GetObjectArray("imageinfo")
	if err != nil || len(imgInfo) == 0 {
		logger.Error("Failed to get imageinfo array from page data", "error", err, "array_len", len(imgInfo))
		return nil, fmt.Errorf("no imageinfo found for %s", thumbnailFileName)
	}

	extMetadata, err := imgInfo[0].GetObject("extmetadata")
	if err != nil {
		logger.Error("Failed to get extmetadata object from imageinfo", "error", err)
		return nil, fmt.Errorf("no extmetadata found for %s", thumbnailFileName)
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
		return "", "", fmt.Errorf("failed to parse artist HTML: %w", err)
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
