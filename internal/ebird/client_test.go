package ebird

import (
	"context"
	"net/http"
	"net/http/httptest"
	"sync"
	"sync/atomic"
	"testing"
	"testing/synctest"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/tphakala/birdnet-go/internal/errors"
)

// concurrencyWaitTimeout bounds how long a test waits for its concurrent
// callers to reach the expected state. It is generous so a loaded CI runner
// under -race does not fail the test; a correct run finishes in milliseconds.
const concurrencyWaitTimeout = time.Minute

func TestNewClient(t *testing.T) {
	tests := []struct {
		name    string
		config  Config
		wantErr bool
		errMsg  string
	}{
		{
			name: "valid config",
			config: Config{
				APIKey: "test-key",
			},
			wantErr: false,
		},
		{
			name: "missing API key",
			config: Config{
				APIKey: "",
			},
			wantErr: true,
			errMsg:  "eBird API key is required",
		},
		{
			name: "config with defaults",
			config: Config{
				APIKey:  "test-key",
				BaseURL: "",
			},
			wantErr: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			disableLogging(t)

			client, err := NewClient(tt.config)

			if tt.wantErr {
				require.Error(t, err)
				assert.Contains(t, err.Error(), tt.errMsg)
				return
			}

			require.NoError(t, err)
			require.NotNil(t, client)

			// Check defaults were applied
			if tt.config.BaseURL == "" {
				assert.Equal(t, DefaultConfig().BaseURL, client.config.BaseURL)
			}
			if tt.config.Timeout == 0 {
				assert.Equal(t, DefaultConfig().Timeout, client.config.Timeout)
			}

			// Cleanup
			client.Close()
		})
	}
}

func TestDoRequest(t *testing.T) {
	// Don't use t.Parallel() - these tests share the mock server

	tests := []struct {
		name         string
		method       string
		path         string
		response     mockResponse
		wantErr      bool
		wantCategory errors.ErrorCategory
	}{
		{
			name:   "successful request",
			method: "GET",
			path:   "/success",
			response: mockResponse{
				status: http.StatusOK,
				body:   `{"result": "ok"}`,
			},
			wantErr: false,
		},
		{
			name:   "authentication failure",
			method: "GET",
			path:   "/auth-fail",
			response: mockResponse{
				status: http.StatusUnauthorized,
				body:   loadTestData(t, "error_401.json"),
			},
			wantErr:      true,
			wantCategory: errors.CategoryConfiguration,
		},
		{
			name:   "CSV response error",
			method: "GET",
			path:   "/csv-response",
			response: mockResponse{
				status:      http.StatusOK,
				contentType: "text/csv;charset=utf-8",
				body:        "SCIENTIFIC_NAME,COMMON_NAME\nTurdus migratorius,American Robin",
			},
			wantErr:      true,
			wantCategory: errors.CategoryNetwork,
		},
		{
			name:   "rate limit error",
			method: "GET",
			path:   "/rate-limit",
			response: mockResponse{
				status: http.StatusTooManyRequests,
				body:   `{"title": "Too Many Requests", "status": 429, "detail": "Rate limit exceeded"}`,
			},
			wantErr:      true,
			wantCategory: errors.CategoryLimit,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			server := setupMockServer(t, map[string]mockResponse{
				tt.path: tt.response,
			})
			defer server.Close()

			client := setupTestClient(t, server)

			var result map[string]any
			err := client.doRequest(t.Context(), tt.method, server.URL+tt.path, nil, &result)

			if tt.wantErr {
				require.Error(t, err)

				if enhancedErr, ok := errors.AsType[*errors.EnhancedError](err); ok {
					assert.Equal(t, tt.wantCategory, enhancedErr.Category)
				}
				return
			}

			require.NoError(t, err)
			assert.NotNil(t, result)
		})
	}
}

