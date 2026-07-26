package imageprovider

import (
	"context"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"sync/atomic"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/tphakala/birdnet-go/internal/branding"
)

func TestImageFileCache_StoreAndGet(t *testing.T) {
	t.Parallel()

	cache := NewImageFileCache(filepath.Join(t.TempDir(), "cache"))

	// Minimal valid JPEG: FFD8FF header triggers image/jpeg detection.
	jpegData := []byte{0xFF, 0xD8, 0xFF, 0xE0, 0x00, 0x10, 0x4A, 0x46, 0x49, 0x46}

	storedPath, storeCT, err := cache.Store("wikimedia", "Parus major", jpegData, "https://127.0.0.1/img.jpg", "image/jpeg")
	require.NoError(t, err)
	assert.Contains(t, storedPath, "parus_major.jpg")
	assert.Equal(t, "image/jpeg", storeCT)

	// Read back and verify contents.
	got, err := os.ReadFile(storedPath)
	require.NoError(t, err)
	assert.Equal(t, jpegData, got)

	// Get should find the cached file.
	path, contentType, fresh, err := cache.Get("wikimedia", "Parus major")
	require.NoError(t, err)
	assert.Equal(t, storedPath, path)
	assert.Equal(t, "image/jpeg", contentType)
	assert.True(t, fresh, "newly stored file should be fresh")
}

func TestImageFileCache_GetMiss(t *testing.T) {
	t.Parallel()

	cache := NewImageFileCache(filepath.Join(t.TempDir(), "cache"))

	path, contentType, fresh, err := cache.Get("wikimedia", "Nonexistent species")
	require.NoError(t, err)
	assert.Empty(t, path)
	assert.Empty(t, contentType)
	assert.False(t, fresh)
}

func TestImageFileCache_IsFresh(t *testing.T) {
	t.Parallel()

	cache := NewImageFileCache(filepath.Join(t.TempDir(), "cache"))

	jpegData := []byte{0xFF, 0xD8, 0xFF, 0xE0}
	storedPath, _, err := cache.Store("test", "Turdus merula", jpegData, "", "")
	require.NoError(t, err)

	// With a 30-day TTL the file should be fresh.
	assert.True(t, cache.IsFresh(storedPath, 30*24*time.Hour))

	// With a zero TTL the file should be stale.
	assert.False(t, cache.IsFresh(storedPath, 0))
}

func TestImageFileCache_NormalizeName(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name     string
		input    string
		expected string
	}{
		{name: "simple lowercase", input: "parus major", expected: "parus_major"},
		{name: "mixed case", input: "Parus Major", expected: "parus_major"},
		{name: "all caps", input: "PARUS MAJOR", expected: "parus_major"},
		{name: "multiple spaces", input: "Parus  major", expected: "parus__major"},
		{name: "no spaces", input: "turdus", expected: "turdus"},
		{name: "already normalized", input: "parus_major", expected: "parus_major"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			assert.Equal(t, tt.expected, normalizeSpeciesName(tt.input))
		})
	}
}

func TestImageFileCache_RejectsPathTraversal(t *testing.T) {
	t.Parallel()

	cache := NewImageFileCache(filepath.Join(t.TempDir(), "cache"))
	data := []byte{0xFF, 0xD8, 0xFF}

	tests := []struct {
		name     string
		provider string
		species  string
	}{
		{name: "dotdot in provider", provider: "../etc", species: "Parus major"},
		{name: "slash in provider", provider: "wiki/evil", species: "Parus major"},
		{name: "backslash in provider", provider: "wiki\\evil", species: "Parus major"},
		{name: "dotdot in species", provider: "wikimedia", species: "../../../etc/passwd"},
		{name: "slash in species", provider: "wikimedia", species: "evil/path"},
		{name: "backslash in species", provider: "wikimedia", species: "evil\\path"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			_, _, err := cache.Store(tt.provider, tt.species, data, "", "")
			require.Error(t, err, "Store should reject path traversal")

			_, _, _, err = cache.Get(tt.provider, tt.species)
			require.Error(t, err, "Get should reject path traversal")
		})
	}
}

