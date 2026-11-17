package models

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/labstack/echo/v4"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/tphakala/birdnet-go/internal/classifier"
	"github.com/tphakala/birdnet-go/internal/errors"
	"github.com/tphakala/birdnet-go/internal/inference"
)

const (
	// docShaN and docShaLicense are the digests of the agreement and licence
	// documents of entry N (apiDepN); docShaA is the agreement document of entry A.
	docShaN       = "aa11"
	docShaLicense = "bb22"
	docShaA       = "cc33"
	docRepoN      = "t/n"
	docRepoA      = "t/a"
	docText       = "the agreement text"
)

// fakeDocuments is a documentService over fixed documents. Text answers for the
// digests in texts and fails for the rest; Record keeps what it was given.
type fakeDocuments struct {
	mu       sync.Mutex
	byEntry  map[string][]classifier.RepoDocument
	texts    map[string]string
	recorded []classifier.RepoDocument
	// gate, when set, blocks Text until it is closed or the context ends.
	gate chan struct{}
	// entered, when set, receives a value each time Text starts waiting on gate.
	entered chan struct{}
}

func (f *fakeDocuments) EntryDocuments(entry *classifier.CatalogEntry) []classifier.RepoDocument {
	return f.byEntry[entry.ID]
}

func (f *fakeDocuments) DocumentBySHA256(sha string) (classifier.RepoDocument, bool) {
	for _, docs := range f.byEntry {
		for _, d := range docs {
			if d.SHA256 == sha {
				return d, true
			}
		}
	}
	return classifier.RepoDocument{}, false
}

func (f *fakeDocuments) Text(ctx context.Context, doc *classifier.RepoDocument) ([]byte, error) {
	if f.gate != nil {
		if f.entered != nil {
			select {
			case f.entered <- struct{}{}:
			default:
			}
		}
		select {
		case <-f.gate:
		case <-ctx.Done():
			return nil, errors.Join(classifier.ErrDocumentUnavailable, ctx.Err())
		}
	}
	if text, ok := f.texts[doc.SHA256]; ok {
		return []byte(text), nil
	}
	return nil, errors.Join(classifier.ErrDocumentUnavailable, errors.NewStd("HTTP request failed for https://example.invalid/x"))
}

func (f *fakeDocuments) Agreed(doc *classifier.RepoDocument) bool {
	f.mu.Lock()
	defer f.mu.Unlock()
	for _, d := range f.recorded {
		if d.Repo == doc.Repo && d.Kind == doc.Kind && d.SHA256 == doc.SHA256 {
			return true
		}
	}
	return false
}

func (f *fakeDocuments) Record(docs []classifier.RepoDocument) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.recorded = append(f.recorded, docs...)
	return nil
}

func (f *fakeDocuments) recordedDocs() []classifier.RepoDocument {
	f.mu.Lock()
	defer f.mu.Unlock()
	return slices.Clone(f.recorded)
}

// newFakeDocuments gives entry N (apiDepN) an agreement and a licence document of
// repo t/n, and entry A (apiDepA) an agreement document of repo t/a. The text of
// every document is available.
func newFakeDocuments() *fakeDocuments {
	policy := func(repo, sha string) classifier.RepoDocument {
		return classifier.RepoDocument{
			Repo: repo, Kind: classifier.DocumentKindAcceptableUse, RequiresAgreement: true, SHA256: sha,
		}
	}
	return &fakeDocuments{
		byEntry: map[string][]classifier.RepoDocument{
			apiDepN: {
				policy(docRepoN, docShaN),
				{Repo: docRepoN, Kind: "license", SHA256: docShaLicense},
			},
			apiDepA: {policy(docRepoA, docShaA)},
		},
		texts: map[string]string{docShaN: docText, docShaA: docText, docShaLicense: "licence"},
	}
}

// documentFixture is a handler over the guard catalog with the fake service and a
// cancelled core, so an accepted install fails in the background without network.
func documentFixture(t *testing.T, configure func([]classifier.CatalogEntry)) (h *Handler, fake *fakeDocuments, mm *classifier.ModelManager) {
	t.Helper()
	catalog := guardCatalog()
	if configure != nil {
		configure(catalog)
	}
	useCatalog(t, catalog)
	mm, _ = guardInstalledManager(t)
	h = dependencyHandler(t, mm)
	h.Cancel()
	h.ortProbe = availableORT
	fake = newFakeDocuments()
	h.documents = fake
	return h, fake, mm
}