func TestGetTaxonomy(t *testing.T) {
	// Don't use t.Parallel() - tests share cache

	taxonomyData := loadTestData(t, "taxonomy.json")
	finnishData := loadTestData(t, "taxonomy_finnish.json")

	server := setupMockServer(t, map[string]mockResponse{
		"/ref/taxonomy/ebird?fmt=json": {
			status: http.StatusOK,
			body:   taxonomyData,
		},
		"/ref/taxonomy/ebird?fmt=json&locale=fi": {
			status: http.StatusOK,
			body:   finnishData,
		},
	})
	defer server.Close()

	client := setupTestClient(t, server)

	// Test 1: First request - cache miss
	t.Run("cache miss", func(t *testing.T) {
		taxonomy, err := client.GetTaxonomy(t.Context(), "")
		require.NoError(t, err)
		assert.Len(t, taxonomy, 3)
		assert.Equal(t, "Turdus migratorius", taxonomy[0].ScientificName)
		assert.Equal(t, "American Robin", taxonomy[0].CommonName)
	})

	// Test 2: Second request - cache hit
	t.Run("cache hit", func(t *testing.T) {
		taxonomy, err := client.GetTaxonomy(t.Context(), "")
		require.NoError(t, err)
		assert.Len(t, taxonomy, 3)
	})

	// Test 3: Different locale - cache miss
	t.Run("locale variation", func(t *testing.T) {
		taxonomy, err := client.GetTaxonomy(t.Context(), "fi")
		require.NoError(t, err)
		assert.Len(t, taxonomy, 2)
		assert.Equal(t, "punarintarastas", taxonomy[0].CommonName)
	})
}

func TestGetSpeciesTaxonomy(t *testing.T) {
	server := setupMockServer(t, map[string]mockResponse{
		"/ref/taxonomy/ebird/amerob?fmt=json": {
			status: http.StatusOK,
			body:   `[{"sciName": "Turdus migratorius", "comName": "American Robin", "speciesCode": "amerob"}]`,
		},
		"/ref/taxonomy/ebird/invalid?fmt=json": {
			status: http.StatusOK,
			body:   `[]`, // Empty array for not found
		},
	})
	defer server.Close()

	client := setupTestClient(t, server)
	disableLogging(t)

	t.Run("existing species", func(t *testing.T) {
		entry, err := client.GetSpeciesTaxonomy(t.Context(), "amerob", "")
		require.NoError(t, err)
		assert.Equal(t, "Turdus migratorius", entry.ScientificName)
		assert.Equal(t, "American Robin", entry.CommonName)
	})

	t.Run("non-existent species", func(t *testing.T) {
		_, err := client.GetSpeciesTaxonomy(t.Context(), "invalid", "")
		require.Error(t, err)
		assert.Contains(t, err.Error(), "species not found")

		if enhancedErr, ok := errors.AsType[*errors.EnhancedError](err); ok {
			assert.Equal(t, errors.CategoryNotFound, enhancedErr.Category)
		}
	})
}

func TestBuildFamilyTree(t *testing.T) {
	taxonomyData := loadTestData(t, "taxonomy.json")

	server := setupMockServer(t, map[string]mockResponse{
		"/ref/taxonomy/ebird?fmt=json": {
			status: http.StatusOK,
			body:   taxonomyData,
		},
	})
	defer server.Close()

	client := setupTestClient(t, server)

	t.Run("valid species", func(t *testing.T) {
		tree, err := client.BuildFamilyTree(t.Context(), "Turdus migratorius")
		require.NoError(t, err)

		assert.Equal(t, "Animalia", tree.Kingdom)
		assert.Equal(t, "Chordata", tree.Phylum)
		assert.Equal(t, "Aves", tree.Class)
		assert.Equal(t, "Passeriformes", tree.Order)
		assert.Equal(t, "Turdidae", tree.Family)
		assert.Equal(t, "Thrushes and Allies", tree.FamilyCommon)
		assert.Equal(t, "Turdus", tree.Genus)
		assert.Equal(t, "Turdus migratorius", tree.Species)
		assert.Equal(t, "American Robin", tree.SpeciesCommon)
		assert.Len(t, tree.Subspecies, 1) // One subspecies in test data
	})

	t.Run("non-existent species", func(t *testing.T) {
		_, err := client.BuildFamilyTree(t.Context(), "Nonexistent species")
		require.Error(t, err)
		assert.Contains(t, err.Error(), "species not found in eBird taxonomy")
	})
}

