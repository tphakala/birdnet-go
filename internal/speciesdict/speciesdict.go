// Package speciesdict serves per-locale species name dictionaries (scientific name ->
// localized common name) to the dashboard. The dictionaries are generated at build time
// from the embedded OpenFauna dataset (see ./gen) and embedded here as precompressed,
// public, cacheable static assets.
//
// The browser fetches one dictionary per active UI locale and uses it to localize
// displayed species names and to reverse-resolve the search box, so per-visitor
// localization happens entirely client-side with no per-request backend cost. The
// backend only ever serves static bytes; it never decodes the dataset on a request
// path, which is what keeps this safe to expose publicly on RAM-limited hardware.
package speciesdict

import (
	"crypto/sha256"
	"embed"
	"encoding/hex"
	"io/fs"
	"maps"
	"slices"
	"strings"

	"github.com/tphakala/birdnet-go/internal/errors"
	"github.com/tphakala/birdnet-go/internal/openfauna"
)

//go:generate go run ./gen

// dictFS holds the generated, precompressed per-locale dictionaries. Serving from the
// embedded read-only segment means no new resident backend maps and no decode cost.
//
//go:embed data/*.json.gz
var dictFS embed.FS

const (
	dataDir = "data"
	fileExt = ".json.gz"
)

// ErrUnknownLocale is returned for any locale without an embedded dictionary so callers
// can map it to a 404 without leaking filesystem details.
var ErrUnknownLocale = errors.NewStd("unknown species dictionary locale")

// supported is the fixed set of locale codes with an embedded dictionary, computed once
// from the embedded files. Per-request validation checks this in-memory set, never the
// embedded filesystem, so untrusted input is rejected before it can reach embed.FS.
var supported = loadSupported()

func loadSupported() map[string]struct{} {
	set := make(map[string]struct{})
	entries, err := fs.ReadDir(dictFS, dataDir)
	if err != nil {
		// An embed read failure here means the package was built without generated
		// dictionaries, which is a build-time error surfaced by the embed directive.
		return set
	}
	for _, e := range entries {
		if code, ok := strings.CutSuffix(e.Name(), fileExt); ok {
			set[code] = struct{}{}
		}
	}
	return set
}

// Has reports whether an embedded dictionary exists for the given locale code.
func Has(locale string) bool {
	_, ok := supported[locale]
	return ok
}

// SupportedLocales returns the sorted locale codes that have an embedded dictionary.
func SupportedLocales() []string {
	out := slices.Collect(maps.Keys(supported))
	slices.Sort(out)
	return out
}

// Read returns the precompressed (gzip) JSON dictionary bytes for the given locale.
// It returns ErrUnknownLocale for any locale not in the embedded set, so untrusted
// input cannot reach the embedded filesystem with an arbitrary path.
func Read(locale string) ([]byte, error) {
	if !Has(locale) {
		return nil, ErrUnknownLocale
	}
	return dictFS.ReadFile(dataDir + "/" + locale + fileExt)
}

// version is a content hash of the embedded dictionaries, computed once. It changes
// whenever the produced bytes change, whether from a dataset update OR from a change to
// the generator or locale-mapping logic, so the content-addressed (immutable-cached)
// dictionary URL invalidates correctly on any of those, not only on a dataset version
// bump. Using openfauna.DataVersion() alone would strand immutable-cached clients on
// stale content after a generator/mapping change.
var version = computeVersion()

func computeVersion() string {
	h := sha256.New()
	// Seed with the dataset provenance so the version still tracks dataset updates even
	// if two datasets happened to produce identical embedded bytes.
	_, _ = h.Write([]byte(openfauna.DataVersion()))
	// fs.ReadDir returns entries sorted by name, so the hash is deterministic.
	entries, err := fs.ReadDir(dictFS, dataDir)
	if err == nil {
		for _, e := range entries {
			b, readErr := dictFS.ReadFile(dataDir + "/" + e.Name())
			if readErr != nil {
				continue
			}
			_, _ = h.Write(b)
		}
	}
	return hex.EncodeToString(h.Sum(nil))[:12]
}

// Version returns a stable content hash of the embedded dictionaries. Clients use it to
// build a content-addressed (cache-busting) dictionary URL, so any change to the served
// bytes changes the URL and invalidates immutable-cached responses.
func Version() string {
	return version
}
