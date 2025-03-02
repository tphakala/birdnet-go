// wikipedia.go: Package imageprovider provides functionality for fetching and caching bird images.
package imageprovider

import (
	"bytes"
	"context"
	"fmt"
	"strings"
	"time"

	"cgt.name/pkg/go-mwclient"
	"github.com/antonholmquist/jason"
	"github.com/google/uuid"
	"github.com/k3a/html2text"
	"github.com/tphakala/birdnet-go/internal/conf"
	"github.com/tphakala/birdnet-go/internal/logger"
	"golang.org/x/net/html"
	"golang.org/x/time/rate"
)

// wikiMediaProvider implements the ImageProvider interface for Wikipedia.
type wikiMediaProvider struct {
	client     *mwclient.Client
	debug      bool
	limiter    *rate.Limiter
	maxRetries int
	logger     *logger.Logger
}

// wikiMediaAuthor represents the author information for a Wikipedia image.
type wikiMediaAuthor struct {
	name        string
	URL         string
	licenseName string
	licenseURL  string
}

// NewWikiMediaProvider creates a new WikiMedia image provider
func NewWikiMediaProvider(parentLogger *logger.Logger) (*wikiMediaProvider, error) {
	settings := conf.Setting()

	client, err := mwclient.New("https://commons.wikimedia.org/w/api.php", "BirdNET-Go/1.0")
	if err != nil {
		return nil, err
	}

	// Use the parent logger or fall back to global logger
	var componentLogger *logger.Logger
	if parentLogger != nil {
		componentLogger = parentLogger.Named("imageprovider.wikipedia")
	} else {
		// Fallback to global logger (will be removed after migration)
		componentLogger = logger.GetGlobal().Named("imageprovider.wikipedia")
	}

	// Rate limit: 10 requests per second with burst of 10
	return &wikiMediaProvider{
		client:     client,
		debug:      settings.Realtime.Dashboard.Thumbnails.Debug,
		limiter:    rate.NewLimiter(rate.Limit(10), 10),
		maxRetries: 3,
		logger:     componentLogger,
	}, nil
}

// queryWithRetry performs a query with retry logic.
// It waits for rate limiter, retries on error, and waits before retrying.
func (l *wikiMediaProvider) queryWithRetry(reqID string, params map[string]string) (*jason.Object, error) {
	var lastErr error
	for attempt := 0; attempt < l.maxRetries; attempt++ {
		if l.debug && l.logger != nil {
			l.logger.Debug("API request attempt",
				"request_id", reqID,
				"attempt", attempt+1)
		}

		// Wait for rate limiter
		err := l.limiter.Wait(context.Background())
		if err != nil {
			return nil, fmt.Errorf("rate limiter error: %w", err)
		}

		resp, err := l.client.Get(params)
		if err == nil {
			return resp, nil
		}

		lastErr = err
		if l.debug && l.logger != nil {
			l.logger.Debug("API request failed",
				"request_id", reqID,
				"attempt", attempt+1,
				"error", err)
		}

		// Wait before retry (exponential backoff)
		time.Sleep(time.Second * time.Duration(1<<attempt))
	}

	return nil, fmt.Errorf("all %d attempts failed, last error: %w", l.maxRetries, lastErr)
}

// queryAndGetFirstPage queries Wikipedia with given parameters and returns the first page hit.
// It handles the API request and response parsing.
func (l *wikiMediaProvider) queryAndGetFirstPage(reqID string, params map[string]string) (*jason.Object, error) {
	if l.debug && l.logger != nil {
		l.logger.Debug("Querying Wikipedia API",
			"request_id", reqID,
			"params", fmt.Sprintf("%v", params))
	}

	resp, err := l.queryWithRetry(reqID, params)
	if err != nil {
		if l.debug && l.logger != nil {
			l.logger.Debug("Wikipedia API query failed",
				"request_id", reqID,
				"error", err)
		}
		return nil, fmt.Errorf("failed to query Wikipedia: %w", err)
	}

	if l.debug && l.logger != nil {
		l.logger.Debug("Raw Wikipedia API response",
			"request_id", reqID,
			"response", fmt.Sprintf("%v", resp))
	}

	pages, err := resp.GetObjectArray("query", "pages")
	if err != nil {
		if l.debug && l.logger != nil {
			l.logger.Debug("Failed to parse Wikipedia response pages",
				"request_id", reqID,
				"error", err)

			if obj, err := resp.Object(); err == nil {
				l.logger.Debug("Response structure",
					"request_id", reqID,
					"structure", fmt.Sprintf("%v", obj))
			}
		}
		return nil, fmt.Errorf("no pages in response: %w", err)
	}

	if len(pages) > 0 {
		firstPage := pages[0]
		if l.debug && l.logger != nil {
			l.logger.Debug("First page content",
				"request_id", reqID,
				"content", fmt.Sprintf("%v", firstPage))
			l.logger.Debug("Successfully retrieved Wikipedia page",
				"request_id", reqID)
		}
		return firstPage, nil
	}

	if l.debug && l.logger != nil {
		l.logger.Debug("No pages found in Wikipedia response",
			"request_id", reqID,
			"params", fmt.Sprintf("%v", params))

		if obj, err := resp.Object(); err == nil {
			l.logger.Debug("Full response structure",
				"request_id", reqID,
				"structure", fmt.Sprintf("%v", obj))
		}
	}

	return nil, fmt.Errorf("no pages found in response")
}

