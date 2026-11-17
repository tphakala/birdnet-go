package classifier

import (
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

const (
	testDocRepo     = "owner/model-a"
	testDocOtherRep = "owner/model-b"
	testDocRevision = "0123456789abcdef0123456789abcdef01234567"
	testDocPath     = "POLICY.md"
	testDocText     = "You may use this model only for conservation.\n"
)

// testPolicyDoc returns a hosted agreement document for repo pinned to text.
func testPolicyDoc(repo, text string) RepoDocument {
	return RepoDocument{
		Repo:              repo,
		Kind:              DocumentKindAcceptableUse,
		RequiresAgreement: true,
		Path:              testDocPath,
		Revision:          testDocRevision,
		SHA256:            sha256Hex([]byte(text)),
	}
}

// docServer is an httptest server that serves body for every request and counts
// the requests and records their paths.
type docServer struct {
	*httptest.Server
	mu    sync.Mutex
	paths []string
}

func newDocServer(t *testing.T, status int, body string) *docServer {
	t.Helper()
	ds := &docServer{}
	ds.Server = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		ds.mu.Lock()
		ds.paths = append(ds.paths, r.URL.Path)
		ds.mu.Unlock()
		w.WriteHeader(status)
		_, _ = w.Write([]byte(body))
	}))
	t.Cleanup(ds.Close)
	return ds
}

func (ds *docServer) requests() []string {
	ds.mu.Lock()
	defer ds.mu.Unlock()
	return slices.Clone(ds.paths)
}

// newTestDocuments builds a service over the endpoints, with a state dir, and
// returns it with the list of endpoints noted as working.
func newTestDocuments(t *testing.T, dir string, endpoints ...string) (svc *ModelDocuments, noted *[]string) {
	t.Helper()
	noted = &[]string{}
	svc = newModelDocuments(func() []string { return endpoints }, func(e string) { *noted = append(*noted, e) })
	if dir != "" {
		svc.SetStateDir(dir)
	}
	return svc, noted
}