// TestImageFileCache_DownloadAndStore_SSRFGuardRejectsLoopback asserts that the
// production HTTP client refuses a loopback target.
//
// This was named _InvalidURL, but "http://[::1]:0/img.jpg" parses fine; what rejects
// it is imageHTTPClient's DialContext SSRF guard, before a wire request is ever built.
// The old name and its bare assert.Error meant the test passed for any cause at all and
// left the status check, size cap, content-type read and Store handoff untested. Those
// are covered by the newServedCache tests below, which inject a client that can reach
// an httptest server.
func TestImageFileCache_DownloadAndStore_SSRFGuardRejectsLoopback(t *testing.T) {
	t.Parallel()
	cache := NewImageFileCache(t.TempDir())
	_, _, err := cache.DownloadAndStore(t.Context(), "avicommons", "Test species", "http://[::1]:0/img.jpg")
	require.Error(t, err)
	assert.Contains(t, err.Error(), "no safe IP addresses",
		"the SSRF dialer, not URL parsing, should be what rejects a loopback target")
}

// pngBytes is a minimal PNG signature, enough for http.DetectContentType to sniff.
var pngBytes = []byte{0x89, 0x50, 0x4E, 0x47, 0x0D, 0x0A, 0x1A, 0x0A, 0x00, 0x00, 0x00, 0x0D, 0x49, 0x48, 0x44, 0x52}

// newServedCache starts an httptest server running handler and returns a file cache
// wired to reach it. The production client's SSRF dialer rejects the server's loopback
// address, so the injected client is what makes DownloadAndStore testable at all.
func newServedCache(t *testing.T, handler http.HandlerFunc) (*ImageFileCache, *httptest.Server) {
	t.Helper()
	server := httptest.NewServer(handler)
	t.Cleanup(server.Close)

	cache := NewImageFileCache(filepath.Join(t.TempDir(), "cache"))
	cache.httpClient = server.Client()
	return cache, server
}

// TestDownloadAndStore_SendsUserAgent is the regression test for the Wikimedia
// User-Agent policy violation: DownloadAndStore built its request with no headers at
// all, so Go sent "Go-http-client/1.1" and upload.wikimedia.org answered 403 for every
// species, forever. This test fails without the fix.
func TestDownloadAndStore_SendsUserAgent(t *testing.T) {
	t.Parallel()

	// Buffered channel rather than a shared variable: the handler runs on the server's
	// goroutine, and a channel gives the read a happens-before edge without relying on
	// net/http incidentally providing one.
	uaCh := make(chan string, 1)
	cache, server := newServedCache(t, func(w http.ResponseWriter, r *http.Request) {
		select {
		case uaCh <- r.Header.Get("User-Agent"):
		default:
		}
		w.Header().Set("Content-Type", "image/jpeg")
		_, _ = w.Write([]byte{0xFF, 0xD8, 0xFF, 0xE0})
	})

	_, _, err := cache.DownloadAndStore(t.Context(), "wikimedia", "Delichon urbicum", server.URL+"/img.jpg")
	require.NoError(t, err)

	var gotUA string
	select {
	case gotUA = <-uaCh:
	default:
		t.Fatal("handler never observed a request")
	}
	require.NotEmpty(t, gotUA, "image download must send a User-Agent")
	assert.NotContains(t, gotUA, "Go-http-client",
		"Go's default User-Agent is rejected with 403 by upload.wikimedia.org")
	assert.Contains(t, gotUA, userAgentName+"/", "User-Agent must carry the client name and a version token")
	assert.Contains(t, gotUA, branding.RepoURL(), "Wikimedia policy requires contact information")

	// Assert against the shared builder rather than re-spelling the string, so the API
	// path and the image path cannot drift apart.
	assert.Equal(t, buildUserAgent(currentAppVersion()), gotUA,
		"image download must send the same User-Agent as the Wikipedia API path")
}

// TestDownloadAndStore_403DoesNotPopulateCache asserts the cascade described in the bug
// report: a rejected download must leave the file cache empty, which is what makes
// ServeSpeciesImageProxy fall through to a 302 to the external URL on every request.
func TestDownloadAndStore_403DoesNotPopulateCache(t *testing.T) {
	t.Parallel()

	cache, server := newServedCache(t, func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusForbidden)
	})

	_, _, err := cache.DownloadAndStore(t.Context(), "wikimedia", "Delichon urbicum", server.URL+"/img.jpg")
	require.Error(t, err)
	assert.Contains(t, err.Error(), "403")

	path, contentType, fresh, getErr := cache.Get("wikimedia", "Delichon urbicum")
	require.NoError(t, getErr, "a cache miss is not an error")
	assert.Empty(t, path, "a failed download must not leave a cached file behind")
	assert.Empty(t, contentType)
	assert.False(t, fresh)
}

