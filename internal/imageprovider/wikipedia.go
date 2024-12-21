// wikipedia.go: Package imageprovider provides functionality for fetching and caching bird images.
package imageprovider

import (
	"bytes"
	"context"
	"fmt"
	"log"
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
	settings := conf.Setting()
	client, err := mwclient.New("https://wikipedia.org/w/api.php", "BirdNET-Go")
	if err != nil {
		return nil, fmt.Errorf("failed to create mwclient: %w", err)
	}

	// Rate limit: 10 requests per second with burst of 10
	return &wikiMediaProvider{
		client:     client,
		debug:      settings.Realtime.Dashboard.Thumbnails.Debug,
		limiter:    rate.NewLimiter(rate.Limit(10), 10),
		maxRetries: 3,
	}, nil
}

// queryWithRetry performs a query with retry logic.
// It waits for rate limiter, retries on error, and waits before retrying.
func (l *wikiMediaProvider) queryWithRetry(reqID string, params map[string]string) (*jason.Object, error) {
	var lastErr error
	for attempt := 0; attempt < l.maxRetries; attempt++ {
		if l.debug {
			log.Printf("[%s] Debug: API request attempt %d", reqID, attempt+1)
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
		if l.debug {
			log.Printf("Debug: API request attempt %d failed: %v", attempt+1, err)
		}

		// Wait before retry (exponential backoff)
		time.Sleep(time.Second * time.Duration(1<<attempt))
	}

	return nil, fmt.Errorf("all %d attempts failed, last error: %w", l.maxRetries, lastErr)
}

// queryAndGetFirstPage queries Wikipedia with given parameters and returns the first page hit.
// It handles the API request and response parsing.
func (l *wikiMediaProvider) queryAndGetFirstPage(reqID string, params map[string]string) (*jason.Object, error) {
	if l.debug {
		log.Printf("[%s] Debug: Querying Wikipedia API with params: %v", reqID, params)
	}

	resp, err := l.queryWithRetry(reqID, params)
	if err != nil {
		if l.debug {
			log.Printf("Debug: Wikipedia API query failed after retries: %v", err)
		}
		return nil, fmt.Errorf("failed to query Wikipedia: %w", err)
	}

	if l.debug {
		if obj, err := resp.Object(); err == nil {
			log.Printf("[%s] Debug: Raw Wikipedia API response: %v", reqID, obj)
		}
	}

	pages, err := resp.GetObjectArray("query", "pages")
	if err != nil {
		if l.debug {
			log.Printf("[%s] Debug: Failed to parse Wikipedia response pages: %v", reqID, err)
			if obj, err := resp.Object(); err == nil {
				log.Printf("[%s] Debug: Response structure: %v", reqID, obj)
			}
		}
		return nil, fmt.Errorf("failed to get pages from response: %w", err)
	}

	if l.debug {
		if firstPage, err := pages[0].Object(); err == nil {
			log.Printf("[%s] Debug: First page content: %v", reqID, firstPage)
			log.Printf("[%s] Debug: Successfully retrieved Wikipedia page", reqID)
		}
	}

	if len(pages) == 0 {
		if l.debug {
			log.Printf("Debug: No pages found in Wikipedia response for params: %v", params)
			if obj, err := resp.Object(); err == nil {
				log.Printf("Debug: Full response structure: %v", obj)
			}
		}
		return nil, fmt.Errorf("no pages found for request: %v", params)
	}

	return pages[0], nil
}

// fetch retrieves the bird image for a given scientific name.
// It queries for the thumbnail and author information, then constructs a BirdImage.
func (l *wikiMediaProvider) Fetch(scientificName string) (BirdImage, error) {
	reqID := uuid.New().String()[:8] // Using first 8 chars for brevity
	if l.debug {
		log.Printf("[%s] Debug: Starting Wikipedia fetch for species: %s", reqID, scientificName)
	}

	thumbnailURL, thumbnailSourceFile, err := l.queryThumbnail(reqID, scientificName)
	if err != nil {
		if l.debug {
			log.Printf("[%s] Debug: Failed to fetch thumbnail for %s: %v", reqID, scientificName, err)
		}
		return BirdImage{}, fmt.Errorf("failed to query thumbnail of bird: %s : %w", scientificName, err)
	}

	if l.debug {
		log.Printf("[%s] Debug: Successfully retrieved thumbnail - URL: %s, File: %s", reqID, thumbnailURL, thumbnailSourceFile)
		log.Printf("[%s] Debug: Thumbnail source file: %s", reqID, thumbnailSourceFile)
	}

	authorInfo, err := l.queryAuthorInfo(reqID, thumbnailSourceFile)
	if err != nil {
		if l.debug {
			log.Printf("[%s] Debug: Failed to fetch author info for %s: %v", reqID, scientificName, err)
		}
		return BirdImage{}, fmt.Errorf("failed to query thumbnail credit of bird: %s : %w", scientificName, err)
	}

	if l.debug {
		log.Printf("[%s] Debug: Successfully retrieved author info for %s - Author: %s", reqID, scientificName, authorInfo.name)
	}

	return BirdImage{
		URL:         thumbnailURL,
		AuthorName:  authorInfo.name,
		AuthorURL:   authorInfo.URL,
		LicenseName: authorInfo.licenseName,
		LicenseURL:  authorInfo.licenseURL,
	}, nil
}

// queryThumbnail queries Wikipedia for the thumbnail image of the given scientific name.
// It returns the URL and file name of the thumbnail.
func (l *wikiMediaProvider) queryThumbnail(reqID, scientificName string) (url, fileName string, err error) {
	if l.debug {
		log.Printf("[%s] Debug: Querying thumbnail for species: %s", reqID, scientificName)
	}

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
		if l.debug {
			log.Printf("Debug: Failed to query thumbnail page: %v", err)
		}
		return "", "", fmt.Errorf("failed to query thumbnail: %w", err)
	}

	url, err = page.GetString("thumbnail", "source")
	if err != nil {
		if l.debug {
			log.Printf("Debug: Failed to extract thumbnail URL: %v", err)
		}
		return "", "", fmt.Errorf("failed to get thumbnail URL: %w", err)
	}

	fileName, err = page.GetString("pageimage")
	if err != nil {
		if l.debug {
			log.Printf("Debug: Failed to extract thumbnail filename: %v", err)
		}
		return "", "", fmt.Errorf("failed to get thumbnail file name: %w", err)
	}

	if l.debug {
		log.Printf("[%s] Debug: Successfully retrieved thumbnail - URL: %s, File: %s", reqID, url, fileName)
		log.Printf("[%s] Debug: Successfully retrieved thumbnail URL: %s", reqID, url)
		log.Printf("[%s] Debug: Thumbnail source file: %s", reqID, fileName)
	}

	return url, fileName, nil
}

