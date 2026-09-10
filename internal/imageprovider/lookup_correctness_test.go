// lookup_correctness_test.go covers the Wikipedia lookup contract: which answers
// count as "this species has no image" and which do not.
//
// The distinction is load-bearing. A not-found verdict is persisted as a
// negative cache entry, so classifying a transient provider failure as one marks
// the species image-less for the whole negative TTL, and classifying a
// redirect-to-genus as a success caches an image of the wrong bird.
package imageprovider

import (
	"context"
	"net/http"
	"slices"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/tphakala/birdnet-go/internal/conf"
	"github.com/tphakala/birdnet-go/internal/conf/conftest"
	"github.com/tphakala/birdnet-go/internal/errors"
)

// titleRecorder collects the titles a stub handler was asked for. The handler
// runs on the server's goroutine, and a loopback socket is not a happens-before
// edge, so the slice needs its own lock; wikipedia_http_test.go uses atomic
// counters for the same reason.
type titleRecorder struct {
	mu     sync.Mutex
	titles []string
}

func (r *titleRecorder) add(title string) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.titles = append(r.titles, title)
}

func (r *titleRecorder) snapshot() []string {
	r.mu.Lock()
	defer r.mu.Unlock()
	return slices.Clone(r.titles)
}

// writeJSON is the shape every MediaWiki stub in this file answers with.
func writeJSON(w http.ResponseWriter, body string) {
	w.Header().Set("Content-Type", "application/json")
	_, _ = w.Write([]byte(body))
}