// Fetch retrieves an image for the given species from Wikipedia.
func (l *wikiMediaProvider) Fetch(scientificName string) (BirdImage, error) {
	// Create a request ID for tracing
	reqID := uuid.New().String()[:8]

	if l.debug && l.logger != nil {
		l.logger.Debug("Starting Wikipedia fetch",
			"request_id", reqID,
			"species", scientificName)
	}

	// First get the thumbnail URL
	thumbnailURL, thumbnailSourceFile, err := l.queryThumbnail(reqID, scientificName)
	if err != nil {
		if l.debug && l.logger != nil {
			l.logger.Debug("Failed to fetch thumbnail",
				"request_id", reqID,
				"species", scientificName,
				"error", err)
		}
		return BirdImage{}, fmt.Errorf("failed to fetch image: %w", err)
	}

	if l.debug && l.logger != nil {
		l.logger.Debug("Successfully retrieved thumbnail",
			"request_id", reqID,
			"url", thumbnailURL,
			"file", thumbnailSourceFile)
		l.logger.Debug("Thumbnail source file",
			"request_id", reqID,
			"file", thumbnailSourceFile)
	}

	// Then get author info for attribution
	authorInfo, err := l.queryAuthorInfo(reqID, thumbnailSourceFile)
	if err != nil {
		if l.debug && l.logger != nil {
			l.logger.Debug("Failed to fetch author info",
				"request_id", reqID,
				"species", scientificName,
				"error", err)
		}
		// Return the image anyway, just without attribution
		return BirdImage{
			URL:            thumbnailURL,
			ScientificName: scientificName,
			CachedAt:       time.Now(),
		}, nil
	}

	if l.debug && l.logger != nil {
		l.logger.Debug("Successfully retrieved author info",
			"request_id", reqID,
			"species", scientificName,
			"author", authorInfo.name)
	}

	return BirdImage{
		URL:            thumbnailURL,
		ScientificName: scientificName,
		LicenseName:    authorInfo.licenseName,
		LicenseURL:     authorInfo.licenseURL,
		AuthorName:     authorInfo.name,
		AuthorURL:      authorInfo.URL,
		CachedAt:       time.Now(),
	}, nil
}

// queryThumbnail queries Wikipedia for a thumbnail image of the given species.
func (l *wikiMediaProvider) queryThumbnail(reqID, scientificName string) (url, fileName string, err error) {
	if l.debug && l.logger != nil {
		l.logger.Debug("Querying thumbnail",
			"request_id", reqID,
			"species", scientificName)
	}

	// Build query parameters to search for the species page
	params := map[string]string{
		"action":      "query",
		"prop":        "pageimages",
		"format":      "json",
		"piprop":      "original",
		"titles":      scientificName,
		"redirects":   "1",
		"pithumbsize": "500",
	}

	page, err := l.queryAndGetFirstPage(reqID, params)
	if err != nil {
		if l.debug && l.logger != nil {
			l.logger.Debug("Failed to query thumbnail page",
				"request_id", reqID,
				"error", err)
		}
		return "", "", fmt.Errorf("failed to find image for species: %s", scientificName)
	}

	// Extract thumbnail URL from page
	originalObj, err := page.GetObject("original")
	if err != nil {
		if l.debug && l.logger != nil {
			l.logger.Debug("Failed to extract thumbnail URL",
				"request_id", reqID,
				"error", err)
		}
		return "", "", fmt.Errorf("no image found for species: %s", scientificName)
	}

	thumbnailURL, err := originalObj.GetString("source")
	if err != nil {
		if l.debug && l.logger != nil {
			l.logger.Debug("Failed to extract thumbnail filename",
				"request_id", reqID,
				"error", err)
		}
		return "", "", fmt.Errorf("no image found for species: %s", scientificName)
	}

	// Extract filename from URL (we need it for attribution queries)
	parts := strings.Split(thumbnailURL, "/")
	fileName = parts[len(parts)-1]

	if l.debug && l.logger != nil {
		l.logger.Debug("Successfully retrieved thumbnail",
			"request_id", reqID,
			"url", url,
			"file", fileName)
		l.logger.Debug("Successfully retrieved thumbnail URL",
			"request_id", reqID,
			"url", thumbnailURL)
		l.logger.Debug("Thumbnail source file",
			"request_id", reqID,
			"file", fileName)
	}

	return thumbnailURL, fileName, nil
}