func TestCaching(t *testing.T) {
	// Track API calls
	callCount := 0
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		callCount++
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(`[{"sciName": "Test species", "comName": "Test", "speciesCode": "test1"}]`))
	}))
	defer server.Close()

	client := setupTestClient(t, server)
	disableLogging(t)

	// First call - should hit API
	_, err := client.GetTaxonomy(t.Context(), "")
	require.NoError(t, err)
	assert.Equal(t, 1, callCount)

	// Second call - should use cache
	_, err = client.GetTaxonomy(t.Context(), "")
	require.NoError(t, err)
	assert.Equal(t, 1, callCount, "Should use cached response")

	// Clear cache and verify
	client.ClearCache()
	_, err = client.GetTaxonomy(t.Context(), "")
	require.NoError(t, err)
	assert.Equal(t, 2, callCount, "Should hit API after cache clear")
}

func TestRateLimiting(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(`{"result": "ok"}`))
	}))
	defer server.Close()

	client := setupTestClient(t, server)
	// Need to stop the existing rate limiter and create a new one
	client.rateLimiter.Stop()
	client.config.RateLimitMS = 100 // 100ms between requests
	client.rateLimiter = time.NewTicker(time.Duration(client.config.RateLimitMS) * time.Millisecond)
	t.Cleanup(func() {
		client.rateLimiter.Stop()
	})
	disableLogging(t)

	start := time.Now()

	// Make 3 requests
	for range 3 {
		err := client.doRequest(t.Context(), "GET", server.URL+"/test", nil, nil)
		require.NoError(t, err)
	}

	elapsed := time.Since(start)
	assert.GreaterOrEqual(t, elapsed, 200*time.Millisecond, "Should enforce rate limit")
}

func TestAuthenticationLogging(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		if r.Header.Get("X-eBirdApiToken") == "" {
			w.WriteHeader(http.StatusUnauthorized)
			_, _ = w.Write([]byte(`{"title": "Unauthorized", "status": 401, "detail": "Missing API key"}`))
			return
		}
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(`[{"sciName": "Test"}]`))
	}))
	defer server.Close()

	t.Run("successful authentication", func(t *testing.T) {
		client := setupTestClient(t, server)

		// First successful request should complete without error
		_, err := client.GetTaxonomy(t.Context(), "")
		require.NoError(t, err)
	})

	t.Run("failed authentication", func(t *testing.T) {
		client := setupTestClient(t, server)
		client.config.APIKey = "" // Remove API key

		_, err := client.GetTaxonomy(t.Context(), "")
		require.Error(t, err)
	})
}

func TestParseErrors(t *testing.T) {
	tests := []struct {
		name        string
		response    string
		contentType string
		wantErr     string
	}{
		{
			name:        "invalid JSON",
			response:    `{invalid json`,
			contentType: "application/json",
			wantErr:     "failed to parse response",
		},
		{
			name:        "CSV instead of JSON",
			response:    "SCIENTIFIC_NAME,COMMON_NAME",
			contentType: "text/csv",
			wantErr:     "eBird API returned non-JSON response",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			server := setupMockServer(t, map[string]mockResponse{
				"/test": {
					status:      http.StatusOK,
					body:        tt.response,
					contentType: tt.contentType,
				},
			})
			defer server.Close()

			client := setupTestClient(t, server)

			var result any
			err := client.doRequest(t.Context(), "GET", server.URL+"/test", nil, &result)
			require.Error(t, err)
			assert.Contains(t, err.Error(), tt.wantErr)
		})
	}
}

