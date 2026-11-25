package models

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"slices"
	"time"

	"github.com/labstack/echo/v4"
	"github.com/tphakala/birdnet-go/internal/classifier"
	"github.com/tphakala/birdnet-go/internal/errors"
	"github.com/tphakala/birdnet-go/internal/logger"
)

// documentUnavailableKey is the i18n key of the 502 returned when the text of a
// licence or policy document could not be fetched and verified.
const documentUnavailableKey = "analysis.gallery.errors.documentUnavailable"

// agreementRequiredKey is the i18n key of the 409 returned when a user-initiated
// install, variant swap or reinstall is refused because a document of the entry's
// own repo that requires agreement has none recorded. The frontend interpolates
// error_params.name (the model) and reads error_params.documents ({repo, kind,
// sha256}) to show exactly what is outstanding.
const agreementRequiredKey = "analysis.gallery.errors.agreementRequired"

// agreementRequiredMsg is the message of the agreement-required 409.
const agreementRequiredMsg = "a document of this model requires agreement before it can be installed"

// documentRequestTimeout bounds the whole document resolution of one request: the
// text endpoint, or every accepted document of an install or reinstall. It sits
// below the HTTP server's write timeout (DefaultWriteTimeout in internal/api/config.go,
// 30 seconds), so a slow fetch ends in the unavailable 502 instead of a dropped
// connection.
const documentRequestTimeout = 20 * time.Second

// documentUnavailableMsg is the message of the document-unavailable 502.
const documentUnavailableMsg = "the document text could not be fetched and verified"

// documentService is the document and agreement behaviour the handlers use. The
// live implementation reads the model manager's ModelDocuments and the compiled-in
// registry; tests inject a fake through Handler.documents.
type documentService interface {
	// EntryDocuments lists the documents of the entry's own repo.
	EntryDocuments(entry *classifier.CatalogEntry) []classifier.RepoDocument
	// DocumentBySHA256 finds a hosted document by digest, for the text endpoint.
	DocumentBySHA256(sha string) (classifier.RepoDocument, bool)
	// Text returns the verified text of a document.
	Text(ctx context.Context, doc *classifier.RepoDocument) ([]byte, error)
	// Agreed reports whether an agreement is recorded for a document.
	Agreed(doc *classifier.RepoDocument) bool
	// Record stores agreements for the documents.
	Record(docs []classifier.RepoDocument) error
}

// liveDocuments is the production documentService. A nil store (no model
// manager) has no text and no records.
type liveDocuments struct {
	store *classifier.ModelDocuments
}

func (liveDocuments) EntryDocuments(entry *classifier.CatalogEntry) []classifier.RepoDocument {
	return classifier.EntryDocuments(entry)
}

func (liveDocuments) DocumentBySHA256(sha string) (classifier.RepoDocument, bool) {
	return classifier.DocumentBySHA256(sha)
}

func (l liveDocuments) Text(ctx context.Context, doc *classifier.RepoDocument) ([]byte, error) {
	if l.store == nil {
		return nil, classifier.ErrDocumentUnavailable
	}
	return l.store.Text(ctx, doc)
}

func (l liveDocuments) Agreed(doc *classifier.RepoDocument) bool {
	return l.store != nil && l.store.Agreed(doc)
}

func (l liveDocuments) Record(docs []classifier.RepoDocument) error {
	if l.store == nil {
		return nil
	}
	return l.store.Record(docs)
}

// documentContext derives the context that bounds document resolution for one
// request from the request context.
func (c *Handler) documentContext(ctx echo.Context) (context.Context, context.CancelFunc) {
	timeout := documentRequestTimeout
	if c.documentTimeout > 0 {
		timeout = c.documentTimeout
	}
	return context.WithTimeout(ctx.Request().Context(), timeout)
}

// documentSvc returns the injected document service, or the live one.
func (c *Handler) documentSvc() documentService {
	if c.documents != nil {
		return c.documents
	}
	live := liveDocuments{}
	if c.ModelManager != nil {
		live.store = c.ModelManager.Documents()
	}
	return live
}

// ModelDocumentResponse is the body of GET /models/documents/:sha256.
type ModelDocumentResponse struct {
	// Repo is the HuggingFace repo that owns the document.
	Repo string `json:"repo"`
	// Kind is the document kind, for example "license" or "acceptable_use".
	Kind string `json:"kind"`
	// RequiresAgreement marks a document the user must agree to before installing.
	RequiresAgreement bool `json:"requiresAgreement"`
	// SHA256 is the digest the text was verified against.
	SHA256 string `json:"sha256"`
	// Text is the verified document text.
	Text string `json:"text"`
}

// GetModelDocument serves the verified text of a hosted document by its digest.
// The server fetches the text at the pinned revision, checks it against the
// digest and caches it; text that does not verify is never returned. An unknown
// digest is a 404, an unavailable or unverifiable text a 502 with
// documentUnavailableKey.
func (c *Handler) GetModelDocument(ctx echo.Context) error {
	sha := ctx.Param("sha256")
	svc := c.documentSvc()
	doc, ok := svc.DocumentBySHA256(sha)
	if !ok {
		return c.HandleError(ctx, nil, "unknown document", http.StatusNotFound)
	}
	reqCtx, cancel := c.documentContext(ctx)
	defer cancel()
	text, err := svc.Text(reqCtx, &doc)
	if err != nil {
		return c.documentUnavailable(ctx, err)
	}
	return ctx.JSON(http.StatusOK, ModelDocumentResponse{
		Repo:              doc.Repo,
		Kind:              doc.Kind,
		RequiresAgreement: doc.RequiresAgreement,
		SHA256:            doc.SHA256,
		Text:              string(text),
	})
}

