package classifier

import (
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"slices"
	"sync"
	"sync/atomic"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// fakeEndpointResolver drives the download failover loop with an explicit chain
// and records which endpoints were reported working.
type fakeEndpointResolver struct {
	endpoints []string
	mu        sync.Mutex
	noted     []string
}

func (f *fakeEndpointResolver) OrderedEndpoints(string) []string { return f.endpoints }

func (f *fakeEndpointResolver) NoteWorking(endpoint string) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.noted = append(f.noted, endpoint)
}

func (f *fakeEndpointResolver) working() []string {
	f.mu.Lock()
	defer f.mu.Unlock()
	return slices.Clone(f.noted)
}

// countingServer serves fixed content with a fixed status and counts requests.
func countingServer(t *testing.T, status int, body []byte) (url string, hits *atomic.Int64) {
	t.Helper()
	hits = &atomic.Int64{}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		hits.Add(1)
		if status != http.StatusOK {
			w.WriteHeader(status)
			return
		}
		_, _ = w.Write(body)
	}))
	t.Cleanup(srv.Close)
	return srv.URL, hits
}

// unreachableURL returns a URL whose server is already closed, so a connection
// to it is refused (a transport-level failure).
func unreachableURL(t *testing.T) string {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(http.ResponseWriter, *http.Request) {}))
	url := srv.URL
	srv.Close()
	return url
}

func newFailoverManager(t *testing.T, endpoints []string) (*ModelManager, *fakeEndpointResolver) {
	t.Helper()
	mm := NewModelManager(t.TempDir(), nil, nil)
	mm.downloading["m"] = &DownloadState{CatalogID: "m", Status: StatusDownloading}
	resolver := &fakeEndpointResolver{endpoints: endpoints}
	mm.SetEndpointResolver(resolver)
	return mm, resolver
}

func TestDownloadModelFile_FailsOverOnUnreachableHost(t *testing.T) {
	t.Parallel()
	body := []byte("model-bytes")
	mirrorURL, mirrorHits := countingServer(t, http.StatusOK, body)
	mm, resolver := newFailoverManager(t, []string{unreachableURL(t), mirrorURL})
	dest := filepath.Join(t.TempDir(), "model.onnx")

	err := mm.downloadModelFile(t.Context(), "m", "owner/repo", "model.onnx", dest, sha256Hex(body), 0, "")
	require.NoError(t, err)
	assert.Equal(t, int64(1), mirrorHits.Load(), "mirror should have served the file")
	assert.Equal(t, []string{mirrorURL}, resolver.working(), "the mirror is recorded as the working endpoint")
}

func TestDownloadModelFile_FailsOverOnGatewayStatus(t *testing.T) {
	t.Parallel()
	// Every gateway status (502/503/504) means the origin is down while the host
	// itself answered, so a mirror may still serve the file. Drive each one end to
	// end through downloadModelFile, not just IsGatewayStatus, so the whole
	// failover path is proven for all three, not only the 503 case.
	statuses := []struct {
		name   string
		status int
	}{
		{"502 bad gateway", http.StatusBadGateway},
		{"503 service unavailable", http.StatusServiceUnavailable},
		{"504 gateway timeout", http.StatusGatewayTimeout},
	}
	for _, tc := range statuses {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			body := []byte("model-bytes")
			badURL, badHits := countingServer(t, tc.status, nil)
			mirrorURL, mirrorHits := countingServer(t, http.StatusOK, body)
			mm, resolver := newFailoverManager(t, []string{badURL, mirrorURL})
			dest := filepath.Join(t.TempDir(), "model.onnx")

			err := mm.downloadModelFile(t.Context(), "m", "owner/repo", "model.onnx", dest, sha256Hex(body), 0, "")
			require.NoError(t, err)
			assert.Equal(t, int64(1), badHits.Load())
			assert.Equal(t, int64(1), mirrorHits.Load())
			assert.Equal(t, []string{mirrorURL}, resolver.working())
		})
	}
}

func TestDownloadModelFile_DoesNotFailOverOn404(t *testing.T) {
	t.Parallel()
	primaryURL, primaryHits := countingServer(t, http.StatusNotFound, nil)
	mirrorURL, mirrorHits := countingServer(t, http.StatusOK, []byte("unused"))
	mm, resolver := newFailoverManager(t, []string{primaryURL, mirrorURL})
	dest := filepath.Join(t.TempDir(), "model.onnx")

	err := mm.downloadModelFile(t.Context(), "m", "owner/repo", "model.onnx", dest, "", 0, "")
	require.Error(t, err, "a 404 means the file is genuinely missing and must be surfaced")
	assert.Equal(t, int64(1), primaryHits.Load())
	assert.Equal(t, int64(0), mirrorHits.Load(), "a reachable 404 must NOT trigger a mirror hop")
	assert.Empty(t, resolver.working())
}

