package main

import (
	"bytes"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"sync"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestNewGitHubClient_TrimsTrailingSlash(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name    string
		baseURL string
		want    string
	}{
		{name: "no slash", baseURL: "https://api.github.com", want: "https://api.github.com"},
		{name: "one trailing slash", baseURL: "https://api.github.com/", want: "https://api.github.com"},
		{name: "multiple trailing slashes", baseURL: "https://api.github.com///", want: "https://api.github.com"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			assert.Equal(t, tt.want, newGitHubClient(tt.baseURL, "").baseURL)
		})
	}
}

func TestGitHubClient_DownloadOK(t *testing.T) {
	t.Parallel()
	const body = "abc123  birdnet-go-linux-amd64.tar.gz\n"
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte(body))
	}))
	defer srv.Close()

	data, err := newGitHubClient(srv.URL, "").Download(t.Context(), srv.URL)
	require.NoError(t, err)
	assert.Equal(t, body, string(data))
}

func TestGitHubClient_DownloadRejectsOversized(t *testing.T) {
	t.Parallel()
	big := bytes.Repeat([]byte("a"), maxAssetBytes+10)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write(big)
	}))
	defer srv.Close()

	_, err := newGitHubClient(srv.URL, "").Download(t.Context(), srv.URL)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "exceeds")
}

// releasesServer serves total releases in pages of releasesPerPage, as the
// GitHub API does, and records the page numbers requested.
func releasesServer(t *testing.T, total int, mu *sync.Mutex, pages *[]string) *httptest.Server {
	t.Helper()
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		page := r.URL.Query().Get("page")
		mu.Lock()
		*pages = append(*pages, page)
		mu.Unlock()
		var n int
		_, err := fmt.Sscan(page, &n)
		if err != nil || r.URL.Query().Get("per_page") != fmt.Sprint(releasesPerPage) {
			http.Error(w, "bad paging", http.StatusBadRequest)
			return
		}
		out := []ghRelease{}
		for i := (n - 1) * releasesPerPage; i < total && i < n*releasesPerPage; i++ {
			out = append(out, ghRelease{TagName: fmt.Sprintf("r%d", i)})
		}
		_ = json.NewEncoder(w).Encode(out)
	}))
}

func TestGitHubClient_ListReleasesFollowsPages(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name      string
		total     int
		wantPages []string
	}{
		{name: "no releases", total: 0, wantPages: []string{"1"}},
		{name: "single partial page", total: 34, wantPages: []string{"1"}},
		{name: "full page then partial", total: releasesPerPage + 3, wantPages: []string{"1", "2"}},
		{name: "exactly one full page", total: releasesPerPage, wantPages: []string{"1", "2"}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			var (
				mu    sync.Mutex
				pages []string
			)
			srv := releasesServer(t, tt.total, &mu, &pages)
			defer srv.Close()

			releases, err := newGitHubClient(srv.URL, "").ListReleases(t.Context(), "o/r")
			require.NoError(t, err)
			require.Len(t, releases, tt.total)
			if tt.total > 0 {
				assert.Equal(t, fmt.Sprintf("r%d", tt.total-1), releases[tt.total-1].TagName, "last release from the last page")
			}
			mu.Lock()
			defer mu.Unlock()
			assert.Equal(t, tt.wantPages, pages)
		})
	}
}

func TestGitHubClient_ListReleasesFailsOnLaterPageError(t *testing.T) {
	t.Parallel()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Query().Get("page") != "1" {
			http.Error(w, "boom", http.StatusBadGateway)
			return
		}
		full := make([]ghRelease, releasesPerPage)
		_ = json.NewEncoder(w).Encode(full)
	}))
	defer srv.Close()

	_, err := newGitHubClient(srv.URL, "").ListReleases(t.Context(), "o/r")
	require.Error(t, err)
	assert.Contains(t, err.Error(), "page 2")
}

func TestGitHubClient_ListReleasesFailsPastPageCap(t *testing.T) {
	t.Parallel()
	var (
		mu    sync.Mutex
		pages []string
	)
	srv := releasesServer(t, maxReleasePages*releasesPerPage, &mu, &pages)
	defer srv.Close()

	_, err := newGitHubClient(srv.URL, "").ListReleases(t.Context(), "o/r")
	require.Error(t, err)
	assert.Contains(t, err.Error(), fmt.Sprintf("more than %d pages", maxReleasePages))
	mu.Lock()
	defer mu.Unlock()
	assert.Len(t, pages, maxReleasePages, "stops at the cap without requesting another page")
}
