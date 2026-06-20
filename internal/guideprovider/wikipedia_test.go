package guideprovider

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/tphakala/birdnet-go/internal/errors"
)

// newWikipediaTestProvider points the provider at a test server by overriding
// its HTTP client transport to rewrite the host.
func newWikipediaTestProvider(t *testing.T, srv *httptest.Server) *WikipediaGuideProvider {
	t.Helper()
	p := NewWikipediaGuideProviderWithMetrics(noopMetrics{})
	p.client = srv.Client()
	p.client.Transport = rewriteTransport{base: srv.URL, rt: srv.Client().Transport}
	return p
}

// rewriteTransport redirects all requests to the test server's base URL while
// preserving the original path and query.
type rewriteTransport struct {
	base string
	rt   http.RoundTripper
}

func (t rewriteTransport) RoundTrip(req *http.Request) (*http.Response, error) {
	base := req.URL
	// Replace scheme+host with the test server's.
	newURL := t.base + base.Path
	if base.RawQuery != "" {
		newURL += "?" + base.RawQuery
	}
	r2, err := http.NewRequestWithContext(req.Context(), req.Method, newURL, req.Body)
	if err != nil {
		return nil, err
	}
	r2.Header = req.Header
	rt := t.rt
	if rt == nil {
		rt = http.DefaultTransport
	}
	return rt.RoundTrip(r2)
}

const sampleWikiResponse = `{
  "query": {
    "pages": {
      "12345": {
        "pageid": 12345,
        "title": "Common Blackbird",
        "fullurl": "https://en.wikipedia.org/wiki/Common_blackbird",
        "extract": "The common blackbird is a species of true thrush.\n\n== Voice ==\nThe male sings.\n\n=== Dialects ===\nRegional variation exists.\n\n== Similar species ==\nThe ring ouzel is similar."
      }
    }
  }
}`

func TestWikipediaProvider_FetchSuccess(t *testing.T) {
	t.Parallel()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(sampleWikiResponse))
	}))
	t.Cleanup(srv.Close)

	p := newWikipediaTestProvider(t, srv)
	g, err := p.Fetch(t.Context(), "Turdus merula", FetchOptions{Locale: "en"})
	require.NoError(t, err)
	require.NotNil(t, g)
	assert.Equal(t, "Common Blackbird", g.CommonName)
	assert.Equal(t, WikipediaProviderName, g.SourceProvider)
	assert.Equal(t, wikipediaLicense, g.License)
	assert.Contains(t, g.Description, "## Voice")
	assert.Contains(t, g.Description, "## Similar species")
	// Deeper headers are flattened, not promoted to "## ".
	assert.NotContains(t, g.Description, "## Dialects")
	assert.Contains(t, g.Description, "Dialects")
}

// TestWikipediaProvider_UserAgent guards the Wikimedia UA-policy fix: the
// provider must send a "Mozilla/5.0 (compatible; ...)" User-Agent. Bare
// "App/1.0 (url)" agents are rejected by Wikimedia with HTTP 403 (phab T400119),
// which silently degraded every guide lookup to "not found".
func TestWikipediaProvider_UserAgent(t *testing.T) {
	t.Parallel()
	var gotUA string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotUA = r.Header.Get("User-Agent")
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(sampleWikiResponse))
	}))
	t.Cleanup(srv.Close)

	p := newWikipediaTestProvider(t, srv)
	_, err := p.Fetch(t.Context(), "Turdus merula", FetchOptions{Locale: "en"})
	require.NoError(t, err)
	assert.Equal(t, wikipediaUserAgent, gotUA)
	assert.True(t, strings.HasPrefix(gotUA, "Mozilla/5.0 (compatible;"),
		"Wikimedia rejects non-browser-shaped User-Agents with 403")
}

func TestWikipediaProvider_NotFound(t *testing.T) {
	t.Parallel()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusNotFound)
	}))
	t.Cleanup(srv.Close)

	p := newWikipediaTestProvider(t, srv)
	_, err := p.Fetch(t.Context(), "Nope", FetchOptions{Locale: "en"})
	require.Error(t, err)
	assert.True(t, errors.Is(err, ErrGuideNotFound))
	assert.False(t, IsTransient(err))
}

func TestWikipediaProvider_MissingPage(t *testing.T) {
	t.Parallel()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte(`{"query":{"pages":{"-1":{"title":"X","missing":{}}}}}`))
	}))
	t.Cleanup(srv.Close)

	p := newWikipediaTestProvider(t, srv)
	_, err := p.Fetch(t.Context(), "Nope", FetchOptions{Locale: "en"})
	assert.True(t, errors.Is(err, ErrGuideNotFound))
}

func TestWikipediaProvider_ServerErrorIsTransient(t *testing.T) {
	t.Parallel()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusBadGateway)
	}))
	t.Cleanup(srv.Close)

	p := newWikipediaTestProvider(t, srv)
	_, err := p.Fetch(t.Context(), "Turdus merula", FetchOptions{Locale: "en"})
	require.Error(t, err)
	assert.True(t, IsTransient(err), "5xx must be transient so no negative entry is cached")
}

func TestConvertWikiSections(t *testing.T) {
	t.Parallel()
	in := "Intro text.\n\n== Voice ==\nSings.\n\n=== Subsong ===\nQuiet.\n\n== Habitat ==\nForests."
	out := convertWikiSections(in)
	assert.Contains(t, out, "## Voice")
	assert.Contains(t, out, "## Habitat")
	assert.NotContains(t, out, "== Voice ==")
	assert.NotContains(t, out, "## Subsong")
	assert.Contains(t, out, "Subsong")

	// Splitting by parseGuideDescription's contract: leading intro then sections.
	parts := strings.Split(out, "## ")
	assert.GreaterOrEqual(t, len(parts), 3)
}
