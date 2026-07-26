// lookup_correctness_test.go covers the Wikipedia lookup contract: which answers
// count as "this species has no image" and which do not.
//
// The distinction is load-bearing. A not-found verdict is persisted as a
// negative cache entry, so classifying a transient provider failure as one marks
// the species image-less for the whole negative TTL, and classifying a
// redirect-to-genus as a success caches an image of the wrong bird.
package imageprovider

import (
	"net/http"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/tphakala/birdnet-go/internal/conf"
	"github.com/tphakala/birdnet-go/internal/conf/conftest"
	"github.com/tphakala/birdnet-go/internal/errors"
)

// writeJSON is the shape every MediaWiki stub in this file answers with.
func writeJSON(w http.ResponseWriter, body string) {
	w.Header().Set("Content-Type", "application/json")
	_, _ = w.Write([]byte(body))
}

// TestRedirectLeftSpecies is the unit-level half of root cause C2.
//
// Redirect following has to stay on, because most bird articles are titled by
// common name and a scientific name only resolves through its redirect. What
// must not pass is a redirect up the taxonomy: Wikipedia sends a species with no
// article of its own to its genus or family article, whose pageimage is
// routinely a distribution map or a congener.
func TestRedirectLeftSpecies(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name       string
		species    string
		redirects  []wikiRedirect
		wantLeft   bool
		wantTarget string
	}{
		{
			name:    "no redirect",
			species: "Turdus merula",
		},
		{
			name:      "common-name article is what redirects are for",
			species:   "Turdus merula",
			redirects: []wikiRedirect{{From: "Turdus merula", To: "Common blackbird"}},
		},
		{
			name:      "single-word common name",
			species:   "Opisthocomus hoazin",
			redirects: []wikiRedirect{{From: "Opisthocomus hoazin", To: "Hoatzin"}},
		},
		{
			name:       "genus article",
			species:    "Delichon urbicum",
			redirects:  []wikiRedirect{{From: "Delichon urbicum", To: "Delichon"}},
			wantLeft:   true,
			wantTarget: "Delichon",
		},
		{
			name:       "family article",
			species:    "Delichon urbicum",
			redirects:  []wikiRedirect{{From: "Delichon urbicum", To: "Hirundinidae"}},
			wantLeft:   true,
			wantTarget: "Hirundinidae",
		},
		{
			name:       "subfamily article",
			species:    "Delichon urbicum",
			redirects:  []wikiRedirect{{From: "Delichon urbicum", To: "Hirundininae"}},
			wantLeft:   true,
			wantTarget: "Hirundininae",
		},
		{
			name:       "order article",
			species:    "Delichon urbicum",
			redirects:  []wikiRedirect{{From: "Delichon urbicum", To: "Passeriformes"}},
			wantLeft:   true,
			wantTarget: "Passeriformes",
		},
		{
			name:      "underscores and case are title syntax, not a different page",
			species:   "Turdus merula",
			redirects: []wikiRedirect{{From: "Turdus_merula", To: "Common_blackbird"}},
		},
		{
			name:    "chained redirect ending on the genus",
			species: "Delichon urbica",
			redirects: []wikiRedirect{
				{From: "Delichon urbica", To: "Delichon urbicum"},
				{From: "Delichon urbicum", To: "Delichon"},
			},
			wantLeft:   true,
			wantTarget: "Delichon",
		},
		{
			name:      "a synonym redirect to another binomial is fine",
			species:   "Delichon urbica",
			redirects: []wikiRedirect{{From: "Delichon urbica", To: "Delichon urbicum"}},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			target, left := redirectLeftSpecies(tt.species, tt.redirects)
			assert.Equal(t, tt.wantLeft, left, "redirect verdict for %q", tt.species)
			assert.Equal(t, tt.wantTarget, target)
		})
	}
}

