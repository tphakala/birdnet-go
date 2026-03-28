package guideprovider

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestWikipediaGuideProvider_Fetch_Success(t *testing.T) {
	t.Parallel()

	// Create mock Wikipedia REST API server
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		response := wikipediaSummaryResponse{
			Type:    "standard",
			Title:   "Common blackbird",
			Extract: "The common blackbird is a species of true thrush.",
		}
		response.ContentURLs.Desktop.Page = "https://en.wikipedia.org/wiki/Common_blackbird"

		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(response)
	}))
	defer server.Close()

	provider := NewWikipediaGuideProvider()
	// Override the base URL to use our test server
	// We'll test by creating a custom fetch that uses our server URL
	guide, err := fetchFromTestServer(t, server.URL, "Turdus merula")
	require.NoError(t, err)

	assert.Equal(t, "Common blackbird", guide.CommonName)
	assert.Equal(t, "The common blackbird is a species of true thrush.", guide.Description)
	assert.Equal(t, WikipediaProviderName, guide.SourceProvider)
	assert.Equal(t, "CC BY-SA 4.0", guide.LicenseName)
	assert.True(t, guide.Partial)

	_ = provider // ensure provider is used
}

func TestWikipediaGuideProvider_Fetch_NotFound(t *testing.T) {
	t.Parallel()

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusNotFound)
	}))
	defer server.Close()

	_, err := fetchFromTestServer(t, server.URL, "Nonexistent species")
	assert.ErrorIs(t, err, ErrGuideNotFound)
}

func TestWikipediaGuideProvider_Fetch_Disambiguation(t *testing.T) {
	t.Parallel()

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		response := wikipediaSummaryResponse{
			Type:  "disambiguation",
			Title: "Blackbird",
		}
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(response)
	}))
	defer server.Close()

	_, err := fetchFromTestServer(t, server.URL, "Blackbird")
	assert.ErrorIs(t, err, ErrGuideNotFound)
}

func TestWikipediaGuideProvider_Fetch_RateLimited(t *testing.T) {
	t.Parallel()

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusTooManyRequests)
	}))
	defer server.Close()

	provider := newTestWikipediaProvider(server.URL)
	_, err := provider.fetchSummary(context.Background(), "Turdus merula")
	assert.Error(t, err)

	// Circuit breaker should be tripped
	open, reason := provider.isCircuitOpen()
	assert.True(t, open)
	assert.Equal(t, "rate limited", reason)
}

func TestWikipediaGuideProvider_Fetch_EmptyExtract(t *testing.T) {
	t.Parallel()

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		response := wikipediaSummaryResponse{
			Type:    "standard",
			Title:   "Some page",
			Extract: "",
		}
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(response)
	}))
	defer server.Close()

	_, err := fetchFromTestServer(t, server.URL, "Empty page")
	assert.ErrorIs(t, err, ErrGuideNotFound)
}

func TestWikipediaGuideProvider_CircuitBreaker(t *testing.T) {
	t.Parallel()

	provider := NewWikipediaGuideProvider()

	// Initially closed
	open, _ := provider.isCircuitOpen()
	assert.False(t, open)

	// Trip it
	provider.tripCircuitBreaker(1*time.Minute, "test reason")
	open, reason := provider.isCircuitOpen()
	assert.True(t, open)
	assert.Equal(t, "test reason", reason)
	assert.Equal(t, 1, provider.circuitFailures)

	// Trip again to verify consecutive failure tracking
	provider.tripCircuitBreaker(1*time.Minute, "second failure")
	assert.Equal(t, 2, provider.circuitFailures)

	// Reset on success
	provider.resetCircuit()
	open, _ = provider.isCircuitOpen()
	assert.False(t, open)
	assert.Equal(t, 0, provider.circuitFailures)
	assert.Equal(t, "", provider.circuitLastError)
}

func TestWikipediaGuideProvider_DescriptionTruncation(t *testing.T) {
	t.Parallel()

	// Create a long description
	longText := make([]byte, maxDescriptionLength+500)
	for i := range longText {
		longText[i] = 'a'
	}

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		response := wikipediaSummaryResponse{
			Type:    "standard",
			Title:   "Test",
			Extract: string(longText),
		}
		response.ContentURLs.Desktop.Page = "https://en.wikipedia.org/wiki/Test"

		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(response)
	}))
	defer server.Close()

	guide, err := fetchFromTestServer(t, server.URL, "Test")
	require.NoError(t, err)

	// Should be truncated to maxDescriptionLength + "..."
	assert.Equal(t, maxDescriptionLength+3, len(guide.Description))
	assert.True(t, len(guide.Description) <= maxDescriptionLength+3)
}

// fetchFromTestServer is a test helper that creates a provider pointing at a test server.
func fetchFromTestServer(t *testing.T, serverURL, title string) (SpeciesGuide, error) {
	t.Helper()
	provider := newTestWikipediaProvider(serverURL)
	return provider.fetchSummary(context.Background(), title)
}

// newTestWikipediaProvider creates a WikipediaGuideProvider pointing at a test server.
func newTestWikipediaProvider(baseURL string) *WikipediaGuideProvider {
	provider := NewWikipediaGuideProvider()
	// We override the fetch by modifying the base URL constant via a closure.
	// Since we can't modify the const, we create a provider that builds URLs
	// using the test server URL instead.
	provider.testBaseURL = baseURL
	return provider
}