// queryAuthorInfo queries Wikipedia for author information for the given image.
func (l *wikiMediaProvider) queryAuthorInfo(reqID, thumbnailURL string) (*wikiMediaAuthor, error) {
	if l.debug && l.logger != nil {
		l.logger.Debug("Querying author info",
			"request_id", reqID,
			"thumbnail", thumbnailURL)
	}

	// Build query parameters for image info
	params := map[string]string{
		"action":              "query",
		"prop":                "imageinfo",
		"format":              "json",
		"iiprop":              "extmetadata",
		"titles":              "File:" + thumbnailURL,
		"iiextmetadatafilter": "Artist|LicenseUrl|LicenseShortName",
	}

	page, err := l.queryAndGetFirstPage(reqID, params)
	if err != nil {
		if l.debug && l.logger != nil {
			l.logger.Debug("Failed to query author info page",
				"request_id", reqID,
				"error", err)
		}
		return nil, fmt.Errorf("failed to retrieve author info: %w", err)
	}

	// Extract metadata
	imageInfo, err := page.GetObjectArray("imageinfo")
	if err != nil || len(imageInfo) == 0 {
		if l.debug && l.logger != nil {
			l.logger.Debug("Processing image info response",
				"request_id", reqID,
				"response", fmt.Sprintf("%v", page))
		}
	}

	if len(imageInfo) == 0 {
		if l.debug && l.logger != nil {
			l.logger.Debug("Failed to extract image info",
				"request_id", reqID,
				"error", err)
			l.logger.Debug("Page content",
				"request_id", reqID,
				"content", fmt.Sprintf("%v", page))
		}
		return nil, fmt.Errorf("no image info found")
	}

	// Handle case where no image info is found
	extmetadata, err := imageInfo[0].GetObject("extmetadata")
	if err != nil {
		if l.debug && l.logger != nil {
			l.logger.Debug("No image info found",
				"request_id", reqID,
				"thumbnail", thumbnailURL)
		}
		return nil, fmt.Errorf("no metadata found")
	}

	// Extract artist info from HTML
	var artistName, artistURL string
	artist, err := extmetadata.GetObject("Artist")
	if err == nil {
		artistHTML, err := artist.GetString("value")
		if err == nil {
			artistURL, artistName, err = extractArtistInfo(artistHTML)
			if err != nil {
				// Use the plain text as fallback
				artistName = html2text.HTML2Text(artistHTML)
			}
		}
	}

	// Extract license info
	var licenseName, licenseURL string
	license, err := extmetadata.GetObject("LicenseShortName")
	if err == nil {
		licenseName, err = license.GetString("value")
		if err != nil {
			licenseName = "Unknown License"
		}
	}

	licenseUrlObj, err := extmetadata.GetObject("LicenseUrl")
	if err == nil {
		licenseURL, err = licenseUrlObj.GetString("value")
		if err != nil {
			licenseURL = ""
		}
	}

	if l.debug && l.logger != nil {
		l.logger.Debug("Successfully extracted author info",
			"request_id", reqID,
			"author_name", artistName,
			"author_url", artistURL)
	}

	return &wikiMediaAuthor{
		name:        artistName,
		URL:         artistURL,
		licenseName: licenseName,
		licenseURL:  licenseURL,
	}, nil
}

// extractArtistInfo tries to extract the author information from the given HTML string.
// It parses the HTML and attempts to find the most relevant link and text.
func extractArtistInfo(htmlStr string) (href, text string, err error) {
	// First check if the string contains any HTML-like content
	if !strings.Contains(htmlStr, "<") {
		// If it's plain text, return it as the text with empty href
		return "", strings.TrimSpace(htmlStr), nil
	}

	doc, err := html.Parse(strings.NewReader(htmlStr))
	if err != nil {
		return "", "", err
	}

	links := findLinks(doc)

	if len(links) == 0 {
		return "", "", fmt.Errorf("failed to extract link from HTML: %s", htmlStr)
	}

	if len(links) == 1 {
		link := links[0]
		href = extractHref(link)
		text = extractText(link)
		return href, text, nil
	}

	wikipediaUserLinks := findWikipediaUserLinks(links)

	if len(wikipediaUserLinks) == 0 {
		return "", "", fmt.Errorf("failed to extract link from HTML: %s", htmlStr)
	}

	if len(wikipediaUserLinks) == 1 {
		wikipediaLink := wikipediaUserLinks[0]
		href = extractHref(wikipediaLink)
		text = extractText(wikipediaLink)
		return href, text, nil
	}

	firstHref := extractHref(wikipediaUserLinks[0])
	allSameHref := true
	for _, link := range wikipediaUserLinks[1:] {
		if extractHref(link) != firstHref {
			allSameHref = false
			break
		}
	}

	if allSameHref {
		wikipediaLink := wikipediaUserLinks[0]
		href = extractHref(wikipediaLink)
		text = extractText(wikipediaLink)
		return href, text, nil
	}

	return "", "", fmt.Errorf("multiple Wikipedia user links found in HTML: %s", htmlStr)
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
