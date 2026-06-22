package main

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
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
)

func newGitHubClient(baseURL, token string) *githubClient {
	return &githubClient{
		httpClient: &http.Client{Timeout: 30 * time.Second},
		baseURL:    baseURL,
		token:      token,
	}
}

func (c *githubClient) newRequest(ctx context.Context, url string) (*http.Request, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, http.NoBody)
	if err != nil {
		return nil, fmt.Errorf("build request: %w", err)
	}
	req.Header.Set("Accept", apiAcceptHeader)
	req.Header.Set("X-GitHub-Api-Version", apiVersion)
	if c.token != "" {
		req.Header.Set("Authorization", "Bearer "+c.token)
	}
	return req, nil
}

func (c *githubClient) ListReleases(ctx context.Context, repo string) ([]ghRelease, error) {
	url := fmt.Sprintf("%s/repos/%s/releases?per_page=100", c.baseURL, repo)
	req, err := c.newRequest(ctx, url)
	if err != nil {
		return nil, err
	}
	resp, err := c.httpClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("list releases: %w", err)
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(io.LimitReader(resp.Body, maxAssetBytes))
		return nil, fmt.Errorf("list releases: unexpected status %d: %s", resp.StatusCode, string(body))
	}
	var releases []ghRelease
	if err := json.NewDecoder(resp.Body).Decode(&releases); err != nil {
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
	data, err := io.ReadAll(io.LimitReader(resp.Body, maxAssetBytes))
	if err != nil {
		return nil, fmt.Errorf("read %s: %w", url, err)
	}
	return data, nil
}