// TestInstallAndReinstall_AcceptedDocuments pins the agreement body of both
// starters: an accepted agreement document of the entry's own repo is recorded and
// reported as agreed, anything else is refused, and a request refused for another
// reason records nothing.
func TestInstallAndReinstall_AcceptedDocuments(t *testing.T) {
	type route struct {
		name   string
		id     string
		handle func(h *Handler) echo.HandlerFunc
	}
	// N is not installed (a fresh install); A is installed (a reinstall).
	install := route{"install", apiDepN, func(h *Handler) echo.HandlerFunc { return h.InstallModel }}
	reinstall := route{"reinstall", apiDepA, func(h *Handler) echo.HandlerFunc { return h.ReinstallModel }}
	body := func(shas ...string) string {
		b, err := json.Marshal(installModelRequest{AcceptedDocuments: shas})
		require.NoError(t, err)
		return string(b)
	}
	ownSha := map[string]string{apiDepN: docShaN, apiDepA: docShaA}

	for _, rt := range []route{install, reinstall} {
		t.Run(rt.name+" records an accepted agreement and the catalog reports it", func(t *testing.T) {
			h, fake, _ := documentFixture(t, nil)
			rec := guardRequest(t, rt.handle(h), http.MethodPost, rt.id, body(ownSha[rt.id]))
			require.Equal(t, http.StatusAccepted, rec.Code, rec.Body.String())
			h.Wait()

			recorded := fake.recordedDocs()
			require.Len(t, recorded, 1)
			assert.Equal(t, ownSha[rt.id], recorded[0].SHA256)

			entry := findEntry(catalogRequest(t, h), rt.id)
			require.NotNil(t, entry)
			for _, d := range entry.Documents {
				assert.Equal(t, d.RequiresAgreement, d.Agreed, "only the agreement document is agreed: %s", d.Kind)
			}
		})

		t.Run(rt.name+" refuses a digest that is not an agreement document of the entry", func(t *testing.T) {
			otherSha := map[string]string{apiDepN: docShaA, apiDepA: docShaN}[rt.id]
			for name, sha := range map[string]string{
				"unknown":                    "ffff",
				"a document of another repo": otherSha,
				"a licence document":         docShaLicense,
			} {
				t.Run(name, func(t *testing.T) {
					h, fake, mm := documentFixture(t, nil)
					rec := guardRequest(t, rt.handle(h), http.MethodPost, rt.id, body(sha))
					require.Equal(t, http.StatusBadRequest, rec.Code, rec.Body.String())
					assert.Empty(t, fake.recordedDocs())
					lease, err := mm.BeginOperation(classifier.OperationInstall, rt.id)
					require.NoError(t, err, "a refused request frees the operation slot")
					lease.Release()
				})
			}
		})

		t.Run(rt.name+" answers 502 and records nothing when the text cannot be verified", func(t *testing.T) {
			h, fake, mm := documentFixture(t, nil)
			delete(fake.texts, ownSha[rt.id])
			rec := guardRequest(t, rt.handle(h), http.MethodPost, rt.id, body(ownSha[rt.id]))
			require.Equal(t, http.StatusBadGateway, rec.Code, rec.Body.String())
			var resp struct {
				ErrorKey string `json:"error_key"`
			}
			require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &resp))
			assert.Equal(t, documentUnavailableKey, resp.ErrorKey)
			assert.NotContains(t, rec.Body.String(), "example.invalid", "the fetch error stays in the log")
			assert.Empty(t, fake.recordedDocs())
			lease, err := mm.BeginOperation(classifier.OperationInstall, rt.id)
			require.NoError(t, err, "a refused request frees the operation slot")
			lease.Release()
		})

		t.Run(rt.name+" keeps working with no body", func(t *testing.T) {
			h, fake, _ := documentFixture(t, nil)
			rec := guardRequest(t, rt.handle(h), http.MethodPost, rt.id, "")
			require.Equal(t, http.StatusAccepted, rec.Code, rec.Body.String())
			h.Wait()
			assert.Empty(t, fake.recordedDocs(), "this build does not refuse and does not record without a body")
		})
	}

	t.Run("a repeated digest is recorded once and an empty list records nothing", func(t *testing.T) {
		h, fake, _ := documentFixture(t, nil)
		rec := guardRequest(t, h.InstallModel, http.MethodPost, apiDepN, body(docShaN, docShaN))
		require.Equal(t, http.StatusAccepted, rec.Code, rec.Body.String())
		h.Wait()
		assert.Len(t, fake.recordedDocs(), 1, "the repeated digest is one agreement")

		fake.recorded = nil
		rec = guardRequest(t, h.ReinstallModel, http.MethodPost, apiDepA, body())
		require.Equal(t, http.StatusAccepted, rec.Code, rec.Body.String())
		h.Wait()
		assert.Empty(t, fake.recordedDocs())
	})

	t.Run("a request refused for another reason records nothing", func(t *testing.T) {
		t.Run("install while another operation runs", func(t *testing.T) {
			h, fake, mm := documentFixture(t, nil)
			holdSlot(t, mm)
			rec := guardRequest(t, h.InstallModel, http.MethodPost, apiDepN, body(docShaN))
			requireOperationInProgress(t, rec, apiDepG, "Geomodel", "uninstall")
			assert.Empty(t, fake.recordedDocs())
		})
		t.Run("install of a model that needs a missing ONNX Runtime", func(t *testing.T) {
			h, fake, _ := documentFixture(t, func(catalog []classifier.CatalogEntry) {
				for i := range catalog {
					if catalog[i].ID == apiDepN {
						catalog[i].RequiresONNX = true
					}
				}
			})
			h.ortProbe = func(string) inference.ORTStatus { return inference.ORTStatus{Error: "missing"} }
			rec := guardRequest(t, h.InstallModel, http.MethodPost, apiDepN, body(docShaN))
			require.Equal(t, http.StatusConflict, rec.Code, rec.Body.String())
			assert.Empty(t, fake.recordedDocs())
		})
		t.Run("reinstall while another operation runs", func(t *testing.T) {
			h, fake, mm := documentFixture(t, nil)
			holdSlot(t, mm)
			rec := guardRequest(t, h.ReinstallModel, http.MethodPost, apiDepA, body(docShaA))
			requireOperationInProgress(t, rec, apiDepG, "Geomodel", "uninstall")
			assert.Empty(t, fake.recordedDocs())
		})
		t.Run("reinstall of a model that is not installed", func(t *testing.T) {
			h, fake, _ := documentFixture(t, nil)
			rec := guardRequest(t, h.ReinstallModel, http.MethodPost, apiDepN, body(docShaN))
			require.Equal(t, http.StatusBadRequest, rec.Code, rec.Body.String())
			assert.Empty(t, fake.recordedDocs())
		})
	})

	t.Run("two repos pinning the same digest record the requested entry's repo", func(t *testing.T) {
		h, fake, _ := documentFixture(t, nil)
		// Entry A's agreement now has the very digest of entry N's.
		fake.byEntry[apiDepA][0].SHA256 = docShaN
		rec := guardRequest(t, h.InstallModel, http.MethodPost, apiDepN, body(docShaN))
		require.Equal(t, http.StatusAccepted, rec.Code, rec.Body.String())
		h.Wait()
		recorded := fake.recordedDocs()
		require.Len(t, recorded, 1)
		assert.Equal(t, docRepoN, recorded[0].Repo)

		rec = guardRequest(t, h.ReinstallModel, http.MethodPost, apiDepA, body(docShaN))
		require.Equal(t, http.StatusAccepted, rec.Code, rec.Body.String())
		h.Wait()
		recorded = fake.recordedDocs()
		require.Len(t, recorded, 2)
		assert.Equal(t, docRepoA, recorded[1].Repo)
	})
}