// TestQueryThumbnail_RedirectToGenusIsNotAnImage is the end-to-end half of C2.
//
// The lookup succeeds at the HTTP level, which is exactly why this was so
// durable: a positive entry was written, and the taxonomy-synonym map, which is
// only consulted on failure, never got a chance to correct it.
func TestQueryThumbnail_RedirectToGenusIsNotAnImage(t *testing.T) {
	t.Parallel()

	provider := newTestWikiProvider(t, func(w http.ResponseWriter, _ *http.Request) {
		writeJSON(w, `{"query":{
			"redirects":[{"from":"Delichon urbicum","to":"Delichon"}],
			"pages":[{"title":"Delichon","pageimage":"Delichon_distribution.png",
			          "thumbnail":{"source":"https://example.invalid/map.png"}}]}}`)
	})

	url, file, err := provider.queryThumbnail(t.Context(), "test", "Delichon urbicum", nil)

	require.Error(t, err)
	require.ErrorIs(t, err, ErrImageNotFound,
		"a genus article's image is not this species' image, and the synonym retry only runs on not-found")
	assert.Empty(t, url)
	assert.Empty(t, file)
}

// TestQueryThumbnail_MissingPageImageStillYieldsTheThumbnail covers the discarded
// thumbnail: pageimage is only the key for the second, attribution query, and the
// caller already falls back to "Unknown" attribution.
func TestQueryThumbnail_MissingPageImageStillYieldsTheThumbnail(t *testing.T) {
	t.Parallel()

	provider := newTestWikiProvider(t, func(w http.ResponseWriter, _ *http.Request) {
		writeJSON(w, `{"query":{"pages":[{"title":"Turdus merula",
			"thumbnail":{"source":"https://example.invalid/blackbird.jpg"}}]}}`)
	})

	url, file, err := provider.queryThumbnail(t.Context(), "test", "Turdus merula", nil)

	require.NoError(t, err, "a usable free thumbnail must not be thrown away for want of an attribution key")
	assert.Equal(t, "https://example.invalid/blackbird.jpg", url)
	assert.Empty(t, file, "no pageimage means no file page to ask for attribution")
}

// TestFetch_MissingPageImageUsesUnknownAttribution checks the whole fetch, since
// the attribution query has to be skipped rather than failed.
func TestFetch_MissingPageImageUsesUnknownAttribution(t *testing.T) {
	t.Parallel()

	var requests int
	provider := newTestWikiProvider(t, func(w http.ResponseWriter, _ *http.Request) {
		requests++
		writeJSON(w, `{"query":{"pages":[{"title":"Turdus merula",
			"thumbnail":{"source":"https://example.invalid/blackbird.jpg"}}]}}`)
	})

	img, err := provider.fetchWithLimiter(t.Context(), "Turdus merula", nil)

	require.NoError(t, err)
	assert.Equal(t, "https://example.invalid/blackbird.jpg", img.URL)
	assert.Equal(t, unknownMetadataValue, img.AuthorName)
	assert.Equal(t, unknownMetadataValue, img.LicenseName)
	assert.Equal(t, 1, requests, "with no pageimage there is no file page to query")
}

// TestQueryAndGetFirstPage_StructuredErrorIsNotNotFound is the negative-cache
// poisoning guard. MediaWiki returns ratelimited, maxlag and readonly with HTTP
// 200 and no query object, so reading a nil query as "no such species" turned a
// Wikipedia throttling window into durable __NOT_FOUND__ rows.
func TestQueryAndGetFirstPage_StructuredErrorIsNotNotFound(t *testing.T) {
	t.Parallel()

	provider := newTestWikiProvider(t, func(w http.ResponseWriter, _ *http.Request) {
		writeJSON(w, `{"error":{"code":"ratelimited","info":"You've exceeded your rate limit."}}`)
	})

	page, redirects, err := provider.queryAndGetFirstPageWithLimiter(t.Context(), "test",
		map[string]string{"titles": "Turdus merula"}, nil)

	require.Error(t, err)
	require.NotErrorIs(t, err, ErrImageNotFound,
		"a refused request must not be cached as 'this species has no image'")
	assert.Equal(t, errors.CategoryNetwork, errors.CategoryOf(err))
	assert.Nil(t, page)
	assert.Nil(t, redirects)

	open, _ := provider.isCircuitOpen()
	assert.True(t, open, "a rate-limit error arrives with HTTP 200, so only this path can trip the breaker")
}