// documentUnavailable answers the 502 for a document whose text could not be
// fetched and verified.
func (c *Handler) documentUnavailable(ctx echo.Context, err error) error {
	return c.HandleErrorWithKey(ctx, err, documentUnavailableMsg, http.StatusBadGateway, documentUnavailableKey, nil)
}

// decodeInstallRequest reads the optional JSON body of an install or reinstall
// request. The body is optional: an absent or empty body yields the zero request,
// so an empty-body io.EOF is tolerated. Decoding the body directly (rather than
// ctx.Bind, which 400s an empty body under a JSON content-type, and rather than
// gating on ContentLength, which is -1 for chunked requests) keeps that contract
// robust while still binding a real chunked body. The body reader is bounded by
// the server's body-limit middleware.
func decodeInstallRequest(ctx echo.Context) (installModelRequest, error) {
	var req installModelRequest
	if err := json.NewDecoder(ctx.Request().Body).Decode(&req); err != nil && !errors.Is(err, io.EOF) {
		return installModelRequest{}, err
	}
	return req, nil
}

// acceptDocuments resolves the digests a request says the user agreed to. Each
// must be a requires-agreement document of the entry's own repo (never of a file
// installed with it), else the request is a 400; each is then resolved through
// the verified text path, so an agreement is only ever recorded for text the
// server could verify (502 otherwise). It records nothing: the caller records the
// returned documents once the operation slot is held. handled is true when a
// response was written.
func (c *Handler) acceptDocuments(ctx echo.Context, entry *classifier.CatalogEntry, accepted []string) (docs []classifier.RepoDocument, handled bool, err error) {
	if len(accepted) == 0 {
		return nil, false, nil
	}
	svc := c.documentSvc()
	own := svc.EntryDocuments(entry)
	// Validate every digest before fetching any text, so a refused request never
	// fetches a document.
	for _, sha := range accepted {
		idx := slices.IndexFunc(own, func(d classifier.RepoDocument) bool {
			return d.RequiresAgreement && d.SHA256 != "" && d.SHA256 == sha
		})
		if idx < 0 {
			return nil, true, c.HandleError(ctx, nil, "accepted document "+sha+" is not an agreement document of model "+entry.ID, http.StatusBadRequest)
		}
		if !slices.ContainsFunc(docs, func(d classifier.RepoDocument) bool { return d.SHA256 == sha }) {
			docs = append(docs, own[idx])
		}
	}
	reqCtx, cancel := c.documentContext(ctx)
	defer cancel()
	for i := range docs {
		if _, terr := svc.Text(reqCtx, &docs[i]); terr != nil {
			return nil, true, c.documentUnavailable(ctx, terr)
		}
	}
	return docs, false, nil
}

// recordAgreements stores the agreements of an accepted install. A failed write
// keeps the records in memory and is logged; it never fails the request.
func (c *Handler) recordAgreements(catalogID string, docs []classifier.RepoDocument) {
	if len(docs) == 0 {
		return
	}
	if err := c.documentSvc().Record(docs); err != nil {
		c.LogWarnIfEnabled("Model agreement could not be saved; it is kept until restart",
			logger.String("catalog_id", catalogID),
			logger.Error(err))
	}
}

// agreementDocumentParam identifies one outstanding agreement document in the
// error_params of the agreement-required 409.
type agreementDocumentParam struct {
	Repo   string `json:"repo"`
	Kind   string `json:"kind"`
	SHA256 string `json:"sha256"`
}

// refuseOutstandingAgreements answers the agreement-required 409 when the entry's
// own repo has a requires-agreement document that is neither agreed nor in
// accepted, and reports whether it did. It records nothing and holds no
// operation slot, so a refusal leaves no trace.
//
// The check is the user-facing boundary and looks only at classifier.EntryDocuments
// of the requested entry: never at companion files, DependsOn entries or the
// manager's install path, because a model installed automatically with another
// (the Geomodel files that arrive with Perch v2 and BirdNET+ v3.0) must never be
// blocked by a terms agreement. An entry whose repo has no registry row, such as
// the hidden BSG entry (repo tphakala/BSG), has nothing to agree to and passes.
func (c *Handler) refuseOutstandingAgreements(ctx echo.Context, entry *classifier.CatalogEntry, accepted []classifier.RepoDocument) (handled bool, err error) {
	svc := c.documentSvc()
	own := svc.EntryDocuments(entry)
	var outstanding []agreementDocumentParam
	for i := range own {
		doc := &own[i]
		if !doc.RequiresAgreement || svc.Agreed(doc) {
			continue
		}
		if slices.ContainsFunc(accepted, func(a classifier.RepoDocument) bool {
			return a.Repo == doc.Repo && a.Kind == doc.Kind && a.SHA256 == doc.SHA256
		}) {
			continue
		}
		outstanding = append(outstanding, agreementDocumentParam{Repo: doc.Repo, Kind: doc.Kind, SHA256: doc.SHA256})
	}
	if len(outstanding) == 0 {
		return false, nil
	}
	return true, c.HandleErrorWithKey(ctx, nil, agreementRequiredMsg, http.StatusConflict,
		agreementRequiredKey, map[string]any{
			"name":      entry.Name,
			"documents": outstanding,
		})
}