func TestDownloadAndStore_ErrorResponses(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name       string
		status     int
		wantErrSub string
	}{
		{name: "forbidden", status: http.StatusForbidden, wantErrSub: "403"},
		{name: "rate limited", status: http.StatusTooManyRequests, wantErrSub: "429"},
		{name: "server error", status: http.StatusInternalServerError, wantErrSub: "500"},
		{name: "not found", status: http.StatusNotFound, wantErrSub: "404"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			cache, server := newServedCache(t, func(w http.ResponseWriter, _ *http.Request) {
				w.WriteHeader(tt.status)
			})

			_, _, err := cache.DownloadAndStore(t.Context(), "wikimedia", "Parus major", server.URL+"/img.jpg")
			require.Error(t, err)
			assert.Contains(t, err.Error(), tt.wantErrSub)

			path, _, _, getErr := cache.Get("wikimedia", "Parus major")
			require.NoError(t, getErr)
			assert.Empty(t, path, "non-200 response must not populate the cache")
		})
	}
}

func TestDownloadAndStore_CancelledContext(t *testing.T) {
	t.Parallel()

	released := make(chan struct{})
	cache, server := newServedCache(t, func(w http.ResponseWriter, _ *http.Request) {
		<-released
		w.WriteHeader(http.StatusOK)
	})
	t.Cleanup(func() { close(released) })

	ctx, cancel := context.WithCancel(t.Context())
	cancel()

	_, _, err := cache.DownloadAndStore(ctx, "wikimedia", "Parus major", server.URL+"/img.jpg")
	require.Error(t, err)
	assert.ErrorIs(t, err, context.Canceled)
}

// TestDownloadAndStore_ContentTypes covers the extension mapping through the real
// download path, including the empty-Content-Type case that forces the
// http.DetectContentType sniff.
func TestDownloadAndStore_ContentTypes(t *testing.T) {
	t.Parallel()

	const svg = `<svg xmlns="http://www.w3.org/2000/svg" width="1" height="1"></svg>`

	tests := []struct {
		name            string
		upstreamCT      string
		body            []byte
		wantExt         string
		wantContentType string
	}{
		{
			name:            "png",
			upstreamCT:      "image/png",
			body:            pngBytes,
			wantExt:         ".png",
			wantContentType: "image/png",
		},
		{
			name:            "webp",
			upstreamCT:      "image/webp",
			body:            []byte("RIFF\x00\x00\x00\x00WEBPVP8 "),
			wantExt:         ".webp",
			wantContentType: "image/webp",
		},
		{
			name:            "svg",
			upstreamCT:      "image/svg+xml",
			body:            []byte(svg),
			wantExt:         ".svg",
			wantContentType: "image/svg+xml",
		},
		{
			name:            "gif",
			upstreamCT:      "image/gif",
			body:            []byte("GIF89a"),
			wantExt:         ".gif",
			wantContentType: "image/gif",
		},
		{
			name: "empty content type falls back to sniffing",
			// No Content-Type header: Store must sniff the bytes instead.
			upstreamCT:      "",
			body:            pngBytes,
			wantExt:         ".png",
			wantContentType: "image/png",
		},
		{
			name:            "unknown content type defaults to jpeg",
			upstreamCT:      "application/octet-stream",
			body:            []byte{0xFF, 0xD8, 0xFF, 0xE0},
			wantExt:         ".jpg",
			wantContentType: "image/jpeg",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			cache, server := newServedCache(t, func(w http.ResponseWriter, _ *http.Request) {
				if tt.upstreamCT != "" {
					w.Header().Set("Content-Type", tt.upstreamCT)
				} else {
					// Go sniffs and sets Content-Type on write unless it is suppressed.
					w.Header()["Content-Type"] = nil
				}
				_, _ = w.Write(tt.body)
			})

			path, contentType, err := cache.DownloadAndStore(t.Context(), "wikimedia", "Parus major", server.URL+"/img")
			require.NoError(t, err)
			assert.Equal(t, tt.wantExt, filepath.Ext(path), "cached file extension")
			assert.Equal(t, tt.wantContentType, contentType)

			stored, readErr := os.ReadFile(path)
			require.NoError(t, readErr)
			assert.Equal(t, tt.body, stored, "cached bytes must match what was served")
		})
	}
}

