package main

import (
	"bytes"
	"fmt"
	"go/format"
	"maps"
	"net/url"
	"os"
	"path"
	"regexp"
	"slices"
	"strings"
)

const (
	// documentsOutputFile is the generated document registry the classifier
	// package compiles in.
	documentsOutputFile = "model_catalog_documents_gen.go"

	documentKindLicense = "license"

	// revisionHexLen and sha256HexLen are the lengths of a git commit id and a
	// SHA-256 digest in lowercase hex.
	revisionHexLen = 40
	sha256HexLen   = 64

	externalDocumentScheme = "https://"
	// unsafeLocatorChars are characters a hosted path or an external URL never
	// contains: a backslash and whitespace.
	unsafeLocatorChars = "\\ \t\r\n"
)

// documentManifests lists every vendored manifest whose documents reach the
// registry: the three regional families and the document-only manifests of the
// other gallery entries. The document-only ones live apart because the catalog
// consistency test compares every manifest in gen/manifests with the catalog's
// models, which these do not describe.
var documentManifests = []string{
	"gen/manifests/BirdNET-v3.0-Models.models.json",
	"gen/manifests/Perch-v2-Models.models.json",
	"gen/manifests/BirdNET-Plus-V3.0.models.json",
	"gen/manifests/documents/BirdNET-Geomodel.models.json",
	"gen/manifests/documents/BirdNET-v2.4.models.json",
	"gen/manifests/documents/BattyBirdNET-onnx.models.json",
	"gen/manifests/documents/BSG-BAT.models.json",
}

var (
	revisionPattern = regexp.MustCompile("^[0-9a-f]{" + fmt.Sprint(revisionHexLen) + "}$")
	sha256Pattern   = regexp.MustCompile("^[0-9a-f]{" + fmt.Sprint(sha256HexLen) + "}$")
)

// manifestFamilyMeta mirrors the family_meta fields the generator reads.
type manifestFamilyMeta struct {
	Documents []manifestDocument `json:"documents"`
	// CurrentVersion is the release the manifest offers; a multi-release family
	// requires it to be its newest listed release.
	CurrentVersion string                        `json:"current_version"`
	Versions       map[string]manifestVersionDoc `json:"versions"`
}

// manifestVersionDoc is a release's optional document override. Documents is a
// pointer so a release without the key is told apart from one with an empty list.
type manifestVersionDoc struct {
	Documents *[]manifestDocument `json:"documents"`
}

// manifestDocument mirrors one family_meta.documents item: either hosted in the
// model repo (path, revision, sha256) or external (url).
type manifestDocument struct {
	Kind              string `json:"kind"`
	RequiresAgreement bool   `json:"requires_agreement"`
	Path              string `json:"path"`
	Revision          string `json:"revision"`
	SHA256            string `json:"sha256"`
	URL               string `json:"url"`
}

// namedManifest pairs a parsed manifest with its file name for error messages.
type namedManifest struct {
	name string
	m    *manifest
}

// normalizeRepo is the repo comparison key: spaces and trailing slashes trimmed,
// lowercase. The classifier package applies the same rule when it looks a
// repo up.
func normalizeRepo(repo string) string {
	return strings.ToLower(strings.TrimRight(strings.TrimSpace(repo), "/"))
}

// buildDocuments validates every manifest's document list and returns it keyed
// by repo. It hard-fails on anything that would embed a gate that does not gate.
// An unknown kind is not an error: consumers show it generically.
func buildDocuments(manifests []namedManifest) (map[string][]manifestDocument, error) {
	out := make(map[string][]manifestDocument, len(manifests))
	seen := make(map[string]string, len(manifests))
	for _, nm := range manifests {
		repo := nm.m.Repo
		if strings.TrimSpace(repo) == "" {
			return nil, fmt.Errorf("%s: missing repo", nm.name)
		}
		key := normalizeRepo(repo)
		if prev, dup := seen[key]; dup {
			return nil, fmt.Errorf("%s: repo %q collides with %s (repos are compared case-insensitively)", nm.name, repo, prev)
		}
		seen[key] = nm.name

		docs := nm.m.FamilyMeta.Documents
		if err := validateDocuments(docs); err != nil {
			return nil, fmt.Errorf("%s: %w", nm.name, err)
		}
		for ver, v := range nm.m.FamilyMeta.Versions {
			if v.Documents != nil && !slices.Equal(*v.Documents, docs) {
				return nil, fmt.Errorf("%s: release %q lists documents that differ from the family list; per-release document resolution must be implemented before it can be generated", nm.name, ver)
			}
		}
		out[repo] = docs
	}
	return out, nil
}

// validateDocuments checks one family document list.
func validateDocuments(docs []manifestDocument) error {
	licenses := 0
	kinds := make(map[string]bool, len(docs))
	for i := range docs {
		d := &docs[i]
		if d.Kind == "" {
			return fmt.Errorf("document %d has no kind", i)
		}
		if kinds[d.Kind] {
			return fmt.Errorf("document kind %q is repeated", d.Kind)
		}
		kinds[d.Kind] = true
		if d.Kind == documentKindLicense {
			licenses++
		}
		if err := validateDocumentSource(d); err != nil {
			return fmt.Errorf("document %q: %w", d.Kind, err)
		}
	}
	if licenses != 1 {
		return fmt.Errorf("want exactly one %q document, got %d", documentKindLicense, licenses)
	}
	return nil
}

