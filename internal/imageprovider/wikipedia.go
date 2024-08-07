// wikipedia.go: Package imageprovider provides functionality for fetching and caching bird images.
package imageprovider

import (
	"bytes"
	"fmt"
	"strings"

	"cgt.name/pkg/go-mwclient"
	"github.com/antonholmquist/jason"
	"github.com/k3a/html2text"
	"golang.org/x/net/html"
)

// wikiMediaProvider implements the ImageProvider interface for Wikipedia.
type wikiMediaProvider struct {
	client *mwclient.Client
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
	client, err := mwclient.New("https://wikipedia.org/w/api.php", "BirdNET-Go")
	if err != nil {
		return nil, fmt.Errorf("failed to create mwclient: %w", err)
	}
	return &wikiMediaProvider{
		client: client,
	}, nil
}

// queryAndGetFirstPage queries Wikipedia with given parameters and returns the first page hit.
// It handles the API request and response parsing.
func (l *wikiMediaProvider) queryAndGetFirstPage(params map[string]string) (*jason.Object, error) {
	resp, err := l.client.Get(params)
	if err != nil {
		return nil, fmt.Errorf("failed to query Wikipedia: %w", err)
	}

	pages, err := resp.GetObjectArray("query", "pages")
	if err != nil {
		return nil, fmt.Errorf("failed to get pages from response: %w", err)
	}

	if len(pages) == 0 {
		return nil, fmt.Errorf("no pages found for request: %v", params)
	}

	return pages[0], nil
}

// fetch retrieves the bird image for a given scientific name.
// It queries for the thumbnail and author information, then constructs a BirdImage.
func (l *wikiMediaProvider) Fetch(scientificName string) (BirdImage, error) {
	thumbnailURL, thumbnailSourceFile, err := l.queryThumbnail(scientificName)
	if err != nil {
		return BirdImage{}, fmt.Errorf("failed to query thumbnail of bird: %s : %w", scientificName, err)
	}

	authorInfo, err := l.queryAuthorInfo(thumbnailSourceFile)
	if err != nil {
		return BirdImage{}, fmt.Errorf("failed to query thumbnail credit of bird: %s : %w", scientificName, err)
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
func (l *wikiMediaProvider) queryThumbnail(scientificName string) (url, fileName string, err error) {
	params := map[string]string{
		"action":      "query",
		"prop":        "pageimages",
		"piprop":      "thumbnail|name",
		"pilicense":   "free",
		"titles":      scientificName,
		"pithumbsize": "400",
		"redirects":   "",
	}

	page, err := l.queryAndGetFirstPage(params)
	if err != nil {
		return "", "", fmt.Errorf("failed to query thumbnail: %w", err)
	}

	url, err = page.GetString("thumbnail", "source")
	if err != nil {
		return "", "", fmt.Errorf("failed to get thumbnail URL: %w", err)
	}

	fileName, err = page.GetString("pageimage")
	if err != nil {
		return "", "", fmt.Errorf("failed to get thumbnail file name: %w", err)
	}

	return url, fileName, nil
}

// queryAuthorInfo queries Wikipedia for the author information of the given thumbnail URL.
// It returns a wikiMediaAuthor struct containing the author and license information.
func (l *wikiMediaProvider) queryAuthorInfo(thumbnailURL string) (*wikiMediaAuthor, error) {
	params := map[string]string{
		"action":    "query",
		"prop":      "imageinfo",
		"iiprop":    "extmetadata",
		"titles":    "File:" + thumbnailURL,
		"redirects": "",
	}

	page, err := l.queryAndGetFirstPage(params)
	if err != nil {
		return nil, fmt.Errorf("failed to query thumbnail: %w", err)
	}

	imageInfo, err := page.GetObjectArray("imageinfo")
	if err != nil {
		return nil, fmt.Errorf("failed to get image info from response: %w", err)
	}
	if len(imageInfo) == 0 {
		return nil, fmt.Errorf("no image info found for thumbnail URL: %s", thumbnailURL)
	}

	extMetadata, err := imageInfo[0].GetObject("extmetadata")
	if err != nil {
		return nil, fmt.Errorf("failed to get extmetadata from response: %w", err)
	}

	licenseName, err := extMetadata.GetString("LicenseShortName", "value")
	if err != nil {
		return nil, fmt.Errorf("failed to get license name from extmetadata: %w", err)
	}

	licenseURL, err := extMetadata.GetString("LicenseUrl", "value")
	if err != nil {
		return nil, fmt.Errorf("failed to get license URL from extmetadata: %w", err)
	}

	artistHref, err := extMetadata.GetString("Artist", "value")
	if err != nil {
		return nil, fmt.Errorf("failed to get artist from extmetadata: %w", err)
	}

	href, text, err := extractArtistInfo(artistHref)
	if err != nil {
		return nil, fmt.Errorf("failed to extract link information: %w", err)
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
	doc, err := html.Parse(strings.NewReader(htmlStr))
	if err != nil {
		return "", "", err
	}

	links := findLinks(doc)

	if len(links) == 0 {
		return "", html2text.HTML2Text(htmlStr), nil
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