func TestDownloadAndStore_RejectsOversizeBody(t *testing.T) {
	t.Parallel()

	cache, server := newServedCache(t, func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "image/jpeg")
		// One byte over the cap, so the limit itself is what rejects it.
		_, _ = w.Write(make([]byte, maxImageSize+1))
	})

	_, _, err := cache.DownloadAndStore(t.Context(), "wikimedia", "Parus major", server.URL+"/img.jpg")
	require.Error(t, err)
	assert.Contains(t, err.Error(), "exceeds maximum size")

	path, _, _, getErr := cache.Get("wikimedia", "Parus major")
	require.NoError(t, getErr)
	assert.Empty(t, path, "an oversize download must not write anything to disk")
}

func TestImageFileCache_DetectsContentType(t *testing.T) {
	t.Parallel()

	cache := NewImageFileCache(filepath.Join(t.TempDir(), "cache"))

	storedPath, storedCT, err := cache.Store("wikimedia", "Cyanistes caeruleus", pngBytes, "https://127.0.0.1/img.png", "")
	require.NoError(t, err)
	assert.Equal(t, ".png", filepath.Ext(storedPath), "expected .png extension")
	assert.Equal(t, "image/png", storedCT)

	path, contentType, _, err := cache.Get("wikimedia", "Cyanistes caeruleus")
	require.NoError(t, err)
	assert.NotEmpty(t, path)
	assert.Equal(t, "image/png", contentType)
}

// TestDownloadAndStore_PermanentRejectionLatch asserts the escalation bookkeeping for a
// permanent host rejection.
//
// Without this, deleting the logPermanentImageRejection call site entirely left the whole
// package green: the 403 tests execute that function incidentally, so it reported 100%
// coverage while no assertion depended on it. Coverage proves a line ran, never that a
// regression would fail a test.
func TestDownloadAndStore_PermanentRejectionLatch(t *testing.T) {
	t.Parallel()

	// status is switched per request so one cache can be walked through
	// reject -> reject-again -> succeed.
	var status atomic.Int64
	status.Store(http.StatusForbidden)

	cache, server := newServedCache(t, func(w http.ResponseWriter, _ *http.Request) {
		code := int(status.Load())
		if code != http.StatusOK {
			w.WriteHeader(code)
			return
		}
		w.Header().Set("Content-Type", "image/jpeg")
		_, _ = w.Write([]byte{0xFF, 0xD8, 0xFF, 0xE0})
	})

	require.False(t, cache.rejectionLogged.Load(), "latch should start clear")

	// A 403 latches the escalation.
	_, _, err := cache.DownloadAndStore(t.Context(), "wikimedia", "Rejected one", server.URL+"/a.jpg")
	require.Error(t, err)
	assert.True(t, cache.rejectionLogged.Load(), "a 403 must latch the escalated log")

	// A second rejection must not re-escalate, which is what keeps the log quiet.
	// The cooldown armed by the first 403 is lifted first: this test is about the log
	// latch, and leaving the cooldown open would short-circuit the request before it
	// could reach the logging path at all.
	cache.blockedUntil.Delete("wikimedia")
	_, _, err = cache.DownloadAndStore(t.Context(), "wikimedia", "Rejected two", server.URL+"/b.jpg")
	require.Error(t, err)
	assert.True(t, cache.rejectionLogged.Load(), "latch stays set while rejections continue")

	// A success clears it, so a later block is visible again rather than silent forever.
	cache.blockedUntil.Delete("wikimedia")
	status.Store(http.StatusOK)
	_, _, err = cache.DownloadAndStore(t.Context(), "wikimedia", "Recovered", server.URL+"/c.jpg")
	require.NoError(t, err)
	assert.False(t, cache.rejectionLogged.Load(),
		"a successful download must re-arm the escalation, mirroring resetCircuit")
}