// validateDocumentSource checks that a document is either fully hosted or purely
// external, that a hosted path names a file inside the repo, and that an
// external URL is https with a host.
func validateDocumentSource(d *manifestDocument) error {
	if d.URL != "" {
		if !validExternalURL(d.URL) {
			return fmt.Errorf("external url %q is not https with a host", d.URL)
		}
		if d.RequiresAgreement {
			return fmt.Errorf("an external document cannot require agreement (its text cannot be verified)")
		}
		if d.Path != "" || d.Revision != "" || d.SHA256 != "" {
			return fmt.Errorf("an external document must not set path, revision or sha256")
		}
		return nil
	}
	if d.Path == "" || !revisionPattern.MatchString(d.Revision) || !sha256Pattern.MatchString(d.SHA256) {
		return fmt.Errorf("a hosted document needs a path, a %d-hex revision and a %d-hex sha256", revisionHexLen, sha256HexLen)
	}
	if !validHostedPath(d.Path) {
		return fmt.Errorf("path %q must name a file by a clean relative path that stays inside the repo", d.Path)
	}
	return nil
}

// validExternalURL reports whether u is an https URL with a host and no
// whitespace, so a link the gallery shows can be opened.
func validExternalURL(u string) bool {
	if !strings.HasPrefix(u, externalDocumentScheme) || strings.ContainsAny(u, unsafeLocatorChars) {
		return false
	}
	parsed, err := url.Parse(u)
	return err == nil && parsed.Hostname() != ""
}

// validHostedPath reports whether p names a file inside the repo: relative, in
// clean form (no "." or ".." segment, no trailing slash), and free of
// backslashes and whitespace, so the resolve URL built from it points at that file.
func validHostedPath(p string) bool {
	return !path.IsAbs(p) && p != "." && path.Clean(p) == p &&
		!slices.Contains(strings.Split(p, "/"), "..") && !strings.ContainsAny(p, unsafeLocatorChars)
}

// runDocuments reads every document manifest and writes the registry file.
func runDocuments() error {
	named := make([]namedManifest, 0, len(documentManifests))
	for _, f := range documentManifests {
		m, err := loadManifest(f)
		if err != nil {
			return err
		}
		named = append(named, namedManifest{name: path.Base(f), m: m})
	}
	byRepo, err := buildDocuments(named)
	if err != nil {
		return err
	}
	src, err := renderDocuments(byRepo)
	if err != nil {
		return err
	}
	if err := os.WriteFile(documentsOutputFile, src, 0o644); err != nil { //nolint:gosec // generated, non-secret source file
		return fmt.Errorf("write %s: %w", documentsOutputFile, err)
	}
	fmt.Printf("wrote %s (%d repos)\n", documentsOutputFile, len(byRepo))
	return nil
}

// renderDocuments renders the registry deterministically: repos sorted,
// documents sorted by kind, fixed field order, zero fields omitted.
func renderDocuments(byRepo map[string][]manifestDocument) ([]byte, error) {
	repos := slices.Sorted(maps.Keys(byRepo))

	var buf bytes.Buffer
	buf.WriteString(documentsFileHeader)
	for _, r := range repos {
		docs := slices.Clone(byRepo[r])
		slices.SortFunc(docs, func(a, b manifestDocument) int { return strings.Compare(a.Kind, b.Kind) })
		fmt.Fprintf(&buf, "\t%q: {\n", r)
		for i := range docs {
			d := &docs[i]
			fmt.Fprintf(&buf, "\t\t{Kind: %q", d.Kind)
			if d.RequiresAgreement {
				buf.WriteString(", RequiresAgreement: true")
			}
			if d.Path != "" {
				fmt.Fprintf(&buf, ", Path: %q", d.Path)
			}
			if d.Revision != "" {
				fmt.Fprintf(&buf, ", Revision: %q", d.Revision)
			}
			if d.SHA256 != "" {
				fmt.Fprintf(&buf, ", SHA256: %q", d.SHA256)
			}
			if d.URL != "" {
				fmt.Fprintf(&buf, ", URL: %q", d.URL)
			}
			buf.WriteString("},\n")
		}
		buf.WriteString("\t},\n")
	}
	buf.WriteString("}\n")
	formatted, err := format.Source(buf.Bytes())
	if err != nil {
		return nil, fmt.Errorf("gofmt generated documents source: %w", err)
	}
	return formatted, nil
}

const documentsFileHeader = `// Code generated by "go run ./gen" from gen/manifests/*.models.json and gen/manifests/documents/*.models.json; DO NOT EDIT.

package classifier

// catalogDocuments lists the licence and policy documents of each model repo, as
// pinned by the vendored acoustic-models manifests.
var catalogDocuments = map[string][]CatalogDocument{
`
