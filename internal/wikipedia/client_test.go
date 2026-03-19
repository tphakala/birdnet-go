package wikipedia

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/patrickmn/go-cache"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func newTestSummary() *Summary {
	return &Summary{
		Title:       "Masked Lapwing",
		Extract:     "The masked lapwing is a large, common and conspicuous bird native to Australia.",
		Description: "Species of bird in the family Charadriidae",
		Thumbnail: &Thumbnail{
			Source: "https://upload.wikimedia.org/test.jpg",
			Width:  320,
			Height: 240,
		},
		ContentURLs: &ContentURL{
			Mobile:  &PageURL{Page: "https://en.m.wikipedia.org/wiki/Masked_lapwing"},
			Desktop: &PageURL{Page: "https://en.wikipedia.org/wiki/Masked_lapwing"},
		},
	}
}

func newTestServer(t *testing.T, handler http.HandlerFunc) *httptest.Server {
	t.Helper()
	return httptest.NewServer(handler)
}

func TestGetSummary_Success(t *testing.T) {
	t.Parallel()

	expected := newTestSummary()
	server := newTestServer(t, func(w http.ResponseWriter, r *http.Request) {
		assert.Contains(t, r.URL.Path, "Masked_Lapwing")
		assert.Equal(t, UserAgent, r.Header.Get("User-Agent"))
		w.Header().Set("Content-Type", "application/json")
		err := json.NewEncoder(w).Encode(expected)
		require.NoError(t, err)
	})
	defer server.Close()

	client := &Client{
		httpClient: server.Client(),
		cache:      cache.New(DefaultCacheTTL, DefaultCacheCleanup),
		lang:       "en",
	}
	// Override the fetch to use our test server
	// We need to test with a real URL pattern, so we test fetchSummary directly
	// with a modified client that points to our test server
	client.httpClient = &http.Client{
		Timeout: DefaultTimeout,
		Transport: &rewriteTransport{
			base:    http.DefaultTransport,
			baseURL: server.URL,
		},
	}

	summary, err := client.GetSummary(context.Background(), "Masked Lapwing", "Vanellus miles")
	require.NoError(t, err)
	assert.Equal(t, expected.Title, summary.Title)
	assert.Equal(t, expected.Extract, summary.Extract)
	assert.Equal(t, expected.Description, summary.Description)
}

func TestGetSummary_CacheHit(t *testing.T) {
	t.Parallel()

	expected := newTestSummary()
	callCount := 0

	server := newTestServer(t, func(w http.ResponseWriter, _ *http.Request) {
		callCount++
		w.Header().Set("Content-Type", "application/json")
		err := json.NewEncoder(w).Encode(expected)
		require.NoError(t, err)
	})
	defer server.Close()

	client := &Client{
		httpClient: &http.Client{
			Timeout: DefaultTimeout,
			Transport: &rewriteTransport{
				base:    http.DefaultTransport,
				baseURL: server.URL,
			},
		},
		cache: cache.New(DefaultCacheTTL, DefaultCacheCleanup),
		lang:  "en",
	}

	// First call - should hit the server
	_, err := client.GetSummary(context.Background(), "Masked Lapwing", "Vanellus miles")
	require.NoError(t, err)
	assert.Equal(t, 1, callCount)

	// Second call - should hit cache
	_, err = client.GetSummary(context.Background(), "Masked Lapwing", "Vanellus miles")
	require.NoError(t, err)
	assert.Equal(t, 1, callCount, "expected cache hit, but server was called again")
}

func TestGetSummary_FallbackToScientificName(t *testing.T) {
	t.Parallel()

	expected := newTestSummary()

	server := newTestServer(t, func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/api/rest_v1/page/summary/Unknown_Bird" {
			w.WriteHeader(http.StatusNotFound)
			return
		}
		// Scientific name should work
		assert.Contains(t, r.URL.Path, "Vanellus_miles")
		w.Header().Set("Content-Type", "application/json")
		err := json.NewEncoder(w).Encode(expected)
		require.NoError(t, err)
	})
	defer server.Close()

	client := &Client{
		httpClient: &http.Client{
			Timeout: DefaultTimeout,
			Transport: &rewriteTransport{
				base:    http.DefaultTransport,
				baseURL: server.URL,
			},
		},
		cache: cache.New(DefaultCacheTTL, DefaultCacheCleanup),
		lang:  "en",
	}

	summary, err := client.GetSummary(context.Background(), "Unknown Bird", "Vanellus miles")
	require.NoError(t, err)
	assert.Equal(t, expected.Title, summary.Title)
}

func TestGetSummary_NotFound(t *testing.T) {
	t.Parallel()

	server := newTestServer(t, func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusNotFound)
	})
	defer server.Close()

	client := &Client{
		httpClient: &http.Client{
			Timeout: DefaultTimeout,
			Transport: &rewriteTransport{
				base:    http.DefaultTransport,
				baseURL: server.URL,
			},
		},
		cache: cache.New(DefaultCacheTTL, DefaultCacheCleanup),
		lang:  "en",
	}

	_, err := client.GetSummary(context.Background(), "Nonexistent Bird", "Nonexistent species")
	assert.Error(t, err)
}

func TestGetSummary_Timeout(t *testing.T) {
	t.Parallel()

	server := newTestServer(t, func(w http.ResponseWriter, _ *http.Request) {
		time.Sleep(200 * time.Millisecond)
		w.WriteHeader(http.StatusOK)
	})
	defer server.Close()

	client := &Client{
		httpClient: &http.Client{
			Timeout: 50 * time.Millisecond,
			Transport: &rewriteTransport{
				base:    http.DefaultTransport,
				baseURL: server.URL,
			},
		},
		cache: cache.New(DefaultCacheTTL, DefaultCacheCleanup),
		lang:  "en",
	}

	_, err := client.GetSummary(context.Background(), "Masked Lapwing", "Vanellus miles")
	assert.Error(t, err)
}

func TestArticleURL(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name     string
		summary  Summary
		expected string
	}{
		{
			name: "prefers mobile URL",
			summary: Summary{
				ContentURLs: &ContentURL{
					Mobile:  &PageURL{Page: "https://en.m.wikipedia.org/wiki/Test"},
					Desktop: &PageURL{Page: "https://en.wikipedia.org/wiki/Test"},
				},
			},
			expected: "https://en.m.wikipedia.org/wiki/Test",
		},
		{
			name: "falls back to desktop URL",
			summary: Summary{
				ContentURLs: &ContentURL{
					Desktop: &PageURL{Page: "https://en.wikipedia.org/wiki/Test"},
				},
			},
			expected: "https://en.wikipedia.org/wiki/Test",
		},
		{
			name:     "returns empty for nil content URLs",
			summary:  Summary{},
			expected: "",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			assert.Equal(t, tt.expected, tt.summary.ArticleURL())
		})
	}
}

// rewriteTransport rewrites all requests to point to the test server.
type rewriteTransport struct {
	base    http.RoundTripper
	baseURL string
}

func (t *rewriteTransport) RoundTrip(req *http.Request) (*http.Response, error) {
	// Rewrite the URL to point to test server, preserving the path
	req.URL.Scheme = "http"
	req.URL.Host = t.baseURL[len("http://"):]
	return t.base.RoundTrip(req)
}