// queryAuthorInfo queries Wikipedia for the author information of the given thumbnail URL.
// It returns a wikiMediaAuthor struct containing the author and license information.
func (l *wikiMediaProvider) queryAuthorInfo(reqID, thumbnailURL string) (*wikiMediaAuthor, error) {
	if l.debug {
		log.Printf("[%s] Debug: Querying author info for thumbnail: %s", reqID, thumbnailURL)
	}

	params := map[string]string{
		"action":    "query",
		"prop":      "imageinfo",
		"iiprop":    "extmetadata",
		"titles":    "File:" + thumbnailURL,
		"redirects": "",
	}

	page, err := l.queryAndGetFirstPage(reqID, params)
	if err != nil {
		if l.debug {
			log.Printf("Debug: Failed to query author info page: %v", err)
		}
		return nil, fmt.Errorf("failed to query thumbnail: %w", err)
	}

	if l.debug {
		if obj, err := page.Object(); err == nil {
			log.Printf("Debug: Processing image info response: %v", obj)
		}
	}

	imageInfo, err := page.GetObjectArray("imageinfo")
	if err != nil {
		if l.debug {
			log.Printf("Debug: Failed to extract image info: %v", err)
			if obj, err := page.Object(); err == nil {
				log.Printf("Debug: Page content: %v", obj)
			}
		}
		return nil, fmt.Errorf("failed to get image info from response: %w", err)
	}
	if len(imageInfo) == 0 {
		if l.debug {
			log.Printf("Debug: No image info found for thumbnail: %s", thumbnailURL)
		}
		return nil, fmt.Errorf("no image info found for thumbnail URL: %s", thumbnailURL)
	}

	extMetadata, err := imageInfo[0].GetObject("extmetadata")
	if err != nil {
		return nil, fmt.Errorf("failed to get extmetadata from response: %w", err)
	}

	licenseName, err := extMetadata.GetString("LicenseShortName", "value")
	if err != nil {
		if l.debug {
			log.Printf("[%s] Debug: License name not found, using 'Unknown'", reqID)
		}
		licenseName = "Unknown"
	}

	licenseURL, err := extMetadata.GetString("LicenseUrl", "value")
	if err != nil {
		if l.debug {
			log.Printf("[%s] Debug: License URL not found, using empty string", reqID)
		}
		licenseURL = ""
	}

	artistHref, err := extMetadata.GetString("Artist", "value")
	if err != nil {
		return nil, fmt.Errorf("failed to get artist from extmetadata: %w", err)
	}

	href, text, err := extractArtistInfo(artistHref)
	if err != nil {
		if l.debug {
			log.Printf("Debug: Failed to extract artist info from HTML: %v", err)
		}
		return nil, fmt.Errorf("failed to extract link information: %w", err)
	}

	if l.debug {
		log.Printf("[%s] Debug: Successfully extracted author info - Name: %s, URL: %s", reqID, text, href)
	}

	return &wikiMediaAuthor{
		name:        text,
		URL:         href,
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