// TestRedirectLeftSpecies is the unit-level half of root cause C2.
//
// Redirect following has to stay on, because most bird articles are titled by
// common name and a scientific name only resolves through its redirect
// (measured: 548 of 550 shipped species redirect). What must not pass is a
// redirect up to a family or order article, whose pageimage is a different
// bird or a distribution map.
//
// A redirect to the bare genus deliberately DOES pass: a monotypic genus keeps
// one combined article at the genus title and that article's image is this
// species' image, so rejecting it would discard a correct photo and, because
// the rejection is reported as ErrImageNotFound, persist the species as
// image-less for the negative TTL.
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
			// Every single-word redirect target in a 550-species live sample was
			// one of these, and none was a genus article.
			name:      "measured single-word common names",
			species:   "Lullula arborea",
			redirects: []wikiRedirect{{From: "Lullula arborea", To: "Woodlark"}},
		},
		{
			name:      "genus article passes: it may be a monotypic genus",
			species:   "Steatornis caripensis",
			redirects: []wikiRedirect{{From: "Steatornis caripensis", To: "Steatornis"}},
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
			// Only a single-word target is tested, so a qualified title passes.
			// Wikipedia keeps bird families at the bare name, so this shape does
			// not arise; the alternative, testing every word, rejects the nine
			// binomials below and is the worse trade by far.
			name:      "a qualified title is not tested",
			species:   "Accipiter nisus",
			redirects: []wikiRedirect{{From: "Accipiter nisus", To: "Accipitridae (family)"}},
		},
		{
			// Regression guard. These epithets are Latin genitives that happen to
			// end in a supra-generic suffix. Nine such binomials are in the
			// shipped V2.4 label set, and rejecting one caches it as "no image".
			name:      "a binomial whose epithet ends in -inae is not a higher taxon",
			species:   "Pyrrhura emma",
			redirects: []wikiRedirect{{From: "Pyrrhura emma", To: "Pyrrhura molinae"}},
		},
		{
			name:      "a binomial whose epithet ends in -idae is not a higher taxon",
			species:   "Setophaga petechia",
			redirects: []wikiRedirect{{From: "Setophaga petechia", To: "Setophaga adelaidae"}},
		},
		{
			name:      "underscores and case are title syntax, not a different page",
			species:   "Turdus merula",
			redirects: []wikiRedirect{{From: "Turdus_merula", To: "Common_blackbird"}},
		},
		{
			name:    "chained redirect ending on a family",
			species: "Delichon urbica",
			redirects: []wikiRedirect{
				{From: "Delichon urbica", To: "Delichon urbicum"},
				{From: "Delichon urbicum", To: "Hirundinidae"},
			},
			wantLeft:   true,
			wantTarget: "Hirundinidae",
		},
		{
			name:      "a synonym redirect to another binomial is fine",
			species:   "Delichon urbica",
			redirects: []wikiRedirect{{From: "Delichon urbica", To: "Delichon urbicum"}},
		},
		{
			// A cycle must not spin and must fail open, keeping the image.
			name:    "cycle fails open",
			species: "Turdus merula",
			redirects: []wikiRedirect{
				{From: "Turdus merula", To: "Blackbird"},
				{From: "Blackbird", To: "Turdus merula"},
			},
		},
		{
			name:      "self redirect fails open",
			species:   "Turdus merula",
			redirects: []wikiRedirect{{From: "Turdus merula", To: "Turdus merula"}},
		},
		{
			name:      "empty target fails open",
			species:   "Turdus merula",
			redirects: []wikiRedirect{{From: "Turdus merula", To: ""}},
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

// TestResolveRedirectTargetEmptyIsSafe pins the guard on the only indexing
// expression in the function, which its single caller currently makes
// unreachable.
func TestResolveRedirectTargetEmptyIsSafe(t *testing.T) {
	t.Parallel()

	assert.NotPanics(t, func() {
		assert.Empty(t, resolveRedirectTarget("Turdus merula", nil))
	})
}

// TestQueryThumbnail_RedirectToFamilyIsNotAnImage is the end-to-end half of C2.
//
// The lookup succeeds at the HTTP level, which is exactly why this was so
// durable: a positive entry was written, and the taxonomy-synonym map, which is
// only consulted on failure, never got a chance to correct it.
func TestQueryThumbnail_RedirectToFamilyIsNotAnImage(t *testing.T) {
	t.Parallel()

	provider := newTestWikiProvider(t, func(w http.ResponseWriter, _ *http.Request) {
		writeJSON(w, `{"query":{
			"redirects":[{"from":"Delichon urbicum","to":"Hirundinidae"}],
			"pages":[{"title":"Hirundinidae","pageimage":"Hirundinidae_distribution.png",
			          "thumbnail":{"source":"https://example.invalid/map.png"}}]}}`)
	})

	url, file, err := provider.queryThumbnail(t.Context(), "test", "Delichon urbicum", nil)

	require.Error(t, err)
	require.ErrorIs(t, err, ErrImageNotFound,
		"a family article's image is not this species' image, and the synonym retry only runs on not-found")
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

	var requests atomic.Int64
	provider := newTestWikiProvider(t, func(w http.ResponseWriter, _ *http.Request) {
		requests.Add(1)
		writeJSON(w, `{"query":{"pages":[{"title":"Turdus merula",
			"thumbnail":{"source":"https://example.invalid/blackbird.jpg"}}]}}`)
	})

	img, err := provider.fetchWithLimiter(t.Context(), "Turdus merula", nil)

	require.NoError(t, err)
	assert.Equal(t, "https://example.invalid/blackbird.jpg", img.URL)
	assert.Equal(t, unknownMetadataValue, img.AuthorName)
	assert.Equal(t, unknownMetadataValue, img.LicenseName)
	assert.Equal(t, int64(1), requests.Load(), "with no pageimage there is no file page to query")
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

	// Settings are nil only before the config loads or when loading failed.
	// Allowing outbound traffic in that window contradicts a configured
	// fallbackpolicy. Asserted through the seam because conf.Setting() lazy-loads
	// from disk, so installing nil in the global does NOT produce a nil read: a
	// subtest that tried would pass on the loaded config's policy instead, and
	// stay green with the guard removed.
	t.Run("no settings fails closed", func(t *testing.T) {
		allowed, reason := wikiFetchAllowedFor(nil)
		assert.False(t, allowed, "an unreadable configuration must not permit outbound fetches")
		assert.Equal(t, reasonSettingsUnavailable, reason)
	})
}

// TestShouldRefreshCacheImpliesFetchAllowed is the invariant that matters between
// the two gates: the hourly refresh must never be enabled while the fetches it
// schedules are denied. It used to be, both through the case-sensitivity
// mismatch and through accepting a provider name as a policy value.
func TestShouldRefreshCacheImpliesFetchAllowed(t *testing.T) {
	// No t.Parallel(): mutates the settings global.
	prev := conf.GetSettings()
	t.Cleanup(func() { conftest.SetTestSettings(prev) })

	lazy := NewLazyWikiMediaProvider()
	refreshEnabled := 0
	for _, provider := range []string{"wikimedia", "avicommons", "auto", "", "WikiMedia"} {
		for _, policy := range []string{"none", "all", "All", "all ", "wikimedia", ""} {
			conftest.SetTestSettings(conftest.NewTestSettings().WithImageProvider(provider, policy).Build())

			if lazy.ShouldRefreshCache() {
				refreshEnabled++
				allowed, _ := wikiFetchAllowed()
				assert.True(t, allowed,
					"refresh enabled but fetch denied for provider=%q policy=%q", provider, policy)
			}
		}
	}
	// Without this the whole matrix passes vacuously if ShouldRefreshCache ever
	// starts returning false everywhere.
	require.Positive(t, refreshEnabled, "the matrix must exercise the refresh-enabled case")
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

	var titles titleRecorder
	provider := newTestWikiProvider(t, func(w http.ResponseWriter, r *http.Request) {
		title := r.URL.Query().Get("titles")
		titles.add(title)
		writeJSON(w, `{"query":{"pages":[{"title":"`+title+`",
			"thumbnail":{"source":"https://example.invalid/goshawk.jpg"}}]}}`)
	})

	img, err := provider.fetchWithLimiter(t.Context(), birdnetName, nil)

	require.NoError(t, err)
	got := titles.snapshot()
	require.NotEmpty(t, got)
	assert.Equal(t, synonymName, got[0], "the synonym is queried first, not as a retry")
	assert.Len(t, got, 1, "a successful synonym lookup must not also query the original name")
	assert.Equal(t, birdnetName, img.ScientificName,
		"the result is still keyed by the name the caller asked for")
}

// TestFetch_FallsBackToTheOriginalNameWhenTheSynonymMisses keeps the inversion
// from costing coverage: the synonym may itself be the stale name.
func TestFetch_FallsBackToTheOriginalNameWhenTheSynonymMisses(t *testing.T) {
	t.Parallel()

	const birdnetName = "Accipiter gentilis"

	var titles titleRecorder
	provider := newTestWikiProvider(t, func(w http.ResponseWriter, r *http.Request) {
		title := r.URL.Query().Get("titles")
		titles.add(title)
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
	got := titles.snapshot()
	require.Len(t, got, 2)
	assert.Equal(t, birdnetName, got[1], "the original name is still tried when the synonym misses")
}

// TestWikiFetchAllowedFor_DecidesFromItsArgument pins the seam's contract.
//
// The fallback-policy half used to be read from the global settings while the
// provider half came from the argument, so a caller passing anything other than
// the global got a verdict spliced from two sources.
func TestWikiFetchAllowedFor_DecidesFromItsArgument(t *testing.T) {
	// No t.Parallel(): installs a global to prove the argument overrides it.
	prev := conf.GetSettings()
	t.Cleanup(func() { conftest.SetTestSettings(prev) })

	// Global says fallback is off; the argument says it is on.
	conftest.SetTestSettings(conftest.NewTestSettings().WithImageProvider("avicommons", "none").Build())
	passed := conftest.NewTestSettings().WithImageProvider("avicommons", "all").Build()

	allowed, _ := wikiFetchAllowedFor(passed)
	assert.True(t, allowed, "the decision must come from the settings passed in, not the global")

	// And the reverse, so the assertion cannot pass by reading the global.
	conftest.SetTestSettings(conftest.NewTestSettings().WithImageProvider("avicommons", "all").Build())
	denied := conftest.NewTestSettings().WithImageProvider("avicommons", "none").Build()

	allowed, reason := wikiFetchAllowedFor(denied)
	assert.False(t, allowed)
	assert.NotEmpty(t, reason)
}

// TestEnsureInitialized_AcquisitionIsCancellable covers the other half of the
// same hazard: a caller with a short deadline must be able to give up rather
// than block on another caller's config wait, which runs for up to 10 seconds.
func TestEnsureInitialized_AcquisitionIsCancellable(t *testing.T) {
	t.Parallel()

	lazy := NewLazyWikiMediaProvider()
	// Hold the slot, as an in-flight initialization would.
	lazy.initSem <- struct{}{}
	t.Cleanup(func() { <-lazy.initSem })

	ctx, cancel := context.WithCancel(t.Context())
	done := make(chan error, 1)
	go func() {
		_, err := lazy.ensureInitialized(ctx)
		done <- err
	}()

	cancel()
	select {
	case err := <-done:
		require.ErrorIs(t, err, context.Canceled,
			"a caller that gives up must not wait out the holder's config wait")
	case <-time.After(2 * time.Second):
		t.Fatal("ensureInitialized blocked on the init slot after its context was cancelled")
	}
}