func TestCacheStats(t *testing.T) {
	server := setupMockServer(t, map[string]mockResponse{
		"/ref/taxonomy/ebird?fmt=json": {
			status: http.StatusOK,
			body:   `[{"sciName": "Test species", "comName": "Test", "speciesCode": "test1"}]`,
		},
		"/ref/taxonomy/ebird/x?fmt=json": {
			status: http.StatusOK,
			body:   `[{"sciName": "Test species", "comName": "Test", "speciesCode": "x"}]`,
		},
		"/v2/data/obs/geo/recent?lat=60.1700&lng=24.9400&back=14&maxResults=200": {
			status: http.StatusOK,
			body:   `[]`,
		},
	})
	defer server.Close()

	client := setupTestClient(t, server)
	disableLogging(t)

	// Initially empty
	count, size := client.GetCacheStats()
	assert.Equal(t, 0, count)
	assert.Equal(t, int64(0), size)

	_, err := client.GetTaxonomy(t.Context(), "")
	require.NoError(t, err)
	_, err = client.GetSpeciesTaxonomy(t.Context(), "x", "")
	require.NoError(t, err)
	_, err = client.BuildFamilyTree(t.Context(), "Test species")
	require.NoError(t, err)
	_, err = client.GetRecentObservations(t.Context(), 60.17, 24.94, 14)
	require.NoError(t, err)

	// One item in each of the four caches.
	count, _ = client.GetCacheStats()
	assert.Equal(t, 4, count)

	client.ClearCache()
	count, _ = client.GetCacheStats()
	assert.Equal(t, 0, count)
}

func TestGetTaxonomy_ConcurrentMissesFetchOnce(t *testing.T) {
	const callers = 8
	var requests atomic.Int32
	release := make(chan struct{})
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requests.Add(1)
		<-release
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`[{"sciName": "Test species", "comName": "Test", "speciesCode": "test1"}]`))
	}))
	defer server.Close()
	// Registered after server.Close so it runs first: a failed wait below must
	// not leave the handlers parked, because Close waits for them.
	var releaseOnce sync.Once
	defer releaseOnce.Do(func() { close(release) })

	client := setupTestClient(t, server)

	var wg sync.WaitGroup
	errs := make([]error, callers)
	lens := make([]int, callers)
	for i := range callers {
		wg.Go(func() {
			tax, err := client.GetTaxonomy(t.Context(), "")
			errs[i], lens[i] = err, len(tax)
		})
	}
	// Wait until the one fetch is in flight, give the other callers time to
	// miss and join it, then release it.
	require.Eventually(t, func() bool { return requests.Load() == 1 }, concurrencyWaitTimeout, time.Millisecond)
	require.Eventually(t, func() bool {
		return client.taxonomy.Stats().Misses == callers
	}, concurrencyWaitTimeout, time.Millisecond)
	releaseOnce.Do(func() { close(release) })
	wg.Wait()

	for i := range callers {
		require.NoError(t, errs[i])
		assert.Equal(t, 1, lens[i])
	}
	assert.Equal(t, int32(1), requests.Load())
}

func TestGetTaxonomy_CallerCancelDoesNotAbortSharedFetch(t *testing.T) {
	var requests atomic.Int32
	release := make(chan struct{})
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requests.Add(1)
		<-release
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`[{"sciName": "Test species", "comName": "Test", "speciesCode": "test1"}]`))
	}))
	defer server.Close()
	// Registered after server.Close so it runs first (see the test above).
	var releaseOnce sync.Once
	defer releaseOnce.Do(func() { close(release) })

	client := setupTestClient(t, server)

	ctxA, cancelA := context.WithCancel(t.Context())
	errA := make(chan error, 1)
	go func() {
		_, err := client.GetTaxonomy(ctxA, "")
		errA <- err
	}()
	require.Eventually(t, func() bool { return requests.Load() == 1 }, concurrencyWaitTimeout, time.Millisecond)

	type result struct {
		tax []TaxonomyEntry
		err error
	}
	resB := make(chan result, 1)
	go func() {
		tax, err := client.GetTaxonomy(t.Context(), "")
		resB <- result{tax, err}
	}()
	require.Eventually(t, func() bool { return client.taxonomy.Stats().Misses == 2 }, concurrencyWaitTimeout, time.Millisecond)

	cancelA()
	select {
	case err := <-errA:
		require.ErrorIs(t, err, context.Canceled)
	case <-time.After(concurrencyWaitTimeout): // only reached when the caller waits for the shared fetch
		require.FailNow(t, "the cancelled caller did not return while the shared fetch was running")
	}

	releaseOnce.Do(func() { close(release) })
	r := <-resB
	require.NoError(t, r.err)
	assert.Len(t, r.tax, 1)
	assert.Equal(t, int32(1), requests.Load(), "the abandoned caller did not abort or repeat the fetch")
}