// TestQueryAndGetFirstPage_MissingPageIsNotFound is the other half: with
// formatversion=2 an absent title is reported as pages:[{missing:true}], and that
// one genuinely is a not-found.
func TestQueryAndGetFirstPage_MissingPageIsNotFound(t *testing.T) {
	t.Parallel()

	provider := newTestWikiProvider(t, func(w http.ResponseWriter, _ *http.Request) {
		writeJSON(w, `{"query":{"pages":[{"title":"Nonexistent species","missing":true}]}}`)
	})

	page, _, err := provider.queryAndGetFirstPageWithLimiter(t.Context(), "test",
		map[string]string{"titles": "Nonexistent species"}, nil)

	require.Error(t, err)
	require.ErrorIs(t, err, ErrImageNotFound)
	assert.Nil(t, page)
}

// TestQueryAndGetFirstPage_InvalidTitleIsNotFound covers the API error codes that
// mean the title can never resolve, which are the only ones worth caching.
func TestQueryAndGetFirstPage_InvalidTitleIsNotFound(t *testing.T) {
	t.Parallel()

	provider := newTestWikiProvider(t, func(w http.ResponseWriter, _ *http.Request) {
		writeJSON(w, `{"error":{"code":"invalidtitle","info":"Bad title"}}`)
	})

	_, _, err := provider.queryAndGetFirstPageWithLimiter(t.Context(), "test",
		map[string]string{"titles": "["}, nil)

	require.Error(t, err)
	require.ErrorIs(t, err, ErrImageNotFound)
}

// TestClassifyForbiddenBody is the request-amplification guard.
//
// The classifier used to match the bare tokens "rate" and "limit", which also
// occur inside "corporate", "moderate" and "unlimited". A hard block whose page
// contained any of those got the 60-second rate-limit breaker instead of the
// five-minute block breaker, so the app resumed hammering a host that had just
// refused it.
func TestClassifyForbiddenBody(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name string
		body string
		want wikiErrorType
	}{
		{name: "user-agent policy", body: "your user-agent is not allowed", want: wikiErrorUserAgent},
		{name: "robot policy", body: "see our robot policy", want: wikiErrorUserAgent},
		{name: "rate limit prose", body: "you have hit our rate limit", want: wikiErrorRateLimit},
		{name: "ratelimited api code", body: `{"code":"ratelimited"}`, want: wikiErrorRateLimit},
		{name: "too many requests", body: "too many requests, slow down", want: wikiErrorRateLimit},
		{name: "throttled", body: "your client is being throttled", want: wikiErrorRateLimit},
		{name: "corporate is not a rate limit", body: "contact our corporate office", want: wikiErrorBlocked},
		{name: "moderate is not a rate limit", body: "this request was moderated", want: wikiErrorBlocked},
		{name: "unlimited is not a rate limit", body: "unlimited access requires approval", want: wikiErrorBlocked},
		{name: "plain block", body: "access denied", want: wikiErrorBlocked},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			assert.Equal(t, tt.want, classifyForbiddenBody(tt.body))
		})
	}
}

// TestWikiFetchAllowed pins the configuration gate.
//
// It mutates the global settings, so it must not run in parallel with anything
// that reads them.
func TestWikiFetchAllowed(t *testing.T) {
	prev := conf.GetSettings()
	t.Cleanup(func() { conftest.SetTestSettings(prev) })

	newSettings := func(provider, fallback string) *conf.Settings {
		return conftest.NewTestSettings().WithImageProvider(provider, fallback).Build()
	}

	tests := []struct {
		name        string
		settings    *conf.Settings
		wantAllowed bool
	}{
		{name: "wikimedia is the provider", settings: newSettings("wikimedia", "none"), wantAllowed: true},
		{name: "auto may elect wikimedia", settings: newSettings("auto", "none"), wantAllowed: true},
		{name: "unset provider behaves as auto", settings: newSettings("", "none"), wantAllowed: true},
		{name: "another provider with fallback off", settings: newSettings("avicommons", "none")},
		{name: "another provider with fallback on", settings: newSettings("avicommons", "all"), wantAllowed: true},
		// Both of these used to read as "off" on the fetch side while the refresh
		// side, which lowercased and trimmed, read them as "on".
		{name: "capitalised policy", settings: newSettings("avicommons", "All"), wantAllowed: true},
		{name: "policy with a trailing space", settings: newSettings("avicommons", "all "), wantAllowed: true},
		// Settings are nil only before the config loads or when loading failed.
		// Allowing traffic in that window contradicts a configured fallbackpolicy.
		{name: "no settings fails closed", settings: nil},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			// No t.Parallel(): these mutate the settings global.
			conftest.SetTestSettings(tt.settings)

			allowed, reason := wikiFetchAllowed()
			assert.Equal(t, tt.wantAllowed, allowed)
			if !allowed {
				assert.NotEmpty(t, reason, "a refusal must say why")
			}
		})
	}
}