func TestDownloadModelFile_DoesNotFailOverOnChecksumMismatch(t *testing.T) {
	t.Parallel()
	primaryURL, primaryHits := countingServer(t, http.StatusOK, []byte("corrupt"))
	mirrorURL, mirrorHits := countingServer(t, http.StatusOK, []byte("also-unused"))
	mm, resolver := newFailoverManager(t, []string{primaryURL, mirrorURL})
	dest := filepath.Join(t.TempDir(), "model.onnx")

	// Expect the checksum of the real (different) content, so the primary's body fails verification.
	err := mm.downloadModelFile(t.Context(), "m", "owner/repo", "model.onnx", dest, sha256Hex([]byte("expected")), 0, "")
	require.Error(t, err, "a corrupt file must be surfaced, not masked by a mirror hop")
	assert.Equal(t, int64(1), primaryHits.Load())
	assert.Equal(t, int64(0), mirrorHits.Load())
	assert.Empty(t, resolver.working())
}

func TestDownloadModelFile_SucceedsOnFirstHost(t *testing.T) {
	t.Parallel()
	body := []byte("model-bytes")
	primaryURL, primaryHits := countingServer(t, http.StatusOK, body)
	mirrorURL, mirrorHits := countingServer(t, http.StatusOK, body)
	mm, resolver := newFailoverManager(t, []string{primaryURL, mirrorURL})
	dest := filepath.Join(t.TempDir(), "model.onnx")

	err := mm.downloadModelFile(t.Context(), "m", "owner/repo", "model.onnx", dest, sha256Hex(body), 0, "")
	require.NoError(t, err)
	assert.Equal(t, int64(1), primaryHits.Load())
	assert.Equal(t, int64(0), mirrorHits.Load(), "the mirror must not be contacted when the primary works")
	assert.Equal(t, []string{primaryURL}, resolver.working())
}

func TestDownloadModelFile_AllHostsUnreachable(t *testing.T) {
	t.Parallel()
	mm, resolver := newFailoverManager(t, []string{unreachableURL(t), unreachableURL(t)})
	dest := filepath.Join(t.TempDir(), "model.onnx")

	err := mm.downloadModelFile(t.Context(), "m", "owner/repo", "model.onnx", dest, "", 0, "")
	require.Error(t, err, "both hosts unreachable must produce a clear failure")
	assert.Empty(t, resolver.working())
}

func TestDownloadModelFile_NilResolverUsesSingleEndpoint(t *testing.T) {
	t.Parallel()
	body := []byte("model-bytes")
	// A settings override yields a single endpoint; with no resolver, that host
	// is the only one tried and there is no failover.
	primaryURL, primaryHits := countingServer(t, http.StatusOK, body)
	mm := NewModelManager(t.TempDir(), nil, nil)
	mm.downloading["m"] = &DownloadState{CatalogID: "m", Status: StatusDownloading}
	dest := filepath.Join(t.TempDir(), "model.onnx")

	err := mm.downloadModelFile(t.Context(), "m", "owner/repo", "model.onnx", dest, sha256Hex(body), 0, primaryURL)
	require.NoError(t, err)
	assert.Equal(t, int64(1), primaryHits.Load())
}

func TestDownloadModelFile_SingleEndpointMakesOneAttempt(t *testing.T) {
	t.Parallel()
	// With a nil resolver the chain has one element, so a failing host is tried
	// exactly once: there is no second host to fail over to, even though 503 is
	// otherwise a gateway status that would trigger failover on a 2-host chain.
	badURL, badHits := countingServer(t, http.StatusServiceUnavailable, nil)
	mm := NewModelManager(t.TempDir(), nil, nil)
	mm.downloading["m"] = &DownloadState{CatalogID: "m", Status: StatusDownloading}
	dest := filepath.Join(t.TempDir(), "model.onnx")

	err := mm.downloadModelFile(t.Context(), "m", "owner/repo", "model.onnx", dest, "", 0, badURL)
	require.Error(t, err)
	assert.Equal(t, int64(1), badHits.Load(), "a single-element chain must make exactly one attempt")
}

func TestSetEndpointResolver_NilDisablesFailover(t *testing.T) {
	t.Parallel()
	// Clearing the resolver reverts to the single configured endpoint with no
	// failover, matching the pre-feature behavior. The two placeholder endpoints
	// must never be contacted once the resolver is nil.
	body := []byte("model-bytes")
	primaryURL, primaryHits := countingServer(t, http.StatusOK, body)
	mm, resolver := newFailoverManager(t, []string{"http://placeholder-a.invalid", "http://placeholder-b.invalid"})
	mm.SetEndpointResolver(nil) // untyped nil clears the resolver
	dest := filepath.Join(t.TempDir(), "model.onnx")

	err := mm.downloadModelFile(t.Context(), "m", "owner/repo", "model.onnx", dest, sha256Hex(body), 0, primaryURL)
	require.NoError(t, err)
	assert.Equal(t, int64(1), primaryHits.Load())
	assert.Empty(t, resolver.working(), "a cleared resolver must not be consulted")
}