func TestMetrics(t *testing.T) {
	server := setupMockServer(t, map[string]mockResponse{
		"/ref/taxonomy/ebird?fmt=json": {
			status: http.StatusOK,
			body:   `[{"sciName": "Test species", "comName": "Test", "speciesCode": "test1"}]`,
		},
		"/error": {
			status: http.StatusInternalServerError,
			body:   `{"error": "Internal Server Error"}`,
		},
	})
	defer server.Close()

	client := setupTestClient(t, server)
	disableLogging(t)

	// Initial metrics should be zero
	metrics := client.GetMetrics()
	assert.Equal(t, int64(0), metrics.APICalls)
	assert.Equal(t, int64(0), metrics.CacheHits)
	assert.Equal(t, int64(0), metrics.CacheMisses)
	assert.Equal(t, int64(0), metrics.APIErrors)

	// Make a successful API call (cache miss)
	_, err := client.GetTaxonomy(t.Context(), "")
	require.NoError(t, err)

	metrics = client.GetMetrics()
	assert.Equal(t, int64(1), metrics.APICalls)
	assert.Equal(t, int64(0), metrics.CacheHits)
	assert.Equal(t, int64(1), metrics.CacheMisses)
	assert.Equal(t, int64(0), metrics.APIErrors)
	assert.Greater(t, metrics.TotalDuration, time.Duration(0))
	assert.Greater(t, metrics.AvgDuration, time.Duration(0))

	// Make another call (cache hit)
	_, err = client.GetTaxonomy(t.Context(), "")
	require.NoError(t, err)

	metrics = client.GetMetrics()
	assert.Equal(t, int64(1), metrics.APICalls, "API calls should not increase for cache hit")
	assert.Equal(t, int64(1), metrics.CacheHits)
	assert.Equal(t, int64(1), metrics.CacheMisses)

	// Make an error call
	_ = client.doRequestWithRetry(t.Context(), "GET", server.URL+"/error", nil, nil)

	metrics = client.GetMetrics()
	assert.Equal(t, int64(4), metrics.APICalls, "Should count all retry attempts")
	assert.Equal(t, int64(3), metrics.APIErrors, "Should count all error responses")
}

// countingServer serves fixed JSON bodies by path and query and counts the
// requests made for each.
type countingServer struct {
	*httptest.Server
	mu       sync.Mutex
	requests map[string]int
}

func newCountingServer(t *testing.T, bodies map[string]string) *countingServer {
	t.Helper()
	cs := &countingServer{requests: make(map[string]int)}
	cs.Server = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		key := r.URL.Path
		if r.URL.RawQuery != "" {
			key += "?" + r.URL.RawQuery
		}
		cs.mu.Lock()
		cs.requests[key]++
		cs.mu.Unlock()
		body, ok := bodies[key]
		if !ok {
			w.WriteHeader(http.StatusNotFound)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(body))
	}))
	t.Cleanup(cs.Close)
	return cs
}

func (cs *countingServer) count(key string) int {
	cs.mu.Lock()
	defer cs.mu.Unlock()
	return cs.requests[key]
}

const (
	testTaxonomyPath   = "/ref/taxonomy/ebird?fmt=json"
	testSpeciesPath    = "/ref/taxonomy/ebird/x?fmt=json"
	testSpeciesPathFi  = "/ref/taxonomy/ebird/x?fmt=json&locale=fi"
	testObservationURL = "/v2/data/obs/geo/recent?lat=60.1700&lng=24.9400&back=14&maxResults=200"
	testTaxonomyBody   = `[{"sciName": "Test species", "comName": "Test", "speciesCode": "test1", "familySciName": "Testidae"}]`
)

