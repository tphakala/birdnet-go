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

		t.Run(rt.name+" keeps working with no body once agreed", func(t *testing.T) {
			h, fake, _ := documentFixture(t, nil)
			require.NoError(t, fake.Record(fake.byEntry[rt.id]))
			before := len(fake.recordedDocs())
			rec := guardRequest(t, rt.handle(h), http.MethodPost, rt.id, "")
			require.Equal(t, http.StatusAccepted, rec.Code, rec.Body.String())
			h.Wait()
			assert.Len(t, fake.recordedDocs(), before, "an empty body records nothing")
		})
	}

	t.Run("a repeated digest is recorded once and an empty list records nothing", func(t *testing.T) {
		h, fake, _ := documentFixture(t, nil)
		rec := guardRequest(t, h.InstallModel, http.MethodPost, apiDepN, body(docShaN, docShaN))
		require.Equal(t, http.StatusAccepted, rec.Code, rec.Body.String())
		h.Wait()
		assert.Len(t, fake.recordedDocs(), 1, "the repeated digest is one agreement")

		fake.recorded = nil
		require.NoError(t, fake.Record(fake.byEntry[apiDepA]))
		rec = guardRequest(t, h.ReinstallModel, http.MethodPost, apiDepA, body())
		require.Equal(t, http.StatusAccepted, rec.Code, rec.Body.String())
		h.Wait()
		assert.Len(t, fake.recordedDocs(), 1, "an empty list records nothing beyond the earlier agreement")
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

// TestReinstallModel_RefusedBeforeFetchingDocuments pins that a reinstall the
// request checks refuse answers that refusal, not a document error: a model that
// is not installed is a 400 even when its accepted document cannot be fetched.
func TestReinstallModel_RefusedBeforeFetchingDocuments(t *testing.T) {
	h, fake, _ := documentFixture(t, nil)
	delete(fake.texts, docShaN)

	rec := guardRequest(t, h.ReinstallModel, http.MethodPost, apiDepN, `{"acceptedDocuments":["`+docShaN+`"]}`)
	require.Equal(t, http.StatusBadRequest, rec.Code, rec.Body.String())
	assert.Contains(t, rec.Body.String(), "not installed")
	assert.Empty(t, fake.recordedDocs())
}

// TestAcceptDocuments_ValidatesBeforeFetching pins that every accepted digest is
// checked before any text is fetched: a list holding an agreement document that
// cannot be fetched and a digest that is not one is a 400, not a 502.
func TestAcceptDocuments_ValidatesBeforeFetching(t *testing.T) {
	h, fake, _ := documentFixture(t, nil)
	delete(fake.texts, docShaN)

	rec := guardRequest(t, h.InstallModel, http.MethodPost, apiDepN, `{"acceptedDocuments":["`+docShaN+`","`+docShaLicense+`"]}`)
	require.Equal(t, http.StatusBadRequest, rec.Code, rec.Body.String())
	assert.Empty(t, fake.recordedDocs())
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

// gateParams is the error_params shape of the agreement-required 409.
type gateParams struct {
	Name      string `json:"name"`
	Documents []struct {
		Repo   string `json:"repo"`
		Kind   string `json:"kind"`
		SHA256 string `json:"sha256"`
	} `json:"documents"`
}

// requireAgreementRequired asserts rec is the agreement-required 409 for the
// named model listing exactly the given digests.
func requireAgreementRequired(t *testing.T, rec *httptest.ResponseRecorder, name string, shas ...string) {
	t.Helper()
	require.Equal(t, http.StatusConflict, rec.Code, rec.Body.String())
	var body struct {
		ErrorKey    string     `json:"error_key"`
		ErrorParams gateParams `json:"error_params"`
	}
	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &body))
	assert.Equal(t, agreementRequiredKey, body.ErrorKey)
	assert.Equal(t, name, body.ErrorParams.Name)
	got := make([]string, 0, len(body.ErrorParams.Documents))
	for _, d := range body.ErrorParams.Documents {
		assert.NotEmpty(t, d.Repo)
		assert.Equal(t, classifier.DocumentKindAcceptableUse, d.Kind)
		got = append(got, d.SHA256)
	}
	assert.Equal(t, shas, got)
}

// TestInstall_AgreementGate pins the refusal of a user-initiated install, variant
// swap or reinstall of an entry whose own repo has an agreement document nobody
// agreed to, and that it never looks beyond the requested entry.
func TestInstall_AgreementGate(t *testing.T) {
	const docShaP = "dd44"
	acceptBody := func(sha string) string {
		b, err := json.Marshal(installModelRequest{AcceptedDocuments: []string{sha}})
		require.NoError(t, err)
		return string(b)
	}
	swapBody := `{"variantId":"` + guardVariantDFT + `","allowIncompatible":true}`
	swapAccepted := `{"variantId":"` + guardVariantDFT + `","allowIncompatible":true,"acceptedDocuments":["` + docShaP + `"]}`

	type row struct {
		name string
		call func(h *Handler, body string) *httptest.ResponseRecorder
		body string
		// agreedEntry, when set, is the entry whose documents were agreed earlier.
		agreedEntry string
		// refusedName is the model named in the 409; empty means 202.
		refusedName string
		shas        []string
		recorded    int
	}
	install := func(h *Handler, body string) *httptest.ResponseRecorder {
		return guardRequest(t, h.InstallModel, http.MethodPost, apiDepN, body)
	}
	swap := func(h *Handler, body string) *httptest.ResponseRecorder {
		return guardRequest(t, h.InstallModel, http.MethodPost, apiDepP, body)
	}
	reinstall := func(h *Handler, body string) *httptest.ResponseRecorder {
		return guardRequest(t, h.ReinstallModel, http.MethodPost, apiDepA, body)
	}
	rows := []row{
		{name: "install with nothing accepted", call: install, refusedName: "No geomodel", shas: []string{docShaN}},
		{name: "install with the agreement accepted", call: install, body: acceptBody(docShaN), recorded: 1},
		{name: "install agreed earlier with an empty body", call: install, agreedEntry: apiDepN},
		{name: "reinstall with nothing accepted", call: reinstall, refusedName: "Dependent A", shas: []string{docShaA}},
		{name: "reinstall with the agreement accepted", call: reinstall, body: acceptBody(docShaA), recorded: 1},
		{name: "reinstall agreed earlier with an empty body", call: reinstall, agreedEntry: apiDepA},
		{name: "variant swap of an installed gated entry is refused like an install", call: swap, body: swapBody, refusedName: "Primary", shas: []string{docShaP}},
		{name: "variant swap with the agreement accepted", call: swap, body: swapAccepted, recorded: 1},
	}
	for _, tc := range rows {
		t.Run(tc.name, func(t *testing.T) {
			h, fake, mm := documentFixture(t, nil)
			fake.byEntry[apiDepP] = []classifier.RepoDocument{{
				Repo: "t/p", Kind: classifier.DocumentKindAcceptableUse, RequiresAgreement: true, SHA256: docShaP,
			}}
			fake.texts[docShaP] = docText
			if tc.agreedEntry != "" {
				require.NoError(t, fake.Record(fake.byEntry[tc.agreedEntry]))
			}
			before := len(fake.recordedDocs())

			rec := tc.call(h, tc.body)

			if tc.refusedName != "" {
				requireAgreementRequired(t, rec, tc.refusedName, tc.shas...)
				assert.Len(t, fake.recordedDocs(), before, "a refusal records nothing")
				// Neither a leaked slot nor a started download: a second request is
				// not answered "operation in progress".
				lease, err := mm.BeginOperation(classifier.OperationInstall, apiDepN)
				require.NoError(t, err, "a refusal leaves the operation slot free")
				lease.Release()
				return
			}
			require.Equal(t, http.StatusAccepted, rec.Code, rec.Body.String())
			h.Wait()
			assert.Len(t, fake.recordedDocs(), before+tc.recorded)
		})
	}

	t.Run("an entry depending on a gated entry is not refused", func(t *testing.T) {
		h, fake, _ := documentFixture(t, nil)
		// G is gated; P depends on G and has no document of its own.
		fake.byEntry[apiDepG] = []classifier.RepoDocument{{
			Repo: "t/g", Kind: classifier.DocumentKindAcceptableUse, RequiresAgreement: true, SHA256: "ee55",
		}}
		rec := guardRequest(t, h.InstallModel, http.MethodPost, apiDepP, swapBody)
		require.Equal(t, http.StatusAccepted, rec.Code, rec.Body.String())
		h.Wait()
		assert.Empty(t, fake.recordedDocs())
	})

	t.Run("a reinstall while another operation runs still answers operation in progress", func(t *testing.T) {
		h, _, mm := documentFixture(t, nil)
		holdSlot(t, mm)
		rec := guardRequest(t, h.ReinstallModel, http.MethodPost, apiDepA, "")
		requireOperationInProgress(t, rec, apiDepG, "Geomodel", "uninstall")
	})

	t.Run("a reinstall of a model that is not installed is not an agreement question", func(t *testing.T) {
		h, _, _ := documentFixture(t, nil)
		rec := guardRequest(t, h.ReinstallModel, http.MethodPost, apiDepN, "")
		require.Equal(t, http.StatusBadRequest, rec.Code, rec.Body.String())
	})
}

// TestInstall_AgreementGateReadsOwnRepoOnly runs the live registry: an entry
// whose own repo is gated is refused, while an entry that merely downloads files
// from that repo (the Perch v2 shape) or whose repo has no registry row (the
// hidden BSG shape) installs with an empty body.
func TestInstall_AgreementGateReadsOwnRepoOnly(t *testing.T) {
	const gatedRepo = "tphakala/BirdNET-Plus-V3.0"
	file := func(name, repo string) classifier.CatalogFile {
		f := apiFile(classifier.RoleModel, name, apiSizeModel)
		f.HuggingFaceRepo = repo
		return f
	}
	entry := func(id, repo string, files ...classifier.CatalogFile) classifier.CatalogEntry {
		return classifier.CatalogEntry{
			ID: id, Name: id, Version: "1.0", Category: classifier.CategoryBird,
			HuggingFaceRepo: repo, Files: files,
		}
	}
	useCatalog(t, []classifier.CatalogEntry{
		entry("own-gated", gatedRepo, file("a.onnx", "")),
		entry("companion-gated", "example/ungated", file("b.onnx", ""), file("c.onnx", gatedRepo)),
		entry("no-registry-row", "tphakala/BSG", file("d.onnx", "")),
	})
	newHandler := func() *Handler {
		h := dependencyHandler(t, classifier.NewModelManager(t.TempDir(), nil, nil))
		h.Cancel()
		h.ortProbe = availableORT
		return h
	}

	h := newHandler()
	rec := guardRequest(t, h.InstallModel, http.MethodPost, "own-gated", "")
	require.Equal(t, http.StatusConflict, rec.Code, rec.Body.String())
	assert.Contains(t, rec.Body.String(), agreementRequiredKey)

	for _, id := range []string{"companion-gated", "no-registry-row"} {
		h := newHandler()
		rec := guardRequest(t, h.InstallModel, http.MethodPost, id, "")
		require.Equal(t, http.StatusAccepted, rec.Code, "%s: %s", id, rec.Body.String())
		h.Wait()
	}
}
