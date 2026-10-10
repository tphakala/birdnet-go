package main

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"
)

// ghRelease is the subset of the GitHub Releases API response we consume.
type ghRelease struct {
	TagName     string    `json:"tag_name"`
	Name        string    `json:"name"`
	Body        string    `json:"body"`
	Draft       bool      `json:"draft"`
	Prerelease  bool      `json:"prerelease"`
	PublishedAt time.Time `json:"published_at"`
	HTMLURL     string    `json:"html_url"`
	Assets      []ghAsset `json:"assets"`
}

// ghAsset is a single release asset.
type ghAsset struct {
	Name               string `json:"name"`
	Size               int64  `json:"size"`
	BrowserDownloadURL string `json:"browser_download_url"`
}

// releaseSource abstracts the GitHub API so the assembly logic can be tested
// against a fake.
type releaseSource interface {
	// ListReleases returns the most recent releases for a repo, newest first.
	ListReleases(ctx context.Context, repo string) ([]ghRelease, error)
	// Download fetches the raw bytes of an asset URL.
	Download(ctx context.Context, url string) ([]byte, error)
}

// githubClient talks to the GitHub REST API over HTTP.
type githubClient struct {
	httpClient *http.Client
	baseURL    string // e.g. https://api.github.com
	token      string // optional; raises the rate limit and reads private repos
}

const (
	apiAcceptHeader = "application/vnd.github+json"
	apiVersion      = "2022-11-28"
	maxAssetBytes   = 1 << 20 // 1 MiB cap for checksum file downloads
	maxReleasesBody = 8 << 20 // 8 MiB cap for each releases listing page
	// releasesPerPage is the page size requested from the releases API, its
	// documented maximum.
	releasesPerPage = 100
	// maxReleasePages bounds the listing so a misbehaving API cannot loop
	// forever. A last page that is still full fails the listing, so up to
	// maxReleasePages*releasesPerPage-1 releases are read.
	maxReleasePages = 50
	// userAgent is sent on every request; the GitHub REST API requires a
	// User-Agent header and returns 403 without one.
	userAgent = "birdnet-go-release-manifest"
)

func newGitHubClient(baseURL, token string) *githubClient {
	return &githubClient{
		httpClient: &http.Client{Timeout: 30 * time.Second},
		// Trim a trailing slash so a base URL like "https://api.github.com/"
		// does not produce a double slash when paths are appended.
		baseURL: strings.TrimRight(baseURL, "/"),
		token:   token,
	}
}

func (c *githubClient) newRequest(ctx context.Context, url string) (*http.Request, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, http.NoBody)
	if err != nil {
		return nil, fmt.Errorf("build request: %w", err)
	}
	req.Header.Set("Accept", apiAcceptHeader)
	req.Header.Set("X-GitHub-Api-Version", apiVersion)
	req.Header.Set("User-Agent", userAgent)
	if c.token != "" {
		req.Header.Set("Authorization", "Bearer "+c.token)
	}
	return req, nil
}

// ListReleases fetches every release, following pages until one comes back
// short. The generator checks every published release, so the listing must be
// complete; it fails rather than returning a partial list.
func (c *githubClient) ListReleases(ctx context.Context, repo string) ([]ghRelease, error) {
	var releases []ghRelease
	for page := 1; page <= maxReleasePages; page++ {
		batch, err := c.listReleasesPage(ctx, repo, page)
		if err != nil {
			return nil, fmt.Errorf("page %d: %w", page, err)
		}
		releases = append(releases, batch...)
		if len(batch) < releasesPerPage {
			return releases, nil
		}
	}
	return nil, fmt.Errorf("more than %d pages", maxReleasePages)
}

// listReleasesPage fetches one page of the releases listing.
func (c *githubClient) listReleasesPage(ctx context.Context, repo string, page int) ([]ghRelease, error) {
	url := fmt.Sprintf("%s/repos/%s/releases?per_page=%d&page=%d", c.baseURL, repo, releasesPerPage, page)
	req, err := c.newRequest(ctx, url)
	if err != nil {
		return nil, err
	}
	resp, err := c.httpClient.Do(req)
	if err != nil {
		return nil, err
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(io.LimitReader(resp.Body, maxAssetBytes))
		return nil, fmt.Errorf("unexpected status %d: %s", resp.StatusCode, string(body))
	}
	var releases []ghRelease
	if err := json.NewDecoder(io.LimitReader(resp.Body, maxReleasesBody)).Decode(&releases); err != nil {
		return nil, fmt.Errorf("decode releases: %w", err)
	}
	return releases, nil
}

func (c *githubClient) Download(ctx context.Context, url string) ([]byte, error) {
	req, err := c.newRequest(ctx, url)
	if err != nil {
		return nil, err
	}
	// Asset download URLs serve the raw bytes; octet-stream is the documented Accept.
	req.Header.Set("Accept", "application/octet-stream")
	resp, err := c.httpClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("download %s: %w", url, err)
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("download %s: unexpected status %d", url, resp.StatusCode)
	}
	// Read one byte past the cap so an oversized asset is detected and errors
	// rather than silently truncating into a partial checksum map.
	data, err := io.ReadAll(io.LimitReader(resp.Body, maxAssetBytes+1))
	if err != nil {
		return nil, fmt.Errorf("read %s: %w", url, err)
	}
	if len(data) > maxAssetBytes {
		return nil, fmt.Errorf("download %s: asset exceeds %d byte limit", url, maxAssetBytes)
	}
	return data, nil
}
