package guideprovider

import (
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/tphakala/birdnet-go/internal/branding"
	"github.com/tphakala/birdnet-go/internal/errors"
	"github.com/tphakala/birdnet-go/internal/useragent"
)

// newWikipediaTestProvider points the provider at a test server. The redirect
// uses the shared client's before-request hook rather than a custom transport,
// so the request travels the same path production does (timeout handling,
// User-Agent injection, hooks) with only the host swapped.
func newWikipediaTestProvider(t *testing.T, srv *httptest.Server) *WikipediaGuideProvider {
	t.Helper()
	p := NewWikipediaGuideProviderWithMetrics(noopMetrics{})
	t.Cleanup(func() { require.NoError(t, p.Close()) })

	target, err := url.Parse(srv.URL)
	require.NoError(t, err)
	// Preserves the original path and query; replaces scheme+host.
	p.client.SetBeforeRequestHook(func(req *http.Request) {
		req.URL.Scheme = target.Scheme
		req.URL.Host = target.Host
		req.Host = ""
	})
	return p
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
	assert.Equal(t, wikipediaUserAgent(), gotUA)
	assert.True(t, strings.HasPrefix(gotUA, "Mozilla/5.0 (compatible;"),
		"Wikimedia rejects non-browser-shaped User-Agents with 403")
	assert.Contains(t, gotUA, useragent.PoliteBotName)
	// The contact URL must come from branding, not a hardcoded upstream literal, so
	// a rebranded fork advertises its own operator rather than tphakala's.
	assert.Contains(t, gotUA, branding.RepoURL())
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
	require.Error(t, err)
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

// TestWikipediaProvider_RefusalStopsFurtherRequests guards the refusal cooldown: an
// outright refusal (401/403/451) must be non-transient AND must stop the provider
// issuing further requests, because neither a transient nor a plain error is persisted
// by fetchAndStore — so without the cooldown every uncached species would re-issue a
// call already known to fail, and every refresh sweep would do it again.
func TestWikipediaProvider_RefusalStopsFurtherRequests(t *testing.T) {
	t.Parallel()
	for _, status := range []int{http.StatusUnauthorized, http.StatusForbidden, http.StatusUnavailableForLegalReasons} {
		t.Run(http.StatusText(status), func(t *testing.T) {
			t.Parallel()
			var calls int
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
				calls++
				w.WriteHeader(status)
			}))
			t.Cleanup(srv.Close)

			p := newWikipediaTestProvider(t, srv)

			_, err := p.Fetch(t.Context(), "Turdus merula", FetchOptions{Locale: "en"})
			require.Error(t, err)
			require.ErrorIs(t, err, ErrWikipediaRefused, "a refusal reports ErrWikipediaRefused")
			assert.False(t, IsTransient(err), "a refusal must NOT be transient: it does not resolve by retrying")
			require.Equal(t, 1, calls, "the first fetch reaches the upstream")

			// A different species must not reach the upstream while the cooldown holds.
			_, err = p.Fetch(t.Context(), "Parus major", FetchOptions{Locale: "en"})
			require.Error(t, err)
			require.ErrorIs(t, err, ErrWikipediaRefused)
			assert.Equal(t, 1, calls, "no further requests are issued during the cooldown")

			// Once the cooldown lapses the provider retries (the refusal may have been
			// resolved by an operator or an upstream policy change).
			p.refusalMu.Lock()
			p.refusedUntil = time.Now().Add(-time.Second)
			p.refusalMu.Unlock()
			_, err = p.Fetch(t.Context(), "Turdus merula", FetchOptions{Locale: "en"})
			require.Error(t, err)
			assert.Equal(t, 2, calls, "an expired cooldown allows exactly one more attempt")
		})
	}
}

// TestWikipediaProvider_RateLimitAndTimeoutAreTransient guards that 429 (rate
// limited) and 408 (request timeout) are transient: a non-transient error here
// would make the cache persist a 30-minute negative entry and stop retrying a
// species that was merely throttled.
func TestWikipediaProvider_RateLimitAndTimeoutAreTransient(t *testing.T) {
	t.Parallel()
	for _, status := range []int{http.StatusTooManyRequests, http.StatusRequestTimeout} {
		t.Run(http.StatusText(status), func(t *testing.T) {
			t.Parallel()
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
				w.WriteHeader(status)
			}))
			t.Cleanup(srv.Close)

			p := newWikipediaTestProvider(t, srv)
			_, err := p.Fetch(t.Context(), "Turdus merula", FetchOptions{Locale: "en"})
			require.Error(t, err)
			assert.True(t, IsTransient(err), "status %d must be transient so no negative entry is cached", status)
			assert.False(t, errors.Is(err, ErrGuideNotFound))
		})
	}
}

// TestWikipediaSubdomain verifies UI locales are mapped to a valid Wikipedia
// language subdomain: regional subtags are dropped (Wikipedia has no
// pt-br.wikipedia.org) and the nb/nn -> no override is applied.
func TestWikipediaSubdomain(t *testing.T) {
	t.Parallel()
	cases := map[string]string{
		"en":      "en",
		"de":      "de",
		"pt-br":   "pt", // regional subtag dropped
		"pt_PT":   "pt", // underscore separator, uppercase
		"zh-CN":   "zh",
		"en-GB":   "en",
		"nb":      "no", // Norwegian Bokmål lives on no.wikipedia.org
		"nn-no":   "no", // Nynorsk + subtag
		"":        "en", // empty -> default
		"x":       "en", // too short
		"english": "en", // too long
		"e1":      "en", // non-alpha
		// Real hyphenated Wikipedia subdomains are PRESERVED whole (they exist as
		// their own editions), not collapsed to the base subtag.
		wpEditionZhClassical: wpEditionZhClassical,
		wpEditionZhMinNan:    wpEditionZhMinNan,
		wpEditionBeTarask:    wpEditionBeTarask,
		wpEditionNdsNl:       wpEditionNdsNl,
		"zh_classical":       wpEditionZhClassical, // underscore separator normalized
		// A regional variant that is NOT a real subdomain still collapses.
		"zh-cn": "zh",
	}
	for in, want := range cases {
		assert.Equalf(t, want, wikipediaSubdomain(in), "wikipediaSubdomain(%q)", in)
	}
	// Surrounding whitespace is trimmed before the subtag split.
	assert.Equal(t, "fr", wikipediaSubdomain("  fr-ca  "))
}

// TestWikipediaBuildURL_RegionalLocaleUsesBaseSubdomain guards the fix for the
// broken host: a regional locale must resolve to the base-language Wikipedia host,
// not an invalid pt-br.wikipedia.org that fails DNS on every request.
func TestWikipediaBuildURL_RegionalLocaleUsesBaseSubdomain(t *testing.T) {
	t.Parallel()
	endpoint := (&WikipediaGuideProvider{}).buildURL("pt-br", "Turdus merula")
	assert.Contains(t, endpoint, "https://pt.wikipedia.org/w/api.php?")
	assert.NotContains(t, endpoint, "pt-br.wikipedia.org")
}

func TestWikipediaProvider_Name(t *testing.T) {
	t.Parallel()
	assert.Equal(t, WikipediaProviderName, NewWikipediaGuideProviderWithMetrics(noopMetrics{}).Name())
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