func TestModelDocuments_Text(t *testing.T) {
	t.Parallel()

	t.Run("fetches the pinned revision once and serves the cache afterwards", func(t *testing.T) {
		t.Parallel()
		srv := newDocServer(t, http.StatusOK, testDocText)
		dir := t.TempDir()
		svc, noted := newTestDocuments(t, dir, srv.URL)
		doc := testPolicyDoc(testDocRepo, testDocText)

		got, err := svc.Text(t.Context(), &doc)
		require.NoError(t, err)
		assert.Equal(t, testDocText, string(got))
		assert.Equal(t, []string{"/" + testDocRepo + "/resolve/" + testDocRevision + "/" + testDocPath}, srv.requests())
		assert.Equal(t, []string{srv.URL}, *noted)
		assert.FileExists(t, filepath.Join(dir, documentCacheDirName, doc.SHA256))

		got, err = svc.Text(t.Context(), &doc)
		require.NoError(t, err)
		assert.Equal(t, testDocText, string(got))
		assert.Len(t, srv.requests(), 1, "the second read must come from the cache")
	})

	t.Run("bytes that do not match the pin are refused and not cached", func(t *testing.T) {
		t.Parallel()
		srv := newDocServer(t, http.StatusOK, "something else entirely")
		dir := t.TempDir()
		svc, noted := newTestDocuments(t, dir, srv.URL)
		doc := testPolicyDoc(testDocRepo, testDocText)

		_, err := svc.Text(t.Context(), &doc)
		require.ErrorIs(t, err, ErrDocumentMismatch)
		assert.NoFileExists(t, filepath.Join(dir, documentCacheDirName, doc.SHA256))
		assert.Empty(t, *noted, "a host that served the wrong bytes is not a working endpoint")

		_, err = svc.Text(t.Context(), &doc)
		require.ErrorIs(t, err, ErrDocumentMismatch)
		assert.Len(t, srv.requests(), 2, "nothing was cached, so the next call fetches again")
	})

	t.Run("a tampered cache file is refetched and replaced", func(t *testing.T) {
		t.Parallel()
		srv := newDocServer(t, http.StatusOK, testDocText)
		dir := t.TempDir()
		svc, _ := newTestDocuments(t, dir, srv.URL)
		doc := testPolicyDoc(testDocRepo, testDocText)
		cachePath := filepath.Join(dir, documentCacheDirName, doc.SHA256)
		require.NoError(t, os.MkdirAll(filepath.Dir(cachePath), 0o755))
		require.NoError(t, os.WriteFile(cachePath, []byte("I agree to everything"), 0o600))

		got, err := svc.Text(t.Context(), &doc)
		require.NoError(t, err)
		assert.Equal(t, testDocText, string(got))
		assert.Len(t, srv.requests(), 1)
		onDisk, err := os.ReadFile(cachePath) //nolint:gosec // G304: path under t.TempDir
		require.NoError(t, err)
		assert.Equal(t, testDocText, string(onDisk))
	})

	t.Run("a body over the size cap is refused", func(t *testing.T) {
		t.Parallel()
		big := strings.Repeat("x", documentMaxBytes+1)
		srv := newDocServer(t, http.StatusOK, big)
		svc, _ := newTestDocuments(t, t.TempDir(), srv.URL)

		_, err := svc.Text(t.Context(), new(testPolicyDoc(testDocRepo, big)))
		require.ErrorIs(t, err, ErrDocumentUnavailable)
	})

	t.Run("two repos pinning the same bytes share one cache file but each fetch goes to its own repo", func(t *testing.T) {
		t.Parallel()
		srv := newDocServer(t, http.StatusOK, testDocText)
		dir := t.TempDir()
		svc, _ := newTestDocuments(t, dir, srv.URL)
		a := testPolicyDoc(testDocRepo, testDocText)
		b := testPolicyDoc(testDocOtherRep, testDocText)
		require.Equal(t, a.SHA256, b.SHA256)

		_, err := svc.Text(t.Context(), &a)
		require.NoError(t, err)
		// A cache hit is keyed by the bytes, so b needs no request while the file is there.
		_, err = svc.Text(t.Context(), &b)
		require.NoError(t, err)
		require.Len(t, srv.requests(), 1)

		require.NoError(t, os.Remove(filepath.Join(dir, documentCacheDirName, a.SHA256)))
		_, err = svc.Text(t.Context(), &b)
		require.NoError(t, err)
		assert.Equal(t, []string{
			"/" + testDocRepo + "/resolve/" + testDocRevision + "/" + testDocPath,
			"/" + testDocOtherRep + "/resolve/" + testDocRevision + "/" + testDocPath,
		}, srv.requests())
	})

	t.Run("an unreachable endpoint fails over to the next and notes it", func(t *testing.T) {
		t.Parallel()
		dead := httptest.NewServer(http.NotFoundHandler())
		deadURL := dead.URL
		dead.Close()
		live := newDocServer(t, http.StatusOK, testDocText)
		svc, noted := newTestDocuments(t, t.TempDir(), deadURL, live.URL)

		got, err := svc.Text(t.Context(), new(testPolicyDoc(testDocRepo, testDocText)))
		require.NoError(t, err)
		assert.Equal(t, testDocText, string(got))
		assert.Equal(t, []string{live.URL}, *noted)
	})

	t.Run("a 404 does not fail over", func(t *testing.T) {
		t.Parallel()
		missing := newDocServer(t, http.StatusNotFound, "")
		other := newDocServer(t, http.StatusOK, testDocText)
		svc, _ := newTestDocuments(t, t.TempDir(), missing.URL, other.URL)

		_, err := svc.Text(t.Context(), new(testPolicyDoc(testDocRepo, testDocText)))
		require.ErrorIs(t, err, ErrDocumentUnavailable)
		assert.Contains(t, err.Error(), "HTTP 404 for")
		assert.Empty(t, other.requests())
	})

	t.Run("without a state dir the text is held in memory", func(t *testing.T) {
		t.Parallel()
		srv := newDocServer(t, http.StatusOK, testDocText)
		svc, _ := newTestDocuments(t, "", srv.URL)
		doc := testPolicyDoc(testDocRepo, testDocText)

		for range 2 {
			got, err := svc.Text(t.Context(), &doc)
			require.NoError(t, err)
			assert.Equal(t, testDocText, string(got))
		}
		assert.Len(t, srv.requests(), 1)
	})

	t.Run("an external document has no text to fetch", func(t *testing.T) {
		t.Parallel()
		svc, _ := newTestDocuments(t, "", "http://127.0.0.1:1")
		_, err := svc.Text(t.Context(), &RepoDocument{Repo: testDocRepo, Kind: "license", URL: "https://example.com"})
		require.ErrorIs(t, err, ErrDocumentUnavailable)
	})
}