// TestCachedLookups_SpeciesAndFamilyTree pins that repeated species and family
// tree lookups are served from their caches, and that the species cache keeps
// one entry per locale.
func TestCachedLookups_SpeciesAndFamilyTree(t *testing.T) {
	server := newCountingServer(t, map[string]string{
		testTaxonomyPath:  testTaxonomyBody,
		testSpeciesPath:   `[{"sciName": "Test species", "comName": "Test", "speciesCode": "x"}]`,
		testSpeciesPathFi: `[{"sciName": "Test species", "comName": "Testi", "speciesCode": "x"}]`,
	})
	client := setupTestClient(t, server.Server)
	disableLogging(t)
	ctx := t.Context()

	for range 2 {
		entry, err := client.GetSpeciesTaxonomy(ctx, "x", "")
		require.NoError(t, err)
		assert.Equal(t, "Test", entry.CommonName)
		entryFi, err := client.GetSpeciesTaxonomy(ctx, "x", "fi")
		require.NoError(t, err)
		assert.Equal(t, "Testi", entryFi.CommonName, "each locale gets its own entry")
	}
	assert.Equal(t, 1, server.count(testSpeciesPath), "the default locale is fetched once")
	assert.Equal(t, 1, server.count(testSpeciesPathFi), "the fi locale is fetched once")

	first, err := client.BuildFamilyTree(ctx, "Test species")
	require.NoError(t, err)
	second, err := client.BuildFamilyTree(ctx, "Test species")
	require.NoError(t, err)
	assert.Same(t, first, second, "the second tree comes from the family tree cache")
	assert.Equal(t, uint64(1), client.familyTrees.Stats().Hits)
	assert.Equal(t, uint64(1), client.familyTrees.Stats().Misses)
}

// TestMetrics_SumsEveryCache pins that GetMetrics reports the hits and misses
// of all four caches, not only the taxonomy cache.
func TestMetrics_SumsEveryCache(t *testing.T) {
	server := newCountingServer(t, map[string]string{
		testTaxonomyPath:   testTaxonomyBody,
		testSpeciesPath:    `[{"sciName": "Test species", "comName": "Test", "speciesCode": "x"}]`,
		testObservationURL: `[]`,
	})
	client := setupTestClient(t, server.Server)
	disableLogging(t)
	ctx := t.Context()

	for range 2 {
		_, err := client.GetTaxonomy(ctx, "")
		require.NoError(t, err)
		_, err = client.GetSpeciesTaxonomy(ctx, "x", "")
		require.NoError(t, err)
		_, err = client.BuildFamilyTree(ctx, "Test species")
		require.NoError(t, err)
		_, err = client.GetRecentObservations(ctx, 60.17, 24.94, 14)
		require.NoError(t, err)
	}

	// Each cache misses once and then hits once. The first family tree load
	// also reads the taxonomy, which is a second taxonomy hit.
	metrics := client.GetMetrics()
	assert.Equal(t, int64(4), metrics.CacheMisses)
	assert.Equal(t, int64(5), metrics.CacheHits)
}

// TestClose_RateLimitWaitEndsWithRequestTimeout pins that a fetch waiting for
// the rate limiter after Close (which stops the ticker) gives up when its
// request timeout expires instead of blocking forever. The fetch runs as a
// detached cache load, so nothing else would release it.
func TestClose_RateLimitWaitEndsWithRequestTimeout(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		client, err := NewClient(Config{
			APIKey:      "test-key",
			BaseURL:     "http://127.0.0.1:1", // never contacted
			Timeout:     time.Second,
			CacheTTL:    time.Hour,
			RateLimitMS: 10,
		})
		require.NoError(t, err)
		disableLogging(t)
		client.Close()

		_, err = client.GetTaxonomy(t.Context(), "")
		require.Error(t, err)
		assert.ErrorIs(t, err, context.DeadlineExceeded)
	})
}