func TestGetModelDocument(t *testing.T) {
	get := func(h *Handler, sha string) *httptest.ResponseRecorder {
		rec := httptest.NewRecorder()
		ctx := echo.New().NewContext(httptest.NewRequest(http.MethodGet, "/api/v2/models/documents/"+sha, http.NoBody), rec)
		ctx.SetParamNames("sha256")
		ctx.SetParamValues(sha)
		require.NoError(t, h.GetModelDocument(ctx))
		return rec
	}

	t.Run("serves the verified text with its identity", func(t *testing.T) {
		h, _, _ := documentFixture(t, nil)
		rec := get(h, docShaN)
		require.Equal(t, http.StatusOK, rec.Code, rec.Body.String())
		var resp ModelDocumentResponse
		require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &resp))
		assert.Equal(t, ModelDocumentResponse{
			Repo: docRepoN, Kind: classifier.DocumentKindAcceptableUse, RequiresAgreement: true,
			SHA256: docShaN, Text: docText,
		}, resp)
	})
	t.Run("an unknown digest is a 404", func(t *testing.T) {
		h, _, _ := documentFixture(t, nil)
		assert.Equal(t, http.StatusNotFound, get(h, "ffff").Code)
	})
	t.Run("text that cannot be verified is a 502 with the key", func(t *testing.T) {
		h, fake, _ := documentFixture(t, nil)
		delete(fake.texts, docShaN)
		rec := get(h, docShaN)
		require.Equal(t, http.StatusBadGateway, rec.Code)
		assert.Contains(t, rec.Body.String(), documentUnavailableKey)
	})
}