// TestDownloadAndStore_RefusalOpensCooldown covers the breaker the byte-download path
// never had.
//
// The MediaWiki API path opens a circuit on a policy rejection, but the image download
// path retried on every request forever: with the proxy re-attempting a download for
// any species whose file is missing, a blanket 403 meant one guaranteed-failing
// outbound request per uncached species on every page load, indefinitely. That is the
// traffic profile that earns a block in the first place.
func TestDownloadAndStore_RefusalOpensCooldown(t *testing.T) {
	t.Parallel()

	for _, status := range []int{http.StatusUnauthorized, http.StatusForbidden, http.StatusTooManyRequests} {
		t.Run(http.StatusText(status), func(t *testing.T) {
			t.Parallel()

			var requests atomic.Int64
			cache, server := newServedCache(t, func(w http.ResponseWriter, _ *http.Request) {
				requests.Add(1)
				w.WriteHeader(status)
			})

			_, _, err := cache.DownloadAndStore(t.Context(), "wikimedia", "Parus major", server.URL+"/a.jpg")
			require.Error(t, err)
			require.Equal(t, int64(1), requests.Load())

			// A different species: the refusal is about the host, not the file, so this
			// must not reach the network at all.
			_, _, err = cache.DownloadAndStore(t.Context(), "wikimedia", "Turdus merula", server.URL+"/b.jpg")
			require.ErrorIs(t, err, ErrImageDownloadBlocked)
			assert.Equal(t, int64(1), requests.Load(),
				"a refused host must not be contacted again during the cooldown")

			// The cooldown is per provider, so an unrelated provider keeps working.
			_, _, err = cache.DownloadAndStore(t.Context(), "avicommons", "Turdus merula", server.URL+"/c.jpg")
			require.NotErrorIs(t, err, ErrImageDownloadBlocked)
			assert.Equal(t, int64(2), requests.Load())
		})
	}
}

// TestDownloadAndStore_SuccessClearsCooldown asserts recovery does not have to wait out
// the full cooldown once the host is serving us again.
func TestDownloadAndStore_SuccessClearsCooldown(t *testing.T) {
	t.Parallel()

	var status atomic.Int64
	status.Store(http.StatusForbidden)
	cache, server := newServedCache(t, func(w http.ResponseWriter, _ *http.Request) {
		if code := int(status.Load()); code != http.StatusOK {
			w.WriteHeader(code)
			return
		}
		w.Header().Set("Content-Type", "image/jpeg")
		_, _ = w.Write([]byte{0xFF, 0xD8, 0xFF, 0xE0})
	})

	_, _, err := cache.DownloadAndStore(t.Context(), "wikimedia", "Parus major", server.URL+"/a.jpg")
	require.Error(t, err)
	_, open := cache.downloadBlockedUntil("wikimedia")
	require.True(t, open)

	// Simulate the cooldown elapsing rather than sleeping for ten minutes.
	cache.blockedUntil.Store("wikimedia", time.Now().Add(-time.Second))
	_, open = cache.downloadBlockedUntil("wikimedia")
	require.False(t, open, "an elapsed deadline must not count as an open cooldown")

	status.Store(http.StatusOK)
	_, _, err = cache.DownloadAndStore(t.Context(), "wikimedia", "Turdus merula", server.URL+"/b.jpg")
	require.NoError(t, err)

	_, open = cache.downloadBlockedUntil("wikimedia")
	assert.False(t, open, "a successful download must clear the cooldown")
}

// TestDownloadAndStore_NotFoundDoesNotOpenCooldown keeps the breaker scoped to host
// refusals. A 404 is about one image; suppressing every other species because one file
// moved would be a far worse failure than the one being prevented.
func TestDownloadAndStore_NotFoundDoesNotOpenCooldown(t *testing.T) {
	t.Parallel()

	cache, server := newServedCache(t, func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusNotFound)
	})

	_, _, err := cache.DownloadAndStore(t.Context(), "wikimedia", "Parus major", server.URL+"/a.jpg")
	require.Error(t, err)
	_, open := cache.downloadBlockedUntil("wikimedia")
	assert.False(t, open)
}

// TestDownloadAndStore_TransientStatusDoesNotLatch asserts the escalation is reserved for
// policy rejections, so ordinary upstream failures stay at the quiet log level.
func TestDownloadAndStore_TransientStatusDoesNotLatch(t *testing.T) {
	t.Parallel()

	for _, status := range []int{http.StatusNotFound, http.StatusTooManyRequests, http.StatusInternalServerError} {
		t.Run(http.StatusText(status), func(t *testing.T) {
			t.Parallel()
			cache, server := newServedCache(t, func(w http.ResponseWriter, _ *http.Request) {
				w.WriteHeader(status)
			})

			_, _, err := cache.DownloadAndStore(t.Context(), "wikimedia", "Parus major", server.URL+"/img.jpg")
			require.Error(t, err)
			assert.False(t, cache.rejectionLogged.Load(),
				"HTTP %d is transient and must not escalate as a permanent rejection", status)
		})
	}
}