func TestModelDocuments_AgreementsAcrossRestarts(t *testing.T) {
	t.Parallel()

	docA := testPolicyDoc(testDocRepo, "version A")
	docB := testPolicyDoc(testDocRepo, "version B")

	t.Run("records survive a restart and a new digest is outstanding", func(t *testing.T) {
		t.Parallel()
		dir := t.TempDir()
		first, _ := newTestDocuments(t, dir)
		assert.False(t, first.Agreed(&docA))
		assert.Equal(t, []RepoDocument{docA}, first.Outstanding([]RepoDocument{docA}))
		require.NoError(t, first.Record([]RepoDocument{docA}))

		second, _ := newTestDocuments(t, dir)
		assert.True(t, second.Agreed(&docA))
		assert.Equal(t, []RepoDocument{docB}, second.Outstanding([]RepoDocument{docA, docB}), "B has the same repo and kind but a new digest")

		require.NoError(t, second.Record([]RepoDocument{docB}))
		third, _ := newTestDocuments(t, dir)
		assert.True(t, third.Agreed(&docA), "an old record stays after a newer one")
		assert.True(t, third.Agreed(&docB))
		assert.Empty(t, third.Outstanding([]RepoDocument{docA, docB}))
	})

	t.Run("agreement is keyed by repo, kind and digest", func(t *testing.T) {
		t.Parallel()
		svc, _ := newTestDocuments(t, t.TempDir())
		require.NoError(t, svc.Record([]RepoDocument{docA}))

		otherRepo := docA
		otherRepo.Repo = testDocOtherRep
		otherKind := docA
		otherKind.Kind = "license"
		assert.False(t, svc.Agreed(&otherRepo))
		assert.False(t, svc.Agreed(&otherKind))
		assert.True(t, svc.Agreed(&RepoDocument{Repo: " Owner/Model-A/ ", CatalogDocument: docA.CatalogDocument}), "the repo compares normalized")
	})

	t.Run("a document that needs no agreement is never outstanding", func(t *testing.T) {
		t.Parallel()
		svc, _ := newTestDocuments(t, t.TempDir())
		lic := docA
		lic.RequiresAgreement = false
		assert.Empty(t, svc.Outstanding([]RepoDocument{lic}))
	})

	unreadable := map[string]string{
		"a corrupt file":                     "{not json",
		"a version this build does not know": `{"version": 99, "agreements": []}`,
	}
	for name, content := range unreadable {
		t.Run(name+" loads empty, answers from memory and is never overwritten", func(t *testing.T) {
			t.Parallel()
			dir := t.TempDir()
			path := filepath.Join(dir, agreementsFileName)
			require.NoError(t, os.WriteFile(path, []byte(content), 0o600))

			svc, _ := newTestDocuments(t, dir)
			assert.False(t, svc.Agreed(&docA))
			require.NoError(t, svc.Record([]RepoDocument{docA}))
			assert.True(t, svc.Agreed(&docA), "the record is kept in memory")

			onDisk, err := os.ReadFile(path) //nolint:gosec // G304: path under t.TempDir
			require.NoError(t, err)
			assert.Equal(t, content, string(onDisk))
		})
	}

	t.Run("an unwritable directory answers from memory and returns the write error", func(t *testing.T) {
		t.Parallel()
		dir := t.TempDir()
		svc, _ := newTestDocuments(t, dir)
		// Replace the (empty) config directory with a regular file, so creating it
		// for the write fails whatever user the tests run as.
		require.NoError(t, os.Remove(dir))
		require.NoError(t, os.WriteFile(dir, nil, 0o600))

		err := svc.Record([]RepoDocument{docA})
		require.Error(t, err)
		assert.True(t, svc.Agreed(&docA))
	})

	t.Run("a record whose write failed is written by the next Record of the same document", func(t *testing.T) {
		t.Parallel()
		dir := t.TempDir()
		svc, _ := newTestDocuments(t, dir)
		// Make the first write fail: a regular file stands where the directory is.
		require.NoError(t, os.Remove(dir))
		require.NoError(t, os.WriteFile(dir, nil, 0o600))
		require.Error(t, svc.Record([]RepoDocument{docA}))

		// The directory recovers; the same agreement is accepted again.
		require.NoError(t, os.Remove(dir))
		require.NoError(t, os.Mkdir(dir, 0o750))
		require.NoError(t, svc.Record([]RepoDocument{docA}))

		reloaded, _ := newTestDocuments(t, dir)
		assert.True(t, reloaded.Agreed(&docA), "the agreement reached the file on the retry")
	})

	t.Run("without a state dir records stay in memory", func(t *testing.T) {
		t.Parallel()
		svc, _ := newTestDocuments(t, "")
		require.NoError(t, svc.Record([]RepoDocument{docA}))
		assert.True(t, svc.Agreed(&docA))
	})

	t.Run("concurrent records lose no entry", func(t *testing.T) {
		t.Parallel()
		dir := t.TempDir()
		svc, _ := newTestDocuments(t, dir)
		const writers = 20
		docs := make([]RepoDocument, writers)
		for i := range docs {
			docs[i] = testPolicyDoc(testDocRepo, "text "+string(rune('a'+i)))
		}
		var wg sync.WaitGroup
		for i := range docs {
			wg.Go(func() { assert.NoError(t, svc.Record([]RepoDocument{docs[i], docs[0]})) })
		}
		wg.Wait()

		reloaded, _ := newTestDocuments(t, dir)
		for i := range docs {
			assert.True(t, reloaded.Agreed(&docs[i]), "record %d", i)
		}
		assert.Empty(t, reloaded.Outstanding(docs))
	})
}