// TestDocumentResolution_IsBounded pins the request-level deadline: a document
// fetch that never finishes answers the unavailable 502 instead of running on, for
// the text endpoint and for accepted documents of both starters.
func TestDocumentResolution_IsBounded(t *testing.T) {
	body := `{"acceptedDocuments":["` + docShaN + `"]}`
	for name, call := range map[string]func(h *Handler) *httptest.ResponseRecorder{
		"text endpoint": func(h *Handler) *httptest.ResponseRecorder {
			rec := httptest.NewRecorder()
			ctx := echo.New().NewContext(httptest.NewRequest(http.MethodGet, "/", http.NoBody), rec)
			ctx.SetParamNames("sha256")
			ctx.SetParamValues(docShaN)
			require.NoError(t, h.GetModelDocument(ctx))
			return rec
		},
		"install": func(h *Handler) *httptest.ResponseRecorder {
			return guardRequest(t, h.InstallModel, http.MethodPost, apiDepN, body)
		},
		"reinstall": func(h *Handler) *httptest.ResponseRecorder {
			return guardRequest(t, h.ReinstallModel, http.MethodPost, apiDepA, `{"acceptedDocuments":["`+docShaA+`"]}`)
		},
	} {
		t.Run(name, func(t *testing.T) {
			h, fake, _ := documentFixture(t, nil)
			fake.gate = make(chan struct{})
			t.Cleanup(func() { close(fake.gate) })
			h.documentTimeout = 50 * time.Millisecond

			rec := call(h)
			require.Equal(t, http.StatusBadGateway, rec.Code, rec.Body.String())
			assert.Contains(t, rec.Body.String(), documentUnavailableKey)
			assert.Empty(t, fake.recordedDocs())
		})
	}
}

// TestReinstallModel_SlowDocumentDoesNotHoldTheSlot pins that accepted documents
// are resolved before the operation slot is taken: while the text is still being
// fetched, another operation can start.
func TestReinstallModel_SlowDocumentDoesNotHoldTheSlot(t *testing.T) {
	h, fake, mm := documentFixture(t, nil)
	fake.gate = make(chan struct{})
	fake.entered = make(chan struct{}, 1)
	done := make(chan *httptest.ResponseRecorder, 1)
	go func() {
		req := httptest.NewRequest(http.MethodPost, "/api/v2/models/reinstall/"+apiDepA, strings.NewReader(`{"acceptedDocuments":["`+docShaA+`"]}`))
		rec := httptest.NewRecorder()
		ctx := echo.New().NewContext(req, rec)
		ctx.SetParamNames("id")
		ctx.SetParamValues(apiDepA)
		_ = h.ReinstallModel(ctx)
		done <- rec
	}()

	// Wait until the fetch is blocked, then the slot must be free for another operation.
	select {
	case <-fake.entered:
	case <-time.After(guardWait):
		t.Fatal("the reinstall never started resolving the document")
	}
	require.Eventually(t, func() bool {
		lease, err := mm.BeginOperation(classifier.OperationInstall, apiDepN)
		if err != nil {
			return false
		}
		lease.Release()
		return true
	}, guardWait, 10*time.Millisecond, "the slot was held while the document was being fetched")

	close(fake.gate)
	rec := <-done
	assert.Equal(t, http.StatusAccepted, rec.Code, rec.Body.String())
	h.Wait()
}
