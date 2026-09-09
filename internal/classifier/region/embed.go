package region

import (
	"crypto/sha256"
	"embed"
	"encoding/hex"
	"encoding/json"
	"io/fs"
	"regexp"
	"strings"
	"sync"

	"github.com/tphakala/birdnet-go/internal/errors"
)

// snapshotFS holds the build-time region snapshots, one regions.json per model
// family. Refresh them from the acoustic-models checkout with the Taskfile
// `sync-region-snapshots` task; see data/README.md. Adding a family is a
// drop-in: the loader keys tables by each file's own repo field, so no code
// change is needed.
//
//go:embed data/*.regions.json
var snapshotFS embed.FS

// snapshotDir is the directory inside snapshotFS holding the embedded snapshots.
const snapshotDir = "data"

// loadTables parses every embedded snapshot once. It is wrapped in
// sync.OnceValues so the first caller pays the parse cost and later callers get
// the cached result. A parse or validation failure of an embedded file is a
// build defect (the package tests exercise this), so it surfaces as an error
// rather than a panic; callers then fall back to the global model.
var loadTables = sync.OnceValues(func() (map[string]*Table, error) {
	entries, err := fs.ReadDir(snapshotFS, snapshotDir)
	if err != nil {
		return nil, errors.New(err).
			Component("classifier.region").
			Category(errors.CategoryValidation).
			Context("operation", "read_snapshot_dir").
			Build()
	}
	tables := make(map[string]*Table, len(entries))
	for _, e := range entries {
		if e.IsDir() {
			continue
		}
		name := snapshotDir + "/" + e.Name()
		data, rerr := snapshotFS.ReadFile(name)
		if rerr != nil {
			return nil, errors.New(rerr).
				Component("classifier.region").
				Category(errors.CategoryValidation).
				Context("operation", "read_snapshot").
				Context("file", name).
				Build()
		}
		var t Table
		if uerr := json.Unmarshal(data, &t); uerr != nil {
			return nil, errors.New(uerr).
				Component("classifier.region").
				Category(errors.CategoryValidation).
				Context("operation", "parse_snapshot").
				Context("file", name).
				Build()
		}
		if verr := t.validate(); verr != nil {
			return nil, verr
		}
		if _, dup := tables[t.Repo]; dup {
			return nil, errors.Newf("duplicate region snapshot for repo %q", t.Repo).
				Component("classifier.region").
				Category(errors.CategoryValidation).
				Context("operation", "load_snapshots").
				Build()
		}
		tables[t.Repo] = &t
	}
	if len(tables) == 0 {
		return nil, errors.Newf("no region snapshots embedded").
			Component("classifier.region").
			Category(errors.CategoryValidation).
			Context("operation", "load_snapshots").
			Build()
	}
	return tables, nil
})

// Tables returns every embedded region table keyed by its HuggingFace repo id.
// The returned map is shared and must not be mutated.
func Tables() (map[string]*Table, error) {
	return loadTables()
}

// TableForRepo returns the region table for a HuggingFace repo id. ok is false
// when no snapshot describes that repo (a family with no region table), in which
// case the caller falls back to the global model. An embedded-snapshot load
// failure also reports ok=false, so a corrupt snapshot degrades to global rather
// than propagating an error into the hot path.
func TableForRepo(repo string) (t *Table, ok bool) {
	tables, err := loadTables()
	if err != nil {
		return nil, false
	}
	t, ok = tables[repo]
	return t, ok
}

// mapsFS holds the build-time per-region coverage maps (SVG), one file per
// region slug (data/maps/<slug>.svg). The geometry is model-family-agnostic, so
// a tile's map is byte-identical across families and a single slug-keyed set
// serves every family. Refresh them from an acoustic-models checkout with the
// Taskfile `sync-region-snapshots` task; see data/README.md.
//
//go:embed data/maps/*.svg
var mapsFS embed.FS

// mapsDir is the directory inside mapsFS holding the embedded coverage maps.
const mapsDir = "data/maps"

// slugPattern is the allowed shape of a region slug, validated before an
// embedded-map lookup as defense in depth: embed.FS is already inert to path
// traversal, but this rejects anything that is not a well-formed slug (path
// separators, dots, uppercase) before the lookup runs.
var slugPattern = regexp.MustCompile(`^[a-z0-9-]+$`)

// coverageMap is one embedded map SVG paired with a strong ETag derived from a
// hash of its bytes, so the HTTP layer answers conditional requests without
// re-hashing per request.
type coverageMap struct {
	svg  []byte
	etag string
}

// loadCoverageMaps parses every embedded coverage map once, keyed by slug (the
// filename without its .svg suffix). Like loadTables it is wrapped in
// sync.OnceValues so the first caller pays the cost; a read failure of an
// embedded file is a build defect (the package tests exercise this) and
// surfaces as an error, so callers degrade to "no map" and the UI falls back to
// its text-only country list.
var loadCoverageMaps = sync.OnceValues(func() (map[string]coverageMap, error) {
	entries, err := fs.ReadDir(mapsFS, mapsDir)
	if err != nil {
		return nil, errors.New(err).
			Component("classifier.region").
			Category(errors.CategoryValidation).
			Context("operation", "read_maps_dir").
			Build()
	}
	maps := make(map[string]coverageMap, len(entries))
	for _, e := range entries {
		if e.IsDir() {
			continue
		}
		name := e.Name()
		data, rerr := mapsFS.ReadFile(mapsDir + "/" + name)
		if rerr != nil {
			return nil, errors.New(rerr).
				Component("classifier.region").
				Category(errors.CategoryValidation).
				Context("operation", "read_map").
				Context("file", name).
				Build()
		}
		sum := sha256.Sum256(data)
		// Strong ETag: quoted hex of the content hash. Stable for the life of the
		// binary, changing only when a rebuilt binary ships different bytes.
		etag := `"` + hex.EncodeToString(sum[:]) + `"`
		maps[strings.TrimSuffix(name, ".svg")] = coverageMap{svg: data, etag: etag}
	}
	// Unlike loadTables there is intentionally no len==0 guard: `//go:embed
	// data/maps/*.svg` is a compile error when the directory holds no SVG, and an
	// empty set would only make every slug 404 into the UI's text-only country
	// list, not a hard failure worth surfacing.
	return maps, nil
})

// CoverageMap returns the embedded coverage-map SVG for a region slug together
// with a strong ETag for conditional requests. ok is false for a malformed
// slug, an unknown slug, or an embedded-map load failure, in which case the
// caller serves a 404 and the UI falls back to its text-only country list. The
// returned bytes are shared and must not be mutated.
func CoverageMap(slug string) (svg []byte, etag string, ok bool) {
	if !slugPattern.MatchString(slug) {
		return nil, "", false
	}
	maps, err := loadCoverageMaps()
	if err != nil {
		return nil, "", false
	}
	m, found := maps[slug]
	if !found {
		return nil, "", false
	}
	return m.svg, m.etag, true
}