// TestShouldRefreshCacheImpliesFetchAllowed is the invariant that matters between
// the two gates: the hourly refresh must never be enabled while the fetches it
// schedules are denied. It used to be, both through the case-sensitivity
// mismatch and through accepting a provider name as a policy value.
func TestShouldRefreshCacheImpliesFetchAllowed(t *testing.T) {
	prev := conf.GetSettings()
	t.Cleanup(func() { conftest.SetTestSettings(prev) })

	lazy := NewLazyWikiMediaProvider()
	for _, provider := range []string{"wikimedia", "avicommons", "auto", "", "WikiMedia"} {
		for _, policy := range []string{"none", "all", "All", "all ", "wikimedia", ""} {
			conftest.SetTestSettings(conftest.NewTestSettings().WithImageProvider(provider, policy).Build())

			if lazy.ShouldRefreshCache() {
				allowed, _ := wikiFetchAllowed()
				assert.True(t, allowed,
					"refresh enabled but fetch denied for provider=%q policy=%q", provider, policy)
			}
		}
	}
}

// TestFetch_QueriesTheSynonymFirst pins the inversion.
//
// A configured taxonomy synonym is the user asserting that the primary name is
// the wrong one to ask Wikipedia for. Consulting it only after a failure meant
// it could not fix the case it exists for: a lookup that succeeds under the
// primary name and returns the wrong image.
func TestFetch_QueriesTheSynonymFirst(t *testing.T) {
	t.Parallel()

	const (
		birdnetName = "Accipiter gentilis"
		synonymName = "Astur gentilis"
	)
	synonym, hasSynonym := GetTaxonomySynonym(birdnetName)
	require.True(t, hasSynonym, "this test needs a name carrying a built-in synonym")
	require.Equal(t, synonymName, synonym)

	var titles []string
	provider := newTestWikiProvider(t, func(w http.ResponseWriter, r *http.Request) {
		title := r.URL.Query().Get("titles")
		titles = append(titles, title)
		writeJSON(w, `{"query":{"pages":[{"title":"`+title+`",
			"thumbnail":{"source":"https://example.invalid/goshawk.jpg"}}]}}`)
	})

	img, err := provider.fetchWithLimiter(t.Context(), birdnetName, nil)

	require.NoError(t, err)
	require.NotEmpty(t, titles)
	assert.Equal(t, synonymName, titles[0], "the synonym is queried first, not as a retry")
	assert.Len(t, titles, 1, "a successful synonym lookup must not also query the original name")
	assert.Equal(t, birdnetName, img.ScientificName,
		"the result is still keyed by the name the caller asked for")
}

// TestFetch_FallsBackToTheOriginalNameWhenTheSynonymMisses keeps the inversion
// from costing coverage: the synonym may itself be the stale name.
func TestFetch_FallsBackToTheOriginalNameWhenTheSynonymMisses(t *testing.T) {
	t.Parallel()

	const birdnetName = "Accipiter gentilis"

	var titles []string
	provider := newTestWikiProvider(t, func(w http.ResponseWriter, r *http.Request) {
		title := r.URL.Query().Get("titles")
		titles = append(titles, title)
		if title != birdnetName {
			writeJSON(w, `{"query":{"pages":[{"title":"`+title+`","missing":true}]}}`)
			return
		}
		writeJSON(w, `{"query":{"pages":[{"title":"`+title+`",
			"thumbnail":{"source":"https://example.invalid/goshawk.jpg"}}]}}`)
	})

	img, err := provider.fetchWithLimiter(t.Context(), birdnetName, nil)

	require.NoError(t, err)
	assert.Equal(t, "https://example.invalid/goshawk.jpg", img.URL)
	require.Len(t, titles, 2)
	assert.Equal(t, birdnetName, titles[1], "the original name is still tried when the synonym misses")
}
