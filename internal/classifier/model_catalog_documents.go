package classifier

import (
	"maps"
	"slices"
	"strings"
)

// DocumentKindAcceptableUse is the kind of an acceptable use policy. A manifest
// may carry other kinds, "license" among them; the API passes every kind
// through unchanged.
const DocumentKindAcceptableUse = "acceptable_use"

// CatalogDocument is one licence or policy document of a model repo, as pinned
// by the acoustic-models manifest. A hosted document has Path, Revision and
// SHA256 and is fetched from the repo at that revision; an external one has
// only URL and is linked, never fetched.
type CatalogDocument struct {
	// Kind is the document kind, for example "license" or "acceptable_use".
	Kind string
	// RequiresAgreement marks a document the user must agree to before an
	// install of the repo's models.
	RequiresAgreement bool
	// Path is the file path inside the repo (hosted documents only).
	Path string
	// Revision is the 40-hex commit the document is pinned to (hosted only).
	Revision string
	// SHA256 is the hex digest of the pinned bytes (hosted only).
	SHA256 string
	// URL is the external location (external documents only).
	URL string
}

// RepoDocument is a CatalogDocument together with the repo it belongs to. An
// agreement is identified by repo, kind and SHA256.
type RepoDocument struct {
	// Repo is the HuggingFace repo that owns the document.
	Repo string
	CatalogDocument
}

// documentRegistry is the registry the lookups read. It defaults to the
// generated catalogDocuments so in-package tests can swap it.
var documentRegistry = catalogDocuments

// normalizeDocumentRepo is the lookup key for a repo: spaces and trailing
// slashes trimmed, compared case-insensitively. The generator applies the same
// rule and rejects two manifests that collide under it.
func normalizeDocumentRepo(repo string) string {
	return strings.ToLower(strings.TrimRight(strings.TrimSpace(repo), "/"))
}

// EntryDocuments returns the documents of the catalog entry's own repo
// (entry.HuggingFaceRepo) and nothing else. Companion files and DependsOn
// dependencies are installed automatically with another entry, for example the
// Geomodel files that arrive with Perch v2, so their repos never add a document:
// only an install the user starts for this entry itself may be gated on them. An
// unknown or empty repo gives nil.
func EntryDocuments(entry *CatalogEntry) []RepoDocument {
	if entry == nil {
		return nil
	}
	want := normalizeDocumentRepo(entry.HuggingFaceRepo)
	if want == "" {
		return nil
	}
	for repo, docs := range documentRegistry {
		if normalizeDocumentRepo(repo) != want {
			continue
		}
		out := make([]RepoDocument, 0, len(docs))
		for i := range docs {
			out = append(out, RepoDocument{Repo: repo, CatalogDocument: docs[i]})
		}
		return out
	}
	return nil
}

// DocumentBySHA256 finds a hosted document by its digest, in sorted repo order
// when two repos pin the same bytes. Agreement code must not use it to decide
// which repo a document belongs to.
func DocumentBySHA256(sha string) (RepoDocument, bool) {
	if sha == "" {
		return RepoDocument{}, false
	}
	for _, repo := range slices.Sorted(maps.Keys(documentRegistry)) {
		for _, d := range documentRegistry[repo] {
			if d.SHA256 == sha {
				return RepoDocument{Repo: repo, CatalogDocument: d}, true
			}
		}
	}
	return RepoDocument{}, false
}
